package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
)

func TestShouldLogThinkingDeltaSamplesFirstAndInterval(t *testing.T) {
	tests := []struct {
		name       string
		deltaCount int
		want       bool
	}{
		{name: "zero", deltaCount: 0, want: false},
		{name: "first", deltaCount: 1, want: true},
		{name: "before interval", deltaCount: thinkingDeltaLogInterval - 1, want: false},
		{name: "interval", deltaCount: thinkingDeltaLogInterval, want: true},
		{name: "after interval", deltaCount: thinkingDeltaLogInterval + 1, want: false},
		{name: "second interval", deltaCount: thinkingDeltaLogInterval * 2, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldLogThinkingDelta(test.deltaCount); got != test.want {
				t.Fatalf("shouldLogThinkingDelta(%d) = %t, want %t", test.deltaCount, got, test.want)
			}
		})
	}
}

func TestThinkingDeltaSamplingKeepsEveryBrokerEvent(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	stream, err := service.broker.OpenStream("request-thinking", "conversation-thinking", 1, "model", "model", agentv1.AgentMode_AGENT_MODE_AGENT, "")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}

	const deltaCount = thinkingDeltaLogInterval * 2
	for index := 0; index < deltaCount; index++ {
		event := modeladapter.ModelEvent{
			Kind:          modeladapter.ModelEventKindThinkingDelta,
			Text:          "reasoning",
			ThinkingStyle: agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT,
		}
		if err := service.applyProviderModelEvent(stream, event); err != nil {
			t.Fatalf("applyProviderModelEvent(%d) error = %v", index, err)
		}
	}

	events, err := service.broker.ReadFromCursor(stream.RequestID, 0)
	if err != nil {
		t.Fatalf("ReadFromCursor() error = %v", err)
	}
	if len(events) != deltaCount {
		t.Fatalf("thinking events = %d, want %d", len(events), deltaCount)
	}
	for index, event := range events {
		if got := event.Message.GetInteractionUpdate().GetThinkingDelta().GetText(); got != "reasoning" {
			t.Fatalf("thinking event %d text = %q, want reasoning", index, got)
		}
	}
}
