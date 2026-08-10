package modeladapter

import (
	"context"
	"testing"
	"time"

	legacyruntime "cursor/internal/runtime"
)

type capturingModelAdapter struct {
	request StreamRequest
}

func (adapter *capturingModelAdapter) Stream(_ context.Context, req StreamRequest, _ func(ModelEvent) error) error {
	adapter.request = req
	return nil
}

type reasoningLimitResolver struct {
	channel *legacyruntime.ResolvedChannel
}

func (resolver *reasoningLimitResolver) SelectChannelForModel(context.Context, string) (*legacyruntime.ResolvedChannel, error) {
	return resolver.channel, nil
}

func (*reasoningLimitResolver) ProviderStreamIdleTimeout(context.Context) time.Duration { return 0 }
func (*reasoningLimitResolver) TurnStaleTimeout(context.Context) time.Duration          { return 0 }
func (*reasoningLimitResolver) NativeDelegationProgressTimeout(context.Context) time.Duration {
	return 0
}

func TestRouterCapsRuntimeThinkingEffortByChannelMaximum(t *testing.T) {
	adapter := &capturingModelAdapter{}
	router := &Router{
		openai:    adapter,
		anthropic: adapter,
		gemini:    adapter,
		resolver: &reasoningLimitResolver{channel: &legacyruntime.ResolvedChannel{
			ID:              "channel",
			Name:            "test",
			Provider:        "openai",
			BaseURL:         "https://example.com",
			APIKey:          "test-key",
			Model:           "deepseek-v4-flash",
			ReasoningEffort: "medium",
		}},
		healthByChannel: make(map[string]channelHealth),
	}

	err := router.Stream(context.Background(), StreamRequest{
		RequestID:      "request",
		ModelCallID:    "call",
		ModelID:        "deepseek-v4-flash",
		ThinkingEffort: "max",
		RequestKnobs:   map[string]any{},
	}, func(ModelEvent) error { return nil })
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	captured := adapter.request
	if captured.ReasoningEffort != "medium" || captured.ThinkingEffort != "medium" {
		t.Fatalf("effective effort = thinking:%q reasoning:%q", captured.ThinkingEffort, captured.ReasoningEffort)
	}
	if captured.RequestKnobs["runtime_thinking_effort"] != "max" ||
		captured.RequestKnobs["configured_thinking_effort_maximum"] != "medium" ||
		captured.RequestKnobs["effective_thinking_effort"] != "medium" {
		t.Fatalf("effort knobs = %#v", captured.RequestKnobs)
	}
}

func TestRouterThinkingEffortMaximumBranches(t *testing.T) {
	tests := []struct {
		name                  string
		provider              string
		runtimeValue          string
		reasoningMaximum      string
		anthropicMaximum      string
		wantThinking          string
		wantReasoning         string
		wantAnthropic         string
		wantConfiguredMaximum string
	}{
		{
			name:                  "disabled remains allowed",
			provider:              "openai",
			runtimeValue:          "disabled",
			reasoningMaximum:      "medium",
			wantThinking:          "disabled",
			wantConfiguredMaximum: "medium",
		},
		{
			name:                  "lower runtime remains lower",
			provider:              "openai",
			runtimeValue:          "low",
			reasoningMaximum:      "medium",
			wantThinking:          "low",
			wantReasoning:         "low",
			wantAnthropic:         "low",
			wantConfiguredMaximum: "medium",
		},
		{
			name:                  "anthropic uses anthropic maximum",
			provider:              "anthropic",
			runtimeValue:          "max",
			reasoningMaximum:      "high",
			anthropicMaximum:      "medium",
			wantThinking:          "medium",
			wantReasoning:         "medium",
			wantAnthropic:         "medium",
			wantConfiguredMaximum: "medium",
		},
		{
			name:          "missing maximum preserves runtime",
			provider:      "openai",
			runtimeValue:  "max",
			wantThinking:  "max",
			wantReasoning: "max",
			wantAnthropic: "max",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := &capturingModelAdapter{}
			router := &Router{
				openai:    adapter,
				anthropic: adapter,
				gemini:    adapter,
				resolver: &reasoningLimitResolver{channel: &legacyruntime.ResolvedChannel{
					ID:                      "channel",
					Name:                    "test",
					Provider:                test.provider,
					BaseURL:                 "https://example.com",
					APIKey:                  "test-key",
					Model:                   "test-model",
					ReasoningEffort:         test.reasoningMaximum,
					AnthropicThinkingEffort: test.anthropicMaximum,
				}},
				healthByChannel: make(map[string]channelHealth),
			}

			err := router.Stream(context.Background(), StreamRequest{
				RequestID:      "request",
				ModelCallID:    "call",
				ModelID:        "test-model",
				ThinkingEffort: test.runtimeValue,
				RequestKnobs:   map[string]any{},
			}, func(ModelEvent) error { return nil })
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			captured := adapter.request
			if captured.ThinkingEffort != test.wantThinking ||
				captured.ReasoningEffort != test.wantReasoning ||
				captured.AnthropicThinkingEffort != test.wantAnthropic {
				t.Fatalf("effort = thinking:%q reasoning:%q anthropic:%q", captured.ThinkingEffort, captured.ReasoningEffort, captured.AnthropicThinkingEffort)
			}
			if captured.ConfiguredThinkingEffortMaximum != test.wantConfiguredMaximum {
				t.Fatalf("configured maximum = %q, want %q", captured.ConfiguredThinkingEffortMaximum, test.wantConfiguredMaximum)
			}
		})
	}
}
