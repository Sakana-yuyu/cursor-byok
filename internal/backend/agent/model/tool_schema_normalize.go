// tool_schema_normalize.go 承载请求发出前的输入侧工具参数 Schema 清洗：
// 按供应商兼容策略把工具描述符里的 JSON Schema 收敛为当前 provider 稳定接受的形态。
// 与 forwarder 的 tool_schema_recovery（provider 400 点名工具后的隔离重试）互补——
// 本文件在请求发出前剥掉已知会触发严格校验 400 的关键字，把「失败后救」升级为「事前防」；
// 清洗后仍被上游点名的工具继续走既有隔离重试路径，行为保持不变。
//
// 确定性保证（prefix-cache 约束）：清洗是 (schema 内容, kind, strict) 的纯函数——
// 不读全局状态、不依赖时间与随机数。Go map 遍历顺序不影响输出：各键的改写互相独立，
// description 追加按关键字字典序排列，required 排序后写入，JSON 编码本身对 map 键排序，
// 因此同一 adapter 配置下相同输入必然产出逐字节相同的请求体。
package modeladapter

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// 结构防护上限：与 validateToolSchemaStructure 保持一致，超限立即放弃清洗（fail-open），
// 防止恶意 MCP 提供的超深/超大 schema 造成清洗阶段的 CPU/栈放大。
const (
	schemaNormalizeMaxDepth = 32
	schemaNormalizeMaxNodes = 2048
)

// schemaConstraintNotePrefix 追加进 description 的约束说明前缀，
// 让下游与排障时能区分「原始描述」与「清洗时保留下来的语义」。
const schemaConstraintNotePrefix = "[schema-constraint]"

// schemaStripKeywordsBase 全供应商共用的剥离集合：纯注解/元数据关键字，任何 provider
// 都不据此做校验，但部分严格网关会因未知字段直接 400。语义以约束说明并入 description。
var schemaStripKeywordsBase = []string{"$schema", "$id", "$comment", "examples", "default"}

// schemaStripKeywordsRelay 第三方兼容渠道追加剥离的关键字：中转背后的非 OpenAI 后端
// （Kimi/Qwen/GLM 等）普遍不支持 format/pattern 断言，语义以约束说明并入 description
// 后模型仍可遵守。
var schemaStripKeywordsRelay = []string{"format", "pattern"}

// toolSchemaPolicy 描述一个供应商对工具参数 JSON Schema 的接受能力。
// kind 复用 ProviderCompatibility.Kind：空表示默认 OpenAI 兼容策略，非空表示已知
// 第三方兼容渠道（按宽松后端处理）。
type toolSchemaPolicy struct {
	kind           string
	strictPipeline bool
	// stripKeywords 需要剥离的关键字集合；语义以约束说明并入 description。
	stripKeywords map[string]struct{}
	// nullableField 可空标记字段名：Gemini 原生用 "nullable"，其余协议为空
	//（可空语义仅记入 description）。
	nullableField string
}

// newToolSchemaPolicy 按 ProviderCompatibility.Kind 构造清洗策略。
// 所有非空 kind 均为已知第三方兼容渠道，统一追加 format/pattern 剥离。
func newToolSchemaPolicy(kind string) toolSchemaPolicy {
	policy := toolSchemaPolicy{kind: kind, stripKeywords: make(map[string]struct{}, 8)}
	for _, key := range schemaStripKeywordsBase {
		policy.stripKeywords[key] = struct{}{}
	}
	if kind != "" {
		for _, key := range schemaStripKeywordsRelay {
			policy.stripKeywords[key] = struct{}{}
		}
	}
	return policy
}

// newGeminiToolSchemaPolicy 构造 Gemini 原生协议的清洗策略：Gemini function
// declarations 不支持 format/pattern，且原生以 nullable 布尔表达可空联合。
func newGeminiToolSchemaPolicy() toolSchemaPolicy {
	policy := newToolSchemaPolicy("gemini")
	policy.nullableField = "nullable"
	return policy
}

// normalizeToolSchemaForProvider 按策略递归清洗一个工具参数 JSON Schema 节点。
// 纯函数、就地修改并返回同一节点；全程 fail-open：任何解析/结构异常只放弃清洗、
// 原样保留，绝不因清洗失败阻断请求。
func normalizeToolSchemaForProvider(policy toolSchemaPolicy, schema any) any {
	nodes := 0
	walkToolSchemaNode(policy, schema, 0, &nodes)
	return schema
}

// walkToolSchemaNode 递归遍历 schema 节点并依次执行：
// nullable 联合折叠 → strict 封闭化 → 关键字剥离 → 子节点递归。
func walkToolSchemaNode(policy toolSchemaPolicy, value any, depth int, nodes *int) {
	if depth > schemaNormalizeMaxDepth || *nodes > schemaNormalizeMaxNodes {
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		*nodes++
		collapseToolSchemaNullableType(policy, typed)
		if policy.strictPipeline {
			closeStrictObjectSchema(typed)
		}
		stripToolSchemaKeywordsWithNote(policy, typed)
		for _, key := range []string{"properties", "additionalProperties", "items", "prefixItems", "allOf", "anyOf", "oneOf", "not", "$defs", "definitions"} {
			child, exists := typed[key]
			if !exists {
				continue
			}
			if key == "properties" {
				// properties 容器本身不是 schema 节点：只递归各属性值，
				// 避免把与保留关键字同名的属性名误当关键字剥离。
				// 键按字典序排序后再递归：nodes 预算超限时截断的是确定
				// 的前缀集合，保证超限 schema 的清洗结果逐字节确定。
				propertyMap, ok := child.(map[string]any)
				if !ok {
					continue
				}
				propertyNames := make([]string, 0, len(propertyMap))
				for propertyName := range propertyMap {
					propertyNames = append(propertyNames, propertyName)
				}
				sort.Strings(propertyNames)
				for _, propertyName := range propertyNames {
					walkToolSchemaNode(policy, propertyMap[propertyName], depth+1, nodes)
				}
				continue
			}
			walkToolSchemaNode(policy, child, depth+1, nodes)
		}
	case []any:
		*nodes++
		for _, child := range typed {
			walkToolSchemaNode(policy, child, depth+1, nodes)
		}
	}
}

// collapseToolSchemaNullableType 折叠可空类型联合：type:["string","null"] →
// type:"string" + 可空标记。部分严格后端不支持 type 数组，直接 400。
// 多个非 null 类型的联合保守不动（无法无损折叠）；可空语义始终以约束说明记入
// description，不丢信息。
func collapseToolSchemaNullableType(policy toolSchemaPolicy, node map[string]any) {
	rawTypes, ok := node["type"].([]any)
	if !ok {
		return
	}
	nonNull := make([]string, 0, len(rawTypes))
	hasNull := false
	for _, item := range rawTypes {
		name, ok := item.(string)
		if !ok {
			return
		}
		if name == "null" {
			hasNull = true
			continue
		}
		nonNull = append(nonNull, name)
	}
	if !hasNull || len(nonNull) != 1 {
		return
	}
	encoded, err := json.Marshal(rawTypes)
	if err != nil {
		return
	}
	note := "nullable: 原 type 联合 " + string(encoded) + " 折叠为 " + fmt.Sprint(nonNull[0]) + "，该参数可为 null"
	node["type"] = nonNull[0]
	if policy.nullableField != "" {
		node[policy.nullableField] = true
	}
	appendSchemaConstraintNotes(node, []string{note})
}

// closeStrictObjectSchema 实施 OpenAI strict 模式的封闭对象要求：
// additionalProperties:false、required 收录全部属性、非必填属性包装
// anyOf[原 schema, {type:null}]（官方推荐的「可选字段」表达方式）。
func closeStrictObjectSchema(node map[string]any) {
	if strings.TrimSpace(fmt.Sprint(node["type"])) != "object" {
		return
	}
	properties, ok := node["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return
	}
	node["additionalProperties"] = false
	originalRequired := make(map[string]struct{})
	if required, ok := node["required"].([]any); ok {
		for _, item := range required {
			if name, ok := item.(string); ok {
				originalRequired[name] = struct{}{}
			}
		}
	}
	// 属性名排序后写入 required，保证序列化字节稳定。
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	required := make([]any, 0, len(names))
	for _, name := range names {
		propertyMap, ok := properties[name].(map[string]any)
		if !ok {
			// 非对象属性无法包装 anyOf，仍纳入 required（strict 要求全字段必填）。
			required = append(required, name)
			continue
		}
		required = append(required, name)
		if _, wasRequired := originalRequired[name]; wasRequired {
			continue
		}
		properties[name] = map[string]any{
			"anyOf": []any{propertyMap, map[string]any{"type": "null"}},
		}
	}
	node["required"] = required
}

// stripToolSchemaKeywordsWithNote 剥离策略命中的关键字，并把被剥关键字的原始值
// 以约束说明形式追加进 description（不丢信息）。关键字按字典序处理保证确定性。
// format:"uri" 特例：默认策略也剥离（沿用既有行为，见 normalizeOpenAIToolSchemaRequired）。
func stripToolSchemaKeywordsWithNote(policy toolSchemaPolicy, node map[string]any) {
	stripped := make([]string, 0, 4)
	for key := range node {
		if _, hit := policy.stripKeywords[key]; hit {
			stripped = append(stripped, key)
		} else if key == "format" && strings.TrimSpace(fmt.Sprint(node[key])) == "uri" {
			stripped = append(stripped, key)
		}
	}
	if len(stripped) == 0 {
		return
	}
	sort.Strings(stripped)
	notes := make([]string, 0, len(stripped))
	for _, key := range stripped {
		encoded, err := json.Marshal(node[key])
		if err != nil {
			encoded = []byte(`"<unencodable>"`)
		}
		notes = append(notes, key+": "+string(encoded))
		delete(node, key)
	}
	appendSchemaConstraintNotes(node, notes)
}

// appendSchemaConstraintNotes 把约束说明追加进节点 description。仅在 description
// 缺失或为字符串时修改，避免覆盖非字符串的异常 description 造成信息丢失。
func appendSchemaConstraintNotes(node map[string]any, notes []string) {
	if len(notes) == 0 {
		return
	}
	existing, ok := node["description"].(string)
	if ok && strings.TrimSpace(existing) != "" {
		existing = strings.TrimRight(existing, "\n") + "\n"
	} else {
		existing = ""
	}
	node["description"] = existing + schemaConstraintNotePrefix + " " + strings.Join(notes, "; ")
}

// normalizeOpenAIToolSchemasForProvider 清洗 OpenAI 请求体内全部 function 工具的
// parameters schema。兼容 Chat（{"type":"function","function":{...}}）与 Responses
// （{"type":"function","name":...,"parameters":...}）两种形态；strict 标志按
// admitOpenAITools 的同一读取顺序（function 内层优先，回落 tool 根）判定。
func normalizeOpenAIToolSchemasForProvider(body map[string]any, baseURL string, modelID string) {
	if len(body) == 0 {
		return
	}
	items, ok := body["tools"].([]any)
	if !ok {
		return
	}
	basePolicy := newToolSchemaPolicy(classifyProviderCompatibility(baseURL, modelID).Kind)
	for _, item := range items {
		tool, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(fmt.Sprint(tool["type"])) != "function" {
			continue
		}
		shape := tool
		if nested, ok := tool["function"].(map[string]any); ok {
			shape = nested
		}
		parameters, exists := shape["parameters"]
		if !exists || parameters == nil {
			continue
		}
		toolPolicy := basePolicy
		toolPolicy.strictPipeline = openAIToolStrictFlag(tool, shape)
		shape["parameters"] = normalizeToolSchemaForProvider(toolPolicy, parameters)
	}
}

// openAIToolStrictFlag 读取工具的 strict 开关：内层 function 形态优先，回落外层。
func openAIToolStrictFlag(tool map[string]any, shape map[string]any) bool {
	if strict, ok := shape["strict"].(bool); ok {
		return strict
	}
	if strict, ok := tool["strict"].(bool); ok {
		return strict
	}
	return false
}

// normalizeAnthropicToolSchemasForProvider 清洗 Anthropic 请求体内工具的 input_schema。
// Anthropic 原生协议对 JSON Schema 支持完整，仅剥离基础注解关键字。
func normalizeAnthropicToolSchemasForProvider(body map[string]any) {
	if len(body) == 0 {
		return
	}
	items, ok := body["tools"].([]any)
	if !ok {
		return
	}
	policy := newToolSchemaPolicy("")
	for _, item := range items {
		tool, ok := item.(map[string]any)
		if !ok {
			continue
		}
		schema, exists := tool["input_schema"]
		if !exists || schema == nil {
			continue
		}
		tool["input_schema"] = normalizeToolSchemaForProvider(policy, schema)
	}
}

// normalizeGeminiToolSchemasForProvider 清洗 Gemini 请求体内 functionDeclarations
// 的 parametersJsonSchema：剥离 format/pattern 并以 nullable 布尔表达可空联合。
func normalizeGeminiToolSchemasForProvider(body map[string]any) {
	if len(body) == 0 {
		return
	}
	groups, ok := body["tools"].([]any)
	if !ok {
		return
	}
	policy := newGeminiToolSchemaPolicy()
	for _, group := range groups {
		groupMap, ok := group.(map[string]any)
		if !ok {
			continue
		}
		declarations, ok := groupMap["functionDeclarations"].([]any)
		if !ok {
			continue
		}
		for _, item := range declarations {
			declaration, ok := item.(map[string]any)
			if !ok {
				continue
			}
			schema, exists := declaration["parametersJsonSchema"]
			if !exists || schema == nil {
				continue
			}
			declaration["parametersJsonSchema"] = normalizeToolSchemaForProvider(policy, schema)
		}
	}
}
