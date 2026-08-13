package forwarder

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
)

const hiddenEditRecoveryPath = "/ws/main.go"

// openHiddenEditRecoveryStream 建一个已登记 Write/PatchEdit tool_call 的流，
// 模拟「模型已发出编辑调用、隐藏执行桥步骤挂起」的真实状态。
func openHiddenEditRecoveryStream(t *testing.T, requestID string, toolCallID string, toolName string) (*Service, *ActiveStream) {
	t.Helper()
	broker := NewStreamBroker()
	stream, err := broker.OpenStream(requestID, requestID+"-conv", 1, "m", "m", agentv1.AgentMode_AGENT_MODE_AGENT, "edit the file")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	setupCheckpointConversation(stream)
	service := newTestService(broker, func() time.Duration { return time.Second }, nil)

	startedToolCall, err := protojson.Marshal(buildStartedWriteHistoryToolCall(hiddenEditRecoveryPath))
	if err != nil {
		t.Fatalf("marshal started tool call: %v", err)
	}
	if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
		newToolCallEntryWithProviderMetadata(stream.TurnSeq, stream.RequestID, toolCallID, toolName, "", "", "", "", "", nil, "", "", "", startedToolCall),
	}); err != nil {
		t.Fatalf("append tool_call entry: %v", err)
	}
	return service, stream
}

func registerPendingHiddenExec(t *testing.T, stream *ActiveStream, execID string, execKind string, toolCallID string, argsJSON []byte) runtimecore.PendingExec {
	t.Helper()
	pending := runtimecore.PendingExec{
		MessageID:   11,
		ExecID:      execID,
		ExecKind:    execKind,
		ToolCallID:  toolCallID,
		ModelCallID: "mc-hidden",
		ArgsJSON:    argsJSON,
		OpenedAt:    time.Now().UTC().Add(-time.Hour),
	}
	stream.mu.Lock()
	stream.PendingExecs[execID] = pending
	stream.mu.Unlock()
	return pending
}

func hiddenWritePayloadJSON(t *testing.T, before string, after string) []byte {
	t.Helper()
	raw, err := pendingWritePayload{
		VisibleArgs:   writeOperationArgs{Path: hiddenEditRecoveryPath, Contents: after},
		ResolvedPath:  hiddenEditRecoveryPath,
		BeforeContent: before,
		AfterContent:  after,
	}.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal pending write payload: %v", err)
	}
	return raw
}

func hiddenPatchEditPayloadJSON(t *testing.T, before string, after string) []byte {
	t.Helper()
	raw, err := pendingPatchEditPayload{
		ToolName:      patchEditToolName,
		Args:          patchEditArgs{Path: hiddenEditRecoveryPath, OldString: before, NewString: after, NewStringSet: true},
		ResolvedPath:  hiddenEditRecoveryPath,
		BeforeContent: before,
		AfterContent:  after,
	}.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal pending patch edit payload: %v", err)
	}
	return raw
}

// lastToolResultPayload 返回最后一条 tool_result entry 的解码结果。
func lastToolResultPayload(t *testing.T, stream *ActiveStream) toolResultEntryPayload {
	t.Helper()
	var found bool
	var payload toolResultEntryPayload
	for _, entry := range snapshotHistoryEntries(stream) {
		if entry.Kind != "tool_result" {
			continue
		}
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatalf("decode tool_result payload: %v", err)
		}
		found = true
	}
	if !found {
		t.Fatal("recovery appended no tool_result entry")
	}
	return payload
}

// TestRecoverExecWithoutTerminalHiddenWriteStepKeepsWriteToolName 是 watchdog/turn-stale
// 恢复路径的核心回归：隐藏 exec kind 在 deriveToolNameFromPendingExec 里返回空串，
// 旧实现照样以 toolName="" 调 appendToolResult，projector 会因为工具名为空直接丢弃
// 这条 tool_result，进而让 Write 的 assistant tool_call 变成悬空调用被整条裁掉。
func TestRecoverExecWithoutTerminalHiddenWriteStepKeepsWriteToolName(t *testing.T) {
	for _, execKind := range []string{writeReadExecKind, writeWriteExecKind} {
		t.Run(execKind, func(t *testing.T) {
			service, stream := openHiddenEditRecoveryStream(t, "req-"+execKind, "call-write", "Write")
			pending := registerPendingHiddenExec(t, stream, "exec-"+execKind, execKind, "call-write", hiddenWritePayloadJSON(t, "old\n", "new\n"))

			if err := service.recoverExecWithoutTerminal(stream, pending, "exec_watchdog_timeout"); err != nil {
				t.Fatalf("recoverExecWithoutTerminal() error = %v", err)
			}

			payload := lastToolResultPayload(t, stream)
			if payload.ToolName != "Write" {
				t.Fatalf("tool_result ToolName = %q, want \"Write\"", payload.ToolName)
			}
			if strings.Contains(payload.Arguments, "before_content") || strings.Contains(payload.Arguments, "resolved_path") {
				t.Fatalf("tool_result Arguments leaked the internal pending payload: %s", payload.Arguments)
			}
		})
	}
}

// TestRecoverExecWithoutTerminalHiddenWriteStepSurvivesPromptProjection 证明修复的实际收益：
// 恢复写出的 tool_result 必须能通过 ProjectPromptReplay 到达模型，并且 Write 的
// assistant tool_call 不会因为缺少配对结果而被 trimReplayDanglingAssistantToolCalls 删掉。
func TestRecoverExecWithoutTerminalHiddenWriteStepSurvivesPromptProjection(t *testing.T) {
	service, stream := openHiddenEditRecoveryStream(t, "req-write-projection", "call-write", "Write")
	pending := registerPendingHiddenExec(t, stream, "exec-write-projection", writeWriteExecKind, "call-write", hiddenWritePayloadJSON(t, "old\n", "new\n"))

	if err := service.recoverExecWithoutTerminal(stream, pending, "exec_watchdog_timeout"); err != nil {
		t.Fatalf("recoverExecWithoutTerminal() error = %v", err)
	}

	stream.mu.Lock()
	conversation := cloneConversationFile(stream.CheckpointConversation)
	stream.mu.Unlock()
	messages, err := NewHistoryProjector().ProjectPromptReplay(conversation)
	if err != nil {
		t.Fatalf("ProjectPromptReplay() error = %v", err)
	}

	var assistantToolCall *modeladapter.Message
	var toolResult *modeladapter.Message
	for index := range messages {
		switch strings.TrimSpace(messages[index].Role) {
		case "assistant":
			if len(messages[index].ToolCalls) > 0 {
				assistantToolCall = &messages[index]
			}
		case "tool":
			toolResult = &messages[index]
		}
	}
	if assistantToolCall == nil {
		t.Fatal("projected replay dropped the Write assistant tool_call entirely")
	}
	if toolResult == nil {
		t.Fatal("projected replay dropped the recovered tool result")
	}
	if strings.TrimSpace(toolResult.ToolCallID) != "call-write" {
		t.Fatalf("projected tool result tool_call_id = %q, want \"call-write\"", toolResult.ToolCallID)
	}
}

// TestRecoverExecWithoutTerminalWritePreReadReportsUnchangedFile 验证 write_read 阶段的降级语义：
// 写请求还没发出去，文件必然未改动，可以明确告诉模型这一点。
func TestRecoverExecWithoutTerminalWritePreReadReportsUnchangedFile(t *testing.T) {
	service, stream := openHiddenEditRecoveryStream(t, "req-write-preread", "call-write", "Write")
	pending := registerPendingHiddenExec(t, stream, "exec-write-preread", writeReadExecKind, "call-write", hiddenWritePayloadJSON(t, "", "new\n"))

	if err := service.recoverExecWithoutTerminal(stream, pending, "exec_watchdog_timeout"); err != nil {
		t.Fatalf("recoverExecWithoutTerminal() error = %v", err)
	}

	result := lastToolResultPayload(t, stream).ResultText
	if !strings.HasPrefix(result, "Write failed:") {
		t.Fatalf("result must lead with the degradation notice, got:\n%s", result)
	}
	if !strings.Contains(result, "was never sent") {
		t.Fatalf("pre-read recovery must state the write was never dispatched:\n%s", result)
	}
	if strings.Contains(result, `"success"`) {
		t.Fatalf("pre-read recovery must not look like a successful write:\n%s", result)
	}
}

// TestRecoverExecWithoutTerminalWriteStepReportsUnknownOutcome 验证 write_write 阶段的降级语义：
// 写请求已经发给客户端但没有确认，结果必须是「未知」——既不能说成功，也不能说没写。
func TestRecoverExecWithoutTerminalWriteStepReportsUnknownOutcome(t *testing.T) {
	service, stream := openHiddenEditRecoveryStream(t, "req-write-unknown", "call-write", "Write")
	pending := registerPendingHiddenExec(t, stream, "exec-write-unknown", writeWriteExecKind, "call-write", hiddenWritePayloadJSON(t, "old\n", "new\n"))

	if err := service.recoverExecWithoutTerminal(stream, pending, "exec_watchdog_timeout"); err != nil {
		t.Fatalf("recoverExecWithoutTerminal() error = %v", err)
	}

	result := lastToolResultPayload(t, stream).ResultText
	if !strings.HasPrefix(result, "Write outcome UNKNOWN:") {
		t.Fatalf("result must lead with the unknown-outcome notice, got:\n%s", result)
	}
	for _, want := range []string{"may or may not", "Read"} {
		if !strings.Contains(result, want) {
			t.Errorf("result is missing %q:\n%s", want, result)
		}
	}
	if strings.Contains(result, `"success"`) {
		t.Fatalf("unknown outcome must never render as a successful write:\n%s", result)
	}
}

// TestRecoverExecWithoutTerminalWritePostReadKeepsWriteSuccess 验证 write_post_read 阶段：
// 客户端已经回过 WriteResult_Success，写入确实落盘了，超时的只是回读校验。
// 与 handleHiddenWriteExecControl 的 postRead 分支保持一致，按成功收口。
func TestRecoverExecWithoutTerminalWritePostReadKeepsWriteSuccess(t *testing.T) {
	service, stream := openHiddenEditRecoveryStream(t, "req-write-postread", "call-write", "Write")
	pending := registerPendingHiddenExec(t, stream, "exec-write-postread", writePostReadExecKind, "call-write", hiddenWritePayloadJSON(t, "old\n", "new\n"))

	if err := service.recoverExecWithoutTerminal(stream, pending, "exec_watchdog_timeout"); err != nil {
		t.Fatalf("recoverExecWithoutTerminal() error = %v", err)
	}

	payload := lastToolResultPayload(t, stream)
	if payload.ToolName != "Write" {
		t.Fatalf("tool_result ToolName = %q, want \"Write\"", payload.ToolName)
	}
	if !strings.Contains(payload.ResultText, `"success"`) {
		t.Fatalf("post-read recovery must keep the already-confirmed write successful:\n%s", payload.ResultText)
	}
}

// TestRecoverExecWithoutTerminalWriteCanvasDiagnosticsKeepsWriteSuccess 验证诊断步骤超时：
// canvas 渲染依赖 EditResult 为 success，诊断不可用只能降级为「跳过诊断」。
func TestRecoverExecWithoutTerminalWriteCanvasDiagnosticsKeepsWriteSuccess(t *testing.T) {
	service, stream := openHiddenEditRecoveryStream(t, "req-write-canvas", "call-write", "Write")
	pending := registerPendingHiddenExec(t, stream, "exec-write-canvas", writeCanvasDiagnosticsExecKind, "call-write", hiddenWritePayloadJSON(t, "old\n", "new\n"))

	if err := service.recoverExecWithoutTerminal(stream, pending, "exec_watchdog_timeout"); err != nil {
		t.Fatalf("recoverExecWithoutTerminal() error = %v", err)
	}

	payload := lastToolResultPayload(t, stream)
	if payload.ToolName != "Write" {
		t.Fatalf("tool_result ToolName = %q, want \"Write\"", payload.ToolName)
	}
	if !strings.Contains(payload.ResultText, `"success"`) {
		t.Fatalf("canvas diagnostics timeout must not fail the write:\n%s", payload.ResultText)
	}
	if strings.Contains(payload.ResultText, "Canvas TypeScript check") {
		t.Fatalf("skipped diagnostics must not fabricate a check result:\n%s", payload.ResultText)
	}
}

// TestRecoverExecWithoutTerminalHiddenPatchEditStepKeepsPatchEditToolName 覆盖 PatchEdit 侧
// 的同一缺陷，并确认模型看到的仍是 PatchEdit 的可见参数。
func TestRecoverExecWithoutTerminalHiddenPatchEditStepKeepsPatchEditToolName(t *testing.T) {
	service, stream := openHiddenEditRecoveryStream(t, "req-patch-unknown", "call-patch", patchEditToolName)
	pending := registerPendingHiddenExec(t, stream, "exec-patch-unknown", patchEditWriteExecKindName, "call-patch", hiddenPatchEditPayloadJSON(t, "old", "new"))

	if err := service.recoverExecWithoutTerminal(stream, pending, "exec_watchdog_timeout"); err != nil {
		t.Fatalf("recoverExecWithoutTerminal() error = %v", err)
	}

	payload := lastToolResultPayload(t, stream)
	if payload.ToolName != patchEditToolName {
		t.Fatalf("tool_result ToolName = %q, want %q", payload.ToolName, patchEditToolName)
	}
	if !strings.Contains(payload.Arguments, "old_string") {
		t.Fatalf("tool_result Arguments must carry the visible PatchEdit args: %s", payload.Arguments)
	}
	if !strings.HasPrefix(payload.ResultText, "PatchEdit outcome UNKNOWN:") {
		t.Fatalf("result must lead with the unknown-outcome notice, got:\n%s", payload.ResultText)
	}
}

// TestRecoverExecWithoutTerminalPatchEditDrainsQueuedEdits 验证同路径排队的后续编辑
// 不会因为前一次编辑超时而永久卡住——finishPatchEditOperation 负责出队。
func TestRecoverExecWithoutTerminalPatchEditDrainsQueuedEdits(t *testing.T) {
	service, stream := openHiddenEditRecoveryStream(t, "req-patch-queue", "call-patch", patchEditToolName)
	pending := registerPendingHiddenExec(t, stream, "exec-patch-queue", patchEditWriteExecKindName, "call-patch", hiddenPatchEditPayloadJSON(t, "old", "new"))
	enqueuePatchEditOperation(stream, patchEditQueueKey(hiddenEditRecoveryPath), queuedPatchEditOperation{
		ToolCallID:  "call-patch-2",
		ModelCallID: "mc-hidden",
		Payload: pendingPatchEditPayload{
			ToolName:     patchEditToolName,
			Args:         patchEditArgs{Path: hiddenEditRecoveryPath, OldString: "new", NewString: "newer", NewStringSet: true},
			ResolvedPath: hiddenEditRecoveryPath,
		},
	})

	if err := service.recoverExecWithoutTerminal(stream, pending, "exec_watchdog_timeout"); err != nil {
		t.Fatalf("recoverExecWithoutTerminal() error = %v", err)
	}

	stream.mu.Lock()
	remaining := len(stream.PatchEditQueues[patchEditQueueKey(hiddenEditRecoveryPath)])
	stream.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("queued patch edit was never started after recovery, %d still queued", remaining)
	}
}

// TestHiddenEditExecStepsScheduleExecWatchdog 验证 6 个隐藏编辑步骤都登记了 per-exec
// 看门狗。turn-stale 是 per-turn 的，在并发委派/子代理活跃时会整体让路，那时这些
// 步骤没有任何上界；exec 看门狗是唯一与回合状态无关的兜底。
func TestHiddenEditExecStepsScheduleExecWatchdog(t *testing.T) {
	cases := []struct {
		name  string
		start func(service *Service, stream *ActiveStream) error
	}{
		{writeReadExecKind, func(service *Service, stream *ActiveStream) error {
			return service.startHiddenWriteRead(stream, "call-write", "mc", 0, "", "", "", pendingWritePayload{
				VisibleArgs:  writeOperationArgs{Path: hiddenEditRecoveryPath, Contents: "new\n"},
				ResolvedPath: hiddenEditRecoveryPath,
			})
		}},
		{writeWriteExecKind, func(service *Service, stream *ActiveStream) error {
			return service.startHiddenWriteExec(stream, "call-write", "mc", 0, "", "", "", pendingWritePayload{
				VisibleArgs:  writeOperationArgs{Path: hiddenEditRecoveryPath, Contents: "new\n"},
				ResolvedPath: hiddenEditRecoveryPath,
			}, "old\n")
		}},
		{writePostReadExecKind, func(service *Service, stream *ActiveStream) error {
			return service.startHiddenWritePostRead(stream, "call-write", "mc", 0, "", "", "", pendingWritePayload{
				VisibleArgs:  writeOperationArgs{Path: hiddenEditRecoveryPath, Contents: "new\n"},
				ResolvedPath: hiddenEditRecoveryPath,
			})
		}},
		{patchEditReadExecKindName, func(service *Service, stream *ActiveStream) error {
			return service.startHiddenPatchEditRead(stream, "call-patch", "mc", 0, "", "", "", pendingPatchEditPayload{
				ToolName:     patchEditToolName,
				Args:         patchEditArgs{Path: hiddenEditRecoveryPath, OldString: "old", NewString: "new", NewStringSet: true},
				ResolvedPath: hiddenEditRecoveryPath,
			})
		}},
		{patchEditWriteExecKindName, func(service *Service, stream *ActiveStream) error {
			return service.startHiddenPatchEditWrite(stream, "call-patch", "mc", 0, "", "", "", pendingPatchEditPayload{
				ToolName:     patchEditToolName,
				Args:         patchEditArgs{Path: hiddenEditRecoveryPath, OldString: "old", NewString: "new", NewStringSet: true},
				ResolvedPath: hiddenEditRecoveryPath,
			}, "old\n")
		}},
		{patchEditPostReadExecKindName, func(service *Service, stream *ActiveStream) error {
			return service.startHiddenPatchEditPostRead(stream, "call-patch", "mc", 0, "", "", "", pendingPatchEditPayload{
				ToolName:     patchEditToolName,
				Args:         patchEditArgs{Path: hiddenEditRecoveryPath, OldString: "old", NewString: "new", NewStringSet: true},
				ResolvedPath: hiddenEditRecoveryPath,
			})
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service, stream := openHiddenEditRecoveryStream(t, "req-watchdog-"+testCase.name, "call-edit", "Write")
			if err := testCase.start(service, stream); err != nil {
				t.Fatalf("start hidden %s step: %v", testCase.name, err)
			}

			stream.mu.Lock()
			defer stream.mu.Unlock()
			for execID, pending := range stream.PendingExecs {
				if strings.TrimSpace(pending.ExecKind) != testCase.name {
					continue
				}
				if stream.StreamTimers[providerTimerKey(streamTimerExecWatchdog, execID)] == nil {
					t.Fatalf("hidden %s step registered no exec watchdog", testCase.name)
				}
				return
			}
			t.Fatalf("hidden %s step registered no pending exec", testCase.name)
		})
	}
}
