package modeladapter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"cursor/internal/modelchannel"
)

type anthropicRoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn anthropicRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestAnthropicStreamAuthModesAndCustomAuthPrecedence(t *testing.T) {
	tests := []struct {
		name              string
		serverURL         string
		mode              string
		customHeaders     string
		customEnabled     bool
		wantAPIKey        string
		wantAuthorization string
	}{
		{name: "legacy dual", mode: modelchannel.AnthropicAuthModeLegacyDual, wantAPIKey: "token", wantAuthorization: "Bearer token"},
		{name: "explicit API key", mode: modelchannel.AnthropicAuthModeAPIKey, wantAPIKey: "token"},
		{name: "explicit bearer", mode: modelchannel.AnthropicAuthModeBearer, wantAuthorization: "Bearer token"},
		{name: "custom authorization suppresses generated", mode: modelchannel.AnthropicAuthModeAPIKey, customEnabled: true, customHeaders: `{"Authorization":"Bearer custom"}`, wantAuthorization: "Bearer custom"},
		{name: "custom API key suppresses generated", mode: modelchannel.AnthropicAuthModeBearer, customEnabled: true, customHeaders: `{"X-API-KEY":"custom-key"}`, wantAPIKey: "custom-key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAPIKey, gotAuthorization string
			server := newAnthropicTestServer(t, func(request *http.Request) {
				gotAPIKey = request.Header.Get("x-api-key")
				gotAuthorization = request.Header.Get("Authorization")
			})
			defer server.Close()
			adapter := &AnthropicAdapter{client: server.Client()}
			_, err := runAnthropicTestStream(t, adapter, StreamRequest{
				BaseURL: server.URL + "/v1", APIKey: "Bearer token", ProviderModelID: "claude-test", ModelCallID: "call-1",
				AnthropicAuthMode: tt.mode, CustomHeadersEnabled: tt.customEnabled, CustomHeadersJSON: tt.customHeaders,
			}, canonicalAnthropicSSE())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			if gotAPIKey != tt.wantAPIKey || gotAuthorization != tt.wantAuthorization {
				t.Fatalf("headers x-api-key=%q Authorization=%q, want %q/%q", gotAPIKey, gotAuthorization, tt.wantAPIKey, tt.wantAuthorization)
			}
		})
	}
}

func TestAnthropicStreamAcceptsCompleteRelayEOFWithoutMessageStop(t *testing.T) {
	server := newAnthropicTestServer(t, nil)
	defer server.Close()
	adapter := &AnthropicAdapter{client: server.Client()}
	events, err := runAnthropicTestStream(t, adapter, StreamRequest{BaseURL: server.URL + "/v1", APIKey: "token", ProviderModelID: "claude-test", ModelCallID: "call-1"}, relayEOFSSE())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if countAnthropicTurnFinished(events) != 1 {
		t.Fatalf("TurnFinished count = %d, events=%#v", countAnthropicTurnFinished(events), events)
	}
}

func TestAnthropicStreamRejectsTruncatedEOFWithVisibleText(t *testing.T) {
	server := newAnthropicTestServer(t, nil)
	defer server.Close()
	adapter := &AnthropicAdapter{client: server.Client()}
	events, err := runAnthropicTestStream(t, adapter, StreamRequest{BaseURL: server.URL + "/v1", APIKey: "token", ProviderModelID: "claude-test", ModelCallID: "call-1"}, strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-test\"}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n",
	}, ""))
	if !errors.Is(err, ErrMidStreamInterrupted) {
		t.Fatalf("Stream() error = %v, want ErrMidStreamInterrupted", err)
	}
	if countAnthropicTurnFinished(events) != 0 {
		t.Fatalf("truncated stream emitted TurnFinished: %#v", events)
	}
}

func TestAnthropicProviderSSEErrorAfterOutputIsInterrupted(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-test\"}}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n",
			"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"capacity\"}}\n\n",
		}, "")))
	}))
	defer server.Close()
	adapter := &AnthropicAdapter{client: server.Client()}
	_, err := runAnthropicTestStream(t, adapter, StreamRequest{BaseURL: server.URL + "/v1", APIKey: "token", ProviderModelID: "claude-test", ModelCallID: "call-1"}, "")
	if !errors.Is(err, ErrMidStreamInterrupted) {
		t.Fatalf("Stream() error = %v, want ErrMidStreamInterrupted", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("post-output provider error triggered %d requests, want 1", calls.Load())
	}
}

func TestAnthropicStreamRejectsRelayEOFWithOpenToolBlock(t *testing.T) {
	server := newAnthropicTestServer(t, nil)
	defer server.Close()
	adapter := &AnthropicAdapter{client: server.Client()}
	_, err := runAnthropicTestStream(t, adapter, StreamRequest{BaseURL: server.URL + "/v1", APIKey: "token", ProviderModelID: "claude-test", ModelCallID: "call-1"}, strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-test\"}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-1\",\"name\":\"Read\"}}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n",
	}, ""))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Stream() error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func newAnthropicTestServer(t *testing.T, inspect func(*http.Request)) *httptest.Server {
	t.Helper()
	var body string
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if inspect != nil {
			inspect(request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
}

func runAnthropicTestStream(t *testing.T, adapter *AnthropicAdapter, req StreamRequest, body string) ([]ModelEvent, error) {
	t.Helper()
	if adapter == nil || adapter.client == nil {
		t.Fatal("test adapter is not initialized")
	}
	originalClient := adapter.client
	adapter.client = cloneAnthropicTestClient(t, originalClient, body)
	defer func() { adapter.client = originalClient }()
	var events []ModelEvent
	err := adapter.Stream(context.Background(), req, func(event ModelEvent) error {
		events = append(events, event)
		return nil
	})
	return events, err
}

func cloneAnthropicTestClient(t *testing.T, base *http.Client, body string) *http.Client {
	t.Helper()
	clone := *base
	previous := clone.Transport
	if previous == nil {
		previous = http.DefaultTransport
	}
	clone.Transport = anthropicRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		response, err := previous.RoundTrip(request)
		if err != nil || response == nil {
			return response, err
		}
		if body != "" {
			response.Body.Close()
			response.Body = io.NopCloser(strings.NewReader(body))
		}
		return response, nil
	})
	return &clone
}

func canonicalAnthropicSSE() string {
	return strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-test\"}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, "")
}

func relayEOFSSE() string {
	return strings.TrimSuffix(canonicalAnthropicSSE(), "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
}

func countAnthropicTurnFinished(events []ModelEvent) int {
	count := 0
	for _, event := range events {
		if event.Kind == ModelEventKindTurnFinished {
			count++
		}
	}
	return count
}
