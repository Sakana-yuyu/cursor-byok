package forwarder

import (
	"context"
	"testing"
	"time"
)

// TestAppendSeqLostResponseRetry verifies that an idle repeated append_seqno=1
// for the same request epoch is classified as stale, not as a new epoch.
// This is the RED test: it currently fails because the implicit idle-epoch-reset
// in acquire() re-accepts seq=1 when state.next>1 and processing==false.
func TestAppendSeqLostResponseRetry(t *testing.T) {
	tracker := newAppendSequenceTracker()
	ctx := context.Background()
	requestID := "req-lost-response"

	// Step 1: Acquire seq=1 — must be accepted.
	ticket, stale, err := tracker.Acquire(ctx, requestID, 1)
	if err != nil {
		t.Fatalf("Acquire(seq=1) error = %v", err)
	}
	if stale {
		t.Fatal("Acquire(seq=1) returned stale=true, want accepted")
	}
	if ticket.state == nil {
		t.Fatal("Acquire(seq=1) returned nil state")
	}
	if ticket.seq != 1 {
		t.Fatalf("Acquire(seq=1) ticket.seq = %d, want 1", ticket.seq)
	}

	// Step 2: Release seq=1 — simulates successful processing.
	ticket.Release()

	// Step 3: Re-acquire seq=1 for the same request while idle.
	// This must be classified as stale/duplicate — NOT accepted as a new epoch.
	ticket2, stale2, err2 := tracker.Acquire(ctx, requestID, 1)
	if err2 != nil {
		t.Fatalf("Acquire(seq=1) retry error = %v", err2)
	}
	if !stale2 {
		t.Fatal("Acquire(seq=1) retry returned stale=false, want stale=true (duplicate retry must not re-execute)")
	}
	if ticket2.state != nil {
		t.Fatalf("Acquire(seq=1) retry returned non-nil state for stale ticket")
	}
}

// TestAppendSeqResetAllowsSeq1Again verifies that AppendSequenceTracker.Reset(requestID)
// explicitly allows seq=1 again after prior sequence has been processed.
func TestAppendSeqResetAllowsSeq1Again(t *testing.T) {
	tracker := newAppendSequenceTracker()
	ctx := context.Background()
	requestID := "req-reset"

	// Process seq=1 then seq=2, then reset, then seq=1 should be accepted again.
	ticket1, stale, err := tracker.Acquire(ctx, requestID, 1)
	if err != nil || stale {
		t.Fatalf("Acquire(seq=1) error=%v stale=%t, want accepted", err, stale)
	}
	ticket1.Release()

	ticket2, stale, err := tracker.Acquire(ctx, requestID, 2)
	if err != nil || stale {
		t.Fatalf("Acquire(seq=2) error=%v stale=%t, want accepted", err, stale)
	}
	ticket2.Release()

	// After processing, state.next should be 3. Reset to allow seq=1 again.
	tracker.Reset(requestID)

	ticket3, stale, err := tracker.Acquire(ctx, requestID, 1)
	if err != nil {
		t.Fatalf("Acquire(seq=1) after Reset error = %v", err)
	}
	if stale {
		t.Fatal("Acquire(seq=1) after Reset returned stale=true, want accepted")
	}
	if ticket3.state == nil {
		t.Fatal("Acquire(seq=1) after Reset returned nil state")
	}
	ticket3.Release()
}

// TestAppendSeqNewRequestIDIndependent verifies that a new request ID
// accepts seq=1 independently of other request IDs.
func TestAppendSeqNewRequestIDIndependent(t *testing.T) {
	tracker := newAppendSequenceTracker()
	ctx := context.Background()

	// Process seq=1 for req-A.
	ticketA, stale, err := tracker.Acquire(ctx, "req-A", 1)
	if err != nil || stale {
		t.Fatalf("Acquire(req-A seq=1) error=%v stale=%t, want accepted", err, stale)
	}
	ticketA.Release()

	// req-B seq=1 should be accepted independently.
	ticketB, stale, err := tracker.Acquire(ctx, "req-B", 1)
	if err != nil {
		t.Fatalf("Acquire(req-B seq=1) error = %v", err)
	}
	if stale {
		t.Fatal("Acquire(req-B seq=1) returned stale=true, want accepted (new request ID)")
	}
	if ticketB.state == nil {
		t.Fatal("Acquire(req-B seq=1) returned nil state")
	}
	ticketB.Release()
}

// TestAppendSeqInFlightDuplicateSeq1Blocks verifies that when seq=1 is
// in-flight (processing), another seq=1 blocks rather than being classified stale.
func TestAppendSeqInFlightDuplicateSeq1Blocks(t *testing.T) {
	tracker := newAppendSequenceTracker()
	requestID := "req-inflight"

	// Acquire seq=1 but do NOT release — it stays in-flight.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	ticket, stale, err := tracker.Acquire(ctx, requestID, 1)
	if err != nil || stale {
		t.Fatalf("Acquire(seq=1) error=%v stale=%t, want accepted", err, stale)
	}
	defer ticket.Release()

	// Try to acquire another seq=1 while the first is still processing (in-flight).
	// It should block, not return stale immediately.
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer shortCancel()

	_, stale2, err2 := tracker.Acquire(shortCtx, requestID, 1)
	if err2 == nil {
		t.Fatal("Acquire(seq=1) in-flight expected to block/timeout, but returned without error")
	}
	if stale2 {
		t.Fatal("Acquire(seq=1) in-flight returned stale=true, want blocking behavior")
	}
}

// TestAppendSeqOrderingSeqGreaterThanNextBlocks verifies that a seqno > next
// blocks (ordering guarantee) rather than being rejected as stale.
func TestAppendSeqOrderingSeqGreaterThanNextBlocks(t *testing.T) {
	tracker := newAppendSequenceTracker()
	requestID := "req-ordering"

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Try to acquire seq=2 before seq=1 has been seen — should block.
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer shortCancel()

	_, stale, err := tracker.Acquire(shortCtx, requestID, 2)
	if err == nil {
		t.Fatal("Acquire(seq=2) before seq=1 expected to block/timeout, but returned without error")
	}
	if stale {
		t.Fatal("Acquire(seq=2) before seq=1 returned stale=true, want blocking (ordering)")
	}

	// Now acquire seq=1 — should be accepted.
	_, stale1, err1 := tracker.Acquire(ctx, requestID, 1)
	if err1 != nil || stale1 {
		t.Fatalf("Acquire(seq=1) error=%v stale=%t, want accepted", err1, stale1)
	}
}

// TestAppendSeqNilTrackerReturnsEmpty verifies nil-safe behavior.
func TestAppendSeqNilTrackerReturnsEmpty(t *testing.T) {
	var tracker *appendSequenceTracker
	ctx := context.Background()
	ticket, stale, err := tracker.Acquire(ctx, "req", 1)
	if err != nil {
		t.Fatalf("nil tracker Acquire error = %v", err)
	}
	if stale {
		t.Fatal("nil tracker returned stale=true, want false")
	}
	if ticket.state != nil {
		t.Fatal("nil tracker returned non-nil state")
	}
}

// TestAppendSeqInvalidInputsReturnsEmpty verifies that empty request ID or
// non-positive seqno are silently accepted (bypassed).
func TestAppendSeqInvalidInputsReturnsEmpty(t *testing.T) {
	tracker := newAppendSequenceTracker()
	ctx := context.Background()

	ticket, stale, err := tracker.Acquire(ctx, "", 1)
	if err != nil || stale || ticket.state != nil {
		t.Fatal("Acquire with empty request_id should return empty ticket")
	}

	ticket, stale, err = tracker.Acquire(ctx, "req", 0)
	if err != nil || stale || ticket.state != nil {
		t.Fatal("Acquire with seq=0 should return empty ticket")
	}

	ticket, stale, err = tracker.Acquire(ctx, "req", -1)
	if err != nil || stale || ticket.state != nil {
		t.Fatal("Acquire with seq=-1 should return empty ticket")
	}
}

// TestAppendSeqLateSeq1ReturnsStaleWhenLaterSeqInFlight verifies that when
// seq=1 was already processed (next>1) and a later seqno is in-flight
// (processing=true), a late seq=1 returns stale immediately — it does NOT
// wait for the in-flight item to complete. This is a regression test for the
// removal of the implicit idle-epoch-reset block: before the removal, the old
// code would wait for the in-flight item; after removal, it returns stale.
func TestAppendSeqLateSeq1ReturnsStaleWhenLaterSeqInFlight(t *testing.T) {
	tracker := newAppendSequenceTracker()
	ctx := context.Background()
	requestID := "req-late-seq1"

	// Step 1: Acquire and release seq=1 — next advances to 2.
	ticket1, stale, err := tracker.Acquire(ctx, requestID, 1)
	if err != nil || stale {
		t.Fatalf("Acquire(seq=1) error=%v stale=%t, want accepted", err, stale)
	}
	ticket1.Release()

	// Step 2: Acquire seq=2 and keep it in-flight (processing=true).
	ticket2, stale, err := tracker.Acquire(ctx, requestID, 2)
	if err != nil || stale {
		t.Fatalf("Acquire(seq=2) error=%v stale=%t, want accepted", err, stale)
	}
	defer ticket2.Release()

	// Step 3: Acquire late seq=1 with a short context.
	// After the fix, this should return stale=true immediately.
	// Before the fix, the old code would block waiting for seq=2 to finish.
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer shortCancel()

	ticket3, stale3, err3 := tracker.Acquire(shortCtx, requestID, 1)
	if err3 != nil {
		t.Fatalf("Acquire(late seq=1) error = %v, want stale=true immediately (not timeout)", err3)
	}
	if !stale3 {
		t.Fatal("Acquire(late seq=1) returned stale=false, want stale=true (stale immediately, not waiting for in-flight)")
	}
	if ticket3.state != nil {
		t.Fatal("Acquire(late seq=1) returned non-nil state for stale ticket")
	}

	// Step 4: Release seq=2 cleanly.
	ticket2.Release()
}

// TestAppendSeqResetOnNilOrEmptyDoesNotPanic verifies Reset is nil-safe.
func TestAppendSeqResetOnNilOrEmptyDoesNotPanic(t *testing.T) {
	var tracker *appendSequenceTracker
	tracker.Reset("req") // must not panic

	tracker2 := newAppendSequenceTracker()
	tracker2.Reset("") // must not panic
}

// TestAppendSeqReleaseNilTicketDoesNotPanic verifies ticket.Release is nil-safe.
func TestAppendSeqReleaseNilTicketDoesNotPanic(t *testing.T) {
	var ticket appendSequenceTicket
	ticket.Release() // must not panic

	ticket2 := appendSequenceTicket{seq: 1} // state is nil
	ticket2.Release()                       // must not panic
}
