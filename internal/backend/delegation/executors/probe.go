package executors

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"cursor/internal/backend/delegation"
)

const (
	CLIProbeOutputLimit    = 8 * 1024
	defaultCLIProbeTimeout = 5 * time.Second
	maximumCLIProbeTimeout = 30 * time.Second

	CLIProbeDiagnosticReady           = "ready"
	CLIProbeDiagnosticNotInstalled    = "not_installed"
	CLIProbeDiagnosticVersionMissing  = "version_missing"
	CLIProbeDiagnosticOutputTruncated = "version_output_truncated"
	CLIProbeDiagnosticTimeout         = "probe_timeout"
	CLIProbeDiagnosticCanceled        = "probe_canceled"
	CLIProbeDiagnosticFailed          = "probe_failed"
)

type CLIProbeSpec struct {
	Executable         string
	Args               []string
	Dir                string
	Timeout            time.Duration
	Environment        map[string]string
	InheritEnvironment []string
	Capabilities       []delegation.ExecutorCapability
}

func ProbeCLI(
	ctx context.Context,
	runner *delegation.ProcessRunner,
	spec CLIProbeSpec,
) (delegation.ExecutorProbeResult, error) {
	probedAt := time.Now().UTC()
	if runner == nil {
		err := errors.New("process runner is nil")
		return unhealthyCLIProbeResult(spec, probedAt, CLIProbeDiagnosticFailed, err.Error()), err
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = defaultCLIProbeTimeout
	}
	if timeout > maximumCLIProbeTimeout {
		timeout = maximumCLIProbeTimeout
	}
	processResult, err := runner.Run(ctx, delegation.ProcessRequest{
		Executable:         spec.Executable,
		Args:               append([]string{}, spec.Args...),
		Dir:                spec.Dir,
		Env:                cloneEnvironment(spec.Environment),
		InheritEnvironment: append([]string{}, spec.InheritEnvironment...),
		Timeout:            timeout,
		StdoutLimit:        CLIProbeOutputLimit,
		StderrLimit:        CLIProbeOutputLimit,
	})
	probedAt = time.Now().UTC()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return delegation.ExecutorProbeResult{
				State:          delegation.ExecutorProbeNotInstalled,
				Installed:      false,
				AuthState:      delegation.ExecutorAuthUnknown,
				Capabilities:   append([]delegation.ExecutorCapability{}, spec.Capabilities...),
				DiagnosticCode: CLIProbeDiagnosticNotInstalled,
				DiagnosticText: "executable was not found",
				ProbedAt:       probedAt,
			}, nil
		}
		code := CLIProbeDiagnosticFailed
		if errors.Is(err, context.DeadlineExceeded) {
			code = CLIProbeDiagnosticTimeout
		} else if errors.Is(err, context.Canceled) {
			code = CLIProbeDiagnosticCanceled
		}
		diagnostic := strings.TrimSpace(strings.Join([]string{err.Error(), processResult.Stderr}, "\n"))
		return unhealthyCLIProbeResult(spec, probedAt, code, diagnostic), err
	}
	if processResult.StdoutTruncated || processResult.StderrTruncated {
		return incompatibleCLIProbeResult(
			spec,
			processResult.ExecutablePath,
			probedAt,
			CLIProbeDiagnosticOutputTruncated,
			"version command output exceeded the probe limit",
		), nil
	}
	version := strings.TrimSpace(processResult.Stdout)
	if version == "" {
		version = strings.TrimSpace(processResult.Stderr)
	}
	if version == "" {
		return incompatibleCLIProbeResult(
			spec,
			processResult.ExecutablePath,
			probedAt,
			CLIProbeDiagnosticVersionMissing,
			"version command returned no output",
		), nil
	}
	return delegation.ExecutorProbeResult{
		State:          delegation.ExecutorProbeReady,
		ExecutablePath: processResult.ExecutablePath,
		Version:        version,
		Installed:      true,
		AuthState:      delegation.ExecutorAuthUnknown,
		Capabilities:   append([]delegation.ExecutorCapability{}, spec.Capabilities...),
		DiagnosticCode: CLIProbeDiagnosticReady,
		ProbedAt:       probedAt,
	}, nil
}

func unhealthyCLIProbeResult(
	spec CLIProbeSpec,
	probedAt time.Time,
	code string,
	diagnostic string,
) delegation.ExecutorProbeResult {
	path, _ := delegation.ResolveExecutable(spec.Executable)
	return delegation.ExecutorProbeResult{
		State:          delegation.ExecutorProbeUnhealthy,
		ExecutablePath: path,
		Installed:      path != "",
		AuthState:      delegation.ExecutorAuthUnknown,
		Capabilities:   append([]delegation.ExecutorCapability{}, spec.Capabilities...),
		DiagnosticCode: code,
		DiagnosticText: strings.TrimSpace(diagnostic),
		ProbedAt:       probedAt,
	}
}

func incompatibleCLIProbeResult(
	spec CLIProbeSpec,
	path string,
	probedAt time.Time,
	code string,
	diagnostic string,
) delegation.ExecutorProbeResult {
	return delegation.ExecutorProbeResult{
		State:          delegation.ExecutorProbeIncompatible,
		ExecutablePath: path,
		Installed:      true,
		AuthState:      delegation.ExecutorAuthUnknown,
		Capabilities:   append([]delegation.ExecutorCapability{}, spec.Capabilities...),
		DiagnosticCode: code,
		DiagnosticText: strings.TrimSpace(diagnostic),
		ProbedAt:       probedAt,
	}
}

func cloneEnvironment(source map[string]string) map[string]string {
	if len(source) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
