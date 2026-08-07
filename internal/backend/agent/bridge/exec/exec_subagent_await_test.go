package execbridge

import (
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

// TestDecodeSubagentAwaitArgs 验证 agent_id 必填与 timeout_ms 解析。
func TestDecodeSubagentAwaitArgs(t *testing.T) {
	args, err := decodeSubagentAwaitArgs([]byte(`{"agent_id":"local-delegation:abc","timeout_ms":5000}`))
	if err != nil {
		t.Fatalf("decodeSubagentAwaitArgs() error = %v", err)
	}
	if args.AgentID != "local-delegation:abc" {
		t.Fatalf("AgentID = %q", args.AgentID)
	}
	if args.TimeoutMS != 5000 {
		t.Fatalf("TimeoutMS = %d, want 5000", args.TimeoutMS)
	}
	if _, err := decodeSubagentAwaitArgs([]byte(`{"timeout_ms":100}`)); err == nil {
		t.Fatal("decodeSubagentAwaitArgs() missing agent_id = nil error, want rejection")
	}
	if _, err := decodeSubagentAwaitArgs([]byte(`{"agent_id":"x","timeout_ms":-5}`)); err == nil {
		t.Fatal("decodeSubagentAwaitArgs() negative timeout = nil error, want rejection")
	}
}

// TestOpenSubagentAwait 验证 open 构造出 subagent_await_args 且 exec kind 正确。
func TestOpenSubagentAwait(t *testing.T) {
	bridge := NewBridge()
	serverMessage, pending, err := bridge.OpenExec(OpenExecContext{}, runtimecore.ToolInvocation{
		ToolName: "SubagentAwait",
		CallID:   "call-1",
		ArgsJSON: []byte(`{"agent_id":"local-delegation:abc"}`),
	})
	if err != nil {
		t.Fatalf("OpenExec() error = %v", err)
	}
	if pending.ExecKind != "subagent_await" {
		t.Fatalf("ExecKind = %q, want subagent_await", pending.ExecKind)
	}
	execMessage := serverMessage.GetExecServerMessage()
	if execMessage == nil {
		t.Fatal("exec server message missing")
	}
	args := execMessage.GetSubagentAwaitArgs()
	if args == nil {
		t.Fatal("subagent_await_args missing")
	}
	if args.GetAgentId() != "local-delegation:abc" {
		t.Fatalf("AgentId = %q", args.GetAgentId())
	}
	if args.GetTimeoutMs() != 30000 {
		t.Fatalf("TimeoutMs = %d, want default 30000", args.GetTimeoutMs())
	}
}

// TestApplySubagentAwaitStillRunning 验证 still_running 不是终态。
func TestApplySubagentAwaitStillRunning(t *testing.T) {
	bridge := NewBridge()
	result, err := bridge.ApplyExecClientMessage(&agentv1.ExecClientMessage{
		Id:     1,
		ExecId: "exec-subagent-await-1",
		Message: &agentv1.ExecClientMessage_SubagentAwaitResult{
			SubagentAwaitResult: &agentv1.SubagentAwaitResult{
				Result: &agentv1.SubagentAwaitResult_StillRunning{
					StillRunning: &agentv1.SubagentAwaitStillRunning{AgentId: "local-delegation:abc"},
				},
			},
		},
	}, runtimecore.PendingExec{ToolCallID: "call-1", ExecKind: "subagent_await"})
	if err != nil {
		t.Fatalf("ApplyExecClientMessage() error = %v", err)
	}
	if result.IsTerminal {
		t.Fatal("IsTerminal = true for still_running, want false")
	}
	if result.ToolResultPayload == "" {
		t.Fatal("ToolResultPayload empty for still_running")
	}
}

// TestApplySubagentAwaitComplete 验证 complete 是终态。
func TestApplySubagentAwaitComplete(t *testing.T) {
	bridge := NewBridge()
	result, err := bridge.ApplyExecClientMessage(&agentv1.ExecClientMessage{
		Id:     1,
		ExecId: "exec-subagent-await-1",
		Message: &agentv1.ExecClientMessage_SubagentAwaitResult{
			SubagentAwaitResult: &agentv1.SubagentAwaitResult{
				Result: &agentv1.SubagentAwaitResult_Complete{
					Complete: &agentv1.SubagentAwaitComplete{AgentId: "local-delegation:abc", FinalMessage: stringPtr("done")},
				},
			},
		},
	}, runtimecore.PendingExec{ToolCallID: "call-1", ExecKind: "subagent_await"})
	if err != nil {
		t.Fatalf("ApplyExecClientMessage() error = %v", err)
	}
	if !result.IsTerminal {
		t.Fatal("IsTerminal = false for complete, want true")
	}
	if result.ToolResultPayload != "done" {
		t.Fatalf("ToolResultPayload = %q, want done", result.ToolResultPayload)
	}
}

// TestConvertSubagentAwaitResult 验证终态映射为 SubagentResult 语义。
func TestConvertSubagentAwaitResult(t *testing.T) {
	// still_running → nil
	if got := ConvertSubagentAwaitResult(&agentv1.SubagentAwaitResult{
		Result: &agentv1.SubagentAwaitResult_StillRunning{StillRunning: &agentv1.SubagentAwaitStillRunning{AgentId: "a"}},
	}); got != nil {
		t.Fatalf("still_running converted to %v, want nil", got)
	}
	// complete → success
	got := ConvertSubagentAwaitResult(&agentv1.SubagentAwaitResult{
		Result: &agentv1.SubagentAwaitResult_Complete{Complete: &agentv1.SubagentAwaitComplete{AgentId: "a", FinalMessage: stringPtr("ok")}},
	})
	if got == nil || got.GetSuccess() == nil {
		t.Fatalf("complete not mapped to success: %v", got)
	}
	// not_found → error
	got = ConvertSubagentAwaitResult(&agentv1.SubagentAwaitResult{
		Result: &agentv1.SubagentAwaitResult_NotFound{NotFound: &agentv1.SubagentAwaitNotFound{AgentId: "a"}},
	})
	if got == nil || got.GetError() == nil || got.GetError().GetError() != "subagent not found" {
		t.Fatalf("not_found not mapped to error: %v", got)
	}
}
