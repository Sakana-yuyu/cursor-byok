package forwarder

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
	"cursor/internal/logger"
)

const (
	// defaultExecTimeout 是非 shell exec 的默认超时（从最后一次活动算起）。
	defaultExecTimeout = 10 * time.Minute
	// longRunningExecTimeout 是 subagent/delegation aggregate 的最终兜底超时。
	longRunningExecTimeout = 30 * time.Minute
	// execWatchdogTick 是 watchdog 扫描间隔。
	execWatchdogTick = 30 * time.Second
)

const streamTimerExecWatchdog = "exec_watchdog"

// execTimeoutForKind 返回指定执行桥的最终兜底时长。
func execTimeoutForKind(kind string) time.Duration {
	switch strings.TrimSpace(kind) {
	case "subagent", "delegation_aggregate":
		return longRunningExecTimeout
	default:
		return defaultExecTimeout
	}
}

// scheduleExecWatchdog 为非 shell pending exec 注册超时监管。
// shell 有自己的 foreground recovery。
func (service *Service) scheduleExecWatchdog(requestID string, pending runtimecore.PendingExec) {
	if service == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(pending.ExecID) == "" {
		return
	}
	kind := strings.TrimSpace(pending.ExecKind)
	if kind == "shell" {
		return // shell 有自己的 recovery 机制
	}
	stream, ok := service.broker.Get(requestID)
	if !ok || stream == nil {
		return
	}
	deadline := pending.OpenedAt
	if deadline.IsZero() {
		deadline = time.Now().UTC()
	}
	timeout := execTimeoutForKind(kind)
	deadline = deadline.Add(timeout)
	service.scheduleStreamTimer(
		stream,
		providerTimerKey(streamTimerExecWatchdog, pending.ExecID),
		time.Until(deadline),
		streamTimerExecWatchdog,
		pending.ExecID,
		pending.MessageID,
		"exec_watchdog_timeout",
	)
}

// rescheduleExecWatchdog 在收到非终态 exec result 时重置 watchdog，
// 实现"心跳续期"：只要子代理持续有活动（发送 delta/progress），就不会超时。
func (service *Service) rescheduleExecWatchdog(requestID string, pending runtimecore.PendingExec) {
	updated := pending
	updated.OpenedAt = time.Now().UTC()
	service.scheduleExecWatchdog(requestID, updated)
}

// recoverStaleExecWithoutTerminal 在 exec 超时且没有收到终态时强制收口。
func (service *Service) recoverStaleExecWithoutTerminal(stream *ActiveStream, execID string, messageID uint32, reason string) error {
	if stream == nil || strings.TrimSpace(execID) == "" {
		return nil
	}
	current, status, found := snapshotPendingExecWithStatus(stream, execID)
	if !found || current.MessageID != messageID || isTerminalStreamStatus(status) {
		return nil
	}
	if strings.TrimSpace(current.ExecKind) == "shell" {
		return nil // shell 有自己的 recovery
	}
	return service.recoverExecWithoutTerminal(stream, current, reason)
}

// recoverExecWithoutTerminal 强制完成一个没有收到终态的 pending exec。
func (service *Service) recoverExecWithoutTerminal(stream *ActiveStream, pending runtimecore.PendingExec, reason string) error {
	if stream == nil {
		return nil
	}
	// native Cursor 子代理超时收口时，先向客户端执行桥发 abort，真正取消客户端侧
	// 仍在运行的子代理，避免出现「Cursor 任务还在进行中、byok 已显示超时」的割裂。
	// 普通工具（read/write/shell 等）不需要 abort，客户端侧没有对应任务在跑。
	if strings.TrimSpace(pending.ExecKind) == "subagent" {
		if err := service.broker.Publish(stream.RequestID, StreamEvent{Message: buildExecAbortMessage(pending)}); err != nil {
			logger.Errorf("forwarder exec watchdog abort publish failed request_id=%s exec_id=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), err)
		}
	}
	markExecCompleted(stream, pending)
	if strings.TrimSpace(pending.ExecKind) == "subagent" {
		service.updateNativeDelegationStatus(pending.ExecID, delegation.TaskTimedOut, "Cursor 子代理超时", strings.TrimSpace(reason))
	}
	resultPayload := buildSyntheticExecResultPayload(pending, reason)
	logger.Infof(
		"forwarder synthetic exec recovery request_id=%s tool_call_id=%s exec_id=%s exec_kind=%s reason=%s",
		strings.TrimSpace(stream.RequestID),
		strings.TrimSpace(pending.ToolCallID),
		strings.TrimSpace(pending.ExecID),
		strings.TrimSpace(pending.ExecKind),
		strings.TrimSpace(reason),
	)
	toolName := deriveToolNameFromPendingExec(pending)
	if err := service.appendToolResult(stream, pending.ToolCallID, toolName, pending.ArgsJSON, resultPayload, pending.ReasoningContent, nil); err != nil {
		return err
	}
	if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
		newMetadataEntry(stream.TurnSeq, stream.RequestID, "exec_watchdog_recovery", map[string]any{
			"tool_call_id": pending.ToolCallID,
			"message_id":   pending.MessageID,
			"exec_id":      pending.ExecID,
			"exec_kind":    pending.ExecKind,
			"reason":       strings.TrimSpace(reason),
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
	if err := service.publishCheckpoint(stream.RequestID, stream.ConversationID); err != nil {
		return err
	}
	return service.reconcileStream(stream)
}

// buildSyntheticExecResultPayload 构造超时/丢失终态时的 tool_result 文本。
func buildSyntheticExecResultPayload(pending runtimecore.PendingExec, reason string) string {
	toolName := deriveToolNameFromPendingExec(pending)
	timeout := execTimeoutForKind(pending.ExecKind)
	detail := fmt.Sprintf("[exec watchdog] %s timed out or lost terminal signal after %s (exec_id=%s, reason=%s)", toolName, timeout, pending.ExecID, reason)
	summary := map[string]any{
		"status":    "timeout",
		"detail":    detail,
		"reason":    reason,
		"exec_kind": strings.TrimSpace(pending.ExecKind),
		"timeout":   timeout.String(),
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return detail
	}
	return string(encoded)
}

// cleanupAllPendingExecs 在 stream 取消/终止时清理所有 pending exec。
// 对每个 pending exec 发送 abort 消息并从 map 中移除。
func cleanupAllPendingExecs(stream *ActiveStream) []runtimecore.PendingExec {
	if stream == nil {
		return nil
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	pending := make([]runtimecore.PendingExec, 0, len(stream.PendingExecs))
	for execID, item := range stream.PendingExecs {
		pending = append(pending, item)
		delete(stream.PendingExecs, execID)
	}
	stream.UpdatedAt = time.Now().UTC()
	return pending
}
