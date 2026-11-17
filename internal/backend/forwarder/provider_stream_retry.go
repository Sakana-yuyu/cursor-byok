// provider_stream_retry.go 承载 provider 流「已产出后中断 / 输出上限截断」的续写式恢复。
//
// 背景：router 层对已向客户端产出内容的流绝不整体重放（会重复输出/重复工具调用），
// 只打 ErrMidStreamInterrupted 标记返回。若转发层也直接判失败，用户看到的就是
// 「响应在传输过程中中断」，回合被截断。参照 dude1wudv/cursor-byok 的做法：
// 把已产出的正文/思考先落盘，追加一次「从已有输出处继续、不要重复」的 prompt_context，
// 然后退避后续写一轮 provider pass。工具已派发（有副作用）时不恢复。
package forwarder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/logger"
)

const (
	// providerStreamRetryLimit 表示单回合内「中途断流」续写恢复的最大次数。
	providerStreamRetryLimit = 3
	// providerOutputContinuationLimit 表示单回合内「输出上限截断」自动续写的最大次数。
	providerOutputContinuationLimit = 3
)

// providerStreamRetryClassification 判断 provider 错误是否适合续写式恢复。
// 只接管 router 已放弃重放的「已产出后中断」：未产出前的瞬时错误 router 已经
// 重试/切换渠道过，这里再重试只会放大尝试次数。明确的 4xx（408/429 除外）是
// 请求本身或策略问题，续写同样会失败，不接管。
func providerStreamRetryClassification(err error) (string, bool) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, errProviderLoopInterrupted) {
		return "", false
	}
	if !errors.Is(err, modeladapter.ErrMidStreamInterrupted) {
		return "", false
	}
	var statusErr *modeladapter.HTTPStatusError
	if errors.As(err, &statusErr) {
		code := statusErr.StatusCode
		if code >= 400 && code < 500 && code != 408 && code != 429 {
			return "", false
		}
		return fmt.Sprintf("mid_stream_http_%d", code), true
	}
	if modeladapter.IsStreamConnectionReset(err) {
		return "mid_stream_connection", true
	}
	return "mid_stream_interrupted", true
}

// providerStreamRetryDelay 返回第 attempt 次续写恢复前的退避时长。
func providerStreamRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 2 * time.Second
	case 2:
		return 5 * time.Second
	default:
		return 10 * time.Second
	}
}

func providerStreamRecoveryReminderText() string {
	return "The previous provider stream ended before its terminal event. Continue exactly from the existing assistant output without repeating emitted content, and do not repeat or re-execute any action."
}

func providerOutputContinuationReminderText() string {
	return "The previous provider pass reached its output limit. Continue exactly from the prior assistant output, avoid repeating content already emitted, and finish the current task."
}

// providerRecoveryReminderInjected 判断本回合是否已追加过指定来源的续写提示（只追加一次）。
func (service *Service) providerRecoveryReminderInjected(stream *ActiveStream, turnSeq int64, source string) bool {
	conversation, _, _, err := service.snapshotCheckpointConversation(stream)
	if err != nil || conversation == nil {
		return false
	}
	return currentTurnHasPromptContextSource(conversation, turnSeq, source)
}

// scheduleProviderStreamRetry 在 provider 流已产出后中断时，落盘部分输出并排定一次延迟续写。
// 返回 (true, nil) 表示已接管（调用方应直接 return）；(false, nil) 表示不适用，调用方走原有失败收口。
func (service *Service) scheduleProviderStreamRetry(
	stream *ActiveStream,
	providerErr error,
	accumulatedText string,
	accumulatedReasoning string,
	accumulatedReasoningSignature string,
	accumulatedReasoningSignatureSource string,
	accumulatedReasoningItemID string,
	accumulatedReasoningStatus string,
	accumulatedReasoningSummary []byte,
	hasToolSideEffects bool,
) (bool, error) {
	if stream == nil || hasToolSideEffects || pendingBridgeCount(stream) > 0 {
		return false, nil
	}
	retryClass, retryable := providerStreamRetryClassification(providerErr)
	if !retryable {
		return false, nil
	}
	stream.mu.Lock()
	if stream.ProviderStreamRetryCount >= providerStreamRetryLimit {
		stream.mu.Unlock()
		return false, nil
	}
	stream.ProviderStreamRetryCount++
	retryCount := stream.ProviderStreamRetryCount
	requestID := stream.RequestID
	conversationID := stream.ConversationID
	turnSeq := stream.TurnSeq
	modelCallID := stream.CurrentModelCallID
	stream.mu.Unlock()
	retryDelay := providerStreamRetryDelay(retryCount)

	if err := service.flushAssistantText(stream, conversationID, turnSeq, requestID, accumulatedText, accumulatedReasoning, accumulatedReasoningSignature, accumulatedReasoningSignatureSource, accumulatedReasoningItemID, accumulatedReasoningStatus, accumulatedReasoningSummary, true); err != nil {
		return true, err
	}
	hasText := strings.TrimSpace(accumulatedText) != ""
	hasReasoning := strings.TrimSpace(accumulatedReasoning) != ""
	entries := []HistoryEntry{newMetadataEntry(turnSeq, requestID, "provider_stream_retry_scheduled", map[string]any{
		"model_call_id": modelCallID,
		"attempt":       retryCount,
		"limit":         providerStreamRetryLimit,
		"delay_seconds": int(retryDelay.Seconds()),
		"retry_class":   retryClass,
		"error":         providerErr.Error(),
		"has_text":      hasText,
		"has_reasoning": hasReasoning,
	})}
	if (hasText || hasReasoning) && !service.providerRecoveryReminderInjected(stream, turnSeq, promptContextSourceProviderStreamRecovery) {
		entries = append(entries, newPromptContextEntry(turnSeq, requestID, newPromptContextReminder(
			promptContextSourceProviderStreamRecovery,
			providerStreamRecoveryReminderText(),
		)))
	}
	if _, err := service.appendConversationEntries(stream, conversationID, entries); err != nil {
		return true, err
	}
	if err := service.syncSummaryCarryForward(conversationID, requestID, modelCallID); err != nil {
		return true, err
	}
	if err := service.publishCheckpoint(requestID, conversationID); err != nil {
		return true, err
	}
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), requestID, conversationID, "provider_stream_retry_scheduled", map[string]any{
			"attempt":       retryCount,
			"limit":         providerStreamRetryLimit,
			"delay_seconds": int(retryDelay.Seconds()),
			"retry_class":   retryClass,
			"error":         providerErr.Error(),
		})
	}
	logger.Infof("forwarder provider stream retry scheduled request_id=%s attempt=%d/%d delay=%s class=%s err=%v",
		strings.TrimSpace(requestID), retryCount, providerStreamRetryLimit, retryDelay, retryClass, providerErr)

	// 只挂起 resume 意图、不立即 reconcile：由定时器在退避后驱动下一轮 provider pass。
	stream.mu.Lock()
	stream.PendingProviderAction = providerActionResume
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	service.setTurnPhase(stream, TurnPhaseWaitingExternal)
	service.scheduleStreamTimer(stream, providerTimerKey(streamTimerProviderRetry, ""), retryDelay, streamTimerProviderRetry, "", 0, retryClass)
	return true, nil
}

// handleProviderRetryTimer 在退避结束后驱动续写；期间若已被其它事件推进则忽略。
func (service *Service) handleProviderRetryTimer(stream *ActiveStream) error {
	stream.mu.Lock()
	providerActive := stream.ProviderActive
	action := stream.PendingProviderAction
	status := stream.Status
	stream.mu.Unlock()
	if providerActive || isTerminalStreamStatus(status) || action != providerActionResume || pendingBridgeCount(stream) > 0 {
		return nil
	}
	return service.reconcileStream(stream)
}

// scheduleOutputLimitContinuation 在 provider 因输出上限截断、且已向用户输出正文时自动续写。
// 调用前正文已由 flushAssistantText 落盘。返回 (true, nil) 表示已排定续写。
func (service *Service) scheduleOutputLimitContinuation(
	stream *ActiveStream,
	conversationID string,
	turnSeq int64,
	requestID string,
	modelCallID string,
	usage turnUsageSnapshot,
	finishReason string,
) (bool, error) {
	if stream == nil {
		return false, nil
	}
	stream.mu.Lock()
	if stream.ProviderOutputContinuationCount >= providerOutputContinuationLimit {
		stream.mu.Unlock()
		return false, nil
	}
	stream.ProviderOutputContinuationCount++
	attempt := stream.ProviderOutputContinuationCount
	stream.mu.Unlock()

	// 本 pass 的 usage 已在成功收尾路径开头记录过，这里不重复落库。
	entries := []HistoryEntry{newMetadataEntry(turnSeq, requestID, "provider_continuation_scheduled", map[string]any{
		"model_call_id": modelCallID,
		"attempt":       attempt,
		"limit":         providerOutputContinuationLimit,
		"finish_reason": strings.TrimSpace(finishReason),
	})}
	if !service.providerRecoveryReminderInjected(stream, turnSeq, promptContextSourceProviderContinuation) {
		entries = append(entries, newPromptContextEntry(turnSeq, requestID, newPromptContextReminder(
			promptContextSourceProviderContinuation,
			providerOutputContinuationReminderText(),
		)))
	}
	if _, err := service.appendConversationEntries(stream, conversationID, entries); err != nil {
		return true, err
	}
	logger.Infof("forwarder provider output continuation request_id=%s attempt=%d/%d finish_reason=%s",
		strings.TrimSpace(requestID), attempt, providerOutputContinuationLimit, strings.TrimSpace(finishReason))
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
