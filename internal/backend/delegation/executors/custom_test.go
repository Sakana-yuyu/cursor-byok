package executors

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"cursor/internal/backend/delegation"
)

func TestCustomCLIRegistrationSubstitutesArgumentsAndReturnsPlainText(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{
		{result: delegation.ProcessResult{Stdout: "custom 1.0", ExecutablePath: "C:/tools/custom.exe"}},
		{result: delegation.ProcessResult{Stdout: "done\n"}},
	}}
	registration, err := NewCustomCLIRegistration(runner, delegation.RuntimeExecutorConfig{
		ID: "grok-cli", Kind: "custom", DisplayName: "Grok Compatible", Enabled: true, Executable: "C:/tools/custom.exe",
		Options: map[string]string{
			CustomOptionArguments:        `["--prompt","{{prompt}}","--workspace={{workspace}}","--readonly={{readonly}}"]`,
			CustomOptionVersionArguments: `["--version"]`, CustomOptionStdinMode: "none", CustomOptionOutputMode: "text", CustomOptionOutputLimitBytes: "1024",
		},
	})
	if err != nil {
		t.Fatalf("NewCustomCLIRegistration() error=%v", err)
	}
	probe, err := registration.Probe(t.Context())
	if err != nil || probe.State != delegation.ExecutorProbeReady || probe.Version != "custom 1.0" {
		t.Fatalf("probe=%#v err=%v", probe, err)
	}
	result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "inspect; & literal", WorkspaceHint: "E:/workspace", Readonly: true})
	if result.Error != nil || result.Output != "done" {
		t.Fatalf("result=%#v", result)
	}
	want := []string{"--prompt", "inspect; & literal", "--workspace=E:/workspace", "--readonly=true"}
	if !reflect.DeepEqual(runner.requests[1].Args, want) || runner.requests[1].Stdin != "" {
		t.Fatalf("request=%#v", runner.requests[1])
	}
}

func TestCustomCLIRegistrationDeliversStdinAndParsesJSONLines(t *testing.T) {
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{result: delegation.ProcessResult{Stdout: strings.Join([]string{
		`{"event":{"delta":"working"}}`,
		`{"event":{"result":{"text":"finished"}}}`,
	}, "\n")}}}}
	registration, err := NewCustomCLIRegistration(runner, delegation.RuntimeExecutorConfig{
		ID: "custom-jsonl", Kind: "custom", Enabled: true, Executable: "custom",
		Options: map[string]string{
			CustomOptionArguments: `[]`, CustomOptionStdinMode: "prompt", CustomOptionOutputMode: "jsonl", CustomOptionFinalField: "event.result.text", CustomOptionProgressField: "event.delta", CustomOptionOutputLimitBytes: "2048",
		},
	})
	if err != nil {
		t.Fatalf("NewCustomCLIRegistration() error=%v", err)
	}
	updates := []string{}
	ctx := delegation.WithWorkerVisibleUpdatePublisher(t.Context(), func(value string) bool { updates = append(updates, value); return true })
	result := registration.Execute(ctx, delegation.TaskRequest{Prompt: "stdin prompt", WorkspaceHint: t.TempDir()})
	if result.Error != nil || result.Output != "finished" || !reflect.DeepEqual(updates, []string{"working"}) {
		t.Fatalf("result=%#v updates=%#v", result, updates)
	}
	if runner.requests[0].Stdin != "stdin prompt" || len(runner.requests[0].Args) != 0 {
		t.Fatalf("request=%#v", runner.requests[0])
	}
}

func TestCustomCLIRegistrationDiagnosesNotInstalledAndPreservesCancellation(t *testing.T) {
	notFound := &claudeScriptedRunner{steps: []claudeRunnerStep{{err: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, delegation.ProcessErrorCodeNotFound, errors.New("missing"))}}}
	registration, _ := NewCustomCLIRegistration(notFound, customRuntimeConfigForTest("grok-cli"))
	probe, err := registration.Probe(t.Context())
	if err != nil || probe.State != delegation.ExecutorProbeNotInstalled || probe.Installed {
		t.Fatalf("probe=%#v err=%v", probe, err)
	}
	canceled := &claudeScriptedRunner{steps: []claudeRunnerStep{{err: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureTerminal, false, delegation.ProcessErrorCodeCanceled, context.Canceled)}}}
	registration, _ = NewCustomCLIRegistration(canceled, customRuntimeConfigForTest("custom-cli"))
	result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "test", WorkspaceHint: t.TempDir()})
	if !errors.Is(result.Error, context.Canceled) {
		t.Fatalf("cancel error=%v", result.Error)
	}
}

func TestCustomCLIRegistrationRejectsInvalidRuntimeContractAndMissingFinal(t *testing.T) {
	_, err := NewCustomCLIRegistration(&claudeScriptedRunner{}, delegation.RuntimeExecutorConfig{ID: "custom-cli", Kind: "custom", Executable: "custom", Options: map[string]string{CustomOptionArguments: `["{{secret}}"]`}})
	if err == nil || !strings.Contains(err.Error(), "unknown template variable") {
		t.Fatalf("constructor error=%v", err)
	}
	runner := &claudeScriptedRunner{steps: []claudeRunnerStep{{result: delegation.ProcessResult{Stdout: `{"event":{"delta":"only progress"}}`}}}}
	cfg := customRuntimeConfigForTest("custom-jsonl")
	cfg.Options[CustomOptionOutputMode] = "jsonl"
	cfg.Options[CustomOptionFinalField] = "event.result"
	registration, _ := NewCustomCLIRegistration(runner, cfg)
	result := registration.Execute(t.Context(), delegation.TaskRequest{Prompt: "test", WorkspaceHint: t.TempDir()})
	if result.Error == nil || executorErrorCodeForTest(result.Error) != CustomErrorCodeFinalMissing {
		t.Fatalf("result=%#v", result)
	}
}

func TestCustomCLIRegistrationRejectsRuntimeSecretUnknownOptionAndBuiltinCollision(t *testing.T) {
	tests := []struct {
		name    string
		id      delegation.ExecutorID
		options map[string]string
		want    string
	}{
		{name: "secret literal", id: "custom-cli", options: map[string]string{CustomOptionArguments: `["--key","sk-runtime-secret-12345678","{{prompt}}"]`}, want: "secret literal"},
		{name: "unknown option", id: "custom-cli", options: map[string]string{CustomOptionArguments: `["{{prompt}}"]`, "shellCommand": "echo unsafe"}, want: "not supported"},
		{name: "builtin collision", id: "claude-code", options: map[string]string{CustomOptionArguments: `["{{prompt}}"]`}, want: "reserved"},
		{name: "version task token", id: "custom-cli", options: map[string]string{CustomOptionArguments: `["{{prompt}}"]`, CustomOptionVersionArguments: `["{{workspace}}"]`}, want: "versionArguments cannot contain template variables"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewCustomCLIRegistration(&claudeScriptedRunner{}, delegation.RuntimeExecutorConfig{ID: tc.id, Kind: "custom", Executable: "custom", Options: tc.options})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("constructor error=%v want=%q", err, tc.want)
			}
		})
	}
}

func customRuntimeConfigForTest(id delegation.ExecutorID) delegation.RuntimeExecutorConfig {
	return delegation.RuntimeExecutorConfig{
		ID: id, Kind: "custom", Enabled: true, Executable: "custom",
		Options: map[string]string{CustomOptionArguments: `["{{prompt}}"]`, CustomOptionVersionArguments: `["--version"]`, CustomOptionStdinMode: "none", CustomOptionOutputMode: "text", CustomOptionOutputLimitBytes: "1024"},
	}
}
