// send_final_summary_test.go 验证 send_final_summary 终结工具处理：
// - 工具被识别为本地即时终结工具（isImmediateNativeTool）
// - 参数解析 final_summary 文本
// - 调用后发布最终总结文本并标记终结工具调用（不再 resume 死循环）
package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func TestIsImmediateNativeToolSendFinalSummary(t *testing.T) {
	if !isImmediateNativeTool("send_final_summary") {
		t.Fatal("send_final_summary must be treated as an immediate native tool")
	}
}

func TestDecodeSendFinalSummaryArgs(t *testing.T) {
	args, err := decodeSendFinalSummaryArgs([]byte(`{"final_summary":"任务完成，全部测试通过。"}`))
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if got := args.GetFinalSummary(); got != "任务完成，全部测试通过。" {
		t.Fatalf("final_summary = %q", got)
	}

	// 空参数应返回空结构而不报错。
	empty, err := decodeSendFinalSummaryArgs(nil)
	if err != nil {
		t.Fatalf("empty args decode error: %v", err)
	}
	if empty.GetFinalSummary() != "" {
		t.Fatalf("empty final_summary = %q", empty.GetFinalSummary())
	}
}

func TestDecodeSendFinalSummaryArgsInvalidJSON(t *testing.T) {
	if _, err := decodeSendFinalSummaryArgs([]byte(`{not-json`)); err == nil {
		t.Fatal("invalid json must return an error")
	}
}

func TestBuildSendFinalSummaryToolCall(t *testing.T) {
	args := &agentv1.SendFinalSummaryArgs{FinalSummary: stringPtr("done")}
	result := &agentv1.SendFinalSummaryResult{
		Result: &agentv1.SendFinalSummaryResult_Success{
			Success: &agentv1.SendFinalSummarySuccess{FinalSummary: "done"},
		},
	}
	toolCall := buildSendFinalSummaryToolCall(args, result)
	wrapped, ok := toolCall.GetTool().(*agentv1.ToolCall_SendFinalSummaryToolCall)
	if !ok || wrapped == nil || wrapped.SendFinalSummaryToolCall == nil {
		t.Fatalf("expected ToolCall_SendFinalSummaryToolCall, got %T", toolCall.GetTool())
	}
	if got := wrapped.SendFinalSummaryToolCall.GetArgs().GetFinalSummary(); got != "done" {
		t.Fatalf("args final_summary = %q", got)
	}
	if got := wrapped.SendFinalSummaryToolCall.GetResult().GetSuccess().GetFinalSummary(); got != "done" {
		t.Fatalf("result final_summary = %q", got)
	}
}

func TestBuildStartedToolCallSendFinalSummary(t *testing.T) {
	invocation := runtimecore.ToolInvocation{
		ToolName: "send_final_summary",
		ArgsJSON: []byte(`{"final_summary":"总结文本"}`),
	}
	toolCall := buildStartedToolCall(invocation)
	wrapped, ok := toolCall.GetTool().(*agentv1.ToolCall_SendFinalSummaryToolCall)
	if !ok || wrapped == nil || wrapped.SendFinalSummaryToolCall == nil {
		t.Fatalf("expected started ToolCall_SendFinalSummaryToolCall, got %T", toolCall.GetTool())
	}
	if got := wrapped.SendFinalSummaryToolCall.GetArgs().GetFinalSummary(); got != "总结文本" {
		t.Fatalf("started args final_summary = %q", got)
	}
}

// TestHandleSendFinalSummaryToolInvocation 验证终结工具调用完整流程：
// final_summary 文本被发布为 text delta，且 stream 被标记终结工具调用
// （ProviderTerminalToolInvocation=true），provider pass 收口后不再 resume。
func TestHandleSendFinalSummaryToolInvocation(t *testing.T) {
	broker := NewStreamBroker()
	service := &Service{
		broker:    broker,
		projector: NewHistoryProjector(),
		debug:     newDebugRecorder("", broker, nil),
	}
	stream, err := broker.OpenStream(
		"request-final-summary",
		"conversation-final-summary",
		1,
		"model",
		"model",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"请完成任务",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = &ConversationFile{
		ConversationID: "conversation-final-summary",
		Mode:           "agent",
		NextTurnSeq:    2,
		NextEntrySeq:   1,
	}

	invocation := runtimecore.ToolInvocation{
		CallID:   "call-final-1",
		ToolName: "send_final_summary",
		ArgsJSON: []byte(`{"final_summary":"全部工作完成。"}`),
	}
	if err := service.handleSendFinalSummaryToolInvocation(stream, invocation); err != nil {
		t.Fatalf("handleSendFinalSummaryToolInvocation() error = %v", err)
	}

	stream.mu.Lock()
	terminal := stream.ProviderTerminalToolInvocation
	stream.mu.Unlock()
	if !terminal {
		t.Fatal("ProviderTerminalToolInvocation must be true after send_final_summary")
	}
	if stream.Status == StreamStatusStreaming {
		t.Fatal("stream must not stay in streaming after send_final_summary")
	}

	// 工具调用已完成（历史含 tool_result）。
	conversation := stream.CheckpointConversation
	if conversation == nil {
		t.Fatal("checkpoint conversation is nil")
	}
	entry := conversation.Entries[len(conversation.Entries)-1]
	if entry.Kind != "tool_result" {
		t.Fatalf("last entry kind = %q, want tool_result", entry.Kind)
	}
}
