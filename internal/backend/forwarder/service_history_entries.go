// service_history_entries.go 承载 history entry 构造器簇：run 首批 entry、mode 变更、
// assistant 文本、tool_call/tool_result、metadata 与 hook 附加记录序列化。
package forwarder

import (
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
	promptengine "cursor/internal/backend/agent/prompt"
)

// buildRunEntries 构造一次 run intent 需要写入 history 的首批 entry。
func buildRunEntries(intent InboundIntent, effectiveMode agentv1.AgentMode, turnSeq int64) ([]HistoryEntry, error) {
	entries := make([]HistoryEntry, 0, 4)
	if intent.RequestContext != nil {
		normalized := normalizeRequestContextForStorageMode(intent.RequestContext, turnSeq == 1)
		if normalized != nil {
			payload, err := protojson.Marshal(normalized)
			if err != nil {
				return nil, err
			}
			entries = append(entries, HistoryEntry{
				TurnSeq:   turnSeq,
				RequestID: intent.RequestID,
				Role:      "user",
				Kind:      "request_context",
				Payload:   payload,
			})
		}
	}
	if intent.UserMessage != nil {
		normalized := normalizeUserMessageForStorage(intent.UserMessage)
		payload, err := protojson.Marshal(normalized)
		if err != nil {
			return nil, err
		}
		entries = append(entries, HistoryEntry{
			TurnSeq:   turnSeq,
			RequestID: intent.RequestID,
			Role:      "user",
			Kind:      "user_message",
			Payload:   payload,
		})
		if commandMessage, ok := promptengine.BuildSelectedCursorCommandsReplayMessage(normalized); ok {
			entries = append(entries, newPromptContextEntry(turnSeq, intent.RequestID, newPromptContextMessage(
				promptContextSourceSelectedCursorCommands,
				modeladapter.Message{Role: commandMessage.Role, Content: commandMessage.Content},
				true,
			)))
		}
	}
	modeEntry, err := newModeMetadataEntry(turnSeq, intent.RequestID, effectiveMode, intent.HasExplicitMode, intent.ModeSource)
	if err != nil {
		return nil, err
	}
	entries = append(entries,
		modeEntry,
		newMetadataEntry(turnSeq, intent.RequestID, "run_request", buildRunRequestMetadata(intent)),
	)
	if intent.HasExplicitMode {
		entries = append(entries, newModeChangePromptContextEntry(turnSeq, intent.RequestID, effectiveMode))
	}
	return entries, nil
}

func buildRunRequestMetadata(intent InboundIntent) map[string]any {
	return map[string]any{
		"model_id":   intent.ModelID,
		"model_name": intent.ModelName,
		"prewarm":    intent.Prewarm,
	}
}

func newModeMetadataEntry(turnSeq int64, requestID string, mode agentv1.AgentMode, explicit bool, source ModeSource) (HistoryEntry, error) {
	modeAliasValue, err := modeAlias(mode)
	if err != nil {
		return HistoryEntry{}, err
	}
	payload := map[string]any{
		"mode": modeAliasValue,
	}
	if explicit {
		payload["explicit"] = true
	}
	if strings.TrimSpace(string(source)) != "" {
		payload["source"] = strings.TrimSpace(string(source))
	}
	return newMetadataEntry(turnSeq, requestID, "mode", payload), nil
}

func newModeChangePromptContextEntry(turnSeq int64, requestID string, mode agentv1.AgentMode) HistoryEntry {
	modeAliasValue, err := modeAlias(mode)
	if err != nil {
		modeAliasValue = "agent"
	}
	return newPromptContextEntry(turnSeq, requestID, newPromptContextMessage(
		"mode_change",
		modeladapter.Message{
			Role:    "user",
			Content: wrapSystemReminder(fmt.Sprintf("At this point, the active mode changed to %s; follow later mode reminders if present.", modeAliasValue)),
		},
		true,
	))
}

// newAssistantTextEntry 构造 assistant 文本 entry。
func newAssistantTextEntry(turnSeq int64, requestID string, text string, reasoningContent string, reasoningSignature string) HistoryEntry {
	return newAssistantTextEntryWithProviderMetadata(turnSeq, requestID, text, reasoningContent, reasoningSignature, "", "", "", nil)
}

func newAssistantTextEntryWithProviderMetadata(turnSeq int64, requestID string, text string, reasoningContent string, reasoningSignature string, reasoningSignatureSource string, reasoningItemID string, reasoningStatus string, reasoningSummary json.RawMessage) HistoryEntry {
	payload, _ := json.Marshal(assistantTextPayload{
		Text:                     text,
		ReasoningContent:         reasoningContent,
		ReasoningSignature:       strings.TrimSpace(reasoningSignature),
		ReasoningSignatureSource: strings.TrimSpace(reasoningSignatureSource),
		ReasoningItemID:          strings.TrimSpace(reasoningItemID),
		ReasoningStatus:          strings.TrimSpace(reasoningStatus),
		ReasoningSummary:         append(json.RawMessage(nil), reasoningSummary...),
	})
	return HistoryEntry{
		TurnSeq:   turnSeq,
		RequestID: strings.TrimSpace(requestID),
		Role:      "assistant",
		Kind:      "assistant_text",
		Payload:   payload,
	}
}

// newToolCallEntry 构造 tool_call entry。
func newToolCallEntry(turnSeq int64, requestID string, toolCallID string, toolName string, reasoningContent string, reasoningSignature string, toolCall json.RawMessage) HistoryEntry {
	return newToolCallEntryWithProviderMetadata(turnSeq, requestID, toolCallID, toolName, reasoningContent, reasoningSignature, "", "", "", nil, "", "", "", toolCall)
}

func newToolCallEntryWithProviderMetadata(turnSeq int64, requestID string, toolCallID string, toolName string, reasoningContent string, reasoningSignature string, reasoningSignatureSource string, reasoningItemID string, reasoningStatus string, reasoningSummary json.RawMessage, providerItemID string, providerCallID string, providerStatus string, toolCall json.RawMessage) HistoryEntry {
	payload, _ := json.Marshal(toolCallEntryPayload{
		ToolCallID:               strings.TrimSpace(toolCallID),
		ToolName:                 strings.TrimSpace(toolName),
		ReasoningContent:         reasoningContent,
		ReasoningSignature:       strings.TrimSpace(reasoningSignature),
		ReasoningSignatureSource: strings.TrimSpace(reasoningSignatureSource),
		ReasoningItemID:          strings.TrimSpace(reasoningItemID),
		ReasoningStatus:          strings.TrimSpace(reasoningStatus),
		ReasoningSummary:         append(json.RawMessage(nil), reasoningSummary...),
		ProviderItemID:           strings.TrimSpace(providerItemID),
		ProviderCallID:           strings.TrimSpace(providerCallID),
		ProviderStatus:           strings.TrimSpace(providerStatus),
		ToolCall:                 append(json.RawMessage(nil), toolCall...),
	})
	return HistoryEntry{
		TurnSeq:    turnSeq,
		RequestID:  strings.TrimSpace(requestID),
		Role:       "assistant",
		Kind:       "tool_call",
		ToolCallID: strings.TrimSpace(toolCallID),
		Payload:    payload,
	}
}

// newToolResultEntry 构造 tool_result entry。
func newToolResultEntry(turnSeq int64, requestID string, toolCallID string, toolName string, arguments string, resultText string, reasoningContent string, toolCall json.RawMessage) HistoryEntry {
	payload, _ := json.Marshal(toolResultEntryPayload{
		ToolCallID:       strings.TrimSpace(toolCallID),
		ToolName:         strings.TrimSpace(toolName),
		Arguments:        strings.TrimSpace(arguments),
		ResultText:       strings.TrimSpace(resultText),
		ReasoningContent: strings.TrimSpace(reasoningContent),
		ToolCall:         append(json.RawMessage(nil), toolCall...),
	})
	return HistoryEntry{
		TurnSeq:    turnSeq,
		RequestID:  strings.TrimSpace(requestID),
		Role:       "tool",
		Kind:       "tool_result",
		ToolCallID: strings.TrimSpace(toolCallID),
		Payload:    payload,
	}
}

// newMetadataEntry 构造 metadata entry。
func newMetadataEntry(turnSeq int64, requestID string, eventType string, values map[string]any) HistoryEntry {
	payload, _ := json.Marshal(metadataPayload{
		Type:  strings.TrimSpace(eventType),
		Value: values,
	})
	return HistoryEntry{
		TurnSeq:   turnSeq,
		RequestID: strings.TrimSpace(requestID),
		Role:      "system",
		Kind:      "metadata",
		Payload:   payload,
	}
}

// hookAdditionalContextsToRecords 把 hook 附加上下文转换为可序列化的记录列表。
func hookAdditionalContextsToRecords(contexts []*agentv1.HookAdditionalContext) []map[string]string {
	if len(contexts) == 0 {
		return nil
	}
	records := make([]map[string]string, 0, len(contexts))
	for _, item := range contexts {
		if item == nil {
			continue
		}
		records = append(records, map[string]string{
			"hook_event_name": strings.TrimSpace(item.GetHookEventName()),
			"content":         strings.TrimSpace(item.GetContent()),
		})
	}
	return records
}

