package modeladapter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIResponsesUsageForwardsReasoningTokens(t *testing.T) {
	server := newOpenAIUsageStreamServer(t, []string{
		`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.6-sol","status":"completed","usage":{"input_tokens":100,"output_tokens":240,"output_tokens_details":{"reasoning_tokens":180}}}}`,
	})
	defer server.Close()

	adapter := &OpenAIAdapter{client: server.Client()}
	finished := ModelEvent{}
	err := adapter.streamResponses(context.Background(), StreamRequest{
		RequestID:      "request-responses-usage",
		ModelCallID:    "model-call-responses-usage",
		OpenAIEndpoint: "/responses",
		RequestBodyOverride: map[string]any{
			"model":  "gpt-5.6-sol",
			"input":  []any{},
			"stream": true,
		},
	}, server.URL, "test-key", "gpt-5.6-sol", 0, false, func(event ModelEvent) error {
		if event.Kind == ModelEventKindTurnFinished {
			finished = event
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream responses: %v", err)
	}
	if finished.OutputTokens != 240 || finished.ReasoningTokens != 180 {
		t.Fatalf("usage = output:%d reasoning:%d, want output:240 reasoning:180", finished.OutputTokens, finished.ReasoningTokens)
	}
}

func TestOpenAIChatUsageForwardsReasoningTokens(t *testing.T) {
	server := newOpenAIUsageStreamServer(t, []string{
		`{"id":"chatcmpl_1","model":"deepseek-v4-flash","choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		`{"id":"chatcmpl_1","model":"deepseek-v4-flash","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":240,"completion_tokens_details":{"reasoning_tokens":180}}}`,
		`[DONE]`,
	})
	defer server.Close()

	adapter := &OpenAIAdapter{client: server.Client()}
	finished := ModelEvent{}
	err := adapter.streamChatCompletions(context.Background(), StreamRequest{
		RequestID:      "request-chat-usage",
		ModelCallID:    "model-call-chat-usage",
		OpenAIEndpoint: "/v1/chat/completions",
		RequestBodyOverride: map[string]any{
			"model":    "deepseek-v4-flash",
			"messages": []any{},
			"stream":   true,
		},
	}, server.URL, "test-key", "deepseek-v4-flash", 0, false, func(event ModelEvent) error {
		if event.Kind == ModelEventKindTurnFinished {
			finished = event
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream chat completions: %v", err)
	}
	if finished.OutputTokens != 240 || finished.ReasoningTokens != 180 {
		t.Fatalf("usage = output:%d reasoning:%d, want output:240 reasoning:180", finished.OutputTokens, finished.ReasoningTokens)
	}
}

func newOpenAIUsageStreamServer(t *testing.T, events []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
		}
	}))
}
