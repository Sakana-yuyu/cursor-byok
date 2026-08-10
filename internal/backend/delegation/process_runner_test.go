package delegation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const processRunnerHelperEnvironment = "GO_WANT_PROCESS_RUNNER_HELPER"

func TestProcessRunnerUsesArgumentArrayAndWorkingDirectory(t *testing.T) {
	executable := processRunnerTestExecutable(t)
	workingDirectory := t.TempDir()
	marker := filepath.Join(workingDirectory, "shell-interpolation-marker")
	literalArgument := "$(touch " + marker + "); & echo injected | ignored"
	runner := NewProcessRunner(ProcessRunnerConfig{})

	result, err := runner.Run(t.Context(), ProcessRequest{
		Executable: executable,
		Args:       processRunnerHelperArgs("args-and-cwd", literalArgument),
		Dir:        workingDirectory,
		Env: map[string]string{
			processRunnerHelperEnvironment: "process-runner-helper-enabled",
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExecutablePath == "" || !filepath.IsAbs(result.ExecutablePath) {
		t.Fatalf("Run() executable path = %q, want absolute path", result.ExecutablePath)
	}
	if !strings.Contains(result.Stdout, "arg="+literalArgument) {
		t.Fatalf("Run() stdout did not preserve literal argument: %q", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "cwd="+workingDirectory) {
		t.Fatalf("Run() stdout cwd = %q, want %q", result.Stdout, workingDirectory)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("argument was interpreted by a shell: stat error = %v", statErr)
	}
}

func TestProcessRunnerDeliversBoundedStdinWithoutShellInterpretation(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "stdin-shell-marker")
	stdin := "prompt; & echo literal $(touch " + marker + ")"
	runner := NewProcessRunner(ProcessRunnerConfig{})
	result, err := runner.Run(t.Context(), ProcessRequest{
		Executable: processRunnerTestExecutable(t),
		Args:       processRunnerHelperArgs("stdin"),
		Stdin:      stdin,
		Env: map[string]string{
			processRunnerHelperEnvironment: "process-runner-helper-enabled",
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Stdout != stdin {
		t.Fatalf("Run() stdin stdout = %q, want %q", result.Stdout, stdin)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stdin was interpreted by a shell: stat error = %v", statErr)
	}
}

func TestProcessRunnerRejectsOversizedStdin(t *testing.T) {
	runner := NewProcessRunner(ProcessRunnerConfig{})
	_, err := runner.Run(t.Context(), ProcessRequest{
		Executable: processRunnerTestExecutable(t),
		Args:       processRunnerHelperArgs("stdin"),
		Stdin:      strings.Repeat("x", MaximumProcessStdinBytes+1),
	})
	class, retrySafe := ExecutorErrorClassification(err)
	if err == nil || class != ExecutorFailureTerminal || retrySafe || executorErrorCode(err) != ProcessErrorCodeInvalidRequest {
		t.Fatalf("oversized stdin error=%v class=%s retrySafe=%t", err, class, retrySafe)
	}
}

func TestProcessRunnerBoundsStdoutAndStderr(t *testing.T) {
	runner := NewProcessRunner(ProcessRunnerConfig{})
	result, err := runner.Run(t.Context(), ProcessRequest{
		Executable: processRunnerTestExecutable(t),
		Args:       processRunnerHelperArgs("bounded-output", "2048"),
		Env: map[string]string{
			processRunnerHelperEnvironment: "process-runner-helper-enabled",
		},
		StdoutLimit: 64,
		StderrLimit: 32,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Stdout) != 64 || !result.StdoutTruncated {
		t.Fatalf("Run() stdout len/truncated = %d/%t, want 64/true", len(result.Stdout), result.StdoutTruncated)
	}
	if len(result.Stderr) != 32 || !result.StderrTruncated {
		t.Fatalf("Run() stderr len/truncated = %d/%t, want 32/true", len(result.Stderr), result.StderrTruncated)
	}
}

func TestProcessRunnerUsesEnvironmentAllowlistAndRedactsOverrides(t *testing.T) {
	t.Setenv("PROCESS_RUNNER_ALLOWED", "allowed-value")
	t.Setenv("PROCESS_RUNNER_BLOCKED", "blocked-value")
	secret := "sk-process-runner-secret-value"
	runner := NewProcessRunner(ProcessRunnerConfig{})

	result, err := runner.Run(t.Context(), ProcessRequest{
		Executable: processRunnerTestExecutable(t),
		Args: processRunnerHelperArgs(
			"environment",
			"PROCESS_RUNNER_ALLOWED",
			"PROCESS_RUNNER_BLOCKED",
			"PROCESS_RUNNER_OVERRIDE_SECRET",
		),
		Env: map[string]string{
			processRunnerHelperEnvironment:   "process-runner-helper-enabled",
			"PROCESS_RUNNER_OVERRIDE_SECRET": secret,
		},
		InheritEnvironment: []string{"PROCESS_RUNNER_ALLOWED"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(result.Stdout, "PROCESS_RUNNER_ALLOWED=allowed-value") {
		t.Fatalf("Run() stdout missing allowlisted environment: %q", result.Stdout)
	}
	if strings.Contains(result.Stdout, "blocked-value") {
		t.Fatalf("Run() stdout leaked non-allowlisted environment: %q", result.Stdout)
	}
	if strings.Contains(result.Stdout, secret) || !strings.Contains(result.Stdout, "<redacted>") {
		t.Fatalf("Run() stdout secret redaction = %q", result.Stdout)
	}
}

func TestProcessRunnerRedactsSecretAcrossOutputLimitBoundary(t *testing.T) {
	secret := "sk-process-runner-boundary-secret"
	runner := NewProcessRunner(ProcessRunnerConfig{})
	result, err := runner.Run(t.Context(), ProcessRequest{
		Executable: processRunnerTestExecutable(t),
		Args:       processRunnerHelperArgs("secret-boundary"),
		Env: map[string]string{
			processRunnerHelperEnvironment: "process-runner-helper-enabled",
			"PROCESS_RUNNER_SECRET":        secret,
		},
		StdoutLimit: 64,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.Contains(result.Stdout, secret[:4]) {
		t.Fatalf("Run() leaked a truncated secret prefix: %q", result.Stdout)
	}
	if len(result.Stdout) > 64 || !result.StdoutTruncated {
		t.Fatalf("Run() stdout len/truncated = %d/%t", len(result.Stdout), result.StdoutTruncated)
	}
}

func TestProcessRunnerTimeoutClassAndDescendantTermination(t *testing.T) {
	executable := processRunnerTestExecutable(t)
	childPIDPath := filepath.Join(t.TempDir(), "descendant.pid")
	runner := NewProcessRunner(ProcessRunnerConfig{WaitDelay: 500 * time.Millisecond})

	result, err := runner.Run(t.Context(), ProcessRequest{
		Executable: executable,
		Args:       processRunnerHelperArgs("spawn-descendant", childPIDPath),
		Env: map[string]string{
			processRunnerHelperEnvironment: "process-runner-helper-enabled",
		},
		Timeout: 2 * time.Second,
	})
	if err == nil {
		t.Fatalf("Run() error = nil, result = %#v", result)
	}
	class, retrySafe := ExecutorErrorClassification(err)
	if class != ExecutorFailureTerminal || retrySafe {
		t.Fatalf("Run() timeout classification = %q retry_safe=%t", class, retrySafe)
	}
	if executorErrorCode(err) != ProcessErrorCodeTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() timeout error = %T %v", err, err)
	}

	childPID := waitForProcessRunnerChildPID(t, childPIDPath)
	t.Cleanup(func() {
		if processAliveForTest(childPID) {
			_ = killProcessForTest(childPID)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for processAliveForTest(childPID) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if processAliveForTest(childPID) {
		t.Fatalf("descendant process %d survived process-tree cancellation", childPID)
	}
}

func TestProcessRunnerParentCancellationIsTerminal(t *testing.T) {
	runner := NewProcessRunner(ProcessRunnerConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runner.Run(ctx, ProcessRequest{
		Executable: processRunnerTestExecutable(t),
		Args:       processRunnerHelperArgs("sleep", "10s"),
		Env: map[string]string{
			processRunnerHelperEnvironment: "process-runner-helper-enabled",
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() cancellation error = %v", err)
	}
	class, retrySafe := ExecutorErrorClassification(err)
	if class != ExecutorFailureTerminal || retrySafe || executorErrorCode(err) != ProcessErrorCodeCanceled {
		t.Fatalf("Run() cancellation classification = %q retry_safe=%t code=%q", class, retrySafe, executorErrorCode(err))
	}
}

func TestProcessRunnerCleansDescendantsAfterSuccessfulParentExit(t *testing.T) {
	childPIDPath := filepath.Join(t.TempDir(), "successful-descendant.pid")
	runner := NewProcessRunner(ProcessRunnerConfig{WaitDelay: 500 * time.Millisecond})
	result, err := runner.Run(t.Context(), ProcessRequest{
		Executable: processRunnerTestExecutable(t),
		Args:       processRunnerHelperArgs("spawn-descendant-exit", childPIDPath),
		Env: map[string]string{
			processRunnerHelperEnvironment: "process-runner-helper-enabled",
		},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run() error = %v, result = %#v", err, result)
	}
	childPID := waitForProcessRunnerChildPID(t, childPIDPath)
	t.Cleanup(func() {
		if processAliveForTest(childPID) {
			_ = killProcessForTest(childPID)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for processAliveForTest(childPID) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if processAliveForTest(childPID) {
		t.Fatalf("descendant process %d survived successful parent cleanup", childPID)
	}
}

func TestProcessRunnerExecutableNotFoundIsClassified(t *testing.T) {
	runner := NewProcessRunner(ProcessRunnerConfig{})
	_, err := runner.Run(t.Context(), ProcessRequest{Executable: "cursor-byok-definitely-not-installed-task15"})
	if err == nil || !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("Run() not-installed error = %T %v", err, err)
	}
	class, retrySafe := ExecutorErrorClassification(err)
	if class != ExecutorFailureSwitchable || !retrySafe || executorErrorCode(err) != ProcessErrorCodeNotFound {
		t.Fatalf("Run() not-installed classification = %q retry_safe=%t code=%q", class, retrySafe, executorErrorCode(err))
	}
}

func TestProcessRunnerRejectsOversizedEnvironment(t *testing.T) {
	runner := NewProcessRunner(ProcessRunnerConfig{})
	_, err := runner.Run(t.Context(), ProcessRequest{
		Executable: processRunnerTestExecutable(t),
		Args:       processRunnerHelperArgs("sleep", "1ms"),
		Env: map[string]string{
			processRunnerHelperEnvironment: "process-runner-helper-enabled",
			"PROCESS_RUNNER_SECRET":        strings.Repeat("s", MaximumProcessEnvironmentValueBytes+1),
		},
	})
	if err == nil {
		t.Fatal("Run() oversized environment error = nil")
	}
	class, retrySafe := ExecutorErrorClassification(err)
	if class != ExecutorFailureTerminal || retrySafe || executorErrorCode(err) != ProcessErrorCodeInvalidRequest {
		t.Fatalf("Run() oversized environment classification = %q retry_safe=%t code=%q", class, retrySafe, executorErrorCode(err))
	}
}

func TestProcessRunnerHelperProcess(t *testing.T) {
	if os.Getenv(processRunnerHelperEnvironment) != "process-runner-helper-enabled" {
		return
	}
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(2)
	}
	arguments := os.Args[separator+1:]
	switch arguments[0] {
	case "args-and-cwd":
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(3)
		}
		fmt.Printf("arg=%s\ncwd=%s\n", arguments[1], cwd)
	case "bounded-output":
		size, err := strconv.Atoi(arguments[1])
		if err != nil {
			os.Exit(4)
		}
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("O", size))
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("E", size))
	case "stdin":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(11)
		}
		_, _ = os.Stdout.Write(data)
	case "environment":
		for _, name := range arguments[1:] {
			fmt.Printf("%s=%s\n", name, os.Getenv(name))
		}
	case "secret-boundary":
		fmt.Printf("%s%s", strings.Repeat("X", 60), os.Getenv("PROCESS_RUNNER_SECRET"))
	case "sleep":
		duration, err := time.ParseDuration(arguments[1])
		if err != nil {
			os.Exit(5)
		}
		time.Sleep(duration)
	case "spawn-descendant":
		command := exec.Command(
			os.Args[0],
			"-test.run=TestProcessRunnerHelperProcess",
			"--",
			"descendant",
			arguments[1],
		)
		command.Env = append(os.Environ(), processRunnerHelperEnvironment+"=process-runner-helper-enabled")
		if err := command.Start(); err != nil {
			os.Exit(6)
		}
		if err := command.Wait(); err != nil {
			os.Exit(7)
		}
	case "spawn-descendant-exit":
		command := exec.Command(
			os.Args[0],
			"-test.run=TestProcessRunnerHelperProcess",
			"--",
			"descendant",
			arguments[1],
		)
		command.Env = append(os.Environ(), processRunnerHelperEnvironment+"=process-runner-helper-enabled")
		if err := command.Start(); err != nil {
			os.Exit(10)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(arguments[1]); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	case "descendant":
		if err := os.WriteFile(arguments[1], []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(8)
		}
		time.Sleep(30 * time.Second)
	default:
		os.Exit(9)
	}
	os.Exit(0)
}

func processRunnerTestExecutable(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	return executable
}

func processRunnerHelperArgs(mode string, arguments ...string) []string {
	result := []string{"-test.run=TestProcessRunnerHelperProcess", "--", mode}
	return append(result, arguments...)
}

func waitForProcessRunnerChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr != nil {
				t.Fatalf("parse descendant PID %q: %v", data, parseErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read descendant PID: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("descendant PID file %q was not created", path)
	return 0
}
