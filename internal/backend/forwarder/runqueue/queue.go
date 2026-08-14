// Package runqueue serializes per-conversation agent runs. It depends only on
// intent identity (conversation + request IDs), not on forwarder internals.
package runqueue

import (
	"strings"
	"sync"
)

// Intent is the minimal identity needed to queue and promote runs.
type Intent interface {
	GetConversationID() string
	GetRequestID() string
}

type SubmitResult string

const (
	Start     SubmitResult = "start"
	Queued    SubmitResult = "queued"
	Duplicate SubmitResult = "duplicate"
)

type Cancellation[T Intent] struct {
	Intent   T
	Position int
}

type conversationState[T Intent] struct {
	ownerRequestID string
	pending        []T
}

// Queue enforces one active owner per conversation with FIFO promotion.
type Queue[T Intent] struct {
	mu     sync.Mutex
	states map[string]*conversationState[T]
	closed bool
}

func New[T Intent]() *Queue[T] {
	return &Queue[T]{states: make(map[string]*conversationState[T])}
}

func (q *Queue[T]) Submit(intent T) (result SubmitResult, ownerRequestID string, queuePosition int) {
	if q == nil {
		return "", "", 0
	}
	conversationID := strings.TrimSpace(intent.GetConversationID())
	requestID := strings.TrimSpace(intent.GetRequestID())
	if conversationID == "" || requestID == "" {
		return Duplicate, "", 0
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return Duplicate, "", 0
	}
	state := q.states[conversationID]
	if state == nil {
		q.states[conversationID] = &conversationState[T]{ownerRequestID: requestID}
		return Start, "", 0
	}
	if state.ownerRequestID == requestID {
		return Duplicate, state.ownerRequestID, 0
	}
	for _, pending := range state.pending {
		if pending.GetRequestID() == requestID {
			return Duplicate, state.ownerRequestID, 0
		}
	}
	state.pending = append(state.pending, intent)
	return Queued, state.ownerRequestID, len(state.pending)
}

func (q *Queue[T]) Finish(conversationID string, requestID string) (next T, ok bool) {
	var zero T
	if q == nil {
		return zero, false
	}
	conversationID = strings.TrimSpace(conversationID)
	requestID = strings.TrimSpace(requestID)
	if conversationID == "" || requestID == "" {
		return zero, false
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	state := q.states[conversationID]
	if state == nil || state.ownerRequestID != requestID {
		return zero, false
	}
	if q.closed {
		delete(q.states, conversationID)
		return zero, false
	}
	if len(state.pending) == 0 {
		delete(q.states, conversationID)
		return zero, false
	}

	next = state.pending[0]
	var cleared T
	state.pending[0] = cleared
	state.pending = state.pending[1:]
	state.ownerRequestID = strings.TrimSpace(next.GetRequestID())
	return next, true
}

func (q *Queue[T]) CancelQueued(conversationID string, requestID string) (Cancellation[T], bool) {
	var zero Cancellation[T]
	if q == nil {
		return zero, false
	}
	conversationID = strings.TrimSpace(conversationID)
	requestID = strings.TrimSpace(requestID)
	if conversationID == "" || requestID == "" {
		return zero, false
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	state := q.states[conversationID]
	if state == nil {
		return zero, false
	}
	return removePendingLocked(state, requestID)
}

func removePendingLocked[T Intent](state *conversationState[T], requestID string) (Cancellation[T], bool) {
	var zero Cancellation[T]
	if state == nil {
		return zero, false
	}
	for index := range state.pending {
		if strings.TrimSpace(state.pending[index].GetRequestID()) != requestID {
			continue
		}
		canceled := Cancellation[T]{Intent: state.pending[index], Position: index + 1}
		var cleared T
		state.pending[index] = cleared
		copy(state.pending[index:], state.pending[index+1:])
		last := len(state.pending) - 1
		state.pending[last] = cleared
		state.pending = state.pending[:last]
		return canceled, true
	}
	return zero, false
}

func (q *Queue[T]) CancelQueuedByRequestID(requestID string) (conversationID string, canceled Cancellation[T], ok bool) {
	var zero Cancellation[T]
	if q == nil {
		return "", zero, false
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return "", zero, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	var matchConversation string
	for conversation, state := range q.states {
		if state == nil {
			continue
		}
		for index := range state.pending {
			if strings.TrimSpace(state.pending[index].GetRequestID()) != requestID {
				continue
			}
			if matchConversation != "" {
				return "", zero, false
			}
			matchConversation = conversation
			_ = index
		}
	}
	if matchConversation == "" {
		return "", zero, false
	}
	canceled, removed := removePendingLocked(q.states[matchConversation], requestID)
	if !removed {
		return "", zero, false
	}
	return matchConversation, canceled, true
}

func (q *Queue[T]) Close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	for conversationID, state := range q.states {
		var cleared T
		for index := range state.pending {
			state.pending[index] = cleared
		}
		state.pending = nil
		if state.ownerRequestID != "" {
			delete(q.states, conversationID)
		}
	}
}

func (q *Queue[T]) IsOwner(conversationID string, requestID string) bool {
	if q == nil {
		return false
	}
	conversationID = strings.TrimSpace(conversationID)
	requestID = strings.TrimSpace(requestID)
	if conversationID == "" || requestID == "" {
		return false
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	state := q.states[conversationID]
	return state != nil && state.ownerRequestID == requestID
}

func (q *Queue[T]) Owner(conversationID string) string {
	if q == nil {
		return ""
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ""
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if state := q.states[conversationID]; state != nil {
		return state.ownerRequestID
	}
	return ""
}

func (q *Queue[T]) Len(conversationID string) int {
	if q == nil {
		return 0
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return 0
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if state := q.states[conversationID]; state != nil {
		return len(state.pending)
	}
	return 0
}
