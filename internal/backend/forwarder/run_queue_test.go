package forwarder

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"cursor/gen/agentv1"
)

func testRunIntent(conversationID string, requestID string) InboundIntent {
	return InboundIntent{
		Kind:           "run",
		ConversationID: conversationID,
		RequestID:      requestID,
		Mode:           agentv1.AgentMode_AGENT_MODE_AGENT,
		Prewarm:        true,
	}
}

func TestRunQueueSerializesOneConversationFIFO(t *testing.T) {
	queue := newRunQueue()
	first := testRunIntent("conversation-a", "request-1")
	second := testRunIntent("conversation-a", "request-2")
	third := testRunIntent("conversation-a", "request-3")

	result, _, _ := queue.Submit(first)
	if result != runQueueStart {
		t.Fatalf("first submit = %q", result)
	}
	result, owner, position := queue.Submit(second)
	if result != runQueueQueued || owner != "request-1" || position != 1 {
		t.Fatalf("second submit = %q owner=%q position=%d", result, owner, position)
	}
	result, _, position = queue.Submit(third)
	if result != runQueueQueued || position != 2 {
		t.Fatalf("third position = %d", position)
	}

	next, ok := queue.Finish("conversation-a", "request-1")
	if !ok || next.RequestID != "request-2" {
		t.Fatalf("first finish next = %#v ok=%t", next, ok)
	}
	next, ok = queue.Finish("conversation-a", "request-2")
	if !ok || next.RequestID != "request-3" {
		t.Fatalf("second finish next = %#v ok=%t", next, ok)
	}
	if next, ok = queue.Finish("conversation-a", "request-3"); ok || next.RequestID != "" {
		t.Fatalf("last finish next = %#v ok=%t", next, ok)
	}
}

func TestRunQueueAllowsDifferentConversationsToOwnConcurrently(t *testing.T) {
	queue := newRunQueue()
	for _, intent := range []InboundIntent{
		testRunIntent("conversation-a", "request-a"),
		testRunIntent("conversation-b", "request-b"),
	} {
		if result, _, _ := queue.Submit(intent); result != runQueueStart {
			t.Fatalf("submit %s = %q", intent.RequestID, result)
		}
	}
	if queue.Owner("conversation-a") != "request-a" || queue.Owner("conversation-b") != "request-b" {
		t.Fatalf("owners = %q, %q", queue.Owner("conversation-a"), queue.Owner("conversation-b"))
	}
}

func TestRunQueueConcurrentSubmitElectsOneOwner(t *testing.T) {
	const submissions = 32

	queue := newRunQueue()
	start := make(chan struct{})
	ready := make(chan struct{}, submissions)
	results := make(chan struct {
		requestID      string
		result         runQueueSubmitResult
		ownerRequestID string
		position       int
	}, submissions)

	var workers sync.WaitGroup
	workers.Add(submissions)
	for i := 0; i < submissions; i++ {
		requestID := fmt.Sprintf("request-%02d", i)
		go func(requestID string) {
			defer workers.Done()
			ready <- struct{}{}
			<-start
			result, owner, position := queue.Submit(testRunIntent("conversation-a", requestID))
			results <- struct {
				requestID      string
				result         runQueueSubmitResult
				ownerRequestID string
				position       int
			}{requestID, result, owner, position}
		}(requestID)
	}

	for i := 0; i < submissions; i++ {
		<-ready
	}
	close(start)
	workers.Wait()
	close(results)

	startCount := 0
	queuedCount := 0
	ownerRequestID := ""
	seen := make(map[string]struct{}, submissions)
	for submission := range results {
		if _, ok := seen[submission.requestID]; ok {
			t.Fatalf("request %q submitted more than once", submission.requestID)
		}
		seen[submission.requestID] = struct{}{}
		switch submission.result {
		case runQueueStart:
			startCount++
			if submission.ownerRequestID != "" || submission.position != 0 {
				t.Fatalf("start submission = owner=%q position=%d", submission.ownerRequestID, submission.position)
			}
			ownerRequestID = submission.requestID
		case runQueueQueued:
			queuedCount++
			if submission.ownerRequestID == "" || submission.position < 1 || submission.position >= submissions {
				t.Fatalf("queued submission = owner=%q position=%d", submission.ownerRequestID, submission.position)
			}
		default:
			t.Fatalf("submission %q result = %q", submission.requestID, submission.result)
		}
	}
	if startCount != 1 || queuedCount != submissions-1 {
		t.Fatalf("submit results = start=%d queued=%d", startCount, queuedCount)
	}
	if ownerRequestID == "" || queue.Owner("conversation-a") != ownerRequestID || queue.Len("conversation-a") != submissions-1 {
		t.Fatalf("owner=%q queue_owner=%q queue_len=%d", ownerRequestID, queue.Owner("conversation-a"), queue.Len("conversation-a"))
	}

	observed := make(map[string]struct{}, submissions)
	currentRequestID := ownerRequestID
	for {
		if _, ok := observed[currentRequestID]; ok {
			t.Fatalf("request %q observed more than once while finishing", currentRequestID)
		}
		observed[currentRequestID] = struct{}{}

		next, ok := queue.Finish("conversation-a", currentRequestID)
		if !ok {
			if next.RequestID != "" {
				t.Fatalf("final finish next = %#v", next)
			}
			break
		}
		if next.RequestID == "" {
			t.Fatal("finish promoted an empty request ID")
		}
		currentRequestID = next.RequestID
	}
	if len(observed) != submissions {
		t.Fatalf("finished requests = %d, want %d", len(observed), submissions)
	}
	for requestID := range seen {
		if _, ok := observed[requestID]; !ok {
			t.Fatalf("submitted request %q was not observed while finishing", requestID)
		}
	}
}

func TestRunQueueDuplicateOwnerOrPendingDoesNotEnqueueTwice(t *testing.T) {
	queue := newRunQueue()

	if result, _, _ := queue.Submit(testRunIntent(" conversation-a ", " request-1 ")); result != runQueueStart {
		t.Fatalf("owner submit = %q", result)
	}
	if result, owner, position := queue.Submit(testRunIntent("conversation-a", "request-1")); result != runQueueDuplicate || owner != "request-1" || position != 0 {
		t.Fatalf("owner duplicate = %q owner=%q position=%d", result, owner, position)
	}
	if result, _, position := queue.Submit(testRunIntent("conversation-a", "request-2")); result != runQueueQueued || position != 1 {
		t.Fatalf("pending submit = %q position=%d", result, position)
	}
	if result, owner, position := queue.Submit(testRunIntent(" conversation-a ", " request-2 ")); result != runQueueDuplicate || owner != "request-1" || position != 0 {
		t.Fatalf("pending duplicate = %q owner=%q position=%d", result, owner, position)
	}
	if queue.Len("conversation-a") != 1 {
		t.Fatalf("queue length after duplicates = %d", queue.Len("conversation-a"))
	}
}

func TestRunQueueCancelQueuedRemovesOnlyTarget(t *testing.T) {
	queue := newRunQueue()
	if result, _, _ := queue.Submit(testRunIntent("conversation-a", "request-1")); result != runQueueStart {
		t.Fatalf("owner submit = %q", result)
	}
	for _, requestID := range []string{"request-2", "request-3", "request-4"} {
		if result, _, _ := queue.Submit(testRunIntent("conversation-a", requestID)); result != runQueueQueued {
			t.Fatalf("submit %s = %q", requestID, result)
		}
	}

	if _, ok := queue.CancelQueued("conversation-a", "request-1"); ok {
		t.Fatal("CancelQueued removed the owner")
	}
	if queue.Owner("conversation-a") != "request-1" || queue.Len("conversation-a") != 3 {
		t.Fatalf("owner cancellation changed state owner=%q queue_len=%d", queue.Owner("conversation-a"), queue.Len("conversation-a"))
	}

	canceled, ok := queue.CancelQueued(" conversation-a ", " request-3 ")
	if !ok {
		t.Fatal("CancelQueued did not remove target")
	}
	if canceled.Intent.ConversationID != "conversation-a" || canceled.Intent.RequestID != "request-3" || canceled.Position != 2 {
		t.Fatalf("canceled = %#v", canceled)
	}
	if queue.Owner("conversation-a") != "request-1" || queue.Len("conversation-a") != 2 {
		t.Fatalf("owner=%q queue_len=%d", queue.Owner("conversation-a"), queue.Len("conversation-a"))
	}
	if _, ok := queue.CancelQueued("conversation-a", "request-3"); ok {
		t.Fatal("CancelQueued removed target twice")
	}

	next, ok := queue.Finish("conversation-a", "request-1")
	if !ok || next.RequestID != "request-2" {
		t.Fatalf("finish owner next = %#v ok=%t", next, ok)
	}
	next, ok = queue.Finish("conversation-a", "request-2")
	if !ok || next.RequestID != "request-4" {
		t.Fatalf("finish request-2 next = %#v ok=%t", next, ok)
	}
}

func TestRunQueueFinishIsIdempotentAndRejectsWrongOwner(t *testing.T) {
	queue := newRunQueue()
	if result, _, _ := queue.Submit(testRunIntent("conversation-a", "request-1")); result != runQueueStart {
		t.Fatalf("owner submit = %q", result)
	}
	if result, _, _ := queue.Submit(testRunIntent("conversation-a", "request-2")); result != runQueueQueued {
		t.Fatalf("pending submit = %q", result)
	}

	if next, ok := queue.Finish("conversation-a", "wrong-owner"); ok || next.RequestID != "" {
		t.Fatalf("wrong finish = %#v ok=%t", next, ok)
	}
	if queue.Owner("conversation-a") != "request-1" || queue.Len("conversation-a") != 1 {
		t.Fatalf("wrong finish changed state owner=%q queue_len=%d", queue.Owner("conversation-a"), queue.Len("conversation-a"))
	}

	next, ok := queue.Finish(" conversation-a ", " request-1 ")
	if !ok || next.RequestID != "request-2" || queue.Owner("conversation-a") != "request-2" {
		t.Fatalf("matching finish = %#v ok=%t owner=%q", next, ok, queue.Owner("conversation-a"))
	}
	if next, ok = queue.Finish("conversation-a", "request-1"); ok || next.RequestID != "" {
		t.Fatalf("stale finish = %#v ok=%t", next, ok)
	}
	if next, ok = queue.Finish("conversation-a", "request-2"); ok || next.RequestID != "" {
		t.Fatalf("final finish = %#v ok=%t", next, ok)
	}
	if queue.Owner("conversation-a") != "" || queue.Len("conversation-a") != 0 {
		t.Fatalf("state after final finish owner=%q queue_len=%d", queue.Owner("conversation-a"), queue.Len("conversation-a"))
	}
}

func TestDispatchInboundIntentQueuesBeforeOpeningSecondConversationStream(t *testing.T) {
	service := &Service{broker: NewStreamBroker(), runQueue: newRunQueue()}
	first := testRunIntent("conversation-a", "request-1")
	second := testRunIntent("conversation-a", "request-2")

	if err := service.dispatchInboundIntent(first); err != nil {
		t.Fatal(err)
	}
	if err := service.dispatchInboundIntent(second); err != nil {
		t.Fatal(err)
	}

	if _, ok := service.broker.Get("request-1"); !ok {
		t.Fatal("owner stream was not opened")
	}
	if _, ok := service.broker.Get("request-2"); ok {
		t.Fatal("queued stream was opened before promotion")
	}
	if got := service.runQueue.Len("conversation-a"); got != 1 {
		t.Fatalf("queue len = %d", got)
	}
}

func TestDispatchInboundIntentDoesNotPersistQueuedRun(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	if _, err := store.CreateConversation("conversation-a", agentv1.AgentMode_AGENT_MODE_AGENT, "", "", ""); err != nil {
		t.Fatal(err)
	}
	service := &Service{broker: NewStreamBroker(), runQueue: newRunQueue(), store: store}
	if result, _, _ := service.runQueue.Submit(testRunIntent("conversation-a", "request-1")); result != runQueueStart {
		t.Fatalf("owner submit = %q", result)
	}

	if err := service.dispatchInboundIntent(testRunIntent("conversation-a", "request-2")); err != nil {
		t.Fatal(err)
	}

	conversation, err := store.LoadConversation("conversation-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range conversation.Entries {
		if entry.RequestID == "request-2" {
			t.Fatalf("queued request persisted context item: %#v", entry)
		}
	}
	if conversation.CurrentRequestID == "request-2" {
		t.Fatal("queued request mutated conversation state")
	}
}

func TestDispatchInboundIntentRunsDifferentConversationsConcurrently(t *testing.T) {
	service := &Service{broker: NewStreamBroker(), runQueue: newRunQueue()}
	for _, intent := range []InboundIntent{
		testRunIntent("conversation-a", "request-a"),
		testRunIntent("conversation-b", "request-b"),
	} {
		if err := service.dispatchInboundIntent(intent); err != nil {
			t.Fatal(err)
		}
	}

	if _, ok := service.broker.Get("request-a"); !ok {
		t.Fatal("conversation-a stream was not opened")
	}
	if _, ok := service.broker.Get("request-b"); !ok {
		t.Fatal("conversation-b stream was not opened")
	}
	if !service.runQueue.IsOwner("conversation-a", "request-a") || !service.runQueue.IsOwner("conversation-b", "request-b") {
		t.Fatalf("owners = %q, %q", service.runQueue.Owner("conversation-a"), service.runQueue.Owner("conversation-b"))
	}
}

func TestDispatchInboundIntentDuplicateRequestDoesNotQueueAgain(t *testing.T) {
	service := &Service{broker: NewStreamBroker(), runQueue: newRunQueue()}
	owner := testRunIntent("conversation-a", "request-1")
	queued := testRunIntent("conversation-a", "request-2")

	for _, intent := range []InboundIntent{owner, owner, queued, queued} {
		if err := service.dispatchInboundIntent(intent); err != nil {
			t.Fatal(err)
		}
	}

	if got := service.runQueue.Len("conversation-a"); got != 1 {
		t.Fatalf("queue len after duplicates = %d", got)
	}
	if _, ok := service.broker.Get("request-2"); ok {
		t.Fatal("duplicate queued request opened a stream")
	}
}

func TestDrainRunQueueStaleFinalizationDoesNotReleaseSuccessor(t *testing.T) {
	service := &Service{broker: NewStreamBroker(), runQueue: newRunQueue()}
	for _, requestID := range []string{"request-1", "request-2", "request-3"} {
		result, _, _ := service.runQueue.Submit(testRunIntent("conversation-a", requestID))
		if requestID == "request-1" && result != runQueueStart {
			t.Fatalf("owner submit = %q", result)
		}
		if requestID != "request-1" && result != runQueueQueued {
			t.Fatalf("queued submit %s = %q", requestID, result)
		}
	}

	service.drainRunQueue("conversation-a", "request-1")
	waitForRunQueueOwner(t, service.runQueue, "conversation-a", "request-2")
	service.drainRunQueue("conversation-a", "request-1")

	if owner := service.runQueue.Owner("conversation-a"); owner != "request-2" {
		t.Fatalf("owner after stale finalization = %q", owner)
	}
	if got := service.runQueue.Len("conversation-a"); got != 1 {
		t.Fatalf("queue len after stale finalization = %d", got)
	}
	if _, ok := service.broker.Get("request-3"); ok {
		t.Fatal("stale finalization promoted request-3")
	}
}

func TestDispatchInboundIntentStartupFailureTerminalizesStreamBeforePromotion(t *testing.T) {
	service := &Service{broker: NewStreamBroker(), runQueue: newRunQueue()}
	service.startOwnedRunHook = func(intent InboundIntent) error {
		_, err := service.streamForIntent(intent)
		if err != nil {
			return err
		}
		return errors.New("forced post failure")
	}
	if result, _, _ := service.runQueue.Submit(testRunIntent("conversation-a", "request-2")); result != runQueueStart {
		t.Fatalf("successor seed submit = %q", result)
	}
	service.runQueue.Finish("conversation-a", "request-2")

	first := testRunIntent("conversation-a", "request-1")
	second := testRunIntent("conversation-a", "request-2")
	if result, _, _ := service.runQueue.Submit(first); result != runQueueStart {
		t.Fatalf("owner submit = %q", result)
	}
	if result, _, _ := service.runQueue.Submit(second); result != runQueueQueued {
		t.Fatalf("successor submit = %q", result)
	}

	if err := service.startAdmittedRun(first); err == nil {
		t.Fatal("startup failure was not returned")
	}

	stream, ok := service.broker.Get("request-1")
	if !ok || stream == nil {
		t.Fatal("failed owner stream was not opened")
	}
	stream.mu.Lock()
	status := stream.Status
	stream.mu.Unlock()
	if status != StreamStatusFailed && status != StreamStatusCanceled {
		t.Fatalf("failed owner stream status = %q", status)
	}
	waitForRunQueueOwner(t, service.runQueue, "conversation-a", "request-2")
}

func waitForRunQueueOwner(t *testing.T, queue *runQueue, conversationID string, requestID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if queue.IsOwner(conversationID, requestID) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("owner = %q, want %q", queue.Owner(conversationID), requestID)
}
