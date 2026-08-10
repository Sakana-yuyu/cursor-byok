package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func TestBuildNativeTaskOpenContextMarksDirectParentChild(t *testing.T) {
	stream := &ActiveStream{
		ConversationID: "conversation-1",
		ModelID:        "model-1",
	}

	context := buildNativeTaskOpenContextForStream(stream, nil)
	if !context.DirectMetaParentChild {
		t.Fatal("native Task open context must mark the child as a direct parent-child subagent")
	}
	if context.ConversationID != "conversation-1" {
		t.Fatalf("ConversationID = %q, want conversation-1", context.ConversationID)
	}
}

func TestBuildExecOpenContextForStreamDoesNotMarkGenericExecAsDirectChild(t *testing.T) {
	stream := &ActiveStream{
		SubagentModelOverrides: map[string]runtimecore.SubagentModelOverrideSelection{},
	}

	if buildExecOpenContextForStream(stream, nil).DirectMetaParentChild {
		t.Fatal("generic exec context must not opt every exec into the Task parent-child protocol")
	}
}

func TestCursorAgentCapabilityRequiresLiveSubscriber(t *testing.T) {
	service := NewService(t.TempDir(), nil)
	if service.CursorAgentExecutionAvailable("parent-1") {
		t.Fatal("missing stream must not expose Cursor agent capability")
	}
	stream, err := service.broker.OpenStream("parent-1", "conversation-1", 1, "model-1", "model-1", agentv1.AgentMode_AGENT_MODE_AGENT, "test")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	if service.CursorAgentExecutionAvailable("parent-1") {
		t.Fatal("stream without subscriber must not expose Cursor agent capability")
	}
	subscriberID, _, _, err := service.broker.Subscribe("parent-1")
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if !service.CursorAgentExecutionAvailable("parent-1") || !service.CursorAgentExecutionAvailable("") {
		t.Fatal("live subscriber must expose request-specific and global Cursor agent capability")
	}
	service.broker.Unsubscribe("parent-1", subscriberID)
	if service.CursorAgentExecutionAvailable("parent-1") {
		t.Fatal("disconnected subscriber must revoke Cursor agent capability")
	}

	stream.mu.Lock()
	stream.Status = StreamStatusCompleted
	stream.Subscribers["stale"] = &StreamSubscriber{Signal: make(chan struct{}, 1)}
	stream.mu.Unlock()
	if service.CursorAgentExecutionAvailable("parent-1") {
		t.Fatal("terminal stream must not expose Cursor agent capability")
	}
}
