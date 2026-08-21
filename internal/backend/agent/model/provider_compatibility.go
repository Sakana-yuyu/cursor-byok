package modeladapter

import (
	"strings"

	"cursor/internal/modelchannel"
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
	// prompt_cache_key 默认对所有 provider 开启。绝大多数 OpenAI 兼容服务
	// （OpenAI 官方、xAI Grok、智谱 GLM、通义 Qwen、月之暗面 Kimi、DeepSeek 及各类第三方中转）
	// 要么原生支持 prompt_cache_key，要么会忽略未知字段，因此默认发送，让 provider 按
	// conversation 复用前缀缓存，显著提升缓存命中率。仅对已知会因未知字段报错的 provider 显式关闭。
	policy := ProviderCompatibility{PromptCacheKey: true}
	switch {
	case strings.Contains(base, "api.openai.com") || strings.Contains(base, "chatgpt.com/backend-api/codex"):
		// OpenAI 官方端点，继承默认 PromptCacheKey=true。
	case strings.Contains(signal, "githubcopilot") || strings.Contains(signal, "copilot"):
		policy.Kind = "copilot"
		// Copilot 会因未知字段报错，显式关闭。
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
		// Kimi 继承默认 PromptCacheKey=true；api.kimi.com/coding 端点原生支持。
	case strings.Contains(signal, "openrouter"):
		policy.Kind = "openrouter"
	case strings.Contains(signal, "siliconflow"):
		policy.Kind = "siliconflow"
	case strings.Contains(signal, "bigmodel") || strings.Contains(signal, "z.ai") || strings.Contains(signal, "zhipu") || (strings.Contains(model, "glm") && isZhipuOfficialBaseURL(base)):
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

// isOpenAIChannelClaudeModel 判断一个 type=openai 的渠道是否实际承载 claude 模型。
//
// 用于运行时把 claude 模型从 OpenAI 协议自动升级到 Anthropic 原生协议：claude 的前缀
// 缓存依赖 cache_control 断点（Anthropic /v1/messages 协议），而 OpenAI 的 prompt_cache_key
// 对 claude 无效。升级后走 AnthropicAdapter，请求发往 {baseURL}/messages 并带 cache_control 断点。
//
// 边界：modelID 为 meta alias（如 "auto"）的渠道不在请求时确定真实模型（由中转路由），
// 无法可靠判断是否是 claude，因此不升级——漏判是安全的（继续走 openai 协议不会出错）。
// 若希望这类渠道也走 anthropic 协议，应把 modelID 显式设为 claude-* 前缀。
func isOpenAIChannelClaudeModel(providerType, modelID string) bool {
	if strings.ToLower(strings.TrimSpace(providerType)) != "openai" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelID)), "claude")
}

// upgradeOpenAIClaudeToAnthropic 把 type=openai 且承载 claude 模型的渠道运行时升级到
// Anthropic 原生协议（修改 resolved 的 Provider/ProtocolGroup 等）。返回 true 表示发生了升级。
// protocolMode=fixed 表示用户明确锁定协议（逃生口），此时跳过升级。
// 升级失败（如中转商不支持 /v1/messages）时，router.streamChannel 会降级回 openai。
func upgradeOpenAIClaudeToAnthropic(resolved *StreamRequest) bool {
	if resolved == nil {
		return false
	}
	if !isOpenAIChannelClaudeModel(resolved.Provider, resolved.ProviderModelID) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(resolved.ProtocolMode), "fixed") {
		return false
	}
	resolved.Provider = "anthropic"
	resolved.ProtocolGroup = "messages"
	resolved.BaseURL = modelchannel.BaseURLWithoutKnownOpenAIEndpoint(resolved.BaseURL)
	if strings.TrimSpace(resolved.AnthropicThinkingEffort) == "" {
		resolved.AnthropicThinkingEffort = normalizeRuntimeThinkingEffort(resolved.ReasoningEffort)
	}
	// OpenAI 专属字段不再适用，避免残留导致误解。
	resolved.OpenAIEndpoint = ""
	resolved.OpenAIRequestGroup = ""
	return true
}

// isZhipuOfficialBaseURL 判断 baseURL 是否指向智谱官方端点。
// 仅官方端点确认支持 thinking 字段；第三方中转站（如 daoxe.com）即使转发 glm 模型
// 也不应注入 thinking 字段，否则上游会返回 400 "Invalid request for the selected model"。
func isZhipuOfficialBaseURL(base string) bool {
	switch {
	case strings.Contains(base, "bigmodel.cn"),
		strings.Contains(base, "bigmodel.com"),
		strings.Contains(base, "z.ai"),
		strings.Contains(base, "zhipuai.ai"),
		strings.Contains(base, "open.bigmodel"),
		strings.Contains(base, "api.z.ai"):
		return true
	}
	return false
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
