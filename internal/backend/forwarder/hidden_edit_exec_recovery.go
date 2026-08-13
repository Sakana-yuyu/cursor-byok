// hidden_edit_exec_recovery.go 负责 Write / PatchEdit 的隐藏执行桥步骤在没有收到终态时的收口。
//
// 这些步骤（write_read / write_write / write_post_read / write_canvas_diagnostics 及
// patch_edit_* 对应四步）不是独立的工具调用，而是一次 Write/PatchEdit 内部的中间步骤：
//
//   - deriveToolNameFromPendingExec 对它们返回空串。通用超时收口照样以 toolName="" 写
//     tool_result，projector 会因为工具名为空整条丢弃，Write 的 assistant tool_call 随即
//     变成悬空调用被 trimReplayDanglingAssistantToolCalls 连同 reasoning 一起裁掉——
//     模型看不到自己发过这次编辑，也看不到任何失败信号。
//   - pending.ArgsJSON 存的是内部 payload（含 before_content/after_content 全文），
//     直接当 tool_result 的 arguments 会把整份文件内容写进历史，并覆盖 assistant
//     tool_call 的可见参数。
//
// 因此这里不走通用路径，而是复刻 handleHiddenWriteExecControl / handleHiddenPatchEditExecControl
// 的分阶段语义，用真实工具名、可见参数和阶段准确的 EditResult 收口。
package forwarder

import (
	"fmt"
	"strings"

	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/logger"
)

// recoverHiddenEditExecWithoutTerminal 强制收口一个没有收到终态的隐藏编辑步骤。
func (service *Service) recoverHiddenEditExecWithoutTerminal(stream *ActiveStream, pending runtimecore.PendingExec, reason string) error {
	if stream == nil {
		return nil
	}
	markExecCompleted(stream, pending)
	logger.Infof(
		"forwarder hidden edit exec recovery request_id=%s tool_call_id=%s exec_id=%s exec_kind=%s reason=%s",
		strings.TrimSpace(stream.RequestID),
		strings.TrimSpace(pending.ToolCallID),
		strings.TrimSpace(pending.ExecID),
		strings.TrimSpace(pending.ExecKind),
		strings.TrimSpace(reason),
	)
	if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
		newMetadataEntry(stream.TurnSeq, stream.RequestID, "hidden_edit_exec_recovery", map[string]any{
			"tool_call_id": pending.ToolCallID,
			"message_id":   pending.MessageID,
			"exec_id":      pending.ExecID,
			"exec_kind":    pending.ExecKind,
			"reason":       strings.TrimSpace(reason),
		}),
	}); err != nil {
		return err
	}
	if isHiddenWriteExecKind(pending.ExecKind) {
		return service.recoverHiddenWriteExecWithoutTerminal(stream, pending, reason)
	}
	return service.recoverHiddenPatchEditExecWithoutTerminal(stream, pending, reason)
}

func (service *Service) recoverHiddenWriteExecWithoutTerminal(stream *ActiveStream, pending runtimecore.PendingExec, reason string) error {
	payload, err := decodePendingWritePayload(pending.ArgsJSON)
	if err != nil {
		return err
	}
	switch strings.TrimSpace(pending.ExecKind) {
	case writePostReadExecKind:
		// 客户端已经回过 WriteResult_Success，写入确实落盘了；超时的只是事后回读。
		// 与控制通道异常时的处理保持一致，按成功收口。
		return service.finishWriteOperation(stream, pending.ToolCallID, pending.ModelCallID, pending.ProviderPass, pending.ReasoningContent, payload.VisibleArgs,
			buildSuccessfulWriteResult(payload.ResolvedPath, payload.BeforeContent, payload.AfterContent))
	case writeCanvasDiagnosticsExecKind:
		// canvas 的渲染判定依赖 EditResult 为 success，诊断不可用一律降级为「跳过诊断」。
		return service.finishWriteOperationWithCanvasDiagnostics(stream, pending, payload, "")
	case writeWriteExecKind:
		return service.finishWriteOperation(stream, pending.ToolCallID, pending.ModelCallID, pending.ProviderPass, pending.ReasoningContent, payload.VisibleArgs,
			buildEditErrorResult(payload.ResolvedPath, buildHiddenEditUnknownOutcomePayload("Write", payload.ResolvedPath, pending, reason)))
	default:
		return service.finishWriteOperation(stream, pending.ToolCallID, pending.ModelCallID, pending.ProviderPass, pending.ReasoningContent, payload.VisibleArgs,
			buildEditErrorResult(payload.ResolvedPath, buildHiddenEditNotDispatchedPayload("Write", payload.ResolvedPath, pending, reason)))
	}
}

func (service *Service) recoverHiddenPatchEditExecWithoutTerminal(stream *ActiveStream, pending runtimecore.PendingExec, reason string) error {
	payload, err := decodePendingPatchEditPayload(pending.ArgsJSON)
	if err != nil {
		return err
	}
	path := firstNonEmpty(strings.TrimSpace(payload.ResolvedPath), patchEditPayloadPath(payload))
	switch strings.TrimSpace(pending.ExecKind) {
	case patchEditPostReadExecKindName:
		return service.finishPatchEditOperation(stream, pending.ToolCallID, pending.ModelCallID, pending.ProviderPass, pending.ReasoningContent, payload,
			buildFinalEditSuccessResult(path, payload.AfterContent, patchEditPayloadAsEditPayload(payload)))
	case patchEditCanvasDiagnosticsExecKindName:
		return service.finishPatchEditOperationWithCanvasDiagnostics(stream, pending, payload, "")
	case patchEditWriteExecKindName:
		return service.finishPatchEditOperation(stream, pending.ToolCallID, pending.ModelCallID, pending.ProviderPass, pending.ReasoningContent, payload,
			buildEditErrorResult(path, buildHiddenEditUnknownOutcomePayload(patchEditToolName, path, pending, reason)))
	default:
		return service.finishPatchEditOperation(stream, pending.ToolCallID, pending.ModelCallID, pending.ProviderPass, pending.ReasoningContent, payload,
			buildEditErrorResult(path, buildHiddenEditNotDispatchedPayload(patchEditToolName, path, pending, reason)))
	}
}

// buildHiddenEditNotDispatchedPayload 是 pre-read 阶段的降级文案。此时写请求还没发出去，
// 「文件未改动」是可以确定的事实，明说它能省掉模型一次自证清白的回读。
func buildHiddenEditNotDispatchedPayload(toolName string, path string, pending runtimecore.PendingExec, reason string) string {
	return strings.Join([]string{
		fmt.Sprintf("%s failed: the editor client never answered the pre-edit read of %s, so the write was never sent and the file is unchanged.", toolName, strings.TrimSpace(path)),
		"This is an editor/transport failure. It does not mean the edit was rejected, and it does not mean the file is missing.",
		fmt.Sprintf("Retry the %s once. If it fails the same way again, fall back to the Shell tool to inspect and modify the file.", toolName),
		hiddenEditRecoveryTrailer(pending, reason),
	}, "\n")
}

// buildHiddenEditUnknownOutcomePayload 是 write 阶段的降级文案。写请求已经发给客户端、
// 但没有回确认，落盘与否无从判断——这里唯一不能做的就是替模型下结论。
func buildHiddenEditUnknownOutcomePayload(toolName string, path string, pending runtimecore.PendingExec, reason string) string {
	trimmedPath := strings.TrimSpace(path)
	return strings.Join([]string{
		fmt.Sprintf("%s outcome UNKNOWN: the editor client never confirmed the write of %s.", toolName, trimmedPath),
		"The write had already been dispatched to the editor, so the file may or may not have been modified. Do NOT assume it succeeded, and do NOT assume it was skipped.",
		fmt.Sprintf("Use the Read tool on %s to see the current contents, then redo the edit only if the change is missing.", trimmedPath),
		hiddenEditRecoveryTrailer(pending, reason),
	}, "\n")
}

func hiddenEditRecoveryTrailer(pending runtimecore.PendingExec, reason string) string {
	return fmt.Sprintf("[exec recovery] exec_kind=%s exec_id=%s reason=%s",
		strings.TrimSpace(pending.ExecKind), strings.TrimSpace(pending.ExecID), strings.TrimSpace(reason))
}
