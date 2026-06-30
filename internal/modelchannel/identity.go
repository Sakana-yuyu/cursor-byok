package modelchannel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

const ChannelIDHexLength = 16

const (
	OpenAIEndpointResponses       = "/v1/responses"
	OpenAIEndpointChatCompletions = "/v1/chat/completions"
)

func NormalizeBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("模型适配器 baseURL 不能为空")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("模型适配器 baseURL 不是合法 URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("模型适配器 baseURL 仅支持 http 或 https")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("模型适配器 baseURL 缺少主机名")
	}
	parsed.Scheme = strings.ToLower(strings.TrimSpace(parsed.Scheme))
	parsed.Host = strings.ToLower(strings.TrimSpace(parsed.Host))
	normalized := strings.TrimRight(parsed.String(), "/")
	if normalized == "" {
		normalized = parsed.String()
	}
	return normalized, nil
}

func NormalizeOpenAIEndpoint(providerType string, endpoint string) string {
	if strings.TrimSpace(strings.ToLower(providerType)) != "openai" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case "", OpenAIEndpointResponses:
		return OpenAIEndpointResponses
	case OpenAIEndpointChatCompletions:
		return OpenAIEndpointChatCompletions
	default:
		return ""
	}
}

func BuildLegacyChannelID(baseURL string, modelID string, apiKey string, name string) string {
	return buildChannelID([]string{
		strings.TrimSpace(baseURL),
		strings.TrimSpace(modelID),
		strings.TrimSpace(apiKey),
		strings.TrimSpace(name),
	})
}

func BuildChannelID(baseURL string, modelID string, apiKey string, name string, openAIEndpoint string) string {
	endpoint := strings.TrimSpace(openAIEndpoint)
	if endpoint == "" {
		return BuildLegacyChannelID(baseURL, modelID, apiKey, name)
	}
	return buildChannelID([]string{
		strings.TrimSpace(baseURL),
		strings.TrimSpace(modelID),
		strings.TrimSpace(apiKey),
		strings.TrimSpace(name),
		endpoint,
	})
}

func buildChannelID(parts []string) string {
	payload := strings.Join(parts, "\n")
	hashBytes := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(hashBytes[:])[:ChannelIDHexLength]
}
