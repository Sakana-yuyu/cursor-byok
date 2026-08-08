// compaction_algorithms.go 承载 compaction 域纯算法层：候选构建与选择、turn 摘要、
// 摘要消息估计、tool_call 形状提取与规范化、输出文本截断。
package forwarder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"cursor/gen/agentv1"
)

func buildCurrentTurnCompactionCandidate(entries []HistoryEntry, turnSeq int64, requestID string) (compactionCandidateTurn, bool) {
	if len(entries) == 0 || turnSeq <= 0 || strings.TrimSpace(requestID) == "" {
		return compactionCandidateTurn{}, false
	}
	normalizedRequestID := strings.TrimSpace(requestID)
	latestToolCallID := latestCompletedToolCallIDForTurn(entries, turnSeq, normalizedRequestID)
	if latestToolCallID == "" {
		return compactionCandidateTurn{}, false
	}
	preservedEntryIndexes := autoCompactionPreservedEntryIndexes(entries, turnSeq, normalizedRequestID, latestToolCallID)
	summary := compactedTurnSummary{}
	replayCount := int32(0)
	estimatedTokens := int64(0)
	removedToolHistory := false
	for index, entry := range entries {
		if entry.TurnSeq != turnSeq || strings.TrimSpace(entry.RequestID) != normalizedRequestID {
			continue
		}
		if strings.TrimSpace(entry.Kind) == "user_message" && strings.TrimSpace(summary.UserText) == "" {
			summary.UserText = currentTurnUserText(entry)
		}
		if _, ok := preservedEntryIndexes[index]; ok {
			continue
		}
		switch strings.TrimSpace(entry.Kind) {
		case "assistant_text":
			if step := summarizeCurrentTurnAssistantEntry(entry); step != "" {
				summary.Steps = append(summary.Steps, step)
			}
			replayCount++
			estimatedTokens += estimateTextTokens(string(entry.Payload))
		case "tool_call":
			removedToolHistory = true
			if step := summarizeCurrentTurnToolCallEntry(entry); step != "" {
				summary.Steps = append(summary.Steps, step)
			}
			replayCount++
			estimatedTokens += estimateTextTokens(string(entry.Payload))
		case "tool_result":
			removedToolHistory = true
			if step := summarizeCurrentTurnToolResultEntry(entry); step != "" {
				summary.Steps = append(summary.Steps, step)
			}
			replayCount++
			estimatedTokens += estimateTextTokens(string(entry.Payload))
		}
	}
	if !removedToolHistory || replayCount <= 0 {
		return compactionCandidateTurn{}, false
	}
	if len(summary.Steps) == 0 {
		summary.Steps = append(summary.Steps, "tool_history=earlier current-turn tool history compacted")
	}
	return compactionCandidateTurn{
		Summary:         summary,
		ReplayCount:     replayCount,
		EstimatedTokens: estimatedTokens,
	}, true
}

func buildContextCompactionCandidates(entries []HistoryEntry, currentTurnSeq int64, currentRequestID string) []compactionCandidateTurn {
	if len(entries) == 0 {
		return nil
	}
	turnOrder := make([]int64, 0)
	grouped := make(map[int64][]HistoryEntry)
	for _, entry := range entries {
		if entry.TurnSeq <= 0 || isCompactionSummaryKind(entry.Kind) {
			continue
		}
		if entry.TurnSeq == currentTurnSeq && strings.TrimSpace(entry.RequestID) == strings.TrimSpace(currentRequestID) {
			continue
		}
		if _, ok := grouped[entry.TurnSeq]; !ok {
			turnOrder = append(turnOrder, entry.TurnSeq)
		}
		grouped[entry.TurnSeq] = append(grouped[entry.TurnSeq], entry)
	}
	candidates := make([]compactionCandidateTurn, 0, len(turnOrder))
	for _, turnSeq := range turnOrder {
		if candidate, ok := buildContextTurnCompactionCandidate(grouped[turnSeq]); ok {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func buildContextTurnCompactionCandidate(entries []HistoryEntry) (compactionCandidateTurn, bool) {
	if len(entries) == 0 {
		return compactionCandidateTurn{}, false
	}
	summary := compactedTurnSummary{}
	replayCount := int32(0)
	estimatedTokens := int64(0)
	for _, entry := range entries {
		switch strings.TrimSpace(entry.Kind) {
		case "user_message":
			if strings.TrimSpace(summary.UserText) == "" {
				summary.UserText = currentTurnUserText(entry)
			}
			replayCount++
			estimatedTokens += estimateTextTokens(string(entry.Payload))
		case "assistant_text":
			if step := summarizeCurrentTurnAssistantEntry(entry); step != "" {
				summary.Steps = append(summary.Steps, step)
			}
			replayCount++
			estimatedTokens += estimateTextTokens(string(entry.Payload))
		case "tool_call":
			if step := summarizeCurrentTurnToolCallEntry(entry); step != "" {
				summary.Steps = append(summary.Steps, step)
			}
			replayCount++
			estimatedTokens += estimateTextTokens(string(entry.Payload))
		case "tool_result":
			if step := summarizeCurrentTurnToolResultEntry(entry); step != "" {
				summary.Steps = append(summary.Steps, step)
			}
			replayCount++
			estimatedTokens += estimateTextTokens(string(entry.Payload))
		}
	}
	if replayCount <= 0 || (strings.TrimSpace(summary.UserText) == "" && len(summary.Steps) == 0) {
		return compactionCandidateTurn{}, false
	}
	return compactionCandidateTurn{
		Summary:         summary,
		ReplayCount:     replayCount,
		EstimatedTokens: estimatedTokens,
	}, true
}

func currentTurnUserText(entry HistoryEntry) string {
	userMessage := &agentv1.UserMessage{}
	if err := protojson.Unmarshal(entry.Payload, userMessage); err != nil {
		return ""
	}
	return truncateCompactionText(userMessage.GetText(), compactionTurnSnippetMaxChars/3)
}

func summarizeCurrentTurnAssistantEntry(entry HistoryEntry) string {
	var payload assistantTextPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		return ""
	}
	if text := truncateCompactionText(payload.Text, compactionTurnSnippetMaxChars/3); text != "" {
		return "assistant=" + text
	}
	if text := truncateCompactionText(payload.ReasoningContent, compactionTurnSnippetMaxChars/4); text != "" {
		return "thinking=" + text
	}
	return ""
}

func summarizeCurrentTurnToolCallEntry(entry HistoryEntry) string {
	var payload toolCallEntryPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		return ""
	}
	toolName := strings.TrimSpace(payload.ToolName)
	if toolName == "" {
		toolName = "tool_call"
	}
	return toolName + "=called"
}

func summarizeCurrentTurnToolResultEntry(entry HistoryEntry) string {
	var payload toolResultEntryPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		return ""
	}
	if len(payload.ToolCall) > 0 {
		toolCall := &agentv1.ToolCall{}
		if err := protojson.Unmarshal(payload.ToolCall, toolCall); err == nil {
			if toolName, detail := summarizeCompactedToolCall(toolCall); toolName != "" {
				return toolName + "=" + detail
			}
		}
	}
	toolName := strings.TrimSpace(payload.ToolName)
	if toolName == "" {
		toolName = "tool_result"
	}
	if result := truncateCompactionText(payload.ResultText, compactionTurnSnippetMaxChars/3); result != "" {
		return toolName + "=" + result
	}
	return toolName + "=completed"
}

func rewriteAutoCompactionToolResultEntry(entry HistoryEntry, limitBytes int, minimal bool) (HistoryEntry, bool) {
	if strings.TrimSpace(entry.Kind) != "tool_result" {
		return entry, false
	}
	var payload toolResultEntryPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		return entry, false
	}
	toolName := firstNonEmpty(strings.TrimSpace(payload.ToolName), "tool")
	resultText := strings.TrimSpace(payload.ResultText)
	if compacted, ok := compactProjectedEditToolResultReplay(toolName, resultText); ok {
		resultText = compacted
	}
	if minimal {
		resultText = autoCompactionOmittedToolResultText(toolName)
		payload.ToolCall = nil
	} else {
		if resultText == "" {
			resultText = autoCompactionOmittedToolResultText(toolName)
		} else {
			resultText = truncateProjectedReplayText(toolName, resultText, limitBytes)
		}
		if len(payload.ToolCall) > limitBytes || len(payload.ResultText) > limitBytes {
			payload.ToolCall = nil
		}
	}
	payload.ResultText = resultText
	encoded, err := json.Marshal(payload)
	if err != nil {
		return entry, false
	}
	if bytes.Equal(bytes.TrimSpace(entry.Payload), bytes.TrimSpace(encoded)) {
		return entry, false
	}
	entry.Payload = encoded
	return entry, true
}

func autoCompactionOmittedToolResultText(toolName string) string {
	return fmt.Sprintf(
		"[%s result omitted by auto compaction because the preserved current turn still exceeded the context budget; rerun the relevant tool if exact output is needed]",
		firstNonEmpty(strings.TrimSpace(toolName), "tool"),
	)
}

func autoCompactionPreservedEntryIndexes(entries []HistoryEntry, turnSeq int64, requestID string, latestToolCallID string) map[int]struct{} {
	preserved := make(map[int]struct{})
	if len(entries) == 0 || turnSeq <= 0 {
		return preserved
	}
	normalizedRequestID := strings.TrimSpace(requestID)
	normalizedToolCallID := strings.TrimSpace(latestToolCallID)
	latestToolCallIndex := -1
	for index, entry := range entries {
		if entry.TurnSeq != turnSeq || strings.TrimSpace(entry.RequestID) != normalizedRequestID {
			continue
		}
		if shouldPreserveAutoCompactionEntry(entry, normalizedToolCallID) {
			preserved[index] = struct{}{}
		}
		if strings.TrimSpace(entry.Kind) == "tool_call" && historyEntryToolCallID(entry) == normalizedToolCallID {
			latestToolCallIndex = index
		}
	}
	if latestToolCallIndex < 0 || !toolCallEntryNeedsReasoningCarrier(entries[latestToolCallIndex]) {
		return preserved
	}
	if carrierIndex := latestAssistantReasoningCarrierIndex(entries, latestToolCallIndex, turnSeq, normalizedRequestID); carrierIndex >= 0 {
		preserved[carrierIndex] = struct{}{}
	}
	return preserved
}

func toolCallEntryNeedsReasoningCarrier(entry HistoryEntry) bool {
	if strings.TrimSpace(entry.Kind) != "tool_call" {
		return false
	}
	var payload toolCallEntryPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		return false
	}
	return strings.TrimSpace(payload.ReasoningContent) == "" || strings.TrimSpace(payload.ReasoningSignature) == ""
}

func latestAssistantReasoningCarrierIndex(entries []HistoryEntry, beforeIndex int, turnSeq int64, requestID string) int {
	if len(entries) == 0 || beforeIndex <= 0 {
		return -1
	}
	for index := beforeIndex - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.TurnSeq != turnSeq || strings.TrimSpace(entry.RequestID) != strings.TrimSpace(requestID) {
			continue
		}
		switch strings.TrimSpace(entry.Kind) {
		case "assistant_text":
			var payload assistantTextPayload
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				continue
			}
			if strings.TrimSpace(payload.ReasoningContent) != "" && strings.TrimSpace(payload.ReasoningSignature) != "" {
				return index
			}
		case "user_message", "request_context":
			return -1
		}
	}
	return -1
}

func historyEntryToolCallID(entry HistoryEntry) string {
	if toolCallID := strings.TrimSpace(entry.ToolCallID); toolCallID != "" {
		return toolCallID
	}
	switch strings.TrimSpace(entry.Kind) {
	case "tool_call":
		var payload toolCallEntryPayload
		if err := json.Unmarshal(entry.Payload, &payload); err == nil {
			return strings.TrimSpace(payload.ToolCallID)
		}
	case "tool_result":
		var payload toolResultEntryPayload
		if err := json.Unmarshal(entry.Payload, &payload); err == nil {
			return strings.TrimSpace(payload.ToolCallID)
		}
	}
	return ""
}

func shouldPreserveAutoCompactionEntry(entry HistoryEntry, latestToolCallID string) bool {
	switch strings.TrimSpace(entry.Kind) {
	case "request_context", "user_message":
		return true
	case "tool_call", "tool_result":
		toolCallID := historyEntryToolCallID(entry)
		return toolCallID != "" && toolCallID == strings.TrimSpace(latestToolCallID)
	case "metadata":
		var payload metadataPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			return false
		}
		switch strings.TrimSpace(payload.Type) {
		case "mode", "run_request":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func latestCompletedToolCallIDForTurn(entries []HistoryEntry, turnSeq int64, requestID string) string {
	normalizedRequestID := strings.TrimSpace(requestID)
	latest := ""
	for _, entry := range entries {
		if entry.TurnSeq != turnSeq || strings.TrimSpace(entry.RequestID) != normalizedRequestID {
			continue
		}
		if strings.TrimSpace(entry.Kind) != "tool_result" {
			continue
		}
		toolCallID := strings.TrimSpace(entry.ToolCallID)
		if toolCallID == "" {
			var payload toolResultEntryPayload
			if err := json.Unmarshal(entry.Payload, &payload); err == nil {
				toolCallID = strings.TrimSpace(payload.ToolCallID)
			}
		}
		if toolCallID != "" {
			latest = toolCallID
		}
	}
	return latest
}

func existingConversationSummaryText(conversation *ConversationFile) string {
	texts := compactionSummaryTexts(conversation)
	if len(texts) == 0 {
		return ""
	}
	return strings.TrimSpace(texts[len(texts)-1])
}

func encodeConversationSummaryBytes(summary string) []byte {
	text := strings.TrimSpace(summary)
	if text == "" {
		return nil
	}
	payload, err := proto.Marshal(&agentv1.ConversationSummary{Summary: text})
	if err != nil {
		return nil
	}
	return payload
}

func summarizeCompactedToolCall(toolCall *agentv1.ToolCall) (string, string) {
	shape, ok := extractCompactToolCallShape(toolCall)
	if !ok {
		return "", ""
	}
	return shape.ToolName, truncateCompactionText(shape.ResultJSON, compactionTurnSnippetMaxChars/3)
}

type compactToolCallShape struct {
	ArgsJSON   string
	ToolName   string
	ResultJSON string
}

func extractCompactToolCallShape(toolCall *agentv1.ToolCall) (compactToolCallShape, bool) {
	if toolCall == nil {
		return compactToolCallShape{}, false
	}
	value := toolCall.ProtoReflect()
	oneof := value.Descriptor().Oneofs().ByName("tool")
	if oneof == nil {
		return compactToolCallShape{}, false
	}
	selected := value.WhichOneof(oneof)
	if selected == nil {
		return compactToolCallShape{}, false
	}
	selectedValue := value.Get(selected)
	if !selectedValue.IsValid() {
		return compactToolCallShape{}, false
	}
	selectedMessage := selectedValue.Message()
	if !selectedMessage.IsValid() {
		return compactToolCallShape{}, false
	}
	argsJSON, _ := extractCompactFieldJSON(selectedMessage, "args")
	resultJSON, _ := extractCompactFieldJSON(selectedMessage, "result")
	toolName := canonicalCompactToolName(string(selected.Name()), string(selectedMessage.Descriptor().Name()), argsJSON, resultJSON)
	if toolName == "" {
		return compactToolCallShape{}, false
	}
	return compactToolCallShape{
		ArgsJSON:   argsJSON,
		ToolName:   toolName,
		ResultJSON: resultJSON,
	}, true
}

func canonicalCompactToolName(fieldName string, messageName string, argsJSON string, resultJSON string) string {
	switch strings.TrimSpace(fieldName) {
	case "mcp_tool_call":
		return "CallMcpTool"
	case "read_mcp_resource_tool_call":
		return "FetchMcpResource"
	case "update_todos_tool_call":
		return "TodoWrite"
	case "read_todos_tool_call":
		return "ReadTodos"
	case "sem_search_tool_call":
		return "SemanticSearch"
	case "edit_tool_call":
		return compactEditToolName(argsJSON, resultJSON)
	}
	trimmed := strings.TrimSuffix(strings.TrimSpace(messageName), "ToolCall")
	return strings.TrimSpace(trimmed)
}

func compactEditToolName(argsJSON string, resultJSON string) string {
	if editResultJSONLooksLikeStructuredEdit(resultJSON) {
		return "Edit"
	}
	if compactEditArgsIndicateWrite(argsJSON) {
		return "Write"
	}
	return "Edit"
}

func compactEditArgsIndicateWrite(argsJSON string) bool {
	trimmed := strings.TrimSpace(argsJSON)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return false
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
		return false
	}
	for _, key := range []string{"stream_content", "streamContent"} {
		if _, ok := args[key]; ok {
			return true
		}
	}
	return false
}

func editResultJSONLooksLikeStructuredEdit(resultJSON string) bool {
	trimmed := strings.TrimSpace(resultJSON)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return false
	}
	success, ok := payload["success"].(map[string]any)
	if !ok || len(success) == 0 {
		return false
	}
	if _, ok := success["beforeFullFileContent"]; ok {
		return true
	}
	if _, ok := success["before_full_file_content"]; ok {
		return true
	}
	if _, ok := success["diffString"]; ok {
		return true
	}
	if _, ok := success["diff_string"]; ok {
		return true
	}
	return false
}

func extractCompactFieldJSON(message protoreflect.Message, fieldName string) (string, bool) {
	if !message.IsValid() {
		return "", false
	}
	field := message.Descriptor().Fields().ByName(protoreflect.Name(fieldName))
	if field == nil || !message.Has(field) {
		return "", false
	}
	value := message.Get(field)
	if !value.IsValid() {
		return "", false
	}
	child := value.Message()
	if !child.IsValid() {
		return "", false
	}
	item, ok := child.Interface().(proto.Message)
	if !ok {
		return "", false
	}
	payload, err := protojson.MarshalOptions{EmitUnpopulated: false}.Marshal(item)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(payload)), true
}

func truncateCompactionText(text string, maxChars int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || maxChars <= 0 {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= maxChars {
		return trimmed
	}
	return string(runes[:maxChars]) + "..."
}
