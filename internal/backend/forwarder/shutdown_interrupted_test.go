package forwarder

import (
	"context"
	"fmt"
	"testing"
	"time"

	"cursor/gen/agentv1"
)

func seedRunningConversation(t *testing.T, store *ConversationFileStore, conversationID string, requestID string) *ConversationFile {
	t.Helper()
	conversation := &ConversationFile{
		ConversationID:     conversationID,
		RootConversationID: conversationID,
		Mode:               "agent",
		NextTurnSeq:        1,
		NextEntrySeq:       1,
	}
	persisted, err := store.SaveConversationWithEntries(conversationID, conversation, []HistoryEntry{
		testUserMessageEntry(t, 1, requestID, "开始一个长任务"),
		newAssistantTextEntry(1, requestID, "正在处理", "", ""),
	})
	if err != nil {
		t.Fatalf("SaveConversationWithEntries(%s) error = %v", conversationID, err)
	}
	if persisted.CurrentLoopStatus != "running" {
		t.Fatalf("seeded %s status = %q, want running", conversationID, persisted.CurrentLoopStatus)
	}
	return persisted
}

// stallStreamActor 让该流的 actor 邮箱有缓冲但无人消费：postStreamCommandWait 会一直
// 等 result，Shutdown 的每流 1.5s 预算必然耗尽，复现「取消通知发不完」的真实场景。
func stallStreamActor(t *testing.T, stream *ActiveStream) {
	t.Helper()
	done := make(chan struct{})
	stream.mu.Lock()
	stream.ActorMailbox = make(chan streamCommandEnvelope, 128)
	stream.ActorDone = done
	stream.mu.Unlock()
	t.Cleanup(func() { close(done) })
}

func TestShutdownPersistsInterruptedForEveryActiveStreamBeyondCancelBudget(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), nil, nil, broker)
	// 不碰进程级共享的 MCP 运行时，避免 Shutdown 影响同一测试进程里的其他用例。
	service.mcpRuntime = nil

	const streamCount = 6
	conversationIDs := make([]string, 0, streamCount)
	for index := 0; index < streamCount; index++ {
		conversationID := fmt.Sprintf("aaaaaaaa-0000-0000-0000-00000000000%d", index)
		requestID := fmt.Sprintf("request-%d", index)
		seedRunningConversation(t, store, conversationID, requestID)
		stream, err := broker.OpenStream(requestID, conversationID, 1, "model-a", "model-a", agentv1.AgentMode_AGENT_MODE_AGENT, "开始一个长任务")
		if err != nil {
			t.Fatalf("OpenStream(%s) error = %v", requestID, err)
		}
		stallStreamActor(t, stream)
		conversationIDs = append(conversationIDs, conversationID)
	}

	// 关闭预算远小于「每流 1.5s × 6 条」，取消循环必然中途因 ctx 到期退出。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = service.Shutdown(ctx)

	for _, conversationID := range conversationIDs {
		conversation, err := store.LoadConversation(conversationID)
		if err != nil {
			t.Fatalf("LoadConversation(%s) error = %v", conversationID, err)
		}
		if conversation.CurrentLoopStatus != conversationStatusInterrupted {
			t.Fatalf("%s status = %q, want interrupted", conversationID, conversation.CurrentLoopStatus)
		}
	}
}
