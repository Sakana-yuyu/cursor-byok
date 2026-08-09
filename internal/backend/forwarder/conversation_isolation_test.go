package forwarder

import (
	"context"
	"strings"
	"testing"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
)

// conversationIsolationTestProvider 是可阻塞的 ProviderGateway：
// StartStream 先把 ProviderRequest 投递到 entered（无缓冲，证明真的进入 provider），
// 再阻塞在 release 上，由测试放行。用于断言不同 conversation 并发进入 provider。
type conversationIsolationTestProvider struct {
	entered chan ProviderRequest
	release chan struct{}
}

func (provider *conversationIsolationTestProvider) StartStream(_ context.Context, request ProviderRequest, _ func(modeladapter.ModelEvent) error) error {
	provider.entered <- request
	<-provider.release
	return errProviderLoopInterrupted
}

// TestDifferentConversationsSharingModelEnterProviderConcurrently 验证两个不同
// conversation 在同一个后端进程内、使用同一 provider/model 时可以同时进入 provider，
// 互不阻塞；同一个 conversation 的第二条 run 则不会创建第二个 provider 调用。
func TestDifferentConversationsSharingModelEnterProviderConcurrently(t *testing.T) {
	entered := make(chan ProviderRequest, 2)
	release := make(chan struct{})
	defer close(release)
	provider := &conversationIsolationTestProvider{entered: entered, release: release}

	service := newServiceWithDependencies(NewConversationFileStore(t.TempDir()), NewHistoryProjector(), contextProjectionLifecycleCompiler{}, provider, NewStreamBroker())

	runA := testRunIntent("conversation-a", "request-a")
	runA.Prewarm = false
	runB := testRunIntent("conversation-b", "request-b")
	runB.Prewarm = false

	if err := service.dispatchInboundIntent(runA); err != nil {
		t.Fatalf("dispatch A: %v", err)
	}
	if err := service.dispatchInboundIntent(runB); err != nil {
		t.Fatalf("dispatch B: %v", err)
	}

	seen := make(map[string]ProviderRequest, 2)
	for len(seen) < 2 {
		select {
		case request := <-entered:
			if request.ConversationID == "" || request.RequestID == "" {
				t.Fatalf("provider request missing correlation ids: %#v", request)
			}
			seen[request.ConversationID] = request
		case <-time.After(5 * time.Second):
			t.Fatalf("两个 conversation 未同时进入 provider，已到达 %v", seen)
		}
	}
	if seen["conversation-a"].RequestID != "request-a" {
		t.Fatalf("conversation-a 关联错误: %#v", seen["conversation-a"])
	}
	if seen["conversation-b"].RequestID != "request-b" {
		t.Fatalf("conversation-b 关联错误: %#v", seen["conversation-b"])
	}

	// 同一 conversation 的第二条 run 必须排队，不得创建第二个 provider 调用。
	secondA := testRunIntent("conversation-a", "request-a2")
	secondA.Prewarm = false
	if err := service.dispatchInboundIntent(secondA); err != nil {
		t.Fatalf("dispatch A2: %v", err)
	}
	if service.runQueue.Len("conversation-a") != 1 {
		t.Fatalf("conversation-a 排队长度 = %d, want 1", service.runQueue.Len("conversation-a"))
	}
	if _, ok := service.broker.Get("request-a2"); ok {
		t.Fatal("queued request-a2 不应创建活动流")
	}
	select {
	case <-entered:
		t.Fatal("queued request-a2 不应进入 provider")
	case <-time.After(300 * time.Millisecond):
	}
}

// TestQueuedRunDoesNotWriteHistoryBeforePromotion 验证排队中的 run intent 不会提前
// 持久化任何 user/request_context 条目：queue 在真正获得 owner 前不能污染 prompt replay。
func TestQueuedRunDoesNotWriteHistoryBeforePromotion(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	entered := make(chan ProviderRequest, 1)
	release := make(chan struct{})
	defer close(release)
	provider := &conversationIsolationTestProvider{entered: entered, release: release}
	service := newServiceWithDependencies(store, NewHistoryProjector(), contextProjectionLifecycleCompiler{}, provider, NewStreamBroker())

	first := testRunIntent("conversation-a", "request-1")
	first.Prewarm = false
	if err := service.dispatchInboundIntent(first); err != nil {
		t.Fatalf("dispatch first: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first run 未进入 provider")
	}

	second := testRunIntent("conversation-a", "request-2")
	second.Prewarm = false
	if err := service.dispatchInboundIntent(second); err != nil {
		t.Fatalf("dispatch queued: %v", err)
	}
	if service.runQueue.Len("conversation-a") != 1 {
		t.Fatalf("排队长度 = %d, want 1", service.runQueue.Len("conversation-a"))
	}

	conversation, err := store.LoadConversation("conversation-a")
	if err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	if conversation != nil {
		for _, entry := range conversation.Entries {
			if strings.TrimSpace(entry.RequestID) == "request-2" {
				t.Fatalf("queued request-2 提前写入了 history: %#v", entry)
			}
		}
	}
}

// TestCrossConversationProjectionIsolation 验证两个 conversation 的 ProjectPromptReplay
// 只包含各自 entries，且 checkpoint 投影不串档。
func TestCrossConversationProjectionIsolation(t *testing.T) {
	projector := NewHistoryProjector()
	conversationA := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-a", "question A only"),
		newAssistantTextEntry(1, "request-a", "answer A only", "", ""),
	})
	conversationB := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-b", "question B only"),
		newAssistantTextEntry(1, "request-b", "answer B only", "", ""),
	})

	replayA, err := projector.ProjectPromptReplay(conversationA)
	if err != nil {
		t.Fatalf("replay A: %v", err)
	}
	replayB, err := projector.ProjectPromptReplay(conversationB)
	if err != nil {
		t.Fatalf("replay B: %v", err)
	}
	textA := contextProjectionMessageText(replayA)
	textB := contextProjectionMessageText(replayB)
	if !strings.Contains(textA, "question A only") || strings.Contains(textA, "question B only") {
		t.Fatalf("conversation A replay 污染: %q", textA)
	}
	if !strings.Contains(textB, "question B only") || strings.Contains(textB, "question A only") {
		t.Fatalf("conversation B replay 污染: %q", textB)
	}
}
