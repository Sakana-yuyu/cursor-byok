package forwarder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/logger"
	promptassets "cursor/prompt"
	"github.com/google/uuid"
)

const (
	compactionAutoReserveTokens      = 10000
	compactionTriggerRemainingTokens = 8192
	compactionReserveFloorTokens     = 8192
	compactionSummaryMaxChars        = 12000
	compactionSummaryOutputMaxTokens = 4096
	compactionTurnSnippetMaxChars    = 900

	autoCompactionPreservedToolResultLimitBytes = 16 * 1024
	autoCompactionFallbackToolResultLimitBytes  = 4 * 1024
)

const (
	compactionRequestSourcePromptAsset = "prompt_asset"
	compactionOverflowTerminalCode     = "context_overflow_after_compaction"
)

type compactionPlan struct {
	Trigger                            string
	ContextTokens                      int64
	ContextWindowSize                  int64
	ContextUsagePercent                float64
	ReserveTokens                      int64
	MessageCount                       int32
	MessagesToCompact                  int32
	CompactTurnCount                   int32
	IsFirstCompaction                  bool
	ExistingSummary                    string
	CompactedTurns                     []compactedTurnSummary
	ManualInstruction                  string
	RequestSource                      string
	CurrentTurnSeq                     int64
	CurrentRequestID                   string
	CurrentUserText                    string
	PreserveCurrentTurnInputs          bool
	ProjectionConversationID           string
	ProjectionRootConversationID       string
	ProjectionParentConversationID     string
	ProjectionParentToolCallID         string
	ProjectionModelKey                 string
	ProjectionContextVersion           int64
	ProjectionSummaryStartEntrySeq     int64
	ProjectionCoveredEntrySeq          int64
	ProjectionCoveredPrefixFingerprint string
}

type compactedTurnSummary struct {
	UserText string
	Steps    []string
}

// contextProjectionArtifactObserver deliberately keeps automatic maintenance
// out of canonical conversation metadata. Its summary call is still accounted
// for by recordCompactionSummaryUsage.
type contextProjectionArtifactObserver struct{}

func (contextProjectionArtifactObserver) RecordLLMRequest(string, string, string, map[string]any) (string, error) {
	return "", nil
}

func (contextProjectionArtifactObserver) AppendLLMResponseChunk(string, string, string, string) (string, error) {
	return "", nil
}

func (contextProjectionArtifactObserver) RecordLLMSummary(string, string, string, map[string]any) (string, error) {
	return "", nil
}

type compactionCandidateTurn struct {
	Summary         compactedTurnSummary
	ReplayCount     int32
	EstimatedTokens int64
}

func (service *Service) maybeCompactBeforeProvider(stream *ActiveStream, conversation *ConversationFile, compiled CompiledConversation) (bool, error) {
	if service == nil || stream == nil || conversation == nil {
		return false, nil
	}
	manualInstruction, manual := streamManualCompactionDirective(stream)
	plan, err := service.buildCompactionPlan(stream, conversation, compiled, manual, manualInstruction)
	if err != nil {
		return false, err
	}
	if plan == nil {
		if manual {
			return true, service.finishManualCompactionNoop(stream)
		}
		return false, nil
	}
	return true, service.beginPendingCompaction(stream, plan)
}

func (service *Service) buildCompactionPlan(stream *ActiveStream, conversation *ConversationFile, compiled CompiledConversation, manual bool, manualInstruction string) (*compactionPlan, error) {
	if stream == nil || conversation == nil {
		return nil, nil
	}
	if manual {
		return service.buildManualCompactionPlan(stream, conversation, compiled, manualInstruction)
	}
	return service.buildAutoCompactionPlan(stream, conversation, compiled)
}

func (service *Service) buildManualCompactionPlan(stream *ActiveStream, conversation *ConversationFile, compiled CompiledConversation, manualInstruction string) (*compactionPlan, error) {
	if stream == nil || conversation == nil {
		return nil, nil
	}
	contextWindowSize := compactionContextWindowSize(conversation)
	if contextWindowSize <= 0 {
		return nil, nil
	}
	contextTokens, err := service.resolveCompactionBaselineTokens(stream.ConversationID, compiled, conversation)
	if err != nil {
		return nil, err
	}
	if contextTokens <= 0 {
		return nil, nil
	}
	usagePercent := 0.0
	if contextWindowSize > 0 {
		usagePercent = float64(contextTokens) / float64(contextWindowSize)
	}
	base := &compactionPlan{
		Trigger:                   "manual",
		ContextTokens:             contextTokens,
		ContextWindowSize:         contextWindowSize,
		ContextUsagePercent:       usagePercent,
		ReserveTokens:             compactionAutoReserveTokens,
		MessageCount:              clampInt64ToInt32(int64(len(compiled.Messages))),
		IsFirstCompaction:         len(compactionSummaryTexts(conversation)) == 0,
		ExistingSummary:           existingConversationSummaryText(conversation),
		ManualInstruction:         strings.TrimSpace(manualInstruction),
		CurrentTurnSeq:            stream.TurnSeq,
		CurrentRequestID:          strings.TrimSpace(stream.RequestID),
		CurrentUserText:           strings.TrimSpace(stream.LatestUserText),
		PreserveCurrentTurnInputs: false,
	}
	return service.buildLegacyCompactionPlan(base, conversation, true, 0)
}

func (service *Service) buildAutoCompactionPlan(stream *ActiveStream, conversation *ConversationFile, compiled CompiledConversation) (*compactionPlan, error) {
	if stream == nil || conversation == nil {
		return nil, nil
	}
	contextWindowSize := compactionContextWindowSize(conversation)
	if contextWindowSize <= 0 {
		return nil, nil
	}
	estimatedCompiledTokens := estimateCompiledPromptTokens(compiled)
	reserveTokens := service.resolveCompactionReserveTokens(stream.ModelID)
	if reserveTokens <= 0 {
		reserveTokens = conversation.AutoCompactionReserveTokens
	}
	if reserveTokens <= 0 {
		reserveTokens = compactionAutoReserveTokens
	}
	// 使用 80% 的上下文窗口作为预算，留出安全余量防止估算偏差导致 context_length_exceeded。
	budgetTokens := int64(float64(contextWindowSize)*0.8) - reserveTokens
	preflightExceeded := estimatedCompiledTokens > 0 && estimatedCompiledTokens > budgetTokens
	contextTokens := estimatedCompiledTokens
	if contextTokens <= 0 {
		contextTokens = conversation.AutoCompactionPromptTokens
	}
	pendingExceeded := conversation.AutoCompactionPending && contextTokens > 0 && contextTokens > budgetTokens
	if !pendingExceeded && !preflightExceeded {
		return nil, nil
	}
	// Automatic pressure handling is projection-only. Canonical tool-result snipping
	// is deliberately reserved for an explicit maintenance action: changing entries
	// here would invalidate the canonical history contract and prompt-cache prefix.
	var existing *contextProjectionState
	if service.store != nil {
		existing, _ = service.store.LoadContextProjection(conversation.ConversationID)
		if valid, _ := validateContextProjectionState(existing, conversation, contextProjectionModelKey(stream)); !valid {
			existing = nil
		}
	}
	plan, err := buildContextProjectionSummaryPlan(
		conversation,
		contextProjectionModelKey(stream),
		existing,
		contextTokens,
		contextWindowSize,
		reserveTokens,
	)
	if err != nil {
		return nil, err
	}
	if plan == nil && preflightExceeded {
		// 滑动窗口压缩返回 nil（可能因轮次不足），但 prompt 确实超限。
		// 用 force 模式重试：强制压缩，忽略 contextTokens <= hardBudget 的短路判断，
		// 并让 buildContextProjectionSummaryPlanWithRecentTail 自适应减少尾部保留轮次。
		// 这样即使子代理刚启动轮次很少，也能压缩出空间而非直接报错。
		plan, err = buildContextProjectionSummaryPlanWithRecentTail(
			conversation,
			contextProjectionModelKey(stream),
			existing,
			contextTokens,
			contextWindowSize,
			reserveTokens,
			contextProjectionRecentTailTurns,
			true,
		)
		if err != nil {
			return nil, err
		}
		if plan == nil {
			// force 重试仍返回 nil（轮次太少连 1 轮都无法压缩），才报错。
			return nil, compactionTerminalError{
				code: compactionOverflowTerminalCode,
				message: fmt.Sprintf(
					"compiled prompt exceeds context budget before provider request (estimated=%d budget=%d)",
					estimatedCompiledTokens,
					budgetTokens,
				),
			}
		}
	}
	return plan, nil
}

func (service *Service) contextProjectionPressureExceeded(stream *ActiveStream, conversation *ConversationFile, compiled CompiledConversation) bool {
	if service == nil || stream == nil || conversation == nil {
		return false
	}
	contextWindowSize := compactionContextWindowSize(conversation)
	if contextWindowSize <= 0 {
		return false
	}
	estimatedCompiledTokens := estimateCompiledPromptTokens(compiled)
	reserveTokens := service.resolveCompactionReserveTokens(stream.ModelID)
	if reserveTokens <= 0 {
		reserveTokens = conversation.AutoCompactionReserveTokens
	}
	if reserveTokens <= 0 {
		reserveTokens = compactionAutoReserveTokens
	}
	budgetTokens := int64(float64(contextWindowSize)*contextProjectionHardRatio) - reserveTokens
	contextTokens := estimatedCompiledTokens
	if contextTokens <= 0 {
		contextTokens = conversation.AutoCompactionPromptTokens
	}
	return (estimatedCompiledTokens > 0 && estimatedCompiledTokens > budgetTokens) ||
		(conversation.AutoCompactionPending && contextTokens > 0 && contextTokens > budgetTokens)
}

// buildForcedCompactionPlan constructs a sidecar projection after a provider reports
// context_length_exceeded. Automatic recovery must never use the legacy compaction
// path because that path rewrites canonical conversation entries.
func (service *Service) buildForcedCompactionPlan(stream *ActiveStream, conversation *ConversationFile, compiled CompiledConversation) (*compactionPlan, error) {
	if stream == nil || conversation == nil {
		return nil, nil
	}
	contextWindowSize := compactionContextWindowSize(conversation)
	if contextWindowSize <= 0 {
		return nil, nil
	}
	contextTokens, err := service.resolveCompactionBaselineTokens(stream.ConversationID, compiled, conversation)
	if err != nil {
		return nil, err
	}
	reserveTokens := service.resolveCompactionReserveTokens(stream.ModelID)
	if reserveTokens <= 0 {
		reserveTokens = conversation.AutoCompactionReserveTokens
	}
	if reserveTokens <= 0 {
		reserveTokens = compactionAutoReserveTokens
	}
	var existing *contextProjectionState
	if service.store != nil {
		existing, _ = service.store.LoadContextProjection(conversation.ConversationID)
		if valid, _ := validateContextProjectionState(existing, conversation, contextProjectionModelKey(stream)); !valid {
			existing = nil
		}
	}
	stream.mu.Lock()
	overflowAttempts := stream.ContextOverflowCompactionAttempts
	stream.mu.Unlock()
	recentTailTurns := contextProjectionRecentTailTurns - overflowAttempts - 1
	if recentTailTurns < 1 {
		recentTailTurns = 1
	}
	return buildContextProjectionSummaryPlanWithRecentTail(
		conversation,
		contextProjectionModelKey(stream),
		existing,
		contextTokens,
		contextWindowSize,
		reserveTokens,
		recentTailTurns,
		true,
	)
}

func (service *Service) resolveCompactionBaselineTokens(conversationID string, compiled CompiledConversation, conversation *ConversationFile) (int64, error) {
	contextTokens := estimateCompiledPromptTokens(compiled)
	if contextTokens > 0 {
		return contextTokens, nil
	}
	if conversation != nil && conversation.TokenDetailsUsedTokens > 0 {
		return int64(conversation.TokenDetailsUsedTokens), nil
	}
	if conversationHasLocalCarryForwardState(conversation) {
		return 0, nil
	}
	if service != nil && strings.TrimSpace(conversationID) != "" {
		promptTokens, ok, err := service.loadLatestSummaryPromptTokens(conversationID)
		if err != nil {
			return 0, err
		}
		if ok && promptTokens > 0 {
			return promptTokens, nil
		}
	}
	return 0, nil
}

func conversationHasLocalCarryForwardState(conversation *ConversationFile) bool {
	if conversation == nil {
		return false
	}
	return len(compactionSummaryTexts(conversation)) > 0
}

func (service *Service) buildLegacyCompactionPlan(base *compactionPlan, conversation *ConversationFile, _ bool, _ int64) (*compactionPlan, error) {
	if conversation == nil || base == nil {
		return nil, nil
	}
	candidates := buildContextCompactionCandidates(checkpointProjectionEntries(conversation.Entries), base.CurrentTurnSeq, base.CurrentRequestID)
	if len(candidates) == 0 {
		return nil, nil
	}
	compactedTurns := make([]compactedTurnSummary, 0, len(candidates))
	messagesToCompact := int32(0)
	for _, candidate := range candidates {
		compactedTurns = append(compactedTurns, candidate.Summary)
		messagesToCompact += candidate.ReplayCount
	}
	plan := cloneCompactionPlanBase(base)
	plan.RequestSource = compactionRequestSourcePromptAsset
	plan.MessagesToCompact = messagesToCompact
	plan.CompactTurnCount = clampInt64ToInt32(int64(len(candidates)))
	plan.CompactedTurns = compactedTurns
	return &plan, nil
}

func (service *Service) beginPendingCompaction(stream *ActiveStream, plan *compactionPlan) error {
	if service == nil || stream == nil || plan == nil {
		return nil
	}
	if plan.Trigger == contextProjectionTrigger {
		pending := newPendingCompaction(plan)
		stream.mu.Lock()
		stream.PendingCompaction = pending
		stream.PendingProviderAction = providerActionNone
		stream.Phase = TurnPhaseCompacting
		stream.UpdatedAt = time.Now().UTC()
		stream.mu.Unlock()
		return service.startPendingCompactionSummary(stream, pending)
	}
	request := buildPreCompactHookRequest(stream, plan)
	serverMessage, pendingExec, err := service.execBridge.OpenExecuteHook(request, "execute_hook_pre_compact")
	if err != nil {
		return err
	}
	stream.mu.Lock()
	pendingExec.ModelCallID = strings.TrimSpace(stream.CurrentModelCallID)
	pendingExec.ProviderPass = stream.ProviderPassCount
	stream.PendingCompaction = newPendingCompaction(plan)
	stream.PendingProviderAction = providerActionNone
	stream.PendingExecs[pendingExec.ExecID] = pendingExec
	stream.Phase = TurnPhaseCompacting
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	// 先发 checkpoint 让前端获得会话上下文，再发 SummaryStarted 触发 summarize UI。
	if err := service.publishCheckpoint(stream.RequestID, stream.ConversationID); err != nil {
		return err
	}
	if err := service.broker.Publish(stream.RequestID, StreamEvent{
		Message: buildSummaryStartedMessage(),
	}); err != nil {
		return err
	}
	return service.broker.Publish(stream.RequestID, StreamEvent{Message: serverMessage})
}

func (service *Service) handlePreCompactTerminal(stream *ActiveStream, sourcePass int, hookMessage string) error {
	if service == nil || stream == nil {
		return nil
	}
	_ = sourcePass
	stream.mu.Lock()
	if stream.PendingCompaction == nil {
		stream.mu.Unlock()
		if strings.TrimSpace(hookMessage) != "" {
			if err := service.publishSummaryCompleted(stream, hookMessage); err != nil {
				return err
			}
		}
		return service.requestProviderAction(stream, providerActionResume)
	}
	plan := clonePendingCompaction(stream.PendingCompaction)
	plan.HookMessage = strings.TrimSpace(hookMessage)
	stream.PendingCompaction = plan
	stream.mu.Unlock()
	return service.startPendingCompactionSummary(stream, plan)
}

func (service *Service) startPendingCompactionSummary(stream *ActiveStream, plan *PendingCompaction) error {
	if service == nil || stream == nil || plan == nil {
		return nil
	}
	summaryModelCallID := uuid.NewString()
	ctx, cancel := context.WithCancel(context.Background())
	stream.mu.Lock()
	if stream.Status == StreamStatusCanceled || stream.Status == StreamStatusCompleted || stream.Status == StreamStatusFailed {
		stream.PendingCompaction = nil
		stream.mu.Unlock()
		cancel()
		return nil
	}
	stream.CurrentCompactionToken++
	token := stream.CurrentCompactionToken
	stream.CurrentModelCallID = summaryModelCallID
	stream.ProviderActive = true
	stream.ProviderCancel = cancel
	stream.PendingProviderAction = providerActionNone
	stream.PendingCompaction.SummaryModelCallID = summaryModelCallID
	plan.SummaryModelCallID = summaryModelCallID
	stream.Phase = TurnPhaseCompacting
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if plan.Trigger != contextProjectionTrigger {
		if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
			newCompactionRequestEntry(plan),
		}); err != nil {
			cancel()
			return err
		}
	}
	go service.runPendingCompaction(stream, token, clonePendingCompaction(plan), summaryModelCallID, ctx)
	return nil
}

func (service *Service) runPendingCompaction(stream *ActiveStream, token uint64, plan *PendingCompaction, modelCallID string, ctx context.Context) {
	if service == nil || stream == nil || plan == nil {
		return
	}
	summaryText, err := service.generateCompactionSummary(ctx, stream, plan, modelCallID)
	if err == nil {
		if trimmed := strings.TrimSpace(summaryText); trimmed != "" {
			summaryText = trimmed
		} else {
			summaryText = buildFallbackCompactionSummary(plan)
		}
	}
	if postErr := service.postStreamCommandWait(stream, streamCommand{
		Kind: streamCommandCompactionEvent,
		Compaction: &streamCompactionEvent{
			Token:       token,
			Plan:        plan,
			SummaryText: strings.TrimSpace(summaryText),
			Err:         err,
		},
	}); postErr != nil && !errors.Is(postErr, errProviderLoopInterrupted) {
		logger.Errorf("forwarder compaction completion post failed request_id=%s token=%d err=%v", strings.TrimSpace(stream.RequestID), token, postErr)
		_ = service.failStreamIfNonTerminal(stream, "unknown", postErr)
	}
}

func (service *Service) handleCompactionEvent(stream *ActiveStream, payload *streamCompactionEvent) error {
	if service == nil || stream == nil || payload == nil {
		return nil
	}
	stream.mu.Lock()
	if stream.CurrentCompactionToken != payload.Token {
		stream.mu.Unlock()
		return nil
	}
	status := stream.Status
	stream.ProviderActive = false
	stream.ProviderCancel = nil
	stream.PendingProviderAction = providerActionNone
	stream.PendingCompaction = nil
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if errors.Is(payload.Err, errProviderLoopInterrupted) || isTerminalStreamStatus(status) {
		return nil
	}
	if payload.Plan != nil && payload.Plan.Trigger == contextProjectionTrigger {
		if payload.Err != nil {
			conversation, _, _, err := service.snapshotCheckpointConversation(stream)
			if err != nil {
				return service.failStream(stream, "unknown", err)
			}
			if err := service.completeContextProjectionRecentTailFallback(conversation, payload.Plan); err != nil {
				service.setTurnPhase(stream, TurnPhaseFailed)
				return service.failStream(stream, "unknown", fmt.Errorf("context projection summary failed (%v) and recent-tail fallback could not be saved: %w", payload.Err, err))
			}
			service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "context_projection_summary_fallback", map[string]any{
				"mode":              contextProjectionModeRecentTail,
				"covered_entry_seq": payload.Plan.ProjectionCoveredEntrySeq,
				"cause":             payload.Err.Error(),
			})
			return service.requestProviderAction(stream, providerActionResume)
		}
		conversation, _, _, err := service.snapshotCheckpointConversation(stream)
		if err != nil {
			return service.failStream(stream, "unknown", err)
		}
		if err := service.completeContextProjectionSummary(conversation, payload.Plan, payload.SummaryText); err != nil {
			service.setTurnPhase(stream, TurnPhaseFailed)
			return service.failStream(stream, "unknown", err)
		}
		return service.requestProviderAction(stream, providerActionResume)
	}
	if payload.Err != nil {
		if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
			newCompactionFailedEntry(payload.Plan, payload.Err),
		}); err != nil {
			return err
		}
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", payload.Err)
	}
	if payload.Plan == nil {
		return fmt.Errorf("pending compaction plan is missing")
	}
	if err := service.applyCompactionPlan(stream, stream.ConversationID, payload.Plan, payload.SummaryText); err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	if err := service.syncSummaryCarryForward(stream.ConversationID, stream.RequestID, stream.CurrentModelCallID); err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	if err := service.broker.Publish(stream.RequestID, StreamEvent{
		Message: buildSummaryMessage(payload.SummaryText),
	}); err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	if err := service.publishCheckpoint(stream.RequestID, stream.ConversationID); err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	if err := service.publishSummaryCompleted(stream, firstNonEmpty(payload.Plan.HookMessage, "Conversation context compacted.")); err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	if payload.Plan.Trigger == "manual" {
		if err := service.completeManualCompactionTurn(stream); err != nil {
			return service.failStream(stream, "unknown", err)
		}
		if err := service.broker.Publish(stream.RequestID, StreamEvent{
			Message: buildTurnEndedMessage(0, 0, 0, 0),
		}); err != nil {
			return service.failStream(stream, "unknown", err)
		}
		if err := service.broker.Complete(stream.RequestID, "", ""); err != nil {
			return service.failStream(stream, "unknown", err)
		}
		service.setTurnPhase(stream, TurnPhaseCompleted)
		// manual compaction 也是当前 turn 的终态：释放 conversation owner，
		// 推进队列后继。失败分支已走 failStream（由 failActiveStream 统一释放）。
		service.finishConversationTurn(stream.ConversationID, stream.RequestID)
		return nil
	}
	return service.requestProviderAction(stream, providerActionResume)
}

func (service *Service) finishCompactionWithError(stream *ActiveStream, cancel context.CancelFunc, err error) {
	if cancel != nil {
		cancel()
	}
	if stream == nil {
		return
	}
	stream.mu.Lock()
	stream.ProviderActive = false
	stream.ProviderCancel = nil
	stream.PendingCompaction = nil
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	terminalCode := "unknown"
	var coded interface{ TerminalCode() string }
	if errors.As(err, &coded) && strings.TrimSpace(coded.TerminalCode()) != "" {
		terminalCode = strings.TrimSpace(coded.TerminalCode())
	}
	_ = service.failStream(stream, terminalCode, err)
}

func (service *Service) finishManualCompactionNoop(stream *ActiveStream) error {
	if service == nil || stream == nil {
		return nil
	}
	if err := service.broker.Publish(stream.RequestID, StreamEvent{
		Message: buildSummaryStartedMessage(),
	}); err != nil {
		return err
	}
	if err := service.publishCheckpoint(stream.RequestID, stream.ConversationID); err != nil {
		return err
	}
	if err := service.publishSummaryCompleted(stream, "Nothing to compact."); err != nil {
		return err
	}
	if err := service.completeManualCompactionTurn(stream); err != nil {
		return err
	}
	if err := service.broker.Publish(stream.RequestID, StreamEvent{
		Message: buildTurnEndedMessage(0, 0, 0, 0),
	}); err != nil {
		return err
	}
	if err := service.broker.Complete(stream.RequestID, "", ""); err != nil {
		return err
	}
	// manual compaction no-op 也是当前 turn 的终态：释放 conversation owner，
	// 推进队列后继。
	service.finishConversationTurn(stream.ConversationID, stream.RequestID)
	return nil
}

func (service *Service) completeManualCompactionTurn(stream *ActiveStream) error {
	if service == nil || stream == nil {
		return nil
	}
	requestID := strings.TrimSpace(stream.RequestID)
	conversationID := strings.TrimSpace(stream.ConversationID)
	turnSeq := stream.TurnSeq
	modelCallID := "turn:" + requestID
	if conversationID == "" {
		return nil
	}
	if _, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		newMetadataEntry(turnSeq, requestID, "turn_completed", map[string]any{
			"model_call_id": modelCallID,
			"provider_call": false,
		}),
	}); err != nil {
		return err
	}
	if err := service.syncSummaryCarryForward(conversationID, requestID, modelCallID); err != nil {
		return err
	}
	service.setTurnPhase(stream, TurnPhaseCompleted)
	return nil
}

func (service *Service) publishSummaryCompleted(stream *ActiveStream, hookMessage string) error {
	if service == nil || stream == nil {
		return nil
	}
	thought := strings.TrimSpace(hookMessage)
	if thought == "" {
		thought = defaultSummaryCompletedThought
	}
	if strings.TrimSpace(stream.ConversationID) != "" {
		if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
			newMetadataEntry(stream.TurnSeq, stream.RequestID, "thought_annotation", map[string]any{
				"kind":    "summary_completed",
				"thought": thought,
			}),
		}); err != nil {
			return err
		}
		if err := service.syncSummaryCarryForward(stream.ConversationID, stream.RequestID, stream.CurrentModelCallID); err != nil {
			return err
		}
	}
	return service.broker.Publish(stream.RequestID, StreamEvent{
		Message: buildSummaryCompletedMessage(stream.RequestID),
	})
}

func (service *Service) applyCompactionPlan(stream *ActiveStream, conversationID string, plan *PendingCompaction, summaryText string) error {
	if service == nil || stream == nil || service.compiler == nil || plan == nil {
		return nil
	}
	candidateConversation, err := service.snapshotCompactionCandidate(stream, conversationID)
	if err != nil {
		return err
	}
	if err := applyCompactionToConversation(candidateConversation, plan, summaryText); err != nil {
		return err
	}
	latestUserText := ""
	if plan.PreserveCurrentTurnInputs {
		latestUserText = plan.CurrentUserText
	}
	recompiled, err := service.compiler.Compile(candidateConversation, stream.Mode, latestUserText, stream.ModelName, stream.CustomSystemPrompt, stream.Goal != nil)
	if err != nil {
		return err
	}
	if validationErr := validateCompactionCandidateBudget(recompiled, plan); validationErr != nil {
		return validationErr
	}
	replacementEntries := append([]HistoryEntry(nil), candidateConversation.Entries...)
	if service.store != nil {
		persisted, err := service.store.ReplaceEntries(conversationID, replacementEntries, func(item *ConversationFile) error {
			if item == nil {
				return nil
			}
			item.TokenDetailsUsedTokens = 0
			clearConversationAutoCompactionState(item)
			return nil
		})
		if err != nil {
			return err
		}
		stream.mu.Lock()
		stream.CheckpointConversation = persisted
		stream.UpdatedAt = time.Now().UTC()
		stream.mu.Unlock()
		return nil
	}
	_, err = service.updateConversationMetaAndCheckpoint(stream, conversationID, func(item *ConversationFile) error {
		if item == nil {
			return nil
		}
		item.Entries = nil
		item.NextEntrySeq = 1
		item.NextTurnSeq = 1
		appendEntriesInPlace(item, resetEntrySequences(replacementEntries))
		item.TokenDetailsUsedTokens = 0
		clearConversationAutoCompactionState(item)
		return nil
	})
	return err
}

func (service *Service) snapshotCompactionCandidate(stream *ActiveStream, conversationID string) (*ConversationFile, error) {
	if service == nil {
		return nil, nil
	}
	candidate, _, _, err := service.snapshotCheckpointConversation(stream)
	if err == nil && candidate != nil {
		return candidate, nil
	}
	if service.recorder != nil {
		latestState, ok, loadErr := service.loadLatestSummaryState(conversationID)
		if loadErr != nil {
			return nil, loadErr
		}
		if ok && latestState != nil && latestState.RuntimeSnapshot != nil {
			return cloneConversationFile(latestState.RuntimeSnapshot), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("conversation %q not found for compaction", strings.TrimSpace(conversationID))
}

type compactionTerminalError struct {
	code    string
	message string
}

func (err compactionTerminalError) Error() string {
	if strings.TrimSpace(err.message) != "" {
		return strings.TrimSpace(err.message)
	}
	if strings.TrimSpace(err.code) != "" {
		return strings.TrimSpace(err.code)
	}
	return "compaction failed"
}

func (err compactionTerminalError) TerminalCode() string {
	return strings.TrimSpace(err.code)
}

func (service *Service) buildCompactionSummaryMessages(plan *PendingCompaction) ([]modeladapter.Message, error) {
	if plan == nil {
		return nil, nil
	}
	systemText, err := promptassets.ReadCompactionPrompt()
	if err != nil {
		return nil, err
	}
	systemText = strings.TrimSpace(systemText)
	if systemText == "" {
		return nil, fmt.Errorf("compaction prompt asset is empty")
	}
	sections := make([]string, 0, len(plan.CompactedTurns)+4)
	if strings.TrimSpace(plan.ExistingSummary) != "" {
		sections = append(sections, "Existing summary:\n"+strings.TrimSpace(plan.ExistingSummary))
	}
	lines := make([]string, 0, len(plan.CompactedTurns))
	for index, item := range plan.CompactedTurns {
		parts := make([]string, 0, 1+len(item.Steps))
		if strings.TrimSpace(item.UserText) != "" {
			parts = append(parts, "user="+strings.TrimSpace(item.UserText))
		}
		parts = append(parts, item.Steps...)
		if len(parts) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("%d. %s", index+1, strings.Join(parts, " | ")))
	}
	if len(lines) > 0 {
		sections = append(sections, "History to compact:\n"+strings.Join(lines, "\n"))
	}
	if strings.TrimSpace(plan.HookMessage) != "" {
		sections = append(sections, "Pre-compact hook guidance:\n"+strings.TrimSpace(plan.HookMessage))
	}
	if strings.TrimSpace(plan.ManualInstruction) != "" {
		sections = append(sections, "User emphasis for this manual compact:\n"+strings.TrimSpace(plan.ManualInstruction))
	}
	sections = append(sections, "Return only the replacement summary text.")
	return []modeladapter.Message{
		{Role: "system", Content: systemText},
		{Role: "user", Content: strings.Join(sections, "\n\n")},
	}, nil
}

func (service *Service) generateCompactionSummary(ctx context.Context, stream *ActiveStream, plan *PendingCompaction, modelCallID string) (string, error) {
	if service == nil || stream == nil || plan == nil {
		return "", nil
	}
	messages, err := service.buildCompactionSummaryMessages(plan)
	if err != nil {
		return "", err
	}
	if len(messages) == 0 {
		return "", nil
	}
	accumulated := ""
	usage := turnUsageSnapshot{}
	observer := modeladapter.LLMArtifactObserver(service.recorder)
	if plan.Trigger == contextProjectionTrigger {
		observer = contextProjectionArtifactObserver{}
	}
	err = service.provider.StartStream(ctx, ProviderRequest{
		RequestID:      stream.RequestID,
		ConversationID: stream.ConversationID,
		RunID:          stream.RequestID,
		ModelCallID:    modelCallID,
		ModelID:        stream.ModelID,
		Mode:           agentv1.AgentMode_AGENT_MODE_AGENT,
		Messages:       messages,
		MaxTokens:      compactionSummaryOutputMaxTokens,
		CompileSummary: fmt.Sprintf("compaction trigger=%s source=%s turns=%d messages=%d", plan.Trigger, plan.RequestSource, plan.CompactTurnCount, plan.MessagesToCompact),
		Observer:       observer,
		ArtifactPaths:  &modeladapter.LLMArtifactPaths{},
	}, func(event modeladapter.ModelEvent) error {
		if err := providerLoopInterruptErr(ctx, stream, modelCallID); err != nil {
			return err
		}
		switch event.Kind {
		case modeladapter.ModelEventKindTextDelta:
			accumulated += event.Text
			if plan.Trigger == contextProjectionTrigger {
				return nil
			}
			if strings.TrimSpace(accumulated) == "" {
				return nil
			}
			return service.broker.Publish(stream.RequestID, StreamEvent{
				Message: buildSummaryMessage(accumulated),
			})
		case modeladapter.ModelEventKindThinkingDelta, modeladapter.ModelEventKindThinkingCompleted:
			return nil
		case modeladapter.ModelEventKindToolLikeCompleted:
			return fmt.Errorf("compaction summary generation must not invoke tools")
		case modeladapter.ModelEventKindTurnFinished:
			usage = turnUsageSnapshot{
				Provider:          event.Provider,
				Model:             event.Model,
				InputTokens:       event.InputTokens,
				OutputTokens:      event.OutputTokens,
				CacheReadTokens:   event.CacheReadTokens,
				CacheWriteTokens:  event.CacheWriteTokens,
				UsagePresent:      event.UsagePresent,
				CacheReadPresent:  event.CacheReadPresent,
				CacheWritePresent: event.CacheWritePresent,
			}
			return nil
		case modeladapter.ModelEventKindProviderError:
			if event.Err != nil {
				return providerTerminalError{cause: event.Err}
			}
			return providerTerminalError{cause: fmt.Errorf("provider error")}
		default:
			return nil
		}
	})
	if err != nil {
		if !errors.Is(err, errProviderLoopInterrupted) {
			stream.mu.Lock()
			conversationID := stream.ConversationID
			requestID := stream.RequestID
			turnSeq := stream.TurnSeq
			stream.mu.Unlock()
			if strings.TrimSpace(usage.ErrorCode) == "" {
				if code := extractUsageErrorCodeFromCause(err); code != "" {
					usage.ErrorCode = code
				} else if code := extractUsageErrorCode(err.Error()); code != "" {
					usage.ErrorCode = code
				} else {
					usage.ErrorCode = "provider_error"
				}
			}
			if usageErr := service.recordCompactionSummaryUsage(stream, plan, conversationID, turnSeq, requestID, modelCallID, "provider_error", usage, err.Error()); usageErr != nil {
				return "", fmt.Errorf("record compaction provider error usage: %w", usageErr)
			}
		}
		return "", err
	}
	stream.mu.Lock()
	conversationID := stream.ConversationID
	requestID := stream.RequestID
	turnSeq := stream.TurnSeq
	stream.mu.Unlock()
	if err := service.recordCompactionSummaryUsage(stream, plan, conversationID, turnSeq, requestID, modelCallID, "completed", usage, ""); err != nil {
		return "", fmt.Errorf("record compaction provider usage: %w", err)
	}
	return strings.TrimSpace(accumulated), nil
}

// recordCompactionSummaryUsage keeps automatic projection maintenance outside
// canonical conversation state while preserving provider usage accounting.
func (service *Service) recordCompactionSummaryUsage(stream *ActiveStream, plan *PendingCompaction, conversationID string, turnSeq int64, requestID string, modelCallID string, status string, usage turnUsageSnapshot, errorText string) error {
	if plan != nil && plan.Trigger == contextProjectionTrigger {
		return upsertStandaloneUsageSnapshot(service.usageStore, requestID, modelCallID, status, usage, errorText)
	}
	return service.recordTurnUsageSnapshot(stream, conversationID, turnSeq, requestID, modelCallID, status, usage, errorText, false)
}
