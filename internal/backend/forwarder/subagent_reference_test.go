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

func TestParallelSubagentReferenceRoutingUsesExactParentToolCall(t *testing.T) {
	stream := &ActiveStream{
		PendingExecs: map[string]runtimecore.PendingExec{
			"exec-first":  {ExecID: "exec-first", ExecKind: "subagent", ToolCallID: "tool-first"},
			"exec-second": {ExecID: "exec-second", ExecKind: "subagent", ToolCallID: "tool-second"},
		},
		DelegationRunProgress: map[string]string{},
		DelegationRunTerminals: map[string]*agentv1.SubagentRunState{
			// 第二个任务先完成，标题相同也不能改变 map key 的归属。
			"tool-second": {
				ParentToolCallId: "tool-second",
				SubagentId:       stringPtr(delegationSubagentID("tool-second")),
				Status:           agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_SUCCESS,
				Title:            stringPtr("相同标题"),
			},
			"tool-first": {
				ParentToolCallId: "tool-first",
				SubagentId:       stringPtr(delegationSubagentID("tool-first")),
				Status:           agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_ERROR,
				Title:            stringPtr("相同标题"),
			},
		},
	}

	state := &agentv1.ConversationStateStructure{}
	attachDelegationRunStates(stream, state)
	runs := state.GetSubagentRunsByParentToolCallId()
	if got := runs["tool-first"].GetStatus(); got != agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_ERROR {
		t.Fatalf("tool-first status = %v, want ERROR", got)
	}
	if got := runs["tool-second"].GetStatus(); got != agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_SUCCESS {
		t.Fatalf("tool-second status = %v, want SUCCESS", got)
	}
	if got := runs["tool-first"].GetSubagentId(); got != delegationSubagentID("tool-first") {
		t.Fatalf("tool-first subagent_id = %q", got)
	}
	if got := runs["tool-second"].GetSubagentId(); got != delegationSubagentID("tool-second") {
		t.Fatalf("tool-second subagent_id = %q", got)
	}
	if _, ok := runs["missing-tool"]; ok {
		t.Fatal("missing parent tool call unexpectedly routed to another subagent")
	}
}
