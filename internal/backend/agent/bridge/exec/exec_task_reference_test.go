package execbridge

import (
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func TestApplyNativeTaskResultKeepsParentToolCallReference(t *testing.T) {
	bridge := NewBridge()
	toolCallID := "task-call-native-1"
	pending := runtimecore.PendingExec{
		ToolCallID: toolCallID,
		ExecKind:   "subagent",
		ArgsJSON:   []byte(`{"subagent_type":"explore","description":"same title","prompt":"inspect"}`),
	}
	finalMessage := "完成检查"
	result, err := bridge.ApplyExecClientMessage(&agentv1.ExecClientMessage{
		Message: &agentv1.ExecClientMessage_SubagentResult{
			SubagentResult: &agentv1.SubagentResult{
				Result: &agentv1.SubagentResult_Success{
					Success: &agentv1.SubagentSuccess{
						AgentId:      "cursor-child-id-that-must-not-replace-parent-reference",
						FinalMessage: &finalMessage,
					},
				},
			},
		},
	}, pending)
	if err != nil {
		t.Fatalf("ApplyExecClientMessage() error = %v", err)
	}
	if result.ToolCall == nil {
		t.Fatal("ToolCall is nil")
	}
	wantAgentID := "local-delegation:" + toolCallID
	if got := result.ToolCall.GetTaskToolCall().GetArgs().GetAgentId(); got != wantAgentID {
		t.Fatalf("completed Task agent_id = %q, want %q", got, wantAgentID)
	}
}
