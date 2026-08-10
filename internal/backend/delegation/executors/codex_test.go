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

func TestCodexCLIRegistrationUsesInstalledJSONLContract(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{
		{result: delegation.ProcessResult{Stdout: "codex-cli 0.147.0\n", ExecutablePath: "C:/tools/codex.exe"}},
		{result: delegation.ProcessResult{Stdout: "Logged in using an API key\n"}},
		{result: delegation.ProcessResult{Stdout: strings.Join([]string{
			`{"type":"thread.started","thread_id":"thread-1"}`,
			`{"type":"item.completed","item":{"id":"item-1","type":"agent_message","text":"正在检查工作区"}}`,
			`{"type":"item.completed","item":{"id":"item-2","type":"command_execution","command":"rg --files","status":"completed"}}`,
			`{"type":"turn.completed","usage":{"input_tokens":19,"cached_input_tokens":4,"output_tokens":7,"reasoning_output_tokens":2}}`,
		}, "\n") + "\n"}},
	}}
	registration, err := NewCodexCLIRegistration(runner, delegation.RuntimeExecutorConfig{
		ID: CodexCLIExecutorID, Enabled: true, Priority: 5, Executable: "C:/tools/codex.exe",
		ProbeTimeoutSeconds: 4, ExecutionTimeoutSeconds: 30, EnvironmentVariables: []string{"OPENAI_API_KEY"},
	})
	if err != nil {
		t.Fatalf("NewCodexCLIRegistration() error = %v", err)
	}
	probe, err := registration.Probe(t.Context())
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if probe.State != delegation.ExecutorProbeReady || probe.AuthState != delegation.ExecutorAuthReady || probe.Version != "codex-cli 0.147.0" {
		t.Fatalf("Probe() = %#v", probe)
	}
	updates := []string{}
	ctx := delegation.WithWorkerVisibleUpdatePublisher(t.Context(), func(value string) bool { updates = append(updates, value); return true })
	result := registration.Execute(ctx, delegation.TaskRequest{Prompt: "Inspect", WorkspaceHint: "E:/workspace", Timeout: 20 * time.Second, Readonly: true})
	if result.Error != nil {
		t.Fatalf("Execute() error = %v", result.Error)
	}
	if result.Output != "正在检查工作区" || result.ToolCallCount != 1 || !reflect.DeepEqual(updates, []string{"正在检查工作区"}) {
		t.Fatalf("result=%#v updates=%#v", result, updates)
	}
	if result.Metadata[CodexMetadataThreadIDKey] != "thread-1" || result.Metadata[CodexMetadataInputTokensKey] != "19" || result.Metadata[CodexMetadataOutputTokensKey] != "7" {
		t.Fatalf("metadata=%#v", result.Metadata)
	}
	wantArgs := []string{"--sandbox", "read-only", "--ask-for-approval", "never", "-C", "E:/workspace", "exec", "--json", "--ephemeral", "--color", "never", "Inspect"}
	if !reflect.DeepEqual(runner.requests[2].Args, wantArgs) || runner.requests[2].Dir != "E:/workspace" || runner.requests[2].Timeout != 20*time.Second {
		t.Fatalf("request=%#v", runner.requests[2])
	}
}

func TestCodexCLIWritableExecutionUsesWorkspaceSandbox(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
		result: delegation.ProcessResult{Stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"done"}}` + "\n" + `{"type":"turn.completed"}`},
	}}}
	registration, _ := NewCodexCLIRegistration(runner, delegation.RuntimeExecutorConfig{ID: CodexCLIExecutorID, Enabled: true, Executable: "codex.exe"})
	result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "edit", WorkspaceHint: t.TempDir()})
	if result.Error != nil {
		t.Fatalf("Execute() error = %v", result.Error)
	}
	if !reflect.DeepEqual(runner.requests[0].Args[:2], []string{"--sandbox", "workspace-write"}) {
		t.Fatalf("args=%#v", runner.requests[0].Args)
	}
}

func TestCodexCLIProbeReportsNotInstalledWithoutApplicationError(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
		err: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, delegation.ProcessErrorCodeNotFound, errors.New("missing")),
	}}}
	registration, _ := NewCodexCLIRegistration(runner, delegation.RuntimeExecutorConfig{ID: CodexCLIExecutorID, Executable: "missing-codex"})
	probe, err := registration.Probe(t.Context())
	if err != nil || probe.State != delegation.ExecutorProbeNotInstalled || probe.Installed {
		t.Fatalf("probe=%#v err=%v", probe, err)
	}
}

func TestCodexCLIProbeRejectsUnknownAuthenticationOutput(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{
		{result: delegation.ProcessResult{Stdout: "codex-cli 0.147.0", ExecutablePath: "codex.exe"}},
		{result: delegation.ProcessResult{Stdout: "authentication state unavailable", ExecutablePath: "codex.exe"}},
	}}
	registration, _ := NewCodexCLIRegistration(runner, delegation.RuntimeExecutorConfig{ID: CodexCLIExecutorID, Executable: "codex.exe"})
	probe, err := registration.Probe(t.Context())
	if err == nil || probe.State != delegation.ExecutorProbeUnhealthy || probe.AuthState != delegation.ExecutorAuthUnknown {
		t.Fatalf("probe=%#v err=%v", probe, err)
	}
}

func TestCodexCLIExecutionClassifiesFailures(t *testing.T) {
	tests := []struct {
		name, stdout, stderr string
		processErr           error
		class                delegation.ExecutorFailureClass
		safe                 bool
		code                 string
	}{
		{name: "login", stdout: `{"type":"error","message":"Not logged in. Run codex login."}`, class: delegation.ExecutorFailureUserActionRequired, code: CodexErrorCodeAuthRequired},
		{name: "approval", stdout: `{"type":"turn.failed","error":{"message":"Approval required by policy"}}`, class: delegation.ExecutorFailureUserActionRequired, code: CodexErrorCodeApprovalRequired},
		{name: "sandbox", stderr: "sandbox policy denied filesystem access", processErr: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, delegation.ProcessErrorCodeExitFailed, errors.New("exit 1")), class: delegation.ExecutorFailureUserActionRequired, code: CodexErrorCodeSandboxRequired},
		{name: "rate", stdout: `{"type":"error","message":"429 rate limit exceeded"}`, class: delegation.ExecutorFailureSwitchable, safe: true, code: CodexErrorCodeRateLimited},
		{name: "crash", stderr: "codex crashed", processErr: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, delegation.ProcessErrorCodeExitFailed, errors.New("exit 17")), class: delegation.ExecutorFailureSwitchable, safe: true, code: CodexErrorCodeProcessFailed},
		{name: "malformed", stdout: "not-json\n", class: delegation.ExecutorFailureSwitchable, safe: true, code: CodexErrorCodeStreamInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
				result: delegation.ProcessResult{Stdout: tc.stdout, Stderr: tc.stderr},
				err:    tc.processErr,
			}}}
			registration, _ := NewCodexCLIRegistration(runner, delegation.RuntimeExecutorConfig{ID: CodexCLIExecutorID, Enabled: true, Executable: "codex.exe"})
			result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "test", WorkspaceHint: t.TempDir()})
			class, safe := delegation.ExecutorErrorClassification(result.Error)
			if result.Error == nil || class != tc.class || safe != tc.safe || executorErrorCodeForTest(result.Error) != tc.code {
				t.Fatalf("error=%v class=%s safe=%t code=%s", result.Error, class, safe, executorErrorCodeForTest(result.Error))
			}
		})
	}
}

func TestCodexCLIVisibleUpdatesAreBoundedAndCancellationPreserved(t *testing.T) {
	long := strings.Repeat("界", CodexVisibleUpdateLimit+20)
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
		result: delegation.ProcessResult{Stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"` + long + `"}}` + "\n" + `{"type":"turn.completed"}`},
	}}}
	registration, _ := NewCodexCLIRegistration(runner, delegation.RuntimeExecutorConfig{ID: CodexCLIExecutorID, Enabled: true, Executable: "codex.exe"})
	updates := []string{}
	ctx := delegation.WithWorkerVisibleUpdatePublisher(t.Context(), func(value string) bool { updates = append(updates, value); return true })
	if result := registration.Execute(ctx, delegation.TaskRequest{Prompt: "test", WorkspaceHint: t.TempDir()}); result.Error != nil {
		t.Fatalf("Execute() error=%v", result.Error)
	}
	if len(updates) != 1 || len([]rune(updates[0])) != CodexVisibleUpdateLimit {
		t.Fatalf("updates=%#v", updates)
	}
	cancelRunner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
		err: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureTerminal, false, delegation.ProcessErrorCodeCanceled, context.Canceled),
	}}}
	cancelRegistration, _ := NewCodexCLIRegistration(cancelRunner, delegation.RuntimeExecutorConfig{ID: CodexCLIExecutorID, Enabled: true, Executable: "codex.exe"})
	result := cancelRegistration.Execute(t.Context(), delegation.TaskRequest{Prompt: "test", WorkspaceHint: t.TempDir()})
	if !errors.Is(result.Error, context.Canceled) || executorErrorCodeForTest(result.Error) != delegation.ProcessErrorCodeCanceled {
		t.Fatalf("cancel error=%v", result.Error)
	}
}
