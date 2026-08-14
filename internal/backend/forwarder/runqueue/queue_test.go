package runqueue_test

import (
	"testing"

	"cursor/internal/backend/forwarder/runqueue"
)

type testIntent struct {
	conversationID string
	requestID      string
}

func (intent testIntent) GetConversationID() string { return intent.conversationID }
func (intent testIntent) GetRequestID() string      { return intent.requestID }

func TestQueueSerializesOneConversationFIFO(t *testing.T) {
	queue := runqueue.New[testIntent]()
	first := testIntent{conversationID: "conversation-a", requestID: "request-1"}
	second := testIntent{conversationID: "conversation-a", requestID: "request-2"}
	third := testIntent{conversationID: "conversation-a", requestID: "request-3"}

	result, _, _ := queue.Submit(first)
	if result != runqueue.Start {
		t.Fatalf("first submit = %q", result)
	}
	result, owner, position := queue.Submit(second)
	if result != runqueue.Queued || owner != "request-1" || position != 1 {
		t.Fatalf("second submit = %q owner=%q position=%d", result, owner, position)
	}
	result, _, position = queue.Submit(third)
	if result != runqueue.Queued || position != 2 {
		t.Fatalf("third position = %d", position)
	}

	next, ok := queue.Finish("conversation-a", "request-1")
	if !ok || next.requestID != "request-2" {
		t.Fatalf("first finish next = %#v ok=%t", next, ok)
	}
	next, ok = queue.Finish("conversation-a", "request-2")
	if !ok || next.requestID != "request-3" {
		t.Fatalf("second finish next = %#v ok=%t", next, ok)
	}
	if next, ok = queue.Finish("conversation-a", "request-3"); ok || next.requestID != "" {
		t.Fatalf("last finish next = %#v ok=%t", next, ok)
	}
}

func TestQueueClosePreventsPromotion(t *testing.T) {
	queue := runqueue.New[testIntent]()
	first := testIntent{conversationID: "conversation-a", requestID: "request-1"}
	second := testIntent{conversationID: "conversation-a", requestID: "request-2"}
	if result, _, _ := queue.Submit(first); result != runqueue.Start {
		t.Fatalf("first submit = %q", result)
	}
	if result, _, _ := queue.Submit(second); result != runqueue.Queued {
		t.Fatalf("second submit = %q", result)
	}
	queue.Close()
	if next, ok := queue.Finish("conversation-a", "request-1"); ok || next.requestID != "" {
		t.Fatalf("finish after close promoted next = %#v ok=%t", next, ok)
	}
}

func TestQueueCancelQueuedByRequestIDRequiresUniqueMatch(t *testing.T) {
	queue := runqueue.New[testIntent]()
	_ = testIntent{conversationID: "conversation-a", requestID: "request-1"}
	queue.Submit(testIntent{conversationID: "conversation-a", requestID: "request-1"})
	queue.Submit(testIntent{conversationID: "conversation-a", requestID: "request-2"})
	queue.Submit(testIntent{conversationID: "conversation-b", requestID: "request-b1"})
	queue.Submit(testIntent{conversationID: "conversation-b", requestID: "request-2"})

	if conversationID, _, ok := queue.CancelQueuedByRequestID("request-2"); ok {
		t.Fatalf("ambiguous cancel succeeded for conversation %q", conversationID)
	}
	if _, _, ok := queue.CancelQueuedByRequestID("request-1"); ok {
		t.Fatal("cancel removed owner")
	}
	canceled, ok := queue.CancelQueued("conversation-a", "request-2")
	if !ok || canceled.Intent.requestID != "request-2" {
		t.Fatalf("cancel queued = %#v ok=%t", canceled, ok)
	}
	owner := queue.Owner("conversation-a")
	if owner != "request-1" {
		t.Fatalf("owner = %q", owner)
	}
}
