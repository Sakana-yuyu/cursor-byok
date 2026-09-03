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

func TestResolveModelAdapterChannelUsesProviderSafeDefaultOutputBudgets(t *testing.T) {
	tests := []struct {
		name                string
		adapter             ModelAdapterConfig
		wantMaxTokens       int
		wantAnthropicTokens int
	}{
		{
			name: "openai",
			adapter: ModelAdapterConfig{
				Type:    "openai",
				BaseURL: "https://example.com/v1",
				ModelID: "model-openai",
			},
			wantMaxTokens:       4096,
			wantAnthropicTokens: 65536,
		},
		{
			name: "gemini",
			adapter: ModelAdapterConfig{
				Type:    "gemini",
				BaseURL: "https://example.com/v1beta",
				ModelID: "model-gemini",
			},
			wantMaxTokens:       4096,
			wantAnthropicTokens: 65536,
		},
		{
			name: "anthropic",
			adapter: ModelAdapterConfig{
				Type:    "anthropic",
				BaseURL: "https://example.com",
				ModelID: "model-anthropic",
			},
			wantMaxTokens:       4096,
			wantAnthropicTokens: 65536,
		},
		{
			name: "explicit values",
			adapter: ModelAdapterConfig{
				Type:                "openai",
				BaseURL:             "https://example.com/v1",
				ModelID:             "model-explicit",
				MaxCompletionTokens: 8192,
				AnthropicMaxTokens:  1234,
			},
			wantMaxTokens:       8192,
			wantAnthropicTokens: 1234,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := resolveModelAdapterChannel([]ModelAdapterConfig{tt.adapter}, tt.adapter.ModelID)
			if err != nil {
				t.Fatalf("resolveModelAdapterChannel() error = %v", err)
			}
			if resolved.MaxTokens != tt.wantMaxTokens {
				t.Fatalf("MaxTokens = %d, want %d", resolved.MaxTokens, tt.wantMaxTokens)
			}
			if resolved.AnthropicMaxTokens != tt.wantAnthropicTokens {
				t.Fatalf("AnthropicMaxTokens = %d, want %d", resolved.AnthropicMaxTokens, tt.wantAnthropicTokens)
			}
		})
	}
}
