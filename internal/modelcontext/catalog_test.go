package modelcontext

import "testing"

func TestWindowTokensFromSpreadsheetRules(t *testing.T) {
	tests := map[string]int{
		"claude-opus-4-8":          1_000_000,
		"claude-sonnet-4.6-latest": 1_000_000,
		"gpt-5.6-luna":             1_000_000,
		"gpt-4o":                   128_000,
		"grok-4.5":                 500_000,
		"grok-4.20-fast":           1_000_000,
		"Qwen/Qwen3.8-Max-Preview": 1_000_000,
		"deepseek-v4-pro":          1_000_000,
		"kimi-k2.6":                256_000,
		"glm-5.2":                  200_000,
	}
	for modelID, want := range tests {
		if got := WindowTokens(modelID); got != want {
			t.Errorf("WindowTokens(%q) = %d, want %d", modelID, got, want)
		}
	}
}

func TestResolveKeepsExplicitValue(t *testing.T) {
	if got := Resolve("gpt-4o", 42_000); got != 42_000 {
		t.Fatalf("Resolve() = %d, want 42000", got)
	}
	if got := Resolve("unknown-model", 0); got != 0 {
		t.Fatalf("Resolve() unknown = %d, want 0", got)
	}
}
