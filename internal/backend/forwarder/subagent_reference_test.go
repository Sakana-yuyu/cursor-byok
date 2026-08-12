package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func TestSubagentReferenceIdentityStaysBoundToToolCall(t *testing.T) {
	cases := []struct {
		name     string
		toolCall string
	}{
		{name: "first parallel task", toolCall: "task-call-1"},
		{name: "second parallel task", toolCall: "task-call-2"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invocation := runtimecore.ToolInvocation{
				ToolName: "Task",
				CallID:   tc.toolCall,
				ArgsJSON: []byte(`{"description":"same title","prompt":"independent work"}`),
			}
			started := buildStartedToolCall(invocation)
			wantID := delegationSubagentID(tc.toolCall)
			if got := started.GetTaskToolCall().GetArgs().GetAgentId(); got != wantID {
				t.Fatalf("started agent_id = %q, want %q", got, wantID)
			}

			stream := &ActiveStream{
				PendingExecs: map[string]runtimecore.PendingExec{
					"exec-" + tc.toolCall: {
						ExecID:     "exec-" + tc.toolCall,
						ExecKind:   "delegation_aggregate",
						ToolCallID: tc.toolCall,
					},
				},
				DelegationRunProgress:  map[string]string{},
				DelegationRunTerminals: map[string]*agentv1.SubagentRunState{},
			}
			state := &agentv1.ConversationStateStructure{}
			attachDelegationRunStates(stream, state)
			run := state.GetSubagentRunsByParentToolCallId()[tc.toolCall]
			if run == nil {
				t.Fatalf("missing run for parent tool call %q", tc.toolCall)
			}
			if got := run.GetSubagentId(); got != wantID {
				t.Fatalf("checkpoint subagent_id = %q, want %q", got, wantID)
			}

			completed := buildDelegationCompletedTaskToolCall(invocation.ArgsJSON, `{"status":"completed"}`, wantID, 1)
			if got := completed.GetTaskToolCall().GetResult().GetSuccess().GetAgentId(); got != wantID {
				t.Fatalf("completed agent_id = %q, want %q", got, wantID)
			}
		})
	}
}
