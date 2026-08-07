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

func TestParseGoalCommand(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantText   string
		wantStrict bool
		wantGoal   bool
	}{
		{"slash goal", "/goal 修复登录 bug", "修复登录 bug", false, true},
		{"hash goal", "#goal 跑通全部单测", "跑通全部单测", false, true},
		{"uppercase", "/GOAL 重构模块", "重构模块", false, true},
		{"strict", "/goal --strict 实现支付流程", "实现支付流程", true, true},
		{"goal empty", "/goal   ", "", false, false},
		{"no prefix", "修复登录 bug", "", false, false},
		{"prefix in middle", "请 /goal 修复", "", false, false},
		{"goal colon", "goal: 整理依赖", "整理依赖", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotText, gotStrict, gotGoal := parseGoalCommand(tc.input)
			if gotText != tc.wantText || gotStrict != tc.wantStrict || gotGoal != tc.wantGoal {
				t.Fatalf("parseGoalCommand(%q) = (%q, %v, %v), want (%q, %v, %v)", tc.input, gotText, gotStrict, gotGoal, tc.wantText, tc.wantStrict, tc.wantGoal)
			}
		})
	}
}

func TestGoalSnapshotsEmpty(t *testing.T) {
	service := &Service{goals: make(map[string]*GoalState)}
	snaps := service.GoalSnapshots()
	if len(snaps) != 0 {
		t.Fatalf("expected empty snapshots, got %d", len(snaps))
	}
}