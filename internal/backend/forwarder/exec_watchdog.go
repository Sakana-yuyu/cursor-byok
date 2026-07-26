package forwarder

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	runtimecore "cursor/internal/backend/agent/core"
)

const (
	// defaultExecTimeout 是非 shell exec 的默认超时（从最后一次活动算起）。
	defaultExecTimeout = 10 * time.Minute
	// execWatchdogTick 是 watchdog 扫描间隔。
	execWatchdogTick = 30 * time.Second
)

const streamTimerExecWatchdog = "exec_watchdog"

// scheduleExecWatchdog 为非 shell、非 subagent 的 pending exec 注册超时监管。
// shell 有自己的 foreground recovery；subagent 由 Cursor 客户端管理生命周期，跳过。
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
	// subagent 可能需要很长时间，使用 30 分钟兜底防止客户端漏发终态时无限等待。
	timeout := defaultExecTimeout
	if kind == "subagent" {
		timeout = 30 * time.Minute
	}
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
	markExecCompleted(stream, pending)
	resultPayload := buildSyntheticExecResultPayload(pending, reason)
	log.Printf(
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
	detail := fmt.Sprintf("[exec watchdog] %s timed out or lost terminal signal (exec_id=%s, reason=%s)", toolName, pending.ExecID, reason)
	summary := map[string]any{
		"status":  "timeout",
		"detail":  detail,
		"reason":  reason,
		"timeout": defaultExecTimeout.String(),
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
