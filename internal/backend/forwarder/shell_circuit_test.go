package forwarder

import (
	"fmt"
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func TestShouldOpenShellCircuit(t *testing.T) {
	tests := []struct {
		name            string
		state           shellCircuitState
		rejectionClass  string
		wantOpenCircuit bool
	}{
		{name: "capability rejection", rejectionClass: "capability", wantOpenCircuit: true},
		{name: "permission rejection", rejectionClass: "permission", wantOpenCircuit: true},
		{name: "first parser rejection", rejectionClass: "command_parse"},
		{name: "second parser rejection remains metadata", state: shellCircuitState{ParseRejections: 1}, rejectionClass: "command_parse"},
		{name: "already open", state: shellCircuitState{Open: true}, rejectionClass: "capability"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldOpenShellCircuit(tt.state, tt.rejectionClass); got != tt.wantOpenCircuit {
				t.Fatalf("shouldOpenShellCircuit() = %t, want %t", got, tt.wantOpenCircuit)
			}
		})
	}
}

func TestShellTerminalRejectionClassifiesSkippedStream(t *testing.T) {
	message := &agentv1.ExecClientMessage{
		Message: &agentv1.ExecClientMessage_ShellStream{
			ShellStream: &agentv1.ShellStream{
				Event: &agentv1.ShellStream_Rejected{
					Rejected: &agentv1.ShellRejected{Reason: "Skipped by Cursor"},
				},
			},
		},
	}
	reason, class := shellTerminalRejection(message)
	if reason != "Skipped by Cursor" || class != "capability" {
		t.Fatalf("shellTerminalRejection() = (%q, %q), want skipped capability rejection", reason, class)
	}
}

func TestCurrentTurnShellCircuitIgnoresTransportStall(t *testing.T) {
	const turnSeq = int64(7)
	stream := &ActiveStream{
		TurnSeq: turnSeq,
		CheckpointConversation: &ConversationFile{Entries: []HistoryEntry{
			newMetadataEntry(turnSeq, "request", "shell_stream_stalled", map[string]any{
				"reason": "transport_closed",
			}),
		}},
	}
	if circuit := currentTurnShellCircuit(stream); circuit.Open {
		t.Fatal("transport-only shell stall opened the rejection circuit")
	}
}

func TestPreDispatchShellRejectionOpensCircuitOnRepeat(t *testing.T) {
	// 复现实际观测到的空转：inspect 子代理同一命令因确定性校验错误被反复拒绝。
	// 第 1 次仅记账，第 2 次同指纹开路，第 3 次起由 handleToolInvocation 的 circuit.Open 分支拦截。
	broker := NewStreamBroker()
	stream, err := broker.OpenStream("request", "conversation", 1, "model", "model", agentv1.AgentMode_AGENT_MODE_PLAN, "test")
	if err != nil {
		t.Fatal(err)
	}
	stream.CheckpointConversation = &ConversationFile{ConversationID: "conversation", Mode: "plan", NextTurnSeq: 2, NextEntrySeq: 1}
	service := &Service{
		broker:    broker,
		projector: NewHistoryProjector(),
		debug:     newDebugRecorder("", broker, nil),
	}
	invocation := runtimecore.ToolInvocation{
		CallID:   "tool-1",
		ToolName: "Shell",
		ArgsJSON: []byte(`{"command":"git push origin main","working_directory":"E:\\repo"}`),
	}
	cause := fmt.Errorf("inspect Shell git subcommand %q is not read-only", "push")

	opened, err := service.recordPreDispatchShellRejection(stream, invocation, cause)
	if err != nil {
		t.Fatal(err)
	}
	if opened {
		t.Fatal("first deterministic rejection must not open the circuit")
	}
	if circuit := currentTurnShellCircuit(stream); circuit.Open {
		t.Fatal("circuit open after a single rejection")
	}

	// 模型对同一命令仅微调无关参数重试：command/cwd 归一化后指纹一致。
	invocation.CallID = "tool-2"
	invocation.ArgsJSON = []byte(`{"command":"git  push origin main","working_directory":"e:/repo","description":"retry"}`)
	opened, err = service.recordPreDispatchShellRejection(stream, invocation, cause)
	if err != nil {
		t.Fatal(err)
	}
	if !opened {
		t.Fatal("second identical-fingerprint rejection must open the circuit")
	}
	circuit := currentTurnShellCircuit(stream)
	if !circuit.Open {
		t.Fatal("event-sourced circuit state did not reflect the open")
	}
	if circuit.RejectionClass != "policy" {
		t.Fatalf("rejection class = %q, want policy", circuit.RejectionClass)
	}
}