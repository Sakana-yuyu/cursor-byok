package forwarder

import (
	"encoding/json"
	"fmt"
	"strings"
)

func validateContextProjectionTurnToolChain(entries []HistoryEntry) string {
	if len(entries) == 0 {
		return "turn is empty"
	}
	calls := make(map[string]struct{})
	results := make(map[string]struct{})
	requestID := ""
	for _, entry := range entries {
		entryRequestID := strings.TrimSpace(entry.RequestID)
		if entryRequestID != "" {
			if requestID == "" {
				requestID = entryRequestID
			} else if entryRequestID != requestID {
				return fmt.Sprintf("mixed request ids %q and %q", requestID, entryRequestID)
			}
		}
		switch strings.TrimSpace(entry.Kind) {
		case "tool_call":
			toolCallID := historyEntryToolCallID(entry)
			if toolCallID == "" {
				return "tool call is missing an id"
			}
			if _, exists := calls[toolCallID]; exists {
				return fmt.Sprintf("duplicate tool call id %q", toolCallID)
			}
			calls[toolCallID] = struct{}{}
		case "tool_result":
			toolCallID := historyEntryToolCallID(entry)
			if toolCallID == "" {
				return "tool result is missing a call id"
			}
			if _, exists := results[toolCallID]; exists {
				return fmt.Sprintf("duplicate tool result for call %q", toolCallID)
			}
			if _, exists := calls[toolCallID]; !exists {
				var payload toolResultEntryPayload
				if err := json.Unmarshal(entry.Payload, &payload); err != nil || len(payload.ToolCall) == 0 {
					return fmt.Sprintf("orphan tool result for call %q", toolCallID)
				}
				calls[toolCallID] = struct{}{}
			}
			results[toolCallID] = struct{}{}
		}
	}
	for toolCallID := range calls {
		if _, exists := results[toolCallID]; !exists {
			return fmt.Sprintf("incomplete tool chain for call %q", toolCallID)
		}
	}
	return ""
}
