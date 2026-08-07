// actor_recovery.go 承载回合级恢复域：max_output_tokens 截断恢复与 subagent 空 stop 恢复，
// 含幂等去重辅助（currentTurnHasToolResult / currentTurnHasPromptContextSource）。
package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"cursor/internal/logger"
)

// isMaxOutputTokensTruncation 判断 provider 流式收口原因是否表示被输出预算截断（而非模型主动结束）。
// 与 provider_cache.go 的 isTruncationFinishReason 语义一致，但范围收窄到恢复能获益的截断原因：
// max_output_tokens / length / incomplete。content_filter 是策略拦截而非预算截断，恢复无益，故排除。
func isMaxOutputTokensTruncation(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "max_output_tokens", "length", "incomplete":
		return true
	default:
		return false
	}
}

// handleMaxOutputTokensRecovery 在 provider 流式被输出预算截断、整回合无可见输出时，
// 追加一条 prompt_context 提示消息引导模型重新产出可见回复/工具调用，并续写一轮 provider pass。
// 返回 (true, nil) 表示已挂起恢复（调用方应直接 return）；返回 (false, nil) 表示不适用或本回合已恢复过，
// 调用方走正常收口。镜像 handleSubagentEmptyStopAfterToolResult 的结构与幂等去重。
func (service *Service) handleMaxOutputTokensRecovery(stream *ActiveStream, conversationID string, turnSeq int64, requestID string, modelCallID string, providerPass int, finishReason string) (bool, error) {
	if stream == nil {
		return false, nil
	}
	conversation, _, _, err := service.snapshotCheckpointConversation(stream)
	if err != nil {
		return true, err
	}
	if conversation == nil {
		return false, nil
	}
	// 幂等去重：本回合已追加过该提示则不再恢复，避免无限循环。
	if currentTurnHasPromptContextSource(conversation, turnSeq, promptContextSourceMaxOutputTokensRecovery) {
		return false, nil
	}
	reminder := newPromptContextReminder(promptContextSourceMaxOutputTokensRecovery, maxOutputTokensRecoveryText())
	if _, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		newPromptContextEntry(turnSeq, requestID, reminder),
	}); err != nil {
		return true, err
	}
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), requestID, conversationID, "max_output_tokens_recovery_triggered", map[string]any{
			"provider_pass": providerPass,
			"finish_reason": strings.TrimSpace(finishReason),
		})
	}
	logger.Infof("forwarder max_output_tokens recovery request_id=%s pass=%d finish_reason=%s",
		strings.TrimSpace(requestID), providerPass, strings.TrimSpace(finishReason))
	if err := service.syncSummaryCarryForward(conversationID, requestID, modelCallID); err != nil {
		return true, err
	}
	if err := service.publishCheckpoint(requestID, conversationID); err != nil {
		return true, err
	}
	if err := service.requestProviderAction(stream, providerActionResume); err != nil {
		return true, err
	}
	return true, nil
}

// maxOutputTokensRecoveryText 是 max_output_tokens 截断恢复追加的 prompt_context 提示文案。
// 参考 Reasonix emptyFinalRetryMessage：明确告知模型上一轮被截断、要求给出可见回复而非只输出思考。
func maxOutputTokensRecoveryText() string {
	return "上一轮回复因输出 token 上限被截断（max_output_tokens），只产出了思考过程，没有可见正文或工具调用。请基于本轮任务直接给出简洁的可见回复，或发起必要的工具调用，不要只输出思考内容。"
}

const subagentEmptyStopErrorText = "subagent returned empty response after tool result"

func (service *Service) handleSubagentEmptyStopAfterToolResult(stream *ActiveStream, conversationID string, turnSeq int64, requestID string, modelCallID string, finishReason string, accumulatedText string) (bool, error) {
	if stream == nil || strings.TrimSpace(finishReason) != "stop" || strings.TrimSpace(accumulatedText) != "" {
		return false, nil
	}
	conversation, _, _, err := service.snapshotCheckpointConversation(stream)
	if err != nil {
		return true, err
	}
	if conversation == nil || !isChildConversationSubagentTypeName(conversation.SubagentTypeName) || !currentTurnHasToolResult(conversation, turnSeq) {
		return false, nil
	}
	if currentTurnHasPromptContextSource(conversation, turnSeq, promptContextSourceSubagentEmptyStopRecovery) {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return true, service.failStream(stream, "empty_response", errors.New(subagentEmptyStopErrorText))
	}
	context := newPromptContextReminder(promptContextSourceSubagentEmptyStopRecovery, subagentEmptyStopRecoveryText())
	if _, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		newPromptContextEntry(turnSeq, requestID, context),
	}); err != nil {
		return true, err
	}
	if err := service.syncSummaryCarryForward(conversationID, requestID, modelCallID); err != nil {
		return true, err
	}
	if err := service.publishCheckpoint(requestID, conversationID); err != nil {
		return true, err
	}
	if err := service.requestProviderAction(stream, providerActionResume); err != nil {
		return true, err
	}
	return true, nil
}

func subagentEmptyStopRecoveryText() string {
	return "During this subagent turn, a prior provider pass stopped after tool results without visible assistant output. Continue from the latest tool result and return a concise investigation result for the parent. Only call another allowed read-only tool if necessary."
}

func currentTurnHasToolResult(conversation *ConversationFile, turnSeq int64) bool {
	if conversation == nil || turnSeq <= 0 {
		return false
	}
	for _, entry := range conversation.Entries {
		if entry.TurnSeq == turnSeq && strings.TrimSpace(entry.Kind) == "tool_result" {
			return true
		}
	}
	return false
}

func currentTurnHasPromptContextSource(conversation *ConversationFile, turnSeq int64, source string) bool {
	if conversation == nil || turnSeq <= 0 || strings.TrimSpace(source) == "" {
		return false
	}
	for _, entry := range conversation.Entries {
		if entry.TurnSeq != turnSeq || strings.TrimSpace(entry.Kind) != "prompt_context" {
			continue
		}
		var payload promptContextEntryPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			continue
		}
		if strings.TrimSpace(payload.Source) == strings.TrimSpace(source) {
			return true
		}
	}
	return false
}

func hasPendingAwaitingUserInteraction(stream *ActiveStream) bool {
	if stream == nil {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	for _, pending := range stream.PendingInteractions {
		if !shouldAutoResumeAfterInteraction(pending) {
			return true
		}
	}
	return false
}
