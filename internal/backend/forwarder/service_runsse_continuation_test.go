package forwarder

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"

	"cursor/gen/agentv1"
	"cursor/gen/aiserverv1"
)

func TestWaitForRunSSEContinuationDetectsReopenedRequest(t *testing.T) {
	service, stream, signal, cursor := completedRunSSETestStream(t)
	reopenErr := make(chan error, 1)

	go func() {
		time.Sleep(10 * time.Millisecond)
		if err := service.reopenTerminalStreamForNewTurn(stream); err != nil {
			reopenErr <- err
			return
		}
		reopenErr <- service.broker.Publish(stream.RequestID, StreamEvent{Message: buildHeartbeatMessage()})
	}()

	if !service.waitForRunSSEContinuation(context.Background(), stream.RequestID, cursor, signal, 200*time.Millisecond) {
		t.Fatal("同一 request 重开后未被识别为 RunSSE continuation")
	}
	if err := <-reopenErr; err != nil {
		t.Fatalf("重开同一 request: %v", err)
	}
}

func TestWaitForRunSSEContinuationTimesOutWithoutContinuation(t *testing.T) {
	service, stream, signal, cursor := completedRunSSETestStream(t)
	select {
	case <-signal:
	default:
	}

	if service.waitForRunSSEContinuation(context.Background(), stream.RequestID, cursor, signal, 20*time.Millisecond) {
		t.Fatal("没有新回合时不应保留 RunSSE")
	}
}

func TestWaitForRunSSEContinuationIgnoresStaleSignal(t *testing.T) {
	service, stream, signal, cursor := completedRunSSETestStream(t)

	if service.waitForRunSSEContinuation(context.Background(), stream.RequestID, cursor, signal, 20*time.Millisecond) {
		t.Fatal("旧终态信号不应被识别为 RunSSE continuation")
	}
}

func TestStreamForIntentReopensCompletedSameRequestWithoutForceFlag(t *testing.T) {
	service, stream := completedRunSSEHTTPTestStream(t, "request-runsse-queued-turn")
	done := make(chan struct{})
	close(done)
	stream.mu.Lock()
	stream.ActorMailbox = make(chan streamCommandEnvelope, 1)
	stream.ActorDone = done
	stream.mu.Unlock()

	reopened, err := service.streamForIntent(InboundIntent{
		Kind:           "run",
		RequestID:      stream.RequestID,
		ConversationID: stream.ConversationID,
		ModelID:        "model-id",
		ModelName:      "model-name",
		Mode:           agentv1.AgentMode_AGENT_MODE_MULTITASK,
	})
	if err != nil {
		t.Fatalf("重开排队回合: %v", err)
	}
	if reopened != stream {
		t.Fatal("同一 request 的排队回合应复用原 stream")
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.Status != StreamStatusCreated || stream.Phase != TurnPhaseIdle {
		t.Fatalf("重开后状态 = %s/%s，期望 created/idle", stream.Status, stream.Phase)
	}
	if stream.ActorMailbox != nil || stream.ActorDone != nil {
		t.Fatal("重开后不应保留已退出的旧 actor")
	}
}

func TestShouldReuseActiveRunDistinguishesQueuedUserMessageFromReconnect(t *testing.T) {
	service, stream := completedRunSSEHTTPTestStream(t, "request-runsse-queued-message")
	stream.mu.Lock()
	stream.Status = StreamStatusStreaming
	stream.Phase = TurnPhaseProviderRunning
	stream.TurnSeq = 3
	stream.CheckpointConversation = &ConversationFile{
		ConversationID: stream.ConversationID,
		Entries: []HistoryEntry{
			testUserMessageEntry(t, 3, stream.RequestID, "first message"),
		},
	}
	stream.mu.Unlock()

	currentMessageID := "message-3"
	baseIntent := InboundIntent{
		Kind:           "run",
		RequestID:      stream.RequestID,
		ConversationID: stream.ConversationID,
		StartsRun:      true,
		ModelID:        stream.ModelID,
	}
	reconnect := baseIntent
	reconnect.UserMessage = &agentv1.UserMessage{MessageId: currentMessageID, Text: "first message"}
	if !service.shouldReuseActiveRun(reconnect) {
		t.Fatal("相同 user message ID 的 RunSSE 重连应复用当前回合")
	}

	queued := baseIntent
	queued.UserMessage = &agentv1.UserMessage{MessageId: "message-4", Text: "queued message"}
	if service.shouldReuseActiveRun(queued) {
		t.Fatal("不同 user message ID 的排队消息不应被误判为 RunSSE 重连")
	}
}

func TestRunSSETerminalContinuation(t *testing.T) {
	t.Run("成功终态承接同 request 新回合", func(t *testing.T) {
		service, stream := completedRunSSEHTTPTestStream(t, "request-runsse-success")
		server := newRunSSETestServer(t, service)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		response, err := newRunSSETestClient(server).CallServerStream(ctx, connect.NewRequest(&aiserverv1.BidiRequestId{RequestId: stream.RequestID}))
		if err != nil {
			t.Fatalf("建立 RunSSE: %v", err)
		}
		if !response.Receive() {
			t.Fatalf("未收到旧回合消息: %v", response.Err())
		}
		if err := service.reopenTerminalStreamForNewTurn(stream); err != nil {
			t.Fatalf("重开同一 request: %v", err)
		}
		if err := service.broker.Publish(stream.RequestID, StreamEvent{Message: buildHeartbeatMessage()}); err != nil {
			t.Fatalf("发布新回合消息: %v", err)
		}
		if err := service.broker.Complete(stream.RequestID, "", ""); err != nil {
			t.Fatalf("完成新回合: %v", err)
		}
		if !response.Receive() {
			t.Fatalf("旧成功 End 后未收到新回合消息: %v", response.Err())
		}
		for response.Receive() {
		}
		if err := response.Err(); err != nil {
			t.Fatalf("RunSSE 新回合结束: %v", err)
		}
	})

	for _, test := range []struct {
		name     string
		request  string
		complete func(*StreamBroker, string) error
		wantCode connect.Code
	}{
		{name: "失败终态立即返回", request: "request-runsse-failed", complete: func(broker *StreamBroker, requestID string) error {
			return broker.Fail(requestID, "provider_error", "provider failed")
		}, wantCode: connect.CodeUnavailable},
		{name: "取消终态立即返回", request: "request-runsse-canceled", complete: func(broker *StreamBroker, requestID string) error {
			return broker.Cancel(requestID, "user canceled")
		}, wantCode: connect.CodeCanceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			broker := NewStreamBroker()
			service := &Service{broker: broker}
			stream, err := broker.OpenStream(test.request, "conversation-runsse-terminal", 1, "model-id", "model-name", agentv1.AgentMode_AGENT_MODE_AGENT, "message")
			if err != nil {
				t.Fatalf("打开测试流: %v", err)
			}
			if err := test.complete(broker, stream.RequestID); err != nil {
				t.Fatalf("写入终态: %v", err)
			}
			server := newRunSSETestServer(t, service)
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			response, err := newRunSSETestClient(server).CallServerStream(ctx, connect.NewRequest(&aiserverv1.BidiRequestId{RequestId: stream.RequestID}))
			if err == nil {
				for response.Receive() {
				}
				err = response.Err()
			}
			if connect.CodeOf(err) != test.wantCode {
				t.Fatalf("终态错误码 = %s，期望 %s，错误: %v", connect.CodeOf(err), test.wantCode, err)
			}
		})
	}
}

func completedRunSSEHTTPTestStream(t *testing.T, requestID string) (*Service, *ActiveStream) {
	t.Helper()
	broker := NewStreamBroker()
	service := &Service{broker: broker}
	stream, err := broker.OpenStream(requestID, "conversation-runsse-success", 1, "model-id", "model-name", agentv1.AgentMode_AGENT_MODE_AGENT, "message")
	if err != nil {
		t.Fatalf("打开测试流: %v", err)
	}
	if err := broker.Publish(stream.RequestID, StreamEvent{Message: buildHeartbeatMessage()}); err != nil {
		t.Fatalf("发布旧回合消息: %v", err)
	}
	service.setTurnPhase(stream, TurnPhaseCompleted)
	if err := broker.Complete(stream.RequestID, "", ""); err != nil {
		t.Fatalf("完成测试流: %v", err)
	}
	return service, stream
}

func newRunSSETestServer(t *testing.T, service *Service) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(connect.NewServerStreamHandler("/agent.v1.AgentService/RunSSE", service.RunSSE))
	t.Cleanup(server.Close)
	return server
}

func newRunSSETestClient(server *httptest.Server) *connect.Client[aiserverv1.BidiRequestId, agentv1.AgentServerMessage] {
	return connect.NewClient[aiserverv1.BidiRequestId, agentv1.AgentServerMessage](server.Client(), server.URL+"/agent.v1.AgentService/RunSSE")
}

func completedRunSSETestStream(t *testing.T) (*Service, *ActiveStream, <-chan struct{}, int) {
	t.Helper()

	broker := NewStreamBroker()
	service := &Service{broker: broker}
	stream, err := broker.OpenStream(
		"request-runsse-continuation",
		"conversation-runsse-continuation",
		1,
		"model-id",
		"model-name",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"first message",
	)
	if err != nil {
		t.Fatalf("打开测试流: %v", err)
	}
	_, signal, _, err := broker.Subscribe(stream.RequestID)
	if err != nil {
		t.Fatalf("订阅测试流: %v", err)
	}
	service.setTurnPhase(stream, TurnPhaseCompleted)
	if err := broker.Complete(stream.RequestID, "", ""); err != nil {
		t.Fatalf("完成测试流: %v", err)
	}
	backlog, err := broker.ReadFromCursor(stream.RequestID, 0)
	if err != nil {
		t.Fatalf("读取测试流 backlog: %v", err)
	}
	return service, stream, signal, len(backlog)
}
