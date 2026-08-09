package forwarder

import (
	"bytes"
	"context"
	"cursor/internal/logger"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
)

type TurnPhase string

const (
	TurnPhaseIdle            TurnPhase = "idle"
	TurnPhaseProviderRunning TurnPhase = "provider_running"
	TurnPhaseWaitingExternal TurnPhase = "waiting_external"
	TurnPhaseAwaitingUser    TurnPhase = "awaiting_user"
	TurnPhaseCompacting      TurnPhase = "compacting"
	TurnPhaseCheckpointing   TurnPhase = "checkpointing"
	TurnPhaseCompleted       TurnPhase = "completed"
	TurnPhaseFailed          TurnPhase = "failed"
	TurnPhaseCanceled        TurnPhase = "canceled"
)

type providerAction string

const (
	providerActionNone   providerAction = ""
	providerActionStart  providerAction = "start"
	providerActionResume providerAction = "resume"
)

type pendingCompletionDisposition string

const (
	completionDispositionNone                  pendingCompletionDisposition = ""
	completionDispositionResumeAfterExternal   pendingCompletionDisposition = "resume_after_external"
	completionDispositionCompleteAfterExternal pendingCompletionDisposition = "complete_after_external"
)

type streamCommandKind string

const (
	streamCommandRun               streamCommandKind = "run"
	streamCommandCancel            streamCommandKind = "cancel"
	streamCommandMetadata          streamCommandKind = "metadata"
	streamCommandExecResult        streamCommandKind = "exec_result"
	streamCommandExecControl       streamCommandKind = "exec_control"
	streamCommandInteractionResult streamCommandKind = "interaction_result"
	streamCommandProviderEvent     streamCommandKind = "provider_event"
	streamCommandTimerFired        streamCommandKind = "timer_fired"
	streamCommandCompactionEvent   streamCommandKind = "compaction_event"
	streamCommandDelegationResult  streamCommandKind = "delegation_result"
	streamCommandMaybeOrphaned     streamCommandKind = "maybe_orphaned"
)

type streamTimerKind string

const (
	streamTimerProviderResume       streamTimerKind = "provider_resume"
	streamTimerNonStreamingRecovery streamTimerKind = "non_streaming_recovery"
	streamTimerInteractionTimeout   streamTimerKind = "interaction_timeout"
	streamTimerShellForeground      streamTimerKind = "shell_foreground"
	streamTimerShellTransportClose  streamTimerKind = "shell_transport_close"
	streamTimerCheckpointBlobs      streamTimerKind = "checkpoint_blobs"
	streamTimerOrphanCancel         streamTimerKind = "orphan_cancel"
	streamTimerTurnStale            streamTimerKind = "turn_stale"
)

type streamProviderEvent struct {
	Token uint64
	Event modeladapter.ModelEvent
	Done  bool
	Err   error
}

type streamTimerEvent struct {
	Key       string
	Kind      streamTimerKind
	Token     uint64
	ExecID    string
	MessageID uint32
	Reason    string
}

type streamCompactionEvent struct {
	Token       uint64
	Plan        *PendingCompaction
	SummaryText string
	Err         error
}

type streamCommand struct {
	Kind       streamCommandKind
	Intent     InboundIntent
	Provider   *streamProviderEvent
	Timer      *streamTimerEvent
	Compaction *streamCompactionEvent
	Delegation *streamDelegationResult
	Reason     string
}

type streamCommandEnvelope struct {
	command streamCommand
	result  chan error
}

func commandKindForIntent(intent InboundIntent) (streamCommandKind, error) {
	switch strings.TrimSpace(intent.Kind) {
	case "run":
		return streamCommandRun, nil
	case "cancel":
		return streamCommandCancel, nil
	case "metadata", "kv_result":
		return streamCommandMetadata, nil
	case "exec_result":
		return streamCommandExecResult, nil
	case "exec_control":
		return streamCommandExecControl, nil
	case "interaction_result":
		return streamCommandInteractionResult, nil
	default:
		return "", fmt.Errorf("unsupported inbound intent: %s", intent.Kind)
	}
}

func (service *Service) dispatchInboundIntent(intent InboundIntent) error {
	if service == nil {
		return fmt.Errorf("forwarder service is nil")
	}
	if strings.TrimSpace(intent.Kind) == "run" {
		result, ownerRequestID, queuePosition := service.runQueue.Submit(intent)
		switch result {
		case runQueueQueued:
			logger.Infof("forwarder conversation run queued request_id=%s conversation_id=%s owner_request_id=%s queue_len=%d queue_position=%d",
				strings.TrimSpace(intent.RequestID), strings.TrimSpace(intent.ConversationID), ownerRequestID,
				service.runQueue.Len(intent.ConversationID), queuePosition)
			return nil
		case runQueueDuplicate:
			logger.Infof("forwarder duplicate conversation run ignored request_id=%s conversation_id=%s owner_request_id=%s",
				strings.TrimSpace(intent.RequestID), strings.TrimSpace(intent.ConversationID), ownerRequestID)
			return nil
		case runQueueStart:
			return service.startAdmittedRun(intent)
		}
	}
	stream, err := service.streamForIntent(intent)
	if err != nil {
		return err
	}
	if stream == nil {
		return nil
	}
	commandKind, err := commandKindForIntent(intent)
	if err != nil {
		return err
	}
	// exec_result / interaction_result 必须异步：并行工具结果会在 stream actor 中串行处理，
	// mailbox FIFO 保证命令仍按序处理，同时避免 BidiAppend 等待前序工具结果。
	if commandKind == streamCommandExecResult || commandKind == streamCommandInteractionResult {
		return service.postStreamCommandAsync(stream, streamCommand{
			Kind:   commandKind,
			Intent: intent,
		})
	}
	return service.postStreamCommandWait(stream, streamCommand{
		Kind:   commandKind,
		Intent: intent,
	})
}

func (service *Service) startOwnedRun(intent InboundIntent) error {
	if service.startOwnedRunHook != nil {
		return service.startOwnedRunHook(intent)
	}
	stream, err := service.streamForIntent(intent)
	if err != nil {
		return err
	}
	if stream == nil {
		return nil
	}
	return service.postStreamCommandAsync(stream, streamCommand{
		Kind:   streamCommandRun,
		Intent: intent,
	})
}

func (service *Service) streamForIntent(intent InboundIntent) (*ActiveStream, error) {
	switch strings.TrimSpace(intent.Kind) {
	case "run":
		if intent.ForceNewTurn {
			if existing, ok := service.broker.Get(intent.RequestID); ok && existing != nil {
				if err := service.reopenTerminalStreamForNewTurn(existing); err != nil {
					return nil, err
				}
				return existing, nil
			}
		}
		stream, err := service.broker.OpenStream(
			intent.RequestID,
			intent.ConversationID,
			0,
			intent.ModelID,
			intent.ModelName,
			intent.Mode,
			userMessageText(intent.UserMessage),
		)
		if err != nil {
			return nil, err
		}
		if stream == nil {
			return nil, fmt.Errorf("open stream failed")
		}
		return stream, nil
	case "metadata", "kv_result":
		stream, ok := service.broker.Get(intent.RequestID)
		if !ok || stream == nil {
			if intent.HasExplicitMode || intent.StartsRun {
				return nil, fmt.Errorf("metadata intent requires active request context: %s", intent.RequestID)
			}
			return nil, nil
		}
		if isTerminalIntentStream(stream) {
			return nil, nil
		}
		return stream, nil
	default:
		stream, ok := service.broker.Get(intent.RequestID)
		if !ok || stream == nil {
			return nil, fmt.Errorf("request is not active: %s", intent.RequestID)
		}
		if isTerminalIntentStream(stream) {
			return nil, nil
		}
		return stream, nil
	}
}

func (service *Service) reopenTerminalStreamForNewTurn(stream *ActiveStream) error {
	if stream == nil {
		return nil
	}
	stream.mu.Lock()
	terminal := isTerminalStreamStatus(stream.Status)
	switch stream.Phase {
	case TurnPhaseCanceled, TurnPhaseCompleted, TurnPhaseFailed:
		terminal = true
	}
	actorDone := stream.ActorDone
	stream.mu.Unlock()
	if !terminal {
		return nil
	}
	if actorDone != nil {
		select {
		case <-actorDone:
		case <-time.After(2 * time.Second):
			return fmt.Errorf("previous request actor did not stop before new conversation action")
		}
	}

	stream.mu.Lock()
	if stream.ProviderCancel != nil {
		stream.ProviderCancel()
		stream.ProviderCancel = nil
	}
	stopAllStreamTimersLocked(stream)
	if service != nil && service.broker != nil {
		service.broker.stopTerminalCleanupTimerLocked(stream)
	}
	stream.ProviderActive = false
	stream.CurrentProviderToken++
	stream.CurrentCompactionToken++
	stream.PendingProviderAction = providerActionNone
	stream.PendingProviderCompletion = nil
	stream.PendingCompaction = nil
	stream.Status = StreamStatusCreated
	stream.Phase = TurnPhaseIdle
	stream.ActorMailbox = nil
	stream.ActorDone = nil
	stream.BacklogStartCursor = len(stream.Backlog)
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	return nil
}

func (service *Service) prepareStreamForForcedTurn(intent InboundIntent) error {
	if service == nil || service.broker == nil || !intent.ForceNewTurn {
		return nil
	}
	stream, ok := service.broker.Get(intent.RequestID)
	if !ok || stream == nil {
		return nil
	}
	if service.multitaskDelegation != nil {
		service.multitaskDelegation.CancelStream(stream)
	}

	stream.mu.Lock()
	if stream.ProviderCancel != nil {
		stream.ProviderCancel()
		stream.ProviderCancel = nil
	}
	stream.ProviderActive = false
	stream.CurrentProviderToken++
	stream.CurrentCompactionToken++
	stopAllStreamTimersLocked(stream)
	stream.PendingProviderAction = providerActionNone
	stream.PendingProviderCompletion = nil
	stream.PendingCompaction = nil
	stream.PendingInteractions = make(map[string]runtimecore.PendingInteraction)
	stream.PartialToolCallIDs = make(map[string]struct{})
	stream.PatchEditQueues = make(map[string][]queuedPatchEditOperation)
	stream.Status = StreamStatusCreated
	stream.Phase = TurnPhaseIdle
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()

	for _, pending := range cleanupAllPendingExecs(stream) {
		if strings.TrimSpace(pending.ExecKind) == "delegation_aggregate" {
			continue
		}
		_ = service.broker.Publish(intent.RequestID, StreamEvent{
			Message: buildExecAbortMessage(pending),
		})
	}
	stream.mu.Lock()
	stream.BacklogStartCursor = len(stream.Backlog)
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	return nil
}

func isTerminalIntentStream(stream *ActiveStream) bool {
	if stream == nil {
		return true
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if isTerminalStreamStatus(stream.Status) {
		return true
	}
	switch stream.Phase {
	case TurnPhaseCanceled, TurnPhaseCompleted, TurnPhaseFailed:
		return true
	default:
		return false
	}
}

func (service *Service) ensureStreamActor(stream *ActiveStream) (chan streamCommandEnvelope, chan struct{}, error) {
	if stream == nil {
		return nil, nil, fmt.Errorf("active stream is required")
	}
	stream.mu.Lock()
	if stream.ActorMailbox != nil && stream.ActorDone != nil {
		mailbox := stream.ActorMailbox
		done := stream.ActorDone
		stream.mu.Unlock()
		return mailbox, done, nil
	}
	mailbox := make(chan streamCommandEnvelope, 128)
	done := make(chan struct{})
	stream.ActorMailbox = mailbox
	stream.ActorDone = done
	if stream.TimerTokens == nil {
		stream.TimerTokens = make(map[string]uint64)
	}
	if stream.StreamTimers == nil {
		stream.StreamTimers = make(map[string]*time.Timer)
	}
	if strings.TrimSpace(string(stream.Phase)) == "" {
		stream.Phase = TurnPhaseIdle
	}
	stream.mu.Unlock()
	go service.runStreamActor(stream, mailbox, done)
	return mailbox, done, nil
}

// streamCommandResultPool 复用 postStreamCommandWait 每事件创建的 result channel，
// 消除高频 provider 事件（text/thinking delta）下每次 make(chan error, 1) 的堆分配。
// channel 容量固定为 1，归还前必须 drain，避免 done 分支中 actor 已写入的残留值污染复用。
var streamCommandResultPool = sync.Pool{
	New: func() any {
		return make(chan error, 1)
	},
}

func (service *Service) postStreamCommandWait(stream *ActiveStream, command streamCommand) error {
	if stream == nil {
		return nil
	}
	mailbox, done, err := service.ensureStreamActor(stream)
	if err != nil {
		return err
	}
	result := streamCommandResultPool.Get().(chan error)
	// 防御性 drain：正常归还前已清空，此处兜底保证复用 channel 干净。
	select {
	case <-result:
	default:
	}
	envelope := streamCommandEnvelope{
		command: command,
		result:  result,
	}
	select {
	case <-done:
		streamCommandResultPool.Put(result)
		return errProviderLoopInterrupted
	case mailbox <- envelope:
	}
	var commandErr error
	select {
	case <-done:
		commandErr = errProviderLoopInterrupted
	case commandErr = <-result:
	}
	// drain 残留：done 分支可能恰好发生在 actor 已写入 result 之后。
	select {
	case <-result:
	default:
	}
	streamCommandResultPool.Put(result)
	return commandErr
}

func (service *Service) postStreamCommandAsync(stream *ActiveStream, command streamCommand) error {
	if stream == nil {
		return nil
	}
	mailbox, done, err := service.ensureStreamActor(stream)
	if err != nil {
		return err
	}
	if command.Kind == streamCommandDelegationResult || command.Kind == streamCommandCancel {
		logger.Infof("forwarder stream command enqueue request_id=%s kind=%s exec_id=%s tool_call_id=%s reason=%s", strings.TrimSpace(stream.RequestID), command.Kind, delegationExecID(command), delegationToolCallID(command), strings.TrimSpace(command.Reason))
	}
	envelope := streamCommandEnvelope{command: command}
	select {
	case <-done:
		logger.Errorf("forwarder stream command enqueue rejected request_id=%s kind=%s reason=actor_done", strings.TrimSpace(stream.RequestID), command.Kind)
		return errProviderLoopInterrupted
	case mailbox <- envelope:
		return nil
	}
}

func delegationExecID(command streamCommand) string {
	if command.Delegation == nil {
		return ""
	}
	return strings.TrimSpace(command.Delegation.ExecID)
}

func delegationToolCallID(command streamCommand) string {
	if command.Delegation == nil {
		return ""
	}
	return strings.TrimSpace(command.Delegation.ToolCallID)
}

func (service *Service) runStreamActor(stream *ActiveStream, mailbox <-chan streamCommandEnvelope, done chan struct{}) {
	defer close(done)
	for {
		envelope, ok := <-mailbox
		if !ok {
			return
		}
		err := service.handleStreamCommand(stream, envelope.command)
		if envelope.result != nil {
			envelope.result <- err
		} else if err != nil {
			_ = service.failStream(stream, "unknown", err)
		}
		if shouldStopStreamActor(stream) {
			return
		}
	}
}

func shouldStopStreamActor(stream *ActiveStream) bool {
	if stream == nil {
		return true
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if isTerminalStreamStatus(stream.Status) {
		return true
	}
	switch stream.Phase {
	case TurnPhaseCompleted, TurnPhaseFailed, TurnPhaseCanceled:
		return true
	default:
		return false
	}
}

func (service *Service) handleStreamCommand(stream *ActiveStream, command streamCommand) error {
	switch command.Kind {
	case streamCommandRun:
		return service.handleRunIntent(command.Intent)
	case streamCommandCancel:
		return service.handleCancelIntent(command.Intent)
	case streamCommandMetadata:
		if strings.TrimSpace(command.Intent.Kind) == "kv_result" {
			return service.handleCheckpointBlobResult(stream, command.Intent.KVClientMessage)
		}
		return service.handleMetadataIntent(command.Intent)
	case streamCommandExecResult:
		return service.handleExecResult(command.Intent)
	case streamCommandExecControl:
		return service.handleExecControl(command.Intent)
	case streamCommandInteractionResult:
		return service.handleInteractionResult(command.Intent)
	case streamCommandProviderEvent:
		return service.handleProviderEvent(stream, command.Provider)
	case streamCommandTimerFired:
		return service.handleTimerEvent(stream, command.Timer)
	case streamCommandCompactionEvent:
		return service.handleCompactionEvent(stream, command.Compaction)
	case streamCommandDelegationResult:
		return service.handleDelegationResult(stream, command.Delegation)
	case streamCommandMaybeOrphaned:
		if stream == nil {
			return nil
		}
		stream.mu.Lock()
		subscriberCount := len(stream.Subscribers)
		status := stream.Status
		stream.mu.Unlock()
		if subscriberCount > 0 || isTerminalStreamStatus(status) {
			return nil
		}
		service.scheduleStreamTimer(stream, providerTimerKey(streamTimerOrphanCancel, ""), orphanSubscriberGracePeriod, streamTimerOrphanCancel, "", 0, command.Reason)
		return nil
	default:
		return fmt.Errorf("unsupported stream command kind: %s", command.Kind)
	}
}

func (service *Service) requestProviderAction(stream *ActiveStream, action providerAction) error {
	if stream == nil {
		return nil
	}
	stream.mu.Lock()
	switch action {
	case providerActionStart:
		stream.PendingProviderAction = providerActionStart
	case providerActionResume:
		if stream.PendingProviderAction != providerActionStart {
			stream.PendingProviderAction = providerActionResume
		}
	default:
		stream.PendingProviderAction = providerActionNone
	}
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	return service.reconcileStream(stream)
}

func (service *Service) reconcileStream(stream *ActiveStream) error {
	if stream == nil {
		return nil
	}

	stream.mu.Lock()
	if isTerminalStreamStatus(stream.Status) {
		stream.mu.Unlock()
		return nil
	}
	providerActive := stream.ProviderActive
	pendingExecCount := len(stream.PendingExecs)
	pendingInteractionCount := len(stream.PendingInteractions)
	hasPendingCompaction := stream.PendingCompaction != nil
	action := stream.PendingProviderAction
	completion := stream.PendingProviderCompletion
	stream.mu.Unlock()

	if providerActive {
		return nil
	}
	if pendingExecCount+pendingInteractionCount > 0 {
		if hasPendingAwaitingUserInteraction(stream) {
			service.setTurnPhase(stream, TurnPhaseAwaitingUser)
		} else if hasPendingCompaction {
			service.setTurnPhase(stream, TurnPhaseCompacting)
		} else {
			service.setTurnPhase(stream, TurnPhaseWaitingExternal)
		}
		return nil
	}
	if hasPendingCompaction {
		service.setTurnPhase(stream, TurnPhaseCompacting)
		return nil
	}

	if completion != nil {
		if completion.Disposition == completionDispositionResumeAfterExternal {
			stream.mu.Lock()
			stream.PendingProviderCompletion = nil
			if stream.PendingProviderAction != providerActionStart {
				stream.PendingProviderAction = providerActionResume
			}
			stream.UpdatedAt = time.Now().UTC()
			stream.mu.Unlock()
			action = providerActionResume
		} else {
			clearPendingProviderCompletion(stream)
			if err := service.completeSuccessfulTurn(stream, *completion); err != nil {
				return service.failStreamIfNonTerminal(stream, "unknown", err)
			}
			return nil
		}
	}

	switch action {
	case providerActionStart:
		return service.driveProvider(stream)
	case providerActionResume:
		service.setTurnPhase(stream, TurnPhaseWaitingExternal)
		service.scheduleStreamTimer(stream, providerTimerKey(streamTimerProviderResume, ""), providerResumeDebounce, streamTimerProviderResume, "", 0, "")
		return nil
	default:
		service.setTurnPhase(stream, TurnPhaseIdle)
		return nil
	}
}

func (service *Service) handleProviderEvent(stream *ActiveStream, payload *streamProviderEvent) error {
	if stream == nil || payload == nil {
		return nil
	}
	if !providerTokenMatches(stream, payload.Token) {
		return nil
	}
	if payload.Done {
		return service.handleProviderDoneEvent(stream, payload)
	}
	return service.applyProviderModelEvent(stream, payload.Event)
}

func providerTokenMatches(stream *ActiveStream, token uint64) bool {
	if stream == nil || token == 0 {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.CurrentProviderToken == token
}

func (service *Service) applyProviderModelEvent(stream *ActiveStream, event modeladapter.ModelEvent) error {
	if stream == nil {
		return nil
	}
	stream.mu.Lock()
	requestID := stream.RequestID
	conversationID := stream.ConversationID
	turnSeq := stream.TurnSeq
	modelCallID := stream.CurrentModelCallID
	accumulatedReasoningSignature := stream.ProviderAccumulatedReasoningSignature
	accumulatedReasoningSignatureSource := stream.ProviderAccumulatedReasoningSignatureSource
	accumulatedReasoningItemID := stream.ProviderAccumulatedReasoningItemID
	accumulatedReasoningStatus := stream.ProviderAccumulatedReasoningStatus
	accumulatedReasoningSummary := append([]byte(nil), stream.ProviderAccumulatedReasoningSummary...)
	stream.mu.Unlock()

	switch event.Kind {
	case modeladapter.ModelEventKindTextDelta:
		stream.mu.Lock()
		stream.ProviderAccumulatedText = append(stream.ProviderAccumulatedText, event.Text...)
		stream.UpdatedAt = time.Now().UTC()
		stream.mu.Unlock()
		service.markConversationActivity(conversationID)
		return service.broker.Publish(requestID, StreamEvent{Message: buildTextDeltaMessage(event.Text)})
	case modeladapter.ModelEventKindThinkingDelta:
		stream.mu.Lock()
		stream.ProviderAccumulatedReasoning = append(stream.ProviderAccumulatedReasoning, event.Text...)
		stream.ProviderThinkingDeltaCount++
		deltaCount := stream.ProviderThinkingDeltaCount
		accumulatedLength := len(stream.ProviderAccumulatedReasoning)
		stream.UpdatedAt = time.Now().UTC()
		stream.mu.Unlock()
		service.markConversationActivity(conversationID)
		logger.Infof("forwarder thinking delta request_id=%s conversation_id=%s model_call_id=%s provider_pass=%d delta_count=%d accumulated_bytes=%d", strings.TrimSpace(requestID), strings.TrimSpace(conversationID), strings.TrimSpace(modelCallID), currentProviderPass(stream), deltaCount, accumulatedLength)
		if service.debug != nil {
			service.debug.LogRuntime(context.Background(), requestID, conversationID, "thinking_delta_forwarded", map[string]any{
				"model_call_id":     strings.TrimSpace(modelCallID),
				"provider_pass":     currentProviderPass(stream),
				"delta_count":       deltaCount,
				"accumulated_bytes": accumulatedLength,
			})
		}
		return service.broker.Publish(requestID, StreamEvent{Message: buildThinkingDeltaMessage(event.Text, event.ThinkingStyle)})
	case modeladapter.ModelEventKindThinkingCompleted:
		shouldEmitSyntheticThinking := false
		encryptedOnlyThinking := false
		suppressThinkingCompleted := false
		completedDuration := event.ThinkingDurationMS
		stream.mu.Lock()
		stream.ProviderThinkingCompletedCount++
		completedCount := stream.ProviderThinkingCompletedCount
		stream.UpdatedAt = time.Now().UTC()
		stream.mu.Unlock()
		if strings.TrimSpace(event.ThinkingSignature) != "" {
			stream.mu.Lock()
			stream.ProviderAccumulatedReasoningSignature = strings.TrimSpace(event.ThinkingSignature)
			stream.ProviderAccumulatedReasoningSignatureSource = strings.TrimSpace(event.ThinkingSignatureSource)
			stream.ProviderAccumulatedReasoningItemID = strings.TrimSpace(event.ProviderItemID)
			stream.ProviderAccumulatedReasoningStatus = strings.TrimSpace(event.ProviderStatus)
			stream.ProviderAccumulatedReasoningSummary = append([]byte(nil), event.ProviderSummary...)
			shouldEmitSyntheticThinking = len(bytes.TrimSpace(stream.ProviderAccumulatedReasoning)) == 0 &&
				strings.TrimSpace(event.ThinkingSignatureSource) == modeladapter.ReasoningSignatureSourceOpenAIResponses
			if shouldEmitSyntheticThinking {
				encryptedOnlyThinking = true
				if stream.ProviderSyntheticThinkingStartedAt.IsZero() {
					stream.ProviderSyntheticThinkingStartedAt = time.Now().UTC()
				}
				if completedDuration <= 0 {
					completedDuration = int32(time.Since(stream.ProviderSyntheticThinkingStartedAt).Milliseconds())
					if completedDuration <= 0 {
						completedDuration = 1
					}
				}
				// OpenAI Responses 的加密签名是 replay 元数据，不是可读 thinking。
				// 首次遇到时发布一条合成占位文本，让 Cursor 客户端有 thinking 指示
				// （与上游 fork 体验对齐）；后续 encrypted-only 事件则抑制，避免重复噪音。
				if !stream.ProviderSyntheticThinkingPublished {
					stream.ProviderSyntheticThinkingPublished = true
				} else {
					shouldEmitSyntheticThinking = false
					suppressThinkingCompleted = true
				}
			}
			stream.UpdatedAt = time.Now().UTC()
			stream.mu.Unlock()
		}
		if shouldEmitSyntheticThinking {
			// encrypted-only 场景：首次发布合成占位文本，让用户看到 thinking 指示。
			if err := service.broker.Publish(requestID, StreamEvent{
				Message: buildThinkingDeltaMessage("模型正在加密推理中，请稍候……", event.ThinkingStyle),
			}); err != nil {
				return err
			}
		}
		if suppressThinkingCompleted {
			stream.mu.Lock()
			stream.ProviderThinkingSuppressedCount++
			suppressedCount := stream.ProviderThinkingSuppressedCount
			stream.mu.Unlock()
			logger.Infof("forwarder thinking completion suppressed request_id=%s conversation_id=%s model_call_id=%s provider_pass=%d completed_count=%d suppressed_count=%d", strings.TrimSpace(requestID), strings.TrimSpace(conversationID), strings.TrimSpace(modelCallID), currentProviderPass(stream), completedCount, suppressedCount)
			if service.debug != nil {
				eventName := "thinking_completed_suppressed"
				if encryptedOnlyThinking {
					eventName = "thinking_placeholder_suppressed"
				}
				service.debug.LogRuntime(context.Background(), requestID, conversationID, eventName, map[string]any{
					"model_call_id":      strings.TrimSpace(modelCallID),
					"provider_pass":      currentProviderPass(stream),
					"completed_count":    completedCount,
					"suppressed_count":   suppressedCount,
					"has_reasoning_text": len(bytes.TrimSpace(stream.ProviderAccumulatedReasoning)) != 0,
					"has_signature":      strings.TrimSpace(event.ThinkingSignature) != "",
				})
			}
			return nil
		}
		logger.Infof("forwarder thinking completion forwarded request_id=%s conversation_id=%s model_call_id=%s provider_pass=%d completed_count=%d synthetic=%t duration_ms=%d", strings.TrimSpace(requestID), strings.TrimSpace(conversationID), strings.TrimSpace(modelCallID), currentProviderPass(stream), completedCount, shouldEmitSyntheticThinking, completedDuration)
		if service.debug != nil {
			service.debug.LogRuntime(context.Background(), requestID, conversationID, "thinking_completed_forwarded", map[string]any{
				"model_call_id":   strings.TrimSpace(modelCallID),
				"provider_pass":   currentProviderPass(stream),
				"completed_count": completedCount,
				"synthetic":       shouldEmitSyntheticThinking,
				"duration_ms":     completedDuration,
			})
		}
		return service.broker.Publish(requestID, StreamEvent{Message: buildThinkingCompletedMessage(completedDuration)})
	case modeladapter.ModelEventKindPartialToolCall:
		toolCallID := strings.TrimSpace(event.ToolCallID)
		if toolCallID == "" || event.ToolCall == nil {
			return nil
		}
		displayToolCall := service.rewriteTaskToolCallModelForDisplay(stream, event.ToolCall)
		stream.mu.Lock()
		if stream.PartialToolCallIDs == nil {
			stream.PartialToolCallIDs = make(map[string]struct{})
		}
		stream.PartialToolCallIDs[toolCallID] = struct{}{}
		stream.UpdatedAt = time.Now().UTC()
		stream.mu.Unlock()
		if inferToolName(displayToolCall) == "GenerateImage" {
			return service.broker.Publish(requestID, StreamEvent{
				Message: buildToolCallStartedMessage(toolCallID, modelCallID, displayToolCall),
			})
		}
		return service.broker.Publish(requestID, StreamEvent{
			Message: buildPartialToolCallMessage(toolCallID, modelCallID, displayToolCall, event.ArgsTextDelta),
		})
	case modeladapter.ModelEventKindToolCallDelta:
		if strings.TrimSpace(event.ToolCallID) == "" || event.ToolCallDelta == nil {
			return nil
		}
		return service.broker.Publish(requestID, StreamEvent{
			Message: buildToolCallDeltaMessage(event.ToolCallID, modelCallID, event.ToolCallDelta),
		})
	case modeladapter.ModelEventKindToolLikeCompleted:
		stream.mu.Lock()
		accumulatedText := string(stream.ProviderAccumulatedText)
		accumulatedReasoning := string(stream.ProviderAccumulatedReasoning)
		stream.mu.Unlock()
		reasoningForTool := accumulatedReasoning
		reasoningSignatureForTool := accumulatedReasoningSignature
		reasoningSignatureSourceForTool := accumulatedReasoningSignatureSource
		reasoningItemIDForTool := accumulatedReasoningItemID
		reasoningStatusForTool := accumulatedReasoningStatus
		reasoningSummaryForTool := append([]byte(nil), accumulatedReasoningSummary...)
		if strings.TrimSpace(accumulatedText) != "" {
			if err := service.flushAssistantText(stream, conversationID, turnSeq, requestID, accumulatedText, accumulatedReasoning, accumulatedReasoningSignature, accumulatedReasoningSignatureSource, accumulatedReasoningItemID, accumulatedReasoningStatus, accumulatedReasoningSummary, false); err != nil {
				return err
			}
		}
		if event.ToolInvocation == nil {
			return fmt.Errorf("tool invocation is required")
		}
		invocation := *event.ToolInvocation
		invocation.ReasoningContent = reasoningForTool
		invocation.ReasoningSignature = reasoningSignatureForTool
		invocation.ReasoningSignatureSource = reasoningSignatureSourceForTool
		invocation.ReasoningProviderItemID = reasoningItemIDForTool
		invocation.ReasoningProviderStatus = reasoningStatusForTool
		invocation.ReasoningProviderSummary = reasoningSummaryForTool
		invocation.ModelCallID = modelCallID
		stream.mu.Lock()
		stream.ProviderAccumulatedText = nil
		stream.UpdatedAt = time.Now().UTC()
		stream.mu.Unlock()
		return service.handleToolInvocation(stream, invocation)
	case modeladapter.ModelEventKindTurnFinished:
		stream.mu.Lock()
		stream.ProviderFinishReason = strings.TrimSpace(event.FinishReason)
		stream.ProviderUsage = turnUsageSnapshot{
			Provider:          event.Provider,
			Model:             event.Model,
			Role:              "parent",
			ParentModel:       "",
			LogicalModel:      stream.ModelName,
			ProviderModel:     event.Model,
			ExecutionMode:     "parent",
			BaseURL:           event.BaseURL,
			GroupName:         event.GroupName,
			InputTokens:       event.InputTokens,
			OutputTokens:      event.OutputTokens,
			CacheReadTokens:   event.CacheReadTokens,
			CacheWriteTokens:  event.CacheWriteTokens,
			UsagePresent:      event.UsagePresent,
			CacheReadPresent:  event.CacheReadPresent,
			CacheWritePresent: event.CacheWritePresent,
		}
		stream.UpdatedAt = time.Now().UTC()
		stream.mu.Unlock()
		return nil
	case modeladapter.ModelEventKindProviderError:
		if event.Err != nil {
			return providerTerminalError{cause: event.Err}
		}
		return providerTerminalError{cause: fmt.Errorf("provider error")}
	default:
		return nil
	}
}

func (service *Service) handleProviderDoneEvent(stream *ActiveStream, payload *streamProviderEvent) error {
	if stream == nil || payload == nil {
		return nil
	}

	stream.mu.Lock()
	requestID := stream.RequestID
	conversationID := stream.ConversationID
	turnSeq := stream.TurnSeq
	modelCallID := stream.CurrentModelCallID
	accumulatedText := string(stream.ProviderAccumulatedText)
	accumulatedReasoning := string(stream.ProviderAccumulatedReasoning)
	accumulatedReasoningSignature := stream.ProviderAccumulatedReasoningSignature
	accumulatedReasoningSignatureSource := stream.ProviderAccumulatedReasoningSignatureSource
	accumulatedReasoningItemID := stream.ProviderAccumulatedReasoningItemID
	accumulatedReasoningStatus := stream.ProviderAccumulatedReasoningStatus
	accumulatedReasoningSummary := append([]byte(nil), stream.ProviderAccumulatedReasoningSummary...)
	finishReason := stream.ProviderFinishReason
	usage := stream.ProviderUsage
	hadToolInvocation := stream.ToolInvocationCount > 0
	providerPass := stream.ProviderPassCount
	thinkingDeltaCount := stream.ProviderThinkingDeltaCount
	thinkingCompletedCount := stream.ProviderThinkingCompletedCount
	thinkingSuppressedCount := stream.ProviderThinkingSuppressedCount
	terminalToolInvocation := stream.ProviderTerminalToolInvocation
	existingCompletion := stream.PendingProviderCompletion
	stream.ProviderActive = false
	stream.ProviderCancel = nil
	stream.PendingProviderAction = providerActionNone
	stream.ProviderAccumulatedText = nil
	stream.ProviderAccumulatedReasoning = nil
	stream.ProviderAccumulatedReasoningSignature = ""
	stream.ProviderAccumulatedReasoningSignatureSource = ""
	stream.ProviderAccumulatedReasoningItemID = ""
	stream.ProviderAccumulatedReasoningStatus = ""
	stream.ProviderAccumulatedReasoningSummary = nil
	stream.ProviderFinishReason = ""
	stream.ProviderUsage = turnUsageSnapshot{}
	stream.ProviderTerminalToolInvocation = false
	stream.ToolInvocationCount = 0
	status := stream.Status
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	logger.Infof("forwarder provider pass done request_id=%s conversation_id=%s model_call_id=%s provider_pass=%d thinking_delta_count=%d thinking_completed_count=%d thinking_suppressed_count=%d had_tool=%t finish_reason=%s", strings.TrimSpace(requestID), strings.TrimSpace(conversationID), strings.TrimSpace(modelCallID), providerPass, thinkingDeltaCount, thinkingCompletedCount, thinkingSuppressedCount, hadToolInvocation, strings.TrimSpace(finishReason))
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), requestID, conversationID, "provider_pass_done", map[string]any{
			"model_call_id":             strings.TrimSpace(modelCallID),
			"provider_pass":             providerPass,
			"thinking_delta_count":      thinkingDeltaCount,
			"thinking_completed_count":  thinkingCompletedCount,
			"thinking_suppressed_count": thinkingSuppressedCount,
			"had_tool_invocation":       hadToolInvocation,
			"finish_reason":             strings.TrimSpace(finishReason),
		})
	}

	if errors.Is(payload.Err, errProviderLoopInterrupted) || isTerminalStreamStatus(status) {
		return nil
	}
	if payload.Err != nil {
		// 已实时推送给客户端的部分输出（文本/推理）在错误收口前必须先落盘，
		// 避免 history 缺失客户端已展示的内容（失败后可回溯、可续接）。
		flushPartialOutput := func() error {
			return service.flushAssistantText(stream, conversationID, turnSeq, requestID, accumulatedText, accumulatedReasoning, accumulatedReasoningSignature, accumulatedReasoningSignatureSource, accumulatedReasoningItemID, accumulatedReasoningStatus, accumulatedReasoningSummary, false)
		}
		// 优先处理「上下文超限」：尝试强制压缩上下文并重试，而不是直接判失败。
		// 这能挽救因 contextWindowTokens 配置偏大（或模型真实窗口小于配置）导致的 context_length_exceeded。
		if isContextLengthExceededError(payload.Err) {
			if recovered, err := service.recoverFromContextOverflow(stream, conversationID, requestID, accumulatedText, accumulatedReasoning); err != nil {
				if flushErr := flushPartialOutput(); flushErr != nil {
					return service.failStreamIfNonTerminal(stream, "unknown", flushErr)
				}
				return service.failStreamIfNonTerminal(stream, "unknown", err)
			} else if recovered {
				return nil
			}
		}
		// max_tokens 超过中转站限制（400）：catalog 静态上限可能高于中转站实际限制，
		// 从错误文本解析真实限制、降级 max_tokens 后重试。仅在尚未输出内容时重试
		//（max_tokens 400 在首个 chunk 前返回，accumulatedText 必为空）。
		if accumulatedText == "" && isMaxTokensExceededError(payload.Err) {
			if recovered, err := service.recoverFromMaxTokensExceeded(stream, requestID, payload.Err); err != nil {
				if flushErr := flushPartialOutput(); flushErr != nil {
					return service.failStreamIfNonTerminal(stream, "unknown", flushErr)
				}
				return service.failStreamIfNonTerminal(stream, "unknown", err)
			} else if recovered {
				return nil
			}
		}
		if reason := classifyProvider400Recovery(payload.Err); reason != "" &&
			providerPass == 1 &&
			strings.TrimSpace(accumulatedText) == "" &&
			strings.TrimSpace(accumulatedReasoning) == "" &&
			strings.TrimSpace(accumulatedReasoningSignature) == "" &&
			len(accumulatedReasoningSummary) == 0 &&
			!hadToolInvocation {
			if service.claimProvider400Recovery(requestID, turnSeq) {
				if service.debug != nil {
					service.debug.LogRuntime(context.Background(), requestID, conversationID, "provider_400_recovery_triggered", map[string]any{
						"reason":        string(reason),
						"provider_pass": providerPass,
						"attempt_limit": 1,
						"condition":     "pre_output_and_no_tool_invocation",
					})
				}
				logger.Infof("forwarder provider 400 recovery request_id=%s reason=%s pass=%d", strings.TrimSpace(requestID), string(reason), providerPass)
				if err := service.requestProviderAction(stream, providerActionResume); err != nil {
					return service.failStreamIfNonTerminal(stream, "unknown", err)
				}
				return nil
			}
		}
		var providerErr providerTerminalError
		if errors.As(payload.Err, &providerErr) {
			// goal 模式：provider 错误先自动重试（有限次数），不要直接判失败。
			if stream.Goal != nil {
				retried, retryErr := service.handleGoalProviderError(stream, conversationID, turnSeq, requestID, modelCallID, providerPass, payload.Err)
				if retryErr != nil {
					return service.failStreamIfNonTerminal(stream, "unknown", retryErr)
				}
				if retried {
					return nil
				}
			}
			service.setTurnPhase(stream, TurnPhaseFailed)
			return service.closeStreamWithProviderError(stream, conversationID, turnSeq, requestID, accumulatedText, accumulatedReasoning, accumulatedReasoningSignature, accumulatedReasoningSignatureSource, accumulatedReasoningItemID, accumulatedReasoningStatus, accumulatedReasoningSummary, usage, providerErr, !hadToolInvocation)
		}
		if flushErr := flushPartialOutput(); flushErr != nil {
			return service.failStreamIfNonTerminal(stream, "unknown", flushErr)
		}
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", payload.Err)
	}
	if err := service.flushAssistantText(stream, conversationID, turnSeq, requestID, accumulatedText, accumulatedReasoning, accumulatedReasoningSignature, accumulatedReasoningSignatureSource, accumulatedReasoningItemID, accumulatedReasoningStatus, accumulatedReasoningSummary, !hadToolInvocation); err != nil {
		return service.failStreamIfNonTerminal(stream, "unknown", err)
	}
	if err := service.recordTurnUsageSnapshot(stream, conversationID, turnSeq, requestID, modelCallID, "completed", usage, "", false); err != nil {
		return service.failStreamIfNonTerminal(stream, "usage_persistence_error", err)
	}
	if err := service.updateConversationTokenState(stream, conversationID, usage, modelCallID, true); err != nil {
		return service.failStreamIfNonTerminal(stream, "unknown", err)
	}
	if err := service.syncSummaryCarryForward(conversationID, requestID, modelCallID); err != nil {
		return service.failStreamIfNonTerminal(stream, "unknown", err)
	}
	if err := service.emitTurnSummary(stream, requestID, modelCallID); err != nil {
		return service.failStreamIfNonTerminal(stream, "unknown", err)
	}

	pendingCount := pendingBridgeCount(stream)
	if pendingCount > 0 {
		awaitingUser := hasPendingAwaitingUserInteraction(stream)
		forceComplete := awaitingUser
		rememberPendingProviderCompletion(stream, pendingTurnCompletion{
			ConversationID: conversationID,
			RequestID:      requestID,
			TurnSeq:        turnSeq,
			ModelCallID:    modelCallID,
			ProviderPass:   currentProviderPass(stream),
			Usage:          usage,
			Disposition:    completionDispositionForExternalResults(finishReason, forceComplete, hadToolInvocation),
		})
		if awaitingUser {
			service.setTurnPhase(stream, TurnPhaseAwaitingUser)
		} else {
			service.setTurnPhase(stream, TurnPhaseWaitingExternal)
		}
		if err := service.publishCheckpoint(requestID, conversationID); err != nil {
			return service.failStreamIfNonTerminal(stream, "unknown", err)
		}
		return nil
	}

	if existingCompletion == nil {
		handled, err := service.handleSubagentEmptyStopAfterToolResult(stream, conversationID, turnSeq, requestID, modelCallID, finishReason, accumulatedText)
		if err != nil {
			return service.failStreamIfNonTerminal(stream, "unknown", err)
		}
		if handled {
			return nil
		}
	}

	if existingCompletion != nil {
		completion := *existingCompletion
		if strings.TrimSpace(completion.ModelCallID) == "" {
			completion.ModelCallID = modelCallID
		}
		if completion.ProviderPass == 0 {
			completion.ProviderPass = currentProviderPass(stream)
		}
		completion.Usage = usage
		clearPendingProviderCompletion(stream)
		if completion.Disposition == completionDispositionResumeAfterExternal {
			if err := service.publishCheckpoint(requestID, conversationID); err != nil {
				return service.failStreamIfNonTerminal(stream, "unknown", err)
			}
			if err := service.requestProviderAction(stream, providerActionResume); err != nil {
				return service.failStreamIfNonTerminal(stream, "unknown", err)
			}
			return nil
		}
		if err := service.completeSuccessfulTurn(stream, completion); err != nil {
			return service.failStreamIfNonTerminal(stream, "unknown", err)
		}
		return nil
	}

	// goal 模式：无工具调用不直接收口，先做目标自检 / 预算判定 / 校验。
	if stream.Goal != nil {
		handled, err := service.handleGoalPassFinished(stream, conversationID, turnSeq, requestID, modelCallID, providerPass, finishReason, accumulatedText, hadToolInvocation)
		if err != nil {
			return service.failStreamIfNonTerminal(stream, "unknown", err)
		}
		if handled {
			return nil
		}
	}

	if (hadToolInvocation || shouldResumeAfterToolResults(finishReason)) && !terminalToolInvocation {
		if err := service.publishCheckpoint(requestID, conversationID); err != nil {
			return service.failStreamIfNonTerminal(stream, "unknown", err)
		}
		if err := service.requestProviderAction(stream, providerActionResume); err != nil {
			return service.failStreamIfNonTerminal(stream, "unknown", err)
		}
		return nil
	}

	// max_output_tokens 截断恢复：provider 返回 200 但流式被输出预算截断（response.incomplete，
	// incomplete_details.reason=max_output_tokens），整回合只产出了 reasoning、没有助手正文也没有
	// 工具调用。此时若直接 completeSuccessfulTurn 会把"零输出"回合标记成功并关闭 SSE 流，任务无法推进
	//（且该截断响应在开启本地缓存时还会被缓存——由 provider_cache.go 的 completedCleanly 拦截）。
	//
	// 处理方式（参考 Reasonix emptyFinalRetry + 本仓 handleSubagentEmptyStopAfterToolResult 的成熟模式）：
	// 追加一条 prompt_context 提示消息引导模型重新产出可见回复/工具调用，再续写一轮 provider pass。
	// 相比依赖 OpenAI Responses 的 encrypted_content 续写，追加通用 user 提示更稳健、多 provider 兼容。
	// 用 prompt_context source 做幂等去重，本回合最多恢复一次；续写后仍截断则走正常收口。
	if isMaxOutputTokensTruncation(finishReason) && !hadToolInvocation && strings.TrimSpace(accumulatedText) == "" {
		handled, err := service.handleMaxOutputTokensRecovery(stream, conversationID, turnSeq, requestID, modelCallID, providerPass, finishReason)
		if err != nil {
			return service.failStreamIfNonTerminal(stream, "unknown", err)
		}
		if handled {
			return nil
		}
	}

	clearPendingProviderCompletion(stream)
	if err := service.completeSuccessfulTurn(stream, pendingTurnCompletion{
		ConversationID: conversationID,
		RequestID:      requestID,
		TurnSeq:        turnSeq,
		ModelCallID:    modelCallID,
		ProviderPass:   currentProviderPass(stream),
		Usage:          usage,
	}); err != nil {
		return service.failStreamIfNonTerminal(stream, "unknown", err)
	}
	return nil
}

func providerTimerKey(kind streamTimerKind, execID string) string {
	if strings.TrimSpace(execID) == "" {
		return string(kind)
	}
	return string(kind) + ":" + strings.TrimSpace(execID)
}

func (service *Service) scheduleStreamTimer(stream *ActiveStream, key string, delay time.Duration, kind streamTimerKind, execID string, messageID uint32, reason string) {
	if stream == nil || strings.TrimSpace(key) == "" {
		return
	}
	stream.mu.Lock()
	if stream.TimerTokens == nil {
		stream.TimerTokens = make(map[string]uint64)
	}
	if stream.StreamTimers == nil {
		stream.StreamTimers = make(map[string]*time.Timer)
	}
	if previous := stream.StreamTimers[key]; previous != nil {
		previous.Stop()
	}
	stream.TimerTokens[key]++
	token := stream.TimerTokens[key]
	timer := time.AfterFunc(max(delay, 0), func() {
		if err := service.postStreamCommandAsync(stream, streamCommand{
			Kind: streamCommandTimerFired,
			Timer: &streamTimerEvent{
				Key:       key,
				Kind:      kind,
				Token:     token,
				ExecID:    strings.TrimSpace(execID),
				MessageID: messageID,
				Reason:    strings.TrimSpace(reason),
			},
		}); err != nil && !errors.Is(err, errProviderLoopInterrupted) {
			logger.Errorf("forwarder timer post failed request_id=%s key=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(key), err)
		}
	})
	stream.StreamTimers[key] = timer
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
}

func timerEventMatches(stream *ActiveStream, payload *streamTimerEvent) bool {
	if stream == nil || payload == nil || strings.TrimSpace(payload.Key) == "" {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.TimerTokens[payload.Key] == payload.Token
}

func clearStreamTimer(stream *ActiveStream, key string) {
	if stream == nil || strings.TrimSpace(key) == "" {
		return
	}
	stream.mu.Lock()
	if timer := stream.StreamTimers[key]; timer != nil {
		timer.Stop()
		delete(stream.StreamTimers, key)
	}
	delete(stream.TimerTokens, key)
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
}

func stopAllStreamTimersLocked(stream *ActiveStream) {
	if stream == nil {
		return
	}
	for key, timer := range stream.StreamTimers {
		if timer != nil {
			timer.Stop()
		}
		delete(stream.StreamTimers, key)
	}
	for key := range stream.TimerTokens {
		stream.TimerTokens[key]++
	}
}

func (service *Service) handleTimerEvent(stream *ActiveStream, payload *streamTimerEvent) error {
	if stream == nil || payload == nil {
		return nil
	}
	if !timerEventMatches(stream, payload) {
		return nil
	}
	clearStreamTimer(stream, payload.Key)

	switch payload.Kind {
	case streamTimerProviderResume:
		stream.mu.Lock()
		providerActive := stream.ProviderActive
		action := stream.PendingProviderAction
		status := stream.Status
		stream.mu.Unlock()
		if providerActive || isTerminalStreamStatus(status) || action != providerActionResume || pendingBridgeCount(stream) > 0 {
			return nil
		}
		return service.driveProvider(stream)
	case streamTimerNonStreamingRecovery:
		current, ok := snapshotPendingExec(stream, payload.ExecID)
		if !ok || current.MessageID != payload.MessageID || current.StreamState != "transport_closed" {
			return nil
		}
		return service.recoverNonStreamingExecAfterStreamClose(stream, current)
	case streamTimerShellForeground:
		return service.recoverShellWithoutTerminalIfNeeded(stream, payload.ExecID, payload.MessageID, shellRecoveryReasonForegroundDeadline)
	case streamTimerShellTransportClose:
		current, status, found := snapshotPendingExecWithStatus(stream, payload.ExecID)
		if !found || current.MessageID != payload.MessageID || current.StreamState != "transport_closed" || isTerminalStreamStatus(status) {
			return nil
		}
		return service.recoverShellWithoutTerminal(stream, current, shellRecoveryReasonTransportClosed)
	case streamTimerExecWatchdog:
		return service.recoverStaleExecWithoutTerminal(stream, payload.ExecID, payload.MessageID, payload.Reason)
	case streamTimerInteractionTimeout:
		return service.recoverStaleInteractionWithoutResponse(stream, payload)
	case streamTimerTurnStale:
		return service.handleTurnStaleTimeout(stream, payload)
	case streamTimerCheckpointBlobs:
		// checkpoint blob 同步超时：Cursor 断连/blob 确认丢失时，若无人处理，
		// stream 会永久卡在 TurnPhaseCheckpointing，终态 endstream 永不发出，
		// 客户端只能等到连接断开报 RetriableError: Canceled。这里必须显式收口。
		return service.handleCheckpointBlobTimeout(stream)
	case streamTimerOrphanCancel:
		stream.mu.Lock()
		subscriberCount := len(stream.Subscribers)
		status := stream.Status
		providerActive := stream.ProviderActive
		pendingWork := len(stream.PendingExecs) + len(stream.PendingInteractions)
		stream.mu.Unlock()
		if subscriberCount > 0 || isTerminalStreamStatus(status) {
			return nil
		}
		// 网络波动加固：客户端 RunSSE 断连但任务仍在推进（provider 活跃、有待执行
		// 工具/交互、或有活跃委派子代理）时，不取消 turn，保留运行等待客户端重连
		// 订阅；彻底清理交给 turn-stale 看门狗与 broker retention 兜底，避免网络
		// 波动几秒就误杀长任务。定时器已在 handleTimerEvent 开头清理，不续排。
		if providerActive || pendingWork > 0 || service.hasActiveDelegation(stream) {
			logger.Infof("forwarder orphan cancel deferred active turn request_id=%s subscriber_count=%d provider_active=%t pending=%d",
				strings.TrimSpace(stream.RequestID), subscriberCount, providerActive, pendingWork)
			return nil
		}
		logger.Infof("forwarder orphan canceling disconnected request request_id=%s subscriber_count=%d active_delegation=%t",
			strings.TrimSpace(stream.RequestID), subscriberCount, hasActiveDelegationAggregate(stream))
		return service.handleCancelIntent(InboundIntent{
			Kind:         "cancel",
			RequestID:    stream.RequestID,
			CancelReason: firstNonEmpty(payload.Reason, "[canceled] RunSSE client disconnected"),
		})
	default:
		return nil
	}
}

func (service *Service) scheduleOrphanCancelActor(requestID string, reason string) bool {
	if service == nil || service.broker == nil {
		return false
	}
	stream, ok := service.broker.Get(requestID)
	if !ok || stream == nil {
		return false
	}
	stream.mu.Lock()
	placeholder := strings.TrimSpace(stream.ConversationID) == "" &&
		!stream.ProviderActive &&
		len(stream.PendingExecs) == 0 &&
		len(stream.PendingInteractions) == 0 &&
		len(stream.Backlog) == 0
	terminal := isTerminalStreamStatus(stream.Status)
	stream.mu.Unlock()
	if placeholder || terminal {
		return false
	}
	if err := service.postStreamCommandAsync(stream, streamCommand{
		Kind:   streamCommandMaybeOrphaned,
		Reason: firstNonEmpty(strings.TrimSpace(reason), "[canceled] RunSSE client disconnected"),
	}); err != nil {
		return false
	}
	return true
}

func (service *Service) cancelOtherConversationActors(conversationID string, keepRequestID string, reason string) {
	if service == nil || service.broker == nil || strings.TrimSpace(conversationID) == "" {
		return
	}
	for _, requestID := range service.broker.OtherConversationRequestIDs(conversationID, keepRequestID) {
		stream, ok := service.broker.Get(requestID)
		if !ok || stream == nil {
			continue
		}
		if err := service.postStreamCommandWait(stream, streamCommand{
			Kind: streamCommandCancel,
			Intent: InboundIntent{
				Kind:         "cancel",
				RequestID:    requestID,
				CancelReason: reason,
			},
		}); err != nil && !errors.Is(err, errProviderLoopInterrupted) {
			logger.Errorf("forwarder cancel superseded stream failed request_id=%s err=%v", strings.TrimSpace(requestID), err)
		}
	}
}

func (service *Service) setTurnPhase(stream *ActiveStream, phase TurnPhase) {
	if stream == nil {
		return
	}
	stream.mu.Lock()
	stream.Phase = phase
	stream.UpdatedAt = time.Now().UTC()
	// 仅当真正进入「等待外部（工具/交互结果）」阶段时，才挂一个 turn-staleness 看门狗。
	// 它会通过 handleTurnStaleTimeout 二次校验真实状态，仅在「无进展地卡住」时自救。
	waitingExternal := phase == TurnPhaseWaitingExternal
	stream.mu.Unlock()
	if waitingExternal {
		service.scheduleTurnStaleWatchdog(stream)
	} else {
		clearStreamTimer(stream, providerTimerKey(streamTimerTurnStale, ""))
		// 离开等待态时清掉宽限标记，下一次重新进入会从阶段一重新开始。
		stream.mu.Lock()
		stream.TurnStaleGraceStartedAt = time.Time{}
		stream.mu.Unlock()
	}
}

// scheduleTurnStaleWatchdog 安排一个 turn-staleness 看门狗：若回合停留在「等待外部」阶段
// 且在阈值内没有任何进展，则触发两段式自救。每次重新调用都会刷新 token、作废旧触发，
// 因此「有进展」（工具结果到达/provider 重新活跃等会再次走到 setTurnPhase 或 reconcile）
// 会自然延后触发，只有真正卡死才会到期。
func (service *Service) scheduleTurnStaleWatchdog(stream *ActiveStream) {
	if service == nil || stream == nil {
		return
	}
	delay := service.resolveTurnStaleDelay(stream)
	if delay <= 0 {
		return
	}
	service.scheduleStreamTimer(stream, providerTimerKey(streamTimerTurnStale, ""), delay, streamTimerTurnStale, "", 0, "turn_stale_watchdog")
}

func rememberPendingProviderCompletion(stream *ActiveStream, completion pendingTurnCompletion) {
	if stream == nil {
		return
	}
	stream.mu.Lock()
	copy := mergePendingProviderCompletion(stream.PendingProviderCompletion, completion)
	stream.PendingProviderCompletion = &copy
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
}

func mergePendingProviderCompletion(existing *pendingTurnCompletion, incoming pendingTurnCompletion) pendingTurnCompletion {
	if existing == nil {
		if incoming.Disposition == completionDispositionNone {
			incoming.Disposition = completionDispositionCompleteAfterExternal
		}
		return incoming
	}
	merged := *existing
	if merged.ConversationID == "" && incoming.ConversationID != "" {
		merged.ConversationID = incoming.ConversationID
	}
	if merged.RequestID == "" && incoming.RequestID != "" {
		merged.RequestID = incoming.RequestID
	}
	if merged.TurnSeq <= 0 && incoming.TurnSeq > 0 {
		merged.TurnSeq = incoming.TurnSeq
	}
	if strings.TrimSpace(merged.ModelCallID) == "" && strings.TrimSpace(incoming.ModelCallID) != "" {
		merged.ModelCallID = incoming.ModelCallID
	}
	if merged.ProviderPass == 0 && incoming.ProviderPass != 0 {
		merged.ProviderPass = incoming.ProviderPass
	}
	if incoming.Usage.hasAny() {
		merged.Usage = incoming.Usage
	}
	merged.Disposition = mergeCompletionDisposition(merged.Disposition, incoming.Disposition)
	return merged
}

func mergeCompletionDisposition(existing pendingCompletionDisposition, incoming pendingCompletionDisposition) pendingCompletionDisposition {
	if existing == completionDispositionCompleteAfterExternal || incoming == completionDispositionCompleteAfterExternal {
		return completionDispositionCompleteAfterExternal
	}
	if existing == completionDispositionResumeAfterExternal || incoming == completionDispositionResumeAfterExternal {
		return completionDispositionResumeAfterExternal
	}
	return completionDispositionCompleteAfterExternal
}

func completionDispositionForExternalResults(finishReason string, forceComplete bool, hadToolInvocation bool) pendingCompletionDisposition {
	if forceComplete {
		return completionDispositionCompleteAfterExternal
	}
	// Some providers may report end_turn even after emitting a valid tool_use block.
	if hadToolInvocation || shouldResumeAfterToolResults(finishReason) {
		return completionDispositionResumeAfterExternal
	}
	return completionDispositionCompleteAfterExternal
}

func clearPendingProviderCompletion(stream *ActiveStream) {
	if stream == nil {
		return
	}
	stream.mu.Lock()
	stream.PendingProviderCompletion = nil
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
}
