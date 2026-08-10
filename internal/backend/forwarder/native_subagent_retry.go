// native_subagent_retry.go 处理 native Cursor 子代理因瞬时上游故障失败时的自动重试。
//
// 背景：父 agent 的 provider 调用失败后模型会在下一 pass 自行重试，而 native 子代理
// 在客户端运行、一次 provider 调用失败即终态失败。实测（会话 4958d320）：上游 relay
// 出现约 15 分钟窗口的 request_timeout / 503 Connection refused，期间多个子代理被
// 一次性判死。本模块在子代理失败上报处拦截，对可判定的瞬时上游错误自动重新派发
// 同一次 Task（上限 nativeSubagentTransientRetryMax 次），使子代理像父 agent 一样
// 扛住瞬时抖动；只有重试耗尽或非瞬时错误才按原样终态失败。
package forwarder

import (
	"context"
	"strings"
	"time"

	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/logger"
)

// nativeSubagentTransientRetryMax 是 native 子代理因瞬时上游故障自动重试的最大次数。
// 每次重试都重新派发同一次 Task；默认 2 次（共最多 3 次尝试）。
const nativeSubagentTransientRetryMax = 2

// isTransientNativeSubagentFailure 判断子代理失败错误文本是否属于可自动重试的瞬时上游故障。
// 仅覆盖上游/中转站瞬时错误（超时、5xx、连接拒绝、流提前断开），不覆盖逻辑错误
// （context_too_large、工具执行失败等——后者重跑无意义或不该自动重复执行）。
func isTransientNativeSubagentFailure(errorText string) bool {
	text := strings.ToLower(strings.TrimSpace(errorText))
	for _, marker := range []string{
		"request_timeout",
		"stream disconnected before completion",
		"stream closed before response.completed",
		"connection refused",
		"upstream connect",
		"stream idle timeout",
		"status=500",
		"status=502",
		"status=503",
		"status=504",
		"status=529",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// maybeAutoRetryNativeSubagent 在子代理瞬时上游失败时重新派发同一次 Task。
// 返回 true 表示已重新派发（调用方不应再写终态失败、不应交付失败 tool_result）；
// 返回 false 表示不重试（非瞬时错误 / 已到上限 / 流已终止 / 重派失败），按原样终态失败。
func (service *Service) maybeAutoRetryNativeSubagent(stream *ActiveStream, pending runtimecore.PendingExec, errorText string) bool {
	if service == nil || stream == nil || strings.TrimSpace(pending.ExecKind) != "subagent" {
		return false
	}
	if !isTransientNativeSubagentFailure(errorText) {
		return false
	}
	if !streamStillActive(stream) {
		return false
	}
	item, ok := service.nativeDelegationTask(pending.ExecID)
	if !ok {
		// 运行态缺失（异常态）：不盲重重派，按原样终态失败。
		return false
	}
	retryCount := item.ProviderRetryCount
	if retryCount >= nativeSubagentTransientRetryMax {
		return false
	}
	// 释放旧 run（槽位与运行态），旧 pending 由调用方 markExecCompleted 移除；这里幂等兜底。
	stream.mu.Lock()
	delete(stream.PendingExecs, strings.TrimSpace(pending.ExecID))
	stream.mu.Unlock()
	service.releaseNativeDelegationQuietly(pending.ExecID)

	invocation := pendingExecAsNativeTaskInvocation(pending)
	if invocation == nil {
		return false
	}
	subagentOverrides := cloneSubagentModelOverrides(stream.SubagentModelOverrides)
	serverMessage, newPending, err := service.openNativeTaskExec(stream, *invocation, subagentOverrides)
	if err != nil {
		logger.Errorf("forwarder native subagent auto-retry open failed request_id=%s tool_call_id=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ToolCallID), err)
		return false
	}
	service.setNativeDelegationRetryCount(newPending.ExecID, retryCount+1)

	if err := service.broker.Publish(stream.RequestID, StreamEvent{Message: serverMessage}); err != nil {
		// 发布失败：清理新 pending 与运行态，避免残留。
		stream.mu.Lock()
		delete(stream.PendingExecs, strings.TrimSpace(newPending.ExecID))
		stream.mu.Unlock()
		service.releaseNativeDelegationQuietly(newPending.ExecID)
		logger.Errorf("forwarder native subagent auto-retry publish failed request_id=%s exec_id=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(newPending.ExecID), err)
		return false
	}
	logger.Infof("forwarder native subagent auto-retry request_id=%s conversation_id=%s tool_call_id=%s old_exec_id=%s new_exec_id=%s attempt=%d/%d reason=%s",
		strings.TrimSpace(stream.RequestID), strings.TrimSpace(stream.ConversationID), strings.TrimSpace(pending.ToolCallID), strings.TrimSpace(pending.ExecID), strings.TrimSpace(newPending.ExecID), retryCount+1, nativeSubagentTransientRetryMax, strings.TrimSpace(errorText))
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "native_subagent_auto_retry", map[string]any{
			"tool_call_id":     strings.TrimSpace(pending.ToolCallID),
			"old_exec_id":      strings.TrimSpace(pending.ExecID),
			"new_exec_id":      strings.TrimSpace(newPending.ExecID),
			"attempt":          retryCount + 1,
			"max_attempts":     nativeSubagentTransientRetryMax,
			"transient_reason": strings.TrimSpace(errorText),
		})
	}
	return true
}

// pendingExecAsNativeTaskInvocation 从已完成的子代理 PendingExec 重建同一次 Task 的
// ToolInvocation，供重试时重新派发。ToolCallID/ArgsJSON/reasoning 字段与原始一致。
func pendingExecAsNativeTaskInvocation(pending runtimecore.PendingExec) *runtimecore.ToolInvocation {
	if strings.TrimSpace(pending.ToolCallID) == "" {
		return nil
	}
	return &runtimecore.ToolInvocation{
		CallID:                   strings.TrimSpace(pending.ToolCallID),
		ToolName:                 "Task",
		ArgsJSON:                 append([]byte(nil), pending.ArgsJSON...),
		ReasoningContent:         pending.ReasoningContent,
		ReasoningSignature:       pending.ReasoningSignature,
		ReasoningSignatureSource: pending.ReasoningSignatureSource,
		ModelCallID:              pending.ModelCallID,
	}
}

// releaseNativeDelegationQuietly 删除 native 子代理运行态并释放其并发槽位，
// 不发布任何终态事件（用于自动重试时静默替换旧 run）。
func (service *Service) releaseNativeDelegationQuietly(execID string) {
	if service == nil {
		return
	}
	service.delegationRuntimeMu.Lock()
	defer service.delegationRuntimeMu.Unlock()
	item := service.nativeDelegations[strings.TrimSpace(execID)]
	if item == nil {
		return
	}
	if item.holdsSlot {
		item.holdsSlot = false
		service.releaseNativeDelegationSlotLocked()
	}
	delete(service.nativeDelegations, strings.TrimSpace(execID))
}

// setNativeDelegationRetryCount 设置 native 子代理运行态的重试计数（重试继承）。
func (service *Service) setNativeDelegationRetryCount(execID string, count int) {
	if service == nil {
		return
	}
	service.delegationRuntimeMu.Lock()
	defer service.delegationRuntimeMu.Unlock()
	item := service.nativeDelegations[strings.TrimSpace(execID)]
	if item == nil {
		return
	}
	item.ProviderRetryCount = count
	item.UpdatedAt = time.Now().UTC()
}
