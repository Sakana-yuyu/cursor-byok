package forwarder

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
	promptengine "cursor/internal/backend/agent/prompt"
	"google.golang.org/protobuf/encoding/protojson"
)

const contextProjectionSchemaVersion = 1

const (
	contextProjectionTrigger         = "auto_projection"
	contextProjectionModeSummary     = "summary"
	contextProjectionModeRecentTail  = "recent_tail"
	contextProjectionStableHeadTurns = 1
	contextProjectionRecentTailTurns = 5
	contextProjectionHardRatio       = 0.8
)

type contextProjectionState struct {
	SchemaVersion            int       `json:"schema_version"`
	ConversationID           string    `json:"conversation_id"`
	RootConversationID       string    `json:"root_conversation_id,omitempty"`
	ParentConversationID     string    `json:"parent_conversation_id,omitempty"`
	ParentToolCallID         string    `json:"parent_tool_call_id,omitempty"`
	ModelKey                 string    `json:"model_key"`
	Mode                     string    `json:"mode,omitempty"`
	ContextVersion           int64     `json:"context_version"`
	SummaryStartEntrySeq     int64     `json:"summary_start_entry_seq"`
	CoveredEntrySeq          int64     `json:"covered_entry_seq"`
	CoveredPrefixFingerprint string    `json:"covered_prefix_fingerprint"`
	Summary                  string    `json:"summary"`
	Applied                  bool      `json:"applied,omitempty"`
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
}

func contextProjectionModelKey(stream *ActiveStream) string {
	if stream == nil {
		return ""
	}
	if modelName := strings.TrimSpace(stream.ModelName); modelName != "" {
		return modelName
	}
	return strings.TrimSpace(stream.ModelID)
}

func (service *Service) prepareConversationContextProjection(conversation *ConversationFile, modelKey string) (*ConversationFile, bool) {
	projected, _, active, _ := service.prepareConversationContextProjectionState(conversation, modelKey)
	return projected, active
}

func (service *Service) prepareConversationContextProjectionState(conversation *ConversationFile, modelKey string) (*ConversationFile, *contextProjectionState, bool, string) {
	if service == nil || service.store == nil || conversation == nil {
		return conversation, nil, false, "projection_unavailable"
	}
	state, err := service.store.LoadContextProjection(conversation.ConversationID)
	if err != nil {
		return conversation, nil, false, "sidecar_load_failed"
	}
	if state == nil {
		return conversation, nil, false, "sidecar_missing"
	}
	if valid, reason := validateContextProjectionState(state, conversation, modelKey); !valid {
		return conversation, nil, false, reason
	}
	projected, err := projectConversationWithContextProjection(conversation, state, modelKey)
	if err != nil {
		return conversation, nil, false, "projection_compile_failed"
	}
	return projected, state, true, ""
}

func contextProjectionStableMessageCount(state *contextProjectionState, compiledStable int) int {
	if state == nil || !state.Applied || compiledStable <= 0 {
		return 0
	}
	return compiledStable
}

// contextProjectionRequestDiagnostics describes the final provider-visible
// request. It is deliberately request metadata only: callers may put it in
// debug logs and RequestKnobs without changing cache or history semantics.
func contextProjectionRequestDiagnostics(executionKind string, conversation *ConversationFile, state *contextProjectionState, sidecarHit bool, invalidationReason string, before CompiledConversation, after CompiledConversation, contextWindowTokens int64, reserveTokens int64, outputBudgetTokens int, overflowRetryOrdinal int, overflowRetryRatio float64) map[string]any {
	mode := "full"
	if state != nil {
		mode = contextProjectionStateMode(state)
	}
	fields := map[string]any{
		"execution_kind":         strings.TrimSpace(executionKind),
		"mode":                   mode,
		"sidecar_hit":            sidecarHit,
		"context_window_tokens":  contextWindowTokens,
		"reserve_tokens":         reserveTokens,
		"input_tokens_before":    estimateCompiledPromptTokens(before),
		"input_tokens_after":     estimateCompiledPromptTokens(after),
		"output_budget_tokens":   outputBudgetTokens,
		"before_message_count":   len(before.Messages),
		"after_message_count":    len(after.Messages),
		"stable_count_before":    before.StableMessageCount,
		"stable_count_after":     after.StableMessageCount,
		"overflow_retry_ordinal": overflowRetryOrdinal,
		"overflow_retry_ratio":   overflowRetryRatio,
	}
	if state != nil {
		fields["covered_entry_seq"] = state.CoveredEntrySeq
		fields["covered_prefix_fingerprint"] = state.CoveredPrefixFingerprint
		fields["summary_start_entry_seq"] = state.SummaryStartEntrySeq
	}
	if reason := strings.TrimSpace(invalidationReason); reason != "" {
		fields["sidecar_invalidation_reason"] = reason
	}
	if conversation != nil {
		fields["context_version"] = conversation.ContextVersion
		if state != nil {
			if turns, err := contextProjectionTurns(conversation); err == nil {
				droppedTurns := 0
				for _, turn := range turns {
					if turn.StartEntrySeq >= state.SummaryStartEntrySeq && turn.EndEntrySeq <= state.CoveredEntrySeq {
						droppedTurns++
					}
				}
				fields["dropped_turns"] = droppedTurns
				fields["retained_turns"] = len(turns) - droppedTurns
			}
		}
	}
	return fields
}

func (service *Service) markContextProjectionApplied(conversation *ConversationFile, expected *contextProjectionState) error {
	if service == nil || service.store == nil || conversation == nil || expected == nil {
		return nil
	}
	current, err := service.store.LoadContextProjection(conversation.ConversationID)
	if err != nil {
		return err
	}
	if current == nil {
		return fmt.Errorf("context projection disappeared before provider request")
	}
	if valid, reason := validateContextProjectionState(current, conversation, expected.ModelKey); !valid {
		return fmt.Errorf("context projection changed before provider request: %s", reason)
	}
	if current.SummaryStartEntrySeq != expected.SummaryStartEntrySeq ||
		current.CoveredEntrySeq != expected.CoveredEntrySeq ||
		current.CoveredPrefixFingerprint != expected.CoveredPrefixFingerprint ||
		current.Summary != expected.Summary ||
		!current.UpdatedAt.Equal(expected.UpdatedAt) {
		return fmt.Errorf("context projection was replaced before provider request")
	}
	if current.Applied {
		return nil
	}
	current.Applied = true
	return service.store.SaveContextProjection(conversation.ConversationID, current)
}

func (service *Service) completeContextProjectionSummary(conversation *ConversationFile, plan *PendingCompaction, summary string) error {
	if service == nil || service.store == nil {
		return fmt.Errorf("context projection store is unavailable")
	}
	if conversation == nil || plan == nil || plan.Trigger != contextProjectionTrigger {
		return fmt.Errorf("context projection completion metadata is invalid")
	}
	if strings.TrimSpace(plan.ProjectionConversationID) != strings.TrimSpace(conversation.ConversationID) ||
		strings.TrimSpace(plan.ProjectionRootConversationID) != strings.TrimSpace(conversation.RootConversationID) ||
		strings.TrimSpace(plan.ProjectionParentConversationID) != strings.TrimSpace(conversation.ParentConversationID) ||
		strings.TrimSpace(plan.ProjectionParentToolCallID) != strings.TrimSpace(conversation.ParentToolCallID) {
		return fmt.Errorf("context projection lineage changed while summary was generated")
	}
	state, err := newContextProjectionState(
		conversation,
		plan.ProjectionModelKey,
		plan.ProjectionSummaryStartEntrySeq,
		plan.ProjectionCoveredEntrySeq,
		summary,
	)
	if err != nil {
		return err
	}
	if state.CoveredPrefixFingerprint != strings.TrimSpace(plan.ProjectionCoveredPrefixFingerprint) {
		return fmt.Errorf("context projection covered prefix changed while summary was generated")
	}
	if existing, loadErr := service.store.LoadContextProjection(conversation.ConversationID); loadErr != nil {
		return loadErr
	} else if existing != nil && !existing.CreatedAt.IsZero() {
		state.CreatedAt = existing.CreatedAt
	}
	return service.store.SaveContextProjection(conversation.ConversationID, state)
}

func newContextProjectionState(conversation *ConversationFile, modelKey string, summaryStartEntrySeq int64, coveredEntrySeq int64, summary string) (*contextProjectionState, error) {
	if conversation == nil {
		return nil, fmt.Errorf("conversation is required")
	}
	conversationID := strings.TrimSpace(conversation.ConversationID)
	if conversationID == "" {
		return nil, fmt.Errorf("conversation_id is required")
	}
	modelKey = strings.TrimSpace(modelKey)
	if modelKey == "" {
		return nil, fmt.Errorf("model key is required")
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil, fmt.Errorf("projection summary is required")
	}
	if summaryStartEntrySeq <= 0 || coveredEntrySeq < summaryStartEntrySeq {
		return nil, fmt.Errorf("invalid projection entry range %d..%d", summaryStartEntrySeq, coveredEntrySeq)
	}
	fingerprint, found := contextProjectionCoveredPrefixFingerprint(conversation.Entries, coveredEntrySeq)
	if !found {
		return nil, fmt.Errorf("covered entry seq %d is not present", coveredEntrySeq)
	}
	if !contextProjectionEntrySeqExists(conversation.Entries, summaryStartEntrySeq) {
		return nil, fmt.Errorf("summary start entry seq %d is not present", summaryStartEntrySeq)
	}
	if reason, err := contextProjectionEntryRangeBoundaryReason(conversation, summaryStartEntrySeq, coveredEntrySeq); err != nil {
		return nil, fmt.Errorf("validate projection structural boundaries: %w", err)
	} else if reason != "" {
		return nil, fmt.Errorf("invalid projection structural range %d..%d: %s", summaryStartEntrySeq, coveredEntrySeq, reason)
	}
	now := time.Now().UTC()
	return &contextProjectionState{
		SchemaVersion:            contextProjectionSchemaVersion,
		ConversationID:           conversationID,
		RootConversationID:       strings.TrimSpace(conversation.RootConversationID),
		ParentConversationID:     strings.TrimSpace(conversation.ParentConversationID),
		ParentToolCallID:         strings.TrimSpace(conversation.ParentToolCallID),
		ModelKey:                 modelKey,
		Mode:                     contextProjectionModeSummary,
		ContextVersion:           conversation.ContextVersion,
		SummaryStartEntrySeq:     summaryStartEntrySeq,
		CoveredEntrySeq:          coveredEntrySeq,
		CoveredPrefixFingerprint: fingerprint,
		Summary:                  summary,
		CreatedAt:                now,
		UpdatedAt:                now,
	}, nil
}

func validateContextProjectionState(state *contextProjectionState, conversation *ConversationFile, modelKey string) (bool, string) {
	if state == nil {
		return false, "projection_missing"
	}
	if state.SchemaVersion != contextProjectionSchemaVersion {
		return false, "schema_version_mismatch"
	}
	if conversation == nil || strings.TrimSpace(state.ConversationID) != strings.TrimSpace(conversation.ConversationID) {
		return false, "conversation_mismatch"
	}
	if strings.TrimSpace(state.RootConversationID) != strings.TrimSpace(conversation.RootConversationID) ||
		strings.TrimSpace(state.ParentConversationID) != strings.TrimSpace(conversation.ParentConversationID) ||
		strings.TrimSpace(state.ParentToolCallID) != strings.TrimSpace(conversation.ParentToolCallID) {
		return false, "lineage_mismatch"
	}
	if strings.TrimSpace(state.ModelKey) != strings.TrimSpace(modelKey) {
		return false, "model_mismatch"
	}
	if state.ContextVersion > conversation.ContextVersion {
		return false, "context_version_ahead"
	}
	mode := contextProjectionStateMode(state)
	if mode != contextProjectionModeSummary && mode != contextProjectionModeRecentTail {
		return false, "mode_invalid"
	}
	if mode == contextProjectionModeSummary && strings.TrimSpace(state.Summary) == "" {
		return false, "summary_missing"
	}
	if mode == contextProjectionModeRecentTail && strings.TrimSpace(state.Summary) != "" {
		return false, "recent_tail_summary_present"
	}
	if state.SummaryStartEntrySeq <= 0 || !contextProjectionEntrySeqExists(conversation.Entries, state.SummaryStartEntrySeq) {
		return false, "summary_start_missing"
	}
	fingerprint, found := contextProjectionCoveredPrefixFingerprint(conversation.Entries, state.CoveredEntrySeq)
	if !found {
		return false, "covered_prefix_missing"
	}
	if fingerprint != strings.TrimSpace(state.CoveredPrefixFingerprint) {
		return false, "covered_prefix_mismatch"
	}
	if reason, err := contextProjectionEntryRangeBoundaryReason(conversation, state.SummaryStartEntrySeq, state.CoveredEntrySeq); err != nil {
		return false, "structural_groups_invalid"
	} else if reason != "" {
		return false, reason
	}
	return true, ""
}

func contextProjectionStateMode(state *contextProjectionState) string {
	if state == nil {
		return ""
	}
	if mode := strings.TrimSpace(state.Mode); mode != "" {
		return mode
	}
	return contextProjectionModeSummary
}

func contextProjectionEntrySeqExists(entries []HistoryEntry, seq int64) bool {
	for _, entry := range entries {
		if entry.Seq == seq {
			return true
		}
	}
	return false
}

func contextProjectionEntryRangeBoundaryReason(conversation *ConversationFile, summaryStartEntrySeq int64, coveredEntrySeq int64) (string, error) {
	if conversation == nil {
		return "entry_range_invalid", nil
	}
	if summaryStartEntrySeq <= 0 || coveredEntrySeq < summaryStartEntrySeq {
		return "entry_range_invalid", nil
	}
	turns, err := contextProjectionTurns(conversation)
	if err != nil {
		return "", err
	}
	validStarts := make(map[int64]struct{}, len(turns)+1)
	validEnds := make(map[int64]struct{}, len(turns)+1)
	for _, entry := range replayablePromptProjectionEntries(conversation.Entries) {
		if !isCompactionSummaryKind(entry.Kind) {
			continue
		}
		validStarts[entry.Seq] = struct{}{}
		validEnds[entry.Seq] = struct{}{}
	}
	for _, turn := range turns {
		validStarts[turn.StartEntrySeq] = struct{}{}
		validEnds[turn.EndEntrySeq] = struct{}{}
	}
	if _, ok := validStarts[summaryStartEntrySeq]; !ok {
		return "summary_start_boundary_invalid", nil
	}
	if _, ok := validEnds[coveredEntrySeq]; !ok {
		return "covered_boundary_invalid", nil
	}
	for _, turn := range turns {
		if turn.EndEntrySeq < summaryStartEntrySeq || turn.StartEntrySeq > coveredEntrySeq {
			continue
		}
		if turn.StartEntrySeq < summaryStartEntrySeq || turn.EndEntrySeq > coveredEntrySeq {
			return "structural_group_partially_covered", nil
		}
		if strings.TrimSpace(turn.InvalidReason) != "" {
			return "structural_group_invalid", nil
		}
	}
	return "", nil
}

func contextProjectionCoveredPrefixFingerprint(entries []HistoryEntry, coveredEntrySeq int64) (string, bool) {
	if coveredEntrySeq <= 0 {
		return "", false
	}
	prefix := make([]HistoryEntry, 0, len(entries))
	found := false
	for _, entry := range entries {
		if entry.Seq > coveredEntrySeq {
			continue
		}
		prefix = append(prefix, entry)
		if entry.Seq == coveredEntrySeq {
			found = true
		}
	}
	if !found {
		return "", false
	}
	encoded, err := json.Marshal(prefix)
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), true
}

type contextProjectionTurn struct {
	TurnSeq         int64
	StartEntrySeq   int64
	EndEntrySeq     int64
	Summary         compactedTurnSummary
	ReplayCount     int32
	EstimatedTokens int64
	InvalidReason   string
}

func buildContextProjectionSummaryPlan(conversation *ConversationFile, modelKey string, existing *contextProjectionState, contextTokens int64, contextWindowTokens int64, reserveTokens int64) (*compactionPlan, error) {
	return buildContextProjectionSummaryPlanWithRecentTail(
		conversation,
		modelKey,
		existing,
		contextTokens,
		contextWindowTokens,
		reserveTokens,
		contextProjectionRecentTailTurns,
		false,
	)
}

func buildContextProjectionSummaryPlanWithRecentTail(conversation *ConversationFile, modelKey string, existing *contextProjectionState, contextTokens int64, contextWindowTokens int64, reserveTokens int64, recentTailTurns int, force bool) (*compactionPlan, error) {
	if conversation == nil || contextWindowTokens <= 0 {
		return nil, nil
	}
	modelKey = strings.TrimSpace(modelKey)
	if modelKey == "" {
		return nil, fmt.Errorf("model key is required")
	}
	hardBudget := int64(float64(contextWindowTokens)*contextProjectionHardRatio) - reserveTokens
	if hardBudget < 1 {
		hardBudget = 1
	}
	if !force && contextTokens <= hardBudget {
		return nil, nil
	}

	turns, err := contextProjectionTurns(conversation)
	if err != nil {
		return nil, err
	}
	if recentTailTurns < 1 {
		recentTailTurns = 1
	}
	canonicalSummaryEntry, canonicalSummary, hasCanonicalSummary := contextProjectionCanonicalSummary(conversation)
	eligibleStart := 0
	summaryStartEntrySeq := int64(0)
	existingSummary := ""
	if existing != nil {
		valid, reason := validateContextProjectionState(existing, conversation, modelKey)
		if !valid {
			return nil, fmt.Errorf("existing context projection is invalid: %s", reason)
		}
		summaryStartEntrySeq = existing.SummaryStartEntrySeq
		if contextProjectionStateMode(existing) == contextProjectionModeSummary {
			existingSummary = strings.TrimSpace(existing.Summary)
			eligibleStart = firstContextProjectionTurnAfter(turns, existing.CoveredEntrySeq)
		} else {
			eligibleStart = firstContextProjectionTurnAtOrAfter(turns, summaryStartEntrySeq)
			if hasCanonicalSummary && canonicalSummaryEntry.Seq >= summaryStartEntrySeq && canonicalSummaryEntry.Seq <= existing.CoveredEntrySeq {
				existingSummary = canonicalSummary
			}
		}
	} else if hasCanonicalSummary {
		summaryStartEntrySeq = canonicalSummaryEntry.Seq
		existingSummary = canonicalSummary
	} else if len(turns) > 0 && turns[0].TurnSeq <= 0 {
		summaryStartEntrySeq = turns[0].StartEntrySeq
	} else {
		eligibleStart = contextProjectionStableHeadTurns
		if eligibleStart >= len(turns) {
			return nil, nil
		}
		summaryStartEntrySeq = turns[eligibleStart].StartEntrySeq
	}
	eligibleEnd := len(turns) - recentTailTurns
	if eligibleStart >= eligibleEnd {
		return nil, nil
	}

	reclaimTokens := contextTokens - hardBudget
	selected := make([]contextProjectionTurn, 0, eligibleEnd-eligibleStart)
	reclaimed := int64(0)
	for index := eligibleStart; index < eligibleEnd; index++ {
		if reason := strings.TrimSpace(turns[index].InvalidReason); reason != "" {
			return nil, fmt.Errorf("cannot summarize turn %d: %s", turns[index].TurnSeq, reason)
		}
		selected = append(selected, turns[index])
		reclaimed += turns[index].EstimatedTokens
		if reclaimed >= reclaimTokens {
			break
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	coveredEntrySeq := selected[len(selected)-1].EndEntrySeq
	fingerprint, found := contextProjectionCoveredPrefixFingerprint(conversation.Entries, coveredEntrySeq)
	if !found {
		return nil, fmt.Errorf("projection covered entry seq %d is missing", coveredEntrySeq)
	}
	compactedTurns := make([]compactedTurnSummary, 0, len(selected))
	messagesToCompact := int32(0)
	for _, turn := range selected {
		compactedTurns = append(compactedTurns, turn.Summary)
		messagesToCompact += turn.ReplayCount
	}
	usagePercent := 0.0
	if contextWindowTokens > 0 {
		usagePercent = float64(contextTokens) / float64(contextWindowTokens)
	}
	return &compactionPlan{
		Trigger:                            contextProjectionTrigger,
		ContextTokens:                      contextTokens,
		ContextWindowSize:                  contextWindowTokens,
		ContextUsagePercent:                usagePercent,
		ReserveTokens:                      reserveTokens,
		MessageCount:                       clampInt64ToInt32(int64(len(conversation.Entries))),
		MessagesToCompact:                  messagesToCompact,
		CompactTurnCount:                   clampInt64ToInt32(int64(len(selected))),
		IsFirstCompaction:                  existing == nil && !hasCanonicalSummary,
		ExistingSummary:                    existingSummary,
		CompactedTurns:                     compactedTurns,
		RequestSource:                      "context_projection",
		CurrentTurnSeq:                     conversation.CurrentTurnSeq,
		CurrentRequestID:                   strings.TrimSpace(conversation.CurrentRequestID),
		PreserveCurrentTurnInputs:          true,
		ProjectionConversationID:           strings.TrimSpace(conversation.ConversationID),
		ProjectionRootConversationID:       strings.TrimSpace(conversation.RootConversationID),
		ProjectionParentConversationID:     strings.TrimSpace(conversation.ParentConversationID),
		ProjectionParentToolCallID:         strings.TrimSpace(conversation.ParentToolCallID),
		ProjectionModelKey:                 modelKey,
		ProjectionContextVersion:           conversation.ContextVersion,
		ProjectionSummaryStartEntrySeq:     summaryStartEntrySeq,
		ProjectionCoveredEntrySeq:          coveredEntrySeq,
		ProjectionCoveredPrefixFingerprint: fingerprint,
	}, nil
}

func firstContextProjectionTurnAfter(turns []contextProjectionTurn, coveredEntrySeq int64) int {
	for index, turn := range turns {
		if turn.EndEntrySeq > coveredEntrySeq {
			return index
		}
	}
	return len(turns)
}

func firstContextProjectionTurnAtOrAfter(turns []contextProjectionTurn, startEntrySeq int64) int {
	for index, turn := range turns {
		if turn.EndEntrySeq >= startEntrySeq {
			return index
		}
	}
	return len(turns)
}

func contextProjectionCanonicalSummary(conversation *ConversationFile) (HistoryEntry, string, bool) {
	if conversation == nil {
		return HistoryEntry{}, "", false
	}
	entries := replayablePromptProjectionEntries(conversation.Entries)
	for index := len(entries) - 1; index >= 0; index-- {
		if !isCompactionSummaryKind(entries[index].Kind) {
			continue
		}
		if summary, ok := decodeCompactionSummaryEntry(entries[index]); ok {
			return entries[index], summary, true
		}
	}
	return HistoryEntry{}, "", false
}

func contextProjectionTurns(conversation *ConversationFile) ([]contextProjectionTurn, error) {
	if conversation == nil {
		return nil, nil
	}
	entries := replayablePromptProjectionEntries(conversation.Entries)
	turns := make([]contextProjectionTurn, 0)
	imported, err := contextProjectionImportedPrehistory(entries)
	if err != nil {
		return nil, err
	}
	if imported != nil {
		turns = append(turns, *imported)
	}
	turnOrder := make([]int64, 0)
	grouped := make(map[int64][]HistoryEntry)
	for _, entry := range entries {
		if entry.TurnSeq <= 0 || isCompactionSummaryKind(entry.Kind) {
			continue
		}
		if _, ok := grouped[entry.TurnSeq]; !ok {
			turnOrder = append(turnOrder, entry.TurnSeq)
		}
		grouped[entry.TurnSeq] = append(grouped[entry.TurnSeq], entry)
	}
	for _, turnSeq := range turnOrder {
		turnEntries := grouped[turnSeq]
		if len(turnEntries) == 0 {
			continue
		}
		invalidReason := validateContextProjectionTurnToolChain(turnEntries)
		if invalidReason != "" {
			if isHistoricalInterruptedToolChain(invalidReason, turnSeq, conversation.CurrentTurnSeq) {
				turns = append(turns, summarizeInterruptedContextProjectionTurn(turnSeq, turnEntries, invalidReason))
				continue
			}
			turns = append(turns, contextProjectionTurn{
				TurnSeq:       turnSeq,
				StartEntrySeq: turnEntries[0].Seq,
				EndEntrySeq:   turnEntries[len(turnEntries)-1].Seq,
				InvalidReason: invalidReason,
			})
			continue
		}
		turnConversation := &ConversationFile{Entries: append([]HistoryEntry(nil), turnEntries...)}
		replayMessages, err := NewHistoryProjector().ProjectPromptReplay(turnConversation)
		if err != nil {
			return nil, fmt.Errorf("project context projection turn %d: %w", turnSeq, err)
		}
		if len(replayMessages) == 0 {
			continue
		}
		turns = append(turns, contextProjectionTurn{
			TurnSeq:         turnSeq,
			StartEntrySeq:   turnEntries[0].Seq,
			EndEntrySeq:     turnEntries[len(turnEntries)-1].Seq,
			Summary:         summarizeContextProjectionReplay(replayMessages),
			ReplayCount:     clampInt64ToInt32(int64(len(replayMessages))),
			EstimatedTokens: estimateModelMessagesTokens(replayMessages),
		})
	}
	return turns, nil
}

func isHistoricalInterruptedToolChain(reason string, turnSeq int64, currentTurnSeq int64) bool {
	return turnSeq > 0 && turnSeq < currentTurnSeq && strings.HasPrefix(strings.TrimSpace(reason), "incomplete tool chain for call ")
}

func summarizeInterruptedContextProjectionTurn(turnSeq int64, entries []HistoryEntry, reason string) contextProjectionTurn {
	turn := contextProjectionTurn{
		TurnSeq:       turnSeq,
		StartEntrySeq: entries[0].Seq,
		EndEntrySeq:   entries[len(entries)-1].Seq,
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.Kind) != "user_message" {
			continue
		}
		userMessage := &agentv1.UserMessage{}
		if err := protojson.Unmarshal(entry.Payload, userMessage); err == nil {
			if message, ok := promptengine.BuildUserMessageReplayMessage(userMessage); ok {
				turn.Summary.UserText = strings.TrimSpace(message.Content)
			}
		}
		break
	}
	turn.Summary.Steps = []string{"[interrupted tool chain] " + strings.TrimSpace(reason) + "; no terminal tool result was recorded."}
	turn.ReplayCount = 1
	turn.EstimatedTokens = estimateModelMessagesTokens([]modeladapter.Message{{Role: "assistant", Content: turn.Summary.Steps[0]}})
	return turn
}

func contextProjectionImportedPrehistory(entries []HistoryEntry) (*contextProjectionTurn, error) {
	importedEntries := make([]HistoryEntry, 0)
	started := false
	ended := false
	for _, entry := range entries {
		if entry.TurnSeq > 0 {
			ended = true
		}
		isImportedMessage := entry.TurnSeq <= 0 && strings.TrimSpace(entry.Kind) == "model_message"
		if !isImportedMessage {
			if started && isPromptReplayEntryKind(entry.Kind) {
				ended = true
			}
			continue
		}
		if ended {
			return nil, fmt.Errorf("imported model_message prehistory is not contiguous")
		}
		started = true
		importedEntries = append(importedEntries, entry)
	}
	if len(importedEntries) == 0 {
		return nil, nil
	}
	messages := make([]modeladapter.Message, 0, len(importedEntries))
	for _, entry := range importedEntries {
		var payload modelMessageEntryPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			return nil, fmt.Errorf("decode imported model_message entry: %w", err)
		}
		message := cloneReplayModelMessage(payload.Message)
		if strings.TrimSpace(message.Role) == "" {
			return nil, fmt.Errorf("imported model_message is missing a role")
		}
		messages = append(messages, message)
	}
	if err := validateDelegatedMessageStructure(messages); err != nil {
		return nil, fmt.Errorf("invalid imported prehistory: %w", err)
	}
	prehistoryConversation := &ConversationFile{Entries: append([]HistoryEntry(nil), importedEntries...)}
	replayMessages, err := NewHistoryProjector().ProjectPromptReplay(prehistoryConversation)
	if err != nil {
		return nil, fmt.Errorf("project imported prehistory: %w", err)
	}
	if len(replayMessages) == 0 {
		return nil, nil
	}
	return &contextProjectionTurn{
		TurnSeq:         0,
		StartEntrySeq:   importedEntries[0].Seq,
		EndEntrySeq:     importedEntries[len(importedEntries)-1].Seq,
		Summary:         summarizeContextProjectionReplay(replayMessages),
		ReplayCount:     clampInt64ToInt32(int64(len(replayMessages))),
		EstimatedTokens: estimateModelMessagesTokens(replayMessages),
	}, nil
}

func summarizeContextProjectionReplay(messages []modeladapter.Message) compactedTurnSummary {
	summary := compactedTurnSummary{}
	for _, message := range messages {
		role := firstNonEmpty(strings.TrimSpace(message.Role), "message")
		content := contextProjectionMessageSummaryText(message)
		if role == "user" && summary.UserText == "" && content != "" {
			summary.UserText = content
		} else if content != "" {
			summary.Steps = append(summary.Steps, role+"="+content)
		}
		if reasoning := truncateCompactionText(message.ReasoningContent, compactionTurnSnippetMaxChars/4); reasoning != "" {
			summary.Steps = append(summary.Steps, "thinking="+reasoning)
		}
		for _, call := range message.ToolCalls {
			name := firstNonEmpty(strings.TrimSpace(call.Function.Name), "tool")
			arguments := truncateCompactionText(call.Function.Arguments, compactionTurnSnippetMaxChars/4)
			step := name + "=called"
			if arguments != "" && arguments != "{}" {
				step += " args=" + arguments
			}
			summary.Steps = append(summary.Steps, step)
		}
	}
	return summary
}

func contextProjectionMessageSummaryText(message modeladapter.Message) string {
	parts := make([]string, 0, len(message.ContentParts)+2)
	if content := strings.TrimSpace(message.Content); content != "" {
		parts = append(parts, content)
	}
	imageCount := 0
	for _, part := range message.ContentParts {
		if modeladapter.IsImageContentPart(part) {
			imageCount++
			continue
		}
		if text := strings.TrimSpace(part.Text); text != "" && !strings.Contains(strings.Join(parts, "\n"), text) {
			parts = append(parts, text)
		}
	}
	if imageCount > 0 {
		parts = append(parts, fmt.Sprintf("[attached_images=%d]", imageCount))
	}
	return truncateCompactionText(strings.Join(parts, "\n"), compactionTurnSnippetMaxChars/3)
}

func projectConversationWithContextProjection(conversation *ConversationFile, state *contextProjectionState, modelKey string) (*ConversationFile, error) {
	valid, reason := validateContextProjectionState(state, conversation, modelKey)
	if !valid {
		return nil, fmt.Errorf("context projection is invalid: %s", reason)
	}
	projected := cloneConversationFile(conversation)
	projected.LatestRequestPrefix = nil
	projected.TokenDetailsUsedTokens = 0
	projected.Entries = make([]HistoryEntry, 0, len(conversation.Entries))
	summaryInserted := false
	insertSummary := func() {
		if summaryInserted {
			return
		}
		if contextProjectionStateMode(state) == contextProjectionModeRecentTail {
			summaryInserted = true
			return
		}
		payload, _ := json.Marshal(compactionSummaryEntryPayload{Summary: strings.TrimSpace(state.Summary), Trigger: contextProjectionTrigger})
		projected.Entries = append(projected.Entries, HistoryEntry{
			Seq:       state.SummaryStartEntrySeq,
			TurnSeq:   0,
			Role:      "system",
			Kind:      "context_projection_summary",
			Payload:   payload,
			CreatedAt: state.UpdatedAt,
		})
		summaryInserted = true
	}
	for _, entry := range conversation.Entries {
		switch {
		case entry.Seq < state.SummaryStartEntrySeq:
			projected.Entries = append(projected.Entries, entry)
		case entry.Seq <= state.CoveredEntrySeq:
			insertSummary()
			if contextProjectionStateMode(state) == contextProjectionModeRecentTail && isCompactionSummaryKind(entry.Kind) {
				projected.Entries = append(projected.Entries, entry)
				continue
			}
			if !isPromptReplayEntryKind(entry.Kind) {
				projected.Entries = append(projected.Entries, entry)
			}
		default:
			insertSummary()
			projected.Entries = append(projected.Entries, entry)
		}
	}
	insertSummary()
	return projected, nil
}

func (service *Service) completeContextProjectionRecentTailFallback(conversation *ConversationFile, plan *PendingCompaction) error {
	if service == nil || service.store == nil || conversation == nil || plan == nil || plan.Trigger != contextProjectionTrigger {
		return fmt.Errorf("context projection fallback metadata is invalid")
	}
	if strings.TrimSpace(plan.ProjectionConversationID) != strings.TrimSpace(conversation.ConversationID) ||
		strings.TrimSpace(plan.ProjectionRootConversationID) != strings.TrimSpace(conversation.RootConversationID) ||
		strings.TrimSpace(plan.ProjectionParentConversationID) != strings.TrimSpace(conversation.ParentConversationID) ||
		strings.TrimSpace(plan.ProjectionParentToolCallID) != strings.TrimSpace(conversation.ParentToolCallID) {
		return fmt.Errorf("context projection lineage changed while fallback was prepared")
	}
	fingerprint, found := contextProjectionCoveredPrefixFingerprint(conversation.Entries, plan.ProjectionCoveredEntrySeq)
	if !found || fingerprint != strings.TrimSpace(plan.ProjectionCoveredPrefixFingerprint) {
		return fmt.Errorf("context projection covered prefix changed while fallback was prepared")
	}
	if !contextProjectionEntrySeqExists(conversation.Entries, plan.ProjectionSummaryStartEntrySeq) {
		return fmt.Errorf("context projection fallback start entry is missing")
	}
	if reason, err := contextProjectionEntryRangeBoundaryReason(conversation, plan.ProjectionSummaryStartEntrySeq, plan.ProjectionCoveredEntrySeq); err != nil {
		return fmt.Errorf("validate context projection fallback structural boundaries: %w", err)
	} else if reason != "" {
		return fmt.Errorf("context projection fallback structural boundaries are invalid: %s", reason)
	}
	now := time.Now().UTC()
	state := &contextProjectionState{
		SchemaVersion:            contextProjectionSchemaVersion,
		ConversationID:           strings.TrimSpace(conversation.ConversationID),
		RootConversationID:       strings.TrimSpace(conversation.RootConversationID),
		ParentConversationID:     strings.TrimSpace(conversation.ParentConversationID),
		ParentToolCallID:         strings.TrimSpace(conversation.ParentToolCallID),
		ModelKey:                 strings.TrimSpace(plan.ProjectionModelKey),
		Mode:                     contextProjectionModeRecentTail,
		ContextVersion:           conversation.ContextVersion,
		SummaryStartEntrySeq:     plan.ProjectionSummaryStartEntrySeq,
		CoveredEntrySeq:          plan.ProjectionCoveredEntrySeq,
		CoveredPrefixFingerprint: fingerprint,
		CreatedAt:                now,
		UpdatedAt:                now,
	}
	if existing, err := service.store.LoadContextProjection(conversation.ConversationID); err != nil {
		return err
	} else if existing != nil && !existing.CreatedAt.IsZero() {
		state.CreatedAt = existing.CreatedAt
	}
	return service.store.SaveContextProjection(conversation.ConversationID, state)
}
