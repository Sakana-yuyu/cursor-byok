package forwarder

import (
	"encoding/json"
	"strings"
	"testing"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
)

func TestIsMaxOutputTokensTruncationRecognizesNormalizedProviderReasons(t *testing.T) {
	for _, reason := range []string{"max_output_tokens", "length", "incomplete", "max_tokens"} {
		if !isMaxOutputTokensTruncation(reason) {
			t.Fatalf("isMaxOutputTokensTruncation(%q) = false, want true", reason)
		}
	}
	if isMaxOutputTokensTruncation("stop") {
		t.Fatal("isMaxOutputTokensTruncation(stop) = true, want false")
	}
}

// TestProviderOutputTruncationPreservesPartialTextAndFailsTurn 锁定 provider 已经发送
// 部分正文后因输出预算截断时的收口：已展示内容必须可回放，但回合不能伪装为成功。
func TestProviderOutputTruncationPreservesPartialTextAndFailsTurn(t *testing.T) {
	for _, finishReason := range []string{"max_output_tokens", "length", "incomplete", "max_tokens"} {
		t.Run(finishReason, func(t *testing.T) {
			store := NewConversationFileStore(t.TempDir())
			conversation := testConversation([]HistoryEntry{
				testUserMessageEntry(t, 1, "request-truncated", "请给出完整方案"),
			})
			persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
			if err != nil {
				t.Fatalf("SaveConversationWithEntries() error = %v", err)
			}
			broker := NewStreamBroker()
			service := newServiceWithDependencies(store, NewHistoryProjector(), nil, nil, broker)
			stream, err := broker.OpenStream(
				"request-truncated",
				persisted.ConversationID,
				1,
				"model-a",
				"model-a",
				agentv1.AgentMode_AGENT_MODE_AGENT,
				"请给出完整方案",
			)
			if err != nil {
				t.Fatalf("OpenStream() error = %v", err)
			}
			stream.CheckpointConversation = cloneConversationFile(persisted)
			stream.CurrentModelCallID = "call-truncated"
			stream.ProviderPassCount = 1
			stream.Status = StreamStatusStreaming

			if err := service.applyProviderModelEvent(stream, modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "这是已保留的部分回复。"}); err != nil {
				t.Fatalf("apply text delta: %v", err)
			}
			if err := service.applyProviderModelEvent(stream, modeladapter.ModelEvent{
				Kind:         modeladapter.ModelEventKindTurnFinished,
				FinishReason: finishReason,
				InputTokens:  100,
				OutputTokens: 20,
				UsagePresent: true,
			}); err != nil {
				t.Fatalf("apply turn finished: %v", err)
			}
			if err := service.handleProviderDoneEvent(stream, &streamProviderEvent{}); err != nil {
				t.Fatalf("handleProviderDoneEvent() error = %v", err)
			}

			if stream.Status != StreamStatusFailed {
				t.Fatalf("stream status = %q, want %q", stream.Status, StreamStatusFailed)
			}
			events, err := broker.ReadFromCursor(stream.RequestID, 0)
			if err != nil {
				t.Fatalf("ReadFromCursor() error = %v", err)
			}
			if len(events) == 0 {
				t.Fatal("stream events are empty")
			}
			terminal := events[len(events)-1]
			if !terminal.End || terminal.TerminalErrorCode != "output_truncated" {
				t.Fatalf("terminal event = %#v, want output_truncated failure", terminal)
			}

			persisted, err = store.LoadConversation(conversation.ConversationID)
			if err != nil {
				t.Fatalf("LoadConversation() error = %v", err)
			}
			entryKinds := make([]string, 0, len(persisted.Entries))
			metadataTypes := make([]string, 0, len(persisted.Entries))
			assistantTextCount := 0
			for _, entry := range persisted.Entries {
				entryKinds = append(entryKinds, entry.Kind)
				if entry.Kind == "assistant_text" {
					assistantTextCount++
				}
				if entry.Kind == "metadata" {
					var payload metadataPayload
					if err := json.Unmarshal(entry.Payload, &payload); err != nil {
						t.Fatalf("unmarshal metadata payload: %v", err)
					}
					metadataTypes = append(metadataTypes, payload.Type)
				}
			}
			if !strings.Contains(strings.Join(entryKinds, ","), "assistant_text") {
				t.Fatalf("persisted entries omitted partial assistant text: %#v", entryKinds)
			}
			if assistantTextCount != 1 {
				t.Fatalf("partial assistant text entry count = %d, want 1", assistantTextCount)
			}
			if !strings.Contains(strings.Join(metadataTypes, ","), "output_truncated") {
				t.Fatalf("persisted metadata omitted output_truncated: %#v", metadataTypes)
			}
			if strings.Contains(strings.Join(metadataTypes, ","), "turn_completed") {
				t.Fatalf("truncated turn was persisted as completed: %#v", metadataTypes)
			}
		})
	}
}

func TestProviderStopWithPartialTextCompletesTurn(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-complete", "请给出完整方案"),
	})
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), nil, nil, broker)
	stream, err := broker.OpenStream(
		"request-complete",
		persisted.ConversationID,
		1,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"请给出完整方案",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(persisted)
	stream.CurrentModelCallID = "call-complete"
	stream.ProviderPassCount = 1
	stream.Status = StreamStatusStreaming

	if err := service.applyProviderModelEvent(stream, modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "完整回复。"}); err != nil {
		t.Fatalf("apply text delta: %v", err)
	}
	if err := service.applyProviderModelEvent(stream, modeladapter.ModelEvent{
		Kind:         modeladapter.ModelEventKindTurnFinished,
		FinishReason: "stop",
		InputTokens:  100,
		OutputTokens: 20,
		UsagePresent: true,
	}); err != nil {
		t.Fatalf("apply turn finished: %v", err)
	}
	if err := service.handleProviderDoneEvent(stream, &streamProviderEvent{}); err != nil {
		t.Fatalf("handleProviderDoneEvent() error = %v", err)
	}
	if err := acknowledgePendingCheckpointBlobs(service, stream); err != nil {
		t.Fatalf("acknowledgePendingCheckpointBlobs() error = %v", err)
	}
	if stream.Status != StreamStatusCompleted {
		t.Fatalf("stream status = %q, want %q", stream.Status, StreamStatusCompleted)
	}
}
