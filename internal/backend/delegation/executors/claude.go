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
	ClaudeCodeExecutorID delegation.ExecutorID = "claude-code"

	ClaudeDiagnosticReady        = "claude_ready"
	ClaudeDiagnosticNotInstalled = "claude_not_installed"
	ClaudeDiagnosticAuthRequired = "claude_auth_required"
	ClaudeDiagnosticProbeFailed  = "claude_probe_failed"

	ClaudeErrorCodeInvalidTask        = "claude_invalid_task"
	ClaudeErrorCodeAuthRequired       = "claude_auth_required"
	ClaudeErrorCodePermissionRequired = "claude_permission_required"
	ClaudeErrorCodeRateLimited        = "claude_rate_limited"
	ClaudeErrorCodeProcessFailed      = "claude_process_failed"
	ClaudeErrorCodeStreamInvalid      = "claude_stream_invalid"
	ClaudeErrorCodeExecutionFailed    = "claude_execution_failed"

	ClaudeMetadataVersionKey      = "claude_version"
	ClaudeMetadataSessionIDKey    = "claude_session_id"
	ClaudeMetadataInputTokensKey  = "claude_input_tokens"
	ClaudeMetadataOutputTokensKey = "claude_output_tokens"
	ClaudeMetadataCostUSDKey      = "claude_cost_usd"
	ClaudeMetadataContractKey     = "claude_command_contract"

	claudeCommandContract    = "print-stream-json-v2.1.226"
	claudeOutputLimit        = 4 * 1024 * 1024
	ClaudeVisibleUpdateLimit = 4096
)

var claudeCapabilities = []delegation.ExecutorCapability{
	delegation.ExecutorCapabilityReadWorkspace,
	delegation.ExecutorCapabilityWriteWorkspace,
	delegation.ExecutorCapabilityShell,
	delegation.ExecutorCapabilityNetwork,
	delegation.ExecutorCapabilityMCP,
}

type processRunner interface {
	Run(context.Context, delegation.ProcessRequest) (delegation.ProcessResult, error)
}

type claudeCodeAdapter struct {
	runner       processRunner
	config       delegation.RuntimeExecutorConfig
	executable   string
	probeTimeout time.Duration
	runTimeout   time.Duration
	versionMu    sync.RWMutex
	version      string
}

func NewClaudeCodeRegistration(runner processRunner, config delegation.RuntimeExecutorConfig) (delegation.ExecutorRegistration, error) {
	if runner == nil {
		return delegation.ExecutorRegistration{}, fmt.Errorf("Claude Code process runner is required")
	}
	if config.ID == "" {
		config.ID = ClaudeCodeExecutorID
	}
	if config.ID != ClaudeCodeExecutorID {
		return delegation.ExecutorRegistration{}, fmt.Errorf("Claude Code executor id %q is invalid", config.ID)
	}
	executable := strings.TrimSpace(config.Executable)
	if executable == "" {
		executable = defaultClaudeExecutable()
	}
	adapter := &claudeCodeAdapter{
		runner:       runner,
		config:       config,
		executable:   executable,
		probeTimeout: executorTimeout(config.ProbeTimeoutSeconds, 5*time.Second),
		runTimeout:   executorTimeout(config.ExecutionTimeoutSeconds, 2*time.Minute),
	}
	return delegation.ExecutorRegistration{
		ID:           ClaudeCodeExecutorID,
		DisplayName:  firstNonEmpty(strings.TrimSpace(config.DisplayName), "Claude Code"),
		Enabled:      config.Enabled,
		Priority:     config.Priority,
		Capabilities: append([]delegation.ExecutorCapability{}, claudeCapabilities...),
		Probe:        adapter.probe,
		Execute:      adapter.execute,
	}, nil
}

func (adapter *claudeCodeAdapter) probe(ctx context.Context) (delegation.ExecutorProbeResult, error) {
	versionResult, err := adapter.runner.Run(ctx, delegation.ProcessRequest{
		Executable:  adapter.executable,
		Args:        []string{"--version"},
		Timeout:     adapter.probeTimeout,
		StdoutLimit: CLIProbeOutputLimit,
		StderrLimit: CLIProbeOutputLimit,
	})
	probedAt := time.Now().UTC()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || executorErrorCode(err) == delegation.ProcessErrorCodeNotFound {
			return delegation.ExecutorProbeResult{
				State:          delegation.ExecutorProbeNotInstalled,
				AuthState:      delegation.ExecutorAuthUnknown,
				Capabilities:   append([]delegation.ExecutorCapability{}, claudeCapabilities...),
				DiagnosticCode: ClaudeDiagnosticNotInstalled,
				DiagnosticText: "Claude Code executable was not found",
				ProbedAt:       probedAt,
			}, nil
		}
		return adapter.unhealthyProbe(versionResult, probedAt, err), err
	}
	version := strings.TrimSpace(firstNonEmpty(versionResult.Stdout, versionResult.Stderr))
	if version == "" || versionResult.StdoutTruncated || versionResult.StderrTruncated {
		err = errors.New("Claude Code version output is empty or truncated")
		return adapter.unhealthyProbe(versionResult, probedAt, err), err
	}
	adapter.setVersion(version)
	authResult, err := adapter.runner.Run(ctx, delegation.ProcessRequest{
		Executable:  adapter.executable,
		Args:        []string{"auth", "status", "--json"},
		Timeout:     adapter.probeTimeout,
		StdoutLimit: CLIProbeOutputLimit,
		StderrLimit: CLIProbeOutputLimit,
	})
	if err != nil {
		return adapter.unhealthyProbe(authResult, time.Now().UTC(), err), err
	}
	var auth struct {
		LoggedIn bool   `json:"loggedIn"`
		Method   string `json:"authMethod"`
		Provider string `json:"apiProvider"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(authResult.Stdout)), &auth); err != nil {
		return adapter.unhealthyProbe(authResult, time.Now().UTC(), fmt.Errorf("parse Claude auth status: %w", err)), err
	}
	if !auth.LoggedIn {
		return delegation.ExecutorProbeResult{
			State:          delegation.ExecutorProbeActionRequired,
			ExecutablePath: firstNonEmpty(versionResult.ExecutablePath, authResult.ExecutablePath),
			Version:        version,
			Installed:      true,
			AuthState:      delegation.ExecutorAuthRequired,
			Capabilities:   append([]delegation.ExecutorCapability{}, claudeCapabilities...),
			DiagnosticCode: ClaudeDiagnosticAuthRequired,
			DiagnosticText: "Claude Code login is required",
			ProbedAt:       time.Now().UTC(),
		}, nil
	}
	return delegation.ExecutorProbeResult{
		State:          delegation.ExecutorProbeReady,
		ExecutablePath: firstNonEmpty(versionResult.ExecutablePath, authResult.ExecutablePath),
		Version:        version,
		Installed:      true,
		AuthState:      delegation.ExecutorAuthReady,
		Capabilities:   append([]delegation.ExecutorCapability{}, claudeCapabilities...),
		DiagnosticCode: ClaudeDiagnosticReady,
		ProbedAt:       time.Now().UTC(),
	}, nil
}

func (adapter *claudeCodeAdapter) unhealthyProbe(result delegation.ProcessResult, probedAt time.Time, err error) delegation.ExecutorProbeResult {
	return delegation.ExecutorProbeResult{
		State:          delegation.ExecutorProbeUnhealthy,
		ExecutablePath: result.ExecutablePath,
		Version:        adapter.getVersion(),
		Installed:      result.ExecutablePath != "",
		AuthState:      delegation.ExecutorAuthUnknown,
		Capabilities:   append([]delegation.ExecutorCapability{}, claudeCapabilities...),
		DiagnosticCode: ClaudeDiagnosticProbeFailed,
		DiagnosticText: strings.TrimSpace(firstNonEmpty(result.Stderr, err.Error())),
		ProbedAt:       probedAt,
	}
}

func (adapter *claudeCodeAdapter) execute(ctx context.Context, request delegation.TaskRequest) delegation.TaskResult {
	prompt := strings.TrimSpace(request.Prompt)
	workspace := strings.TrimSpace(request.WorkspaceHint)
	if prompt == "" || workspace == "" {
		return delegation.TaskResult{Error: delegation.NewClassifiedExecutorError(
			delegation.ExecutorFailureTerminal,
			false,
			ClaudeErrorCodeInvalidTask,
			errors.New("Claude Code task requires a prompt and workspace"),
		)}
	}
	delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusDispatched, 1, nil, nil, "Claude Code task dispatched", "")
	timeout := adapter.runTimeout
	if request.Timeout > 0 && request.Timeout < timeout {
		timeout = request.Timeout
	}
	permissionMode := "dontAsk"
	if request.Readonly {
		permissionMode = "plan"
	}
	processResult, processErr := adapter.runner.Run(ctx, delegation.ProcessRequest{
		Executable: adapter.executable,
		Args: []string{
			"-p",
			"--output-format", "stream-json",
			"--verbose",
			"--permission-mode", permissionMode,
			"--no-session-persistence",
			prompt,
		},
		Dir:                workspace,
		InheritEnvironment: append([]string{}, adapter.config.EnvironmentVariables...),
		Timeout:            timeout,
		StdoutLimit:        claudeOutputLimit,
		StderrLimit:        claudeOutputLimit,
		OnStdoutLine:       func(line string) { publishClaudeStreamLine(ctx, line) },
	})
	parsed, parseErr := parseClaudeStream(processResult.Stdout, ctx, !processResult.StdoutStreamed)
	if processErr != nil {
		if errors.Is(processErr, context.Canceled) || errors.Is(processErr, context.DeadlineExceeded) {
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCanceled, 2, nil, nil, "Claude Code task canceled", processErr.Error())
			return delegation.TaskResult{Output: parsed.output, Error: processErr, ToolCallCount: parsed.toolCalls, Metadata: parsed.metadata(adapter.getVersion())}
		}
		class, _ := delegation.ExecutorErrorClassification(processErr)
		if class != delegation.ExecutorFailureSwitchable {
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Claude Code process rejected", processErr.Error())
			return delegation.TaskResult{Output: parsed.output, Error: processErr, ToolCallCount: parsed.toolCalls, Metadata: parsed.metadata(adapter.getVersion())}
		}
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Claude Code task failed", firstNonEmpty(parsed.failure, processResult.Stderr, processErr.Error()))
		return delegation.TaskResult{
			Output:        parsed.output,
			Error:         classifyClaudeFailure(firstNonEmpty(parsed.failure, processResult.Stderr, processErr.Error()), ClaudeErrorCodeProcessFailed),
			ToolCallCount: parsed.toolCalls,
			Metadata:      parsed.metadata(adapter.getVersion()),
		}
	}
	if parseErr != nil || processResult.StdoutTruncated {
		cause := parseErr
		if cause == nil {
			cause = errors.New("Claude Code stream output was truncated")
		}
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Claude Code stream invalid", cause.Error())
		return delegation.TaskResult{
			Output:        parsed.output,
			Error:         delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, ClaudeErrorCodeStreamInvalid, cause),
			ToolCallCount: parsed.toolCalls,
			Metadata:      parsed.metadata(adapter.getVersion()),
		}
	}
	if parsed.failed {
		failure := firstNonEmpty(parsed.failure, "Claude Code execution failed without a diagnostic")
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Claude Code task failed", failure)
		return delegation.TaskResult{
			Output:        parsed.output,
			Error:         classifyClaudeFailure(failure, ClaudeErrorCodeExecutionFailed),
			ToolCallCount: parsed.toolCalls,
			Metadata:      parsed.metadata(adapter.getVersion()),
		}
	}
	if parsed.output == "" {
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Claude Code returned no final result", "missing final result")
		return delegation.TaskResult{
			Error:         delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, ClaudeErrorCodeStreamInvalid, errors.New("Claude Code stream did not contain a final result")),
			ToolCallCount: parsed.toolCalls,
			Metadata:      parsed.metadata(adapter.getVersion()),
		}
	}
	delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCompleted, 2, nil, nil, "Claude Code task completed", "")
	return delegation.TaskResult{Output: parsed.output, ToolCallCount: parsed.toolCalls, Metadata: parsed.metadata(adapter.getVersion())}
}

func (adapter *claudeCodeAdapter) setVersion(version string) {
	adapter.versionMu.Lock()
	adapter.version = version
	adapter.versionMu.Unlock()
}

func (adapter *claudeCodeAdapter) getVersion() string {
	adapter.versionMu.RLock()
	defer adapter.versionMu.RUnlock()
	return adapter.version
}

func executorErrorCode(err error) string {
	var coded interface{ Code() string }
	if errors.As(err, &coded) {
		return strings.TrimSpace(coded.Code())
	}
	return ""
}

type claudeStreamResult struct {
	output       string
	failure      string
	failed       bool
	sessionID    string
	inputTokens  int64
	outputTokens int64
	costUSD      float64
	toolCalls    int
}

func parseClaudeStream(output string, ctx context.Context, publishVisible bool) (claudeStreamResult, error) {
	var result claudeStreamResult
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), claudeOutputLimit)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			IsError   bool   `json:"is_error"`
			SessionID string `json:"session_id"`
			Result    string `json:"result"`
			Message   struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
			Usage struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
			CostUSD float64 `json:"total_cost_usd"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return result, fmt.Errorf("parse Claude stream line %d: %w", lineNumber, err)
		}
		if event.SessionID != "" {
			result.sessionID = event.SessionID
		}
		switch event.Type {
		case "assistant":
			for _, content := range event.Message.Content {
				switch content.Type {
				case "text":
					text := strings.TrimSpace(content.Text)
					if publishVisible && text != "" {
						delegation.PublishWorkerVisibleUpdate(ctx, boundClaudeVisibleUpdate(text))
					}
				case "tool_use":
					result.toolCalls++
				}
			}
		case "result":
			result.inputTokens = event.Usage.InputTokens
			result.outputTokens = event.Usage.OutputTokens
			result.costUSD = event.CostUSD
			if event.IsError || event.Subtype != "success" {
				result.failed = true
				result.failure = strings.TrimSpace(event.Result)
			} else {
				result.output = strings.TrimSpace(event.Result)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("scan Claude stream: %w", err)
	}
	return result, nil
}

func publishClaudeStreamLine(ctx context.Context, line string) {
	var event struct {
		Type    string `json:"type"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(line)), &event) != nil || event.Type != "assistant" {
		return
	}
	for _, content := range event.Message.Content {
		if content.Type == "text" {
			if text := strings.TrimSpace(content.Text); text != "" {
				delegation.PublishWorkerVisibleUpdate(ctx, boundClaudeVisibleUpdate(text))
			}
		}
	}
}

func boundClaudeVisibleUpdate(value string) string {
	runes := []rune(value)
	if len(runes) <= ClaudeVisibleUpdateLimit {
		return value
	}
	return string(runes[:ClaudeVisibleUpdateLimit])
}

func (result claudeStreamResult) metadata(version string) map[string]string {
	metadata := map[string]string{ClaudeMetadataContractKey: claudeCommandContract}
	if version != "" {
		metadata[ClaudeMetadataVersionKey] = version
	}
	if result.sessionID != "" {
		metadata[ClaudeMetadataSessionIDKey] = result.sessionID
	}
	if result.inputTokens > 0 {
		metadata[ClaudeMetadataInputTokensKey] = strconv.FormatInt(result.inputTokens, 10)
	}
	if result.outputTokens > 0 {
		metadata[ClaudeMetadataOutputTokensKey] = strconv.FormatInt(result.outputTokens, 10)
	}
	if result.costUSD > 0 {
		metadata[ClaudeMetadataCostUSDKey] = strconv.FormatFloat(result.costUSD, 'f', -1, 64)
	}
	return metadata
}

func classifyClaudeFailure(message string, fallbackCode string) error {
	message = strings.TrimSpace(message)
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "not logged in"), strings.Contains(lower, "run /login"), strings.Contains(lower, "authentication required"):
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureUserActionRequired, false, ClaudeErrorCodeAuthRequired, errors.New(message))
	case strings.Contains(lower, "permission"), strings.Contains(lower, "approval required"), strings.Contains(lower, "workspace trust"):
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureUserActionRequired, false, ClaudeErrorCodePermissionRequired, errors.New(message))
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "rate_limit"), strings.Contains(lower, "429"), strings.Contains(lower, "overloaded"):
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, ClaudeErrorCodeRateLimited, errors.New(message))
	default:
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, fallbackCode, errors.New(message))
	}
}

func defaultClaudeExecutable() string {
	if runtime.GOOS == "windows" {
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			candidate := filepath.Join(appData, "npm", "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return "claude"
}

func executorTimeout(seconds int, fallback time.Duration) time.Duration {
	if seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
