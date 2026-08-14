// service_exec.go 承载 exec 结果/控制消息处理与非流式 exec 恢复。
package forwarder

import (
	"context"
	"cursor/gen/agentv1"
	execbridge "cursor/internal/backend/agent/bridge/exec"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
	"cursor/internal/logger"
	"cursor/internal/safego"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (service *Service) handleExecResult(intent InboundIntent) error {
	// CursorAdapter only owns child worker execs registered in its waiter table.
	// The parent aggregate remains owned by this stream and is closed later by
	// streamDelegationResult -> handleDelegationResult.
	if intent.ExecClientMessage != nil && service.cursorDelegation != nil && service.cursorDelegation.ConsumeExecMessage(intent.RequestID, intent.ExecClientMessage) {
		return nil
	}
	stream, ok := service.broker.Get(intent.RequestID)
	if !ok || stream == nil {
		return fmt.Errorf("request is not active: %s", intent.RequestID)
	}
	if intent.ExecClientMessage == nil {
		return fmt.Errorf("exec client message is required")
	}
	pending, found := selectPendingExec(intent.ExecClientMessage.GetExecId(), intent.ExecClientMessage.GetId(), stream)
	if !found {
		// 后台化的子代理已从父流摘除，其最终结果必然走到这里。静默吸收即可，
		// 不能报 "pending exec not found"，也不能把结果丢掉。
		if service.absorbBackgroundedSubagentExecResult(
			intent.ExecClientMessage.GetExecId(),
			subagentTerminalOutcomeFrom(intent.ExecClientMessage.GetSubagentResult()),
		) {
			return nil
		}
		if service.observeMissingBackgroundShellExecClientMessage(stream, intent.ExecClientMessage) {
			return nil
		}
		if service.observeMissingShellExecClientMessage(stream, intent.ExecClientMessage) {
			return nil
		}
		if shouldIgnoreMissingExecResult(intent.ExecClientMessage, stream) {
			return nil
		}
		return fmt.Errorf("pending exec not found")
	}
	if execID := strings.TrimSpace(intent.ExecClientMessage.GetExecId()); execID != "" &&
		intent.ExecClientMessage.GetId() != 0 && pending.MessageID != 0 &&
		intent.ExecClientMessage.GetId() != pending.MessageID && service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "exec_identity_mismatch_accepted", map[string]any{
			"exec_id":             execID,
			"expected_message_id": pending.MessageID,
			"received_message_id": intent.ExecClientMessage.GetId(),
			"parent_request_id":   stream.RequestID,
			"exec_kind":           strings.TrimSpace(pending.ExecKind),
			"provider_pass":       pending.ProviderPass,
		})
	}
	if service.ignoreStaleExecProviderPass(stream, pending, "exec_client_message") {
		return nil
	}
	service.observeBackgroundShellExecClientMessage(stream, pending, intent.ExecClientMessage)
	service.observeShellExecClientMessage(stream, pending, intent.ExecClientMessage)
	service.markConversationActivity(stream.ConversationID)
	pending = service.applyExecProgress(stream, pending, intent.ExecClientMessage)
	if isHiddenPatchEditExecKind(pending.ExecKind) {
		return service.handleHiddenPatchEditExecResult(stream, pending, intent.ExecClientMessage)
	}
	if isHiddenWriteExecKind(pending.ExecKind) {
		return service.handleHiddenWriteExecResult(stream, pending, intent.ExecClientMessage)
	}
	result, err := service.execBridge.ApplyExecClientMessage(intent.ExecClientMessage, pending)
	if err != nil {
		if strings.TrimSpace(pending.ExecKind) == "subagent" {
			service.updateNativeDelegationStatus(pending.ExecID, delegation.TaskFailed, "Cursor 子代理执行失败", err.Error())
		}
		return err
	}
	// 捕获点：MCP 执行结果（含失败模式），便于后续读取日志针对性修复执行层问题。
	// 对应已知限制：MCP 执行仍依赖 Cursor 客户端；server_not_found/tool_not_found/超时等需要日志证据。
	if strings.TrimSpace(pending.ExecKind) == "mcp" && result.IsTerminal {
		service.captureMCPExecResult(intent, pending, execTerminalResult{
			ToolCallID:        result.ToolCallID,
			ToolResultPayload: result.ToolResultPayload,
		})
	}
	if result.ShellOutputDelta != nil {
		if err := service.broker.Publish(intent.RequestID, StreamEvent{
			Message: buildShellOutputDeltaMessage(result.ShellOutputDelta),
		}); err != nil {
			return err
		}
		if message := buildShellToolCallDeltaMessage(pending.ToolCallID, pending.ModelCallID, result.ShellOutputDelta); message != nil {
			if err := service.broker.Publish(intent.RequestID, StreamEvent{Message: message}); err != nil {
				return err
			}
		}
	}
	if !result.IsTerminal {
		if len(result.HookAdditionalContexts) > 0 {
			if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
				newMetadataEntry(stream.TurnSeq, stream.RequestID, "shell_hook_additional_context", map[string]any{
					"tool_call_id": strings.TrimSpace(pending.ToolCallID),
					"exec_id":      strings.TrimSpace(pending.ExecID),
					"contexts":     hookAdditionalContextsToRecords(result.HookAdditionalContexts),
				}),
			}); err != nil {
				logger.Errorf("forwarder shell hook context metadata failed request_id=%s tool_call_id=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ToolCallID), err)
			}
		}
		// 只有真实执行数据才算有效进展；摘要/heartbeat 不能延长 watchdog。
		if execMessageHasEffectiveProgress(intent.ExecClientMessage, result) {
			service.markNativeDelegationEffectiveProgress(pending.ExecID, "Cursor 子代理正在处理工具结果")
			service.rescheduleExecWatchdog(intent.RequestID, pending)
		}
		return nil
	}
	markExecCompleted(stream, pending)
	// Shell 指纹熔断：terminal 拒绝事件计入本轮账本，达到阈值后开路。
	if strings.TrimSpace(pending.ExecKind) == "shell" {
		if err := service.recordShellRejection(stream, pending, intent.ExecClientMessage); err != nil {
			return err
		}
	}
	if strings.TrimSpace(pending.ExecKind) == "subagent" {
		status := delegation.TaskCompleted
		progress := "Cursor 子代理已完成"
		errorText := ""
		runStatus := agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_SUCCESS
		if subagentResultFailed(intent.ExecClientMessage.GetSubagentResult()) {
			status = delegation.TaskFailed
			progress = "Cursor 子代理执行失败"
			errorText = subagentResultErrorText(intent.ExecClientMessage.GetSubagentResult())
			runStatus = agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_ERROR
			// 瞬时上游故障（request_timeout/503/连接拒绝等）自动重试同一次 Task：
			// 重新派发后不写终态失败、不交付失败 tool_result，父模型继续等待重试结果。
			if service.maybeAutoRetryNativeSubagent(stream, pending, errorText) {
				return nil
			}
		}
		service.updateNativeDelegationStatus(pending.ExecID, status, progress, errorText)
		service.recordDelegationRunTerminal(stream, pending, runStatus, "Cursor 子代理", errorText)
	}
	backgroundShellToolCallID := ""
	if strings.TrimSpace(pending.ExecKind) == "shell" && shellToolCallIsBackgrounded(result.ToolCall) {
		backgroundShellToolCallID = firstNonEmpty(strings.TrimSpace(result.ToolCallID), strings.TrimSpace(pending.ToolCallID))
	}
	if strings.TrimSpace(pending.ExecKind) == "execute_hook_pre_compact" {
		return service.handlePreCompactTerminal(stream, pending.ProviderPass, strings.TrimSpace(result.ToolResultPayload))
	}
	if result.ToolCall != nil {
		if err := service.appendToolResult(stream, result.ToolCallID, deriveToolNameFromPendingExec(pending), pending.ArgsJSON, result.ToolResultPayload, pending.ReasoningContent, result.ToolCall); err != nil {
			return err
		}
	} else if strings.TrimSpace(result.ToolResultPayload) != "" {
		if err := service.appendToolResult(stream, pending.ToolCallID, deriveToolNameFromPendingExec(pending), pending.ArgsJSON, result.ToolResultPayload, pending.ReasoningContent, nil); err != nil {
			return err
		}
	}
	if backgroundShellToolCallID != "" {
		if recordedToolCallID, recorded := recordBackgroundShellActionMemory(stream, backgroundShellToolCallID, time.Now().UTC()); recorded {
			if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
				newBackgroundShellActionMetadataEntry(stream.TurnSeq, stream.RequestID, recordedToolCallID, backgroundShellActionSourceLocalBackgrounded),
			}); err != nil {
				return err
			}
		}
	}
	if err := service.publishToolCallCompleted(intent.RequestID, result.ToolCallID, pending.ModelCallID, result.ToolCall); err != nil {
		return err
	}
	if err := service.syncSummaryCarryForward(stream.ConversationID, intent.RequestID, pending.ModelCallID); err != nil {
		return err
	}
	if err := service.publishExecCheckpoint(stream, pending); err != nil {
		return err
	}
	return service.reconcileStream(stream)
}

func execMessageHasEffectiveProgress(message *agentv1.ExecClientMessage, result execbridge.ExecApplyResult) bool {
	if message == nil {
		return false
	}
	if result.ShellOutputDelta != nil {
		return true
	}
	if message.GetSubagentResult() != nil {
		return true
	}
	if shell := message.GetShellStream(); shell != nil {
		switch shell.GetEvent().(type) {
		case *agentv1.ShellStream_Stdout, *agentv1.ShellStream_Stderr, *agentv1.ShellStream_Start, *agentv1.ShellStream_Exit, *agentv1.ShellStream_Rejected, *agentv1.ShellStream_PermissionDenied, *agentv1.ShellStream_Backgrounded:
			return true
		default:
			return false
		}
	}
	return message.GetReadResult() != nil ||
		message.GetWriteResult() != nil ||
		message.GetDeleteResult() != nil ||
		message.GetGrepResult() != nil ||
		message.GetLsResult() != nil ||
		message.GetDiagnosticsResult() != nil ||
		message.GetMcpResult() != nil ||
		message.GetFetchResult() != nil ||
		message.GetExecuteHookResult() != nil ||
		message.GetWriteShellStdinResult() != nil ||
		message.GetSubagentAwaitResult() != nil
}

// handleExecControl 处理执行桥控制面结果，例如 stream_close 或 throw。
func (service *Service) handleExecControl(intent InboundIntent) error {
	if intent.ExecClientControlMessage != nil && service.cursorDelegation != nil && service.cursorDelegation.ConsumeExecControl(intent.RequestID, intent.ExecClientControlMessage) {
		return nil
	}
	stream, ok := service.broker.Get(intent.RequestID)
	if !ok || stream == nil {
		if shouldIgnoreStaleExecControl(intent.ExecClientControlMessage) {
			return nil
		}
		return fmt.Errorf("request is not active: %s", intent.RequestID)
	}
	if intent.ExecClientControlMessage == nil {
		return fmt.Errorf("exec client control message is required")
	}
	pending, found := selectPendingExecByControl(intent.ExecClientControlMessage, stream)
	if !found {
		if shouldIgnoreMissingExecControl(intent.ExecClientControlMessage, stream) {
			return nil
		}
		return fmt.Errorf("pending exec not found for control message")
	}
	if service.ignoreStaleExecProviderPass(stream, pending, "exec_client_control") {
		return nil
	}
	service.markConversationActivity(stream.ConversationID)
	pending = service.applyExecControlProgress(stream, pending, intent.ExecClientControlMessage)
	if isHiddenPatchEditExecKind(pending.ExecKind) {
		return service.handleHiddenPatchEditExecControl(stream, pending, intent.ExecClientControlMessage)
	}
	if isHiddenWriteExecKind(pending.ExecKind) {
		return service.handleHiddenWriteExecControl(stream, pending, intent.ExecClientControlMessage)
	}
	result, err := service.execBridge.ApplyExecClientControl(intent.ExecClientControlMessage, pending)
	if err != nil {
		if strings.TrimSpace(pending.ExecKind) == "subagent" {
			service.updateNativeDelegationStatus(pending.ExecID, delegation.TaskFailed, "Cursor 子代理控制通道失败", err.Error())
		}
		return err
	}
	if !result.IsTerminal {
		if shouldRecoverNonStreamingExecOnStreamClose(intent.ExecClientControlMessage, pending) {
			markExecTransportClosed(stream, pending)
			service.scheduleNonStreamingExecRecovery(intent.RequestID, pending)
			return nil
		}
		if shouldObserveShellStreamClose(intent.ExecClientControlMessage, pending) {
			service.observeShellStreamClose(stream, pending)
		}
		return nil
	}
	markExecCompleted(stream, pending)
	if strings.TrimSpace(pending.ExecKind) == "subagent" {
		service.updateNativeDelegationStatus(pending.ExecID, delegation.TaskFailed, "Cursor 子代理被控制通道终止", strings.TrimSpace(result.ToolResultPayload))
	}
	if strings.TrimSpace(pending.ExecKind) == "execute_hook_pre_compact" {
		return service.handlePreCompactTerminal(stream, pending.ProviderPass, "")
	}
	if strings.TrimSpace(result.ToolResultPayload) != "" {
		if err := service.appendToolResult(stream, pending.ToolCallID, deriveToolNameFromPendingExec(pending), pending.ArgsJSON, result.ToolResultPayload, pending.ReasoningContent, nil); err != nil {
			return err
		}
		_, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
			newMetadataEntry(stream.TurnSeq, stream.RequestID, "tool_control", map[string]any{
				"tool_call_id": result.ToolCallID,
				"payload":      result.ToolResultPayload,
			}),
		})
		if err != nil {
			return err
		}
	}
	if err := service.syncSummaryCarryForward(stream.ConversationID, intent.RequestID, pending.ModelCallID); err != nil {
		return err
	}
	if err := service.publishToolCallCompleted(intent.RequestID, result.ToolCallID, pending.ModelCallID, nil); err != nil {
		return err
	}
	if err := service.publishExecCheckpoint(stream, pending); err != nil {
		return err
	}
	return service.reconcileStream(stream)
}

func shouldRecoverNonStreamingExecOnStreamClose(message *agentv1.ExecClientControlMessage, pending runtimecore.PendingExec) bool {
	if message == nil || isStreamingPendingExecKind(pending.ExecKind) {
		return false
	}
	switch message.GetMessage().(type) {
	case *agentv1.ExecClientControlMessage_StreamClose:
		return true
	default:
		return false
	}
}

func shouldObserveShellStreamClose(message *agentv1.ExecClientControlMessage, pending runtimecore.PendingExec) bool {
	if message == nil || strings.TrimSpace(pending.ExecKind) != "shell" {
		return false
	}
	switch message.GetMessage().(type) {
	case *agentv1.ExecClientControlMessage_StreamClose:
		return true
	default:
		return false
	}
}

func isStreamingPendingExecKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "shell":
		return true
	default:
		return false
	}
}

func markExecTransportClosed(stream *ActiveStream, pending runtimecore.PendingExec) {
	if stream == nil {
		return
	}
	stream.mu.Lock()
	current, ok := stream.PendingExecs[pending.ExecID]
	if ok {
		now := time.Now().UTC()
		current.StreamState = "transport_closed"
		current.LastShellActivityAt = now
		stream.PendingExecs[pending.ExecID] = current
		stream.UpdatedAt = now
	}
	stream.mu.Unlock()
}

func snapshotPendingExec(stream *ActiveStream, execID string) (runtimecore.PendingExec, bool) {
	if stream == nil || strings.TrimSpace(execID) == "" {
		return runtimecore.PendingExec{}, false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	item, ok := stream.PendingExecs[strings.TrimSpace(execID)]
	return item, ok
}

func (service *Service) scheduleNonStreamingExecRecovery(requestID string, pending runtimecore.PendingExec) {
	if service == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(pending.ExecID) == "" {
		return
	}
	stream, ok := service.broker.Get(requestID)
	if !ok || stream == nil {
		return
	}
	execID := strings.TrimSpace(pending.ExecID)
	messageID := pending.MessageID
	timerKey := providerTimerKey(streamTimerNonStreamingRecovery, execID)

	// Register a completion signal so the terminal-result path can wake us,
	// and a TimerTokens entry for invalidation — same semantics as
	// scheduleStreamTimer.
	signal := make(chan struct{})
	stream.mu.Lock()
	if stream.ExecCompletionSignals == nil {
		stream.ExecCompletionSignals = make(map[string]chan struct{})
	}
	// Idempotent: if a signal already exists for this exec (duplicate
	// stream_close), don't double-register.
	if _, exists := stream.ExecCompletionSignals[execID]; exists {
		stream.mu.Unlock()
		return
	}
	stream.ExecCompletionSignals[execID] = signal
	if stream.TimerTokens == nil {
		stream.TimerTokens = make(map[string]uint64)
	}
	stream.TimerTokens[timerKey]++
	token := stream.TimerTokens[timerKey]
	stream.mu.Unlock()

	// Choose a grace duration. Production uses the injected conservative
	// default; tests override the field for short deterministic timeouts.
	grace := defaultNonStreamingCloseGrace
	if fn := service.nonStreamingCloseGrace; fn != nil {
		grace = fn()
	}

	// Start a background goroutine that waits for one of:
	//   - signal closed → terminal result arrived; recovery not needed.
	//   - timer fires  → post the timer event to the actor.
	// The actor's handleTimerEvent rechecks pending identity / state under
	// stream.mu before synthesizing failure, so late arrival between timeout
	// and actor dispatch is handled correctly.
	// safego 兜底：这个 goroutine 会等待计时器并回写 actor，一旦 panic 未捕获
	// 会拖垮整个进程；恢复后下面的 defer 仍会释放 ExecCompletionSignals 条目。
	safego.Go("forwarder:non-streaming-recovery-timer", func() {
		defer func() {
			// Clean up the map entry on exit (already closed by the
			// terminal/recovery path, or never needed).
			stream.mu.Lock()
			if ch, ok := stream.ExecCompletionSignals[execID]; ok && ch == signal {
				delete(stream.ExecCompletionSignals, execID)
			}
			stream.mu.Unlock()
		}()

		// Use the injectable timer when a test hook is present;
		// otherwise fall back to the production time.After.
		var timerCh <-chan time.Time
		if fn := service.nonStreamingRecoveryTimer; fn != nil {
			timerCh = fn(execID, grace)
		} else {
			timerCh = time.After(max(grace, 0))
		}

		select {
		case <-signal:
			// Terminal result arrived; no recovery needed.
			return
		case <-timerCh:
			// Grace expired; ask the actor to recheck and possibly
			// synthesize failure.
		}

		// Post the timer event to the stream actor. The event carries
		// the token registered above so timerEventMatches can reject
		// stale events after cancel / new-turn invalidation.
		if err := service.postStreamCommandAsync(stream, streamCommand{
			Kind: streamCommandTimerFired,
			Timer: &streamTimerEvent{
				Key:       timerKey,
				Kind:      streamTimerNonStreamingRecovery,
				Token:     token,
				ExecID:    execID,
				MessageID: messageID,
			},
		}); err != nil && !errors.Is(err, errProviderLoopInterrupted) {
			logger.Errorf("forwarder non-streaming recovery post failed request_id=%s exec_id=%s err=%v",
				strings.TrimSpace(requestID), execID, err)
		}
	})
}

func (service *Service) recoverNonStreamingExecAfterStreamClose(stream *ActiveStream, pending runtimecore.PendingExec) error {
	if stream == nil {
		return nil
	}
	markExecCompleted(stream, pending)
	toolName := strings.TrimSpace(deriveToolNameFromPendingExec(pending))
	resultPayload := fmt.Sprintf("%s transport closed before terminal result arrived", firstNonEmpty(toolName, pending.ExecKind, "tool"))
	// GitDiff 的两条恢复路径必须交还同一份退路：只说「传输关了」，模型既拿不到 diff
	// 也不知道该干什么，下一步多半是重试同一个必然再失败的调用。
	if strings.TrimSpace(pending.ExecKind) == "git_diff" {
		resultPayload = buildGitDiffUnavailablePayload(pending, gitDiffTransportClosedReason, execTimeoutForKind(pending.ExecKind))
	}
	logger.Infof("forwarder synthetic exec recovery request_id=%s tool_call_id=%s message_id=%d exec_id=%s exec_kind=%s", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ToolCallID), pending.MessageID, strings.TrimSpace(pending.ExecID), strings.TrimSpace(pending.ExecKind))
	if toolName != "" {
		if err := service.appendToolResult(stream, pending.ToolCallID, toolName, pending.ArgsJSON, resultPayload, pending.ReasoningContent, nil); err != nil {
			return err
		}
	}
	if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
		newMetadataEntry(stream.TurnSeq, stream.RequestID, "tool_transport_closed", map[string]any{
			"tool_call_id": pending.ToolCallID,
			"message_id":   pending.MessageID,
			"exec_id":      pending.ExecID,
			"exec_kind":    pending.ExecKind,
			"payload":      resultPayload,
		}),
	}); err != nil {
		return err
	}
	if err := service.syncSummaryCarryForward(stream.ConversationID, stream.RequestID, pending.ModelCallID); err != nil {
		return err
	}
	if err := service.publishToolCallCompleted(stream.RequestID, pending.ToolCallID, pending.ModelCallID, nil); err != nil {
		return err
	}
	if err := service.publishCheckpointForce(stream.RequestID, stream.ConversationID); err != nil {
		return err
	}
	return service.reconcileStream(stream)
}

func (service *Service) observeShellStreamClose(stream *ActiveStream, pending runtimecore.PendingExec) {
	if service == nil || stream == nil {
		return
	}
	current, ok := snapshotPendingExec(stream, pending.ExecID)
	if !ok {
		return
	}
	recentState := strings.TrimSpace(current.StreamState)
	if recentState == "transport_closed" || recentState == "exited" || recentState == "backgrounded" || recentState == "rejected" || recentState == "permission_denied" || recentState == "sandbox_unsupported" {
		return
	}
	logger.Infof(
		"forwarder shell stream closed without terminal event request_id=%s tool_call_id=%s message_id=%d exec_id=%s stream_state=%s chunk_count=%d",
		strings.TrimSpace(stream.RequestID),
		strings.TrimSpace(current.ToolCallID),
		current.MessageID,
		strings.TrimSpace(current.ExecID),
		recentState,
		current.ChunkCount,
	)
	markExecTransportClosed(stream, current)
	if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
		newMetadataEntry(stream.TurnSeq, stream.RequestID, "shell_stream_transport_closed", map[string]any{
			"tool_call_id":        current.ToolCallID,
			"message_id":          current.MessageID,
			"exec_id":             current.ExecID,
			"exec_kind":           current.ExecKind,
			"recent_stream_state": recentState,
			"chunk_count":         current.ChunkCount,
			"first_chunk_at":      current.FirstChunkAt,
			"reasoning_present":   strings.TrimSpace(current.ReasoningContent) != "",
			"stdout_buffer_bytes": len(current.StdoutBuffer),
			"stderr_buffer_bytes": len(current.StderrBuffer),
		}),
	}); err != nil {
		logger.Errorf("forwarder shell stream close metadata failed request_id=%s tool_call_id=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(current.ToolCallID), err)
	}
	service.scheduleShellTransportCloseRecovery(stream.RequestID, current)
}

// handleMetadataIntent 处理当前不驱动 provider 的轻量元数据上行。
// selectPendingExec 按 exec_id 或 message_id 在当前流里查找挂起执行桥。
func selectPendingExec(execID string, messageID uint32, stream *ActiveStream) (runtimecore.PendingExec, bool) {
	if stream == nil {
		return runtimecore.PendingExec{}, false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	normalizedExecID := strings.TrimSpace(execID)
	if normalizedExecID != "" {
		if item, ok := stream.PendingExecs[normalizedExecID]; ok {
			// exec_id is the stable execution identity. The bridge message id is
			// transport metadata and may be zero or reassigned by the client.
			return item, true
		}
		return runtimecore.PendingExec{}, false
	}
	if messageID != 0 {
		for _, item := range stream.PendingExecs {
			if item.MessageID == messageID {
				return item, true
			}
		}
	}
	return runtimecore.PendingExec{}, false
}

func selectPendingInteraction(message *agentv1.InteractionResponse, stream *ActiveStream) (runtimecore.PendingInteraction, bool) {
	if stream == nil || message == nil {
		return runtimecore.PendingInteraction{}, false
	}
	interactionID := fmt.Sprintf("%d", message.GetId())
	stream.mu.Lock()
	defer stream.mu.Unlock()
	item, ok := stream.PendingInteractions[interactionID]
	return item, ok
}

// selectPendingExecByControl 根据控制消息的桥消息 ID 查找挂起执行桥。
func selectPendingExecByControl(message *agentv1.ExecClientControlMessage, stream *ActiveStream) (runtimecore.PendingExec, bool) {
	messageID, ok := execControlMessageID(message)
	if !ok {
		return runtimecore.PendingExec{}, false
	}
	return selectPendingExec("", messageID, stream)
}

func execControlMessageID(message *agentv1.ExecClientControlMessage) (uint32, bool) {
	if message == nil {
		return 0, false
	}
	switch item := message.GetMessage().(type) {
	case *agentv1.ExecClientControlMessage_StreamClose:
		return item.StreamClose.GetId(), true
	case *agentv1.ExecClientControlMessage_Throw:
		return item.Throw.GetId(), true
	case *agentv1.ExecClientControlMessage_Heartbeat:
		return item.Heartbeat.GetId(), true
	default:
		return 0, false
	}
}

func shouldIgnoreMissingExecResult(message *agentv1.ExecClientMessage, stream *ActiveStream) bool {
	if message == nil {
		return false
	}
	return recentlyCompletedExecExists(stream, message.GetId())
}

func shouldIgnoreMissingExecControl(message *agentv1.ExecClientControlMessage, stream *ActiveStream) bool {
	if shouldIgnoreStaleExecControl(message) {
		return true
	}
	messageID, ok := execControlMessageID(message)
	if !ok {
		return false
	}
	return recentlyCompletedExecExists(stream, messageID)
}

func shouldIgnoreStaleExecControl(message *agentv1.ExecClientControlMessage) bool {
	if message == nil {
		return false
	}
	switch message.GetMessage().(type) {
	case *agentv1.ExecClientControlMessage_Heartbeat, *agentv1.ExecClientControlMessage_StreamClose:
		// Reconnecting Cursor clients may keep sending transport-level exec
		// heartbeats / close acks after the original in-memory pending state is gone.
		// Treat these as idempotent noise instead of surfacing protocol 400s.
		return true
	default:
		return false
	}
}

type pendingAssistantMessage struct {
	ID      string                    `json:"id,omitempty"`
	Role    string                    `json:"role,omitempty"`
	Content []pendingAssistantContent `json:"content,omitempty"`
}

type pendingAssistantContent struct {
	Type       string          `json:"type,omitempty"`
	Text       string          `json:"text,omitempty"`
	Signature  string          `json:"signature,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
}

type pendingToolCallReplay struct {
	OpenedAt time.Time
	SortKey  string
	Raw      string
}

func buildPendingToolCalls(pendingExecs []runtimecore.PendingExec, pendingInteractions []runtimecore.PendingInteraction) []string {
	if len(pendingExecs) == 0 && len(pendingInteractions) == 0 {
		return nil
	}

	items := make([]pendingToolCallReplay, 0, len(pendingExecs)+len(pendingInteractions))
	for _, pending := range pendingExecs {
		raw, ok := encodePendingExecAsAssistantOutput(pending)
		if !ok {
			continue
		}
		items = append(items, pendingToolCallReplay{
			OpenedAt: pending.OpenedAt,
			SortKey:  fmt.Sprintf("exec-%020d", pending.MessageID),
			Raw:      raw,
		})
	}
	for _, pending := range pendingInteractions {
		raw, ok := encodePendingInteractionAsAssistantOutput(pending)
		if !ok {
			continue
		}
		items = append(items, pendingToolCallReplay{
			OpenedAt: pending.OpenedAt,
			SortKey:  "interaction-" + strings.TrimSpace(pending.InteractionID),
			Raw:      raw,
		})
	}
	if len(items) == 0 {
		return nil
	}

	sort.SliceStable(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		switch {
		case left.OpenedAt.Equal(right.OpenedAt):
			return left.SortKey < right.SortKey
		case left.OpenedAt.IsZero():
			return false
		case right.OpenedAt.IsZero():
			return true
		default:
			return left.OpenedAt.Before(right.OpenedAt)
		}
	})

	encoded := make([]string, 0, len(items))
	for _, item := range items {
		encoded = append(encoded, item.Raw)
	}
	return encoded
}

func encodePendingExecAsAssistantOutput(pending runtimecore.PendingExec) (string, bool) {
	toolCallID := strings.TrimSpace(pending.ToolCallID)
	toolName, argsJSON, ok := pendingAssistantToolShape(pending)
	if toolCallID == "" || !ok || strings.TrimSpace(toolName) == "" {
		return "", false
	}

	payload, err := json.Marshal(pendingAssistantMessage{
		ID:      "1",
		Role:    "assistant",
		Content: buildPendingAssistantContents(pending.ReasoningContent, pending.ReasoningSignature, toolCallID, toolName, argsJSON),
	})
	if err != nil {
		return "", false
	}
	return string(payload), true
}

func encodePendingInteractionAsAssistantOutput(pending runtimecore.PendingInteraction) (string, bool) {
	toolCallID := strings.TrimSpace(pending.ToolCallID)
	toolName := strings.TrimSpace(deriveToolNameFromPendingInteraction(pending))
	if toolCallID == "" || toolName == "" {
		return "", false
	}
	payload, err := json.Marshal(pendingAssistantMessage{
		ID:      "1",
		Role:    "assistant",
		Content: buildPendingAssistantContents(pending.ReasoningContent, pending.ReasoningSignature, toolCallID, toolName, pending.ArgsJSON),
	})
	if err != nil {
		return "", false
	}
	return string(payload), true
}

func buildPendingAssistantContents(reasoningContent string, reasoningSignature string, toolCallID string, toolName string, argsJSON []byte) []pendingAssistantContent {
	items := make([]pendingAssistantContent, 0, 2)
	if strings.TrimSpace(reasoningContent) != "" {
		items = append(items, pendingAssistantContent{
			Type:      "reasoning",
			Text:      reasoningContent,
			Signature: strings.TrimSpace(reasoningSignature),
		})
	}
	items = append(items, pendingAssistantContent{
		Type:       "tool-call",
		ToolCallID: toolCallID,
		ToolName:   strings.TrimSpace(toolName),
		Args:       append(json.RawMessage(nil), argsJSON...),
	})
	return items
}

func pendingAssistantToolShape(pending runtimecore.PendingExec) (string, []byte, bool) {
	switch strings.TrimSpace(pending.ExecKind) {
	case patchEditReadExecKindName, patchEditWriteExecKindName, patchEditPostReadExecKindName:
		payload, err := decodePendingPatchEditPayload(pending.ArgsJSON)
		if err != nil {
			return "", nil, false
		}
		argsJSON, err := patchEditPayloadArgsJSON(payload)
		if err != nil {
			return "", nil, false
		}
		return firstNonEmpty(strings.TrimSpace(payload.ToolName), patchEditToolName), argsJSON, true
	case writeReadExecKind, writeWriteExecKind, writePostReadExecKind:
		payload, err := decodePendingWritePayload(pending.ArgsJSON)
		if err != nil {
			return "", nil, false
		}
		argsJSON, err := payload.VisibleArgs.MarshalJSON()
		if err != nil {
			return "", nil, false
		}
		return "Write", argsJSON, true
	default:
		toolName := strings.TrimSpace(deriveToolNameFromPendingExec(pending))
		if toolName == "" {
			return "", nil, false
		}
		return toolName, append([]byte(nil), pending.ArgsJSON...), true
	}
}

// markExecCompleted 保留一个短时 tombstone，避免迟到的 transport-level control 被误判为协议错误。
func markExecCompleted(stream *ActiveStream, pending runtimecore.PendingExec) {
	if stream == nil {
		return
	}
	now := time.Now().UTC()
	cutoff := now.Add(-completedExecRetention)

	stream.mu.Lock()
	delete(stream.PendingExecs, pending.ExecID)
	// Close the completion signal to wake any waiting non-streaming
	// recovery goroutine; idempotent under stream.mu.
	if stream.ExecCompletionSignals != nil {
		if ch, ok := stream.ExecCompletionSignals[pending.ExecID]; ok {
			delete(stream.ExecCompletionSignals, pending.ExecID)
			close(ch)
		}
	}
	if pending.MessageID != 0 {
		if stream.RecentCompletedExecs == nil {
			stream.RecentCompletedExecs = make(map[uint32]time.Time)
		}
		for messageID, completedAt := range stream.RecentCompletedExecs {
			if completedAt.Before(cutoff) {
				delete(stream.RecentCompletedExecs, messageID)
			}
		}
		stream.RecentCompletedExecs[pending.MessageID] = now
	}
	stream.UpdatedAt = now
	stream.mu.Unlock()
}

// ignoreStaleExecProviderPass ignores only an exec whose exact identity is no
// longer pending. A provider pass mismatch by itself is diagnostic metadata and
// must not discard a still-pending terminal result.
func (service *Service) ignoreStaleExecProviderPass(stream *ActiveStream, pending runtimecore.PendingExec, source string) bool {
	if stream == nil || strings.TrimSpace(pending.ExecID) == "" {
		return false
	}
	stream.mu.Lock()
	currentPass := stream.ProviderPassCount
	_, stillPending := stream.PendingExecs[pending.ExecID]
	stream.mu.Unlock()
	// Once the exact exec_id is still pending, message-id drift is transport
	// metadata and must not turn a valid terminal result into a stale result.
	identityMatches := stillPending
	if !identityMatches {
		if service != nil && service.debug != nil {
			service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "stale_exec_result_ignored", map[string]any{
				"source":        strings.TrimSpace(source),
				"exec_id":       strings.TrimSpace(pending.ExecID),
				"message_id":    pending.MessageID,
				"provider_pass": pending.ProviderPass,
				"current_pass":  currentPass,
				"tool_call_id":  strings.TrimSpace(pending.ToolCallID),
				"reason":        "pending identity no longer active",
			})
		}
		return true
	}
	if currentPass <= 0 || pending.ProviderPass <= 0 || currentPass == pending.ProviderPass {
		return false
	}
	// provider_pass changes when the provider resumes, but it does not change the
	// identity or validity of an exec that is still pending. Keep the watchdog and
	// let the terminal result complete the original exec; pass is diagnostic only.
	if service != nil && service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "late_exec_result_accepted", map[string]any{
			"source":        strings.TrimSpace(source),
			"exec_id":       strings.TrimSpace(pending.ExecID),
			"message_id":    pending.MessageID,
			"provider_pass": pending.ProviderPass,
			"current_pass":  currentPass,
			"tool_call_id":  strings.TrimSpace(pending.ToolCallID),
		})
	}
	return false
}

func recentlyCompletedExecExists(stream *ActiveStream, messageID uint32) bool {
	if stream == nil || messageID == 0 {
		return false
	}
	now := time.Now().UTC()
	cutoff := now.Add(-completedExecRetention)

	stream.mu.Lock()
	defer stream.mu.Unlock()
	if len(stream.RecentCompletedExecs) == 0 {
		return false
	}
	completedAt, ok := stream.RecentCompletedExecs[messageID]
	for id, ts := range stream.RecentCompletedExecs {
		if ts.Before(cutoff) {
			delete(stream.RecentCompletedExecs, id)
		}
	}
	if !ok {
		return false
	}
	if completedAt.Before(cutoff) {
		delete(stream.RecentCompletedExecs, messageID)
		return false
	}
	return true
}
