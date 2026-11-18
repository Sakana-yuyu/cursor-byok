package modeladapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"cursor/internal/modelcontext"
	legacyruntime "cursor/internal/runtime"
)

func TestRouterContextBudgetLimitsSurviveMaxMode(t *testing.T) {
	for _, tc := range []struct {
		name, model                          string
		configured, requested, ceiling, want int
	}{
		{"explicit 32k", "gpt-4o", 100000, 32000, 64000, 32000},
		{"configured stays small", "gpt-4o", 16000, 32000, 64000, 16000},
		{"compile ceiling", "gpt-4o", 100000, 64000, 32000, 32000},
		{"actual provider capability", "gpt-4o", 2000000, 0, 0, modelcontext.WindowTokens("gpt-4o")},
		{"legacy configured", "unknown-provider-model", 64000, 0, 0, 64000},
		{"legacy unknown", "unknown-provider-model", 0, 0, 0, 200000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &capturingModelAdapter{}
			channel := &legacyruntime.ResolvedChannel{ID: "budget", Provider: "openai", ProtocolMode: "fixed", Model: tc.model, ContextWindowTokens: tc.configured}
			router := NewRouter(&reasoningLimitResolver{channel: channel})
			router.openai = adapter
			err := router.Stream(context.Background(), StreamRequest{ModelID: "internal-hash-not-a-model", MaxMode: true, MaxTokens: 1000, RequestedContextWindowTokens: tc.requested, ContextWindowTokens: tc.ceiling}, func(ModelEvent) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if got := adapter.request.ResolvedContextWindowTokens; got != tc.want {
				t.Fatalf("resolved window=%d want %d", got, tc.want)
			}
			if adapter.request.ContextWindowTokens != tc.want {
				t.Fatalf("hard ceiling not propagated: %+v", adapter.request)
			}
		})
	}
}

func TestRouterContextBudgetFailoverStopsBeforeSmallChannelHTTP(t *testing.T) {
	var largeCalls, smallCalls atomic.Int32
	var captured map[string]any
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		largeCalls.Add(1)
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"temporarily unavailable"}}`)
	}))
	defer large.Close()
	small := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { smallCalls.Add(1); w.WriteHeader(500) }))
	defer small.Close()
	a := &legacyruntime.ResolvedChannel{ID: "large", Provider: "openai", ProtocolMode: "fixed", ProtocolGroup: "chat_completions", BaseURL: large.URL, APIKey: "key", Model: "budget-test-model", ContextWindowTokens: 64000, MaxTokens: 1000}
	b := &legacyruntime.ResolvedChannel{ID: "small", Provider: "openai", ProtocolMode: "fixed", ProtocolGroup: "chat_completions", BaseURL: small.URL, APIKey: "key", Model: "budget-test-model", ContextWindowTokens: 32000, MaxTokens: 1000}
	router := NewRouter(&scriptedResolver{orders: [][]*legacyruntime.ResolvedChannel{{a, b}, {b, a}}})
	err := router.Stream(context.Background(), StreamRequest{ModelID: "internal-budget-model", ContextWindowTokens: 64000, Messages: []Message{{Role: "user", Content: strings.Repeat("x", 100000)}}}, func(ModelEvent) error { return nil })
	var budgetErr *ContextBudgetExceededError
	if !errors.As(err, &budgetErr) || budgetErr.ContextWindowTokens != 32000 {
		t.Fatalf("expected smaller-channel budget rather than first provider failure, got %v", err)
	}
	if largeCalls.Load() != 1 || smallCalls.Load() != 0 {
		t.Fatalf("HTTP calls: large=%d small=%d", largeCalls.Load(), smallCalls.Load())
	}
	messages, _ := json.Marshal(captured["messages"])
	if !strings.Contains(string(messages), strings.Repeat("x", 1000)) {
		t.Fatal("large-channel payload missing original prompt")
	}
	if budgetInt(captured["max_tokens"]) != 1000 {
		t.Fatalf("wire output cap=%v", captured["max_tokens"])
	}
	if _, exists := router.healthByChannel[channelHealthMapKey(b)]; exists {
		t.Fatal("local budget failure recorded as provider health failure")
	}
}

func TestProviderFinalContextBudgetIncludesToolsAndOverrides(t *testing.T) {
	for _, provider := range []string{"chat", "responses", "anthropic", "gemini", "xml"} {
		t.Run(provider, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			defer server.Close()
			req := StreamRequest{ModelID: "unlisted-budget-model", ProviderModelID: "unlisted-budget-model", BaseURL: server.URL, APIKey: "key", ContextWindowTokens: 2000, MaxTokens: 500, AnthropicMaxTokens: 500, Messages: []Message{{Role: "user", Content: "hello"}}, Tools: []json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"lookup","description":"` + strings.Repeat("schema ", 1500) + `","parameters":{"type":"object","properties":{}}}}`)}}
			var err error
			sink := func(ModelEvent) error { return nil }
			switch provider {
			case "chat", "xml":
				req.OpenAIEndpoint = "/v1/chat/completions"
				req.ProtocolMode = "fixed"
				req.ProtocolGroup = "chat_completions"
				if provider == "xml" {
					req.ToolCallMode = ToolCallModeXMLPrompt
				}
				err = (&OpenAIAdapter{client: server.Client()}).Stream(context.Background(), req, sink)
			case "responses":
				req.OpenAIEndpoint = "/v1/responses"
				req.ProtocolMode = "fixed"
				req.ProtocolGroup = "responses"
				err = (&OpenAIAdapter{client: server.Client()}).Stream(context.Background(), req, sink)
			case "anthropic":
				err = (&AnthropicAdapter{client: server.Client()}).Stream(context.Background(), req, sink)
			case "gemini":
				err = (&GeminiAdapter{client: server.Client()}).Stream(context.Background(), req, sink)
			}
			var budgetErr *ContextBudgetExceededError
			if !errors.As(err, &budgetErr) {
				t.Fatalf("expected budget error, got %v", err)
			}
			if calls.Load() != 0 {
				t.Fatalf("oversized provider payload sent %d times", calls.Load())
			}
			if budgetErr.EstimatedInputTokens <= 1500 || budgetErr.MaxOutputTokens != 500 {
				t.Fatalf("tools/output not counted: %+v", budgetErr)
			}
		})
	}
}

func TestProviderContextBudgetChecksFinalOverridesAndImages(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       map[string]any
		wantOutput int
	}{
		{"extra prompt", map[string]any{"messages": []any{map[string]any{"role": "user", "content": strings.Repeat("x", 10000)}}, "max_tokens": 500}, 500},
		{"extra output", map[string]any{"messages": []any{}, "max_completion_tokens": 3000}, 3000},
		{"image reserve", map[string]any{"messages": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + strings.Repeat("A", 10000)}}, "max_tokens": 1000}, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProviderContextBudget(StreamRequest{ContextWindowTokens: 2000}, "unlisted-budget-model", tc.body, "max_tokens", "max_completion_tokens")
			var budgetErr *ContextBudgetExceededError
			if !errors.As(err, &budgetErr) || budgetErr.MaxOutputTokens != tc.wantOutput {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestProviderContextBudgetWirePayloadFitsWithoutSelection(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	req := StreamRequest{BaseURL: server.URL, APIKey: "key", ModelID: "unlisted-budget-model", OpenAIEndpoint: "/v1/chat/completions", ProtocolMode: "fixed", ProtocolGroup: "chat_completions", Messages: []Message{{Role: "user", Content: "legacy hello"}}, RequestKnobs: map[string]any{}}
	err := (&OpenAIAdapter{client: server.Client()}).Stream(context.Background(), req, func(ModelEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if budgetInt(payload["max_tokens"]) != 4096 {
		t.Fatalf("default reserved output is not a real wire cap: %#v", payload)
	}
	if req.RequestKnobs["context_window_tokens"] != 200000 {
		t.Fatalf("legacy fallback lost: %#v", req.RequestKnobs)
	}
	input, _ := req.RequestKnobs["estimated_input_tokens"].(int64)
	if input+int64(budgetInt(payload["max_tokens"])) > 200000 {
		t.Fatalf("actual payload estimate exceeds effective budget")
	}
}

type budgetEventAdapter struct {
	calls int
	err   error
}

func (adapter *budgetEventAdapter) Stream(_ context.Context, _ StreamRequest, sink func(ModelEvent) error) error {
	adapter.calls++
	if err := sink(ModelEvent{Kind: ModelEventKindTextDelta, Text: "visible"}); err != nil {
		return err
	}
	return adapter.err
}

func TestRouterContextBudgetDoesNotReplayAfterOutput(t *testing.T) {
	channel := &legacyruntime.ResolvedChannel{ID: "output-budget", Provider: "openai", Model: "unknown-model"}
	adapter := &budgetEventAdapter{err: &ContextBudgetExceededError{ContextWindowTokens: 32000, EstimatedInputTokens: 40000, MaxOutputTokens: 1000}}
	router := NewRouter(&reasoningLimitResolver{channel: channel})
	router.openai = adapter
	err := router.Stream(context.Background(), StreamRequest{ModelID: "internal"}, func(ModelEvent) error { return nil })
	var budgetErr *ContextBudgetExceededError
	if !errors.Is(err, ErrMidStreamInterrupted) || !errors.As(err, &budgetErr) || !budgetErr.OutputStarted {
		t.Fatalf("missing no-replay markers: %v", err)
	}
	if adapter.calls != 1 || len(router.healthByChannel) != 0 {
		t.Fatalf("post-output failure replayed or cooled: calls=%d health=%v", adapter.calls, router.healthByChannel)
	}
}

func TestRouterProtocolFallbackDoesNotReplayAfterOutput(t *testing.T) {
	channel := &legacyruntime.ResolvedChannel{ID: "claude-output", Provider: "openai", Model: "claude-sonnet-4-6", MaxTokens: 1000}
	adapter := &budgetEventAdapter{err: &HTTPStatusError{StatusCode: 404, Message: "unsupported endpoint"}}
	fallback := &capturingModelAdapter{}
	router := NewRouter(&reasoningLimitResolver{channel: channel})
	router.anthropic = adapter
	router.openai = fallback
	err := router.Stream(context.Background(), StreamRequest{ModelID: "internal"}, func(ModelEvent) error { return nil })
	if !errors.Is(err, ErrMidStreamInterrupted) || adapter.calls != 1 || fallback.request.Provider != "" {
		t.Fatalf("protocol replay after output: err=%v calls=%d fallback=%q", err, adapter.calls, fallback.request.Provider)
	}
}

func TestRouterExplicit32KMaxModeCapturedPayload(t *testing.T) {
	var calls atomic.Int32
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	channel := &legacyruntime.ResolvedChannel{ID: "explicit32", Provider: "openai", Model: "gpt-4o", ContextWindowTokens: 100000, MaxTokens: 1000, APIKey: "key", BaseURL: server.URL, ProtocolMode: "fixed", ProtocolGroup: "chat_completions", OpenAIEndpoint: "/v1/chat/completions"}
	router := NewRouter(&reasoningLimitResolver{channel: channel})
	req := StreamRequest{ModelID: "internal-hash", MaxMode: true, RequestedContextWindowTokens: 32000, ContextWindowTokens: 64000, Messages: []Message{{Role: "user", Content: strings.Repeat("x", 60000)}}, RequestKnobs: map[string]any{}}
	if err := router.Stream(context.Background(), req, func(ModelEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	input, _ := req.RequestKnobs["estimated_input_tokens"].(int64)
	if calls.Load() != 1 || req.RequestKnobs["context_window_tokens"] != 32000 || input+int64(budgetInt(payload["max_tokens"])) > 32000 {
		t.Fatalf("wire/request budget mismatch: calls=%d knobs=%v", calls.Load(), req.RequestKnobs)
	}
	req.Messages = []Message{{Role: "user", Content: strings.Repeat("x", 100000)}}
	err := router.Stream(context.Background(), req, func(ModelEvent) error { return nil })
	var budgetErr *ContextBudgetExceededError
	if !errors.As(err, &budgetErr) || budgetErr.ContextWindowTokens != 32000 || calls.Load() != 1 {
		t.Fatalf("MaxMode sent oversized 32k payload: err=%v calls=%d", err, calls.Load())
	}
}
