package modeladapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	legacyruntime "cursor/internal/runtime"
)

func TestRequestReplaySafetyClassification(t *testing.T) {
	base := StreamRequest{Messages: []Message{{Role: "user", Content: "hi"}}}
	if safety := requestReplaySafety(base); !safety.Safe {
		t.Fatalf("plain request marked unsafe: %#v", safety)
	}

	withOverride := base
	withOverride.RequestBodyOverride = map[string]any{"store": true}
	if safety := requestReplaySafety(withOverride); safety.Safe {
		t.Fatal("request body override must be unsafe")
	}

	for _, name := range []string{"web_search", "image_generation", "computer_use_preview", "code_interpreter"} {
		withTool := base
		withTool.Tools = []json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"` + name + `"}}`)}
		if safety := requestReplaySafety(withTool); safety.Safe {
			t.Fatalf("hosted tool %q must be unsafe", name)
		}
	}

	clientTool := base
	clientTool.Tools = []json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"bash"}}`)}
	if safety := requestReplaySafety(clientTool); !safety.Safe {
		t.Fatalf("client tool must remain replay-safe: %#v", safety)
	}
}

func TestStreamWithReconnectSkipsReconnectForUnsafeRequests(t *testing.T) {
	calls := 0
	stream := func(_ int, _ func(ModelEvent) error) error {
		calls++
		return io.EOF
	}
	err := streamWithReconnect(context.Background(), func(ModelEvent) error { return nil }, replaySafetyUnsafe("hosted tool"), stream)
	if !errors.Is(err, ErrReplayUnsafeDrop) || !errors.Is(err, io.EOF) {
		t.Fatalf("unsafe reset error = %v, want ErrReplayUnsafeDrop wrapping io.EOF", err)
	}
	if calls != 1 {
		t.Fatalf("unsafe request triggered %d stream attempts, want 1", calls)
	}
}

func TestStreamWithReconnectStillReconnectsSafeRequests(t *testing.T) {
	calls := 0
	stream := func(_ int, _ func(ModelEvent) error) error {
		calls++
		if calls == 1 {
			return io.EOF
		}
		return errors.New("non-reset failure")
	}
	err := streamWithReconnect(context.Background(), func(ModelEvent) error { return nil }, replaySafetySafe(), stream)
	if errors.Is(err, ErrReplayUnsafeDrop) {
		t.Fatalf("safe request was wrongly blocked: %v", err)
	}
	if calls != 2 {
		t.Fatalf("safe request made %d stream attempts, want 2 (reconnect)", calls)
	}
}

func TestRunOpenAIStreamWithReconnectSkipsReconnectForUnsafeRequests(t *testing.T) {
	adapter := &OpenAIAdapter{}
	calls := 0
	stream := func(_ int, _ func(ModelEvent) error) error {
		calls++
		return io.EOF
	}
	err := adapter.runOpenAIStreamWithReconnect(context.Background(), func(ModelEvent) error { return nil }, false, replaySafetyUnsafe("override"), stream)
	if !errors.Is(err, ErrReplayUnsafeDrop) {
		t.Fatalf("unsafe openai reset error = %v, want ErrReplayUnsafeDrop", err)
	}
	if calls != 1 {
		t.Fatalf("unsafe openai request triggered %d attempts, want 1", calls)
	}
}

func TestRouterStreamUnsafeRequestDoesNotRetryOrFailover(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		if hijacker, ok := w.(http.Hijacker); ok {
			conn, _, err := hijacker.Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		t.Fatal("httptest server does not support hijack")
	}))
	defer server.Close()

	resolver := &diagnosticsResolverStub{channels: []*legacyruntime.ResolvedChannel{{
		ID: "openai-channel", Name: "OpenAI", Provider: "openai", ProtocolMode: "fixed",
		ProtocolGroup: "chat_completions", BaseURL: server.URL, APIKey: "key", Model: "model-1",
		OpenAIEndpoint: "/v1/chat/completions", OpenAIRequestGroup: "chat_completions",
	}}}
	router := NewRouter(resolver)

	err := router.Stream(context.Background(), StreamRequest{
		ModelID:         "model-1",
		ProviderModelID: "model-1",
		Messages:        []Message{{Role: "user", Content: "hi"}},
		MaxTokens:       16,
		Stream:          true,
		Tools:           []json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"web_search"}}`)},
	}, func(ModelEvent) error { return nil })

	if !errors.Is(err, ErrReplayUnsafeDrop) {
		t.Fatalf("Stream() error = %v, want ErrReplayUnsafeDrop", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("unsafe request hit provider %d times, want 1", hits.Load())
	}
}
