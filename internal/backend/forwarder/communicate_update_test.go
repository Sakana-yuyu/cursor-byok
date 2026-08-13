// communicate_update_test.go 验证 UpdateCurrentStep（ToolCall.communicate_update_tool_call）：
// - 仅对子会话（Task 子代理）暴露，顶层会话不暴露，因此顶层 tools 前缀不变
// - 参数解析支持 snake_case 与 camelCase 别名
// - started/completed ToolCall 落在 communicate_update_tool_call arm
// - 工具非终结：调用后模型继续，不标记 ProviderTerminalToolInvocation
// - checkpoint 投影把进度写进子会话自身的
//   communicate_update_states_by_parent_tool_call_id[parent_tool_call_id]
package forwarder

import (
	"strings"
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func TestUpdateCurrentStepIsExposedOnlyToChildConversations(t *testing.T) {
	if isToolAllowedInMode(agentv1.AgentMode_AGENT_MODE_AGENT, "", updateCurrentStepToolName) {
		t.Errorf("top-level agent conversation must not expose %q", updateCurrentStepToolName)
	}
	if !isToolAllowedInMode(agentv1.AgentMode_AGENT_MODE_AGENT, "generalPurpose", updateCurrentStepToolName) {
		t.Errorf("child conversation must expose %q", updateCurrentStepToolName)
	}

	_, topLevelNames, err := NewToolCatalog().Load(agentv1.AgentMode_AGENT_MODE_AGENT, "")
	if err != nil {
		t.Fatalf("load top-level agent tools: %v", err)
	}
	for _, name := range topLevelNames {
		if name == updateCurrentStepToolName {
			t.Fatalf("top-level agent catalog exposed %q", updateCurrentStepToolName)
		}
	}

	childTools, childNames, err := NewToolCatalog().Load(agentv1.AgentMode_AGENT_MODE_AGENT, "generalPurpose")
	if err != nil {
		t.Fatalf("load child conversation tools: %v", err)
	}
	if len(childTools) != len(childNames) {
		t.Fatalf("child descriptors = %d, names = %d", len(childTools), len(childNames))
	}
	if childNames[len(childNames)-1] != updateCurrentStepToolName {
		t.Fatalf("child catalog tail = %q, want %q appended last so the tools prefix stays stable", childNames[len(childNames)-1], updateCurrentStepToolName)
	}
}

func TestDecodeUpdateCurrentStepArgsAcceptsBothNamingStyles(t *testing.T) {
	snake, err := decodeUpdateCurrentStepArgs([]byte(`{"current_step":" Adding --json CLI flag ","final_summary":"Added the flag.","completed_subtitle":"Added --json CLI output"}`))
	if err != nil {
		t.Fatalf("decode snake_case args: %v", err)
	}
	if got := snake.GetCurrentStep(); got != "Adding --json CLI flag" {
		t.Fatalf("current_step = %q", got)
	}
	if got := snake.GetFinalSummary(); got != "Added the flag." {
		t.Fatalf("final_summary = %q", got)
	}
	if got := snake.GetCompletedSubtitle(); got != "Added --json CLI output" {
		t.Fatalf("completed_subtitle = %q", got)
	}

	camel, err := decodeUpdateCurrentStepArgs([]byte(`{"currentStep":"Running CLI tests","finalSummary":"Done.","completedSubtitle":"Ran CLI tests"}`))
	if err != nil {
		t.Fatalf("decode camelCase args: %v", err)
	}
	if got := camel.GetCurrentStep(); got != "Running CLI tests" {
		t.Fatalf("camelCase current_step = %q", got)
	}
	if got := camel.GetFinalSummary(); got != "Done." {
		t.Fatalf("camelCase final_summary = %q", got)
	}
	if got := camel.GetCompletedSubtitle(); got != "Ran CLI tests" {
		t.Fatalf("camelCase completed_subtitle = %q", got)
	}
}

func TestDecodeUpdateCurrentStepArgsRejectsMissingCurrentStep(t *testing.T) {
	if _, err := decodeUpdateCurrentStepArgs([]byte(`{"final_summary":"x"}`)); err == nil {
		t.Fatal("current_step is required")
	}
	if _, err := decodeUpdateCurrentStepArgs([]byte(`{not-json`)); err == nil {
		t.Fatal("invalid json must return an error")
	}
}

func TestBuildStartedToolCallUpdateCurrentStep(t *testing.T) {
	toolCall := buildStartedToolCall(runtimecore.ToolInvocation{
		CallID:   "call-step-1",
		ToolName: updateCurrentStepToolName,
		ArgsJSON: []byte(`{"current_step":"Finding invoice_tool modules"}`),
	})
	wrapped, ok := toolCall.GetTool().(*agentv1.ToolCall_CommunicateUpdateToolCall)
	if !ok || wrapped == nil || wrapped.CommunicateUpdateToolCall == nil {
		t.Fatalf("expected ToolCall_CommunicateUpdateToolCall, got %T", toolCall.GetTool())
	}
	if got := wrapped.CommunicateUpdateToolCall.GetArgs().GetCurrentStep(); got != "Finding invoice_tool modules" {
		t.Fatalf("started current_step = %q", got)
	}
}

func newUpdateCurrentStepTestStream(t *testing.T, parentToolCallID string) (*Service, *ActiveStream) {
	t.Helper()
	broker := NewStreamBroker()
	service := &Service{
		broker:    broker,
		projector: NewHistoryProjector(),
		debug:     newDebugRecorder("", broker, nil),
	}
	stream, err := broker.OpenStream(
		"request-communicate-update",
		"conversation-child",
		1,
		"model",
		"model",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"翻译 README",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = &ConversationFile{
		ConversationID:   "conversation-child",
		ParentToolCallID: parentToolCallID,
		SubagentTypeName: "generalPurpose",
		Mode:             "agent",
		NextTurnSeq:      2,
		NextEntrySeq:     1,
	}
	return service, stream
}

func TestHandleUpdateCurrentStepToolInvocationIsNotTerminal(t *testing.T) {
	service, stream := newUpdateCurrentStepTestStream(t, "call-parent-1\nfc_parent_1")

	invocation := runtimecore.ToolInvocation{
		CallID:   "call-step-1",
		ToolName: updateCurrentStepToolName,
		ArgsJSON: []byte(`{"current_step":"Finding primary README"}`),
	}
	if err := service.handleUpdateCurrentStepToolInvocation(stream, invocation); err != nil {
		t.Fatalf("handleUpdateCurrentStepToolInvocation() error = %v", err)
	}

	stream.mu.Lock()
	terminal := stream.ProviderTerminalToolInvocation
	stream.mu.Unlock()
	if terminal {
		t.Fatal("UpdateCurrentStep is a progress tool and must not terminate the provider pass")
	}

	conversation := stream.CheckpointConversation
	entry := conversation.Entries[len(conversation.Entries)-1]
	if entry.Kind != "tool_result" {
		t.Fatalf("last entry kind = %q, want tool_result", entry.Kind)
	}
}

func TestUpdateCurrentStepResultPayloadLeadsWithRecordedStep(t *testing.T) {
	result, payload := buildUpdateCurrentStepSuccessResult("Running CLI tests", 48)
	if got := result.GetSuccess().GetCurrentStep(); got != "Running CLI tests" {
		t.Fatalf("result current_step = %q", got)
	}
	if got := result.GetSuccess().GetMessageIndex(); got != 48 {
		t.Fatalf("result message_index = %d", got)
	}
	// 面向模型的输出整体截断砍尾部，状态必须落在开头。
	if !strings.HasPrefix(payload, "progress recorded") {
		t.Fatalf("payload must lead with the status line, got %q", payload)
	}
	if !strings.Contains(payload, "Running CLI tests") {
		t.Fatalf("payload must echo the recorded step, got %q", payload)
	}
}

func TestCheckpointProjectionCarriesChildCommunicateUpdateState(t *testing.T) {
	service, stream := newUpdateCurrentStepTestStream(t, "call-parent-1\nfc_parent_1")

	steps := []struct {
		callID string
		args   string
	}{
		{"call-step-1", `{"current_step":"Exploring CLI structure"}`},
		{"call-step-2", `{"current_step":"Adding --json CLI flag"}`},
		{"call-step-3", `{"current_step":"Ran CLI tests","final_summary":"Added a --json flag; all 24 tests passed.","completed_subtitle":"Added --json CLI output"}`},
	}
	for _, step := range steps {
		if err := service.handleUpdateCurrentStepToolInvocation(stream, runtimecore.ToolInvocation{
			CallID:   step.callID,
			ToolName: updateCurrentStepToolName,
			ArgsJSON: []byte(step.args),
		}); err != nil {
			t.Fatalf("handleUpdateCurrentStepToolInvocation(%s) error = %v", step.callID, err)
		}
	}

	state, err := NewHistoryProjector().ProjectLegacyCheckpoint(stream.CheckpointConversation)
	if err != nil {
		t.Fatalf("ProjectLegacyCheckpoint() error = %v", err)
	}
	// 官方服务端把 map key 里的换行剥掉，客户端两种写法都查得到。
	turnState := state.GetCommunicateUpdateStatesByParentToolCallId()["call-parent-1fc_parent_1"]
	if turnState == nil {
		t.Fatalf("checkpoint is missing the parent-keyed communicate update state: %#v", state.GetCommunicateUpdateStatesByParentToolCallId())
	}
	if len(turnState.GetHistory()) != len(steps) {
		t.Fatalf("history length = %d, want %d", len(turnState.GetHistory()), len(steps))
	}
	wantSteps := []string{"Exploring CLI structure", "Adding --json CLI flag", "Ran CLI tests"}
	previousIndex := uint32(0)
	for index, historyEntry := range turnState.GetHistory() {
		if historyEntry.GetStep() != wantSteps[index] {
			t.Fatalf("history[%d].step = %q, want %q", index, historyEntry.GetStep(), wantSteps[index])
		}
		// 客户端按 message_index 最大值挑「当前步骤」，必须严格递增。
		if index > 0 && historyEntry.GetMessageIndex() <= previousIndex {
			t.Fatalf("history[%d].message_index = %d is not greater than %d", index, historyEntry.GetMessageIndex(), previousIndex)
		}
		previousIndex = historyEntry.GetMessageIndex()
	}
	if got := turnState.GetCompletedSubtitle(); got != "Added --json CLI output" {
		t.Fatalf("completed_subtitle = %q", got)
	}
	if got := turnState.GetFinalSummary(); got != "Added a --json flag; all 24 tests passed." {
		t.Fatalf("final_summary = %q", got)
	}
}

func TestCheckpointProjectionSkipsCommunicateUpdateStateWithoutParentToolCall(t *testing.T) {
	service, stream := newUpdateCurrentStepTestStream(t, "")
	if err := service.handleUpdateCurrentStepToolInvocation(stream, runtimecore.ToolInvocation{
		CallID:   "call-step-1",
		ToolName: updateCurrentStepToolName,
		ArgsJSON: []byte(`{"current_step":"Exploring CLI structure"}`),
	}); err != nil {
		t.Fatalf("handleUpdateCurrentStepToolInvocation() error = %v", err)
	}
	state, err := NewHistoryProjector().ProjectLegacyCheckpoint(stream.CheckpointConversation)
	if err != nil {
		t.Fatalf("ProjectLegacyCheckpoint() error = %v", err)
	}
	if len(state.GetCommunicateUpdateStatesByParentToolCallId()) != 0 {
		t.Fatalf("a conversation without a parent tool call must not emit a parent-keyed state: %#v", state.GetCommunicateUpdateStatesByParentToolCallId())
	}
}

func TestTrimCheckpointForWireKeepsCommunicateUpdateState(t *testing.T) {
	state := &agentv1.ConversationStateStructure{
		CommunicateUpdateStatesByParentToolCallId: map[string]*agentv1.CommunicateUpdateTurnState{
			"call-parent-1fc_parent_1": {
				History: []*agentv1.CommunicateUpdateHistoryEntry{{Step: "Running CLI tests", MessageIndex: 48}},
			},
		},
		RootPromptMessagesJson: [][]byte{make([]byte, 4096)},
	}
	trimCheckpointForWire(state, 128)
	if len(state.GetCommunicateUpdateStatesByParentToolCallId()) == 0 {
		t.Fatal("trimming must keep the live Task-card progress state")
	}
}
