package forwarder

import (
	"testing"
)

func TestNewGoalState(t *testing.T) {
	state := newGoalState("conv-1", "  修复所有测试  ", false)
	if state.ConversationID != "conv-1" {
		t.Fatalf("conversation id = %q, want conv-1", state.ConversationID)
	}
	if state.GoalText != "修复所有测试" {
		t.Fatalf("goal text = %q, want trimmed", state.GoalText)
	}
	if state.Status != GoalStatusRunning {
		t.Fatalf("status = %q, want running", state.Status)
	}
	if state.StartedAt.IsZero() || state.UpdatedAt.IsZero() {
		t.Fatal("timestamps must be set")
	}
}

func TestDefaultGoalRuntimeConfig(t *testing.T) {
	cfg := defaultGoalRuntimeConfig()
	if cfg.MaxProviderPasses != 30 {
		t.Fatalf("max provider passes = %d, want 30", cfg.MaxProviderPasses)
	}
	if cfg.SelfCheckPasses != 2 {
		t.Fatalf("self check passes = %d, want 2", cfg.SelfCheckPasses)
	}
	if cfg.VerifyMaxRetries != 3 || cfg.ErrorMaxRetries != 3 {
		t.Fatalf("retry defaults wrong: verify=%d error=%d", cfg.VerifyMaxRetries, cfg.ErrorMaxRetries)
	}
	if cfg.ProgressInterval != 5 {
		t.Fatalf("progress interval = %d, want 5", cfg.ProgressInterval)
	}
	if cfg.MaxDuration != 0 || cfg.MaxCostUSD != 0 || cfg.Enabled {
		t.Fatalf("unlimited/defaults wrong: %+v", cfg)
	}
}