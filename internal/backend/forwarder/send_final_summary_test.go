// send_final_summary_test.go 验证 send_final_summary 工具处理：
// - 工具被识别为本地即时工具（isImmediateNativeTool）
// - 参数解析 final_summary 文本
// - 调用后只记录摘要工具结果，不冒充最终正文，并继续 provider 生成真正回复
package forwarder

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
)

type sendFinalSummaryContinuationProvider struct {
	mu        sync.Mutex
	passCount int
	done      chan struct{}
	doneOnce  sync.Once
}

func (provider *sendFinalSummaryContinuationProvider) StartStream(_ context.Context, _ ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	provider.mu.Lock()
	provider.passCount++
	pass := provider.passCount
	provider.mu.Unlock()

	if pass == 1 {
		if err := sink(modeladapter.ModelEvent{
			Kind: modeladapter.ModelEventKindToolLikeCompleted,
			ToolInvocation: &runtimecore.ToolInvocation{
				CallID:   "call-final-summary",
				ToolName: "send_final_summary",
				ArgsJSON: []byte(`{"final_summary":"完成队列验收"}`),
			},
		}); err != nil {
			return err
		}
		return sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTurnFinished, FinishReason: "tool_calls"})
	}

	if err := sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "QUEUE-B-DONE"}); err != nil {
		return err
	}
	err := sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTurnFinished, FinishReason: "stop"})
	provider.doneOnce.Do(func() { close(provider.done) })
	return err
}

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

// TestHandleSendFinalSummaryToolInvocation 验证摘要工具调用完整流程：
// final_summary 仅落为工具结果，stream 保持可续写，等待 provider 生成真正的最终正文。
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
	stream.ProviderActive = true
	stream.Status = StreamStatusStreaming

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
	status := stream.Status
	stream.mu.Unlock()
	if terminal {
		t.Fatal("send_final_summary must not terminate before the provider writes the final reply")
	}
	if status != StreamStatusStreaming {
		t.Fatalf("stream status = %q, want streaming", status)
	}

	// 工具调用已完成，但摘要不能作为 assistant_text 冒充用户可见的最终答复。
	conversation := stream.CheckpointConversation
	if conversation == nil {
		t.Fatal("checkpoint conversation is nil")
	}
	foundToolResult := false
	for _, entry := range conversation.Entries {
		switch entry.Kind {
		case "assistant_text":
			t.Fatal("final summary must not be persisted as assistant_text")
		case "tool_result":
			foundToolResult = true
		}
	}
	if !foundToolResult {
		t.Fatal("send_final_summary tool_result is missing")
	}
}

func TestSendFinalSummaryContinuesToVisibleFinalReply(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-final-continuation", "只回复 QUEUE-B-DONE"),
	})
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	provider := &sendFinalSummaryContinuationProvider{done: make(chan struct{})}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), contextProjectionLifecycleCompiler{}, provider, broker)
	stream, err := broker.OpenStream(
		"request-final-continuation",
		persisted.ConversationID,
		1,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"只回复 QUEUE-B-DONE",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(persisted)
	t.Cleanup(func() {
		_ = broker.Cancel(stream.RequestID, "test cleanup")
		stream.mu.Lock()
		done := stream.ActorDone
		stream.mu.Unlock()
		if done != nil {
			select {
			case <-done:
			case <-time.After(time.Second):
			}
		}
	})

	if err := service.requestProviderAction(stream, providerActionStart); err != nil {
		t.Fatalf("requestProviderAction(start) error = %v", err)
	}
	select {
	case <-provider.done:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not continue after send_final_summary")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		stream.mu.Lock()
		passCount := stream.ProviderPassCount
		entries := append([]HistoryEntry(nil), stream.CheckpointConversation.Entries...)
		stream.mu.Unlock()
		foundVisibleReply := false
		for _, entry := range entries {
			if entry.Kind != "assistant_text" {
				continue
			}
			var payload assistantTextPayload
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				t.Fatalf("decode assistant text payload: %v", err)
			}
			if payload.Text == "QUEUE-B-DONE" {
				foundVisibleReply = true
			}
		}
		if passCount == 2 && foundVisibleReply {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("provider continuation did not persist the visible final reply")
}
