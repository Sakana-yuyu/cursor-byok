// service_turn.go 承载回合终态、checkpoint 广播与 stream 失败收口。
package forwarder

import (
	"context"
	"crypto/sha256"
	"cursor/gen/agentv1"
	"cursor/internal/apperror"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/logger"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
)

// closeStreamWithProviderError 在真实 LLM/provider 出错时通过 RunSSE 传回错误，并正常结束流。
func (service *Service) closeStreamWithProviderError(
	stream *ActiveStream,
	conversationID string,
	turnSeq int64,
	requestID string,
	accumulatedText string,
	accumulatedReasoning string,
	accumulatedReasoningSignature string,
	accumulatedReasoningSignatureSource string,
	accumulatedReasoningItemID string,
	accumulatedReasoningStatus string,
	accumulatedReasoningSummary json.RawMessage,
	usage turnUsageSnapshot,
	providerErr providerTerminalError,
	allowReasoningOnly bool,
) error {
	if stream == nil {
		return nil
	}
	errorText := strings.TrimSpace(providerErr.Error())
	if errorText == "" {
		errorText = "provider error"
	}
	if strings.TrimSpace(usage.ErrorCode) == "" {
		if code := extractUsageErrorCodeFromCause(providerErr); code != "" {
			usage.ErrorCode = code
		} else if code := extractUsageErrorCode(errorText); code != "" {
			usage.ErrorCode = code
		} else {
			usage.ErrorCode = "provider_error"
		}
	}
	// 已知上游「不支持某能力」类错误（如 grok 多代理变体拒绝 client-side tools）
	// 在原始错误前追加按当前 UI 语言本地化的建议；未命中时保持原样。
	if hint := knownProviderErrorHint(providerErr); hint != "" {
		errorText = hint + "\n\n" + errorText
	}
	modelCallID := strings.TrimSpace(stream.CurrentModelCallID)
	if err := service.flushAssistantText(stream, conversationID, turnSeq, requestID, accumulatedText, accumulatedReasoning, accumulatedReasoningSignature, accumulatedReasoningSignatureSource, accumulatedReasoningItemID, accumulatedReasoningStatus, accumulatedReasoningSummary, allowReasoningOnly); err != nil {
		return fmt.Errorf("flush provider error assistant output: %w", err)
	}
	if err := service.recordTurnUsageSnapshot(stream, conversationID, turnSeq, requestID, modelCallID, "provider_error", usage, errorText, false); err != nil {
		return fmt.Errorf("record provider error usage: %w", err)
	}
	if _, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		newMetadataEntry(turnSeq, requestID, "provider_error", map[string]any{
			"model_call_id": modelCallID,
			"error":         errorText,
		}),
	}); err != nil {
		return err
	}
	if err := service.recordTurnFinalizedSnapshot(stream, conversationID, turnSeq, requestID, "provider_error", errorText); err != nil {
		return fmt.Errorf("record provider error turn finalized: %w", err)
	}
	if err := service.updateConversationTokenState(stream, conversationID, usage, modelCallID, false); err != nil {
		return err
	}
	return service.failActiveStream(stream, conversationID, requestID, modelCallID, "provider_error", errorText)
}

const (
	outputTruncatedTerminalCode = "output_truncated"
	outputTruncatedErrorText    = "模型输出达到 token 上限，已保留部分回复；请继续或缩短请求后重试。"
)

// closeStreamWithOutputTruncation 保留已发送的部分正文，并把输出预算截断作为失败终态落库。
// 已有可见正文时不能自动续写，否则模型可能重复、改写或矛盾此前已经展示的内容。
func (service *Service) closeStreamWithOutputTruncation(
	stream *ActiveStream,
	conversationID string,
	turnSeq int64,
	requestID string,
	usage turnUsageSnapshot,
	finishReason string,
) error {
	if stream == nil {
		return nil
	}
	modelCallID := strings.TrimSpace(stream.CurrentModelCallID)
	if strings.TrimSpace(usage.ErrorCode) == "" {
		usage.ErrorCode = outputTruncatedTerminalCode
	}
	if err := service.recordTurnUsageSnapshot(stream, conversationID, turnSeq, requestID, modelCallID, "provider_error", usage, outputTruncatedErrorText, false); err != nil {
		return fmt.Errorf("record truncated output usage: %w", err)
	}
	if _, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		newMetadataEntry(turnSeq, requestID, outputTruncatedTerminalCode, map[string]any{
			"model_call_id": modelCallID,
			"finish_reason": strings.TrimSpace(finishReason),
		}),
	}); err != nil {
		return err
	}
	if err := service.recordTurnFinalizedSnapshot(stream, conversationID, turnSeq, requestID, outputTruncatedTerminalCode, outputTruncatedErrorText); err != nil {
		return fmt.Errorf("record truncated output turn finalized: %w", err)
	}
	if err := service.updateConversationTokenState(stream, conversationID, usage, modelCallID, false); err != nil {
		return err
	}
	return service.failActiveStreamWithDetails(stream, conversationID, requestID, modelCallID, TerminalFailure{
		Code:         outputTruncatedTerminalCode,
		Message:      outputTruncatedErrorText,
		TraceID:      strings.TrimSpace(requestID),
		AppErrorCode: outputTruncatedTerminalCode,
		Disposition:  "retryable",
	})
}

// closeStreamWithTurnBudgetExceeded 在回合触发 provider pass / 时长硬上限时安全收口。
// 防死循环兜底：正常 agent 回合的工具循环远低于上限；超过说明模型陷入死循环，结束回合并告知用户。
func (service *Service) closeStreamWithTurnBudgetExceeded(stream *ActiveStream, reason string) error {
	if stream == nil {
		return nil
	}
	stream.mu.Lock()
	requestID := stream.RequestID
	conversationID := stream.ConversationID
	turnSeq := stream.TurnSeq
	modelCallID := stream.CurrentModelCallID
	stream.mu.Unlock()
	logger.Infof("forwarder turn budget exceeded request_id=%s conversation_id=%s reason=%q", strings.TrimSpace(requestID), strings.TrimSpace(conversationID), reason)
	if _, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		newMetadataEntry(turnSeq, requestID, "turn_budget_exceeded", map[string]any{
			"reason": reason,
		}),
	}); err != nil {
		return err
	}
	if err := service.recordTurnFinalizedSnapshot(stream, conversationID, turnSeq, requestID, "turn_budget_exceeded", reason); err != nil {
		return err
	}
	return service.failActiveStream(stream, conversationID, requestID, modelCallID, "turn_budget_exceeded", reason)
}

func takePendingProviderCompletion(stream *ActiveStream) (pendingTurnCompletion, bool) {
	if stream == nil {
		return pendingTurnCompletion{}, false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.PendingProviderCompletion == nil {
		return pendingTurnCompletion{}, false
	}
	completion := *stream.PendingProviderCompletion
	stream.PendingProviderCompletion = nil
	stream.UpdatedAt = time.Now().UTC()
	return completion, true
}

func pendingBridgeCount(stream *ActiveStream) int {
	if stream == nil {
		return 0
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return len(stream.PendingExecs) + len(stream.PendingInteractions)
}

func (service *Service) finishDeferredTurnAfterInteraction(stream *ActiveStream, pending runtimecore.PendingInteraction) error {
	completion, ok := takePendingProviderCompletion(stream)
	if !ok {
		stream.mu.Lock()
		completion = pendingTurnCompletion{
			ConversationID: stream.ConversationID,
			RequestID:      stream.RequestID,
			TurnSeq:        stream.TurnSeq,
			ModelCallID:    firstNonEmpty(strings.TrimSpace(pending.ModelCallID), strings.TrimSpace(stream.CurrentModelCallID)),
			ProviderPass:   pending.ProviderPass,
		}
		stream.mu.Unlock()
		logger.Infof(
			"forwarder missing deferred turn completion snapshot request_id=%s tool_call_id=%s interaction_kind=%s provider_pass=%d",
			strings.TrimSpace(completion.RequestID),
			strings.TrimSpace(pending.ToolCallID),
			strings.TrimSpace(pending.InteractionKind),
			pending.ProviderPass,
		)
	}
	if strings.TrimSpace(completion.ModelCallID) == "" {
		completion.ModelCallID = strings.TrimSpace(pending.ModelCallID)
	}
	if completion.ProviderPass == 0 {
		completion.ProviderPass = pending.ProviderPass
	}
	return service.completeSuccessfulTurn(stream, completion)
}

func (service *Service) completeSuccessfulTurn(stream *ActiveStream, completion pendingTurnCompletion) error {
	if stream == nil {
		return nil
	}
	requestID := firstNonEmpty(strings.TrimSpace(completion.RequestID), strings.TrimSpace(stream.RequestID))
	conversationID := firstNonEmpty(strings.TrimSpace(completion.ConversationID), strings.TrimSpace(stream.ConversationID))
	modelCallID := firstNonEmpty(strings.TrimSpace(completion.ModelCallID), strings.TrimSpace(stream.CurrentModelCallID))
	turnSeq := completion.TurnSeq
	if turnSeq <= 0 {
		turnSeq = stream.TurnSeq
	}
	service.clearAllProvider400Recovery(requestID, turnSeq)
	usage := completion.Usage
	if err := service.recordTurnUsageSnapshot(stream, conversationID, turnSeq, requestID, modelCallID, "completed", usage, "", false); err != nil {
		return fmt.Errorf("record completed turn usage: %w", err)
	}
	if _, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		newMetadataEntry(turnSeq, requestID, "turn_completed", map[string]any{
			"model_call_id": modelCallID,
		}),
	}); err != nil {
		return err
	}
	if err := service.recordTurnFinalizedSnapshot(stream, conversationID, turnSeq, requestID, "completed", ""); err != nil {
		return fmt.Errorf("record completed turn finalized: %w", err)
	}
	if err := service.syncSummaryCarryForward(conversationID, requestID, modelCallID); err != nil {
		logger.Errorf(
			"forwarder summary sync after turn completion failed request_id=%s model_call_id=%s err=%v",
			strings.TrimSpace(requestID),
			strings.TrimSpace(modelCallID),
			err,
		)
	}
	if err := service.publishCheckpointWithCompletion(requestID, conversationID, &completion); err != nil {
		return err
	}
	return nil
}

func (service *Service) finishSuccessfulTurnAfterCheckpoint(stream *ActiveStream, completion pendingTurnCompletion) error {
	if stream == nil {
		return nil
	}
	requestID := firstNonEmpty(strings.TrimSpace(completion.RequestID), strings.TrimSpace(stream.RequestID))
	usage := completion.Usage
	if err := service.broker.Publish(requestID, StreamEvent{
		Message: buildTurnEndedMessage(usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens),
	}); err != nil {
		return err
	}
	if err := service.broker.Complete(requestID, "", ""); err != nil {
		return err
	}
	service.setTurnPhase(stream, TurnPhaseCompleted)
	// 当前 turn 终态后，排空该会话因「子代理运行期间」排队的新消息。
	service.drainRunQueue(stream.ConversationID, stream.RequestID)
	return nil
}

func (service *Service) failStreamIfNonTerminal(stream *ActiveStream, terminalCode string, cause error) error {
	if stream == nil || cause == nil {
		return nil
	}
	stream.mu.Lock()
	terminal := isTerminalStreamStatus(stream.Status)
	stream.mu.Unlock()
	if terminal {
		logger.Infof("forwarder fail_stream_if_non_terminal skipped request_id=%s terminal_code=%s cause=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(terminalCode), cause)
		return nil
	}
	logger.Infof("forwarder fail_stream_if_non_terminal firing request_id=%s terminal_code=%s cause=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(terminalCode), cause)
	return service.failStream(stream, terminalCode, cause)
}

// publishCheckpoint projects the in-memory conversation and broadcasts a legacy checkpoint.
// Ordinary snapshots are deduplicated and rate-limited; terminal snapshots bypass that gate
// so completion, cancellation, and delegation state cannot remain stale in Cursor.
func (service *Service) publishCheckpoint(requestID string, conversationID string) error {
	return service.publishCheckpointWithOptions(requestID, conversationID, false)
}

func (service *Service) publishCheckpointWithCompletion(requestID string, conversationID string, completion *pendingTurnCompletion) error {
	return service.publishCheckpointWithTerminalAction(requestID, conversationID, checkpointCompletionAction(completion))
}

func (service *Service) publishCheckpointWithTerminalAction(requestID string, conversationID string, terminalAction checkpointTerminalAction) error {
	return service.publishCheckpointWithOptionsAndAction(requestID, conversationID, terminalAction, true)
}

func (service *Service) publishCheckpointForce(requestID string, conversationID string) error {
	return service.publishCheckpointWithOptions(requestID, conversationID, true)
}

// publishExecCheckpoint keeps ordinary tool completions coalesced while task-like
// executions are flushed immediately so their Cursor status reflects the backend.
func (service *Service) publishExecCheckpoint(stream *ActiveStream, pending runtimecore.PendingExec) error {
	if stream == nil {
		return nil
	}
	execKind := strings.TrimSpace(pending.ExecKind)
	force := execKind == "subagent" || execKind == "delegation_aggregate"
	return service.publishCheckpointWithOptions(stream.RequestID, stream.ConversationID, force)
}

func (service *Service) publishCheckpointWithOptions(requestID string, conversationID string, force bool) error {
	return service.publishCheckpointWithOptionsAndAction(requestID, conversationID, checkpointTerminalAction{kind: checkpointTerminalActionNone}, force)
}

func (service *Service) publishCheckpointWithOptionsAndAction(requestID string, _ string, terminalAction checkpointTerminalAction, force bool) error {
	stream, ok := service.broker.Get(requestID)
	if !ok || stream == nil {
		return fmt.Errorf("request is not active: %s", requestID)
	}
	conversation, pendingExecs, pendingInteractions, err := service.snapshotCheckpointConversation(stream)
	if err != nil {
		return err
	}
	projection, err := service.projector.ProjectCheckpointProjection(conversation)
	if err != nil {
		return err
	}
	if projection == nil || projection.State == nil {
		return fmt.Errorf("checkpoint projection is empty")
	}
	projection.State.PendingToolCalls = buildPendingToolCalls(pendingExecs, pendingInteractions)
	service.rewriteCheckpointTokenDetailsForClient(stream, conversation, projection.State)
	attachDelegationRunStates(stream, projection.State)
	delegationRunCount := len(projection.State.GetSubagentRunsByParentToolCallId())
	if delegationRunCount > 0 {
		activeDelegationRuns := 0
		terminalDelegationRuns := 0
		for _, run := range projection.State.GetSubagentRunsByParentToolCallId() {
			if run == nil {
				continue
			}
			switch run.GetStatus() {
			case agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_RUNNING, agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_BACKGROUNDED:
				activeDelegationRuns++
			default:
				terminalDelegationRuns++
			}
		}
		logger.Infof("forwarder delegation checkpoint publishing request_id=%s conversation_id=%s active_runs=%d terminal_runs=%d pending_execs=%d pending_interactions=%d",
			strings.TrimSpace(requestID), strings.TrimSpace(stream.ConversationID), activeDelegationRuns, terminalDelegationRuns, len(pendingExecs), len(pendingInteractions))
		if service.debug != nil {
			service.debug.LogRuntime(context.Background(), requestID, stream.ConversationID, "delegation_checkpoint_publishing", map[string]any{
				"active_run_count":          activeDelegationRuns,
				"terminal_run_count":        terminalDelegationRuns,
				"pending_exec_count":        len(pendingExecs),
				"pending_interaction_count": len(pendingInteractions),
			})
		}
	}
	if delegationRunCount > 0 {
		for key, run := range projection.State.GetSubagentRunsByParentToolCallId() {
			if run == nil {
				continue
			}
			logger.Infof("forwarder delegation checkpoint run request_id=%s map_key=%s parent_tool_call_id=%s subagent_id=%s status=%s env=%s", strings.TrimSpace(requestID), strings.TrimSpace(key), strings.TrimSpace(run.GetParentToolCallId()), strings.TrimSpace(run.GetSubagentId()), run.GetStatus().String(), run.GetEnvironment().String())
		}
	}
	message := buildCheckpointMessage(projection.State)
	wireSize := proto.Size(message)
	wireBytes, marshalErr := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if marshalErr != nil {
		return fmt.Errorf("marshal checkpoint for dedupe: %w", marshalErr)
	}
	wireHash := fmt.Sprintf("%x", sha256.Sum256(wireBytes))
	now := time.Now().UTC()
	stream.mu.Lock()
	lastHash := stream.LastCheckpointWireHash
	lastSentAt := stream.LastCheckpointSentAt
	if !force && wireHash == lastHash {
		if stream.CheckpointPublishTimer != nil {
			stream.CheckpointPublishTimer.Stop()
			stream.CheckpointPublishTimer = nil
		}
		stream.CheckpointPublishPending = false
		stream.mu.Unlock()
		logger.Infof("forwarder checkpoint skipped request_id=%s reason=duplicate hash=%s wire_size=%d", strings.TrimSpace(requestID), wireHash[:12], wireSize)
		if service.debug != nil {
			service.debug.LogRuntime(context.Background(), requestID, stream.ConversationID, "checkpoint_skipped_duplicate", map[string]any{
				"wire_hash": wireHash, "wire_size": wireSize, "pending_exec_count": len(pendingExecs),
			})
		}
		return nil
	}
	if !force && !lastSentAt.IsZero() && now.Sub(lastSentAt) < checkpointMinSendInterval {
		remaining := checkpointMinSendInterval - now.Sub(lastSentAt)
		if stream.CheckpointPublishTimer == nil {
			stream.CheckpointPublishPending = true
			stream.CheckpointPublishTimer = time.AfterFunc(remaining, func() {
				if delayedStream, delayedOK := service.broker.Get(requestID); delayedOK && delayedStream != nil {
					delayedStream.mu.Lock()
					delayedStream.CheckpointPublishTimer = nil
					delayedStream.CheckpointPublishPending = false
					delayedStream.mu.Unlock()
				}
				if service.debug != nil {
					service.debug.LogRuntime(context.Background(), requestID, "", "checkpoint_delayed_publish_fired", map[string]any{
						"delay": remaining.String(),
					})
				}
				if err := service.publishCheckpoint(requestID, ""); err != nil {
					logger.Errorf("forwarder checkpoint delayed publish failed request_id=%s err=%v", strings.TrimSpace(requestID), err)
				}
			})
		}
		stream.mu.Unlock()
		logger.Infof("forwarder checkpoint skipped request_id=%s reason=rate_limited hash=%s wire_size=%d elapsed=%s min_interval=%s", strings.TrimSpace(requestID), wireHash[:12], wireSize, now.Sub(lastSentAt).Round(time.Millisecond), checkpointMinSendInterval)
		if service.debug != nil {
			service.debug.LogRuntime(context.Background(), requestID, stream.ConversationID, "checkpoint_skipped_rate_limited", map[string]any{
				"wire_hash": wireHash, "wire_size": wireSize, "elapsed_since_last": now.Sub(lastSentAt).String(), "min_interval": checkpointMinSendInterval.String(), "pending_exec_count": len(pendingExecs),
			})
		}
		return nil
	}
	stream.LastCheckpointWireHash = wireHash
	stream.LastCheckpointSentAt = now
	stream.CheckpointPublishPending = false
	if stream.CheckpointPublishTimer != nil {
		stream.CheckpointPublishTimer.Stop()
		stream.CheckpointPublishTimer = nil
	}
	stream.mu.Unlock()
	logger.Infof("forwarder checkpoint queued request_id=%s hash=%s wire_size=%d force=%t pending_execs=%d pending_interactions=%d", strings.TrimSpace(requestID), wireHash[:12], wireSize, force, len(pendingExecs), len(pendingInteractions))
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), requestID, stream.ConversationID, "checkpoint_queued", map[string]any{
			"wire_hash": wireHash, "wire_size": wireSize, "force": force, "pending_exec_count": len(pendingExecs), "pending_interaction_count": len(pendingInteractions),
		})
	}
	return service.queueCheckpointProjection(stream, projection, terminalAction)
}

// flushAssistantText 把本轮累计的 assistant 文本一次性写回 history。
func (service *Service) flushAssistantText(stream *ActiveStream, conversationID string, turnSeq int64, requestID string, text string, reasoningContent string, reasoningSignature string, reasoningSignatureSource string, reasoningItemID string, reasoningStatus string, reasoningSummary json.RawMessage, allowReasoningOnly bool) error {
	if strings.TrimSpace(text) == "" && (!allowReasoningOnly || !hasReplayableReasoningPayload(reasoningContent, reasoningSignature, reasoningSignatureSource)) {
		return nil
	}
	_, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		newAssistantTextEntryWithProviderMetadata(turnSeq, requestID, text, reasoningContent, reasoningSignature, reasoningSignatureSource, reasoningItemID, reasoningStatus, reasoningSummary),
	})
	return err
}

// normalizeStreamFailure 将 stream 边界的任意错误归一化为安全、可追踪的用户错误。
// 具体 provider 错误仍保留在 Cause 中，便于 errors.Is/As 和诊断使用。
func normalizeStreamFailure(stream *ActiveStream, cause error) *apperror.AppError {
	if cause == nil {
		return nil
	}
	traceID := ""
	if stream != nil {
		traceID = strings.TrimSpace(stream.RequestID)
	}
	return apperror.Classify("forwarder.stream", cause, apperror.WithTraceID(traceID))
}

// formatTurnFailureCause 生成单行、限长的失败根因，供 fail_active_stream 旁的
// 根因日志使用——用户文案是脱敏的通用兜底，真实原因必须落到日志里可查。
func formatTurnFailureCause(cause error) string {
	if cause == nil {
		return ""
	}
	text := strings.TrimSpace(cause.Error())
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\t", " ")
	const causeLogLimit = 480
	if len(text) > causeLogLimit {
		text = text[:causeLogLimit] + "...(truncated)"
	}
	return text
}

// failStream 在 provider 或投影失败时把错误写入 history 并收口活动流。
func (service *Service) failStream(stream *ActiveStream, terminalCode string, cause error) error {
	if stream == nil {
		return nil
	}
	normalized := normalizeStreamFailure(stream, cause)
	if normalized == nil {
		return nil
	}
	// 用户侧只看到脱敏通用文案；真实根因必须落日志，否则磁盘/解析类故障无从定位。
	logger.Errorf("forwarder turn failed request_id=%s conversation_id=%s terminal_code=%s app_error_code=%s cause=%s",
		strings.TrimSpace(stream.RequestID),
		strings.TrimSpace(stream.ConversationID),
		resolveTerminalCode(terminalCode, cause),
		string(normalized.Code),
		formatTurnFailureCause(cause))
	errorText := normalized.UserMessage
	resolvedTerminalCode := resolveTerminalCode(terminalCode, cause)
	metadataType := "failed"
	var providerErr providerTerminalError
	if errors.As(cause, &providerErr) || resolvedTerminalCode == "provider_error" {
		metadataType = "provider_error"
	}
	stream.mu.Lock()
	retryAttemptCount := stream.ProviderPassCount
	stream.mu.Unlock()
	appendErr := error(nil)
	if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
		newMetadataEntry(stream.TurnSeq, stream.RequestID, metadataType, map[string]any{
			"error":         errorText,
			"error_code":    string(normalized.Code),
			"disposition":   string(normalized.Disposition),
			"trace_id":      normalized.TraceID,
			"retry_attempt": retryAttemptCount,
		}),
	}); err != nil {
		appendErr = fmt.Errorf("append failure metadata: %w", err)
	}
	terminalErr := service.failActiveStreamWithDetails(
		stream,
		stream.ConversationID,
		stream.RequestID,
		stream.CurrentModelCallID,
		TerminalFailure{
			Code:              resolvedTerminalCode,
			Message:           errorText,
			TraceID:           normalized.TraceID,
			AppErrorCode:      string(normalized.Code),
			Disposition:       string(normalized.Disposition),
			RetryAttemptCount: retryAttemptCount,
		},
	)
	return apperror.Join(appendErr, terminalErr)
}

func resolveTerminalCode(fallback string, cause error) string {
	terminalCode := firstNonEmpty(strings.TrimSpace(fallback), "unknown")
	if cause == nil || terminalCode != "unknown" {
		return terminalCode
	}
	var coded interface{ TerminalCode() string }
	if errors.As(cause, &coded) && strings.TrimSpace(coded.TerminalCode()) != "" {
		return strings.TrimSpace(coded.TerminalCode())
	}
	return terminalCode
}

func (service *Service) failActiveStream(stream *ActiveStream, conversationID string, requestID string, modelCallID string, terminalCode string, terminalMessage string) error {
	return service.failActiveStreamWithDetails(stream, conversationID, requestID, modelCallID, TerminalFailure{
		Code:    terminalCode,
		Message: terminalMessage,
	})
}

func (service *Service) failActiveStreamWithDetails(stream *ActiveStream, conversationID string, requestID string, modelCallID string, failure TerminalFailure) error {
	if stream == nil {
		return nil
	}
	terminalCode := strings.TrimSpace(failure.Code)
	terminalMessage := strings.TrimSpace(failure.Message)
	stream.mu.Lock()
	activePending := len(stream.PendingExecs)
	phase := stream.Phase
	status := stream.Status
	stream.mu.Unlock()
	logger.Infof("forwarder fail_active_stream request_id=%s conversation_id=%s model_call_id=%s terminal_code=%s phase=%s status=%s pending_execs=%d message=%q", strings.TrimSpace(requestID), strings.TrimSpace(conversationID), strings.TrimSpace(modelCallID), strings.TrimSpace(terminalCode), phase, status, activePending, strings.TrimSpace(terminalMessage))
	service.clearAllProvider400Recovery(requestID, stream.TurnSeq)
	clearPendingProviderCompletion(stream)
	stream.mu.Lock()
	cancel := stream.ProviderCancel
	stream.ProviderActive = false
	stream.ProviderCancel = nil
	stream.PendingProviderAction = providerActionNone
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if service.multitaskDelegation != nil {
		service.multitaskDelegation.CancelStream(stream)
	}
	service.setTurnPhase(stream, TurnPhaseFailed)
	var terminalizationErrors []error
	if err := service.syncSummaryCarryForward(conversationID, requestID, modelCallID); err != nil {
		terminalizationErrors = append(terminalizationErrors, fmt.Errorf("sync summary carry forward: %w", err))
	}
	if err := service.publishCheckpointForce(requestID, conversationID); err != nil {
		terminalizationErrors = append(terminalizationErrors, fmt.Errorf("publish terminal checkpoint: %w", err))
	}
	if err := service.broker.FailWithDetails(requestID, failure); err != nil {
		terminalizationErrors = append(terminalizationErrors, fmt.Errorf("fail stream broker: %w", err))
	}
	// 当前 turn 终态后，排空该会话因「子代理运行期间」排队的新消息。
	service.drainRunQueue(conversationID, requestID)
	return errors.Join(terminalizationErrors...)
}

func provider400RecoveryKey(reason provider400RecoveryReason, requestID string, turnSeq int64) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d:%s", requestID, turnSeq, strings.TrimSpace(string(reason)))
}

func (service *Service) claimProvider400Recovery(reason provider400RecoveryReason, requestID string, turnSeq int64) bool {
	if service == nil {
		return false
	}
	key := provider400RecoveryKey(reason, requestID, turnSeq)
	if key == "" {
		return false
	}
	service.provider400RecoveryMu.Lock()
	defer service.provider400RecoveryMu.Unlock()
	if service.provider400RecoveryTurns == nil {
		service.provider400RecoveryTurns = make(map[string]struct{})
	}
	if _, exists := service.provider400RecoveryTurns[key]; exists {
		return false
	}
	if len(service.provider400RecoveryTurns) >= provider400RecoveryMaxEntries {
		service.evictProvider400RecoveryLocked(len(service.provider400RecoveryTurns) - provider400RecoveryMaxEntries + 1)
	}
	service.provider400RecoveryTurns[key] = struct{}{}
	return true
}

func (service *Service) clearProvider400Recovery(reason provider400RecoveryReason, requestID string, turnSeq int64) {
	if service == nil {
		return
	}
	key := provider400RecoveryKey(reason, requestID, turnSeq)
	if key == "" {
		return
	}
	service.provider400RecoveryMu.Lock()
	delete(service.provider400RecoveryTurns, key)
	service.provider400RecoveryMu.Unlock()
}

// clearAllProvider400Recovery 清空指定回合的全部 400 恢复命名空间（content_exists 与
// tool_schema），用于回合收尾（完成/失败）时解除恢复状态。
func (service *Service) clearAllProvider400Recovery(requestID string, turnSeq int64) {
	if service == nil {
		return
	}
	service.clearProvider400Recovery(provider400RecoveryContentExists, requestID, turnSeq)
	service.clearProvider400Recovery(provider400RecoveryToolSchema, requestID, turnSeq)
}
