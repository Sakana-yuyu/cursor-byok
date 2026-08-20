package modelcontext

import "testing"

func TestWindowTokensFromSpreadsheetRules(t *testing.T) {
	tests := map[string]int{
		"claude-opus-4-8":          1_000_000,
		"claude-sonnet-4.6-latest": 1_000_000,
		"gpt-5.6-luna":             272_000,  // Codex 实际上限（非理论 1M）
		"gpt-5.6-sol":              272_000,
		"gpt-5.6-terra":            272_000,
		"gpt-5.6":                  1_000_000, // 无后缀=理论最大值
		"gpt-5.5":                  400_000,
		"gpt-5.4":                  400_000,
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
		// Gemini 2.5 — 支持视觉和音频（官方 2M 上下文）
		{"gemini-2.5-pro", true, true, true, 2_000_000},
		// DeepSeek V4 — 不支持视觉
		{"deepseek-v4-flash", false, false, true, 1_000_000},
		{"deepseek-v4-pro", false, false, true, 1_000_000},
		// Kimi K2.6 — 不支持视觉
		{"kimi-k2.6", false, false, true, 256_000},
		// Kimi 3.0 / K3 — 多模态，支持视觉（回归：曾误判为纯文本导致图片被剥离）
		{"kimi-3.0", true, false, true, 256_000},
		{"kimi-k3", true, false, true, 256_000},
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

func TestLookup(t *testing.T) {
	covered := Lookup("claude-sonnet-4.6")
	if !covered.Covered {
		t.Fatalf("Lookup(claude-sonnet-4.6).Covered = false, want true")
	}
	if covered.NormalizedID != "claude-sonnet-4.6" {
		t.Errorf("Lookup().NormalizedID = %q, want %q", covered.NormalizedID, "claude-sonnet-4.6")
	}
	if covered.Capability == nil {
		t.Fatalf("Lookup(claude-sonnet-4.6).Capability = nil, want non-nil")
	}

	// 带前缀/大小写/下划线的模型名应标准化后命中
	withPrefix := Lookup("  Models/CLAUDE_SONNET-4.6 ")
	if !withPrefix.Covered {
		t.Errorf("Lookup(%q).Covered = false, want true", "  Models/CLAUDE_SONNET-4.6 ")
	}
	if withPrefix.NormalizedID != "claude-sonnet-4.6" {
		t.Errorf("Lookup().NormalizedID = %q, want %q", withPrefix.NormalizedID, "claude-sonnet-4.6")
	}

	uncovered := Lookup("brand-new-model-xyz")
	if uncovered.Covered {
		t.Errorf("Lookup(unknown).Covered = true, want false")
	}
	if uncovered.NormalizedID != "brand-new-model-xyz" {
		t.Errorf("Lookup(unknown).NormalizedID = %q, want %q", uncovered.NormalizedID, "brand-new-model-xyz")
	}
	if uncovered.Capability != nil {
		t.Errorf("Lookup(unknown).Capability = %v, want nil", uncovered.Capability)
	}

	// 空/空白输入：不覆盖且无标准化 ID
	empty := Lookup("   ")
	if empty.Covered || empty.NormalizedID != "" || empty.Capability != nil {
		t.Errorf("Lookup(blank) = %+v, want empty result", empty)
	}
}

func TestNormalizeModelID(t *testing.T) {
	cases := map[string]string{
		"  CLAUDE_SONNET-4.6 ": "claude-sonnet-4.6",
		"models/gpt-4o":        "gpt-4o",
		"provider/gemini-2.5-pro": "gemini-2.5-pro",
		"kimi-k3":              "kimi-k3",
		"":                     "",
	}
	for input, want := range cases {
		if got := NormalizeModelID(input); got != want {
			t.Errorf("NormalizeModelID(%q) = %q, want %q", input, got, want)
		}
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

func TestBuiltinPricingKimiCurrentPricing(t *testing.T) {
	tests := []struct {
		modelID    string
		wantInput  float64
		wantOutput float64
		wantCache  float64
	}{
		// 2026-08 官方现价（platform.kimi.ai）：K3 = $3/$15/$0.30，
		// K2.6 = $0.95/$4/$0.16，K2.7(code) = $0.95/$4/$0.19。
		{"kimi-k3", 3.0, 15.0, 0.3},
		{"kimi-3.0", 3.0, 15.0, 0.3},
		{"kimi-k2.6", 0.95, 4.0, 0.16},
		{"kimi-k2.7", 0.95, 4.0, 0.19},
	}
	for _, tt := range tests {
		c := Capabilities(tt.modelID)
		if c == nil || c.Pricing == nil {
			t.Errorf("Capabilities(%q) pricing = nil, want builtin pricing", tt.modelID)
			continue
		}
		if c.Pricing.Input == nil || *c.Pricing.Input != tt.wantInput {
			t.Errorf("Capabilities(%q) input = %#v, want %v", tt.modelID, c.Pricing.Input, tt.wantInput)
		}
		if c.Pricing.Output == nil || *c.Pricing.Output != tt.wantOutput {
			t.Errorf("Capabilities(%q) output = %#v, want %v", tt.modelID, c.Pricing.Output, tt.wantOutput)
		}
		if c.Pricing.CacheRead == nil || *c.Pricing.CacheRead != tt.wantCache {
			t.Errorf("Capabilities(%q) cacheRead = %#v, want %v", tt.modelID, c.Pricing.CacheRead, tt.wantCache)
		}
		if c.Pricing.Currency != "USD" {
			t.Errorf("Capabilities(%q) currency = %q, want USD", tt.modelID, c.Pricing.Currency)
		}
	}
}
