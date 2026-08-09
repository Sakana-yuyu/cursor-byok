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

func (service *Service) startAdmittedRun(intent InboundIntent) error {
	if err := service.startOwnedRun(intent); err != nil {
		startupErr := fmt.Errorf("start admitted run: %w", err)
		stream, opened := service.broker.Get(intent.RequestID)
		if !opened || stream == nil {
			service.finishConversationTurn(intent.ConversationID, intent.RequestID)
			return startupErr
		}
		stream.mu.Lock()
		alreadyTerminal := isTerminalStreamStatus(stream.Status)
		stream.mu.Unlock()
		// 启动失败发生在 handleRunIntent 写历史之前。若该流已被并发路径收口为
		// 终态（如 canceled），不能再调用 broker.Fail：它会覆盖终态状态并重复
		// 发布 endstream。终态流直接释放 owner 即可安全推进后继。
		if alreadyTerminal {
			service.finishConversationTurn(intent.ConversationID, intent.RequestID)
			return startupErr
		}
		service.setTurnPhase(stream, TurnPhaseFailed)
		if terminalErr := service.broker.Fail(intent.RequestID, "startup_error", "[internal] Run startup failed"); terminalErr != nil {
			// 该请求未写任何历史，终态化失败也必须释放 owner，否则会话永久卡死。
			// 记录可诊断的失败字段，仍推进队列后继（后继只读取已持久化终态历史）。
			logger.Errorf("forwarder admitted run startup terminalization failed request_id=%s conversation_id=%s err=%v",
				strings.TrimSpace(intent.RequestID), strings.TrimSpace(intent.ConversationID), terminalErr)
		}
		service.finishConversationTurn(intent.ConversationID, intent.RequestID)
		return startupErr
	}
	return nil
}

func (service *Service) startPromotedRun(intent InboundIntent) {
	if err := service.startAdmittedRun(intent); err != nil {
		logger.Errorf("forwarder promoted conversation run startup failed request_id=%s conversation_id=%s err=%v",
			strings.TrimSpace(intent.RequestID), strings.TrimSpace(intent.ConversationID), err)
	}
}

// drainRunQueue preserves terminal call sites while releasing only the request that terminated.
func (service *Service) drainRunQueue(conversationID string, requestID string) {
	service.finishConversationTurn(conversationID, requestID)
}
