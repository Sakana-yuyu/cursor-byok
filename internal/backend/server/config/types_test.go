package config

import "testing"

func TestNormalizeModelAdapterConfigsDeduplicatesChannelsAndSetsContext(t *testing.T) {
	first := ModelAdapterConfig{
		DisplayName:     "GPT-5.6 Luna",
		Type:            "openai",
		BaseURL:         "https://api.example.com/v1",
		APIKey:          "test-key",
		TooltipData:     "primary",
		ModelID:         "gpt-5.6-luna",
		ReasoningEffort: "medium",
		OpenAIEndpoint:  "/v1/responses",
	}
	duplicate := first
	duplicate.DisplayName = "GPT-5.6 Luna duplicate"
	duplicate.GroupName = "OAI 供应商"

	got, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{first, duplicate})
	if err != nil {
		t.Fatalf("NormalizeModelAdapterConfigs() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("NormalizeModelAdapterConfigs() len = %d, want 1", len(got))
	}
	if got[0].ContextWindowTokens != 1_000_000 {
		t.Errorf("ContextWindowTokens = %d, want 1000000", got[0].ContextWindowTokens)
	}
	if got[0].GroupName != "OAI 供应商" {
		t.Errorf("GroupName = %q, want %q", got[0].GroupName, "OAI 供应商")
	}
}
