package forwarder

import (
	"strings"
	"testing"
	"time"

	agentv1 "cursor/gen/agentv1"
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

// TestApplyGoalCommandIfEnabled 覆盖 goal 开关语义：
// enabled=false 时 /goal 与 /goal --strict 均不被识别，消息原样保留（普通对话）；
// enabled=true 时正常识别并剥离前缀。
func TestApplyGoalCommandIfEnabled(t *testing.T) {
	cases := []struct {
		name         string
		text         string
		enabled      bool
		alreadyGoal  bool
		wantGoalMode bool
		wantText     string
		wantStrict   bool
		wantMsg      string // 期望保留在 UserMessage 中的文本（关闭/未命中时）
	}{
		{"disabled slash goal", "/goal 修复登录 bug", false, false, false, "", false, "/goal 修复登录 bug"},
		{"disabled strict goal", "/goal --strict 实现支付流程", false, false, false, "", false, "/goal --strict 实现支付流程"},
		{"disabled plain message", "修复登录 bug", false, false, false, "", false, "修复登录 bug"},
		{"enabled slash goal", "/goal 修复登录 bug", true, false, true, "修复登录 bug", false, "修复登录 bug"},
		{"enabled strict goal", "/goal --strict 实现支付流程", true, false, true, "实现支付流程", true, "实现支付流程"},
		{"enabled plain message", "修复登录 bug", true, false, false, "", false, "修复登录 bug"},
		{"already goal mode", "/goal 修复登录 bug", true, true, true, "", false, "/goal 修复登录 bug"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			intent := &InboundIntent{
				GoalMode: tc.alreadyGoal,
				UserMessage: &agentv1.UserMessage{Text: tc.text},
			}
			applyGoalCommandIfEnabled(intent, tc.enabled)
			if intent.GoalMode != tc.wantGoalMode {
				t.Fatalf("GoalMode = %v, want %v", intent.GoalMode, tc.wantGoalMode)
			}
			if intent.GoalText != tc.wantText {
				t.Fatalf("GoalText = %q, want %q", intent.GoalText, tc.wantText)
			}
			if intent.GoalStrict != tc.wantStrict {
				t.Fatalf("GoalStrict = %v, want %v", intent.GoalStrict, tc.wantStrict)
			}
			if gotMsg := userMessageText(intent.UserMessage); gotMsg != tc.wantMsg {
				t.Fatalf("UserMessage = %q, want %q", gotMsg, tc.wantMsg)
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

func TestGoalSystemPromptFragment(t *testing.T) {
	goal := &GoalState{GoalText: "修复全部测试"}
	cfg := defaultGoalRuntimeConfig()
	cfg.MaxProviderPasses = 10
	frag := goalSystemPromptFragment(goal, cfg)
	for _, want := range []string{"GOAL", "修复全部测试", "10 轮", "自检", "失败", "完成报告"} {
		if !strings.Contains(frag, want) {
			t.Fatalf("fragment missing %q: %s", want, frag)
		}
	}
	if frag := goalSystemPromptFragment(nil, cfg); frag != "" {
		t.Fatalf("nil goal must produce empty fragment, got %q", frag)
	}
	if frag := goalSystemPromptFragment(&GoalState{GoalText: "  "}, cfg); frag != "" {
		t.Fatalf("blank goal must produce empty fragment, got %q", frag)
	}
}

func TestJoinNonEmpty(t *testing.T) {
	if got := joinNonEmpty("a", "", "b", "  "); got != "a\n\nb" {
		t.Fatalf("joinNonEmpty = %q, want %q", got, "a\n\nb")
	}
	if got := joinNonEmpty("", ""); got != "" {
		t.Fatalf("joinNonEmpty all empty = %q, want empty", got)
	}
}

func TestGoalBudgetExceeded(t *testing.T) {
	cfg := defaultGoalRuntimeConfig()
	cfg.MaxProviderPasses = 5
	goal := &GoalState{ProviderPasses: 4}
	if exceeded, _ := goalBudgetExceeded(goal, cfg); exceeded {
		t.Fatal("pass 4 of 5 must not exceed")
	}
	goal.ProviderPasses = 5
	if exceeded, reason := goalBudgetExceeded(goal, cfg); !exceeded || reason == "" {
		t.Fatalf("pass 5 of 5 must exceed, got exceeded=%v reason=%q", exceeded, reason)
	}
	unlimited := defaultGoalRuntimeConfig()
	unlimited.MaxProviderPasses = 0 // 0 = 不限
	goal.ProviderPasses = 999
	if exceeded, _ := goalBudgetExceeded(goal, unlimited); exceeded {
		t.Fatal("unlimited passes must never exceed")
	}
	durCfg := defaultGoalRuntimeConfig()
	durCfg.MaxDuration = time.Minute
	old := &GoalState{StartedAt: time.Now().UTC().Add(-2 * time.Minute), ProviderPasses: 1}
	if exceeded, _ := goalBudgetExceeded(old, durCfg); !exceeded {
		t.Fatal("started 2m ago with 1m budget must exceed")
	}
}

func TestGoalIdleReminder(t *testing.T) {
	goal := &GoalState{GoalText: "修复测试"}
	msg := goalIdleReminder(goal, 1)
	if msg.Source != promptContextSourceGoalIdle {
		t.Fatalf("source = %q", msg.Source)
	}
	if !strings.Contains(msg.Message.Content, "修复测试") {
		t.Fatalf("reminder missing goal text: %q", msg.Message.Content)
	}
	if !strings.Contains(msg.Message.Content, "连续 1 轮") {
		t.Fatalf("reminder missing idle count: %q", msg.Message.Content)
	}
	escalated := goalIdleReminder(goal, goalStalePivotThreshold)
	if !strings.Contains(escalated.Message.Content, "换策略") {
		t.Fatalf("escalated reminder missing pivot instruction: %q", escalated.Message.Content)
	}
}

func TestGoalVerifyFeedbackReminder(t *testing.T) {
	goal := &GoalState{GoalText: "修复测试"}
	msg := goalVerifyFeedbackReminder(goal, "仍有 3 个用例失败")
	if msg.Source != promptContextSourceGoalVerifyFeedback {
		t.Fatalf("source = %q", msg.Source)
	}
	if !strings.Contains(msg.Message.Content, "仍有 3 个用例失败") {
		t.Fatalf("feedback missing report: %q", msg.Message.Content)
	}
}

func TestParseVerifyDecision(t *testing.T) {
	cases := []struct {
		name       string
		output     string
		wantVer    bool
		wantReport string
	}{
		{"verified", "VERIFIED\n测试全部通过，无回归。", true, "测试全部通过，无回归。"},
		{"not verified", "NOT_VERIFIED\n仍有 3 个用例失败。", false, "仍有 3 个用例失败。"},
		{"lowercase", "verified\nok", true, "ok"},
		{"blank", "", false, ""},
		{"no marker", "测试全部通过", false, "测试全部通过"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotVer, gotReport := parseVerifyDecision(tc.output)
			if gotVer != tc.wantVer || gotReport != tc.wantReport {
				t.Fatalf("parseVerifyDecision(%q) = (%v, %q), want (%v, %q)", tc.output, gotVer, gotReport, tc.wantVer, tc.wantReport)
			}
		})
	}
}

func TestGoalVerifyPrompt(t *testing.T) {
	p := goalVerifyPrompt(&GoalState{GoalText: "跑通全部单测"})
	for _, want := range []string{"VERIFIED", "NOT_VERIFIED", "跑通全部单测", "只读"} {
		if !strings.Contains(p, want) {
			t.Fatalf("verify prompt missing %q", want)
		}
	}
}

func TestTruncateText(t *testing.T) {
	if got := truncateText("你好世界", 2); got != "你好…" {
		t.Fatalf("truncateText = %q", got)
	}
	if got := truncateText("abc", 10); got != "abc" {
		t.Fatalf("truncateText short = %q", got)
	}
	if got := truncateText("", 5); got != "" {
		t.Fatalf("truncateText empty = %q", got)
	}
}

func TestGoalErrorRetryReminder(t *testing.T) {
	reminder := goalErrorRetryReminder("upstream 429")
	if !strings.Contains(reminder.Message.Content, "429") {
		t.Fatalf("reminder missing error text: %q", reminder.Message.Content)
	}
	if !strings.Contains(reminder.Message.Content, "继续") {
		t.Fatalf("reminder missing retry instruction: %q", reminder.Message.Content)
	}
}