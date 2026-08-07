package config

import "testing"

// TestResolveModelAdapterChannelDefaultsToAdaptiveThinking 验证未显式配置
// thinkingBudgetTokens 时 resolver 不得烘焙正数默认预算 —— 否则 Anthropic 适配层
// 可能误走 legacy budget_tokens 形态，output_config.effort 被静默丢弃。
func TestResolveModelAdapterChannelDefaultsToAdaptiveThinking(t *testing.T) {
	adapters := []ModelAdapterConfig{{
		DisplayName: "claude",
		Type:        "anthropic",
		BaseURL:     "https://example.com",
		APIKey:      "sk-test",
		ModelID:     "claude-fable-5",
	}}
	resolved, err := resolveModelAdapterChannel(adapters, "claude-fable-5")
	if err != nil {
		t.Fatalf("resolveModelAdapterChannel() error = %v", err)
	}
	if resolved.ThinkingBudgetTokens != 0 {
		t.Fatalf("ThinkingBudgetTokens = %d, want 0 (adaptive thinking by default)", resolved.ThinkingBudgetTokens)
	}
	if resolved.AnthropicThinkingEffort != "xhigh" {
		t.Fatalf("AnthropicThinkingEffort = %q, want default %q", resolved.AnthropicThinkingEffort, "xhigh")
	}
	if !resolved.ThinkingEnabled {
		t.Fatal("ThinkingEnabled = false, want true")
	}
}

func TestResolveModelAdapterChannelKeepsExplicitThinkingOverrides(t *testing.T) {
	adapters := []ModelAdapterConfig{{
		DisplayName:             "claude",
		Type:                    "anthropic",
		BaseURL:                 "https://example.com",
		APIKey:                  "sk-test",
		ModelID:                 "claude-fable-5",
		ThinkingBudgetTokens:    8000,
		AnthropicThinkingEffort: "medium",
	}}
	resolved, err := resolveModelAdapterChannel(adapters, "claude-fable-5")
	if err != nil {
		t.Fatalf("resolveModelAdapterChannel() error = %v", err)
	}
	if resolved.ThinkingBudgetTokens != 8000 {
		t.Fatalf("ThinkingBudgetTokens = %d, want explicit 8000", resolved.ThinkingBudgetTokens)
	}
	if resolved.AnthropicThinkingEffort != "medium" {
		t.Fatalf("AnthropicThinkingEffort = %q, want explicit %q", resolved.AnthropicThinkingEffort, "medium")
	}
}
