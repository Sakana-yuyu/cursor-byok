package executors

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"cursor/internal/backend/delegation"
)

const (
	GeminiCLIExecutorID delegation.ExecutorID = "gemini-cli"

	GeminiDiagnosticReady        = "gemini_ready"
	GeminiDiagnosticNotInstalled = "gemini_not_installed"
	GeminiDiagnosticIncompatible = "gemini_incompatible"
	GeminiDiagnosticProbeFailed  = "gemini_probe_failed"

	GeminiErrorCodeInvalidTask     = "gemini_invalid_task"
	GeminiErrorCodeAuthRequired    = "gemini_auth_required"
	GeminiErrorCodeRateLimited     = "gemini_rate_limited"
	GeminiErrorCodeProcessFailed   = "gemini_process_failed"
	GeminiErrorCodeStreamInvalid   = "gemini_stream_invalid"
	GeminiErrorCodeExecutionFailed = "gemini_execution_failed"

	GeminiMetadataVersionKey      = "gemini_version"
	GeminiMetadataSessionIDKey    = "gemini_session_id"
	GeminiMetadataModelKey        = "gemini_model"
	GeminiMetadataInputTokensKey  = "gemini_input_tokens"
	GeminiMetadataOutputTokensKey = "gemini_output_tokens"
	GeminiMetadataCachedTokensKey = "gemini_cached_tokens"
	GeminiMetadataContractKey     = "gemini_command_contract"

	geminiCommandContract    = "prompt-stream-json-approval-mode"
	geminiOutputLimit        = 4 * 1024 * 1024
	GeminiVisibleUpdateLimit = 4096
)

var geminiCapabilities = []delegation.ExecutorCapability{
	delegation.ExecutorCapabilityReadWorkspace,
	delegation.ExecutorCapabilityWriteWorkspace,
	delegation.ExecutorCapabilityShell,
	delegation.ExecutorCapabilityNetwork,
	delegation.ExecutorCapabilityMCP,
}

type geminiCLIAdapter struct {
	runner       processRunner
	config       delegation.RuntimeExecutorConfig
	executable   string
	prefixArgs   []string
	probeTimeout time.Duration
	runTimeout   time.Duration
	versionMu    sync.RWMutex
	version      string
}

func NewGeminiCLIRegistration(runner processRunner, config delegation.RuntimeExecutorConfig) (delegation.ExecutorRegistration, error) {
	if runner == nil {
		return delegation.ExecutorRegistration{}, errors.New("Gemini CLI process runner is required")
	}
	if config.ID == "" {
		config.ID = GeminiCLIExecutorID
	}
	if config.ID != GeminiCLIExecutorID {
		return delegation.ExecutorRegistration{}, fmt.Errorf("Gemini CLI executor id %q is invalid", config.ID)
	}
	executable := strings.TrimSpace(config.Executable)
	var prefixArgs []string
	if executable == "" {
		executable, prefixArgs = defaultGeminiCommand()
	}
	adapter := &geminiCLIAdapter{
		runner:       runner,
		config:       config,
		executable:   executable,
		prefixArgs:   prefixArgs,
		probeTimeout: executorTimeout(config.ProbeTimeoutSeconds, 5*time.Second),
		runTimeout:   executorTimeout(config.ExecutionTimeoutSeconds, 2*time.Minute),
	}
	return delegation.ExecutorRegistration{
		ID:           GeminiCLIExecutorID,
		DisplayName:  firstNonEmpty(config.DisplayName, "Gemini CLI"),
		Enabled:      config.Enabled,
		Priority:     config.Priority,
		Capabilities: append([]delegation.ExecutorCapability{}, geminiCapabilities...),
		Probe:        adapter.probe,
		Execute:      adapter.execute,
	}, nil
}

func (adapter *geminiCLIAdapter) probe(ctx context.Context) (delegation.ExecutorProbeResult, error) {
	versionResult, err := adapter.runProbe(ctx, "--version")
	probedAt := time.Now().UTC()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || executorErrorCode(err) == delegation.ProcessErrorCodeNotFound {
			return delegation.ExecutorProbeResult{
				State:          delegation.ExecutorProbeNotInstalled,
				AuthState:      delegation.ExecutorAuthUnknown,
				Capabilities:   append([]delegation.ExecutorCapability{}, geminiCapabilities...),
				DiagnosticCode: GeminiDiagnosticNotInstalled,
				DiagnosticText: "Gemini CLI executable was not found; install it with npm install -g @google/gemini-cli",
				ProbedAt:       probedAt,
			}, nil
		}
		return adapter.unhealthyProbe(versionResult, probedAt, err), err
	}
	version := strings.TrimSpace(firstNonEmpty(versionResult.Stdout, versionResult.Stderr))
	if version == "" || versionResult.StdoutTruncated || versionResult.StderrTruncated {
		err = errors.New("Gemini CLI version output is empty or truncated")
		return adapter.unhealthyProbe(versionResult, probedAt, err), err
	}
	adapter.setVersion(version)
	helpResult, err := adapter.runProbe(ctx, "--help")
	if err != nil {
		return adapter.unhealthyProbe(helpResult, time.Now().UTC(), err), err
	}
	help := strings.ToLower(firstNonEmpty(helpResult.Stdout, helpResult.Stderr))
	compatible := strings.Contains(help, "--prompt") && strings.Contains(help, "--output-format") && strings.Contains(help, "stream-json") && strings.Contains(help, "--approval-mode")
	if !compatible || helpResult.StdoutTruncated || helpResult.StderrTruncated {
		return delegation.ExecutorProbeResult{
			State:          delegation.ExecutorProbeIncompatible,
			ExecutablePath: firstNonEmpty(versionResult.ExecutablePath, helpResult.ExecutablePath),
			Version:        version,
			Installed:      true,
			AuthState:      delegation.ExecutorAuthUnknown,
			Capabilities:   append([]delegation.ExecutorCapability{}, geminiCapabilities...),
			DiagnosticCode: GeminiDiagnosticIncompatible,
			DiagnosticText: "Gemini CLI does not support the required stream-json headless contract",
			ProbedAt:       time.Now().UTC(),
		}, nil
	}
	return delegation.ExecutorProbeResult{
		State:          delegation.ExecutorProbeReady,
		ExecutablePath: firstNonEmpty(versionResult.ExecutablePath, helpResult.ExecutablePath),
		Version:        version,
		Installed:      true,
		AuthState:      delegation.ExecutorAuthUnknown,
		Capabilities:   append([]delegation.ExecutorCapability{}, geminiCapabilities...),
		DiagnosticCode: GeminiDiagnosticReady,
		ProbedAt:       time.Now().UTC(),
	}, nil
}

func (adapter *geminiCLIAdapter) runProbe(ctx context.Context, arg string) (delegation.ProcessResult, error) {
	args := append(append([]string{}, adapter.prefixArgs...), arg)
	return adapter.runner.Run(ctx, delegation.ProcessRequest{
		Executable: adapter.executable, Args: args, Timeout: adapter.probeTimeout,
		StdoutLimit: CLIProbeOutputLimit, StderrLimit: CLIProbeOutputLimit,
	})
}

func (adapter *geminiCLIAdapter) unhealthyProbe(result delegation.ProcessResult, probedAt time.Time, err error) delegation.ExecutorProbeResult {
	return delegation.ExecutorProbeResult{
		State: delegation.ExecutorProbeUnhealthy, ExecutablePath: result.ExecutablePath, Version: adapter.getVersion(),
		Installed: result.ExecutablePath != "", AuthState: delegation.ExecutorAuthUnknown,
		Capabilities:   append([]delegation.ExecutorCapability{}, geminiCapabilities...),
		DiagnosticCode: GeminiDiagnosticProbeFailed, DiagnosticText: firstNonEmpty(result.Stderr, err.Error()), ProbedAt: probedAt,
	}
}

func (adapter *geminiCLIAdapter) execute(ctx context.Context, request delegation.TaskRequest) delegation.TaskResult {
	prompt := strings.TrimSpace(request.Prompt)
	workspace := strings.TrimSpace(request.WorkspaceHint)
	if prompt == "" || workspace == "" {
		return delegation.TaskResult{Error: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureTerminal, false, GeminiErrorCodeInvalidTask, errors.New("Gemini CLI task requires a prompt and workspace"))}
	}
	delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusDispatched, 1, nil, nil, "Gemini CLI task dispatched", "")
	timeout := adapter.runTimeout
	if request.Timeout > 0 && request.Timeout < timeout {
		timeout = request.Timeout
	}
	approvalMode := "auto_edit"
	if request.Readonly {
		approvalMode = "plan"
	}
	args := append(append([]string{}, adapter.prefixArgs...), "-p", prompt, "--output-format", "stream-json", "--approval-mode", approvalMode)
	processResult, processErr := adapter.runner.Run(ctx, delegation.ProcessRequest{
		Executable: adapter.executable, Args: args, Dir: workspace,
		InheritEnvironment: append([]string{}, adapter.config.EnvironmentVariables...),
		Timeout:            timeout, StdoutLimit: geminiOutputLimit, StderrLimit: geminiOutputLimit,
		OnStdoutLine: func(line string) { publishGeminiStreamLine(ctx, line) },
	})
	parsed, parseErr := parseGeminiStream(processResult.Stdout, ctx, !processResult.StdoutStreamed)
	metadata := parsed.metadata(adapter.getVersion())
	if processErr != nil {
		if errors.Is(processErr, context.Canceled) || errors.Is(processErr, context.DeadlineExceeded) {
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCanceled, 2, nil, nil, "Gemini CLI task canceled", processErr.Error())
			return delegation.TaskResult{Output: parsed.output, Error: processErr, ToolCallCount: parsed.toolCalls, Metadata: metadata}
		}
		class, _ := delegation.ExecutorErrorClassification(processErr)
		if class != delegation.ExecutorFailureSwitchable {
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Gemini CLI process rejected", processErr.Error())
			return delegation.TaskResult{Output: parsed.output, Error: processErr, ToolCallCount: parsed.toolCalls, Metadata: metadata}
		}
		failure := firstNonEmpty(parsed.failure, processResult.Stderr, processErr.Error())
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Gemini CLI task failed", failure)
		return delegation.TaskResult{Output: parsed.output, Error: classifyGeminiFailure(failure, GeminiErrorCodeProcessFailed), ToolCallCount: parsed.toolCalls, Metadata: metadata}
	}
	if parseErr != nil || processResult.StdoutTruncated {
		cause := parseErr
		if cause == nil {
			cause = errors.New("Gemini CLI JSONL output was truncated")
		}
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Gemini CLI stream invalid", cause.Error())
		return delegation.TaskResult{Output: parsed.output, Error: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, GeminiErrorCodeStreamInvalid, cause), ToolCallCount: parsed.toolCalls, Metadata: metadata}
	}
	if parsed.failed {
		failure := firstNonEmpty(parsed.failure, "Gemini CLI execution failed without a diagnostic")
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Gemini CLI task failed", failure)
		return delegation.TaskResult{Output: parsed.output, Error: classifyGeminiFailure(failure, GeminiErrorCodeExecutionFailed), ToolCallCount: parsed.toolCalls, Metadata: metadata}
	}
	if !parsed.completed || parsed.output == "" {
		err := errors.New("Gemini CLI stream did not contain a successful result with an assistant message")
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Gemini CLI returned no final result", err.Error())
		return delegation.TaskResult{Output: parsed.output, Error: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, GeminiErrorCodeStreamInvalid, err), ToolCallCount: parsed.toolCalls, Metadata: metadata}
	}
	delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCompleted, 2, nil, nil, "Gemini CLI task completed", "")
	return delegation.TaskResult{Output: parsed.output, ToolCallCount: parsed.toolCalls, Metadata: metadata}
}

type geminiStreamResult struct {
	output, failure, sessionID, model       string
	inputTokens, outputTokens, cachedTokens int64
	toolCalls                               int
	completed, failed                       bool
}

func parseGeminiStream(output string, ctx context.Context, publishVisible bool) (geminiStreamResult, error) {
	var result geminiStreamResult
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), geminiOutputLimit)
	line := 0
	for scanner.Scan() {
		line++
		value := strings.TrimSpace(scanner.Text())
		if value == "" {
			continue
		}
		var event struct {
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
			Model     string `json:"model"`
			Role      string `json:"role"`
			Content   string `json:"content"`
			Delta     bool   `json:"delta"`
			Severity  string `json:"severity"`
			Message   string `json:"message"`
			Status    string `json:"status"`
			Error     string `json:"error"`
			Stats     struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
				Cached       int64 `json:"cached"`
				ToolCalls    int   `json:"tool_calls"`
			} `json:"stats"`
		}
		if err := json.Unmarshal([]byte(value), &event); err != nil {
			return result, fmt.Errorf("parse Gemini JSONL line %d: %w", line, err)
		}
		switch event.Type {
		case "init":
			result.sessionID = event.SessionID
			result.model = event.Model
		case "message":
			if event.Role == "assistant" && event.Content != "" {
				if event.Delta {
					result.output += event.Content
				} else {
					result.output = event.Content
				}
				if publishVisible {
					delegation.PublishWorkerVisibleUpdate(ctx, boundGeminiVisibleUpdate(event.Content))
				}
			}
		case "tool_use":
			result.toolCalls++
		case "error":
			if strings.EqualFold(event.Severity, "error") {
				result.failed = true
				result.failure = strings.TrimSpace(event.Message)
			}
		case "result":
			result.inputTokens = event.Stats.InputTokens
			result.outputTokens = event.Stats.OutputTokens
			result.cachedTokens = event.Stats.Cached
			if event.Stats.ToolCalls > result.toolCalls {
				result.toolCalls = event.Stats.ToolCalls
			}
			if event.Status == "success" {
				result.completed = true
			} else {
				result.failed = true
				result.failure = firstNonEmpty(event.Error, result.failure)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("scan Gemini JSONL: %w", err)
	}
	result.output = strings.TrimSpace(result.output)
	return result, nil
}

func publishGeminiStreamLine(ctx context.Context, line string) {
	var event struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(line)), &event) != nil || event.Type != "message" || event.Role != "assistant" {
		return
	}
	if text := strings.TrimSpace(event.Content); text != "" {
		delegation.PublishWorkerVisibleUpdate(ctx, boundGeminiVisibleUpdate(text))
	}
}

func (result geminiStreamResult) metadata(version string) map[string]string {
	metadata := map[string]string{GeminiMetadataContractKey: geminiCommandContract}
	if version != "" {
		metadata[GeminiMetadataVersionKey] = version
	}
	if result.sessionID != "" {
		metadata[GeminiMetadataSessionIDKey] = result.sessionID
	}
	if result.model != "" {
		metadata[GeminiMetadataModelKey] = result.model
	}
	if result.inputTokens > 0 {
		metadata[GeminiMetadataInputTokensKey] = strconv.FormatInt(result.inputTokens, 10)
	}
	if result.outputTokens > 0 {
		metadata[GeminiMetadataOutputTokensKey] = strconv.FormatInt(result.outputTokens, 10)
	}
	if result.cachedTokens > 0 {
		metadata[GeminiMetadataCachedTokensKey] = strconv.FormatInt(result.cachedTokens, 10)
	}
	return metadata
}

func classifyGeminiFailure(message, fallbackCode string) error {
	message = firstNonEmpty(message, "Gemini CLI execution failed without a diagnostic")
	lower := strings.ToLower(message)
	switch {
	case geminiAuthenticationRequired(lower):
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureUserActionRequired, false, GeminiErrorCodeAuthRequired, errors.New(message))
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "429"), strings.Contains(lower, "quota"), strings.Contains(lower, "resource exhausted"), strings.Contains(lower, "resource_exhausted"):
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, GeminiErrorCodeRateLimited, errors.New(message))
	default:
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, fallbackCode, errors.New(message))
	}
}

func geminiAuthenticationRequired(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "authentication required") || strings.Contains(lower, "sign in") || strings.Contains(lower, "gemini_api_key") || strings.Contains(lower, "google_api_key") || strings.Contains(lower, "oauth")
}

func boundGeminiVisibleUpdate(value string) string {
	runes := []rune(value)
	if len(runes) <= GeminiVisibleUpdateLimit {
		return value
	}
	return string(runes[:GeminiVisibleUpdateLimit])
}

func (adapter *geminiCLIAdapter) setVersion(version string) {
	adapter.versionMu.Lock()
	adapter.version = version
	adapter.versionMu.Unlock()
}

func (adapter *geminiCLIAdapter) getVersion() string {
	adapter.versionMu.RLock()
	defer adapter.versionMu.RUnlock()
	return adapter.version
}

func defaultGeminiCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		appData := strings.TrimSpace(os.Getenv("APPDATA"))
		if appData != "" {
			base := filepath.Join(appData, "npm", "node_modules", "@google", "gemini-cli")
			for _, relative := range []string{filepath.Join("dist", "index.js"), filepath.Join("bundle", "gemini.js")} {
				script := filepath.Join(base, relative)
				if info, err := os.Stat(script); err == nil && !info.IsDir() {
					return "node", []string{script}
				}
			}
		}
	}
	return "gemini", nil
}
