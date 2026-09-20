package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
)

// TestProviderContentFilterWithObservedToolInvocationCompletesWithoutResume 锁定
// content_filter 收口语义（移植自上游 aa47152f）：provider 内容策略拦截的回合，
// 即使本 pass 流内已观察到工具调用，也不得续跑执行——被过滤的工具调用参数往往
// 已被截断或污染，照常执行会绕过安全策略。回合应按正常结束收口。
func TestProviderContentFilterWithObservedToolInvocationCompletesWithoutResume(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-content-filter", "请检索后回答"),
	})
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), nil, nil, broker)
	stream, err := broker.OpenStream(
		"request-content-filter",
		persisted.ConversationID,
		1,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"请检索后回答",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(persisted)
	stream.CurrentModelCallID = "call-content-filter"
	stream.ProviderPassCount = 1
	stream.Status = StreamStatusStreaming
	// 本 pass 流内已观察到工具调用（ToolInvocationCount > 0），但 finish_reason 为 content_filter。
	stream.mu.Lock()
	stream.ToolInvocationCount = 1
	stream.mu.Unlock()

	if err := service.applyProviderModelEvent(stream, modeladapter.ModelEvent{
		Kind:         modeladapter.ModelEventKindTurnFinished,
		FinishReason: "content_filter",
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
		t.Fatalf("stream status = %q, want %q (content_filter must not resume observed tool calls)", stream.Status, StreamStatusCompleted)
	}
}
