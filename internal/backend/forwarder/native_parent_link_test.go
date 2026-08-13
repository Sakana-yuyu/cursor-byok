package forwarder

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

// childRunSSEHeader 复刻客户端 agent-client 在子代理 RunSSE 上真实发送的父链路头
// （bundle: gIg() -> "x-parent-request-id" / "x-root-parent-request-id" / "x-parent-agent-tool-call-id"）。
func childRunSSEHeader(parentRequestID, parentToolCallID string) http.Header {
	header := http.Header{}
	header.Set("x-parent-request-id", parentRequestID)
	header.Set("x-root-parent-request-id", parentRequestID)
	header.Set("x-parent-agent-tool-call-id", parentToolCallID)
	return header
}

func registerParentTaskDelegation(t *testing.T, service *Service, parentRequestID, parentConversationID, toolCallID string) {
	t.Helper()
	stream, err := service.broker.OpenStream(parentRequestID, parentConversationID, 1, "model-id", "Model Name",
		agentv1.AgentMode_AGENT_MODE_AGENT, "delegate one inspection task")
	if err != nil {
		t.Fatal(err)
	}
	pending := runtimecore.PendingExec{
		ExecID:     "exec-parent-task",
		ExecKind:   "subagent",
		ToolCallID: toolCallID,
		ArgsJSON:   []byte(`{"description":"inspect routes","subagent_type":"explore"}`),
	}
	if !service.registerNativeDelegation(stream, pending, nil) {
		t.Fatal("native delegation was not registered")
	}
}

func TestChildRunInheritsParentLinkFromRunSSEHeaders(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	defer service.multitaskDelegation.Close()
	parentRequestID := "req-parent-link"
	parentConversationID := "conv-parent-link"
	parentToolCallID := "call-parent-task"
	childRequestID := "req-child-link"
	childConversationID := "conv-child-link"

	registerParentTaskDelegation(t, service, parentRequestID, parentConversationID, parentToolCallID)
	service.rememberChildParentLink(childRequestID, childRunSSEHeader(parentRequestID, parentToolCallID))

	intent := InboundIntent{
		Kind:             "run",
		RequestID:        childRequestID,
		ConversationID:   childConversationID,
		Mode:             agentv1.AgentMode_AGENT_MODE_AGENT,
		SubagentTypeName: "explore",
		UserMessage:      &agentv1.UserMessage{Text: "inspect routes", MessageId: "message-1"},
	}
	conversation, _, _, _, err := service.bootstrapRuntimeConversation(intent)
	if err != nil {
		t.Fatalf("bootstrapRuntimeConversation() error = %v", err)
	}
	if got := strings.TrimSpace(conversation.ParentConversationID); got != parentConversationID {
		t.Fatalf("child parent_conversation_id = %q, want %q", got, parentConversationID)
	}
	if got := strings.TrimSpace(conversation.ParentToolCallID); got != parentToolCallID {
		t.Fatalf("child parent_tool_call_id = %q, want %q", got, parentToolCallID)
	}
	if got := strings.TrimSpace(conversation.RootConversationID); got != parentConversationID {
		t.Fatalf("child root_conversation_id = %q, want %q", got, parentConversationID)
	}
}

func TestTopLevelRunWithoutParentHeadersKeepsSelfAsRoot(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	defer service.multitaskDelegation.Close()
	intent := InboundIntent{
		Kind:           "run",
		RequestID:      "req-top-level",
		ConversationID: "conv-top-level",
		Mode:           agentv1.AgentMode_AGENT_MODE_AGENT,
		UserMessage:    &agentv1.UserMessage{Text: "hello", MessageId: "message-1"},
	}
	conversation, _, _, _, err := service.bootstrapRuntimeConversation(intent)
	if err != nil {
		t.Fatalf("bootstrapRuntimeConversation() error = %v", err)
	}
	if strings.TrimSpace(conversation.ParentConversationID) != "" {
		t.Fatalf("top-level parent_conversation_id = %q, want empty", conversation.ParentConversationID)
	}
	if got := strings.TrimSpace(conversation.RootConversationID); got != "conv-top-level" {
		t.Fatalf("top-level root_conversation_id = %q", got)
	}
}

func TestMirrorNativeChildInteractionReachesParentTaskBubble(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	defer service.multitaskDelegation.Close()
	parentRequestID := "req-parent-mirror"
	parentConversationID := "conv-parent-mirror"
	parentToolCallID := "call-parent-mirror"
	childRequestID := "req-child-mirror"
	childConversationID := "conv-child-mirror"

	registerParentTaskDelegation(t, service, parentRequestID, parentConversationID, parentToolCallID)
	service.rememberChildParentLink(childRequestID, childRunSSEHeader(parentRequestID, parentToolCallID))

	child, err := service.broker.OpenStream(childRequestID, childConversationID, 1, "model-id", "Model Name",
		agentv1.AgentMode_AGENT_MODE_AGENT, "inspect routes")
	if err != nil {
		t.Fatal(err)
	}
	child.mu.Lock()
	child.CheckpointConversation = &ConversationFile{
		ConversationID:       childConversationID,
		RootConversationID:   parentConversationID,
		ParentConversationID: parentConversationID,
		ParentToolCallID:     parentToolCallID,
		SubagentTypeName:     "explore",
		Mode:                 "agent",
	}
	child.mu.Unlock()

	before, err := service.broker.ReadFromCursor(parentRequestID, 0)
	if err != nil {
		t.Fatalf("ReadFromCursor() error = %v", err)
	}
	service.mirrorNativeChildInteraction(child, "model-call-1",
		buildThinkingDeltaInteraction("子代理正在检查路由入口", agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT))
	after, err := service.broker.ReadFromCursor(parentRequestID, 0)
	if err != nil {
		t.Fatalf("ReadFromCursor() error = %v", err)
	}
	if len(after) <= len(before) {
		t.Fatalf("parent stream events = %d, want more than %d: child output never reached the Task bubble", len(after), len(before))
	}
}

// progressTimeoutResolver 给出非零的无进展阈值；nilResolver 返回 0 会让所有活动都被判成过期。
type progressTimeoutResolver struct{ nilResolver }

func (progressTimeoutResolver) NativeDelegationProgressTimeout(context.Context) time.Duration {
	return 5 * time.Minute
}

func TestNativeDelegationProgressSeesChildConversationActivity(t *testing.T) {
	service := NewService(t.TempDir(), progressTimeoutResolver{})
	defer service.multitaskDelegation.Close()
	parentRequestID := "req-parent-progress"
	parentConversationID := "conv-parent-progress"
	parentToolCallID := "call-parent-progress"
	childRequestID := "req-child-progress"
	childConversationID := "conv-child-progress"

	registerParentTaskDelegation(t, service, parentRequestID, parentConversationID, parentToolCallID)
	service.rememberChildParentLink(childRequestID, childRunSSEHeader(parentRequestID, parentToolCallID))
	if _, err := service.broker.OpenStream(childRequestID, childConversationID, 1, "model-id", "Model Name",
		agentv1.AgentMode_AGENT_MODE_AGENT, "inspect routes"); err != nil {
		t.Fatal(err)
	}

	// 子代理正在长文本生成：活动只会记在子会话上，父会话完全安静。
	service.markConversationActivity(childConversationID)

	item, ok := service.nativeDelegationTask("exec-parent-task")
	if !ok {
		t.Fatal("native delegation runtime disappeared")
	}
	if !service.markDelegationConversationProgress(item) {
		t.Fatal("child conversation activity must count as subagent progress, otherwise the watchdog times out a working subagent")
	}
}

func TestMirrorNativeChildInteractionFallsBackToHeaderLink(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	defer service.multitaskDelegation.Close()
	parentRequestID := "req-parent-fallback"
	parentConversationID := "conv-parent-fallback"
	parentToolCallID := "call-parent-fallback"
	childRequestID := "req-child-fallback"
	childConversationID := "conv-child-fallback"

	registerParentTaskDelegation(t, service, parentRequestID, parentConversationID, parentToolCallID)
	service.rememberChildParentLink(childRequestID, childRunSSEHeader(parentRequestID, parentToolCallID))

	child, err := service.broker.OpenStream(childRequestID, childConversationID, 1, "model-id", "Model Name",
		agentv1.AgentMode_AGENT_MODE_AGENT, "inspect routes")
	if err != nil {
		t.Fatal(err)
	}
	// 历史遗留会话：state.json 里没有父信息（本次修复之前落盘的 873 个子会话都是这样），
	// 仍然必须靠 RunSSE 头恢复镜像。
	child.mu.Lock()
	child.CheckpointConversation = &ConversationFile{
		ConversationID:   childConversationID,
		SubagentTypeName: "explore",
		Mode:             "agent",
	}
	child.mu.Unlock()

	before, err := service.broker.ReadFromCursor(parentRequestID, 0)
	if err != nil {
		t.Fatalf("ReadFromCursor() error = %v", err)
	}
	service.mirrorNativeChildInteraction(child, "model-call-1",
		buildThinkingDeltaInteraction("子代理正在检查路由入口", agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT))
	after, err := service.broker.ReadFromCursor(parentRequestID, 0)
	if err != nil {
		t.Fatalf("ReadFromCursor() error = %v", err)
	}
	if len(after) <= len(before) {
		t.Fatalf("parent stream events = %d, want more than %d", len(after), len(before))
	}
}
