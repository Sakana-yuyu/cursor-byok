package executors

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"cursor/gen/agentv1"
	execbridge "cursor/internal/backend/agent/bridge/exec"
	"cursor/internal/backend/delegation"
)

func TestCursorEditorDetectorResolvesLauncherToRealExecutable(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "resources", "app", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "Cursor.exe")
	if err := os.WriteFile(executable, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(binDir, "cursor.cmd")
	content := "@echo off\r\n\"%~dp0..\\..\\..\\Cursor.exe\" %*\r\n"
	if err := os.WriteFile(launcher, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := cursorExecutableFromLauncher(launcher); got != executable {
		t.Fatalf("cursorExecutableFromLauncher() = %q, want %q", got, executable)
	}
}

func TestCursorCapabilitySeparatesEditorFromAgentExecution(t *testing.T) {
	registration, err := NewCursorRegistration(
		delegation.RuntimeExecutorConfig{ID: CursorExecutorID, Enabled: true},
		func(context.Context) (string, error) { return "D:/cursor/Cursor.exe", nil },
		func(string) bool { return false },
		func(context.Context, delegation.TaskRequest) delegation.TaskResult {
			t.Fatal("editor-only Cursor must not execute")
			return delegation.TaskResult{}
		},
	)
	if err != nil {
		t.Fatalf("NewCursorRegistration() error = %v", err)
	}
	probe, err := registration.Probe(t.Context())
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if !probe.EditorAvailable || probe.AgentExecutionAvailable {
		t.Fatalf("probe = %#v, want editor-only capability", probe)
	}
	if probe.State != delegation.ExecutorProbeActionRequired || probe.DiagnosticCode != CursorDiagnosticEditorOnly {
		t.Fatalf("probe = %#v, want action-required editor-only state", probe)
	}

	registry := delegation.NewExecutorRegistry(delegation.ExecutorRegistryConfig{})
	if err := registry.Register(registration); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := registry.Probe(t.Context(), CursorExecutorID, true); err != nil {
		t.Fatalf("registry Probe() error = %v", err)
	}
	if eligible := registry.Eligible(nil); len(eligible) != 0 {
		t.Fatalf("editor-only Cursor is eligible: %#v", eligible)
	}
}

func TestCursorCapabilityReadyOnlyWithActiveAgentBridge(t *testing.T) {
	agentAvailable := true
	executed := false
	registration, err := NewCursorRegistration(
		delegation.RuntimeExecutorConfig{ID: CursorExecutorID, Enabled: true},
		func(context.Context) (string, error) { return "D:/cursor/Cursor.exe", nil },
		func(parentRequestID string) bool {
			if parentRequestID == "" {
				return agentAvailable
			}
			return agentAvailable && parentRequestID == "parent-1"
		},
		func(_ context.Context, request delegation.TaskRequest) delegation.TaskResult {
			executed = true
			return delegation.TaskResult{Output: request.Prompt}
		},
	)
	if err != nil {
		t.Fatalf("NewCursorRegistration() error = %v", err)
	}
	probe, err := registration.Probe(t.Context())
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if probe.State != delegation.ExecutorProbeReady || !probe.EditorAvailable || !probe.AgentExecutionAvailable {
		t.Fatalf("ready probe = %#v", probe)
	}
	result := registration.Execute(t.Context(), delegation.TaskRequest{ParentRequest: "parent-1", ID: "task-1", Prompt: "ok"})
	if result.Error != nil || result.Output != "ok" || !executed {
		t.Fatalf("Execute() result = %#v, executed=%t", result, executed)
	}
}

func TestCursorExecuteRejectsDisconnectedParentBeforePublishing(t *testing.T) {
	executed := false
	registration, err := NewCursorRegistration(
		delegation.RuntimeExecutorConfig{ID: CursorExecutorID, Enabled: true},
		func(context.Context) (string, error) { return "D:/cursor/Cursor.exe", nil },
		func(parentRequestID string) bool { return parentRequestID == "" },
		func(context.Context, delegation.TaskRequest) delegation.TaskResult {
			executed = true
			return delegation.TaskResult{}
		},
	)
	if err != nil {
		t.Fatalf("NewCursorRegistration() error = %v", err)
	}
	result := registration.Execute(t.Context(), delegation.TaskRequest{ParentRequest: "disconnected", ID: "task-1"})
	if result.Error == nil || executed {
		t.Fatalf("Execute() result = %#v, executed=%t", result, executed)
	}
	class, retrySafe := delegation.ExecutorErrorClassification(result.Error)
	if class != delegation.ExecutorFailureSwitchable || !retrySafe {
		t.Fatalf("classification = %q retrySafe=%t error=%v", class, retrySafe, result.Error)
	}
	var coded interface{ Code() string }
	if !errors.As(result.Error, &coded) || coded.Code() != CursorErrorCodeAgentUnavailable {
		t.Fatalf("error code = %v", result.Error)
	}
}

func TestCursorRegistrationExecutesThroughExistingAdapter(t *testing.T) {
	published := make(chan *agentv1.AgentServerMessage, 1)
	adapter := delegation.NewCursorAdapterWithProgress(
		execbridge.NewBridge(),
		func(_ string, message *agentv1.AgentServerMessage) error {
			published <- message
			return nil
		},
		nil,
	)
	registration, err := NewCursorRegistration(
		delegation.RuntimeExecutorConfig{ID: CursorExecutorID, Enabled: true},
		func(context.Context) (string, error) { return "D:/cursor/Cursor.exe", nil },
		func(string) bool { return true },
		adapter.Execute,
	)
	if err != nil {
		t.Fatalf("NewCursorRegistration() error = %v", err)
	}

	resultCh := make(chan delegation.TaskResult, 1)
	go func() {
		resultCh <- registration.Execute(t.Context(), delegation.TaskRequest{
			ParentRequest:  "parent-1",
			ConversationID: "conversation-1",
			ID:             "task-1",
			SubagentType:   "generalPurpose",
			Prompt:         "inspect",
		})
	}()
	serverMessage := <-published
	execMessage := serverMessage.GetExecServerMessage()
	if execMessage == nil || execMessage.GetSubagentArgs() == nil {
		t.Fatalf("published message = %#v", serverMessage)
	}
	finalMessage := "done"
	clientMessage := &agentv1.ExecClientMessage{
		Id:     execMessage.GetId(),
		ExecId: execMessage.GetExecId(),
		Message: &agentv1.ExecClientMessage_SubagentResult{SubagentResult: &agentv1.SubagentResult{
			Result: &agentv1.SubagentResult_Success{Success: &agentv1.SubagentSuccess{
				AgentId: "child-1", FinalMessage: &finalMessage, ToolCallCount: 2,
			}},
		}},
	}
	if !adapter.ConsumeExecMessage("parent-1", clientMessage) {
		t.Fatal("Cursor adapter did not consume the terminal client message")
	}
	result := <-resultCh
	if result.Error != nil || result.Output != "done" || result.ToolCallCount != 2 {
		t.Fatalf("Execute() result = %#v", result)
	}
}
