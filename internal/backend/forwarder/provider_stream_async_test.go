package forwarder

import (
	"context"
	"testing"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
)

type orderedProviderEvents struct {
	events        []modeladapter.ModelEvent
	afterDeltas   chan struct{}
	turnAttempted chan struct{}
}

func (provider orderedProviderEvents) StartStream(_ context.Context, _ ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	for index, event := range provider.events {
		if index == 2 {
			close(provider.afterDeltas)
		}
		if event.Kind == modeladapter.ModelEventKindTurnFinished {
			close(provider.turnAttempted)
		}
		if err := sink(event); err != nil {
			return err
		}
	}
	return nil
}

// TestRunProviderStreamQueuesDeltasBeforeTerminalBarrier 锁定高频增量不必逐条
// 等待 actor 完整执行，而回合结束仍必须等待前序队列被消费。这样既解除上游 SSE
// 每条增量的同步往返，又不改变事件顺序、终态收口或 mailbox 的有界背压语义。
func TestRunProviderStreamQueuesDeltasBeforeTerminalBarrier(t *testing.T) {
	testCases := []struct {
		name  string
		event modeladapter.ModelEvent
	}{
		{name: "text", event: modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "first"}},
		{name: "thinking", event: modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindThinkingDelta, Text: "reasoning"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			broker := NewStreamBroker()
			stream, err := broker.OpenStream("request-"+testCase.name, "conversation-"+testCase.name, 1, "model", "model", 1, "test")
			if err != nil {
				t.Fatalf("OpenStream() error = %v", err)
			}
			mailbox := make(chan streamCommandEnvelope, 4)
			done := make(chan struct{})
			stream.mu.Lock()
			stream.CurrentProviderToken = 1
			stream.ActorMailbox = mailbox
			stream.ActorDone = done
			stream.mu.Unlock()

			provider := orderedProviderEvents{
				events: []modeladapter.ModelEvent{
					testCase.event,
					testCase.event,
					{Kind: modeladapter.ModelEventKindTurnFinished},
				},
				afterDeltas:   make(chan struct{}),
				turnAttempted: make(chan struct{}),
			}
			service := &Service{provider: provider, broker: broker}
			completed := make(chan struct{})
			go func() {
				service.runProviderStream(stream, 1, context.Background(), ProviderRequest{RequestID: stream.RequestID, ConversationID: stream.ConversationID}, nil)
				close(completed)
			}()

			select {
			case <-provider.turnAttempted:
			case <-time.After(100 * time.Millisecond):
				t.Fatal("delta sink waited for actor completion before provider could reach TurnFinished")
			}
			select {
			case <-completed:
				t.Fatal("TurnFinished must wait until preceding delta is consumed")
			case <-time.After(20 * time.Millisecond):
			}

			first := <-mailbox
			if first.command.Provider == nil || first.command.Provider.Event.Kind != testCase.event.Kind || first.result != nil {
				t.Fatalf("first mailbox event = %#v, want asynchronous %s delta", first.command.Provider, testCase.event.Kind)
			}
			second := <-mailbox
			if second.command.Provider == nil || second.command.Provider.Event.Kind != testCase.event.Kind || second.result != nil {
				t.Fatalf("second mailbox event = %#v, want asynchronous %s delta", second.command.Provider, testCase.event.Kind)
			}
			terminal := <-mailbox
			if terminal.command.Provider == nil || terminal.command.Provider.Event.Kind != modeladapter.ModelEventKindTurnFinished || terminal.result == nil {
				t.Fatalf("third mailbox event = %#v, want synchronous TurnFinished barrier", terminal.command.Provider)
			}
			terminal.result <- nil

			providerDone := <-mailbox
			if providerDone.command.Provider == nil || !providerDone.command.Provider.Done || providerDone.result == nil {
				t.Fatalf("fourth mailbox event = %#v, want synchronous provider completion", providerDone.command.Provider)
			}
			providerDone.result <- nil
			select {
			case <-completed:
			case <-time.After(time.Second):
				t.Fatal("runProviderStream did not return after terminal barriers completed")
			}
		})
	}
}

func TestPostStreamCommandAsyncRejectsExitedActor(t *testing.T) {
	service := &Service{}
	stream := &ActiveStream{
		ActorMailbox: make(chan streamCommandEnvelope, 1),
		ActorDone:    make(chan struct{}),
	}
	close(stream.ActorDone)

	if err := service.postStreamCommandAsync(stream, streamCommand{Kind: streamCommandProviderEvent}); err != errProviderLoopInterrupted {
		t.Fatalf("postStreamCommandAsync() error = %v, want %v", err, errProviderLoopInterrupted)
	}
	if queued := len(stream.ActorMailbox); queued != 0 {
		t.Fatalf("exited actor mailbox queued = %d, want 0", queued)
	}
}
