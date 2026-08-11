package executors

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"cursor/internal/backend/delegation"
)

const (
	KiroCLIExecutorID delegation.ExecutorID = "kiro-cli"

	KiroDiagnosticReady        = "kiro_ready"
	KiroDiagnosticNotInstalled = "kiro_not_installed"
	KiroDiagnosticAuthRequired = "kiro_auth_required"
	KiroDiagnosticIncompatible = "kiro_incompatible"
	KiroDiagnosticProbeFailed  = "kiro_probe_failed"

	KiroErrorCodeInvalidTask   = "kiro_invalid_task"
	KiroErrorCodeAuthRequired  = "kiro_auth_required"
	KiroErrorCodeRateLimited   = "kiro_rate_limited"
	KiroErrorCodeProcessFailed = "kiro_process_failed"
	KiroErrorCodeNoResult      = "kiro_no_result"

	KiroMetadataVersionKey  = "kiro_version"
	KiroMetadataContractKey = "kiro_command_contract"

	kiroCommandContract = "chat-no-interactive-v1"
	kiroOutputLimit     = 4 * 1024 * 1024
)

var kiroCapabilities = []delegation.ExecutorCapability{
	delegation.ExecutorCapabilityReadWorkspace,
	delegation.ExecutorCapabilityWriteWorkspace,
	delegation.ExecutorCapabilityShell,
	delegation.ExecutorCapabilityNetwork,
	delegation.ExecutorCapabilityMCP,
}

type kiroCLIAdapter struct {
	runner       processRunner
	config       delegation.RuntimeExecutorConfig
	executable   string
	probeTimeout time.Duration
	runTimeout   time.Duration
	versionMu    sync.RWMutex
	version      string
}

func NewKiroCLIRegistration(runner processRunner, config delegation.RuntimeExecutorConfig) (delegation.ExecutorRegistration, error) {
	if runner == nil {
		return delegation.ExecutorRegistration{}, errors.New("Kiro CLI process runner is required")
	}
	if config.ID == "" {
		config.ID = KiroCLIExecutorID
	}
	if config.ID != KiroCLIExecutorID {
		return delegation.ExecutorRegistration{}, fmt.Errorf("Kiro CLI executor id %q is invalid", config.ID)
	}
	executable := strings.TrimSpace(config.Executable)
	if executable == "" {
		executable = "kiro-cli"
	}
	adapter := &kiroCLIAdapter{
		runner: runner, config: config, executable: executable,
		probeTimeout: executorTimeout(config.ProbeTimeoutSeconds, 5*time.Second),
		runTimeout:   executorTimeout(config.ExecutionTimeoutSeconds, 2*time.Minute),
	}
	return delegation.ExecutorRegistration{
		ID: KiroCLIExecutorID, DisplayName: firstNonEmpty(config.DisplayName, "Kiro CLI"),
		Enabled: config.Enabled, Priority: config.Priority,
		Capabilities: append([]delegation.ExecutorCapability{}, kiroCapabilities...),
		Probe:        adapter.probe, Execute: adapter.execute,
	}, nil
}

func (adapter *kiroCLIAdapter) probe(ctx context.Context) (delegation.ExecutorProbeResult, error) {
	versionResult, err := adapter.runProbe(ctx, "--version")
	probedAt := time.Now().UTC()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || executorErrorCode(err) == delegation.ProcessErrorCodeNotFound {
			return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeNotInstalled, AuthState: delegation.ExecutorAuthUnknown, Capabilities: append([]delegation.ExecutorCapability{}, kiroCapabilities...), DiagnosticCode: KiroDiagnosticNotInstalled, DiagnosticText: "Kiro CLI executable was not found; install it from https://cli.kiro.dev/install", ProbedAt: probedAt}, nil
		}
		return adapter.unhealthyProbe(versionResult, probedAt, err), err
	}
	version := strings.TrimSpace(firstNonEmpty(versionResult.Stdout, versionResult.Stderr))
	if version == "" || versionResult.StdoutTruncated || versionResult.StderrTruncated {
		err := errors.New("Kiro CLI version output is empty or truncated")
		return adapter.unhealthyProbe(versionResult, probedAt, err), err
	}
	adapter.setVersion(version)
	modelsResult, err := adapter.runProbe(ctx, "chat", "--list-models", "--format", "json")
	if err != nil {
		diagnostic := firstNonEmpty(modelsResult.Stderr, err.Error())
		if kiroAuthenticationRequired(diagnostic) {
			return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeActionRequired, ExecutablePath: firstNonEmpty(versionResult.ExecutablePath, modelsResult.ExecutablePath), Version: version, Installed: true, AuthState: delegation.ExecutorAuthRequired, Capabilities: append([]delegation.ExecutorCapability{}, kiroCapabilities...), DiagnosticCode: KiroDiagnosticAuthRequired, DiagnosticText: "Kiro CLI authentication is required; set KIRO_API_KEY for headless execution or run kiro-cli login", ProbedAt: time.Now().UTC()}, nil
		}
		return adapter.unhealthyProbe(modelsResult, time.Now().UTC(), err), err
	}
	if modelsResult.StdoutTruncated || modelsResult.StderrTruncated || !kiroModelListRecognized(modelsResult.Stdout) {
		return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeIncompatible, ExecutablePath: firstNonEmpty(versionResult.ExecutablePath, modelsResult.ExecutablePath), Version: version, Installed: true, AuthState: delegation.ExecutorAuthUnknown, Capabilities: append([]delegation.ExecutorCapability{}, kiroCapabilities...), DiagnosticCode: KiroDiagnosticIncompatible, DiagnosticText: "Kiro CLI does not support the required chat --list-models --format json probe contract", ProbedAt: time.Now().UTC()}, nil
	}
	return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeReady, ExecutablePath: firstNonEmpty(versionResult.ExecutablePath, modelsResult.ExecutablePath), Version: version, Installed: true, AuthState: delegation.ExecutorAuthReady, Capabilities: append([]delegation.ExecutorCapability{}, kiroCapabilities...), DiagnosticCode: KiroDiagnosticReady, ProbedAt: time.Now().UTC()}, nil
}

func (adapter *kiroCLIAdapter) runProbe(ctx context.Context, args ...string) (delegation.ProcessResult, error) {
	return adapter.runner.Run(ctx, delegation.ProcessRequest{Executable: adapter.executable, Args: args, Timeout: adapter.probeTimeout, InheritEnvironment: adapter.environmentNames(), StdoutLimit: CLIProbeOutputLimit, StderrLimit: CLIProbeOutputLimit})
}

func (adapter *kiroCLIAdapter) execute(ctx context.Context, request delegation.TaskRequest) delegation.TaskResult {
	prompt := strings.TrimSpace(request.Prompt)
	workspace := strings.TrimSpace(request.WorkspaceHint)
	if prompt == "" || workspace == "" {
		return delegation.TaskResult{Error: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureTerminal, false, KiroErrorCodeInvalidTask, errors.New("Kiro CLI task requires a prompt and workspace"))}
	}
	delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusDispatched, 1, nil, nil, "Kiro CLI task dispatched", "")
	timeout := adapter.runTimeout
	if request.Timeout > 0 && request.Timeout < timeout {
		timeout = request.Timeout
	}
	trust := "--trust-all-tools"
	if request.Readonly {
		trust = "--trust-tools=read,grep"
	}
	processResult, processErr := adapter.runner.Run(ctx, delegation.ProcessRequest{
		Executable: adapter.executable, Args: []string{"chat", "--no-interactive", trust, prompt}, Dir: workspace,
		InheritEnvironment: adapter.environmentNames(), Timeout: timeout, StdoutLimit: kiroOutputLimit, StderrLimit: kiroOutputLimit,
	})
	output := strings.TrimSpace(processResult.Stdout)
	metadata := map[string]string{KiroMetadataContractKey: kiroCommandContract}
	if version := adapter.getVersion(); version != "" {
		metadata[KiroMetadataVersionKey] = version
	}
	if processErr != nil {
		if errors.Is(processErr, context.Canceled) || errors.Is(processErr, context.DeadlineExceeded) {
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCanceled, 2, nil, nil, "Kiro CLI task canceled", processErr.Error())
			return delegation.TaskResult{Output: output, Error: processErr, Metadata: metadata}
		}
		failure := firstNonEmpty(processResult.Stderr, processErr.Error())
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Kiro CLI task failed", failure)
		return delegation.TaskResult{Output: output, Error: classifyKiroFailure(failure, KiroErrorCodeProcessFailed), Metadata: metadata}
	}
	if processResult.StdoutTruncated {
		err := delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, KiroErrorCodeProcessFailed, errors.New("Kiro CLI output was truncated"))
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Kiro CLI output was truncated", err.Error())
		return delegation.TaskResult{Output: output, Error: err, Metadata: metadata}
	}
	if output == "" {
		err := delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, KiroErrorCodeNoResult, errors.New("Kiro CLI completed without a final text result"))
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Kiro CLI returned no final result", err.Error())
		return delegation.TaskResult{Error: err, Metadata: metadata}
	}
	delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCompleted, 2, nil, nil, "Kiro CLI task completed", "")
	return delegation.TaskResult{Output: output, Metadata: metadata}
}

func (adapter *kiroCLIAdapter) environmentNames() []string {
	values := append([]string{}, adapter.config.EnvironmentVariables...)
	for _, name := range values {
		if name == "KIRO_API_KEY" {
			return values
		}
	}
	return append(values, "KIRO_API_KEY")
}

func (adapter *kiroCLIAdapter) unhealthyProbe(result delegation.ProcessResult, probedAt time.Time, err error) delegation.ExecutorProbeResult {
	return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeUnhealthy, ExecutablePath: result.ExecutablePath, Version: adapter.getVersion(), Installed: result.ExecutablePath != "", AuthState: delegation.ExecutorAuthUnknown, Capabilities: append([]delegation.ExecutorCapability{}, kiroCapabilities...), DiagnosticCode: KiroDiagnosticProbeFailed, DiagnosticText: firstNonEmpty(result.Stderr, err.Error()), ProbedAt: probedAt}
}

func (adapter *kiroCLIAdapter) setVersion(version string) {
	adapter.versionMu.Lock()
	adapter.version = version
	adapter.versionMu.Unlock()
}

func (adapter *kiroCLIAdapter) getVersion() string {
	adapter.versionMu.RLock()
	defer adapter.versionMu.RUnlock()
	return adapter.version
}

func kiroModelListRecognized(output string) bool {
	value := strings.TrimSpace(output)
	return strings.HasPrefix(value, "{") && strings.Contains(value, "models")
}

func kiroAuthenticationRequired(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "kiro_api_key") || strings.Contains(lower, "api key") || strings.Contains(lower, "authentication required") || strings.Contains(lower, "not authenticated") || strings.Contains(lower, "login required")
}

func classifyKiroFailure(message, fallbackCode string) error {
	message = firstNonEmpty(message, "Kiro CLI execution failed without a diagnostic")
	lower := strings.ToLower(message)
	switch {
	case kiroAuthenticationRequired(lower):
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureUserActionRequired, false, KiroErrorCodeAuthRequired, errors.New(message))
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "429"), strings.Contains(lower, "quota"), strings.Contains(lower, "too many requests"):
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, KiroErrorCodeRateLimited, errors.New(message))
	default:
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, fallbackCode, errors.New(message))
	}
}
