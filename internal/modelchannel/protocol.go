package modelchannel

import "strings"

const (
	ProtocolModeAuto  = "auto"
	ProtocolModeFixed = "fixed"

	ProtocolGroupResponses             = OpenAIRequestGroupResponses
	ProtocolGroupChatCompletions       = OpenAIRequestGroupChatCompletions
	ProtocolGroupChatCompletionsCompat = OpenAIRequestGroupChatCompletionsCompat
	ProtocolGroupAnthropicMessages     = "messages"
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
