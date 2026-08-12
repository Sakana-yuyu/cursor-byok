package forwarder

import (
	"testing"

	agentv1 "cursor/gen/agentv1"
)

func TestBuildShellToolCallDeltaMessageMapsOutput(t *testing.T) {
	testCases := []struct {
		name       string
		output     *agentv1.ShellOutputDeltaUpdate
		wantStdout string
		wantStderr string
	}{
		{
			name: "stdout",
			output: &agentv1.ShellOutputDeltaUpdate{
				Event: &agentv1.ShellOutputDeltaUpdate_Stdout{
					Stdout: &agentv1.ShellStreamStdout{Data: "standard output"},
				},
			},
			wantStdout: "standard output",
		},
		{
			name: "stderr",
			output: &agentv1.ShellOutputDeltaUpdate{
				Event: &agentv1.ShellOutputDeltaUpdate_Stderr{
					Stderr: &agentv1.ShellStreamStderr{Data: "standard error"},
				},
			},
			wantStderr: "standard error",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			message := buildShellToolCallDeltaMessage("call-1", "model-call-1", testCase.output)
			if message == nil {
				t.Fatal("shell tool call delta message is nil")
			}
			delta := message.GetInteractionUpdate().GetToolCallDelta()
			if delta == nil {
				t.Fatal("tool call delta is nil")
			}
			if got := delta.GetCallId(); got != "call-1" {
				t.Fatalf("call id = %q, want call-1", got)
			}
			if got := delta.GetModelCallId(); got != "model-call-1" {
				t.Fatalf("model call id = %q, want model-call-1", got)
			}
			shellDelta := delta.GetToolCallDelta().GetShellToolCallDelta()
			if shellDelta == nil {
				t.Fatal("shell tool call delta is nil")
			}
			if got := shellDelta.GetStdout().GetContent(); got != testCase.wantStdout {
				t.Fatalf("stdout = %q, want %q", got, testCase.wantStdout)
			}
			if got := shellDelta.GetStderr().GetContent(); got != testCase.wantStderr {
				t.Fatalf("stderr = %q, want %q", got, testCase.wantStderr)
			}
		})
	}
}

func TestBuildShellToolCallDeltaMessageIgnoresNonContentEvents(t *testing.T) {
	testCases := []*agentv1.ShellOutputDeltaUpdate{
		nil,
		{Event: &agentv1.ShellOutputDeltaUpdate_Start{Start: &agentv1.ShellStreamStart{}}},
		{Event: &agentv1.ShellOutputDeltaUpdate_Exit{Exit: &agentv1.ShellStreamExit{}}},
		{Event: &agentv1.ShellOutputDeltaUpdate_Stdout{Stdout: &agentv1.ShellStreamStdout{}}},
		{Event: &agentv1.ShellOutputDeltaUpdate_Stderr{Stderr: &agentv1.ShellStreamStderr{}}},
	}

	for _, output := range testCases {
		if message := buildShellToolCallDeltaMessage("call-1", "model-call-1", output); message != nil {
			t.Fatalf("message = %#v, want nil", message)
		}
	}
}
