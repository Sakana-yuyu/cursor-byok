package forwarder

import (
	"strings"
	"testing"
	"time"
)

// waitFor subscribes to a channel with a generous timeout, failing the test on timeout.
func waitFor(t *testing.T, ch <-chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatalf("timed out after %v", timeout)
	}
}

// TestTerminalReplayAfterUnsubscribe table-drives completed / canceled / failed
// terminal streams: each is subscribed, terminated, unsubscribed, then
// re-subscribed to verify the terminal backlog replays in full.  The
// completed variant additionally publishes a pre-terminal heartbeat and
// asserts RemoveIfIdle preservation.
func TestTerminalReplayAfterUnsubscribe(t *testing.T) {
	type terminalCase struct {
		name             string
		prepare          func(b *StreamBroker, rid string) // optional pre-terminal events
		terminalize      func(b *StreamBroker, rid string)
		wantEventCount   int
		wantTerminalCode string
	}
	cases := []terminalCase{
		{
			name: "completed",
			prepare: func(b *StreamBroker, rid string) {
				b.Publish(rid, StreamEvent{Message: buildHeartbeatMessage()})
			},
			terminalize: func(b *StreamBroker, rid string) {
				b.Complete(rid, "", "done")
			},
			wantEventCount: 2, // heartbeat + end
		},
		{
			name: "canceled",
			terminalize: func(b *StreamBroker, rid string) {
				b.Cancel(rid, "user canceled")
			},
			wantEventCount:   1,
			wantTerminalCode: "canceled",
		},
		{
			name: "failed",
			terminalize: func(b *StreamBroker, rid string) {
				b.FailWithDetails(rid, TerminalFailure{
					Code: "provider_error", Message: "provider failed",
				})
			},
			wantEventCount:   1,
			wantTerminalCode: "provider_error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			broker := NewStreamBroker()
			broker.terminalRetention = 500 * time.Millisecond

			rid := "request-" + tc.name
			cid := "conversation-" + tc.name

			_, err := broker.OpenStream(rid, cid, 1, "model", "model", 1, "hello")
			if err != nil {
				t.Fatalf("OpenStream: %v", err)
			}

			sub1, _, cursor, err := broker.Subscribe(rid)
			if err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
			if cursor != 0 {
				t.Fatalf("initial cursor = %d, want 0", cursor)
			}

			if tc.prepare != nil {
				tc.prepare(broker, rid)
			}
			tc.terminalize(broker, rid)

			// Verify terminal backlog before unsubscribe.
			events1, err := broker.ReadFromCursor(rid, cursor)
			if err != nil {
				t.Fatalf("ReadFromCursor: %v", err)
			}
			if len(events1) != tc.wantEventCount {
				t.Fatalf("backlog = %d events, want %d", len(events1), tc.wantEventCount)
			}
			if !events1[len(events1)-1].End {
				t.Fatal("last event should be terminal")
			}
			if tc.wantTerminalCode != "" && events1[len(events1)-1].TerminalErrorCode != tc.wantTerminalCode {
				t.Fatalf("terminal code = %q, want %q",
					events1[len(events1)-1].TerminalErrorCode, tc.wantTerminalCode)
			}

			// Unsubscribe (simulate RunSSE disconnect).
			if remaining := broker.Unsubscribe(rid, sub1); remaining != 0 {
				t.Fatalf("remaining = %d, want 0", remaining)
			}

			// RemoveIfIdle must never delete a terminal stream.
			if broker.RemoveIfIdle(rid) {
				t.Fatal("RemoveIfIdle must not delete a terminal stream in retention")
			}

			// Re-subscribe and assert full terminal replay.
			sub2, _, cursor2, err := broker.Subscribe(rid)
			if err != nil {
				t.Fatalf("re-Subscribe: %v (stream deleted prematurely)", err)
			}
			events2, err := broker.ReadFromCursor(rid, cursor2)
			if err != nil {
				t.Fatalf("ReadFromCursor after re-subscribe: %v", err)
			}
			if len(events2) != tc.wantEventCount {
				t.Fatalf("replay = %d events, want %d", len(events2), tc.wantEventCount)
			}
			if !events2[len(events2)-1].End {
				t.Fatal("replayed last event should be terminal")
			}
			if tc.wantTerminalCode != "" && events2[len(events2)-1].TerminalErrorCode != tc.wantTerminalCode {
				t.Fatalf("replayed terminal code = %q, want %q",
					events2[len(events2)-1].TerminalErrorCode, tc.wantTerminalCode)
			}

			_ = sub2
		})
	}
}

func TestTerminalStreamRetentionDeletion(t *testing.T) {
	broker := NewStreamBroker()
	broker.terminalRetention = 50 * time.Millisecond

	// 1. Create and complete a stream.
	stream, err := broker.OpenStream("request-retain", "conversation-retain", 1, "model", "model", 1, "hello")
	if err != nil || stream == nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	subID, _, _, err := broker.Subscribe("request-retain")
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if err := broker.Complete("request-retain", "", "done"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// 2. Unsubscribe — starts the retention timer.
	remaining := broker.Unsubscribe("request-retain", subID)
	if remaining != 0 {
		t.Fatalf("remaining = %d, want 0", remaining)
	}

	// 3. Stream should still exist immediately after unsubscribe.
	if _, ok := broker.Get("request-retain"); !ok {
		t.Fatal("stream was deleted immediately after unsubscribe (should be retained)")
	}

	// 4. Wait for retention period + buffer, then stream should be cleaned up.
	time.Sleep(150 * time.Millisecond)
	if _, ok := broker.Get("request-retain"); ok {
		t.Fatal("stream still exists after retention period (should be cleaned up)")
	}
}

// TestPlaceholderStreamLifecycle covers placeholder creation, detection,
// and cleanup in one test, consolidating the previously separate
// placeholder-cleanup and placeholder-detection assertions.
func TestPlaceholderStreamLifecycle(t *testing.T) {
	broker := NewStreamBroker()

	// 1. Subscribing to a non-existent request creates a placeholder stream
	//    with minimal identity (no conversation, no provider, no backlog).
	subID, _, _, err := broker.Subscribe("request-ph")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	stream, ok := broker.Get("request-ph")
	if !ok || stream == nil {
		t.Fatal("placeholder stream should exist after Subscribe")
	}

	// 2. Verify placeholder shape: empty conversation, no provider, no backlog,
	//    no pending execs or interactions — a true placeholder.
	stream.mu.Lock()
	isPlaceholder := strings.TrimSpace(stream.ConversationID) == "" &&
		!stream.ProviderActive &&
		len(stream.PendingExecs) == 0 &&
		len(stream.PendingInteractions) == 0 &&
		len(stream.Backlog) == 0
	stream.mu.Unlock()
	if !isPlaceholder {
		t.Fatal("subscribe-created stream should be detected as placeholder")
	}

	// 3. Unsubscribe — the last (and only) subscriber leaves.
	remaining := broker.Unsubscribe("request-ph", subID)
	if remaining != 0 {
		t.Fatalf("remaining = %d, want 0", remaining)
	}

	// 4. RemoveIfIdle (simulating the RunSSE defer path after
	//    scheduleOrphanCancelActor returns false) must clean up an empty
	//    placeholder with no subscribers.
	if !broker.RemoveIfIdle("request-ph") {
		t.Fatal("RemoveIfIdle should delete an empty placeholder stream")
	}
	if _, ok := broker.Get("request-ph"); ok {
		t.Fatal("placeholder should be deleted after RemoveIfIdle")
	}
}

func TestTerminalCompleteWithZeroSubscribersStartsTimer(t *testing.T) {
	broker := NewStreamBroker()
	broker.terminalRetention = 50 * time.Millisecond

	// Complete a stream that has never had subscribers.
	stream, err := broker.OpenStream("request-no-sub", "conversation-no-sub", 1, "model", "model", 1, "hello")
	if err != nil || stream == nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	if err := broker.Complete("request-no-sub", "", "done"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Stream should exist (timer is running).
	if _, ok := broker.Get("request-no-sub"); !ok {
		t.Fatal("stream should exist while retention timer runs")
	}

	// Subscribe should stop the timer and allow replay.
	subID, _, cursor, err := broker.Subscribe("request-no-sub")
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	events, err := broker.ReadFromCursor("request-no-sub", cursor)
	if err != nil {
		t.Fatalf("ReadFromCursor() error = %v", err)
	}
	if len(events) != 1 || !events[0].End {
		t.Fatalf("replay = %d events, want 1 terminal event", len(events))
	}

	// Unsubscribe again — timer restarts.
	broker.Unsubscribe("request-no-sub", subID)
	if _, ok := broker.Get("request-no-sub"); !ok {
		t.Fatal("stream should be retained after re-unsubscribe")
	}

	// Wait for retention.
	time.Sleep(150 * time.Millisecond)
	if _, ok := broker.Get("request-no-sub"); ok {
		t.Fatal("stream should be cleaned up after retention")
	}
}

func TestTerminalStreamSubscribeStopsCleanupTimer(t *testing.T) {
	broker := NewStreamBroker()
	broker.terminalRetention = 50 * time.Millisecond

	// Complete with zero subscribers starts the timer.
	_, err := broker.OpenStream("request-timer", "conversation-timer", 1, "model", "model", 1, "")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	if err := broker.Complete("request-timer", "", "done"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Subscribe (stops timer), wait past retention, stream should still exist.
	subID, _, _, err := broker.Subscribe("request-timer")
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, ok := broker.Get("request-timer"); !ok {
		t.Fatal("stream should still exist after subscribe stopped the timer")
	}

	// The subscriber is still subscribed; even RemoveIfIdle should not delete it.
	if broker.RemoveIfIdle("request-timer") {
		t.Fatal("RemoveIfIdle should not delete a terminal stream with active subscribers")
	}

	_ = subID
}

func TestRemoveIfIdlePreservesTerminalStream(t *testing.T) {
	broker := NewStreamBroker()

	// Create a completed stream with no subscribers.
	_, err := broker.OpenStream("request-idle", "conversation-idle", 1, "model", "model", 1, "")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	if err := broker.Complete("request-idle", "", "done"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// RemoveIfIdle must NOT delete a terminal stream.
	if broker.RemoveIfIdle("request-idle") {
		t.Fatal("RemoveIfIdle should NOT delete a terminal stream")
	}
	if _, ok := broker.Get("request-idle"); !ok {
		t.Fatal("terminal stream should survive RemoveIfIdle")
	}
}

func TestActiveStreamIsNotDeletedByRemoveIfIdle(t *testing.T) {
	broker := NewStreamBroker()

	stream, err := broker.OpenStream("request-active", "conversation-active", 1, "model", "model", 1, "hello")
	if err != nil || stream == nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	// Mark as actively streaming.
	stream.mu.Lock()
	stream.Status = StreamStatusStreaming
	stream.ProviderActive = true
	stream.mu.Unlock()

	if broker.RemoveIfIdle("request-active") {
		t.Fatal("RemoveIfIdle should not delete an active streaming stream")
	}

	// Even with 0 subscribers, an active non-terminal stream is preserved.
	_ = stream
}

func TestSubscriberJoinDuringRetentionExtendsLife(t *testing.T) {
	broker := NewStreamBroker()
	broker.terminalRetention = 100 * time.Millisecond

	_, err := broker.OpenStream("request-extend", "conversation-extend", 1, "model", "model", 1, "")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	if err := broker.Complete("request-extend", "", "done"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Sub 1: subscribe and unsubscribe (timer starts).
	subID1, _, _, _ := broker.Subscribe("request-extend")
	broker.Unsubscribe("request-extend", subID1)

	// Wait a bit, but not full retention.
	time.Sleep(50 * time.Millisecond)

	// Sub 2: join again, this stops the old timer.
	subID2, _, _, err := broker.Subscribe("request-extend")
	if err != nil {
		t.Fatalf("mid-retention Subscribe() error = %v", err)
	}

	// Wait past original retention; stream should survive because timer was reset.
	time.Sleep(150 * time.Millisecond)
	if _, ok := broker.Get("request-extend"); !ok {
		t.Fatal("stream should survive because subscribe stopped the timer")
	}

	// Unsubscribe again — timer restarts.
	broker.Unsubscribe("request-extend", subID2)

	// Now wait and it should be cleaned.
	time.Sleep(200 * time.Millisecond)
	if _, ok := broker.Get("request-extend"); ok {
		t.Fatal("stream should be cleaned up after final retention expiry")
	}
}

func TestNonTerminalStreamWithBacklogNotCleanedPrematurely(t *testing.T) {
	broker := NewStreamBroker()

	stream, err := broker.OpenStream("request-backlog", "conversation-backlog", 1, "model", "model", 1, "hello")
	if err != nil || stream == nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	// Active non-terminal stream with backlog.
	stream.mu.Lock()
	stream.Status = StreamStatusStreaming
	stream.Backlog = append(stream.Backlog, StreamEvent{Message: buildHeartbeatMessage()})
	stream.mu.Unlock()

	if broker.RemoveIfIdle("request-backlog") {
		t.Fatal("RemoveIfIdle should not delete non-terminal stream with backlog")
	}
}

func TestMultipleSubscribersAndTerminalRetention(t *testing.T) {
	broker := NewStreamBroker()
	broker.terminalRetention = 500 * time.Millisecond

	_, err := broker.OpenStream("request-multi", "conversation-multi", 1, "model", "model", 1, "")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}

	sub1, _, _, _ := broker.Subscribe("request-multi")
	sub2, _, _, _ := broker.Subscribe("request-multi")

	if err := broker.Complete("request-multi", "", "done"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Unsubscribe sub1 — one remains, stream should persist.
	remaining := broker.Unsubscribe("request-multi", sub1)
	if remaining != 1 {
		t.Fatalf("remaining after first unsub = %d, want 1", remaining)
	}
	if _, ok := broker.Get("request-multi"); !ok {
		t.Fatal("stream should persist with one remaining subscriber")
	}

	// Unsubscribe sub2 — last one, timer starts.
	remaining = broker.Unsubscribe("request-multi", sub2)
	if remaining != 0 {
		t.Fatalf("remaining after last unsub = %d, want 0", remaining)
	}
	if _, ok := broker.Get("request-multi"); !ok {
		t.Fatal("stream should be retained after last unsubscribe")
	}
}

// TestTerminalCleanupDoesNotDeleteStreamWithConcurrentSubscribe is a deterministic
// race regression that uses a test-only barrier (cleanupIntercept) to pause the
// cleanup callback after validation but before broker.mu deletion, injects a
// concurrent Subscribe, and verifies no subscriber is orphaned.
//
// Before the fix (RED): cleanup's same-pointer delete removes the stream even
// after Subscribe registers a subscriber in the gap between stream.mu release
// and broker.mu acquisition — the subscriber is orphaned.
//
// After the fix (GREEN): cleanup holds broker.mu across validation+deletion;
// Subscribe either blocks until deletion completes (then creates a fresh
// placeholder) or wins the broker.mu race and causes cleanup to abort because
// subscriber count > 0.
func TestTerminalCleanupDoesNotDeleteStreamWithConcurrentSubscribe(t *testing.T) {
	broker := NewStreamBroker()
	broker.terminalRetention = 1 * time.Millisecond

	cleanupAtBarrier := make(chan struct{})
	releaseCleanup := make(chan struct{})

	broker.cleanupIntercept = func(requestID string) {
		close(cleanupAtBarrier)
		<-releaseCleanup
	}

	_, err := broker.OpenStream("req-race", "conv-race", 1, "model", "model", 1, "hello")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := broker.Complete("req-race", "", "done"); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	waitFor(t, cleanupAtBarrier, 5*time.Second)

	subDone := make(chan struct{})
	var subID string
	var subErr error
	go func() {
		subID, _, _, subErr = broker.Subscribe("req-race")
		close(subDone)
	}()

	// In the buggy code Subscribe completes quickly (gap available).
	// In the fixed code Subscribe blocks on broker.mu (held by cleanup).
	select {
	case <-subDone:
		// Buggy path: Subscribe completed during the gap.
	case <-time.After(2 * time.Second):
		// Fixed-code path: Subscribe is blocked.
	}

	// Release cleanup so it can finish deleting (and unblock Subscribe if blocked).
	close(releaseCleanup)

	// Wait for Subscribe to finish if it hasn't already.
	<-subDone
	if subErr != nil {
		t.Fatalf("concurrent Subscribe failed: %v", subErr)
	}

	// Allow cleanup to finish.
	time.Sleep(50 * time.Millisecond)

	stream, ok := broker.Get("req-race")
	if !ok || stream == nil {
		t.Fatal("BUG: stream was deleted despite concurrent Subscribe — subscriber is orphaned")
	}

	stream.mu.Lock()
	_, subExists := stream.Subscribers[subID]
	stream.mu.Unlock()
	if !subExists {
		t.Fatal("subscriber not found on stream after race")
	}
}
