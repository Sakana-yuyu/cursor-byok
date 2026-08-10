package executors

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"cursor/internal/backend/delegation"
)

func TestGeminiCLIRegistrationUsesOfficialHeadlessContract(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{
		{result: delegation.ProcessResult{Stdout: "0.56.0", ExecutablePath: "C:/tools/gemini.exe"}},
		{result: delegation.ProcessResult{Stdout: "Usage: gemini --prompt --output-format stream-json --approval-mode"}},
		{result: delegation.ProcessResult{Stdout: strings.Join([]string{
			`{"type":"init","session_id":"session-1","model":"gemini-2.5-pro"}`,
			`{"type":"message","role":"assistant","content":"正在检查","delta":true}`,
			`{"type":"message","role":"assistant","content":"工作区","delta":true}`,
			`{"type":"tool_use","tool_name":"read_file","tool_id":"tool-1"}`,
			`{"type":"result","status":"success","stats":{"input_tokens":12,"output_tokens":4,"cached":3,"tool_calls":1}}`,
		}, "\n") + "\n"}},
	}}
	registration, err := NewGeminiCLIRegistration(runner, delegation.RuntimeExecutorConfig{ID: GeminiCLIExecutorID, Enabled: true, Priority: 6, Executable: "C:/tools/gemini.exe", ProbeTimeoutSeconds: 4, ExecutionTimeoutSeconds: 30, EnvironmentVariables: []string{"GEMINI_API_KEY"}})
	if err != nil {
		t.Fatalf("NewGeminiCLIRegistration() error=%v", err)
	}
	probe, err := registration.Probe(t.Context())
	if err != nil || probe.State != delegation.ExecutorProbeReady || probe.Version != "0.56.0" {
		t.Fatalf("probe=%#v err=%v", probe, err)
	}
	updates := []string{}
	ctx := delegation.WithWorkerVisibleUpdatePublisher(t.Context(), func(value string) bool { updates = append(updates, value); return true })
	result := registration.Execute(ctx, delegation.TaskRequest{Prompt: "Inspect", WorkspaceHint: "E:/workspace", Readonly: true, Timeout: 20 * time.Second})
	if result.Error != nil {
		t.Fatalf("Execute() error=%v", result.Error)
	}
	if result.Output != "正在检查工作区" || result.ToolCallCount != 1 || !reflect.DeepEqual(updates, []string{"正在检查", "工作区"}) {
		t.Fatalf("result=%#v updates=%#v", result, updates)
	}
	if result.Metadata[GeminiMetadataSessionIDKey] != "session-1" || result.Metadata[GeminiMetadataModelKey] != "gemini-2.5-pro" || result.Metadata[GeminiMetadataInputTokensKey] != "12" {
		t.Fatalf("metadata=%#v", result.Metadata)
	}
	want := []string{"-p", "Inspect", "--output-format", "stream-json", "--approval-mode", "plan"}
	if !reflect.DeepEqual(runner.requests[2].Args, want) || runner.requests[2].Dir != "E:/workspace" {
		t.Fatalf("request=%#v", runner.requests[2])
	}
}

func TestGeminiCLIWritableUsesAutoEditAndRejectsIncompatibleHelp(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
		result: delegation.ProcessResult{Stdout: `{"type":"message","role":"assistant","content":"done"}` + "\n" + `{"type":"result","status":"success"}`},
	}}}
	registration, _ := NewGeminiCLIRegistration(runner, delegation.RuntimeExecutorConfig{ID: GeminiCLIExecutorID, Enabled: true, Executable: "gemini"})
	result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "edit", WorkspaceHint: t.TempDir()})
	if result.Error != nil {
		t.Fatalf("Execute() error=%v", result.Error)
	}
	if !reflect.DeepEqual(runner.requests[0].Args[len(runner.requests[0].Args)-2:], []string{"--approval-mode", "auto_edit"}) {
		t.Fatalf("args=%#v", runner.requests[0].Args)
	}
	probeRunner := &claudeScriptedRunner{steps: []claudeRunnerStep{{result: delegation.ProcessResult{Stdout: "0.1.0", ExecutablePath: "gemini"}}, {result: delegation.ProcessResult{Stdout: "Usage: gemini -p"}}}}
	probeRegistration, _ := NewGeminiCLIRegistration(probeRunner, delegation.RuntimeExecutorConfig{ID: GeminiCLIExecutorID, Executable: "gemini"})
	probe, err := probeRegistration.Probe(t.Context())
	if err != nil || probe.State != delegation.ExecutorProbeIncompatible {
		t.Fatalf("probe=%#v err=%v", probe, err)
	}
}

func TestGeminiCLIWarningDoesNotOverrideSuccessfulResult(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{result: delegation.ProcessResult{Stdout: strings.Join([]string{
		`{"type":"error","severity":"warning","message":"optional tool unavailable"}`,
		`{"type":"message","role":"assistant","content":"done"}`,
		`{"type":"result","status":"success"}`,
	}, "\n")}}}}
	registration, _ := NewGeminiCLIRegistration(runner, delegation.RuntimeExecutorConfig{ID: GeminiCLIExecutorID, Enabled: true, Executable: "gemini"})
	result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "test", WorkspaceHint: t.TempDir()})
	if result.Error != nil || result.Output != "done" {
		t.Fatalf("result=%#v", result)
	}
}

func TestGeminiCLIFinalAssistantMessageReplacesAccumulatedDeltas(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{result: delegation.ProcessResult{Stdout: strings.Join([]string{
		`{"type":"message","role":"assistant","content":"work","delta":true}`,
		`{"type":"message","role":"assistant","content":"working"}`,
		`{"type":"result","status":"success"}`,
	}, "\n")}}}}
	registration, _ := NewGeminiCLIRegistration(runner, delegation.RuntimeExecutorConfig{ID: GeminiCLIExecutorID, Enabled: true, Executable: "gemini"})
	result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "test", WorkspaceHint: t.TempDir()})
	if result.Error != nil || result.Output != "working" {
		t.Fatalf("result=%#v", result)
	}
}

func TestGeminiCLIProbeReportsNotInstalledWithoutApplicationError(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
		err: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, delegation.ProcessErrorCodeNotFound, errors.New("missing")),
	}}}
	registration, _ := NewGeminiCLIRegistration(runner, delegation.RuntimeExecutorConfig{ID: GeminiCLIExecutorID, Executable: "missing-gemini"})
	probe, err := registration.Probe(t.Context())
	if err != nil || probe.State != delegation.ExecutorProbeNotInstalled || probe.Installed || !strings.Contains(probe.DiagnosticText, "npm install -g @google/gemini-cli") {
		t.Fatalf("probe=%#v err=%v", probe, err)
	}
}

func TestGeminiCLIExecutionClassifiesFailures(t *testing.T) {
	tests := []struct {
		name, stdout, stderr string
		processErr           error
		class                delegation.ExecutorFailureClass
		safe                 bool
		code                 string
	}{
		{name: "auth", stdout: `{"type":"error","severity":"error","message":"Authentication required. Set GEMINI_API_KEY or sign in."}` + "\n" + `{"type":"result","status":"error"}`, class: delegation.ExecutorFailureUserActionRequired, code: GeminiErrorCodeAuthRequired},
		{name: "rate", stdout: `{"type":"error","severity":"error","message":"429 rate limit exceeded"}` + "\n" + `{"type":"result","status":"error"}`, class: delegation.ExecutorFailureSwitchable, safe: true, code: GeminiErrorCodeRateLimited},
		{name: "crash", stderr: "gemini crashed", processErr: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, delegation.ProcessErrorCodeExitFailed, errors.New("exit 1")), class: delegation.ExecutorFailureSwitchable, safe: true, code: GeminiErrorCodeProcessFailed},
		{name: "malformed", stdout: "not-json\n", class: delegation.ExecutorFailureSwitchable, safe: true, code: GeminiErrorCodeStreamInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
				result: delegation.ProcessResult{Stdout: tc.stdout, Stderr: tc.stderr},
				err:    tc.processErr,
			}}}
			registration, _ := NewGeminiCLIRegistration(runner, delegation.RuntimeExecutorConfig{ID: GeminiCLIExecutorID, Enabled: true, Executable: "gemini"})
			result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "test", WorkspaceHint: t.TempDir()})
			class, safe := delegation.ExecutorErrorClassification(result.Error)
			if result.Error == nil || class != tc.class || safe != tc.safe || executorErrorCodeForTest(result.Error) != tc.code {
				t.Fatalf("error=%v class=%s safe=%t code=%s", result.Error, class, safe, executorErrorCodeForTest(result.Error))
			}
		})
	}
}

func TestGeminiCLIVisibleUpdatesAreBoundedAndCancellationPreserved(t *testing.T) {
	long := strings.Repeat("界", GeminiVisibleUpdateLimit+10)
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
		result: delegation.ProcessResult{Stdout: `{"type":"message","role":"assistant","content":"` + long + `"}` + "\n" + `{"type":"result","status":"success"}`},
	}}}
	registration, _ := NewGeminiCLIRegistration(runner, delegation.RuntimeExecutorConfig{ID: GeminiCLIExecutorID, Enabled: true, Executable: "gemini"})
	updates := []string{}
	ctx := delegation.WithWorkerVisibleUpdatePublisher(t.Context(), func(value string) bool { updates = append(updates, value); return true })
	if result := registration.Execute(ctx, delegation.TaskRequest{Prompt: "test", WorkspaceHint: t.TempDir()}); result.Error != nil {
		t.Fatalf("error=%v", result.Error)
	}
	if len(updates) != 1 || len([]rune(updates[0])) != GeminiVisibleUpdateLimit {
		t.Fatalf("updates=%#v", updates)
	}
	cancelRunner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
		err: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureTerminal, false, delegation.ProcessErrorCodeCanceled, context.Canceled),
	}}}
	cancelRegistration, _ := NewGeminiCLIRegistration(cancelRunner, delegation.RuntimeExecutorConfig{ID: GeminiCLIExecutorID, Enabled: true, Executable: "gemini"})
	result := cancelRegistration.Execute(t.Context(), delegation.TaskRequest{Prompt: "test", WorkspaceHint: t.TempDir()})
	if !errors.Is(result.Error, context.Canceled) {
		t.Fatalf("cancel error=%v", result.Error)
	}
}
