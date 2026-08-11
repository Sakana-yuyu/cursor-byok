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

func TestDebugRecorderLogRuntimeLazySkipsFieldBuilderWhenDisabled(t *testing.T) {
	recorder := newDebugRecorder(t.TempDir(), nil, stubDebugLogConfig{enabled: false})
	builderCalls := 0

	recorder.LogRuntimeLazy(context.Background(), "request-1", "conversation-1", "text_delta_forwarded", func() map[string]any {
		builderCalls++
		return map[string]any{"delta_sha256": "should-not-be-computed"}
	})

	if builderCalls != 0 {
		t.Fatalf("field builder calls = %d, want 0", builderCalls)
	}
}

func TestShouldWarnDebugQueueDropSamplesFirstAndInterval(t *testing.T) {
	tests := []struct {
		name         string
		droppedTotal uint64
		want         bool
	}{
		{name: "zero", droppedTotal: 0, want: false},
		{name: "first", droppedTotal: 1, want: true},
		{name: "before interval", droppedTotal: debugQueueDropWarningInterval - 1, want: false},
		{name: "interval", droppedTotal: debugQueueDropWarningInterval, want: true},
		{name: "after interval", droppedTotal: debugQueueDropWarningInterval + 1, want: false},
		{name: "second interval", droppedTotal: debugQueueDropWarningInterval * 2, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldWarnDebugQueueDrop(test.droppedTotal); got != test.want {
				t.Fatalf("shouldWarnDebugQueueDrop(%d) = %t, want %t", test.droppedTotal, got, test.want)
			}
		})
	}
}
