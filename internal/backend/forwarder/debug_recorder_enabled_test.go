package forwarder

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type countingDebugLogConfig struct {
	enabledCalls atomic.Int32
}

func (config *countingDebugLogConfig) IsObservabilityLogEnabled(context.Context) bool {
	config.enabledCalls.Add(1)
	return true
}

func (config *countingDebugLogConfig) DebugLogMaxBytes(context.Context) int { return -1 }

func TestLogRunSSEChecksObservabilitySettingOnce(t *testing.T) {
	config := &countingDebugLogConfig{}
	recorder := newDebugRecorder("", nil, config)

	recorder.LogRunSSE(context.Background(), "request", "conversation", "send_message", map[string]any{"cursor": 1})

	if got := config.enabledCalls.Load(); got != 1 {
		t.Fatalf("IsObservabilityLogEnabled() calls = %d, want 1", got)
	}
}

func TestLogProviderChecksObservabilitySettingOnce(t *testing.T) {
	config := &countingDebugLogConfig{}
	recorder := newDebugRecorder("", nil, config)

	recorder.LogProvider(context.Background(), "request", "conversation", "provider_request_prepared", map[string]any{"provider_pass": 1})

	if got := config.enabledCalls.Load(); got != 1 {
		t.Fatalf("IsObservabilityLogEnabled() calls = %d, want 1", got)
	}
}

func TestLogProviderArtifactChecksObservabilitySettingOnce(t *testing.T) {
	config := &countingDebugLogConfig{}
	recorder := newDebugRecorder("", nil, config)

	recorder.LogProviderArtifact(context.Background(), "request", "conversation", "call", "llm_request", map[string]any{"model": "test"})

	if got := config.enabledCalls.Load(); got != 1 {
		t.Fatalf("IsObservabilityLogEnabled() calls = %d, want 1", got)
	}
}

func TestLogBidiRawChecksObservabilitySettingOnce(t *testing.T) {
	config := &countingDebugLogConfig{}
	recorder := newDebugRecorder("", nil, config)

	recorder.LogBidiRaw(context.Background(), "request", "conversation", 1, "abcd", "ok", nil)

	if got := config.enabledCalls.Load(); got != 1 {
		t.Fatalf("IsObservabilityLogEnabled() calls = %d, want 1", got)
	}
}

func TestLogProviderArtifactKeepsProviderEventShape(t *testing.T) {
	root := t.TempDir()
	recorder := newDebugRecorder(root, nil, &countingDebugLogConfig{})
	recorder.LogProviderArtifact(context.Background(), "request", "conversation", "call", "llm_request", map[string]any{"model": "test"})

	path := filepath.Join(root, "conversation", "debug", "provider.jsonl")
	var payload []byte
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			payload = data
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(payload) == 0 {
		t.Fatalf("provider artifact log was not written: %s", path)
	}

	var event struct {
		Layer       string         `json:"layer"`
		Event       string         `json:"event"`
		ModelCallID string         `json:"model_call_id"`
		Payload     map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal provider artifact: %v", err)
	}
	if event.Layer != "provider" || event.Event != "llm_request" || event.ModelCallID != "call" || event.Payload["model"] != "test" {
		t.Fatalf("unexpected provider artifact event: %#v", event)
	}
}
