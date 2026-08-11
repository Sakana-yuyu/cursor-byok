package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
)

func TestRegisterNativeDelegationWaitsForClientSubagentBeforeParentDelta(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	requestID := "req-native-register"
	conversationID := "conv-native-register"
	execID := "exec-native-register"
	toolCallID := "call-native-register"

	stream, err := service.broker.OpenStream(
		requestID,
		conversationID,
		1,
		"model-id",
		"Model Name",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"delegate work",
	)
	if err != nil {
		t.Fatal(err)
	}
	pending := runtimecore.PendingExec{
		ExecID:     execID,
		ExecKind:   "subagent",
		ToolCallID: toolCallID,
		ArgsJSON:   []byte(`{"description":"inspect routes","subagent_type":"explore"}`),
	}

	if !service.registerNativeDelegation(stream, pending, nil) {
		t.Fatal("native delegation was not registered")
	}
	defer service.updateNativeDelegationStatus(execID, delegation.TaskCompleted, "completed", "")

	if got := len(stream.Backlog); got != 0 {
		t.Fatalf("parent backlog length = %d, want 0 before Cursor links the real child composer", got)
	}
}

func TestPublishNativeDelegationProgressScopesDeltaToTaskToolCall(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	requestID := "req-native-progress"
	conversationID := "conv-native-progress"
	execID := "exec-native-progress"
	toolCallID := "call-native-progress"

	stream, err := service.broker.OpenStream(
		requestID,
		conversationID,
		1,
		"model-id",
		"Model Name",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"delegate work",
	)
	if err != nil {
		t.Fatal(err)
	}
	service.nativeDelegations = map[string]*nativeDelegationRuntime{
		execID: {
			ID:              execID,
			ParentRequestID: requestID,
			ToolCallID:      toolCallID,
			Status:          delegation.TaskRunning,
		},
	}

	service.publishNativeDelegationProgress(requestID, execID, "工作摘要：仍在执行。")

	if len(stream.Backlog) != 1 {
		t.Fatalf("backlog length = %d, want 1", len(stream.Backlog))
	}
	update := stream.Backlog[0].Message.GetInteractionUpdate()
	if update == nil {
		t.Fatal("interaction update is nil")
	}
	if update.GetThinkingDelta() != nil {
		t.Fatal("native delegation progress leaked into parent thinking stream")
	}
	toolDelta := update.GetToolCallDelta()
	if toolDelta == nil {
		t.Fatal("task tool call delta is nil")
	}
	if toolDelta.GetCallId() != toolCallID {
		t.Fatalf("call id = %q, want %q", toolDelta.GetCallId(), toolCallID)
	}
	inner := toolDelta.GetToolCallDelta().GetTaskToolCallDelta().GetInteractionUpdate().GetThinkingDelta()
	if inner == nil || inner.GetText() != "工作摘要：仍在执行。" {
		t.Fatalf("inner thinking delta = %#v", inner)
	}
}

func TestNativeChildTextDeltaMirrorsIntoParentTaskBubble(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	parentRequestID := "req-parent"
	parentConversationID := "conv-parent"
	childRequestID := "req-child"
	childConversationID := "conv-child"
	execID := "exec-native-child"
	toolCallID := "call-native-child"

	parent, err := service.broker.OpenStream(parentRequestID, parentConversationID, 1, "model", "Model", agentv1.AgentMode_AGENT_MODE_AGENT, "delegate")
	if err != nil {
		t.Fatal(err)
	}
	child, err := service.broker.OpenStream(childRequestID, childConversationID, 1, "model", "Model", agentv1.AgentMode_AGENT_MODE_AGENT, "inspect")
	if err != nil {
		t.Fatal(err)
	}
	child.CurrentModelCallID = "child-model-call"
	child.CheckpointConversation = &ConversationFile{
		ConversationID:       childConversationID,
		ParentConversationID: parentConversationID,
		ParentToolCallID:     toolCallID,
		SubagentTypeName:     "generalPurpose",
	}
	service.nativeDelegations = map[string]*nativeDelegationRuntime{
		execID: {
			ID:              execID,
			ParentRequestID: parentRequestID,
			ConversationID:  parentConversationID,
			ToolCallID:      toolCallID,
			Status:          delegation.TaskRunning,
		},
	}

	if err := service.applyProviderModelEvent(child, modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "正在检查 API 路由。"}); err != nil {
		t.Fatal(err)
	}

	if len(parent.Backlog) != 1 {
		t.Fatalf("parent backlog length = %d, want 1", len(parent.Backlog))
	}
	delta := parent.Backlog[0].Message.GetInteractionUpdate().GetToolCallDelta()
	if delta == nil || delta.GetCallId() != toolCallID {
		t.Fatalf("parent task delta = %#v", delta)
	}
	if got := delta.GetToolCallDelta().GetTaskToolCallDelta().GetInteractionUpdate().GetTextDelta().GetText(); got != "正在检查 API 路由。" {
		t.Fatalf("mirrored child text = %q", got)
	}

	if err := service.applyProviderModelEvent(child, modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindThinkingDelta, Text: "先核对调用链。", ThinkingStyle: agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT}); err != nil {
		t.Fatal(err)
	}
	if len(parent.Backlog) != 2 {
		t.Fatalf("parent backlog length after thinking delta = %d, want 2", len(parent.Backlog))
	}
	thinking := parent.Backlog[1].Message.GetInteractionUpdate().GetToolCallDelta()
	if thinking == nil || thinking.GetCallId() != toolCallID {
		t.Fatalf("parent thinking task delta = %#v", thinking)
	}
	if got := thinking.GetToolCallDelta().GetTaskToolCallDelta().GetInteractionUpdate().GetThinkingDelta().GetText(); got != "先核对调用链。" {
		t.Fatalf("mirrored child thinking = %q", got)
	}
}
