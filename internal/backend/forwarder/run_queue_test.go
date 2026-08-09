package forwarder

import (
	"fmt"
	"sync"
	"testing"
)

func testRunIntent(conversationID string, requestID string) InboundIntent {
	return InboundIntent{
		Kind:           "run",
		ConversationID: conversationID,
		RequestID:      requestID,
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
