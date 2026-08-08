package forwarder

import (
	"testing"

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
