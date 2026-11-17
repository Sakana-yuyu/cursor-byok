package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
)

func midStreamTestError(cause error) error {
	return providerTerminalError{cause: fmt.Errorf("upstream stream interrupted mid-response: %w: %w", modeladapter.ErrMidStreamInterrupted, cause)}
}

func TestProviderStreamRetryClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"mid-stream eof", midStreamTestError(io.ErrUnexpectedEOF), true},
		{"mid-stream 502", midStreamTestError(&modeladapter.HTTPStatusError{StatusCode: 502}), true},
		{"mid-stream 429", midStreamTestError(&modeladapter.HTTPStatusError{StatusCode: 429}), true},
		{"mid-stream 400", midStreamTestError(&modeladapter.HTTPStatusError{StatusCode: 400}), false},
		{"pre-output eof", providerTerminalError{cause: io.ErrUnexpectedEOF}, false},
		{"canceled", midStreamTestError(context.Canceled), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := providerStreamRetryClassification(tc.err); got != tc.want {
				t.Fatalf("providerStreamRetryClassification() = %t, want %t", got, tc.want)
			}
		})
	}
}

func openRetryTestStream(t *testing.T) (*Service, *ConversationFileStore, *ActiveStream, string) {
	t.Helper()
	store := NewConversationFileStore(t.TempDir())
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-retry", "请给出完整方案"),
	})
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), nil, nil, broker)
	stream, err := broker.OpenStream("request-retry", persisted.ConversationID, 1, "model-a", "model-a", agentv1.AgentMode_AGENT_MODE_AGENT, "请给出完整方案")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(persisted)
	stream.CurrentModelCallID = "call-retry"
	stream.ProviderPassCount = 1
	stream.Status = StreamStatusStreaming
	t.Cleanup(func() { clearStreamTimer(stream, providerTimerKey(streamTimerProviderRetry, "")) })
	if err := service.applyProviderModelEvent(stream, modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "已经展示的半截回复"}); err != nil {
		t.Fatalf("apply text delta: %v", err)
	}
	return service, store, stream, persisted.ConversationID
}

func promptContextSources(t *testing.T, conversation *ConversationFile) []string {
	t.Helper()
	var sources []string
	for _, entry := range conversation.Entries {
		if entry.Kind != "prompt_context" {
			continue
		}
		var payload promptContextEntryPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatalf("unmarshal prompt_context: %v", err)
		}
		sources = append(sources, payload.Source)
	}
	return sources
}

// 已产出后中断：不判失败，部分正文落盘，追加续写提示并挂起延迟 resume。
func TestMidStreamInterruptionSchedulesContinuationRetry(t *testing.T) {
	service, _, stream, _ := openRetryTestStream(t)
	if err := service.handleProviderDoneEvent(stream, &streamProviderEvent{Done: true, Err: midStreamTestError(io.ErrUnexpectedEOF)}); err != nil {
		t.Fatalf("handleProviderDoneEvent() error = %v", err)
	}
	if stream.Status == StreamStatusFailed {
		t.Fatal("stream failed, want continuation retry scheduled")
	}
	if stream.ProviderStreamRetryCount != 1 {
		t.Fatalf("ProviderStreamRetryCount = %d, want 1", stream.ProviderStreamRetryCount)
	}
	if stream.PendingProviderAction != providerActionResume {
		t.Fatalf("PendingProviderAction = %v, want resume", stream.PendingProviderAction)
	}
	if stream.StreamTimers[providerTimerKey(streamTimerProviderRetry, "")] == nil {
		t.Fatal("provider retry timer not scheduled")
	}
	// 回合内追加的条目先进入内存 checkpoint，回合收口时才落盘。
	conversation, _, _, err := service.snapshotCheckpointConversation(stream)
	if err != nil {
		t.Fatalf("snapshotCheckpointConversation() error = %v", err)
	}
	assistantCount := 0
	for _, entry := range conversation.Entries {
		if entry.Kind == "assistant_text" {
			assistantCount++
		}
	}
	if assistantCount != 1 {
		t.Fatalf("assistant_text entries = %d, want 1 (partial output persisted)", assistantCount)
	}
	sources := promptContextSources(t, conversation)
	if len(sources) != 1 || sources[0] != promptContextSourceProviderStreamRecovery {
		t.Fatalf("prompt_context sources = %v, want [%s]", sources, promptContextSourceProviderStreamRecovery)
	}
}

// 已派发过工具（可能有副作用）时不续写，按原逻辑失败收口。
func TestMidStreamInterruptionWithToolSideEffectsFails(t *testing.T) {
	service, _, stream, _ := openRetryTestStream(t)
	stream.ToolInvocationCount = 1
	_ = service.handleProviderDoneEvent(stream, &streamProviderEvent{Done: true, Err: midStreamTestError(io.ErrUnexpectedEOF)})
	if stream.Status != StreamStatusFailed {
		t.Fatalf("stream status = %q, want failed", stream.Status)
	}
	if stream.ProviderStreamRetryCount != 0 {
		t.Fatalf("ProviderStreamRetryCount = %d, want 0", stream.ProviderStreamRetryCount)
	}
}

// 续写额度用尽后按原逻辑失败收口，避免上游持续断流时无限续写。
func TestMidStreamInterruptionRetryLimitExhaustedFails(t *testing.T) {
	service, _, stream, _ := openRetryTestStream(t)
	stream.ProviderStreamRetryCount = providerStreamRetryLimit
	_ = service.handleProviderDoneEvent(stream, &streamProviderEvent{Done: true, Err: midStreamTestError(errors.New("connection reset by peer"))})
	if stream.Status != StreamStatusFailed {
		t.Fatalf("stream status = %q, want failed", stream.Status)
	}
}
