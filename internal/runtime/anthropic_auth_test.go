package runtime

import "testing"

func TestRuntimeNormalizerKeepsDistinctAnthropicAuthModes(t *testing.T) {
	adapters, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{
		{DisplayName: "Claude", GroupName: "Primary", Type: "anthropic", BaseURL: "https://gateway.example/v1", APIKey: "token", TooltipData: "test", ModelID: "claude-test", AnthropicAuthMode: "bearer"},
		{DisplayName: "Claude", GroupName: "Primary", Type: "anthropic", BaseURL: "https://gateway.example/v1", APIKey: "token", TooltipData: "test", ModelID: "claude-test", AnthropicAuthMode: "x_api_key"},
	})
	if err != nil {
		t.Fatalf("NormalizeModelAdapterConfigs() error = %v", err)
	}
	if len(adapters) != 2 || adapters[0].ID == adapters[1].ID {
		t.Fatalf("auth-mode-distinct runtime adapters were collapsed: %#v", adapters)
	}
}
