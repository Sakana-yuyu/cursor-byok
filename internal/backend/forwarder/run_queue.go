package forwarder

import (
	"strings"
	"sync"

	"cursor/internal/logger"
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

// Enqueue preserves the pre-scheduler queue API for existing integration paths.
func (q *runQueue) Enqueue(conversationID string, intent InboundIntent) {
	if q == nil {
		return
	}
	conversationID = strings.TrimSpace(conversationID)
	intent.ConversationID = conversationID
	intent.RequestID = strings.TrimSpace(intent.RequestID)
	if conversationID == "" || intent.RequestID == "" {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	state := q.states[conversationID]
	if state == nil {
		state = &conversationRunState{}
		q.states[conversationID] = state
	}
	state.pending = append(state.pending, intent)
}

// Dequeue preserves the pre-scheduler queue API for existing integration paths.
func (q *runQueue) Dequeue(conversationID string) (InboundIntent, bool) {
	if q == nil {
		return InboundIntent{}, false
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return InboundIntent{}, false
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	state := q.states[conversationID]
	if state == nil || len(state.pending) == 0 {
		return InboundIntent{}, false
	}
	intent := state.pending[0]
	state.pending[0] = InboundIntent{}
	state.pending = state.pending[1:]
	if state.ownerRequestID == "" && len(state.pending) == 0 {
		delete(q.states, conversationID)
	}
	return intent, true
}

// activeConversationHasSubagents diagnoses the legacy subagent-only queue path.
func (service *Service) activeConversationHasSubagents(conversationID string) bool {
	if service == nil || service.broker == nil {
		return false
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return false
	}
	for _, requestID := range service.broker.OtherConversationRequestIDs(conversationID, "") {
		stream, ok := service.broker.Get(requestID)
		if !ok || stream == nil {
			continue
		}
		stream.mu.Lock()
		hit := false
		for _, pending := range stream.PendingExecs {
			if kind := strings.TrimSpace(pending.ExecKind); kind == "subagent" || kind == "delegation_aggregate" {
				hit = true
				break
			}
		}
		stream.mu.Unlock()
		if hit {
			return true
		}
	}
	return false
}

// drainRunQueue preserves the legacy subagent queue dispatch path until service integration is updated.
func (service *Service) drainRunQueue(conversationID string) {
	if service == nil || service.runQueue == nil {
		return
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return
	}
	intent, ok := service.runQueue.Dequeue(conversationID)
	if !ok {
		return
	}
	logger.Infof("forwarder run queue drained request_id=%s conversation_id=%s",
		strings.TrimSpace(intent.RequestID), conversationID)
	if err := service.handleRunIntent(intent); err != nil {
		logger.Errorf("forwarder run queue dispatch failed request_id=%s conversation_id=%s err=%v",
			strings.TrimSpace(intent.RequestID), conversationID, err)
		service.drainRunQueue(conversationID)
	}
}
