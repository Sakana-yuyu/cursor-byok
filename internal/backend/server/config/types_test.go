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
	first.GroupName = "OAI 供应商"
	duplicate.GroupName = first.GroupName

	got, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{first, duplicate})
	if err != nil {
		t.Fatalf("NormalizeModelAdapterConfigs() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("NormalizeModelAdapterConfigs() len = %d, want 1", len(got))
	}
	if got[0].ContextWindowTokens != 272_000 {
		t.Errorf("ContextWindowTokens = %d, want 272000", got[0].ContextWindowTokens)
	}
	if got[0].GroupName != "OAI 供应商" {
		t.Errorf("GroupName = %q, want %q", got[0].GroupName, "OAI 供应商")
	}
}

func TestNormalizeModelAdapterConfigsKeepsSameChannelInDifferentGroups(t *testing.T) {
	base := ModelAdapterConfig{
		DisplayName:     "GPT-5.6 Luna",
		Type:            "openai",
		BaseURL:         "https://api.example.com/v1",
		APIKey:          "test-key",
		TooltipData:     "primary",
		ModelID:         "gpt-5.6-luna",
		ReasoningEffort: "medium",
		OpenAIEndpoint:  "/v1/responses",
	}
	first := base
	first.GroupName = "供应商 A"
	second := base
	second.GroupName = "供应商 B"

	got, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{first, second})
	if err != nil {
		t.Fatalf("NormalizeModelAdapterConfigs() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("NormalizeModelAdapterConfigs() len = %d, want 2", len(got))
	}
	if got[0].GroupName != "供应商 A" || got[1].GroupName != "供应商 B" {
		t.Errorf("group names = [%q, %q], want [供应商 A, 供应商 B]", got[0].GroupName, got[1].GroupName)
	}
}
