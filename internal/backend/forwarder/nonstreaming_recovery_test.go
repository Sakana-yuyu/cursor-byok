package forwarder

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"cursor/gen/agentv1"
	execbridge "cursor/internal/backend/agent/bridge/exec"
	runtimecore "cursor/internal/backend/agent/core"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestService creates a Service wired to the broker with all fields the
// non-streaming recovery path touches.  The caller must assign
// nonStreamingCloseGrace and nonStreamingRecoveryTimer if needed.
func newTestService(broker *StreamBroker, grace func() time.Duration, timerFn func(string, time.Duration) <-chan time.Time) *Service {
	return &Service{
		broker:                    broker,
		execBridge:                execbridge.NewBridge(),
		nonStreamingCloseGrace:    grace,
		nonStreamingRecoveryTimer: timerFn,
	}
}

// setupCheckpointConversation stores a minimal checkpoint conversation on the
// stream so that appendToolResult / appendConversationEntries succeed.
func setupCheckpointConversation(stream *ActiveStream) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.CheckpointConversation = &ConversationFile{
		ConversationID: stream.ConversationID,
		Mode:           "agent",
		Entries:        []HistoryEntry{},
	}
}

// snapshotHistoryEntries returns a copy of the in-memory checkpoint entries.
func snapshotHistoryEntries(stream *ActiveStream) []HistoryEntry {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.CheckpointConversation == nil {
		return nil
	}
	out := make([]HistoryEntry, len(stream.CheckpointConversation.Entries))
	copy(out, stream.CheckpointConversation.Entries)
	return out
}

// countHistoryByKind returns the number of checkpoint entries whose Kind
// matches want.
func countHistoryByKind(stream *ActiveStream, want string) int {
	n := 0
	for _, e := range snapshotHistoryEntries(stream) {
		if e.Kind == want {
			n++
		}
	}
	return n
}

// pendingExecIsTransportClosed returns true when the exec exists and its
// StreamState == "transport_closed".
func pendingExecIsTransportClosed(stream *ActiveStream, execID string) bool {
	p, ok := snapshotPendingExec(stream, execID)
	return ok && p.StreamState == "transport_closed"
}

// awaitCondition polls cond() at interval until it returns true or deadline
// expires.  Returns nil on success, error on timeout.
func awaitCondition(deadline time.Time, interval time.Duration, cond func() bool) error {
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("timeout waiting for condition at %v", deadline)
}

// ---------------------------------------------------------------------------
// injectableTimer is a test-controlled timer channel.  Call fire() to close
// the underlying channel (triggering the timer path exactly once).
// ---------------------------------------------------------------------------
type injectableTimer struct {
	ch   chan time.Time
	once sync.Once
}

func newInjectableTimer() *injectableTimer {
	return &injectableTimer{ch: make(chan time.Time)}
}

func (it *injectableTimer) fire() {
	it.once.Do(func() { close(it.ch) })
}

func (it *injectableTimer) Chan() <-chan time.Time { return it.ch }

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestNonStreamingLateTerminalResult verifies:
//  1. stream_close marks transport_closed and starts the signal goroutine.
//  2. A real terminal result arriving via handleExecResult wakes the signal,
//     prevents synthetic recovery, and produces exactly one tool_result history
//     entry.
//  3. No duplicate tool_result appears after the grace window.
func TestNonStreamingLateTerminalResult(t *testing.T) {
	broker := NewStreamBroker()
	stream, err := broker.OpenStream("req-late", "conv-late", 1, "m", "m", agentv1.AgentMode_AGENT_MODE_AGENT, "test")
	if err != nil {
		t.Fatal(err)
	}
	setupCheckpointConversation(stream)

	execID := "exec-late"
	pending := runtimecore.PendingExec{
		ExecID:       execID,
		MessageID:    1,
		ExecKind:     "read",
		ToolCallID:   "tool-late",
		ArgsJSON:     []byte(`{}`),
		ModelCallID:  "mc-late",
		ProviderPass: 1,
		OpenedAt:     time.Now(),
	}
	stream.mu.Lock()
	stream.PendingExecs[execID] = pending
	stream.TimerTokens = make(map[string]uint64)
	stream.StreamTimers = make(map[string]*time.Timer)
	stream.mu.Unlock()

	// Use a long grace so the real result always wins; timer never fires.
	it := newInjectableTimer()
	service := newTestService(broker,
		func() time.Duration { return 10 * time.Second },
		func(_ string, _ time.Duration) <-chan time.Time { return it.Chan() },
	)

	// Step 1: send stream_close.
	ctrlMsg := &agentv1.ExecClientControlMessage{
		Message: &agentv1.ExecClientControlMessage_StreamClose{
			StreamClose: &agentv1.ExecClientStreamClose{Id: 1},
		},
	}
	if err := service.handleExecControl(InboundIntent{
		Kind:                     "exec_control",
		RequestID:                "req-late",
		ConversationID:           "conv-late",
		ExecClientControlMessage: ctrlMsg,
	}); err != nil {
		t.Fatalf("handleExecControl stream_close: %v", err)
	}

	// Verify signal registered and exec = transport_closed.
	stream.mu.Lock()
	_, sigExists := stream.ExecCompletionSignals[execID]
	stream.mu.Unlock()
	if !sigExists {
		t.Fatal("expected completion signal after stream_close")
	}
	if !pendingExecIsTransportClosed(stream, execID) {
		t.Fatal("exec state must be transport_closed after stream_close")
	}

	// No history yet.
	if n := countHistoryByKind(stream, "tool_result"); n != 0 {
		t.Fatalf("expected 0 tool_result entries before terminal result, got %d", n)
	}

	// Step 2: deliver real terminal result via handleExecResult.
	execMsg := &agentv1.ExecClientMessage{
		ExecId: execID,
		Id:     1,
		Message: &agentv1.ExecClientMessage_ReadResult{
			ReadResult: &agentv1.ReadResult{
				Result: &agentv1.ReadResult_Success{
					Success: &agentv1.ReadSuccess{
						Output: &agentv1.ReadSuccess_Content{Content: "test content"},
					},
				},
			},
		},
	}
	if err := service.handleExecResult(InboundIntent{
		Kind:              "exec_result",
		RequestID:         "req-late",
		ConversationID:    "conv-late",
		ExecClientMessage: execMsg,
	}); err != nil {
		t.Fatalf("handleExecResult: %v", err)
	}

	// Exec is completed.
	if _, ok := snapshotPendingExec(stream, execID); ok {
		t.Fatal("exec should be completed after terminal result")
	}

	// Exactly one tool_result entry.
	if n := countHistoryByKind(stream, "tool_result"); n != 1 {
		t.Fatalf("expected 1 tool_result entry after terminal result, got %d", n)
	}

	// Signal cleaned.
	stream.mu.Lock()
	_, sigExists = stream.ExecCompletionSignals[execID]
	stream.mu.Unlock()
	if sigExists {
		t.Fatal("completion signal should be cleaned up")
	}

	// Step 3: fire the timer channel (simulating grace expiry after terminal).
	// The goroutine should already have exited via signal – firing the timer
	// must be harmless.
	it.fire()

	// Wait a bit and ensure no new history entries appeared.
	time.Sleep(50 * time.Millisecond)
	if n := countHistoryByKind(stream, "tool_result"); n != 1 {
		t.Fatalf("expected exactly 1 tool_result after grace, got %d", n)
	}
}

// TestStreamCloseTerminalTimeout verifies:
//  1. stream_close starts the goroutine.
//  2. After the test-controlled timeout fires, exactly one synthetic
//     tool_result and one tool_transport_closed metadata entry exist.
//  3. A late terminal result after recovery does NOT append a second
//     tool_result.
func TestStreamCloseTerminalTimeout(t *testing.T) {
	broker := NewStreamBroker()
	stream, err := broker.OpenStream("req-timeout", "conv-timeout", 1, "m", "m", agentv1.AgentMode_AGENT_MODE_AGENT, "test")
	if err != nil {
		t.Fatal(err)
	}
	setupCheckpointConversation(stream)

	execID := "exec-timeout"
	pending := runtimecore.PendingExec{
		ExecID:       execID,
		MessageID:    2,
		ExecKind:     "read",
		ToolCallID:   "tool-timeout",
		ArgsJSON:     []byte(`{}`),
		ModelCallID:  "mc-timeout",
		ProviderPass: 1,
		OpenedAt:     time.Now(),
	}
	stream.mu.Lock()
	stream.PendingExecs[execID] = pending
	stream.TimerTokens = make(map[string]uint64)
	stream.StreamTimers = make(map[string]*time.Timer)
	stream.mu.Unlock()

	it := newInjectableTimer()
	service := newTestService(broker,
		func() time.Duration { return 30 * time.Second }, // production-like; timer is injected
		func(_ string, _ time.Duration) <-chan time.Time { return it.Chan() },
	)

	// Send stream_close.
	ctrlMsg := &agentv1.ExecClientControlMessage{
		Message: &agentv1.ExecClientControlMessage_StreamClose{
			StreamClose: &agentv1.ExecClientStreamClose{Id: 2},
		},
	}
	if err := service.handleExecControl(InboundIntent{
		Kind:                     "exec_control",
		RequestID:                "req-timeout",
		ConversationID:           "conv-timeout",
		ExecClientControlMessage: ctrlMsg,
	}); err != nil {
		t.Fatalf("handleExecControl stream_close: %v", err)
	}

	// Verify signal.
	stream.mu.Lock()
	_, sigExists := stream.ExecCompletionSignals[execID]
	stream.mu.Unlock()
	if !sigExists {
		t.Fatal("expected completion signal after stream_close")
	}

	// Trigger the timeout deterministically.
	it.fire()

	// Wait for the actor to process the posted timer event (retry with deadline).
	if err := awaitCondition(time.Now().Add(5*time.Second), 5*time.Millisecond, func() bool {
		_, ok := snapshotPendingExec(stream, execID)
		return !ok
	}); err != nil {
		t.Fatalf("exec not completed after timeout: %v", err)
	}

	// Exactly one synthetic tool_result.
	if n := countHistoryByKind(stream, "tool_result"); n != 1 {
		t.Fatalf("expected 1 synthetic tool_result after timeout, got %d", n)
	}
	// And one metadata entry (event type "tool_transport_closed" inside the payload).
	if n := countHistoryByKind(stream, "metadata"); n != 1 {
		t.Fatalf("expected 1 metadata entry after timeout, got %d", n)
	}

	// Signal cleaned.
	stream.mu.Lock()
	_, sigExists = stream.ExecCompletionSignals[execID]
	stream.mu.Unlock()
	if sigExists {
		t.Fatal("completion signal should be cleaned up after recovery")
	}

	// Step 3: send a late terminal result — must NOT append a second tool_result.
	execMsg := &agentv1.ExecClientMessage{
		ExecId: execID,
		Id:     2,
		Message: &agentv1.ExecClientMessage_ReadResult{
			ReadResult: &agentv1.ReadResult{
				Result: &agentv1.ReadResult_Success{
					Success: &agentv1.ReadSuccess{
						Output: &agentv1.ReadSuccess_Content{Content: "late data"},
					},
				},
			},
		},
	}
	err = service.handleExecResult(InboundIntent{
		Kind:              "exec_result",
		RequestID:         "req-timeout",
		ConversationID:    "conv-timeout",
		ExecClientMessage: execMsg,
	})
	// The late result may be silently ignored (recently-completed tombstone)
	// or rejected with "not found".  Either is correct as long as no second
	// tool_result appears.
	_ = err

	// Still exactly one tool_result.
	if n := countHistoryByKind(stream, "tool_result"); n != 1 {
		t.Fatalf("expected exactly 1 tool_result after late terminal, got %d", n)
	}
}

// TestStreamCloseTerminalCancel verifies:
//  1. stream_close starts the goroutine.
//  2. Calling the real cancel intent closes the signal so the waiter exits.
//  3. No synthetic recovery fires after the grace expires.
//  4. Signal/token maps are cleaned.
func TestStreamCloseTerminalCancel(t *testing.T) {
	broker := NewStreamBroker()
	stream, err := broker.OpenStream("req-cancel", "conv-cancel", 1, "m", "m", agentv1.AgentMode_AGENT_MODE_AGENT, "test")
	if err != nil {
		t.Fatal(err)
	}
	setupCheckpointConversation(stream)

	execID := "exec-cancel"
	pending := runtimecore.PendingExec{
		ExecID:       execID,
		MessageID:    3,
		ExecKind:     "read",
		ToolCallID:   "tool-cancel",
		ArgsJSON:     []byte(`{}`),
		ModelCallID:  "mc-cancel",
		ProviderPass: 1,
		OpenedAt:     time.Now(),
	}
	stream.mu.Lock()
	stream.PendingExecs[execID] = pending
	stream.TimerTokens = make(map[string]uint64)
	stream.StreamTimers = make(map[string]*time.Timer)
	stream.mu.Unlock()

	it := newInjectableTimer()
	service := newTestService(broker,
		func() time.Duration { return 30 * time.Second },
		func(_ string, _ time.Duration) <-chan time.Time { return it.Chan() },
	)

	// Send stream_close.
	ctrlMsg := &agentv1.ExecClientControlMessage{
		Message: &agentv1.ExecClientControlMessage_StreamClose{
			StreamClose: &agentv1.ExecClientStreamClose{Id: 3},
		},
	}
	if err := service.handleExecControl(InboundIntent{
		Kind:                     "exec_control",
		RequestID:                "req-cancel",
		ConversationID:           "conv-cancel",
		ExecClientControlMessage: ctrlMsg,
	}); err != nil {
		t.Fatalf("handleExecControl stream_close: %v", err)
	}

	// Verify signal.
	if _, ok := func() (chan struct{}, bool) {
		stream.mu.Lock()
		defer stream.mu.Unlock()
		ch, ok := stream.ExecCompletionSignals[execID]
		return ch, ok
	}(); !ok {
		t.Fatal("expected completion signal after stream_close")
	}

	// Call the real cancel path.
	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      "req-cancel",
		ConversationID: "conv-cancel",
		CancelReason:   "test cancel",
	}); err != nil {
		t.Fatalf("handleCancelIntent: %v", err)
	}

	// Signal must be cleaned.
	stream.mu.Lock()
	_, sigExists := stream.ExecCompletionSignals[execID]
	stream.mu.Unlock()
	if sigExists {
		t.Fatal("completion signal should be cleaned after cancel")
	}

	// Fire the timer channel; goroutine must have already exited via signal
	// (closed by cleanupAllPendingExecs from handleCancelIntent). The fire
	// must be harmless.
	it.fire()

	// Wait a bit and ensure no synthetic recovery fired.
	time.Sleep(50 * time.Millisecond)
	if n := countHistoryByKind(stream, "tool_result"); n != 0 {
		t.Fatalf("expected 0 tool_result entries after cancel, got %d", n)
	}
}

// TestNonStreamingRecoveryTokenInvalidation verifies cross-turn isolation:
//  1. stream_close registers a recovery waiter with a specific token.
//  2. When the token is invalidated (simulating stopAllStreamTimersLocked or
//     a new turn resetting TimerTokens), the actor rejects the stale event
//     because timerEventMatches fails.
//  3. No synthetic tool_result appears when the old (stale) timer fires.
func TestNonStreamingRecoveryTokenInvalidation(t *testing.T) {
	broker := NewStreamBroker()
	stream, err := broker.OpenStream("req-cross", "conv-cross", 1, "m", "m", agentv1.AgentMode_AGENT_MODE_AGENT, "test")
	if err != nil {
		t.Fatal(err)
	}
	setupCheckpointConversation(stream)

	execID := "exec-cross"
	pending := runtimecore.PendingExec{
		ExecID:       execID,
		MessageID:    4,
		ExecKind:     "read",
		ToolCallID:   "tool-cross",
		ArgsJSON:     []byte(`{}`),
		ModelCallID:  "mc-cross",
		ProviderPass: 1,
		OpenedAt:     time.Now(),
	}
	stream.mu.Lock()
	stream.PendingExecs[execID] = pending
	stream.TimerTokens = make(map[string]uint64)
	stream.StreamTimers = make(map[string]*time.Timer)
	stream.mu.Unlock()

	it := newInjectableTimer()
	service := newTestService(broker,
		func() time.Duration { return 30 * time.Second },
		func(_ string, _ time.Duration) <-chan time.Time { return it.Chan() },
	)

	// Send stream_close for turn T.
	ctrlMsg := &agentv1.ExecClientControlMessage{
		Message: &agentv1.ExecClientControlMessage_StreamClose{
			StreamClose: &agentv1.ExecClientStreamClose{Id: 4},
		},
	}
	if err := service.handleExecControl(InboundIntent{
		Kind:                     "exec_control",
		RequestID:                "req-cross",
		ConversationID:           "conv-cross",
		ExecClientControlMessage: ctrlMsg,
	}); err != nil {
		t.Fatalf("handleExecControl stream_close: %v", err)
	}

	// Verify the token was registered.
	key := providerTimerKey(streamTimerNonStreamingRecovery, execID)
	stream.mu.Lock()
	originalToken := stream.TimerTokens[key]
	sigExists := stream.ExecCompletionSignals[execID] != nil
	stream.mu.Unlock()
	if originalToken == 0 {
		t.Fatal("expected non-zero token registered under stream.mu")
	}
	if !sigExists {
		t.Fatal("expected completion signal after stream_close")
	}

	// Invalidate the token (same effect as stopAllStreamTimersLocked or a
	// new turn allocating fresh TimerTokens).
	stream.mu.Lock()
	stream.TimerTokens[key]++ // now stale
	stream.mu.Unlock()

	// Fire the injectable timer — the goroutine posts with the stale token.
	it.fire()

	// Wait for the actor to process.  The event carries the stale token, so
	// timerEventMatches returns false and handleTimerEvent returns nil
	// without calling recoverNonStreamingExecAfterStreamClose.
	time.Sleep(200 * time.Millisecond)

	// No synthetic tool_result must have appeared.
	if n := countHistoryByKind(stream, "tool_result"); n != 0 {
		t.Fatalf("expected 0 tool_result entries after stale-token event, got %d", n)
	}

	// Exec is still pending (recovery was rejected).
	if _, ok := snapshotPendingExec(stream, execID); !ok {
		t.Fatal("exec should still be pending — stale-token recovery was rejected")
	}
}
