package modelchannel

import "strings"

const (
	ProtocolModeAuto  = "auto"
	ProtocolModeFixed = "fixed"

	ProtocolGroupResponses             = OpenAIRequestGroupResponses
	ProtocolGroupChatCompletions       = OpenAIRequestGroupChatCompletions
	ProtocolGroupChatCompletionsCompat = OpenAIRequestGroupChatCompletionsCompat
	ProtocolGroupAnthropicMessages     = "messages"
	ProtocolGroupGeminiNative          = "gemini_native"
)

func NormalizeProtocolMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", ProtocolModeAuto:
		return ProtocolModeAuto
	case ProtocolModeFixed:
		return ProtocolModeFixed
	default:
		return ""
	}
}

func NormalizeProtocolGroup(providerType string, value string) string {
	provider := strings.ToLower(strings.TrimSpace(providerType))
	group := strings.ToLower(strings.TrimSpace(value))
	switch provider {
	case "openai":
		switch group {
		case ProtocolGroupResponses, ProtocolGroupChatCompletions, ProtocolGroupChatCompletionsCompat:
			return group
		}
	case "anthropic":
		if group == "" || group == ProtocolGroupAnthropicMessages {
			return ProtocolGroupAnthropicMessages
		}
	case "gemini":
		if group == "" || group == ProtocolGroupGeminiNative {
			return ProtocolGroupGeminiNative
		}
	}
	return ""
}

// ClassifyProtocolGroup 将模型归入 provider 支持的协议组。
// configuredGroup 是历史配置或自动探测结果，优先作为兼容提示；没有提示时才按模型与地址推断。
func ClassifyProtocolGroup(providerType string, modelID string, baseURL string, endpoint string, configuredGroup string) string {
	provider := strings.ToLower(strings.TrimSpace(providerType))
	if provider == "anthropic" {
		return ProtocolGroupAnthropicMessages
	}
	if provider == "gemini" {
		return ProtocolGroupGeminiNative
	}
	if provider != "openai" {
		return ""
	}
	if configured := NormalizeProtocolGroup(provider, configuredGroup); configured != "" {
		return configured
	}
	endpointLower := strings.ToLower(strings.TrimSpace(endpoint))
	baseLower := strings.ToLower(strings.TrimSpace(baseURL))
	model := strings.ToLower(strings.TrimSpace(modelID))
	if strings.HasSuffix(baseLower, "/responses") {
		return ProtocolGroupResponses
	}
	if strings.HasSuffix(baseLower, "/chat/completions") {
		return ProtocolGroupChatCompletions
	}
	if strings.Contains(baseLower, "api.openai.com") ||
		strings.HasPrefix(model, "gpt-") ||
		strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4") {
		return ProtocolGroupResponses
	}
	if model != "" {
		return ProtocolGroupChatCompletions
	}
	if strings.HasSuffix(endpointLower, "/responses") {
		return ProtocolGroupResponses
	}
	return ProtocolGroupChatCompletions
}

func ResolveProtocolGroup(mode string, providerType string, modelID string, baseURL string, endpoint string, configuredGroup string) string {
	normalizedMode := NormalizeProtocolMode(mode)
	if normalizedMode == "" {
		return ""
	}
	if normalizedMode == ProtocolModeFixed {
		return NormalizeProtocolGroup(providerType, configuredGroup)
	}
	return ClassifyProtocolGroup(providerType, modelID, baseURL, endpoint, configuredGroup)
}

func OpenAIEndpointForProtocolGroup(group string, currentEndpoint string) string {
	if strings.TrimSpace(currentEndpoint) == OpenAIEndpointCustom {
		return OpenAIEndpointCustom
	}
	switch NormalizeProtocolGroup("openai", group) {
	case ProtocolGroupResponses:
		return OpenAIEndpointResponses
	case ProtocolGroupChatCompletions, ProtocolGroupChatCompletionsCompat:
		return OpenAIEndpointChatCompletions
	default:
		return ""
	}
}

// InferProviderType 根据模型名推断最合适的 provider 协议族。
//
// 用于导入/拉取模型时，避免把本应走原生协议的模型（claude、gemini）错误地套用渠道级
// openai 协议——这正是 claude 缓存失效的根源。规则：
//   - claude-* → anthropic（前缀缓存依赖 cache_control 断点）
//   - gemini-* → gemini（原生协议）
//   - 其余     → fallback（通常为 openai，走 OpenAI 兼容协议）
//
// fallback 作为兜底：导入路径传入渠道当前 type，未识别的模型沿用它。
func InferProviderType(modelID string, fallback string) string {
	model := strings.ToLower(strings.TrimSpace(modelID))
	if strings.HasPrefix(model, "claude") {
		return "anthropic"
	}
	if strings.HasPrefix(model, "gemini") {
		return "gemini"
	}
	fallback = strings.ToLower(strings.TrimSpace(fallback))
	if fallback == "anthropic" || fallback == "gemini" || fallback == "openai" {
		return fallback
	}
	return "openai"
}

// InferProtocolGroupByModel 根据模型名推断协议组，绕过渠道级 type。
// 当用户在一个 openai 渠道里拉取到 claude/gemini 模型时，用模型名纠正协议组，
// 让请求走正确的原生协议（messages / gemini_native）而非 chat_completions。
// 返回 ("", false) 表示模型未命中原生协议族，应沿用渠道级 type 推断。
func InferProtocolGroupByModel(modelID string) (string, bool) {
	model := strings.ToLower(strings.TrimSpace(modelID))
	if strings.HasPrefix(model, "claude") {
		return ProtocolGroupAnthropicMessages, true
	}
	if strings.HasPrefix(model, "gemini") {
		return ProtocolGroupGeminiNative, true
	}
	return "", false
}
