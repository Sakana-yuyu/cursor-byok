package forwarder

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDebugRecorderCountsMarshalFailure(t *testing.T) {
	recorder := newDebugRecorder(t.TempDir(), nil, stubDebugLogConfig{enabled: true})

	recorder.appendJSONL(context.Background(), "request-1", "conversation-1", "runtime.jsonl", map[string]any{
		"unsupported": make(chan int),
	})

	health := recorder.healthSnapshot()
	if health.MarshalFailures != 1 {
		t.Fatalf("marshal failures = %d, want 1", health.MarshalFailures)
	}
}

func TestDebugRecorderCountsWriteFailure(t *testing.T) {
	root := t.TempDir()
	blockedDir := filepath.Join(root, "blocked")
	if err := os.WriteFile(blockedDir, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	recorder := newDebugRecorder(root, nil, stubDebugLogConfig{enabled: true})

	recorder.writeJob(debugWriteJob{
		dir:      blockedDir,
		filename: "runtime.jsonl",
		payload:  []byte(`{"event":"test"}`),
		epoch:    debugPurge.currentEpoch(),
	})

	health := recorder.healthSnapshot()
	if health.WriteFailures != 1 {
		t.Fatalf("write failures = %d, want 1", health.WriteFailures)
	}
}
