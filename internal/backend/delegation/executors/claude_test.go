package executors

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cursor/internal/backend/delegation"
)

type claudeRunnerStep struct {
	result delegation.ProcessResult
	err    error
}

type claudeScriptedRunner struct {
	mu       sync.Mutex
	steps    []claudeRunnerStep
	requests []delegation.ProcessRequest
}

func (runner *claudeScriptedRunner) Run(_ context.Context, request delegation.ProcessRequest) (delegation.ProcessResult, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.requests = append(runner.requests, request)
	if len(runner.steps) == 0 {
		return delegation.ProcessResult{}, errors.New("unexpected process request")
	}
	step := runner.steps[0]
	runner.steps = runner.steps[1:]
	return step.result, step.err
}

func TestClaudeCodeRegistrationUsesExactNonInteractiveContract(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{
		{result: delegation.ProcessResult{Stdout: "2.1.226 (Claude Code)\n", ExecutablePath: "C:/tools/claude.exe"}},
		{result: delegation.ProcessResult{Stdout: `{"loggedIn":true,"authMethod":"oauth_token","apiProvider":"firstParty"}`}},
		{result: delegation.ProcessResult{Stdout: strings.Join([]string{
			`{"type":"system","subtype":"init","session_id":"session-1"}`,
			`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"private chain"},{"type":"text","text":"正在检查工作区"},{"type":"tool_use","name":"Read"}]}}`,
			`{"type":"result","subtype":"success","is_error":false,"session_id":"session-1","result":"TASK17_OK","total_cost_usd":0.01,"usage":{"input_tokens":12,"output_tokens":3}}`,
		}, "\n") + "\n"}},
	}}
	registration, err := NewClaudeCodeRegistration(runner, delegation.RuntimeExecutorConfig{
		ID:                      ClaudeCodeExecutorID,
		Enabled:                 true,
		Priority:                7,
		Executable:              "C:/tools/claude.exe",
		ProbeTimeoutSeconds:     4,
		ExecutionTimeoutSeconds: 30,
		EnvironmentVariables:    []string{"ANTHROPIC_API_KEY"},
	})
	if err != nil {
		t.Fatalf("NewClaudeCodeRegistration() error = %v", err)
	}
	probe, err := registration.Probe(t.Context())
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if probe.State != delegation.ExecutorProbeReady || probe.AuthState != delegation.ExecutorAuthReady || probe.Version != "2.1.226 (Claude Code)" {
		t.Fatalf("Probe() = %#v", probe)
	}

	updates := []string{}
	ctx := delegation.WithWorkerVisibleUpdatePublisher(t.Context(), func(text string) bool {
		updates = append(updates, text)
		return true
	})
	result := registration.Execute(ctx, delegation.TaskRequest{
		ID:            "task-17",
		Prompt:        "Inspect the repository",
		WorkspaceHint: "E:/workspace",
		Timeout:       20 * time.Second,
	})
	if result.Error != nil {
		t.Fatalf("Execute() error = %v", result.Error)
	}
	if result.Output != "TASK17_OK" || result.ToolCallCount != 1 {
		t.Fatalf("Execute() result = %#v", result)
	}
	if strings.Contains(strings.Join(updates, "\n"), "private chain") || !reflect.DeepEqual(updates, []string{"正在检查工作区"}) {
		t.Fatalf("visible updates = %#v", updates)
	}
	if result.Metadata[ClaudeMetadataVersionKey] != "2.1.226 (Claude Code)" ||
		result.Metadata[ClaudeMetadataSessionIDKey] != "session-1" ||
		result.Metadata[ClaudeMetadataInputTokensKey] != "12" ||
		result.Metadata[ClaudeMetadataOutputTokensKey] != "3" {
		t.Fatalf("Execute() metadata = %#v", result.Metadata)
	}

	if len(runner.requests) != 3 {
		t.Fatalf("process requests = %#v", runner.requests)
	}
	if !reflect.DeepEqual(runner.requests[0].Args, []string{"--version"}) {
		t.Fatalf("version args = %#v", runner.requests[0].Args)
	}
	if !reflect.DeepEqual(runner.requests[1].Args, []string{"auth", "status", "--json"}) {
		t.Fatalf("auth args = %#v", runner.requests[1].Args)
	}
	wantArgs := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "dontAsk",
		"--no-session-persistence",
		"Inspect the repository",
	}
	if !reflect.DeepEqual(runner.requests[2].Args, wantArgs) || runner.requests[2].Dir != "E:/workspace" {
		t.Fatalf("execution request = %#v", runner.requests[2])
	}
	if !reflect.DeepEqual(runner.requests[2].InheritEnvironment, []string{"ANTHROPIC_API_KEY"}) || runner.requests[2].Timeout != 20*time.Second {
		t.Fatalf("execution environment/timeout = %#v / %s", runner.requests[2].InheritEnvironment, runner.requests[2].Timeout)
	}
}

func TestClaudeCodeProbeReportsAuthenticationRequired(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{
		{result: delegation.ProcessResult{Stdout: "2.1.226 (Claude Code)", ExecutablePath: "claude.exe"}},
		{result: delegation.ProcessResult{Stdout: `{"loggedIn":false,"authMethod":"none"}`}},
	}}
	registration, err := NewClaudeCodeRegistration(runner, delegation.RuntimeExecutorConfig{ID: ClaudeCodeExecutorID, Enabled: true, Executable: "claude.exe"})
	if err != nil {
		t.Fatalf("NewClaudeCodeRegistration() error = %v", err)
	}
	probe, err := registration.Probe(t.Context())
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if probe.State != delegation.ExecutorProbeActionRequired || probe.AuthState != delegation.ExecutorAuthRequired || probe.DiagnosticCode != ClaudeDiagnosticAuthRequired {
		t.Fatalf("Probe() = %#v", probe)
	}
}

func TestClaudeCodeProbeReportsNotInstalledWithoutApplicationError(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
		err: delegation.NewClassifiedExecutorError(
			delegation.ExecutorFailureSwitchable,
			true,
			delegation.ProcessErrorCodeNotFound,
			errors.New("executable not found"),
		),
	}}}
	registration, err := NewClaudeCodeRegistration(runner, delegation.RuntimeExecutorConfig{ID: ClaudeCodeExecutorID, Executable: "missing-claude"})
	if err != nil {
		t.Fatalf("NewClaudeCodeRegistration() error = %v", err)
	}
	probe, err := registration.Probe(t.Context())
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if probe.State != delegation.ExecutorProbeNotInstalled || probe.Installed || probe.DiagnosticCode != ClaudeDiagnosticNotInstalled {
		t.Fatalf("Probe() = %#v", probe)
	}
}

func TestClaudeCodeProbeReturnsMalformedAuthStatusError(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{
		{result: delegation.ProcessResult{Stdout: "2.1.226 (Claude Code)", ExecutablePath: "claude.exe"}},
		{result: delegation.ProcessResult{Stdout: "not-json"}},
	}}
	registration, err := NewClaudeCodeRegistration(runner, delegation.RuntimeExecutorConfig{ID: ClaudeCodeExecutorID, Executable: "claude.exe"})
	if err != nil {
		t.Fatalf("NewClaudeCodeRegistration() error = %v", err)
	}
	probe, err := registration.Probe(t.Context())
	if err == nil || probe.State != delegation.ExecutorProbeUnhealthy || probe.DiagnosticCode != ClaudeDiagnosticProbeFailed {
		t.Fatalf("Probe() = %#v error=%v", probe, err)
	}
}

func TestClaudeCodeReadonlyExecutionUsesPlanPermissionMode(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
		result: delegation.ProcessResult{Stdout: `{"type":"result","subtype":"success","is_error":false,"result":"read-only result"}`},
	}}}
	registration, err := NewClaudeCodeRegistration(runner, delegation.RuntimeExecutorConfig{ID: ClaudeCodeExecutorID, Enabled: true, Executable: "claude.exe"})
	if err != nil {
		t.Fatalf("NewClaudeCodeRegistration() error = %v", err)
	}
	result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "inspect", WorkspaceHint: t.TempDir(), Readonly: true})
	if result.Error != nil {
		t.Fatalf("Execute() error = %v", result.Error)
	}
	args := runner.requests[0].Args
	permissionIndex := -1
	for index, arg := range args {
		if arg == "--permission-mode" {
			permissionIndex = index
			break
		}
	}
	if permissionIndex < 0 || permissionIndex+1 >= len(args) || args[permissionIndex+1] != "plan" {
		t.Fatalf("readonly args = %#v", args)
	}
}

func TestClaudeCodeExecutionClassifiesFailures(t *testing.T) {
	testCases := []struct {
		name       string
		stdout     string
		stderr     string
		processErr error
		wantClass  delegation.ExecutorFailureClass
		wantSafe   bool
		wantCode   string
	}{
		{name: "login", stdout: `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"Not logged in. Run /login."}`, wantClass: delegation.ExecutorFailureUserActionRequired, wantCode: ClaudeErrorCodeAuthRequired},
		{name: "permission", stdout: `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"Permission denied; approval required."}`, wantClass: delegation.ExecutorFailureUserActionRequired, wantCode: ClaudeErrorCodePermissionRequired},
		{name: "rate-limit", stdout: `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"429 rate limit exceeded"}`, wantClass: delegation.ExecutorFailureSwitchable, wantSafe: true, wantCode: ClaudeErrorCodeRateLimited},
		{name: "crash", stderr: "Claude process crashed", processErr: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, delegation.ProcessErrorCodeExitFailed, errors.New("exit 17")), wantClass: delegation.ExecutorFailureSwitchable, wantSafe: true, wantCode: ClaudeErrorCodeProcessFailed},
		{name: "invalid-process-request", processErr: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureTerminal, false, delegation.ProcessErrorCodeInvalidRequest, errors.New("invalid cwd")), wantClass: delegation.ExecutorFailureTerminal, wantCode: delegation.ProcessErrorCodeInvalidRequest},
		{name: "malformed", stdout: "not-json\n", wantClass: delegation.ExecutorFailureSwitchable, wantSafe: true, wantCode: ClaudeErrorCodeStreamInvalid},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
				result: delegation.ProcessResult{Stdout: testCase.stdout, Stderr: testCase.stderr},
				err:    testCase.processErr,
			}}}
			registration, err := NewClaudeCodeRegistration(runner, delegation.RuntimeExecutorConfig{ID: ClaudeCodeExecutorID, Enabled: true, Executable: "claude.exe"})
			if err != nil {
				t.Fatalf("NewClaudeCodeRegistration() error = %v", err)
			}
			result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "test", WorkspaceHint: t.TempDir()})
			if result.Error == nil {
				t.Fatalf("Execute() error = nil, result = %#v", result)
			}
			class, safe := delegation.ExecutorErrorClassification(result.Error)
			if class != testCase.wantClass || safe != testCase.wantSafe || executorErrorCodeForTest(result.Error) != testCase.wantCode {
				t.Fatalf("classification = %q/%t/%q error=%v", class, safe, executorErrorCodeForTest(result.Error), result.Error)
			}
		})
	}
}

func TestClaudeCodeExecutionUsesStableFallbackForEmptyFailure(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
		result: delegation.ProcessResult{Stdout: `{"type":"result","subtype":"error_during_execution","is_error":true,"result":""}`},
	}}}
	registration, err := NewClaudeCodeRegistration(runner, delegation.RuntimeExecutorConfig{
		ID:         ClaudeCodeExecutorID,
		Enabled:    true,
		Executable: "claude.exe",
	})
	if err != nil {
		t.Fatalf("NewClaudeCodeRegistration() error = %v", err)
	}
	result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "test", WorkspaceHint: t.TempDir()})
	if result.Error == nil || !strings.Contains(result.Error.Error(), "Claude Code execution failed without a diagnostic") {
		t.Fatalf("Execute() error = %v", result.Error)
	}
}

func TestClaudeCodeVisibleUpdatesAreBounded(t *testing.T) {
	longText := strings.Repeat("x", ClaudeVisibleUpdateLimit+100)
	stream := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"` + longText + `"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"done"}`,
	}, "\n")
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
		result: delegation.ProcessResult{Stdout: stream},
	}}}
	registration, err := NewClaudeCodeRegistration(runner, delegation.RuntimeExecutorConfig{ID: ClaudeCodeExecutorID, Enabled: true, Executable: "claude.exe"})
	if err != nil {
		t.Fatalf("NewClaudeCodeRegistration() error = %v", err)
	}
	updates := []string{}
	ctx := delegation.WithWorkerVisibleUpdatePublisher(t.Context(), func(text string) bool {
		updates = append(updates, text)
		return true
	})
	result := registration.Execute(ctx, delegation.TaskRequest{Prompt: "test", WorkspaceHint: t.TempDir()})
	if result.Error != nil {
		t.Fatalf("Execute() error = %v", result.Error)
	}
	if len(updates) != 1 || len(updates[0]) != ClaudeVisibleUpdateLimit {
		t.Fatalf("visible update lengths = %#v", updates)
	}
}

func TestClaudeCodeExecutionPreservesCancellation(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{
		err: delegation.NewClassifiedExecutorError(
			delegation.ExecutorFailureTerminal,
			false,
			delegation.ProcessErrorCodeCanceled,
			context.Canceled,
		),
	}}}
	registration, err := NewClaudeCodeRegistration(runner, delegation.RuntimeExecutorConfig{ID: ClaudeCodeExecutorID, Enabled: true, Executable: "claude.exe"})
	if err != nil {
		t.Fatalf("NewClaudeCodeRegistration() error = %v", err)
	}
	result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "test", WorkspaceHint: t.TempDir()})
	if !errors.Is(result.Error, context.Canceled) || executorErrorCodeForTest(result.Error) != delegation.ProcessErrorCodeCanceled {
		t.Fatalf("Execute() cancellation error = %v", result.Error)
	}
}

func executorErrorCodeForTest(err error) string {
	var coded interface{ Code() string }
	if errors.As(err, &coded) {
		return coded.Code()
	}
	return ""
}
