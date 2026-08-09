package forwarder

import (
	"fmt"
	"strings"
	"sync"

	"cursor/internal/logger"
	"cursor/internal/safego"
)

type runQueueSubmitResult string

const (
	runQueueStart     runQueueSubmitResult = "start"
	runQueueQueued    runQueueSubmitResult = "queued"
	runQueueDuplicate runQueueSubmitResult = "duplicate"
)

type queuedRunCancellation struct {
	Intent   InboundIntent
	Position int
}

type conversationRunState struct {
	ownerRequestID string
	pending        []InboundIntent
}

type runQueue struct {
	mu     sync.Mutex
	states map[string]*conversationRunState
}

func newRunQueue() *runQueue {
	return &runQueue{states: make(map[string]*conversationRunState)}
}

func (q *runQueue) Submit(intent InboundIntent) (result runQueueSubmitResult, ownerRequestID string, queuePosition int) {
	if q == nil {
		return "", "", 0
	}
	conversationID := strings.TrimSpace(intent.ConversationID)
	requestID := strings.TrimSpace(intent.RequestID)
	intent.ConversationID = conversationID
	intent.RequestID = requestID
	if conversationID == "" || requestID == "" {
		return runQueueDuplicate, "", 0
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	state := q.states[conversationID]
	if state == nil {
		q.states[conversationID] = &conversationRunState{ownerRequestID: requestID}
		return runQueueStart, "", 0
	}
	if state.ownerRequestID == requestID {
		return runQueueDuplicate, state.ownerRequestID, 0
	}
	for _, pending := range state.pending {
		if pending.RequestID == requestID {
			return runQueueDuplicate, state.ownerRequestID, 0
		}
	}
	state.pending = append(state.pending, intent)
	return runQueueQueued, state.ownerRequestID, len(state.pending)
}

func (q *runQueue) Finish(conversationID string, requestID string) (next InboundIntent, ok bool) {
	if q == nil {
		return InboundIntent{}, false
	}
	conversationID = strings.TrimSpace(conversationID)
	requestID = strings.TrimSpace(requestID)
	if conversationID == "" || requestID == "" {
		return InboundIntent{}, false
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	state := q.states[conversationID]
	if state == nil || state.ownerRequestID != requestID {
		return InboundIntent{}, false
	}
	if len(state.pending) == 0 {
		delete(q.states, conversationID)
		return InboundIntent{}, false
	}

	next = state.pending[0]
	state.pending[0] = InboundIntent{}
	state.pending = state.pending[1:]
	state.ownerRequestID = strings.TrimSpace(next.RequestID)
	return next, true
}

func (q *runQueue) CancelQueued(conversationID string, requestID string) (queuedRunCancellation, bool) {
	if q == nil {
		return queuedRunCancellation{}, false
	}
	conversationID = strings.TrimSpace(conversationID)
	requestID = strings.TrimSpace(requestID)
	if conversationID == "" || requestID == "" {
		return queuedRunCancellation{}, false
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	state := q.states[conversationID]
	if state == nil {
		return queuedRunCancellation{}, false
	}
	for index := range state.pending {
		if strings.TrimSpace(state.pending[index].RequestID) != requestID {
			continue
		}
		canceled := queuedRunCancellation{Intent: state.pending[index], Position: index + 1}
		state.pending[index] = InboundIntent{}
		copy(state.pending[index:], state.pending[index+1:])
		last := len(state.pending) - 1
		state.pending[last] = InboundIntent{}
		state.pending = state.pending[:last]
		return canceled, true
	}
	return queuedRunCancellation{}, false
}

func (q *runQueue) IsOwner(conversationID string, requestID string) bool {
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

func (q *runQueue) Owner(conversationID string) string {
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

func (q *runQueue) Len(conversationID string) int {
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

func (service *Service) finishConversationTurn(conversationID string, requestID string) {
	if service == nil || service.runQueue == nil {
		return
	}
	next, ok := service.runQueue.Finish(conversationID, requestID)
	if !ok {
		return
	}
	safego.Go("forwarder:promoted-conversation-run", func() {
		service.startPromotedRun(next)
	})
}

func (service *Service) startPromotedRun(intent InboundIntent) {
	for {
		err := service.startOwnedRun(intent)
		if err == nil {
			return
		}
		logger.Errorf("forwarder promoted conversation run startup failed request_id=%s conversation_id=%s err=%v",
			strings.TrimSpace(intent.RequestID), strings.TrimSpace(intent.ConversationID), err)
		if service.broker != nil {
			if stream, ok := service.broker.Get(intent.RequestID); ok && stream != nil {
				_ = service.failStreamIfNonTerminal(stream, "unknown", fmt.Errorf("start promoted run: %w", err))
			}
		}
		// A failed-terminal path may already release ownership and launch the successor.
		// Only finish here when this startup failure still owns the conversation.
		if !service.runQueue.IsOwner(intent.ConversationID, intent.RequestID) {
			return
		}
		next, ok := service.runQueue.Finish(intent.ConversationID, intent.RequestID)
		if !ok {
			return
		}
		intent = next
	}
}

// drainRunQueue preserves the legacy call sites while conversation ownership is completed in later tasks.
func (service *Service) drainRunQueue(conversationID string) {
	if service == nil || service.runQueue == nil {
		return
	}
	ownerRequestID := service.runQueue.Owner(conversationID)
	if ownerRequestID == "" {
		return
	}
	service.finishConversationTurn(conversationID, ownerRequestID)
}
