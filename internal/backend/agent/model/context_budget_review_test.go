package modeladapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	legacyruntime "cursor/internal/runtime"
)

func TestContextBudgetReviewIntegerBoundaries(t *testing.T) {
	for _, value := range []any{int(-1), int64(-1), float64(-1), 0.5, 1.5, math.NaN(), math.Inf(1), math.Inf(-1), json.Number("3.5"), json.Number("9223372036854775808"), float64(math.MaxInt64), "1000"} {
		if n, err := positiveBudgetInt(value); err == nil {
			t.Errorf("accepted invalid %v (%T) as %d", value, value, n)
		}
	}
	for _, value := range []any{int64(math.MaxInt32), float64(math.MaxInt32), json.Number("2147483647")} {
		if n, err := positiveBudgetInt(value); err != nil || int64(n) != math.MaxInt32 {
			t.Errorf("valid MaxInt32 rejected: %v -> %d, %v", value, n, err)
		}
	}
	if strconv.IntSize == 32 {
		for _, value := range []any{int64(math.MaxInt32) + 1, float64(math.MaxInt32) + 1, json.Number("2147483648")} {
			if n, err := positiveBudgetInt(value); err == nil {
				t.Errorf("386 overflow accepted: %v -> %d", value, n)
			}
		}
	}
	req := StreamRequest{RequestedContextWindowTokens: math.MaxInt32, ContextWindowTokens: math.MaxInt32}
	var exceeded *ContextBudgetExceededError
	if err := checkContextBudget(req, "unknown-budget-model", int64(math.MaxInt32)-10, 20); !errors.As(err, &exceeded) {
		t.Fatalf("input+output overflow bypass: %v", err)
	}
	if exceeded.ContextWindowTokens != math.MaxInt32 || exceeded.EstimatedInputTokens != int64(math.MaxInt32)-10 {
		t.Fatalf("integer ceiling truncated: %+v", exceeded)
	}
	if err := checkContextBudget(req, "unknown-budget-model", int64(math.MaxInt32)-20, 20); err != nil {
		t.Fatalf("exact boundary rejected: %v", err)
	}
}

func TestContextBudgetReviewMalformedOutputIsNotDefaulted(t *testing.T) {
	for _, value := range []any{-1, 0, 0.5, 1.5, math.NaN(), math.Inf(1), json.Number("9223372036854775808")} {
		body := map[string]any{"max_tokens": value}
		err := validateProviderContextBudget(StreamRequest{ContextWindowTokens: 32000}, "unknown-budget-model", body, "max_tokens")
		var invalid *ContextBudgetValidationError
		if !errors.As(err, &invalid) {
			t.Errorf("invalid output %v not rejected: %v", value, err)
		}
	}
}

func TestContextBudgetReviewProtocolOutputAndConfiguredDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		aliases     []string
		body        map[string]any
		req         StreamRequest
		want        int
		key         string
	}{
		{"preserve request positive", "max_tokens", nil, map[string]any{}, StreamRequest{MaxTokens: 12000}, 12000, "max_tokens"},
		{"preserve anthropic positive", "max_tokens", nil, map[string]any{}, StreamRequest{MaxTokens: 12000, AnthropicMaxTokens: 9000}, 9000, "max_tokens"},
		{"preserve body alias", "max_tokens", []string{"max_completion_tokens"}, map[string]any{"max_completion_tokens": 15000}, StreamRequest{MaxTokens: 12000}, 15000, "max_completion_tokens"},
		{"responses ignores chat cap", "max_output_tokens", nil, map[string]any{"max_tokens": 1}, StreamRequest{MaxTokens: 12000}, 12000, "max_output_tokens"},
		{"chat ignores responses cap", "max_tokens", []string{"max_completion_tokens"}, map[string]any{"max_output_tokens": 1}, StreamRequest{MaxTokens: 12000}, 12000, "max_tokens"},
		{"native ignores top level cap", "maxOutputTokens", nil, map[string]any{"max_tokens": 1}, StreamRequest{MaxTokens: 12000}, 12000, "maxOutputTokens"},
		{"native snake alias", "maxOutputTokens", nil, map[string]any{"generation_config": map[string]any{"max_output_tokens": 15000}}, StreamRequest{MaxTokens: 12000}, 15000, "maxOutputTokens"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.ContextWindowTokens = 32000
			if err := validateProviderContextBudget(tc.req, "unknown-budget-model", tc.body, tc.field, tc.aliases...); err != nil {
				t.Fatal(err)
			}
			container := tc.body
			if tc.field == "maxOutputTokens" {
				container = tc.body["generationConfig"].(map[string]any)
			}
			if got := budgetInt(container[tc.key]); got != tc.want {
				t.Fatalf("wire cap=%d want=%d body=%v", got, tc.want, tc.body)
			}
		})
	}
}

func TestContextBudgetReviewImageSiblingsAreCounted(t *testing.T) {
	for _, image := range []map[string]any{
		{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": strings.Repeat("A", 100000)}, "extra_text": strings.Repeat("x", 10000)},
		{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + strings.Repeat("A", 100000), "detail": "high"}, "extra_text": strings.Repeat("x", 10000)},
		{"inlineData": map[string]any{"mimeType": "image/png", "data": strings.Repeat("A", 100000)}, "text": strings.Repeat("x", 10000)},
		{"inlineData": map[string]any{"mimeType": "text/plain", "data": strings.Repeat("A", 100000)}},
		{"type": "image", "description": strings.Repeat("x", 10000)},
	} {
		body := map[string]any{"messages": []any{image}, "max_tokens": 500}
		err := validateProviderContextBudget(StreamRequest{ContextWindowTokens: 3200}, "unknown-budget-model", body, "max_tokens")
		var exceeded *ContextBudgetExceededError
		if !errors.As(err, &exceeded) || exceeded.EstimatedInputTokens < 3300 {
			t.Fatalf("image swallowed text/schema siblings: %v", err)
		}
	}
	body := map[string]any{"messages": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + strings.Repeat("A", 100000)}}}, "max_tokens": 500}
	if err := validateProviderContextBudget(StreamRequest{ContextWindowTokens: 3200}, "unknown-budget-model", body, "max_tokens"); err != nil {
		t.Fatalf("base64 counted as text: %v", err)
	}
}

func TestContextBudgetReviewMalformedOverrideNoHTTPOrCooldown(t *testing.T) {
	for _, value := range []any{-1, 1.5, math.NaN(), int64(math.MaxInt64)} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
		channel := &legacyruntime.ResolvedChannel{ID: "invalid-output", Model: "unknown-budget-model", Provider: "openai", BaseURL: server.URL, APIKey: "key", ProtocolMode: "fixed", ProtocolGroup: "chat_completions", OpenAIEndpoint: "/v1/chat/completions"}
		router := NewRouter(&reasoningLimitResolver{channel: channel})
		err := router.Stream(context.Background(), StreamRequest{ModelID: "internal", ContextWindowTokens: 32000, RequestBodyOverride: map[string]any{"model": "unknown-budget-model", "messages": []any{}, "max_tokens": value, "stream": true}}, func(ModelEvent) error { return nil })
		var invalid *ContextBudgetValidationError
		// Huge integers may fail numeric normalization or the window check;
		// either way they must remain a local, pre-send rejection.
		var exceeded *ContextBudgetExceededError
		if !errors.As(err, &invalid) && !errors.As(err, &exceeded) {
			t.Errorf("value=%v got %v", value, err)
		}
		if calls.Load() != 0 || len(router.healthByChannel) != 0 {
			t.Errorf("local error sent/cooled: calls=%d health=%v", calls.Load(), router.healthByChannel)
		}
		server.Close()
	}
}

func TestContextBudgetReviewOverridePreservesPositiveOutputOnWire(t *testing.T) {
	for _, provider := range []string{"chat", "responses", "anthropic", "gemini", "reasoning-chat"} {
		t.Run(provider, func(t *testing.T) {
			var captured map[string]any
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
					t.Error(err)
				}
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"error":{"message":"captured"}}`)
			}))
			defer server.Close()
			req := StreamRequest{ModelID: "unknown-budget-model", BaseURL: server.URL, APIKey: "key", ContextWindowTokens: 32000, RequestedContextWindowTokens: 64000, MaxTokens: 12000, AnthropicMaxTokens: 9000, ProtocolMode: "fixed", RequestBodyOverride: map[string]any{"model": "unknown-budget-model", "messages": []any{}, "input": []any{}, "contents": []any{}, "stream": true}}
			var err error
			sink := func(ModelEvent) error { return nil }
			switch provider {
			case "chat", "reasoning-chat":
				req.OpenAIEndpoint = "/v1/chat/completions"
				req.ProtocolGroup = "chat_completions"
				if provider == "reasoning-chat" {
					req.ProviderModelID = "gpt-5"
					req.RequestBodyOverride["model"] = "gpt-5"
				}
				err = (&OpenAIAdapter{client: server.Client()}).Stream(context.Background(), req, sink)
			case "responses":
				req.OpenAIEndpoint = "/v1/responses"
				req.ProtocolGroup = "responses"
				err = (&OpenAIAdapter{client: server.Client()}).Stream(context.Background(), req, sink)
			case "anthropic":
				err = (&AnthropicAdapter{client: server.Client()}).Stream(context.Background(), req, sink)
			case "gemini":
				err = (&GeminiAdapter{client: server.Client()}).Stream(context.Background(), req, sink)
			}
			var status *HTTPStatusError
			if !errors.As(err, &status) || calls.Load() != 1 {
				t.Fatalf("expected captured HTTP body, got %v calls=%d", err, calls.Load())
			}
			key := "max_tokens"
			want := 12000
			container := captured
			if provider == "responses" {
				key = "max_output_tokens"
			}
			if provider == "reasoning-chat" {
				key = "max_completion_tokens"
				if _, bad := captured["max_tokens"]; bad {
					t.Fatal("default cap reintroduced deprecated max_tokens")
				}
			}
			if provider == "anthropic" {
				want = 9000
			}
			if provider == "gemini" {
				key = "maxOutputTokens"
				container = captured["generationConfig"].(map[string]any)
			}
			if got := budgetInt(container[key]); got != want {
				t.Fatalf("configured positive cap replaced: got=%d want=%d", got, want)
			}
		})
	}
}

func TestContextBudgetReviewActualWireModelCapability(t *testing.T) {
	body := map[string]any{"model": "gpt-4o", "messages": []any{}, "max_tokens": 150000}
	err := validateProviderContextBudget(StreamRequest{ContextWindowTokens: 200000}, "unknown-channel-model", body, "max_tokens")
	var exceeded *ContextBudgetExceededError
	if !errors.As(err, &exceeded) || exceeded.ContextWindowTokens >= 200000 {
		t.Fatalf("override wire model bypassed capability: %v", err)
	}
}

func TestContextBudgetReviewCancellationAfterDeltaKeepsNoReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	channel := &legacyruntime.ResolvedChannel{ID: "cancel-budget", Provider: "openai", Model: "unknown-budget-model"}
	adapter := &budgetEventAdapter{}
	router := NewRouter(&reasoningLimitResolver{channel: channel})
	router.openai = adapter
	err := router.Stream(ctx, StreamRequest{ModelID: "internal"}, func(ModelEvent) error {
		cancel()
		return &ContextBudgetExceededError{ContextWindowTokens: 32000, EstimatedInputTokens: 40000, MaxOutputTokens: 1000}
	})
	var budget *ContextBudgetExceededError
	if !errors.As(err, &budget) || !budget.OutputStarted || !errors.Is(err, ErrMidStreamInterrupted) || adapter.calls != 1 {
		t.Fatalf("cancel fast path lost no-replay marker: err=%v calls=%d", err, adapter.calls)
	}
}

func TestContextBudgetReviewFailoverFinalExtensions(t *testing.T) {
	for _, extensionSize := range []int{30000, 70000} {
		t.Run(strconv.Itoa(extensionSize), func(t *testing.T) {
			var largeCalls, smallCalls atomic.Int32
			var smallBody map[string]any
			large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				largeCalls.Add(1)
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `{"error":{"message":"unavailable"}}`)
			}))
			defer large.Close()
			small := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				smallCalls.Add(1)
				if err := json.NewDecoder(r.Body).Decode(&smallBody); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer small.Close()
			extra, _ := json.Marshal(map[string]any{"vendor_context": strings.Repeat("x", extensionSize)})
			a := &legacyruntime.ResolvedChannel{ID: "large", Provider: "openai", Model: "unknown-budget-model", BaseURL: large.URL, APIKey: "key", ContextWindowTokens: 64000, MaxTokens: 1000, ProtocolMode: "fixed", ProtocolGroup: "chat_completions", OpenAIEndpoint: "/v1/chat/completions"}
			b := &legacyruntime.ResolvedChannel{ID: "small", Provider: "openai", Model: "unknown-budget-model", BaseURL: small.URL, APIKey: "key", ContextWindowTokens: 32000, MaxTokens: 1000, ProtocolMode: "fixed", ProtocolGroup: "chat_completions", OpenAIEndpoint: "/v1/chat/completions", OpenAIExtraParamsEnabled: true, OpenAIExtraParamsJSON: string(extra)}
			router := NewRouter(&scriptedResolver{orders: [][]*legacyruntime.ResolvedChannel{{a, b}, {b, a}}})
			req := StreamRequest{ModelID: "internal", RequestedContextWindowTokens: 32000, ContextWindowTokens: 64000, MaxMode: true, Messages: []Message{{Role: "user", Content: strings.Repeat("x", 30000)}}, RequestKnobs: map[string]any{}}
			err := router.Stream(context.Background(), req, func(ModelEvent) error { return nil })
			if largeCalls.Load() != 1 {
				t.Fatalf("large calls=%d", largeCalls.Load())
			}
			if extensionSize == 70000 {
				var budget *ContextBudgetExceededError
				if !errors.As(err, &budget) || budget.ContextWindowTokens != 32000 || smallCalls.Load() != 0 {
					t.Fatalf("final extension bypass or HTTP masking: err=%v smallCalls=%d", err, smallCalls.Load())
				}
				if _, cooled := router.healthByChannel[channelHealthMapKey(b)]; cooled {
					t.Fatal("budget-rejected failover cooled")
				}
			} else {
				if err != nil || smallCalls.Load() != 1 {
					t.Fatalf("valid fallback blocked: %v calls=%d", err, smallCalls.Load())
				}
				if len(smallBody["vendor_context"].(string)) != extensionSize {
					t.Fatal("extension absent from captured payload")
				}
				input, _ := req.RequestKnobs["estimated_input_tokens"].(int64)
				if req.RequestKnobs["context_window_tokens"] != 32000 || input+int64(budgetInt(smallBody["max_tokens"])) > 32000 {
					t.Fatalf("final body does not fit selected ceiling: %v", req.RequestKnobs)
				}
			}
		})
	}
}
