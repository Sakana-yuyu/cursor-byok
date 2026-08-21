package modeladapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIResponsesTerminalEvents(t *testing.T) {
	tests := []struct {
		name           string
		events         []string
		wantFinish     int
		wantReason     string
		wantUnexpected bool
	}{
		{
			name:       "completed",
			events:     []string{`{"type":"response.completed","response":{"id":"resp_complete","model":"gpt-test","status":"completed"}}`},
			wantFinish: 1,
			wantReason: "completed",
		},
		{
			name:       "incomplete is terminal but not completed",
			events:     []string{`{"type":"response.incomplete","response":{"id":"resp_incomplete","model":"gpt-test","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`},
			wantFinish: 1,
			wantReason: "max_output_tokens",
		},
		{
			name:       "compatibility done completes text-only relay",
			events:     []string{`{"type":"response.output_text.delta","delta":"complete"}`, `[DONE]`},
			wantFinish: 1,
			wantReason: "compat_done",
		},
		{
			name:           "clean EOF without terminal is truncated",
			events:         []string{`{"type":"response.output_text.delta","delta":"partial"}`},
			wantUnexpected: true,
		},
		{
			name:           "done with pending function call is truncated",
			events:         []string{`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"in_progress","call_id":"call_1","name":"Write"}}`, `[DONE]`},
			wantUnexpected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := runOpenAIResponsesTerminalStream(t, tt.events)
			if tt.wantUnexpected {
				if !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("stream error = %v, want io.ErrUnexpectedEOF", err)
				}
				if countOpenAIResponsesTurnFinished(events) != 0 {
					t.Fatalf("truncated stream emitted TurnFinished: %#v", events)
				}
				return
			}
			if err != nil {
				t.Fatalf("stream error = %v", err)
			}
			if countOpenAIResponsesTurnFinished(events) != tt.wantFinish {
				t.Fatalf("TurnFinished count = %d, want %d", countOpenAIResponsesTurnFinished(events), tt.wantFinish)
			}
			for _, event := range events {
				if event.Kind == ModelEventKindTurnFinished && event.FinishReason != tt.wantReason {
					t.Fatalf("finish reason = %q, want %q", event.FinishReason, tt.wantReason)
				}
			}
		})
	}
}

func TestOpenAIResponsesIncompleteDoesNotCompletePendingTool(t *testing.T) {
	events, err := runOpenAIResponsesTerminalStream(t, []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"in_progress","call_id":"call_1","name":"Write"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{\"path\":\"a.txt\"}"}`,
		`{"type":"response.incomplete","response":{"id":"resp_incomplete","model":"gpt-test","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"Write","arguments":"{\"path\":\"a.txt\"}"}}`,
	})
	if err != nil {
		t.Fatalf("stream error = %v", err)
	}
	if countOpenAIResponsesTurnFinished(events) != 1 {
		t.Fatalf("incomplete terminal missing TurnFinished: %#v", events)
	}
	for _, event := range events {
		if event.Kind == ModelEventKindToolLikeCompleted {
			t.Fatalf("incomplete response executed pending tool: %#v", event)
		}
	}
}

func TestOpenAIResponsesIncompleteCompletesOnlyConfirmedTerminalItems(t *testing.T) {
	events, err := runOpenAIResponsesTerminalStream(t, []string{
		`{"type":"response.incomplete","response":{"id":"resp_incomplete","model":"gpt-test","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"id":"fc_done","type":"function_call","status":"completed","call_id":"call_done","name":"Read","arguments":"{}"},{"id":"fc_pending","type":"function_call","status":"in_progress","call_id":"call_pending","name":"Write","arguments":"{}"}]}}`,
	})
	if err != nil {
		t.Fatalf("stream error = %v", err)
	}
	toolCalls := 0
	for _, event := range events {
		if event.Kind == ModelEventKindToolLikeCompleted {
			toolCalls++
			if event.ToolInvocation == nil || event.ToolInvocation.ProviderCallID != "call_done" {
				t.Fatalf("unexpected incomplete terminal tool invocation: %#v", event)
			}
		}
	}
	if toolCalls != 1 {
		t.Fatalf("completed tool count = %d, want 1", toolCalls)
	}
}

func TestOpenAIResponsesCompatibilityDoneRejectsReasoningItems(t *testing.T) {
	events, err := runOpenAIResponsesTerminalStream(t, []string{
		`{"type":"response.output_text.delta","delta":"text"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"id":"rs_1","type":"reasoning","status":"completed","encrypted_content":"signature"}}`,
		`[DONE]`,
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("stream error = %v, want io.ErrUnexpectedEOF", err)
	}
	if countOpenAIResponsesTurnFinished(events) != 0 {
		t.Fatalf("reasoning-bearing compatibility stream emitted TurnFinished: %#v", events)
	}
}

func TestOpenAIResponsesTerminalEnvelopeWinsOverTrailingError(t *testing.T) {
	events, err := runOpenAIResponsesTerminalStream(t, []string{
		`{"type":"response.completed","response":{"id":"resp_complete","model":"good-model","status":"completed"}}`,
		`{"type":"error","error":{"type":"server_error","code":"late","message":"must be ignored"}}`,
	})
	if err != nil {
		t.Fatalf("stream error = %v", err)
	}
	if countOpenAIResponsesTurnFinished(events) != 1 {
		t.Fatalf("terminal envelope did not complete once: %#v", events)
	}
}

func TestOpenAIResponsesLateTerminalEventDoesNotOverwriteUsage(t *testing.T) {
	events, err := runOpenAIResponsesTerminalStream(t, []string{
		`{"type":"response.completed","response":{"id":"resp_complete","model":"good-model","status":"completed","usage":{"input_tokens":1,"output_tokens":2}}}`,
		`{"type":"response.output_text.delta","response":{"model":"bad-model","usage":{"input_tokens":999,"output_tokens":999}},"delta":"ignored"}`,
	})
	if err != nil {
		t.Fatalf("stream error = %v", err)
	}
	for _, event := range events {
		if event.Kind == ModelEventKindTurnFinished {
			if event.Model != "good-model" || event.InputTokens != 1 || event.OutputTokens != 2 {
				t.Fatalf("late event overwrote terminal accounting: %#v", event)
			}
		}
	}
}

func TestOpenAIResponsesCompatibilityDoneRejectsImageItems(t *testing.T) {
	events, err := runOpenAIResponsesTerminalStream(t, []string{
		`{"type":"response.output_text.delta","delta":"text"}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"img_1","type":"image_generation_call","status":"in_progress"}}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"id":"img_1","type":"image_generation_call","status":"completed"}}`,
		`[DONE]`,
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("stream error = %v, want io.ErrUnexpectedEOF", err)
	}
	if countOpenAIResponsesTurnFinished(events) != 0 {
		t.Fatalf("image-bearing compatibility stream emitted TurnFinished: %#v", events)
	}
}

func TestOpenAIResponsesSSEErrorAfterToolProgressIsInterrupted(t *testing.T) {
	events, err := runOpenAIResponsesTerminalStream(t, []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"in_progress","call_id":"call_1","name":"Write"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{\"path\":\"a.txt\"}"}`,
		`{"type":"error","error":{"type":"server_error","code":"overloaded","message":"capacity"}}`,
	})
	if !errors.Is(err, ErrMidStreamInterrupted) {
		t.Fatalf("stream error = %v, want ErrMidStreamInterrupted", err)
	}
	if countOpenAIResponsesTurnFinished(events) != 0 {
		t.Fatalf("tool-progress error stream emitted TurnFinished: %#v", events)
	}
}

func TestOpenAIResponsesSSEErrorAfterOutputIsInterrupted(t *testing.T) {
	events, err := runOpenAIResponsesTerminalStream(t, []string{
		`{"type":"response.output_text.delta","delta":"partial"}`,
		`{"type":"error","error":{"type":"server_error","code":"overloaded","message":"capacity"}}`,
	})
	if !errors.Is(err, ErrMidStreamInterrupted) {
		t.Fatalf("stream error = %v, want ErrMidStreamInterrupted", err)
	}
	if countOpenAIResponsesTurnFinished(events) != 0 {
		t.Fatalf("error stream emitted TurnFinished: %#v", events)
	}
}

func runOpenAIResponsesTerminalStream(t *testing.T, rawEvents []string) ([]ModelEvent, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range rawEvents {
			if event == "[DONE]" {
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
		}
	}))
	defer server.Close()

	adapter := &OpenAIAdapter{client: server.Client()}
	var events []ModelEvent
	err := adapter.streamResponses(context.Background(), StreamRequest{
		RequestID:      "request-terminal",
		ModelCallID:    "call-terminal",
		OpenAIEndpoint: "/responses",
		RequestBodyOverride: map[string]any{
			"model":  "gpt-test",
			"input":  []any{},
			"stream": true,
		},
	}, server.URL, "test-key", "gpt-test", 0, false, func(event ModelEvent) error {
		events = append(events, event)
		return nil
	})
	return events, err
}

func countOpenAIResponsesTurnFinished(events []ModelEvent) int {
	count := 0
	for _, event := range events {
		if event.Kind == ModelEventKindTurnFinished {
			count++
		}
	}
	return count
}
