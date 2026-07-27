package modeladapter

import (
	"strings"
)

// ProviderCompatibility is the conservative compatibility policy for an
// OpenAI-compatible provider. It is intentionally request-shape agnostic so
// Chat Completions and Responses can share the same provider decisions.
type ProviderCompatibility struct {
	Kind                 string
	PromptCacheKey       bool
	ThinkingDisableKind  string
	StripPrivateFields   bool
	FilterResponsesTools bool
	DropResponsesFields  bool
	DropGrok45Sampling   bool
}

func classifyProviderCompatibility(baseURL, modelID string) ProviderCompatibility {
	base := strings.ToLower(strings.TrimSpace(baseURL))
	model := strings.ToLower(strings.TrimSpace(modelID))
	signal := base + " " + model
	policy := ProviderCompatibility{}
	switch {
	case strings.Contains(base, "api.openai.com") || strings.Contains(base, "chatgpt.com/backend-api/codex"):
		policy.PromptCacheKey = true
	case strings.Contains(signal, "githubcopilot") || strings.Contains(signal, "copilot"):
		policy.Kind = "copilot"
		policy.PromptCacheKey = false
		policy.StripPrivateFields = true
	case strings.Contains(signal, "deepseek"):
		policy.Kind = "deepseek"
		policy.StripPrivateFields = true
	case strings.Contains(signal, "api.x.ai") || strings.Contains(model, "grok"):
		policy.Kind = "xai"
		policy.StripPrivateFields = true
		policy.FilterResponsesTools = true
		policy.DropResponsesFields = true
		policy.DropGrok45Sampling = strings.Contains(model, "grok-4.5")
	case strings.Contains(signal, "kimi") || strings.Contains(signal, "moonshot"):
		policy.Kind = "kimi"
		policy.PromptCacheKey = strings.Contains(base, "api.kimi.com/coding")
	case strings.Contains(signal, "openrouter"):
		policy.Kind = "openrouter"
	case strings.Contains(signal, "siliconflow"):
		policy.Kind = "siliconflow"
	case strings.Contains(signal, "bigmodel") || strings.Contains(signal, "z.ai") || strings.Contains(signal, "zhipu") || strings.Contains(model, "glm"):
		policy.Kind = "zhipu"
	case strings.Contains(signal, "dashscope") || strings.Contains(signal, "qwen") || strings.Contains(signal, "aliyun") || strings.Contains(signal, "bailian"):
		policy.Kind = "qwen"
	case strings.Contains(signal, "xiaomimimo") || strings.Contains(signal, "mimo"):
		policy.Kind = "mimo"
	case strings.Contains(signal, "minimax"):
		policy.Kind = "minimax"
	case strings.Contains(signal, "stepfun") || strings.Contains(signal, "step-"):
		policy.Kind = "stepfun"
	}
	policy.ThinkingDisableKind = compatibilityThinkingDisableKind(policy.Kind, modelID)
	return policy
}

func compatibilityThinkingDisableKind(kind, modelID string) string {
	switch kind {
	case "kimi":
		if isKimiK27CodeModel(modelID) {
			return ""
		}
		return "thinking_type"
	case "openrouter":
		return "reasoning_object_none"
	case "siliconflow", "qwen":
		return "enable_thinking"
	case "deepseek", "zhipu", "mimo", "minimax":
		return "thinking_type"
	case "stepfun":
		if stepFunModelSupportsReasoningEffort(modelID) {
			return "reasoning_none"
		}
		return "thinking_type"
	}
	if openAIModelSupportsReasoningNone(strings.ToLower(strings.TrimSpace(modelID))) {
		return "reasoning_none"
	}
	return ""
}

func providerPromptCacheKeyAllowed(baseURL, modelID string) bool {
	return classifyProviderCompatibility(baseURL, modelID).PromptCacheKey
}

// sanitizeProviderPrivateFields removes provider-private underscore fields from
// request objects. JSON Schema property names are data, so names below
// "properties" are deliberately preserved.
func sanitizeProviderPrivateFields(value any) {
	sanitizeProviderPrivateFieldsAt(value, false)
}

func sanitizeProviderPrivateFieldsAt(value any, preserveMapKeys bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if !preserveMapKeys && strings.HasPrefix(key, "_") {
				delete(typed, key)
				continue
			}
			childPreservesKeys := preserveMapKeys || key == "properties" || key == "patternProperties"
			sanitizeProviderPrivateFieldsAt(child, childPreservesKeys)
		}
	case []any:
		for _, child := range typed {
			sanitizeProviderPrivateFieldsAt(child, preserveMapKeys)
		}
	}
}

func applyProviderCompatibilitySanitization(body map[string]any, baseURL, modelID string) {
	policy := classifyProviderCompatibility(baseURL, modelID)
	if policy.StripPrivateFields {
		sanitizeProviderPrivateFields(body)
	}
}
