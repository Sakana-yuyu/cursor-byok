package forwarder

import (
	"testing"
	"time"

	"cursor/gen/agentv1"
)

func TestConversationActivityClearedOnStreamRemoval(t *testing.T) {
	broker := NewStreamBroker()
	service := &Service{
		broker:                   broker,
		runQueue:                 newRunQueue(),
		conversationLastActivity: make(map[string]time.Time),
	}
	service.registerStreamLifecycleHooks()

	conversationID := "conv-activity-cleanup"
	requestID := "req-activity-cleanup"
	service.markConversationActivity(conversationID)

	stream, err := broker.OpenStream(requestID, conversationID, 1, "model-a", "model-a", agentv1.AgentMode_AGENT_MODE_AGENT, "hello")
	if err != nil {
		t.Fatal(err)
	}
	stream.mu.Lock()
	stream.Status = StreamStatusCompleted
	stream.mu.Unlock()

	broker.mu.Lock()
	delete(broker.streams, requestID)
	broker.mu.Unlock()
	service.handleStreamRemoved(StreamRemovedInfo{
		RequestID:      requestID,
		ConversationID: conversationID,
	})

	service.conversationActivityMu.Lock()
	_, stillPresent := service.conversationLastActivity[conversationID]
	service.conversationActivityMu.Unlock()
	if stillPresent {
		t.Fatal("conversationLastActivity entry survived stream removal")
	}
}

func TestProvider400RecoveryPurgedOnStreamRemoval(t *testing.T) {
	service := &Service{
		provider400RecoveryTurns: make(map[string]struct{}),
	}
	requestID := "req-recovery-purge"
	service.claimProvider400Recovery(requestID, 1)
	service.claimProvider400Recovery(requestID, 2)

	service.purgeProvider400RecoveryForRequest(requestID)

	service.provider400RecoveryMu.Lock()
	defer service.provider400RecoveryMu.Unlock()
	if len(service.provider400RecoveryTurns) != 0 {
		t.Fatalf("provider400RecoveryTurns = %v, want empty after purge", service.provider400RecoveryTurns)
	}
}

func TestProvider400RecoveryMaxEntriesEvictsOldestClaims(t *testing.T) {
	service := &Service{
		provider400RecoveryTurns: make(map[string]struct{}),
	}
	for index := 0; index < provider400RecoveryMaxEntries+4; index++ {
		if !service.claimProvider400Recovery("req-cap", int64(index)) {
			t.Fatalf("claimProvider400Recovery(%d) = false, want true", index)
		}
	}
	service.provider400RecoveryMu.Lock()
	size := len(service.provider400RecoveryTurns)
	service.provider400RecoveryMu.Unlock()
	if size > provider400RecoveryMaxEntries {
		t.Fatalf("provider400RecoveryTurns size = %d, want <= %d", size, provider400RecoveryMaxEntries)
	}
}

func TestRunQueueReleasesOwnerWhenStreamRemoved(t *testing.T) {
	broker := NewStreamBroker()
	service := &Service{broker: broker, runQueue: newRunQueue()}
	service.registerStreamLifecycleHooks()

	conversationID := "conv-run-queue-release"
	ownerRequestID := "req-owner-release"
	queuedRequestID := "req-queued-release"
	if result, _, _ := service.runQueue.Submit(testRunIntent(conversationID, ownerRequestID)); result != runQueueStart {
		t.Fatalf("owner submit = %q", result)
	}
	if result, _, _ := service.runQueue.Submit(testRunIntent(conversationID, queuedRequestID)); result != runQueueQueued {
		t.Fatalf("queued submit = %q", result)
	}

	service.handleStreamRemoved(StreamRemovedInfo{
		RequestID:      ownerRequestID,
		ConversationID: conversationID,
	})

	if service.runQueue.IsOwner(conversationID, ownerRequestID) {
		t.Fatal("removed owner still owns the conversation")
	}
	if !service.runQueue.IsOwner(conversationID, queuedRequestID) {
		t.Fatalf("queued request was not promoted, owner=%q", service.runQueue.Owner(conversationID))
	}
}

func TestRunQueueReconcilesStaleOwnerOnSubmit(t *testing.T) {
	broker := NewStreamBroker()
	service := &Service{broker: broker, runQueue: newRunQueue()}
	service.registerStreamLifecycleHooks()

	conversationID := "conv-run-queue-reconcile"
	staleOwner := "req-stale-owner"
	queuedRequestID := "req-queued"
	nextRequestID := "req-next-after-reconcile"
	if result, _, _ := service.runQueue.Submit(testRunIntent(conversationID, staleOwner)); result != runQueueStart {
		t.Fatalf("stale owner submit = %q", result)
	}
	if result, _, _ := service.runQueue.Submit(testRunIntent(conversationID, queuedRequestID)); result != runQueueQueued {
		t.Fatalf("queued submit = %q", result)
	}

	service.reconcileStaleRunQueueOwner(conversationID)
	if service.runQueue.IsOwner(conversationID, staleOwner) {
		t.Fatal("stale owner still owns the conversation after reconcile")
	}
	if owner := service.runQueue.Owner(conversationID); owner != queuedRequestID {
		t.Fatalf("owner after reconcile = %q, want %q", owner, queuedRequestID)
	}

	result, _, _ := service.runQueue.Submit(testRunIntent(conversationID, nextRequestID))
	if result != runQueueQueued {
		t.Fatalf("submit after reconcile = %q, want queued", result)
	}
}

func TestDispatchInboundIntentReconcilesStaleRunQueueOwner(t *testing.T) {
	broker := NewStreamBroker()
	service := &Service{broker: broker, runQueue: newRunQueue()}
	service.registerStreamLifecycleHooks()
	service.startOwnedRunHook = func(InboundIntent) error { return nil }

	conversationID := "conv-dispatch-reconcile"
	staleOwner := "req-stale-owner"
	nextRequestID := "req-next"
	if result, _, _ := service.runQueue.Submit(testRunIntent(conversationID, staleOwner)); result != runQueueStart {
		t.Fatalf("stale owner submit = %q", result)
	}

	if err := service.dispatchInboundIntent(testRunIntent(conversationID, nextRequestID)); err != nil {
		t.Fatalf("dispatchInboundIntent() error = %v", err)
	}
	if service.runQueue.IsOwner(conversationID, staleOwner) {
		t.Fatal("stale owner still owns the conversation after dispatch reconcile")
	}
	if !service.runQueue.IsOwner(conversationID, nextRequestID) {
		t.Fatalf("owner after dispatch = %q, want %q", service.runQueue.Owner(conversationID), nextRequestID)
	}
}

func TestBrokerTerminalCleanupInvokesStreamRemovedHook(t *testing.T) {
	broker := NewStreamBroker()
	broker.terminalRetention = time.Millisecond
	service := &Service{
		broker:                   broker,
		runQueue:                 newRunQueue(),
		conversationLastActivity: make(map[string]time.Time),
	}
	service.registerStreamLifecycleHooks()

	conversationID := "conv-broker-hook"
	requestID := "req-broker-hook"
	service.markConversationActivity(conversationID)
	stream, err := broker.OpenStream(requestID, conversationID, 1, "model-a", "model-a", agentv1.AgentMode_AGENT_MODE_AGENT, "hello")
	if err != nil {
		t.Fatal(err)
	}
	stream.mu.Lock()
	stream.Status = StreamStatusCompleted
	stream.mu.Unlock()
	if !broker.scheduleTerminalCleanup(requestID) {
		t.Fatal("scheduleTerminalCleanup returned false")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := broker.Get(requestID); !ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, ok := broker.Get(requestID); ok {
		t.Fatal("terminal stream was not removed by cleanup timer")
	}

	service.conversationActivityMu.Lock()
	_, stillPresent := service.conversationLastActivity[conversationID]
	service.conversationActivityMu.Unlock()
	if stillPresent {
		t.Fatal("stream removed hook did not clear conversation activity")
	}
}
