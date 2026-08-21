package modelchannel

import (
	"net/http"
	"strings"
)

const (
	AnthropicAuthModeLegacyDual = "legacy_dual"
	AnthropicAuthModeAuto       = "auto"
	AnthropicAuthModeAPIKey     = "x_api_key"
	AnthropicAuthModeBearer     = "bearer"
)

// NormalizeAnthropicAuthMode keeps legacy configurations compatible: an omitted
// value means the historic dual-header behavior. Invalid non-empty values return
// an empty string so configuration validation can reject them explicitly.
func NormalizeAnthropicAuthMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return AnthropicAuthModeLegacyDual
	case AnthropicAuthModeLegacyDual, AnthropicAuthModeAuto, AnthropicAuthModeAPIKey, AnthropicAuthModeBearer:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func AnthropicAuthToken(value string) string {
	token := strings.TrimSpace(value)
	if len(token) >= len("Bearer ") && strings.EqualFold(token[:len("Bearer ")], "Bearer ") {
		return strings.TrimSpace(token[len("Bearer "):])
	}
	return token
}

// AnthropicGeneratedAuthHeaders returns only generated credentials. Explicit
// user-provided auth headers must be applied separately and take final precedence.
func AnthropicGeneratedAuthHeaders(finalURL, mode, apiKey string) http.Header {
	token := AnthropicAuthToken(apiKey)
	headers := make(http.Header)
	if token == "" {
		return headers
	}
	resolvedMode := NormalizeAnthropicAuthMode(mode)
	if resolvedMode == "" {
		return headers
	}
	if resolvedMode == AnthropicAuthModeAuto {
		if URLHostForProtocol(finalURL) == "api.anthropic.com" {
			resolvedMode = AnthropicAuthModeAPIKey
		} else {
			resolvedMode = AnthropicAuthModeLegacyDual
		}
	}
	switch resolvedMode {
	case AnthropicAuthModeAPIKey:
		headers.Set("x-api-key", token)
	case AnthropicAuthModeBearer:
		headers.Set("Authorization", "Bearer "+token)
	case AnthropicAuthModeLegacyDual:
		headers.Set("x-api-key", token)
		headers.Set("Authorization", "Bearer "+token)
	}
	return headers
}

func HasExplicitAnthropicAuthHeader(headers http.Header) bool {
	for name := range headers {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "authorization", "x-api-key":
			return true
		}
	}
	return false
}
