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
