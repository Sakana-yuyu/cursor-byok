package forwarder

import (
	"testing"
	"time"

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

	started := time.Now()
	if err := service.requestProviderAction(stream, providerActionResume); err != nil {
		t.Fatalf("requestProviderAction(resume) error = %v", err)
	}
	select {
	case <-provider.requests:
		if elapsed := time.Since(started); elapsed >= 100*time.Millisecond {
			t.Fatalf("resume waited %s before starting next provider pass", elapsed)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("resume did not start next provider pass within 100ms")
	}
	waitForContextProjectionProviderIdle(t, stream)
}
