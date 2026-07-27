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

func TestCapabilitiesKnownModels(t *testing.T) {
	tests := []struct {
		modelID          string
		wantVision       bool
		wantThinking     bool
		wantTools        bool
		wantContextMin   int
	}{
		// Claude — 支持视觉和思考
		{"claude-sonnet-4.6", true, true, true, 1_000_000},
		{"claude-opus-4.8-latest", true, true, true, 1_000_000},
		// GPT-4o — 支持视觉，不支持思考
		{"gpt-4o", true, false, true, 128_000},
		// GPT-5 — 支持思考
		{"gpt-5", true, true, true, 1_000_000},
		// Gemini 2.5 — 支持视觉和音频
		{"gemini-2.5-pro", true, true, true, 1_000_000},
		// DeepSeek V4 — 不支持视觉
		{"deepseek-v4-flash", false, false, true, 1_000_000},
		{"deepseek-v4-pro", false, false, true, 1_000_000},
		// Kimi K2.6 — 不支持视觉
		{"kimi-k2.6", false, false, true, 256_000},
		// GLM-5.2 — 支持视觉
		{"glm-5.2", true, true, true, 200_000},
		// MiMo — 支持思考
		{"mimo", false, true, true, 128_000},
		{"mimo-vl", true, true, true, 128_000},
		// MiniMax
		{"minimax", false, false, true, 256_000},
		{"minimax-vl", true, false, true, 256_000},
		// StepFun
		{"step-2", true, true, true, 512_000},
		{"step-1v", true, false, true, 128_000},
	}

	for _, tt := range tests {
		c := Capabilities(tt.modelID)
		if c == nil {
			t.Errorf("Capabilities(%q) returned nil, want non-nil", tt.modelID)
			continue
		}
		if c.SupportsVision != tt.wantVision {
			t.Errorf("Capabilities(%q).SupportsVision = %v, want %v", tt.modelID, c.SupportsVision, tt.wantVision)
		}
		if c.SupportsThinking != tt.wantThinking {
			t.Errorf("Capabilities(%q).SupportsThinking = %v, want %v", tt.modelID, c.SupportsThinking, tt.wantThinking)
		}
		if c.SupportsTools != tt.wantTools {
			t.Errorf("Capabilities(%q).SupportsTools = %v, want %v", tt.modelID, c.SupportsTools, tt.wantTools)
		}
		if c.ContextWindowTokens < tt.wantContextMin {
			t.Errorf("Capabilities(%q).ContextWindowTokens = %d, want >= %d", tt.modelID, c.ContextWindowTokens, tt.wantContextMin)
		}
	}
}

func TestCapabilitiesUnknownModel(t *testing.T) {
	if got := Capabilities("unknown-model-xyz"); got != nil {
		t.Errorf("Capabilities(unknown) = %v, want nil", got)
	}
}

func TestSupportsVisionKnownModels(t *testing.T) {
	mustTrue := []string{"claude-sonnet-4.6", "gpt-4o", "gemini-2.5-pro", "grok-4.5"}
	for _, id := range mustTrue {
		got := SupportsVision(id)
		if got == nil || !*got {
			t.Errorf("SupportsVision(%q) = %v, want true", id, got)
		}
	}

	mustFalse := []string{"deepseek-v4-flash", "kimi-k2.6", "deepseek-v4-pro", "moonshot-v1"}
	for _, id := range mustFalse {
		got := SupportsVision(id)
		if got == nil || *got {
			t.Errorf("SupportsVision(%q) = %v, want false", id, got)
		}
	}

	// 未知模型返回 nil（保守策略）
	if got := SupportsVision("totally-unknown-model"); got != nil {
		t.Errorf("SupportsVision(unknown) = %v, want nil", got)
	}
}
