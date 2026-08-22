package executors

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"cursor/internal/backend/delegation"
)

const cliProbeHelperEnvironment = "GO_WANT_CLI_PROBE_HELPER"

// cliProbeHelperTimeout 只为 helper 进程的启动留余量，不是任何用例的被测行为——
// helper 在这些模式下立即退出。ProbeCLI 用 os.Executable() 重新拉起测试二进制本身，
// 冷启动一个大体积二进制在负载高的机器上会超过秒级，取值太紧会让用例偶发报
// context deadline exceeded 而不是被断言的诊断码。真正验证超时语义的是
// TestCLIProbeTimeoutAndFailureDiagnosticsAreSanitized，它用 150ms 配 sleep helper。
const cliProbeHelperTimeout = 30 * time.Second

func TestCLIProbeReadyUsesBoundedNonInteractiveCommand(t *testing.T) {
	runner := delegation.NewProcessRunner(delegation.ProcessRunnerConfig{})
	result, err := ProbeCLI(t.Context(), runner, CLIProbeSpec{
		Executable: cliProbeTestExecutable(t),
		Args:       cliProbeHelperArgs("version"),
		Timeout:    cliProbeHelperTimeout,
		Environment: map[string]string{
			cliProbeHelperEnvironment: "cli-probe-helper-enabled",
		},
		Capabilities: []delegation.ExecutorCapability{delegation.ExecutorCapabilityReadWorkspace},
	})
	if err != nil {
		t.Fatalf("ProbeCLI() error = %v", err)
	}
	if result.State != delegation.ExecutorProbeReady || !result.Installed {
		t.Fatalf("ProbeCLI() state/installed = %q/%t", result.State, result.Installed)
	}
	if result.Version != "task15-cli 1.2.3" || result.ExecutablePath == "" {
		t.Fatalf("ProbeCLI() path/version = %q/%q", result.ExecutablePath, result.Version)
	}
	if len(result.Capabilities) != 1 || result.Capabilities[0] != delegation.ExecutorCapabilityReadWorkspace {
		t.Fatalf("ProbeCLI() capabilities = %v", result.Capabilities)
	}
}

func TestCLIProbeNotInstalledIsDiagnosticNotApplicationError(t *testing.T) {
	runner := delegation.NewProcessRunner(delegation.ProcessRunnerConfig{})
	result, err := ProbeCLI(t.Context(), runner, CLIProbeSpec{Executable: "cursor-byok-not-installed-task15-probe"})
	if err != nil {
		t.Fatalf("ProbeCLI() not-installed error = %v", err)
	}
	if result.State != delegation.ExecutorProbeNotInstalled || result.Installed {
		t.Fatalf("ProbeCLI() not-installed state = %#v", result)
	}
	if result.DiagnosticCode != CLIProbeDiagnosticNotInstalled {
		t.Fatalf("ProbeCLI() diagnostic code = %q", result.DiagnosticCode)
	}
}

func TestCLIProbeRejectsEmptyOrTruncatedVersionOutput(t *testing.T) {
	runner := delegation.NewProcessRunner(delegation.ProcessRunnerConfig{})
	for _, testCase := range []struct {
		name string
		mode string
		code string
	}{
		{name: "empty", mode: "empty", code: CLIProbeDiagnosticVersionMissing},
		{name: "truncated", mode: "large-version", code: CLIProbeDiagnosticOutputTruncated},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := ProbeCLI(t.Context(), runner, CLIProbeSpec{
				Executable: cliProbeTestExecutable(t),
				Args:       cliProbeHelperArgs(testCase.mode),
				Timeout:    cliProbeHelperTimeout,
				Environment: map[string]string{
					cliProbeHelperEnvironment: "cli-probe-helper-enabled",
				},
			})
			if err != nil {
				t.Fatalf("ProbeCLI() error = %v", err)
			}
			if result.State != delegation.ExecutorProbeIncompatible || result.DiagnosticCode != testCase.code {
				t.Fatalf("ProbeCLI() incompatible result = %#v", result)
			}
		})
	}
}

func TestCLIProbeTimeoutAndFailureDiagnosticsAreSanitized(t *testing.T) {
	secret := "sk-cli-probe-secret-value"
	runner := delegation.NewProcessRunner(delegation.ProcessRunnerConfig{})
	for _, testCase := range []struct {
		name    string
		mode    string
		code    string
		timeout time.Duration
	}{
		// timeout 用例需要短预算触发真实超时；failed 用例是重启测试二进制自身，
		// Windows 高负载下拉起进程可能超过 150ms 被误判为 probe_timeout，故放宽。
		{name: "timeout", mode: "sleep", code: CLIProbeDiagnosticTimeout, timeout: 150 * time.Millisecond},
		{name: "failed", mode: "fail", code: CLIProbeDiagnosticFailed, timeout: 5 * time.Second},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := ProbeCLI(t.Context(), runner, CLIProbeSpec{
				Executable: cliProbeTestExecutable(t),
				Args:       cliProbeHelperArgs(testCase.mode),
				Timeout:    testCase.timeout,
				Environment: map[string]string{
					cliProbeHelperEnvironment: "cli-probe-helper-enabled",
					"CLI_PROBE_SECRET":        secret,
				},
			})
			if err == nil {
				t.Fatalf("ProbeCLI() error = nil, result = %#v", result)
			}
			if result.State != delegation.ExecutorProbeUnhealthy || result.DiagnosticCode != testCase.code {
				t.Fatalf("ProbeCLI() unhealthy result = %#v", result)
			}
			if strings.Contains(result.DiagnosticText, secret) {
				t.Fatalf("ProbeCLI() diagnostic leaked secret: %q", result.DiagnosticText)
			}
			if testCase.name == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("ProbeCLI() timeout error = %v", err)
			}
		})
	}
}

func TestCLIProbeHelperProcess(t *testing.T) {
	if os.Getenv(cliProbeHelperEnvironment) != "cli-probe-helper-enabled" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "version":
		fmt.Fprintln(os.Stdout, "task15-cli 1.2.3")
	case "empty":
	case "large-version":
		fmt.Fprintln(os.Stdout, strings.Repeat("v", CLIProbeOutputLimit+1))
	case "sleep":
		time.Sleep(30 * time.Second)
	case "fail":
		fmt.Fprintf(os.Stderr, "token=%s\n", os.Getenv("CLI_PROBE_SECRET"))
		os.Exit(17)
	default:
		os.Exit(18)
	}
	os.Exit(0)
}

func cliProbeTestExecutable(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	return executable
}

func cliProbeHelperArgs(mode string) []string {
	return []string{"-test.run=TestCLIProbeHelperProcess", "--", mode}
}
