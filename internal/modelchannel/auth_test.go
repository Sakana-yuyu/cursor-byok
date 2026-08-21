package modelchannel

import (
	"net/http"
	"testing"
)

func TestNormalizeAnthropicAuthMode(t *testing.T) {
	tests := map[string]string{
		"":            AnthropicAuthModeLegacyDual,
		" AUTO ":      AnthropicAuthModeAuto,
		"x_api_key":   AnthropicAuthModeAPIKey,
		"BEARER":      AnthropicAuthModeBearer,
		"legacy_dual": AnthropicAuthModeLegacyDual,
		"invalid":     "",
	}
	for input, want := range tests {
		if got := NormalizeAnthropicAuthMode(input); got != want {
			t.Fatalf("NormalizeAnthropicAuthMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAnthropicGeneratedAuthHeaders(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		mode       string
		wantAPIKey string
		wantBearer string
	}{
		{name: "legacy dual", url: "https://gateway.example/v1/messages", mode: AnthropicAuthModeLegacyDual, wantAPIKey: "token", wantBearer: "Bearer token"},
		{name: "auto official", url: "https://api.anthropic.com/v1/messages", mode: AnthropicAuthModeAuto, wantAPIKey: "token"},
		{name: "auto proxy", url: "https://gateway.example/v1/messages", mode: AnthropicAuthModeAuto, wantAPIKey: "token", wantBearer: "Bearer token"},
		{name: "auto deceptive host", url: "https://api.anthropic.com.proxy.example/v1/messages", mode: AnthropicAuthModeAuto, wantAPIKey: "token", wantBearer: "Bearer token"},
		{name: "api key only", url: "https://gateway.example/v1/messages", mode: AnthropicAuthModeAPIKey, wantAPIKey: "token"},
		{name: "bearer only", url: "https://gateway.example/v1/messages", mode: AnthropicAuthModeBearer, wantBearer: "Bearer token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := AnthropicGeneratedAuthHeaders(tt.url, tt.mode, "Bearer token")
			if got := headers.Get("x-api-key"); got != tt.wantAPIKey {
				t.Fatalf("x-api-key = %q, want %q", got, tt.wantAPIKey)
			}
			if got := headers.Get("Authorization"); got != tt.wantBearer {
				t.Fatalf("Authorization = %q, want %q", got, tt.wantBearer)
			}
		})
	}
}

func TestHasExplicitAnthropicAuthHeaderUsesHeaderPresence(t *testing.T) {
	for _, headers := range []http.Header{
		{"Authorization": {""}},
		{"X-API-KEY": {""}},
	} {
		if !HasExplicitAnthropicAuthHeader(headers) {
			t.Fatalf("expected explicit auth header in %#v", headers)
		}
	}
	if HasExplicitAnthropicAuthHeader(http.Header{"X-Custom": {"value"}}) {
		t.Fatal("non-auth custom header must not suppress generated auth")
	}
}
