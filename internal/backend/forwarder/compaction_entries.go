// compaction_entries.go 承载 compaction 域的历史 entry 构造与计划纯函数：
// 应用计划、上下文 entries 构建、fallback 摘要、指令解析与计划克隆。
package forwarder

import (
	"encoding/json"
	"fmt"
	"strings"

	"cursor/gen/agentv1"
)

func applyCompactionToConversation(conversation *ConversationFile, plan *PendingCompaction, summaryText string) error {
	if conversation == nil || plan == nil {
		return nil
	}
	replacementEntries, err := buildCompactedContextEntries(conversation, plan, summaryText)
	if err != nil {
		return err
	}
	conversation.Entries = nil
	conversation.NextEntrySeq = 1
	conversation.NextTurnSeq = 1
	appendEntriesInPlace(conversation, resetEntrySequences(replacementEntries))
	conversation.TokenDetailsUsedTokens = 0
	clearConversationAutoCompactionState(conversation)
	if conversation.TokenDetailsMaxTokens == 0 {
		conversation.TokenDetailsMaxTokens = projectedConversationMaxTokens
	}
	return nil
}

func buildCompactedContextEntries(conversation *ConversationFile, plan *PendingCompaction, summaryText string) ([]HistoryEntry, error) {
	if plan == nil {
		return nil, nil
	}
	entries := []HistoryEntry{newCompactionSummaryEntry(plan, summaryText)}
	runtimeEntry, ok, err := newCompactedRuntimeStateEntry(conversation, plan)
	if err != nil {
		return nil, err
	}
	if ok {
		entries = append(entries, runtimeEntry)
	}
	if conversation == nil || !plan.PreserveCurrentTurnInputs {
		return entries, nil
	}
	entries = append(entries, buildAutoCompactionPreservedCurrentTurnEntries(conversation.Entries, plan)...)
	return entries, nil
}

func buildAutoCompactionPreservedCurrentTurnEntries(entries []HistoryEntry, plan *PendingCompaction) []HistoryEntry {
	if len(entries) == 0 || plan == nil || !plan.PreserveCurrentTurnInputs {
		return nil
	}
	latestToolCallID := latestCompletedToolCallIDForTurn(entries, plan.CurrentTurnSeq, plan.CurrentRequestID)
	preservedIndexes := autoCompactionPreservedEntryIndexes(entries, plan.CurrentTurnSeq, plan.CurrentRequestID, latestToolCallID)
	if len(preservedIndexes) == 0 {
		return nil
	}
	preserved := make([]HistoryEntry, 0, len(preservedIndexes))
	for index, entry := range entries {
		if _, ok := preservedIndexes[index]; !ok {
			continue
		}
		switch strings.TrimSpace(entry.Kind) {
		case "compaction_summary", "compacted_summary", "compaction_request":
			continue
		case "tool_result":
			if rewritten, ok := rewriteAutoCompactionToolResultEntry(entry, autoCompactionPreservedToolResultLimitBytes, false); ok {
				entry = rewritten
			}
		}
		preserved = append(preserved, entry)
	}
	return preserved
}

func newCompactionSummaryEntry(plan *PendingCompaction, summaryText string) HistoryEntry {
	payload, _ := json.Marshal(compactionSummaryEntryPayload{
		Summary:                   strings.TrimSpace(summaryText),
		Trigger:                   strings.TrimSpace(plan.Trigger),
		CurrentTurnSeq:            plan.CurrentTurnSeq,
		CurrentRequestID:          strings.TrimSpace(plan.CurrentRequestID),
		CompactTurnCount:          plan.CompactTurnCount,
		MessagesToCompact:         plan.MessagesToCompact,
		PreserveCurrentTurnInputs: plan.PreserveCurrentTurnInputs,
	})
	return HistoryEntry{
		TurnSeq:   plan.CurrentTurnSeq,
		RequestID: strings.TrimSpace(plan.CurrentRequestID),
		Role:      "system",
		Kind:      "compacted_summary",
		Payload:   payload,
	}
}

func newCompactedRuntimeStateEntry(conversation *ConversationFile, plan *PendingCompaction) (HistoryEntry, bool, error) {
	state, err := projectConversationStructuredState(conversation)
	if err != nil {
		return HistoryEntry{}, false, err
	}
	payload := runtimeStateEntryPayload{
		PlanText: state.PlanText,
		Plans:    clonePlanRegistryEntries(state.Plans),
		Todos:    cloneTodoItems(state.Todos),
	}
	if strings.TrimSpace(payload.PlanText) == "" && len(payload.Plans) == 0 && len(payload.Todos) == 0 {
		return HistoryEntry{}, false, nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return HistoryEntry{}, false, fmt.Errorf("encode compacted runtime state: %w", err)
	}
	return HistoryEntry{
		TurnSeq:   plan.CurrentTurnSeq,
		RequestID: strings.TrimSpace(plan.CurrentRequestID),
		Role:      "system",
		Kind:      "runtime_state",
		Payload:   encoded,
	}, true, nil
}

func newCompactionRequestEntry(plan *PendingCompaction) HistoryEntry {
	payload, _ := json.Marshal(map[string]any{
		"trigger":               strings.TrimSpace(plan.Trigger),
		"context_tokens":        plan.ContextTokens,
		"context_window_size":   plan.ContextWindowSize,
		"reserve_tokens":        plan.ReserveTokens,
		"messages_to_compact":   plan.MessagesToCompact,
		"compact_turn_count":    plan.CompactTurnCount,
		"request_source":        strings.TrimSpace(plan.RequestSource),
		"summary_model_call_id": strings.TrimSpace(plan.SummaryModelCallID),
	})
	return HistoryEntry{
		TurnSeq:   plan.CurrentTurnSeq,
		RequestID: strings.TrimSpace(plan.CurrentRequestID),
		Role:      "system",
		Kind:      "compaction_request",
		Payload:   payload,
	}
}

func newCompactionFailedEntry(plan *PendingCompaction, cause error) HistoryEntry {
	payload := map[string]any{
		"error": "compaction failed",
	}
	entry := HistoryEntry{
		Role: "system",
		Kind: "compaction_failed",
	}
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		payload["error"] = strings.TrimSpace(cause.Error())
	}
	if plan != nil {
		payload["trigger"] = strings.TrimSpace(plan.Trigger)
		payload["request_source"] = strings.TrimSpace(plan.RequestSource)
		payload["summary_model_call_id"] = strings.TrimSpace(plan.SummaryModelCallID)
		entry.TurnSeq = plan.CurrentTurnSeq
		entry.RequestID = strings.TrimSpace(plan.CurrentRequestID)
	}
	entry.Payload, _ = json.Marshal(payload)
	return entry
}

func buildFallbackCompactionSummary(plan *PendingCompaction) string {
	sections := []string{
		"Conversation summary",
		"Earlier context was compacted into this summary. Preserve the facts, decisions, tool results, and user intent below when continuing the conversation.",
	}
	if plan == nil {
		return strings.Join(sections, "\n\n")
	}
	if strings.TrimSpace(plan.ExistingSummary) != "" {
		sections = append(sections, "Previous summary:\n"+truncateCompactionText(plan.ExistingSummary, compactionSummaryMaxChars/4))
	}
	lines := make([]string, 0, len(plan.CompactedTurns))
	for index, item := range plan.CompactedTurns {
		parts := make([]string, 0, len(item.Steps)+1)
		if strings.TrimSpace(item.UserText) != "" {
			parts = append(parts, "user="+truncateCompactionText(item.UserText, 400))
		}
		for _, step := range item.Steps {
			if strings.TrimSpace(step) == "" {
				continue
			}
			parts = append(parts, truncateCompactionText(step, 400))
		}
		if len(parts) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("%d. %s", index+1, strings.Join(parts, " | ")))
	}
	if len(lines) > 0 {
		sections = append(sections, "Compacted turns:\n"+truncateCompactionText(strings.Join(lines, "\n"), compactionSummaryMaxChars/2))
	}
	if strings.TrimSpace(plan.HookMessage) != "" {
		sections = append(sections, "Compaction note:\n"+truncateCompactionText(plan.HookMessage, 800))
	}
	if strings.TrimSpace(plan.ManualInstruction) != "" {
		sections = append(sections, "Manual summarize instruction:\n"+truncateCompactionText(plan.ManualInstruction, 800))
	}
	return strings.TrimSpace(truncateCompactionText(strings.Join(sections, "\n\n"), compactionSummaryMaxChars))
}

func validateCompactionCandidateBudget(compiled CompiledConversation, plan *PendingCompaction) error {
	if plan == nil {
		return nil
	}
	budgetTokens := plan.ContextWindowSize - plan.ReserveTokens
	estimatedTokens := estimateCompiledPromptTokens(compiled)
	if budgetTokens > 0 && estimatedTokens <= budgetTokens {
		return nil
	}
	message := fmt.Sprintf(
		"compaction result still exceeds context budget after rebuilding the summary (estimated=%d budget=%d)",
		estimatedTokens,
		budgetTokens,
	)
	if plan.PreserveCurrentTurnInputs {
		message = fmt.Sprintf(
			"compaction result still exceeds context budget after preserving the current user input (estimated=%d budget=%d)",
			estimatedTokens,
			budgetTokens,
		)
	}
	return compactionTerminalError{
		code:    compactionOverflowTerminalCode,
		message: message,
	}
}

func compactionContextWindowSize(conversation *ConversationFile) int64 {
	if conversation == nil {
		return projectedConversationMaxTokens
	}
	if conversation.TokenDetailsMaxTokens > 0 {
		return int64(conversation.TokenDetailsMaxTokens)
	}
	return projectedConversationMaxTokens
}

func (service *Service) resolveCompactionReserveTokens(modelID string) int64 {
	_ = service
	_ = modelID
	return compactionAutoReserveTokens
}

func parseManualCompactionRequest(userMessage *agentv1.UserMessage) (string, bool) {
	if userMessage == nil {
		return "", false
	}
	userText := strings.TrimSpace(userMessage.GetText())
	if instruction, ok := parseManualCompactionDirective(userText); ok {
		return instruction, true
	}
	if userText != "" {
		return "", false
	}
	selectedContext := userMessage.GetSelectedContext()
	if selectedContext == nil {
		return "", false
	}
	for _, command := range selectedContext.GetCursorCommands() {
		if !isCursorSummarizeCommand(command) {
			continue
		}
		instruction, _ := parseManualCompactionDirective(command.GetContent())
		return instruction, true
	}
	return "", false
}

func streamManualCompactionDirective(stream *ActiveStream) (string, bool) {
	if stream == nil {
		return "", false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.ManualCompaction.Requested {
		return strings.TrimSpace(stream.ManualCompaction.Instruction), true
	}
	return parseManualCompactionDirective(stream.LatestUserText)
}

func isCursorSummarizeCommand(command *agentv1.SelectedCursorCommand) bool {
	if command == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(command.GetName()), "glass-action-summarize") {
		return true
	}
	_, ok := parseManualCompactionDirective(command.GetContent())
	return ok
}

func parseManualCompactionDirective(latestUserText string) (string, bool) {
	trimmed := strings.TrimSpace(latestUserText)
	const directive = "/summarize"
	switch {
	case trimmed == directive:
		return "", true
	case strings.HasPrefix(trimmed, directive+" "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, directive)), true
	default:
		return "", false
	}
}

func buildPreCompactHookRequest(stream *ActiveStream, plan *compactionPlan) *agentv1.ExecuteHookRequest {
	if stream == nil || plan == nil {
		return nil
	}
	query := &agentv1.PreCompactRequestQuery{
		Trigger:             strings.TrimSpace(plan.Trigger),
		ContextUsagePercent: plan.ContextUsagePercent,
		ContextTokens:       plan.ContextTokens,
		ContextWindowSize:   plan.ContextWindowSize,
		MessageCount:        plan.MessageCount,
		MessagesToCompact:   plan.MessagesToCompact,
		IsFirstCompaction:   plan.IsFirstCompaction,
		ConversationId:      &stream.ConversationID,
		Model:               &stream.ModelID,
	}
	if generationID := strings.TrimSpace(stream.CurrentModelCallID); generationID != "" {
		query.GenerationId = &generationID
	}
	return &agentv1.ExecuteHookRequest{
		Request: &agentv1.ExecuteHookRequest_PreCompact{
			PreCompact: query,
		},
	}
}

func newPendingCompaction(plan *compactionPlan) *PendingCompaction {
	if plan == nil {
		return nil
	}
	clonedTurns := append([]compactedTurnSummary(nil), plan.CompactedTurns...)
	return &PendingCompaction{
		Trigger:                            plan.Trigger,
		ContextTokens:                      plan.ContextTokens,
		ContextWindowSize:                  plan.ContextWindowSize,
		ContextUsagePercent:                plan.ContextUsagePercent,
		ReserveTokens:                      plan.ReserveTokens,
		MessageCount:                       plan.MessageCount,
		MessagesToCompact:                  plan.MessagesToCompact,
		CompactTurnCount:                   plan.CompactTurnCount,
		IsFirstCompaction:                  plan.IsFirstCompaction,
		ExistingSummary:                    plan.ExistingSummary,
		CompactedTurns:                     clonedTurns,
		ManualInstruction:                  plan.ManualInstruction,
		RequestSource:                      plan.RequestSource,
		CurrentTurnSeq:                     plan.CurrentTurnSeq,
		CurrentRequestID:                   plan.CurrentRequestID,
		CurrentUserText:                    plan.CurrentUserText,
		PreserveCurrentTurnInputs:          plan.PreserveCurrentTurnInputs,
		ProjectionConversationID:           plan.ProjectionConversationID,
		ProjectionRootConversationID:       plan.ProjectionRootConversationID,
		ProjectionParentConversationID:     plan.ProjectionParentConversationID,
		ProjectionParentToolCallID:         plan.ProjectionParentToolCallID,
		ProjectionModelKey:                 plan.ProjectionModelKey,
		ProjectionContextVersion:           plan.ProjectionContextVersion,
		ProjectionSummaryStartEntrySeq:     plan.ProjectionSummaryStartEntrySeq,
		ProjectionCoveredEntrySeq:          plan.ProjectionCoveredEntrySeq,
		ProjectionCoveredPrefixFingerprint: plan.ProjectionCoveredPrefixFingerprint,
	}
}

func clonePendingCompaction(plan *PendingCompaction) *PendingCompaction {
	if plan == nil {
		return nil
	}
	clonedTurns := append([]compactedTurnSummary(nil), plan.CompactedTurns...)
	return &PendingCompaction{
		Trigger:                            plan.Trigger,
		ContextTokens:                      plan.ContextTokens,
		ContextWindowSize:                  plan.ContextWindowSize,
		ContextUsagePercent:                plan.ContextUsagePercent,
		ReserveTokens:                      plan.ReserveTokens,
		MessageCount:                       plan.MessageCount,
		MessagesToCompact:                  plan.MessagesToCompact,
		CompactTurnCount:                   plan.CompactTurnCount,
		IsFirstCompaction:                  plan.IsFirstCompaction,
		ExistingSummary:                    plan.ExistingSummary,
		CompactedTurns:                     clonedTurns,
		ManualInstruction:                  plan.ManualInstruction,
		RequestSource:                      plan.RequestSource,
		CurrentTurnSeq:                     plan.CurrentTurnSeq,
		CurrentRequestID:                   plan.CurrentRequestID,
		CurrentUserText:                    plan.CurrentUserText,
		PreserveCurrentTurnInputs:          plan.PreserveCurrentTurnInputs,
		HookMessage:                        plan.HookMessage,
		SummaryModelCallID:                 plan.SummaryModelCallID,
		StartedAt:                          plan.StartedAt,
		ProjectionConversationID:           plan.ProjectionConversationID,
		ProjectionRootConversationID:       plan.ProjectionRootConversationID,
		ProjectionParentConversationID:     plan.ProjectionParentConversationID,
		ProjectionParentToolCallID:         plan.ProjectionParentToolCallID,
		ProjectionModelKey:                 plan.ProjectionModelKey,
		ProjectionContextVersion:           plan.ProjectionContextVersion,
		ProjectionSummaryStartEntrySeq:     plan.ProjectionSummaryStartEntrySeq,
		ProjectionCoveredEntrySeq:          plan.ProjectionCoveredEntrySeq,
		ProjectionCoveredPrefixFingerprint: plan.ProjectionCoveredPrefixFingerprint,
	}
}

func cloneCompactionPlanBase(base *compactionPlan) compactionPlan {
	if base == nil {
		return compactionPlan{}
	}
	return compactionPlan{
		Trigger:                            base.Trigger,
		ContextTokens:                      base.ContextTokens,
		ContextWindowSize:                  base.ContextWindowSize,
		ContextUsagePercent:                base.ContextUsagePercent,
		ReserveTokens:                      base.ReserveTokens,
		MessageCount:                       base.MessageCount,
		MessagesToCompact:                  base.MessagesToCompact,
		CompactTurnCount:                   base.CompactTurnCount,
		IsFirstCompaction:                  base.IsFirstCompaction,
		ExistingSummary:                    base.ExistingSummary,
		CompactedTurns:                     append([]compactedTurnSummary(nil), base.CompactedTurns...),
		ManualInstruction:                  base.ManualInstruction,
		RequestSource:                      base.RequestSource,
		CurrentTurnSeq:                     base.CurrentTurnSeq,
		CurrentRequestID:                   base.CurrentRequestID,
		CurrentUserText:                    base.CurrentUserText,
		PreserveCurrentTurnInputs:          base.PreserveCurrentTurnInputs,
		ProjectionConversationID:           base.ProjectionConversationID,
		ProjectionRootConversationID:       base.ProjectionRootConversationID,
		ProjectionParentConversationID:     base.ProjectionParentConversationID,
		ProjectionParentToolCallID:         base.ProjectionParentToolCallID,
		ProjectionModelKey:                 base.ProjectionModelKey,
		ProjectionContextVersion:           base.ProjectionContextVersion,
		ProjectionSummaryStartEntrySeq:     base.ProjectionSummaryStartEntrySeq,
		ProjectionCoveredEntrySeq:          base.ProjectionCoveredEntrySeq,
		ProjectionCoveredPrefixFingerprint: base.ProjectionCoveredPrefixFingerprint,
	}
}
