package modeladapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrToolAdmission = errors.New("provider tool admission rejected")

// ToolAdmission is immutable after body construction. It maps the exact provider
// function names advertised for this request back to canonical tool names that
// the forwarder is allowed to dispatch.
type ToolAdmission struct {
	providerToSource   map[string]string
	hostedImage        bool
	admitted           int
	quarantined        int
	downgradedStrict   int
}

func newToolAdmission() *ToolAdmission {
	return &ToolAdmission{providerToSource: make(map[string]string)}
}

func (admission *ToolAdmission) ResolveFunction(providerName string) (string, bool) {
	if admission == nil {
		// Older direct adapter call sites without shaped tools retain legacy behavior.
		return strings.TrimSpace(providerName), strings.TrimSpace(providerName) != ""
	}
	name, ok := admission.providerToSource[strings.TrimSpace(providerName)]
	return name, ok
}

func (admission *ToolAdmission) AllowsHostedImage() bool {
	return admission != nil && admission.hostedImage
}

func (admission *ToolAdmission) diagnostics() map[string]any {
	if admission == nil {
		return nil
	}
	return map[string]any{
		"admitted_count":     admission.admitted,
		"quarantined_count":  admission.quarantined,
		"downgraded_strict":  admission.downgradedStrict,
		"hosted_image":       admission.hostedImage,
	}
}

func toolAdmissionError(reason string) error {
	return fmt.Errorf("%w: %s", ErrToolAdmission, reason)
}

// maxSchemaRequiredEntries 限制 required 数组的长度上限，防止恶意 MCP 提供的超大
// required 数组造成 CPU/内存放大（maxDepth/maxNodes 只约束递归节点，不约束单节点内的线性迭代）。
const maxSchemaRequiredEntries = 1024

func validateToolSchemaStructure(value any) error {
	const maxDepth = 32
	const maxNodes = 2048
	nodes := 0
	var walk func(any, int) error
	walk = func(raw any, depth int) error {
		nodes++
		if depth > maxDepth || nodes > maxNodes {
			return toolAdmissionError("schema exceeds structural limits")
		}
		switch typed := raw.(type) {
		case map[string]any:
			if properties, exists := typed["properties"]; exists {
				propertyMap, ok := properties.(map[string]any)
				if !ok {
					return toolAdmissionError("schema properties must be an object")
				}
				for _, child := range propertyMap {
					if err := walk(child, depth+1); err != nil {
						return err
					}
				}
			}
			if required, exists := typed["required"]; exists && required != nil {
				items, ok := required.([]any)
				if !ok {
					return toolAdmissionError("schema required must be an array")
				}
				if len(items) > maxSchemaRequiredEntries {
					return toolAdmissionError("schema required exceeds structural limits")
				}
				seen := make(map[string]struct{}, len(items))
				for _, item := range items {
					name, ok := item.(string)
					if !ok || strings.TrimSpace(name) == "" {
						return toolAdmissionError("schema required entries must be non-empty strings")
					}
					if _, duplicate := seen[name]; duplicate {
						return toolAdmissionError("schema required contains duplicate properties")
					}
					seen[name] = struct{}{}
				}
			}
			if items, exists := typed["items"]; exists {
				switch child := items.(type) {
				case map[string]any, []any:
					if err := walk(child, depth+1); err != nil {
						return err
					}
				case nil:
				default:
					return toolAdmissionError("schema items must be an object or array")
				}
			}
			for _, key := range []string{"allOf", "anyOf", "oneOf"} {
				if child, exists := typed[key]; exists {
					array, ok := child.([]any)
					if !ok {
						return toolAdmissionError("schema " + key + " must be an array")
					}
					for _, entry := range array {
						if err := walk(entry, depth+1); err != nil {
							return err
						}
					}
				}
			}
		case []any:
			for _, child := range typed {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		case nil:
			return toolAdmissionError("schema must be an object")
		default:
			return toolAdmissionError("schema must be an object")
		}
		return nil
	}
	return walk(value, 0)
}

// validateStrictOpenAISchema 校验 OpenAI strict 模式的封闭 schema 要求：
// 顶层 object、properties 存在、additionalProperties:false、每个属性都在 required 中
// （且数量一致）。这是 strict:true 特有的约束；违反时调用方降级为 non-strict 保留工具，
// 而不是丢弃（见 admitOpenAITools）。
func validateStrictOpenAISchema(parameters map[string]any) error {
	if strings.TrimSpace(fmt.Sprint(parameters["type"])) != "object" {
		return toolAdmissionError("strict schema requires an object type")
	}
	properties, ok := parameters["properties"].(map[string]any)
	if !ok || properties == nil {
		return toolAdmissionError("strict schema requires a properties object")
	}
	if additional, ok := parameters["additionalProperties"].(bool); !ok || additional {
		return toolAdmissionError("strict schema requires additionalProperties false")
	}
	required, ok := parameters["required"].([]any)
	if !ok || len(required) != len(properties) {
		return toolAdmissionError("strict schema requires every property")
	}
	seen := make(map[string]struct{}, len(required))
	for _, item := range required {
		name, ok := item.(string)
		if !ok || strings.TrimSpace(name) == "" {
			return toolAdmissionError("strict schema required entries must be strings")
		}
		seen[name] = struct{}{}
	}
	for name := range properties {
		if _, exists := seen[name]; !exists {
			return toolAdmissionError("strict schema required properties mismatch")
		}
	}
	return nil
}

func downgradeStrictTool(shape map[string]any, tool map[string]any) {
	delete(shape, "strict")
	delete(tool, "strict")
}

func admitOpenAITools(body map[string]any, responses bool, sourceTools []json.RawMessage) (*ToolAdmission, error) {
	admission := newToolAdmission()
	sourceByProviderName := make(map[string]string)
	for _, raw := range sourceTools {
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
		providerName := name
		if responses {
			providerName = sanitizeOpenAIResponsesToolName(name)
		}
		if _, exists := sourceByProviderName[providerName]; !exists {
			sourceByProviderName[providerName] = name
		}
	}

	rawTools, exists := body["tools"]
	if !exists {
		return admission, reconcileOpenAIToolChoice(body, admission)
	}
	items, ok := rawTools.([]any)
	if !ok {
		return nil, toolAdmissionError("provider tools must be an array")
	}
	filtered := make([]any, 0, len(items))
	for _, raw := range items {
		tool, ok := raw.(map[string]any)
		if !ok {
			admission.quarantined++
			continue
		}
		toolType := strings.TrimSpace(fmt.Sprint(tool["type"]))
		if toolType == "image_generation" {
			admission.hostedImage = true
			filtered = append(filtered, tool)
			continue
		}
		if toolType != "function" {
			admission.quarantined++
			continue
		}
		shape := tool
		if nested, ok := tool["function"].(map[string]any); ok {
			shape = nested
		}
		providerName := strings.TrimSpace(asStringMapValue(shape, "name"))
		if providerName == "" {
			admission.quarantined++
			continue
		}
		if _, duplicate := admission.providerToSource[providerName]; duplicate {
			admission.quarantined++
			continue
		}
		parameters, ok := shape["parameters"]
		if !ok || parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}
			shape["parameters"] = parameters
		}
		strict := false
		if rawStrict, exists := shape["strict"]; exists {
			value, ok := rawStrict.(bool)
			if !ok {
				admission.quarantined++
				continue
			}
			strict = value
		} else if rawStrict, exists := tool["strict"]; exists {
			value, ok := rawStrict.(bool)
			if !ok {
				admission.quarantined++
				continue
			}
			strict = value
		}
		if err := validateToolSchemaStructure(parameters); err != nil {
			admission.quarantined++
			continue
		}
		if strict {
			// strict:true 但 schema 不满足 OpenAI 封闭要求时，降级为 non-strict 保留工具：
			// 宽松的 OpenAI 兼容中转此前可用，直接丢弃会无声回归；真实 OpenAI 也会因
			// 非封闭 strict schema 拒绝请求，去掉 strict 后反而可用。
			parametersMap, ok := parameters.(map[string]any)
			if !ok || validateStrictOpenAISchema(parametersMap) != nil {
				downgradeStrictTool(shape, tool)
				admission.downgradedStrict++
			}
		}
		sourceName := sourceByProviderName[providerName]
		if sourceName == "" {
			sourceName = providerName
		}
		admission.providerToSource[providerName] = sourceName
		admission.admitted++
		filtered = append(filtered, tool)
	}
	if len(filtered) == 0 {
		delete(body, "tools")
		delete(body, "parallel_tool_calls")
	} else {
		body["tools"] = filtered
	}
	if err := reconcileOpenAIToolChoice(body, admission); err != nil {
		return nil, err
	}
	if len(filtered) == 0 {
		if choice, exists := body["tool_choice"]; exists && strings.TrimSpace(fmt.Sprint(choice)) != "none" {
			delete(body, "tool_choice")
		}
	}
	return admission, nil
}

func admitAnthropicTools(body map[string]any) (*ToolAdmission, error) {
	admission := newToolAdmission()
	rawTools, exists := body["tools"]
	if !exists {
		return admission, nil
	}
	items, ok := rawTools.([]any)
	if !ok {
		return nil, toolAdmissionError("anthropic tools must be an array")
	}
	filtered := make([]any, 0, len(items))
	for _, raw := range items {
		tool, ok := raw.(map[string]any)
		if !ok {
			admission.quarantined++
			continue
		}
		name := strings.TrimSpace(asStringMapValue(tool, "name"))
		schema := tool["input_schema"]
		if name == "" || schema == nil || validateToolSchemaStructure(schema) != nil {
			admission.quarantined++
			continue
		}
		if _, duplicate := admission.providerToSource[name]; duplicate {
			admission.quarantined++
			continue
		}
		admission.providerToSource[name] = name
		admission.admitted++
		filtered = append(filtered, tool)
	}
	if len(filtered) == 0 {
		delete(body, "tools")
	} else {
		body["tools"] = filtered
	}
	return admission, nil
}

func admitGeminiTools(body map[string]any) (*ToolAdmission, error) {
	admission := newToolAdmission()
	rawTools, exists := body["tools"]
	if !exists {
		return admission, nil
	}
	groups, ok := rawTools.([]any)
	if !ok {
		return nil, toolAdmissionError("gemini tools must be an array")
	}
	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		declarations, ok := group["functionDeclarations"].([]any)
		if !ok {
			continue
		}
		filtered := make([]any, 0, len(declarations))
		for _, rawDeclaration := range declarations {
			declaration, ok := rawDeclaration.(map[string]any)
			if !ok {
				admission.quarantined++
				continue
			}
			name := strings.TrimSpace(asStringMapValue(declaration, "name"))
			schema := declaration["parametersJsonSchema"]
			if name == "" || (schema != nil && validateToolSchemaStructure(schema) != nil) {
				admission.quarantined++
				continue
			}
			if _, duplicate := admission.providerToSource[name]; duplicate {
				admission.quarantined++
				continue
			}
			admission.providerToSource[name] = name
			admission.admitted++
			filtered = append(filtered, declaration)
		}
		if len(filtered) == 0 {
			delete(group, "functionDeclarations")
		} else {
			group["functionDeclarations"] = filtered
		}
	}
	return admission, nil
}

func reconcileOpenAIToolChoice(body map[string]any, admission *ToolAdmission) error {
	choice, exists := body["tool_choice"]
	if !exists || choice == nil {
		return nil
	}
	if text, ok := choice.(string); ok {
		switch strings.TrimSpace(text) {
		case "", "auto", "none":
			return nil
		case "required":
			if admission == nil || admission.admitted == 0 {
				return toolAdmissionError("required tool choice has no admitted tools")
			}
			return nil
		default:
			return toolAdmissionError("invalid tool choice")
		}
	}
	choiceMap, ok := choice.(map[string]any)
	if !ok || strings.TrimSpace(fmt.Sprint(choiceMap["type"])) != "function" {
		return toolAdmissionError("invalid named tool choice")
	}
	name := strings.TrimSpace(asStringMapValue(choiceMap, "name"))
	if function, ok := choiceMap["function"].(map[string]any); ok {
		name = strings.TrimSpace(asStringMapValue(function, "name"))
	}
	if name == "" {
		return toolAdmissionError("named tool choice is missing a name")
	}
	providerName := ""
	if admission != nil {
		for candidate, source := range admission.providerToSource {
			if source == name || candidate == name {
				providerName = candidate
				break
			}
		}
	}
	if providerName == "" {
		return toolAdmissionError("named tool choice is not admitted")
	}
	if function, ok := choiceMap["function"].(map[string]any); ok {
		function["name"] = providerName
	} else {
		choiceMap["name"] = providerName
	}
	return nil
}
