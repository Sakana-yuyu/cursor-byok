package runtime

import "testing"

func TestNormalizeModelAdapterConfigsDeduplicatesChannelsAndSetsContext(t *testing.T) {
	first := ModelAdapterConfig{
		DisplayName:     "Claude Sonnet 4.6",
		Type:            "anthropic",
		BaseURL:         "https://api.example.com",
		APIKey:          "test-key",
		TooltipData:     "primary",
		ModelID:         "claude-sonnet-4-6",
		ReasoningEffort: "medium",
	}
	duplicate := first
	duplicate.DisplayName = "Claude Sonnet duplicate"
	duplicate.GroupName = "A 社供应商"

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
	if got[0].GroupName != "A 社供应商" {
		t.Errorf("GroupName = %q, want %q", got[0].GroupName, "A 社供应商")
	}
}

func TestNormalizeModelAdapterConfigsRejectsDuplicateIDAcrossProtocolVariants(t *testing.T) {
	base := ModelAdapterConfig{
		DisplayName:     "GPT-5.6 Luna",
		Type:            "openai",
		BaseURL:         "https://api.example.com/v1",
		APIKey:          "test-key",
		TooltipData:     "primary",
		ModelID:         "gpt-5.6-luna",
		ReasoningEffort: "medium",
		OpenAIEndpoint:  "/v1/responses",
		GroupName:       "OAI 供应商",
	}
	variant := base
	variant.ProtocolMode = "fixed"

	got, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{base, variant})
	if err == nil {
		t.Fatalf("NormalizeModelAdapterConfigs() error = nil, want duplicate-id rejection (got %d adapters)", len(got))
	}
}

func TestNormalizeModelAdapterConfigsDeduplicatesCursorAccountEntries(t *testing.T) {
	base := ModelAdapterConfig{
		Source:      ModelSourceCursorAccount,
		DisplayName: "Cursor Pro",
		GroupName:   "账户组",
		ModelID:     "gpt-test",
		TooltipData: "Cursor 账户模型",
	}
	duplicate := base

	got, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{base, duplicate})
	if err != nil {
		t.Fatalf("NormalizeModelAdapterConfigs() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("NormalizeModelAdapterConfigs() len = %d, want 1", len(got))
	}
}

func TestNormalizeModelAdapterConfigsKeepsSourcesIsolated(t *testing.T) {
	account := ModelAdapterConfig{
		Source:      ModelSourceCursorAccount,
		DisplayName: "Cursor Pro",
		ModelID:     "gpt-test",
		TooltipData: "Cursor 账户模型",
	}
	thirdParty := ModelAdapterConfig{
		Source:          ModelSourceThirdParty,
		DisplayName:     "Cursor Pro",
		Type:            "openai",
		BaseURL:         "https://api.example.com/v1",
		APIKey:          "test-key",
		TooltipData:     "primary",
		ModelID:         "gpt-test",
		ReasoningEffort: "medium",
		OpenAIEndpoint:  "/v1/responses",
	}

	got, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{account, thirdParty})
	if err != nil {
		t.Fatalf("NormalizeModelAdapterConfigs() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("NormalizeModelAdapterConfigs() len = %d, want 2", len(got))
	}
	if got[0].ID == got[1].ID {
		t.Fatalf("account and third-party adapters share ID %q, want distinct source-scoped IDs", got[0].ID)
	}
}
