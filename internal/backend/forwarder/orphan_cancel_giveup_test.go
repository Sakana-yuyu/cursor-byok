package forwarder

import (
	"encoding/json"
	"strings"
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func orphanTimerToken(t *testing.T, stream *ActiveStream) (uint64, bool) {
	t.Helper()
	stream.mu.Lock()
	defer stream.mu.Unlock()
	token, ok := stream.TimerTokens[providerTimerKey(streamTimerOrphanCancel, "")]
	return token, ok
}

func fireOrphanCancelTimer(t *testing.T, service *Service, stream *ActiveStream, token uint64) {
	t.Helper()
	if err := service.handleTimerEvent(stream, &streamTimerEvent{
		Key:    providerTimerKey(streamTimerOrphanCancel, ""),
		Kind:   streamTimerOrphanCancel,
		Token:  token,
		Reason: "[canceled] RunSSE client disconnected",
	}); err != nil {
		t.Fatalf("handleTimerEvent() error = %v", err)
	}
}

func newOrphanCancelFixture(t *testing.T) (*Service, *ActiveStream, *ConversationFileStore, string) {
	t.Helper()
	store := NewConversationFileStore(t.TempDir())
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), nil, nil, broker)

	conversationID := "cccccccc-0000-0000-0000-000000000001"
	persisted := seedRunningConversation(t, store, conversationID, "request-orphan")
	stream, err := broker.OpenStream("request-orphan", conversationID, 1, "model-a", "model-a", agentv1.AgentMode_AGENT_MODE_AGENT, "长任务")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.mu.Lock()
	stream.CheckpointConversation = cloneConversationFile(persisted)
	// 断连但仍有待执行工具：这正是旧实现「defer 且不续排」后永远无人收口的组合。
	stream.PendingExecs["exec-1"] = runtimecore.PendingExec{ExecID: "exec-1", ExecKind: "shell", ToolCallID: "tool-1"}
	stream.TimerTokens[providerTimerKey(streamTimerOrphanCancel, "")] = 1
	stream.mu.Unlock()
	return service, stream, store, conversationID
}

func TestOrphanCancelRequeuesWhileWorkStillActive(t *testing.T) {
	service, stream, _, _ := newOrphanCancelFixture(t)

	fireOrphanCancelTimer(t, service, stream, 1)

	if _, ok := orphanTimerToken(t, stream); !ok {
		t.Fatal("orphan cancel timer was not requeued after deferring an active turn")
	}
	stream.mu.Lock()
	status := stream.Status
	stream.mu.Unlock()
	if isTerminalStreamStatus(status) {
		t.Fatalf("stream status = %q, want non-terminal while work is still active", status)
	}
}

func TestOrphanCancelGivesUpAsInterruptedAfterMaxDeferrals(t *testing.T) {
	service, stream, store, conversationID := newOrphanCancelFixture(t)

	token := uint64(1)
	for attempt := 0; attempt < orphanCancelMaxDeferrals+5; attempt++ {
		fireOrphanCancelTimer(t, service, stream, token)
		stream.mu.Lock()
		status := stream.Status
		stream.mu.Unlock()
		if isTerminalStreamStatus(status) {
			break
		}
		next, ok := orphanTimerToken(t, stream)
		if !ok {
			t.Fatalf("orphan cancel timer missing after attempt %d without reaching a terminal status", attempt)
		}
		token = next
	}

	stream.mu.Lock()
	status := stream.Status
	stream.mu.Unlock()
	if !isTerminalStreamStatus(status) {
		t.Fatalf("stream status = %q, want terminal after exhausting orphan cancel deferrals", status)
	}

	conversation, err := store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v", err)
	}
	if conversation.CurrentLoopStatus != conversationStatusInterrupted {
		t.Fatalf("conversation status = %q, want interrupted", conversation.CurrentLoopStatus)
	}
	// canceled 条目会触发 sanitizeCanceledReplayEntries 回溯性改写该回合的模型可见历史，
	// 放弃 orphan cancel 属于「被打断」而非「用户取消」，绝不能落成 canceled。
	for _, entry := range conversation.Entries {
		if strings.TrimSpace(entry.Kind) != "metadata" {
			continue
		}
		var payload metadataPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			continue
		}
		if strings.TrimSpace(payload.Type) == "control" && strings.TrimSpace(readStringValue(payload.Value["status"])) == "canceled" {
			t.Fatal("orphan cancel give-up persisted a canceled control entry; replay sanitation would rewrite the sent prefix")
		}
	}
}
