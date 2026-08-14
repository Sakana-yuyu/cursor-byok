package forwarder

import (
	"context"
	"fmt"
	"strings"

	"cursor/internal/backend/forwarder/runqueue"
	"cursor/internal/logger"
	"cursor/internal/safego"
)

type runQueueSubmitResult = runqueue.SubmitResult

const (
	runQueueStart     = runqueue.Start
	runQueueQueued    = runqueue.Queued
	runQueueDuplicate = runqueue.Duplicate
)

type queuedRunCancellation = runqueue.Cancellation[InboundIntent]

type runQueue = runqueue.Queue[InboundIntent]

func newRunQueue() *runQueue {
	return runqueue.New[InboundIntent]()
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
		if alreadyTerminal {
			service.finishConversationTurn(intent.ConversationID, intent.RequestID)
			return startupErr
		}
		service.setTurnPhase(stream, TurnPhaseFailed)
		if terminalErr := service.broker.Fail(intent.RequestID, "startup_error", "[internal] Run startup failed"); terminalErr != nil {
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

func (service *Service) drainRunQueue(conversationID string, requestID string) {
	service.finishConversationTurn(conversationID, requestID)
}

func (service *Service) cancelQueuedRun(intent InboundIntent) (handled bool, err error) {
	if service == nil || service.runQueue == nil {
		return false, nil
	}
	conversationID := strings.TrimSpace(intent.ConversationID)
	requestID := strings.TrimSpace(intent.RequestID)
	if requestID == "" {
		return false, nil
	}
	var canceled queuedRunCancellation
	if conversationID != "" {
		var ok bool
		canceled, ok = service.runQueue.CancelQueued(conversationID, requestID)
		if !ok {
			return false, nil
		}
	} else {
		var foundConversation string
		var ok bool
		foundConversation, canceled, ok = service.runQueue.CancelQueuedByRequestID(requestID)
		if !ok {
			return false, nil
		}
		conversationID = foundConversation
	}
	logger.Infof("forwarder queued run canceled request_id=%s conversation_id=%s owner_request_id=%s queue_position=%d queue_len=%d",
		requestID, conversationID, service.runQueue.Owner(conversationID), canceled.Position, service.runQueue.Len(conversationID))
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), requestID, conversationID, "queued_run_canceled", map[string]any{
			"queue_position":   canceled.Position,
			"queue_len":        service.runQueue.Len(conversationID),
			"owner_request_id": service.runQueue.Owner(conversationID),
		})
	}
	return true, nil
}

func (intent InboundIntent) GetConversationID() string {
	return strings.TrimSpace(intent.ConversationID)
}

func (intent InboundIntent) GetRequestID() string {
	return strings.TrimSpace(intent.RequestID)
}
