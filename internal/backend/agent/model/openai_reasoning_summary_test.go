package modeladapter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIResponsesReasoningSummaryDoesNotRepeatCompletedSnapshot(t *testing.T) {
	events := []string{
		`{"type":"response.reasoning_summary_text.delta","delta":"**Planning proxy research","item_id":"rs_1","output_index":0,"summary_index":0}`,
		`{"type":"response.reasoning_summary_text.delta","delta":" and brainstorming**","item_id":"rs_1","output_index":0,"summary_index":0}`,
		`{"type":"response.reasoning_summary_text.delta","delta":"**Evaluating mode switch for design planning**","item_id":"rs_1","output_index":0,"summary_index":1}`,
		`{"type":"response.reasoning_summary_text.delta","delta":"**Preparing parallel research subagents deployment**","item_id":"rs_1","output_index":0,"summary_index":2}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"completed","encrypted_content":"signature","summary":[{"type":"summary_text","text":"**Planning proxy research and brainstorming**"},{"type":"summary_text","text":"**Evaluating mode switch for design planning**"},{"type":"summary_text","text":"**Preparing parallel research subagents deployment**"}]}}`,
		`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.6-sol","status":"completed","output":[{"id":"rs_1","type":"reasoning","status":"completed","encrypted_content":"signature","summary":[{"type":"summary_text","text":"**Planning proxy research and brainstorming**"},{"type":"summary_text","text":"**Evaluating mode switch for design planning**"},{"type":"summary_text","text":"**Preparing parallel research subagents deployment**"}]}]}}`,
	}

	got := runOpenAIResponsesReasoningSummaryStream(t, events)
	want := strings.Join([]string{
		"**Planning proxy research and brainstorming**",
		"**Evaluating mode switch for design planning**",
		"**Preparing parallel research subagents deployment**",
	}, "\n")
	if got != want {
		t.Fatalf("thinking summary = %q, want %q", got, want)
	}
}

func TestOpenAIResponsesReasoningSummaryUsesCompletedSnapshotAsFallback(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"completed","encrypted_content":"signature","summary":[{"type":"summary_text","text":"First summary"},{"type":"summary_text","text":"Second summary"}]}}`,
		`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.6-sol","status":"completed"}}`,
	}

	if got, want := runOpenAIResponsesReasoningSummaryStream(t, events), "First summary\nSecond summary"; got != want {
		t.Fatalf("thinking summary = %q, want %q", got, want)
	}
}

func TestOpenAIResponsesReasoningSummaryPreservesRepeatedDeltaFragments(t *testing.T) {
	events := []string{
		`{"type":"response.reasoning_summary_text.delta","delta":"ha","item_id":"rs_1","output_index":0,"summary_index":0}`,
		`{"type":"response.reasoning_summary_text.delta","delta":"ha","item_id":"rs_1","output_index":0,"summary_index":0}`,
		`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.6-sol","status":"completed"}}`,
	}

	if got, want := runOpenAIResponsesReasoningSummaryStream(t, events), "haha"; got != want {
		t.Fatalf("thinking summary = %q, want %q", got, want)
	}
}

func runOpenAIResponsesReasoningSummaryStream(t *testing.T, events []string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
		}
	}))
	defer server.Close()

	adapter := &OpenAIAdapter{client: server.Client()}
	var thinking strings.Builder
	err := adapter.streamResponses(context.Background(), StreamRequest{
		RequestID:      "request-1",
		ModelCallID:    "model-call-1",
		OpenAIEndpoint: "/responses",
		RequestBodyOverride: map[string]any{
			"model":  "gpt-5.6-sol",
			"input":  []any{},
			"stream": true,
		},
	}, server.URL, "test-key", "gpt-5.6-sol", 0, false, func(event ModelEvent) error {
		if event.Kind == ModelEventKindThinkingDelta {
			thinking.WriteString(event.Text)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream responses: %v", err)
	}
	return thinking.String()
}
