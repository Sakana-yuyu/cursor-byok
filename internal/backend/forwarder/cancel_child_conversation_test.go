package forwarder

import (
	"net/http"
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
)

type nativeCancelFixture struct {
	service        *Service
	store          *ConversationFileStore
	historyRoot    string
	parentStream   *ActiveStream
	childID        string
	parentID       string
	childRequestID string
}

func newNativeCancelFixture(t *testing.T) *nativeCancelFixture {
	t.Helper()
	historyRoot := t.TempDir()
	store := NewConversationFileStore(historyRoot)
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), nil, nil, broker)

	parentID := "bbbbbbbb-0000-0000-0000-000000000001"
	childID := "bbbbbbbb-0000-0000-0000-000000000002"
	seedRunningConversation(t, store, parentID, "request-parent")
	seedRunningConversation(t, store, childID, "request-child")

	parentStream, err := broker.OpenStream("request-parent", parentID, 1, "model-a", "model-a", agentv1.AgentMode_AGENT_MODE_AGENT, "派个子代理")
	if err != nil {
		t.Fatalf("OpenStream(parent) error = %v", err)
	}
	childStream, err := broker.OpenStream("request-child", childID, 1, "model-a", "model-a", agentv1.AgentMode_AGENT_MODE_AGENT, "子任务")
	if err != nil {
		t.Fatalf("OpenStream(child) error = %v", err)
	}
	_ = childStream

	header := http.Header{}
	header.Set(headerParentRequestID, "request-parent")
	header.Set(headerParentAgentToolCallID, "tool-1")
	service.rememberChildParentLink("request-child", header)

	service.delegationRuntimeMu.Lock()
	service.nativeDelegations["exec-1"] = &nativeDelegationRuntime{
		ID:              "exec-1",
		ParentRequestID: "request-parent",
		ConversationID:  parentID,
		ToolCallID:      "tool-1",
		Status:          delegation.TaskRunning,
	}
	service.delegationRuntimeMu.Unlock()

	parentStream.mu.Lock()
	parentStream.PendingExecs["exec-1"] = runtimecore.PendingExec{
		ExecID:      "exec-1",
		ExecKind:    "subagent",
		ToolCallID:  "tool-1",
		ModelCallID: "model-call-1",
		ArgsJSON:    []byte(`{"description":"子任务"}`),
	}
	parentStream.mu.Unlock()

	return &nativeCancelFixture{
		service:        service,
		store:          store,
		historyRoot:    historyRoot,
		parentStream:   parentStream,
		childID:        childID,
		parentID:       parentID,
		childRequestID: "request-child",
	}
}

func TestHardCancelMarksChildConversationInterrupted(t *testing.T) {
	fixture := newNativeCancelFixture(t)

	if err := fixture.service.handleCancelIntent(InboundIntent{
		Kind:         "cancel",
		RequestID:    "request-parent",
		CancelReason: "user_stopped_generation",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	child, err := fixture.store.LoadConversation(fixture.childID)
	if err != nil {
		t.Fatalf("LoadConversation(child) error = %v", err)
	}
	if child.CurrentLoopStatus != conversationStatusInterrupted {
		t.Fatalf("child status = %q, want interrupted", child.CurrentLoopStatus)
	}
}

// TestFollowUpCancelLeavesChildConversationUntouched 守卫本轮改动之间唯一的真实冲突点：
// follow-up 取消的语义是「子代理继续在后台跑」，父侧给子会话写终态会直接说谎。
func TestFollowUpCancelLeavesChildConversationUntouched(t *testing.T) {
	fixture := newNativeCancelFixture(t)
	before := snapshotConversationFiles(t, fixture.historyRoot, fixture.childID)

	if err := fixture.service.handleCancelIntent(InboundIntent{
		Kind:         "cancel",
		RequestID:    "request-parent",
		CancelReason: "new_message_submitted",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	assertConversationFilesUnchanged(t, fixture.historyRoot, fixture.childID, before)
}
