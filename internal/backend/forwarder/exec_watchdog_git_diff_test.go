package forwarder

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

// TestGitDiffExecWatchdogTimeoutIsShort 验证 git_diff 不使用 10 分钟通用档位。
// 客户端的 git diff 处理器要么秒级返回，要么在 base ref 解析/响应体积检查处抛错
// 且不回包；用通用档位等待，模型会在一次 GitDiff 上白白挂掉十分钟。
func TestGitDiffExecWatchdogTimeoutIsShort(t *testing.T) {
	timeout := execTimeoutForKind("git_diff")
	if timeout > time.Minute {
		t.Fatalf("execTimeoutForKind(git_diff) = %s, want at most 1m", timeout)
	}
	if timeout >= defaultExecTimeout {
		t.Fatalf("execTimeoutForKind(git_diff) = %s, want shorter than the default %s", timeout, defaultExecTimeout)
	}
	if timeout <= 0 {
		t.Fatalf("execTimeoutForKind(git_diff) = %s, want a positive deadline", timeout)
	}
}

// TestGitDiffWatchdogPayloadOffersShellFallbackWithoutClaimingCleanTree 验证客户端不回包时
// 交还给模型的降级文案：把「环境没给出 diff」和「仓库没有改动」彻底区分开，并在开头
// 给出可执行退路。提示必须在开头——整体截断砍掉的是结尾。
func TestGitDiffWatchdogPayloadOffersShellFallbackWithoutClaimingCleanTree(t *testing.T) {
	payload := buildSyntheticExecResultPayload(runtimecore.PendingExec{
		ExecID:   "exec-git-diff-1",
		ExecKind: "git_diff",
		ArgsJSON: []byte(`{}`),
	}, "exec_watchdog_timeout")

	if !strings.HasPrefix(payload, "GitDiff unavailable:") {
		t.Fatalf("payload must lead with the degradation notice, got:\n%s", payload)
	}
	for _, want := range []string{
		"NOT a report that there are no changes",
		"Shell",
		"git status --porcelain",
		"git diff",
	} {
		if !strings.Contains(payload, want) {
			t.Errorf("payload is missing %q:\n%s", want, payload)
		}
	}
	// "git diff empty" 是真实空 diff 的成功文案；降级文案绝不能落到同一措辞上。
	if strings.Contains(payload, "git diff empty") {
		t.Errorf("degraded payload must never read like an empty diff result:\n%s", payload)
	}
}

// TestRecoverStaleExecWithoutTerminalGitDiffHandsBackShellFallback 验证端到端：
// git_diff 看门狗到期时 exec 被收口，并向模型追加一条可执行的工具结果
// （而不是工具调用失败，也不是「没有改动」）。
func TestRecoverStaleExecWithoutTerminalGitDiffHandsBackShellFallback(t *testing.T) {
	broker := NewStreamBroker()
	stream, err := broker.OpenStream("req-git-diff", "conv-git-diff", 1, "m", "m", agentv1.AgentMode_AGENT_MODE_AGENT, "review the branch")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	setupCheckpointConversation(stream)

	execID := "exec-git-diff-stale"
	stream.mu.Lock()
	stream.PendingExecs[execID] = runtimecore.PendingExec{
		MessageID:   9,
		ExecID:      execID,
		ExecKind:    "git_diff",
		ToolCallID:  "call-git-diff",
		ModelCallID: "mc-git-diff",
		ArgsJSON:    []byte(`{}`),
		OpenedAt:    time.Now().UTC().Add(-time.Hour),
	}
	stream.mu.Unlock()

	service := newTestService(broker, func() time.Duration { return time.Second }, nil)
	if err := service.recoverStaleExecWithoutTerminal(stream, execID, 9, "exec_watchdog_timeout"); err != nil {
		t.Fatalf("recoverStaleExecWithoutTerminal() error = %v", err)
	}
	if _, stillPending := snapshotPendingExec(stream, execID); stillPending {
		t.Fatal("git_diff exec is still pending after the watchdog fired")
	}

	resultText := ""
	for _, entry := range snapshotHistoryEntries(stream) {
		if entry.Kind != "tool_result" {
			continue
		}
		var payload toolResultEntryPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatalf("decode tool_result payload: %v", err)
		}
		if payload.ToolName == "GitDiff" {
			resultText = payload.ResultText
		}
	}
	if resultText == "" {
		t.Fatal("watchdog recovery appended no GitDiff tool result")
	}
	if !strings.HasPrefix(resultText, "GitDiff unavailable:") {
		t.Fatalf("tool result must lead with the degradation notice, got:\n%s", resultText)
	}
	if !strings.Contains(resultText, "NOT a report that there are no changes") {
		t.Fatalf("tool result must not be readable as a clean working tree:\n%s", resultText)
	}
	if !strings.Contains(resultText, "Shell") {
		t.Fatalf("tool result must point at the Shell fallback:\n%s", resultText)
	}
}

// TestRecoverNonStreamingExecAfterStreamCloseGitDiffHandsBackShellFallback 验证第二条
// 恢复路径：客户端对 git_diff 发 stream_close 时走的是非流式恢复，而不是 exec 看门狗。
// 它此前有一段独立的硬编码文案，模型同样拿不到任何退路；两条路径必须共用同一份措辞。
func TestRecoverNonStreamingExecAfterStreamCloseGitDiffHandsBackShellFallback(t *testing.T) {
	broker := NewStreamBroker()
	stream, err := broker.OpenStream("req-git-diff-close", "conv-git-diff-close", 1, "m", "m", agentv1.AgentMode_AGENT_MODE_AGENT, "review the branch")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	setupCheckpointConversation(stream)

	pending := runtimecore.PendingExec{
		MessageID:   9,
		ExecID:      "exec-git-diff-closed",
		ExecKind:    "git_diff",
		ToolCallID:  "call-git-diff",
		ModelCallID: "mc-git-diff",
		ArgsJSON:    []byte(`{}`),
	}
	stream.mu.Lock()
	stream.PendingExecs[pending.ExecID] = pending
	stream.mu.Unlock()

	service := newTestService(broker, func() time.Duration { return time.Second }, nil)
	if err := service.recoverNonStreamingExecAfterStreamClose(stream, pending); err != nil {
		t.Fatalf("recoverNonStreamingExecAfterStreamClose() error = %v", err)
	}

	resultText := ""
	for _, entry := range snapshotHistoryEntries(stream) {
		if entry.Kind != "tool_result" {
			continue
		}
		var payload toolResultEntryPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatalf("decode tool_result payload: %v", err)
		}
		if payload.ToolName == "GitDiff" {
			resultText = payload.ResultText
		}
	}
	if resultText == "" {
		t.Fatal("stream_close recovery appended no GitDiff tool result")
	}
	if !strings.HasPrefix(resultText, "GitDiff unavailable:") {
		t.Fatalf("tool result must lead with the degradation notice, got:\n%s", resultText)
	}
	for _, want := range []string{
		"NOT a report that there are no changes",
		"Shell",
		"git status --porcelain",
	} {
		if !strings.Contains(resultText, want) {
			t.Errorf("tool result is missing %q:\n%s", want, resultText)
		}
	}
	// stream_close 是「传输被关掉」，不是「等满了 45 秒」。共用措辞不等于可以谎报原因。
	if strings.Contains(resultText, gitDiffExecTimeout.String()) {
		t.Errorf("transport-closed recovery must not claim it waited out the %s timeout:\n%s", gitDiffExecTimeout, resultText)
	}
}
