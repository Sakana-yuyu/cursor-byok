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

func TestKiroCLIRegistrationUsesOfficialHeadlessContract(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{
		{result: delegation.ProcessResult{Stdout: "Kiro CLI 1.26.0", ExecutablePath: "C:/tools/kiro-cli.exe"}},
		{result: delegation.ProcessResult{Stdout: `{"models":[{"id":"kiro-default"}]}`, ExecutablePath: "C:/tools/kiro-cli.exe"}},
		{result: delegation.ProcessResult{Stdout: "完成检查\n"}},
	}}
	registration, err := NewKiroCLIRegistration(runner, delegation.RuntimeExecutorConfig{ID: KiroCLIExecutorID, Enabled: true, Priority: 6, Executable: "C:/tools/kiro-cli.exe", ProbeTimeoutSeconds: 4, ExecutionTimeoutSeconds: 30})
	if err != nil {
		t.Fatalf("NewKiroCLIRegistration() error=%v", err)
	}
	probe, err := registration.Probe(t.Context())
	if err != nil || probe.State != delegation.ExecutorProbeReady || probe.AuthState != delegation.ExecutorAuthReady || probe.Version != "Kiro CLI 1.26.0" {
		t.Fatalf("probe=%#v err=%v", probe, err)
	}
	result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "检查工作区", WorkspaceHint: "E:/workspace", Readonly: true, Timeout: 20 * time.Second})
	if result.Error != nil || result.Output != "完成检查" {
		t.Fatalf("result=%#v", result)
	}
	want := []string{"chat", "--no-interactive", "--trust-tools=read,grep", "检查工作区"}
	if !reflect.DeepEqual(runner.requests[2].Args, want) || runner.requests[2].Dir != "E:/workspace" {
		t.Fatalf("request=%#v", runner.requests[2])
	}
	if !containsString(runner.requests[2].InheritEnvironment, "KIRO_API_KEY") || result.Metadata[KiroMetadataContractKey] != kiroCommandContract {
		t.Fatalf("request=%#v metadata=%#v", runner.requests[2], result.Metadata)
	}
}

func TestKiroCLIProbeClassifiesNotInstalledAndAuthenticationRequired(t *testing.T) {
	notFound := &claudeScriptedRunner{steps: []claudeRunnerStep{{err: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, delegation.ProcessErrorCodeNotFound, errors.New("missing"))}}}
	registration, _ := NewKiroCLIRegistration(notFound, delegation.RuntimeExecutorConfig{ID: KiroCLIExecutorID, Executable: "missing-kiro"})
	probe, err := registration.Probe(t.Context())
	if err != nil || probe.State != delegation.ExecutorProbeNotInstalled || probe.Installed || !strings.Contains(probe.DiagnosticText, "cli.kiro.dev/install") {
		t.Fatalf("probe=%#v err=%v", probe, err)
	}
	auth := &claudeScriptedRunner{steps: []claudeRunnerStep{
		{result: delegation.ProcessResult{Stdout: "Kiro CLI 1.26.0", ExecutablePath: "kiro-cli"}},
		{result: delegation.ProcessResult{Stderr: "KIRO_API_KEY is required", ExecutablePath: "kiro-cli"}, err: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, delegation.ProcessErrorCodeExitFailed, errors.New("exit 1"))},
	}}
	registration, _ = NewKiroCLIRegistration(auth, delegation.RuntimeExecutorConfig{ID: KiroCLIExecutorID, Executable: "kiro-cli"})
	probe, err = registration.Probe(t.Context())
	if err != nil || probe.State != delegation.ExecutorProbeActionRequired || probe.AuthState != delegation.ExecutorAuthRequired || probe.DiagnosticCode != KiroDiagnosticAuthRequired {
		t.Fatalf("probe=%#v err=%v", probe, err)
	}
}

func TestKiroCLIWritableTrustsToolsAndClassifiesFailures(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{result: delegation.ProcessResult{Stderr: "429 rate limit exceeded"}, err: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, delegation.ProcessErrorCodeExitFailed, errors.New("exit 1"))}}}
	registration, _ := NewKiroCLIRegistration(runner, delegation.RuntimeExecutorConfig{ID: KiroCLIExecutorID, Enabled: true, Executable: "kiro-cli"})
	result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "修复", WorkspaceHint: t.TempDir()})
	class, safe := delegation.ExecutorErrorClassification(result.Error)
	if class != delegation.ExecutorFailureSwitchable || !safe || executorErrorCodeForTest(result.Error) != KiroErrorCodeRateLimited {
		t.Fatalf("result=%#v class=%s safe=%t", result, class, safe)
	}
	if !reflect.DeepEqual(runner.requests[0].Args[:3], []string{"chat", "--no-interactive", "--trust-all-tools"}) {
		t.Fatalf("args=%#v", runner.requests[0].Args)
	}
	canceled := &claudeScriptedRunner{steps: []claudeRunnerStep{{err: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureTerminal, false, delegation.ProcessErrorCodeCanceled, context.Canceled)}}}
	registration, _ = NewKiroCLIRegistration(canceled, delegation.RuntimeExecutorConfig{ID: KiroCLIExecutorID, Executable: "kiro-cli"})
	if result = registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "取消", WorkspaceHint: t.TempDir()}); !errors.Is(result.Error, context.Canceled) {
		t.Fatalf("cancel error=%v", result.Error)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
