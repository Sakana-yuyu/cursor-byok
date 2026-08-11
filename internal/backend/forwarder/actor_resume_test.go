package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
)

// TestProviderResumeStartsNextPassWithoutFixedDelay 锁定工具结果、压缩或恢复逻辑
// 已经完成后，下一轮 provider pass 不再额外等待固定防抖窗口。并发工具结果仍由
// pendingBridgeCount 在最后一个结果收口前拦截，因此无需再用时间等待合并。
func TestProviderResumeStartsNextPassWithoutFixedDelay(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-resume", "继续执行"),
	})
	provider := &contextProjectionRequestProvider{requests: make(chan ProviderRequest, 1)}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), contextProjectionLifecycleCompiler{}, provider, broker)
	stream, err := broker.OpenStream(
		"request-resume",
		conversation.ConversationID,
		1,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"继续执行",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(conversation)

	if err := service.requestProviderAction(stream, providerActionResume); err != nil {
		t.Fatalf("requestProviderAction(resume) error = %v", err)
	}
	stream.mu.Lock()
	providerPassCount := stream.ProviderPassCount
	providerActive := stream.ProviderActive
	pendingAction := stream.PendingProviderAction
	stream.mu.Unlock()
	if providerPassCount != 1 || !providerActive || pendingAction != providerActionNone {
		t.Fatalf("resume state = pass=%d active=%t pending=%q, want pass=1 active=true pending=none", providerPassCount, providerActive, pendingAction)
	}
	<-provider.requests
	waitForContextProjectionProviderIdle(t, stream)
}
