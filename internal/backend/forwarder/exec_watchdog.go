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
	// gitDiffExecTimeout 是 git_diff 的兜底超时。客户端的 git diff 处理器全程同步：
	// 要么秒级返回结构化 diff，要么在 base ref 解析、merge-base 或响应体积检查处抛错。
	// 抛错路径不保证回包，通用 10 分钟档位会把模型白白钉死十分钟，因此单独收紧。
	gitDiffExecTimeout = 45 * time.Second
	// execWatchdogTick 是 watchdog 扫描间隔。
	execWatchdogTick = 30 * time.Second
	// nativeSubagentExecWatchdogMaxDeferrals 是 native 子代理 exec 看门狗的最大延期次数。
	// 每次延期把 30 分钟超时重置一次；默认 1 次（总上限 60 分钟），足以吸收
	// 「子代理恰在 30 分钟整完成、终态信号与看门狗赛跑」的场景，同时防止客户端
	// 异常但父流活跃时无限挂起。
	nativeSubagentExecWatchdogMaxDeferrals = 1
)

const streamTimerExecWatchdog = "exec_watchdog"

// gitDiffTransportClosedReason 标记由客户端 stream_close 触发的 git_diff 恢复。
// 这条路径不是「等满了兜底时长」，降级文案不能照抄超时措辞。
const gitDiffTransportClosedReason = "transport_closed"

// execTimeoutForKind 返回指定执行桥的最终兜底时长。
func execTimeoutForKind(kind string) time.Duration {
	switch strings.TrimSpace(kind) {
	case "subagent", "delegation_aggregate":
		return longRunningExecTimeout
	case "git_diff":
		return gitDiffExecTimeout
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
	// native Cursor 子代理在客户端运行，byok 只在派发与终态之间做中继，客户端不会持续
	// 回传 exec 心跳；长任务可能在 30 分钟 exec 看门狗处被误杀，即使客户端仍在执行且
	// 终态即将到达。若子代理运行态仍在（客户端尚未回报终态）且延期次数未达上限，先延期
	// 一次而非强杀；达到上限后才按超时收口（见 deferNativeSubagentExecWatchdog）。
	if strings.TrimSpace(current.ExecKind) == "subagent" && service.deferNativeSubagentExecWatchdog(execID) {
		service.rescheduleExecWatchdog(stream.RequestID, current)
		logger.Infof("forwarder exec watchdog deferred request_id=%s exec_id=%s kind=subagent max_deferrals=%d",
			strings.TrimSpace(stream.RequestID), strings.TrimSpace(execID), nativeSubagentExecWatchdogMaxDeferrals)
		return nil
	}
	return service.recoverExecWithoutTerminal(stream, current, reason)
}

// recoverExecWithoutTerminal 强制完成一个没有收到终态的 pending exec。
func (service *Service) recoverExecWithoutTerminal(stream *ActiveStream, pending runtimecore.PendingExec, reason string) error {
	if stream == nil {
		return nil
	}
	// Write/PatchEdit 的隐藏步骤是一次工具调用内部的中间步骤，通用超时收口会写出一条
	// 工具名为空的 tool_result 并让整个编辑调用从模型可见历史里消失。见该文件的注释。
	if isHiddenWriteExecKind(pending.ExecKind) || isHiddenPatchEditExecKind(pending.ExecKind) {
		return service.recoverHiddenEditExecWithoutTerminal(stream, pending, reason)
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
	if strings.TrimSpace(pending.ExecKind) == "git_diff" {
		return buildGitDiffUnavailablePayload(pending, reason, timeout)
	}
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

// buildGitDiffUnavailablePayload 构造 GitDiff 无响应时交还给模型的降级结果。
//
// 这是「工具返回了一个有用的结果」而不是「工具调用失败」：模型拿到的是一条可执行的
// 退路。措辞有两条硬约束：
//   - 明说这是环境没给出 diff，不是仓库没有改动。空 diff 的成功文案是 "git diff empty"，
//     两者必须不可混淆——否则模型会据此断言工作区干净，并把后续结论全建在错误前提上。
//   - 提示写在开头。工具结果整体截断砍掉的是结尾，提示放末尾会连同内容一起消失。
//
// 看门狗超时与客户端 stream_close 共用后三行，只有首行说明成因——共用措辞不等于可以
// 谎报原因：传输被关掉时并没有等满 timeout。
func buildGitDiffUnavailablePayload(pending runtimecore.PendingExec, reason string, timeout time.Duration) string {
	lead := fmt.Sprintf("GitDiff unavailable: the editor client returned no git diff response within %s, so this call carries no diff data at all.", timeout)
	if strings.TrimSpace(reason) == gitDiffTransportClosedReason {
		lead = "GitDiff unavailable: the editor client closed the git diff transport before sending any result, so this call carries no diff data at all."
	}
	return strings.Join([]string{
		lead,
		"This is an environment limitation, NOT a report that there are no changes: the working tree may well be dirty, and this result must not be used to conclude that nothing changed.",
		"Fall back to the Shell tool and run git yourself, e.g. `git status --porcelain` for the changed paths and `git diff` (or `git diff --stat` first when the diff may be large) for the contents. Base every conclusion about what changed on that output, and do not retry GitDiff with the same arguments.",
		fmt.Sprintf("[exec watchdog] exec_id=%s reason=%s", strings.TrimSpace(pending.ExecID), strings.TrimSpace(reason)),
	}, "\n")
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
		// Close completion signal to wake any waiting non-streaming
		// recovery goroutine.
		if stream.ExecCompletionSignals != nil {
			if ch, ok := stream.ExecCompletionSignals[execID]; ok {
				delete(stream.ExecCompletionSignals, execID)
				close(ch)
			}
		}
	}
	stream.UpdatedAt = time.Now().UTC()
	return pending
}
