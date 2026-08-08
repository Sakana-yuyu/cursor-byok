package forwarder

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	modeladapter "cursor/internal/backend/agent/model"
)

func TestDelegatedContextBudgetForWindow(t *testing.T) {
	cases := []struct {
		name   string
		window int64
		want   int64
	}{
		{"zero window disables proactive compaction", 0, 0},
		{"negative window disables", -1, 0},
		{"floor does not exceed a small worker window", 10_000, 10_000 - providerOutputSafetyTokens},
		{"normal budget", 272_000, int64(0.8*272_000) - 10_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := delegatedContextBudgetForWindow(tc.window); got != tc.want {
				t.Fatalf("delegatedContextBudgetForWindow(%d) = %d, want %d", tc.window, got, tc.want)
			}
		})
	}
}

func TestDelegatedToolResultOmittedText(t *testing.T) {
	got := delegatedToolResultOmittedText("Read")
	if !strings.Contains(got, "Read") || !strings.Contains(got, "输出过长已省略") {
		t.Fatalf("unexpected omitted text: %q", got)
	}
}

func TestSnipDelegatedOversizedToolResults(t *testing.T) {
	big := strings.Repeat("x", 40*1024)
	messages := []modeladapter.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "task prompt"},
		{Role: "assistant", Content: "let me read", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c1"}}},
		{Role: "tool", Content: big, ToolCallID: "c1", Name: "Read"},
		{Role: "assistant", Content: "done", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c2"}}},
		{Role: "tool", Content: "small result", ToolCallID: "c2", Name: "Shell"},
	}
	// 预算极小，强制压缩（但最近一轮不 snip）
	stats := &delegatedCompactionStats{}
	out, changed := snipDelegatedOversizedToolResults(messages, 100, stats)
	if !changed {
		t.Fatal("expected compaction to happen")
	}
	if stats.SnipCount != 1 {
		t.Fatalf("SnipCount = %d, want 1", stats.SnipCount)
	}
	// 第一条 tool（Read）被截断；最后一条 tool（Shell）不截断
	if out[3].Content == big {
		t.Fatal("oldest oversized tool result should be snipped")
	}
	if len(out[3].Content) > delegatedSnipTargetBytes+len(delegatedToolResultOmittedText("Read")) {
		t.Fatalf("snipped content too long: %d", len(out[3].Content))
	}
	if !strings.Contains(out[3].Content, "输出过长已省略") {
		t.Fatal("snipped content missing omitted marker")
	}
	if out[5].Content != "small result" {
		t.Fatal("recent turn tool result must not be snipped")
	}
}

func TestSnipNoOpWithinBudget(t *testing.T) {
	messages := []modeladapter.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "task"},
	}
	out, changed := snipDelegatedOversizedToolResults(messages, 1_000_000, nil)
	if changed {
		t.Fatal("expected no change within budget")
	}
	if len(out) != len(messages) {
		t.Fatal("messages length changed unexpectedly")
	}
}

func TestDelegatedContextOverflowError(t *testing.T) {
	overflowErrors := []error{
		errors.New("openai responses stream error code=context_too_large: Your input exceeds the context window of this model"),
		errors.New("context_length_exceeded"),
		fmt.Errorf("wrapped: %w", errors.New("input exceeds the context window")),
	}
	for _, err := range overflowErrors {
		if !delegatedContextOverflowError(err) {
			t.Fatalf("delegatedContextOverflowError(%q) = false, want true", err)
		}
	}
	notOverflow := []error{
		errors.New("request_timeout: stream closed before response.completed"),
		errors.New("network error"),
		nil,
	}
	for _, err := range notOverflow {
		if delegatedContextOverflowError(err) {
			t.Fatalf("delegatedContextOverflowError(%v) = true, want false", err)
		}
	}
}

func TestDropDelegatedEarlyMessages(t *testing.T) {
	messages := []modeladapter.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "task prompt"},
		{Role: "assistant", Content: "a1", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c1"}}},
		{Role: "tool", Content: "r1", ToolCallID: "c1", Name: "Shell"},
		{Role: "assistant", Content: "a2", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c2"}}},
		{Role: "tool", Content: "r2", ToolCallID: "c2", Name: "Shell"},
		{Role: "assistant", Content: "a3", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c3"}}},
		{Role: "tool", Content: "r3", ToolCallID: "c3", Name: "Shell"},
		{Role: "assistant", Content: "a4", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c4"}}},
		{Role: "tool", Content: "r4", ToolCallID: "c4", Name: "Shell"},
		{Role: "assistant", Content: "a5", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c5"}}},
		{Role: "tool", Content: "r5", ToolCallID: "c5", Name: "Shell"},
	}
	stats := &delegatedCompactionStats{}
	out, changed := dropDelegatedEarlyMessages(messages, 10, stats)
	if !changed {
		t.Fatal("expected drop to happen")
	}
	if stats.DroppedCount == 0 {
		t.Fatal("DroppedCount should be > 0")
	}
	// system 与首条 user 必须保留
	if out[0].Role != "system" || out[1].Role != "user" {
		t.Fatalf("system/user must be preserved, got roles %q %q", out[0].Role, out[1].Role)
	}
	// 剩余消息必须保持 tool 前有对应 assistant（成对性）
	for i := 1; i < len(out); i++ {
		if out[i].Role == "tool" && out[i-1].Role != "assistant" {
			t.Fatalf("tool message at %d not preceded by assistant", i)
		}
	}
	// 最近 4 轮保留：a2/a3/a4/a5 及其 tool 都在
	kept := 0
	for _, m := range out {
		if m.Role == "assistant" {
			kept++
		}
	}
	if kept != 4 {
		t.Fatalf("expected 4 assistant turns kept, got %d", kept)
	}
	last := out[len(out)-1]
	if last.Role != "tool" || last.Content != "r5" {
		t.Fatalf("recent turn must be preserved, got %+v", last)
	}
}

func TestDropDelegatedEarlyMessagesParallelTools(t *testing.T) {
	// 轮 1 为并行工具调用：1 条 assistant（2 个 ToolCalls）+ 2 条连续 tool 消息。
	// 丢弃该轮时必须整组删除，否则会残留孤立 tool 消息。
	messages := []modeladapter.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "task prompt"},
		{Role: "assistant", Content: "a1", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c1a"}, {ID: "c1b"}}},
		{Role: "tool", Content: "r1a", ToolCallID: "c1a", Name: "Shell"},
		{Role: "tool", Content: "r1b", ToolCallID: "c1b", Name: "Read"},
		{Role: "assistant", Content: "a2", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c2"}}},
		{Role: "tool", Content: "r2", ToolCallID: "c2", Name: "Shell"},
		{Role: "assistant", Content: "a3", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c3"}}},
		{Role: "tool", Content: "r3", ToolCallID: "c3", Name: "Shell"},
		{Role: "assistant", Content: "a4", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c4"}}},
		{Role: "tool", Content: "r4", ToolCallID: "c4", Name: "Shell"},
		{Role: "assistant", Content: "a5", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c5"}}},
		{Role: "tool", Content: "r5", ToolCallID: "c5", Name: "Shell"},
	}
	stats := &delegatedCompactionStats{}
	out, changed := dropDelegatedEarlyMessages(messages, 10, stats)
	if !changed {
		t.Fatal("expected drop to happen")
	}
	if stats.DroppedCount == 0 {
		t.Fatal("DroppedCount should be > 0")
	}
	// system 与首条 user 必须保留
	if out[0].Role != "system" || out[1].Role != "user" {
		t.Fatalf("system/user must be preserved, got roles %q %q", out[0].Role, out[1].Role)
	}
	// 任意 role=tool 消息的前一条必须是 assistant：无孤立 tool 残留
	for i := 1; i < len(out); i++ {
		if out[i].Role == "tool" && out[i-1].Role != "assistant" {
			t.Fatalf("orphan tool message at %d (prev=%q)", i, out[i-1].Role)
		}
	}
	// 并行轮（a1 + r1a + r1b）必须整组消失
	for _, m := range out {
		if m.Content == "a1" || m.ToolCallID == "c1a" || m.ToolCallID == "c1b" {
			t.Fatalf("parallel turn must be dropped entirely, got %+v", m)
		}
		if len(m.ToolCalls) > 1 {
			t.Fatalf("parallel turn assistant must be gone, got %+v", m)
		}
	}
	// 保留最近 4 轮（a2/a3/a4/a5 及其 tool）
	kept := 0
	for _, m := range out {
		if m.Role == "assistant" {
			kept++
		}
	}
	if kept != 4 {
		t.Fatalf("expected 4 assistant turns kept, got %d", kept)
	}
	last := out[len(out)-1]
	if last.Role != "tool" || last.Content != "r5" {
		t.Fatalf("recent turn must be preserved, got %+v", last)
	}
}

func TestMaybeCompactDelegatedMessages(t *testing.T) {
	big := strings.Repeat("y", 20*1024)
	messages := []modeladapter.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "task"},
		{Role: "assistant", Content: "a1", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c1"}}},
		{Role: "tool", Content: big, ToolCallID: "c1", Name: "Read"},
		{Role: "assistant", Content: "a2", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c2"}}},
		{Role: "tool", Content: "r2", ToolCallID: "c2", Name: "Shell"},
	}
	stats := &delegatedCompactionStats{}
	out, changed := maybeCompactDelegatedMessages(messages, 100, stats)
	if !changed {
		t.Fatal("expected compaction")
	}
	if len(out) < 2 || out[0].Role != "system" {
		t.Fatal("system message must remain")
	}
	if stats.SnipCount == 0 && stats.DroppedCount == 0 {
		t.Fatal("expected snip or drop to have run")
	}
	// budget<=0 时不压缩
	if _, c := maybeCompactDelegatedMessages(messages, 0, nil); c {
		t.Fatal("budget<=0 must not compact")
	}
}

func TestBuildDelegatedMessageWindowDropsWholeParallelToolBatchAndLeavesInputUntouched(t *testing.T) {
	large := strings.Repeat("x", 48*1024)
	messages := []modeladapter.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "task"},
		{Role: "user", Content: "older request"},
		{Role: "assistant", Content: "old calls", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "old-a"}, {ID: "old-b"}}},
		{Role: "tool", ToolCallID: "old-a", Name: "Read", Content: large},
		{Role: "tool", ToolCallID: "old-b", Name: "Glob", Content: large},
		{Role: "user", Content: "recent request"},
		{Role: "assistant", Content: "recent call", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "recent"}}},
		{Role: "tool", ToolCallID: "recent", Name: "Read", Content: "recent output"},
	}

	out, changed, err := buildDelegatedMessageWindow(messages, 100, nil)
	if err != nil {
		t.Fatalf("buildDelegatedMessageWindow() error = %v", err)
	}
	if !changed {
		t.Fatal("expected window to change")
	}
	if messages[4].Content != large || messages[5].Content != large {
		t.Fatal("windowing must operate on a clone, not mutate the worker input")
	}
	for _, message := range out {
		if message.ToolCallID == "old-a" || message.ToolCallID == "old-b" || message.Content == "old calls" {
			t.Fatalf("old parallel batch was split rather than dropped as a unit: %+v", message)
		}
	}
	if err := validateDelegatedMessageStructure(out); err != nil {
		t.Fatalf("window output lost structure: %v", err)
	}
}

func TestBuildDelegatedMessageWindowFailsClosedForInvalidToolStructure(t *testing.T) {
	tests := []struct {
		name     string
		messages []modeladapter.Message
	}{
		{
			name: "orphan result",
			messages: []modeladapter.Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "task"},
				{Role: "tool", ToolCallID: "missing", Content: "result"},
			},
		},
		{
			name: "duplicate result",
			messages: []modeladapter.Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "task"},
				{Role: "assistant", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "call"}}},
				{Role: "tool", ToolCallID: "call", Content: "first"},
				{Role: "tool", ToolCallID: "call", Content: "second"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := buildDelegatedMessageWindow(tt.messages, 100, nil); err == nil {
				t.Fatal("expected invalid tool structure to fail closed")
			}
		})
	}
}

func TestBuildDelegatedMessageWindowDropsACompletePlainTurn(t *testing.T) {
	oldRequest := strings.Repeat("old request ", 4_000)
	messages := []modeladapter.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "task"},
		{Role: "user", Content: oldRequest},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "current request"},
	}

	out, changed, err := buildDelegatedMessageWindow(messages, 200, nil)
	if err != nil {
		t.Fatalf("buildDelegatedMessageWindow() error = %v", err)
	}
	if !changed {
		t.Fatal("expected old plain turn to be windowed")
	}
	for _, message := range out {
		if message.Content == oldRequest || message.Content == "old answer" {
			t.Fatalf("ordinary turn was split instead of dropped as a unit: %+v", message)
		}
	}
}
