// xml_tool_protocol.go 实现 in-band XML 工具协议的请求侧：为无原生工具调用
// 能力的模型（toolCallMode=xml_prompt）把工具目录渲染为系统提示追加段，并把
// 历史中的结构化工具调用/结果序列化为文本形态，保证多轮请求逐字节一致可重放。
//
// 协议格式（与 xml_tool_call_scanner.go 的响应侧严格互逆）：
//   - 调用块：<tool_call>{"name":"...","arguments":{...}}</tool_call>
//     arguments 为原始 JSON 对象，不做 XML 转义（定界符匹配语义，非 XML DOM）。
//   - 结果块：<tool_result name="TOOL">content</tool_result>；由系统注入历史，
//     模型永远不应自行输出（响应侧会剥离伪造结果块）。
//   - 多块规则：一次回复可连续输出多个 <tool_call> 块，顺序即调用顺序。
//
// 确定性：目录按工具名称排序、schema 经既有 normalizer 清洗后用 json.Marshal
// 序列化（map 键序确定），同会话同工具集 → 目录段逐字节相同，随系统提示进入
// 稳定前缀；历史调用的 XML 序列化对 arguments 做 json.Compact 规范化，重放稳定。
//
// 扩展点：目前仅 OpenAI chat completions 路径接入（弱模型/本地模型几乎都是
// OpenAI 兼容）。responses API 与 anthropic/gemini 适配器如需支持，应在各自的
// body 构造函数中复用 xmlToolProtocolMessages / xmlToolCatalogPrompt，并在各自
// 流式文本出口接入 xml_tool_call_scanner.go 的扫描器。
package modeladapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	// ToolCallModeNative 表示使用 provider 原生 tool_calls 协议（默认）。
	ToolCallModeNative = "native"
	// ToolCallModeXMLPrompt 表示使用 in-band XML 工具协议（提示注入 + 文本扫描）。
	ToolCallModeXMLPrompt = "xml_prompt"

	xmlToolCallOpenTag    = "<tool_call>"
	xmlToolCallCloseTag   = "</tool_call>"
	xmlToolResultOpenTag  = "<tool_result"
	xmlToolResultCloseTag = "</tool_result>"
)

// xmlToolCallPromptMode 判断本次请求是否启用 in-band XML 工具协议。
func xmlToolCallPromptMode(req StreamRequest) bool {
	return strings.TrimSpace(req.ToolCallMode) == ToolCallModeXMLPrompt
}

// xmlToolProtocolMessages 把统一消息列表转换为 XML 协议的纯文本形态：
//   - assistant 的结构化 ToolCalls 渲染为 <tool_call> 文本块（保留 reasoning 回放）；
//   - role=tool 的结果消息合并转换为 user 消息中的 <tool_result> 块；
//   - 其余消息原样保留。
//
// 输入来自持久化历史（router 侧已做 sanitize/merge），同一会话每次重放的输入
// 一致，因此输出逐字节一致，满足 prefix-cache-stability。
func xmlToolProtocolMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, 0, len(messages))
	for _, message := range messages {
		switch {
		case message.Role == "tool":
			out = appendXMLToolResultMessage(out, message)
		case message.Role == "assistant" && len(message.ToolCalls) > 0:
			rendered := cloneProviderMessage(message)
			rendered.ToolCalls = nil
			rendered.Content = joinXMLTextContent(rendered.Content, xmlRenderAssistantToolCalls(message.ToolCalls))
			out = append(out, rendered)
		default:
			out = append(out, message)
		}
	}
	return out
}

// appendXMLToolResultMessage 把一条 tool 结果消息追加到 out：
// 相邻多条 tool 结果合并在同一个 user 消息内（每条一个 <tool_result> 块，
// 换行分隔），减少消息数并保证确定性分组。
//
// ContentParts（如工具返回的图片）原样保留：下游 openAIContentValue 在存在
// 图片 part 时走 parts 分支并忽略 message.Content，因此这里把非空 Content
// （即累积的 <tool_result> 文本块）作为首个 text part 注入，确保带图结果
// 不丢失全部文本；无图片时仍走纯文本路径，Content 单独生效，不会重复。
func appendXMLToolResultMessage(out []Message, message Message) []Message {
	name := strings.TrimSpace(message.Name)
	escaped := xmlEscapeToolResultContent(message.Content)
	block := "<tool_result name=\"" + name + "\">" + escaped + xmlToolResultCloseTag
	// 相邻合并：上一条消息是本轮转换产生的 tool_result 载体时直接追加块。
	if last := len(out) - 1; last >= 0 && out[last].xmlToolResultCarrier {
		prev := out[last].Content
		out[last].Content = joinXMLTextContent(prev, block)
		out[last].ContentParts = append(out[last].ContentParts, message.ContentParts...)
		xmlSyncCarrierTextPart(&out[last], prev)
		return out
	}
	carrier := Message{
		Role:                 "user",
		Content:              block,
		ContentParts:         append([]ContentPart(nil), message.ContentParts...),
		xmlToolResultCarrier: true,
	}
	xmlSyncCarrierTextPart(&carrier, "")
	return append(out, carrier)
}

// xmlEscapeToolResultContent 转义结果文本中的 <tool_result 定界符前缀
// （大小写不敏感），改写为 "<\/tool_result"（JSON 风格转义斜杠约定）：
// 改写后的形态不再匹配字面 "</tool_result"，既无法提前闭合结果块，也不会被
// 响应侧的伪造结果剥离逻辑误伤——模型侧约定为「<\/ 即字面斜杠」。
// 确定性：纯字节级替换，同输入必得同输出。
func xmlEscapeToolResultContent(content string) string {
	marker := "</tool_result"
	if !strings.Contains(strings.ToLower(content), marker) {
		return content
	}
	lower := strings.ToLower(content)
	var b strings.Builder
	b.Grow(len(content))
	for i := 0; i < len(content); {
		if strings.HasPrefix(lower[i:], marker) {
			b.WriteString("<\\/tool_result")
			i += len(marker)
			continue
		}
		b.WriteByte(content[i])
		i++
	}
	return b.String()
}

// xmlSyncCarrierTextPart 保证带图载体的 <tool_result> 文本块在下游 parts
// 分支可见：openAIContentValue 存在图片 part 时忽略 message.Content，因此
// 把非空 Content 作为首个 text part 注入。injected 为上一轮注入的文本快照：
// 若当前头部文本 part 恰为该快照（即此前注入的副本），就地更新为最新全文，
// 保证相邻合并多次追加时不产生重复副本；无图片或 Content 为空时不做任何
// 修改，纯文本路径逐字节不变。
func xmlSyncCarrierTextPart(carrier *Message, injected string) {
	if !hasImageContentParts(carrier.ContentParts) || strings.TrimSpace(carrier.Content) == "" {
		return
	}
	if len(carrier.ContentParts) > 0 && carrier.ContentParts[0].Type == contentPartTypeText && carrier.ContentParts[0].Text == injected {
		carrier.ContentParts[0].Text = carrier.Content
		return
	}
	text := ContentPart{Type: contentPartTypeText, Text: carrier.Content}
	carrier.ContentParts = append([]ContentPart{text}, carrier.ContentParts...)
}

// xmlRenderAssistantToolCalls 把 assistant 结构化工具调用渲染为连续的
// <tool_call> 文本块（每个块后换行），与扫描器接受的输入严格互逆。
func xmlRenderAssistantToolCalls(calls []ToolCallDescriptor) string {
	var b strings.Builder
	for _, call := range calls {
		name := strings.TrimSpace(call.Function.Name)
		b.WriteString(xmlToolCallOpenTag)
		b.WriteString(`{"name":`)
		b.WriteString(xmlJSONString(name))
		b.WriteString(`,"arguments":`)
		b.WriteString(xmlCompactJSONArgs(call.Function.Arguments))
		b.WriteString("}")
		b.WriteString(xmlToolCallCloseTag)
		b.WriteString("\n")
	}
	return b.String()
}

// xmlCompactJSONArgs 把历史参数 JSON 规范化为紧凑形态（去空白），保证逐字节
// 稳定可重放。参数在流式收口时已验证为 JSON 对象；此处防御性兜底：解析失败时
// 按普通 JSON 字符串编码，绝不因历史畸形数据 panic 或改变其余消息。
func xmlCompactJSONArgs(args string) string {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return "{}"
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, []byte(trimmed)); err == nil && compacted.Len() > 0 {
		return compacted.String()
	}
	return xmlJSONString(trimmed)
}

// xmlJSONString 用 encoding/json 编码一个字符串值（含转义），保证确定性。
func xmlJSONString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}
// xmlToolNameAdmission 从原始工具描述构建「provider 名 → 规范名」映射，供
// 响应侧扫描器解析模型输出的工具名并拒绝未声明工具。xml_prompt 模式下请求体
// 不携带原生 tools，req.ToolAdmission 为空映射，故直接从 req.Tools 构建。
func xmlToolNameAdmission(tools []json.RawMessage) map[string]string {
	if len(tools) == 0 {
		return nil
	}
	names := make(map[string]string, len(tools))
	for _, raw := range tools {
		var descriptor map[string]any
		if json.Unmarshal(raw, &descriptor) != nil {
			continue
		}
		source := descriptor
		if nested, ok := descriptor["function"].(map[string]any); ok {
			source = nested
		}
		name := strings.TrimSpace(asStringMapValue(source, "name"))
		if name == "" {
			continue
		}
		if _, exists := names[name]; !exists {
			names[name] = name
		}
	}
	return names
}

// xmlToolCatalogPrompt 渲染工具目录与协议指令段。tools 为原始工具描述 JSON；
// 输出确定性：工具经 normalizeOpenAIChatTools 清洗去重后按名称排序，每个工具
// 序列化为单行紧凑 JSON（map 键序由 encoding/json 保证）。无可用工具时返回空串。
func xmlToolCatalogPrompt(tools []json.RawMessage) string {
	if len(tools) == 0 {
		return ""
	}
	normalized, err := normalizeOpenAIChatTools(tools)
	if err != nil || len(normalized) == 0 {
		return ""
	}
	type catalogEntry struct {
		name string
		line string
	}
	entries := make([]catalogEntry, 0, len(normalized))
	for _, raw := range normalized {
		var descriptor map[string]any
		if err := json.Unmarshal(raw, &descriptor); err != nil {
			continue
		}
		source := descriptor
		if nested, ok := descriptor["function"].(map[string]any); ok {
			source = nested
		}
		name := strings.TrimSpace(asStringMapValue(source, "name"))
		if name == "" {
			continue
		}
		line := map[string]any{"name": name}
		if description := strings.TrimSpace(asStringMapValue(source, "description")); description != "" {
			line["description"] = description
		}
		if parameters, ok := source["parameters"]; ok && parameters != nil {
			line["parameters"] = parameters
		} else {
			line["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		encoded, err := json.Marshal(line)
		if err != nil {
			continue
		}
		entries = append(entries, catalogEntry{name: name, line: string(encoded)})
	}
	if len(entries) == 0 {
		return ""
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	var b strings.Builder
	b.WriteString(xmlToolProtocolInstructionsHeader)
	b.WriteString("\n<tools>\n")
	for _, entry := range entries {
		b.WriteString(entry.line)
		b.WriteString("\n")
	}
	b.WriteString("</tools>")
	return b.String()
}

// xmlToolProtocolInstructionsHeader 是协议指令部分（置于目录之前）。
const xmlToolProtocolInstructionsHeader = `# Tool Use

You can call tools by emitting tool call blocks in your reply text.

Format:
<tool_call>{"name":"TOOL_NAME","arguments":{"PARAM":"VALUE"}}</tool_call>

Rules:
- "arguments" MUST be a raw JSON object matching the tool's parameters schema. Never wrap it in a string and never XML-escape its content.
- You may emit multiple consecutive <tool_call> blocks in one reply to make multiple calls; they execute in order.
- After tool calls are executed, results arrive as <tool_result name="TOOL">...</tool_result> blocks in the next user message.
- Never emit <tool_result> blocks yourself; results are provided by the system only.
- When you do not need tools, reply with plain text only.

Available tools:`

// appendXMLToolCatalogToMessages 把目录段追加进 provider messages：
// 追加到首条 system 消息末尾（系统提示属于稳定前缀）；没有 system 消息时
// 在最前面插入一条新的 system 消息。目录内容确定性派生，不破坏前缀稳定性。
func appendXMLToolCatalogToMessages(items []map[string]any, catalog string) []map[string]any {
	if catalog == "" {
		return items
	}
	for _, item := range items {
		if strings.TrimSpace(fmt.Sprint(item["role"])) != "system" {
			continue
		}
		// provider messages 的 content 实际只会是 string 或 []map[string]any
		// （JSON 反序列化产物）；[]any 分支仅为兼容手工构造的等价形态保留。
		switch content := item["content"].(type) {
		case string:
			item["content"] = joinXMLPromptText(content, catalog)
		case []map[string]any:
			item["content"] = append(content, map[string]any{"type": "text", "text": catalog})
		case []any:
			item["content"] = append(content, map[string]any{"type": "text", "text": catalog})
		default:
			// 兜底只覆盖 nil/未知形态：不再用 fmt.Sprint 拼接（nil 会产生
			// "<nil>" 垃圾串），直接以目录文本替换，保证系统消息可用且确定。
			item["content"] = catalog
		}
		return items
	}
	head := map[string]any{"role": "system", "content": catalog}
	return append([]map[string]any{head}, items...)
}

// joinXMLTextContent 以空行拼接两段文本，任一段为空时返回另一段。
func joinXMLTextContent(left, right string) string {
	switch {
	case strings.TrimSpace(left) == "":
		return right
	case strings.TrimSpace(right) == "":
		return left
	default:
		return left + "\n" + right
	}
}

// joinXMLPromptText 与 joinXMLTextContent 相同语义（系统提示追加用独立命名，
// 便于阅读调用点语义）。
func joinXMLPromptText(left, right string) string {
	return joinXMLTextContent(left, right)
}
