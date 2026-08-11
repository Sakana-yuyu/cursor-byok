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
	CodexCLIExecutorID delegation.ExecutorID = "codex-cli"

	CodexDiagnosticReady        = "codex_ready"
	CodexDiagnosticNotInstalled = "codex_not_installed"
	CodexDiagnosticAuthRequired = "codex_auth_required"
	CodexDiagnosticProbeFailed  = "codex_probe_failed"

	CodexErrorCodeInvalidTask      = "codex_invalid_task"
	CodexErrorCodeAuthRequired     = "codex_auth_required"
	CodexErrorCodeApprovalRequired = "codex_approval_required"
	CodexErrorCodeSandboxRequired  = "codex_sandbox_required"
	CodexErrorCodeRateLimited      = "codex_rate_limited"
	CodexErrorCodeProcessFailed    = "codex_process_failed"
	CodexErrorCodeStreamInvalid    = "codex_stream_invalid"
	CodexErrorCodeExecutionFailed  = "codex_execution_failed"

	CodexMetadataVersionKey         = "codex_version"
	CodexMetadataThreadIDKey        = "codex_thread_id"
	CodexMetadataInputTokensKey     = "codex_input_tokens"
	CodexMetadataCachedTokensKey    = "codex_cached_input_tokens"
	CodexMetadataOutputTokensKey    = "codex_output_tokens"
	CodexMetadataReasoningTokensKey = "codex_reasoning_output_tokens"
	CodexMetadataContractKey        = "codex_command_contract"
	CodexCLIInstallURL              = "https://developers.openai.com/codex/cli/"

	codexCommandContract    = "exec-jsonl-v0.147.0"
	codexOutputLimit        = 4 * 1024 * 1024
	CodexVisibleUpdateLimit = 4096
)

var codexCapabilities = []delegation.ExecutorCapability{
	delegation.ExecutorCapabilityReadWorkspace,
	delegation.ExecutorCapabilityWriteWorkspace,
	delegation.ExecutorCapabilityShell,
	delegation.ExecutorCapabilityNetwork,
	delegation.ExecutorCapabilityMCP,
}

type codexCLIAdapter struct {
	runner       processRunner
	config       delegation.RuntimeExecutorConfig
	executable   string
	probeTimeout time.Duration
	runTimeout   time.Duration
	versionMu    sync.RWMutex
	version      string
}

func NewCodexCLIRegistration(runner processRunner, config delegation.RuntimeExecutorConfig) (delegation.ExecutorRegistration, error) {
	if runner == nil {
		return delegation.ExecutorRegistration{}, errors.New("Codex CLI process runner is required")
	}
	if config.ID == "" {
		config.ID = CodexCLIExecutorID
	}
	if config.ID != CodexCLIExecutorID {
		return delegation.ExecutorRegistration{}, fmt.Errorf("Codex CLI executor id %q is invalid", config.ID)
	}
	executable := strings.TrimSpace(config.Executable)
	if executable == "" {
		executable = defaultCodexExecutable()
	}
	adapter := &codexCLIAdapter{
		runner: runner, config: config, executable: executable,
		probeTimeout: executorTimeout(config.ProbeTimeoutSeconds, 5*time.Second),
		runTimeout:   executorTimeout(config.ExecutionTimeoutSeconds, 2*time.Minute),
	}
	return delegation.ExecutorRegistration{
		ID: CodexCLIExecutorID, DisplayName: firstNonEmpty(config.DisplayName, "Codex CLI"), InstallURL: CodexCLIInstallURL,
		Enabled: config.Enabled, Priority: config.Priority,
		Capabilities: append([]delegation.ExecutorCapability{}, codexCapabilities...),
		Probe:        adapter.probe, Execute: adapter.execute,
	}, nil
}

func (adapter *codexCLIAdapter) probe(ctx context.Context) (delegation.ExecutorProbeResult, error) {
	versionResult, err := adapter.runner.Run(ctx, delegation.ProcessRequest{
		Executable: adapter.executable, Args: []string{"--version"}, Timeout: adapter.probeTimeout,
		StdoutLimit: CLIProbeOutputLimit, StderrLimit: CLIProbeOutputLimit,
	})
	probedAt := time.Now().UTC()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || executorErrorCode(err) == delegation.ProcessErrorCodeNotFound {
			return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeNotInstalled, AuthState: delegation.ExecutorAuthUnknown, Capabilities: append([]delegation.ExecutorCapability{}, codexCapabilities...), DiagnosticCode: CodexDiagnosticNotInstalled, DiagnosticText: "Codex CLI executable was not found", ProbedAt: probedAt}, nil
		}
		return adapter.unhealthyProbe(versionResult, probedAt, err), err
	}
	version := firstNonEmpty(versionResult.Stdout, versionResult.Stderr)
	if version == "" || versionResult.StdoutTruncated || versionResult.StderrTruncated {
		err = errors.New("Codex CLI version output is empty or truncated")
		return adapter.unhealthyProbe(versionResult, probedAt, err), err
	}
	adapter.setVersion(version)
	authResult, authErr := adapter.runner.Run(ctx, delegation.ProcessRequest{
		Executable: adapter.executable, Args: []string{"login", "status"}, Timeout: adapter.probeTimeout,
		StdoutLimit: CLIProbeOutputLimit, StderrLimit: CLIProbeOutputLimit,
	})
	authText := firstNonEmpty(authResult.Stdout, authResult.Stderr)
	if authErr != nil {
		if codexAuthenticationRequired(authText + " " + authErr.Error()) {
			return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeActionRequired, ExecutablePath: firstNonEmpty(versionResult.ExecutablePath, authResult.ExecutablePath), Version: version, Installed: true, AuthState: delegation.ExecutorAuthRequired, Capabilities: append([]delegation.ExecutorCapability{}, codexCapabilities...), DiagnosticCode: CodexDiagnosticAuthRequired, DiagnosticText: "Codex CLI login is required", ProbedAt: time.Now().UTC()}, nil
		}
		return adapter.unhealthyProbe(authResult, time.Now().UTC(), authErr), authErr
	}
	if codexAuthenticationRequired(authText) {
		return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeActionRequired, ExecutablePath: firstNonEmpty(versionResult.ExecutablePath, authResult.ExecutablePath), Version: version, Installed: true, AuthState: delegation.ExecutorAuthRequired, Capabilities: append([]delegation.ExecutorCapability{}, codexCapabilities...), DiagnosticCode: CodexDiagnosticAuthRequired, DiagnosticText: "Codex CLI login is required", ProbedAt: time.Now().UTC()}, nil
	}
	if !codexAuthenticationReady(authText) {
		err := fmt.Errorf("Codex CLI login status output is not recognized: %q", authText)
		return adapter.unhealthyProbe(authResult, time.Now().UTC(), err), err
	}
	return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeReady, ExecutablePath: firstNonEmpty(versionResult.ExecutablePath, authResult.ExecutablePath), Version: version, Installed: true, AuthState: delegation.ExecutorAuthReady, Capabilities: append([]delegation.ExecutorCapability{}, codexCapabilities...), DiagnosticCode: CodexDiagnosticReady, ProbedAt: time.Now().UTC()}, nil
}

func (adapter *codexCLIAdapter) unhealthyProbe(result delegation.ProcessResult, probedAt time.Time, err error) delegation.ExecutorProbeResult {
	return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeUnhealthy, ExecutablePath: result.ExecutablePath, Version: adapter.getVersion(), Installed: result.ExecutablePath != "", AuthState: delegation.ExecutorAuthUnknown, Capabilities: append([]delegation.ExecutorCapability{}, codexCapabilities...), DiagnosticCode: CodexDiagnosticProbeFailed, DiagnosticText: firstNonEmpty(result.Stderr, err.Error()), ProbedAt: probedAt}
}

func (adapter *codexCLIAdapter) execute(ctx context.Context, request delegation.TaskRequest) delegation.TaskResult {
	prompt := strings.TrimSpace(request.Prompt)
	workspace := strings.TrimSpace(request.WorkspaceHint)
	if prompt == "" || workspace == "" {
		return delegation.TaskResult{Error: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureTerminal, false, CodexErrorCodeInvalidTask, errors.New("Codex CLI task requires a prompt and workspace"))}
	}
	delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusDispatched, 1, nil, nil, "Codex CLI task dispatched", "")
	timeout := adapter.runTimeout
	if request.Timeout > 0 && request.Timeout < timeout {
		timeout = request.Timeout
	}
	sandbox := "workspace-write"
	if request.Readonly {
		sandbox = "read-only"
	}
	args := []string{"--sandbox", sandbox, "--ask-for-approval", "never", "-C", workspace, "exec", "--json", "--ephemeral", "--color", "never", prompt}
	processResult, processErr := adapter.runner.Run(ctx, delegation.ProcessRequest{Executable: adapter.executable, Args: args, Dir: workspace, InheritEnvironment: append([]string{}, adapter.config.EnvironmentVariables...), Timeout: timeout, StdoutLimit: codexOutputLimit, StderrLimit: codexOutputLimit, OnStdoutLine: func(line string) { publishCodexStreamLine(ctx, line) }})
	parsed, parseErr := parseCodexStream(processResult.Stdout, ctx, !processResult.StdoutStreamed)
	metadata := parsed.metadata(adapter.getVersion())
	if processErr != nil {
		if errors.Is(processErr, context.Canceled) || errors.Is(processErr, context.DeadlineExceeded) {
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCanceled, 2, nil, nil, "Codex CLI task canceled", processErr.Error())
			return delegation.TaskResult{Output: parsed.output, Error: processErr, ToolCallCount: parsed.toolCalls, Metadata: metadata}
		}
		class, _ := delegation.ExecutorErrorClassification(processErr)
		if class != delegation.ExecutorFailureSwitchable {
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Codex CLI process rejected", processErr.Error())
			return delegation.TaskResult{Output: parsed.output, Error: processErr, ToolCallCount: parsed.toolCalls, Metadata: metadata}
		}
		failure := firstNonEmpty(parsed.failure, processResult.Stderr, processErr.Error())
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Codex CLI task failed", failure)
		return delegation.TaskResult{Output: parsed.output, Error: classifyCodexFailure(failure, CodexErrorCodeProcessFailed), ToolCallCount: parsed.toolCalls, Metadata: metadata}
	}
	if parseErr != nil || processResult.StdoutTruncated {
		cause := parseErr
		if cause == nil {
			cause = errors.New("Codex CLI JSONL output was truncated")
		}
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Codex CLI stream invalid", cause.Error())
		return delegation.TaskResult{Output: parsed.output, Error: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, CodexErrorCodeStreamInvalid, cause), ToolCallCount: parsed.toolCalls, Metadata: metadata}
	}
	if parsed.failed {
		failure := firstNonEmpty(parsed.failure, "Codex CLI execution failed without a diagnostic")
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Codex CLI task failed", failure)
		return delegation.TaskResult{Output: parsed.output, Error: classifyCodexFailure(failure, CodexErrorCodeExecutionFailed), ToolCallCount: parsed.toolCalls, Metadata: metadata}
	}
	if !parsed.completed || parsed.output == "" {
		err := errors.New("Codex CLI stream did not contain a completed turn with a final message")
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 2, nil, nil, "Codex CLI returned no final result", err.Error())
		return delegation.TaskResult{Error: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, CodexErrorCodeStreamInvalid, err), ToolCallCount: parsed.toolCalls, Metadata: metadata}
	}
	delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCompleted, 2, nil, nil, "Codex CLI task completed", "")
	return delegation.TaskResult{Output: parsed.output, ToolCallCount: parsed.toolCalls, Metadata: metadata}
}

type codexStreamResult struct {
	output, failure, threadID                                string
	inputTokens, cachedTokens, outputTokens, reasoningTokens int64
	toolCalls                                                int
	completed, failed                                        bool
}

func parseCodexStream(output string, ctx context.Context, publishVisible bool) (codexStreamResult, error) {
	var result codexStreamResult
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), codexOutputLimit)
	line := 0
	for scanner.Scan() {
		line++
		value := strings.TrimSpace(scanner.Text())
		if value == "" {
			continue
		}
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Message  string `json:"message"`
			Item     struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Status string `json:"status"`
			} `json:"item"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Usage struct {
				InputTokens           int64 `json:"input_tokens"`
				CachedInputTokens     int64 `json:"cached_input_tokens"`
				OutputTokens          int64 `json:"output_tokens"`
				ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(value), &event); err != nil {
			return result, fmt.Errorf("parse Codex JSONL line %d: %w", line, err)
		}
		switch event.Type {
		case "thread.started":
			result.threadID = event.ThreadID
		case "item.completed":
			if event.Item.Type == "agent_message" {
				result.output = strings.TrimSpace(event.Item.Text)
				if publishVisible && result.output != "" {
					delegation.PublishWorkerVisibleUpdate(ctx, boundCodexVisibleUpdate(result.output))
				}
			} else if codexToolItem(event.Item.Type) {
				result.toolCalls++
			}
		case "turn.completed":
			result.completed = true
			result.inputTokens = event.Usage.InputTokens
			result.cachedTokens = event.Usage.CachedInputTokens
			result.outputTokens = event.Usage.OutputTokens
			result.reasoningTokens = event.Usage.ReasoningOutputTokens
		case "turn.failed":
			result.failed = true
			result.failure = strings.TrimSpace(event.Error.Message)
		case "error":
			result.failed = true
			result.failure = strings.TrimSpace(firstNonEmpty(event.Message, event.Error.Message))
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("scan Codex JSONL: %w", err)
	}
	return result, nil
}

func publishCodexStreamLine(ctx context.Context, line string) {
	var event struct {
		Type string `json:"type"`
		Item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(line)), &event) != nil || event.Type != "item.completed" || event.Item.Type != "agent_message" {
		return
	}
	if text := strings.TrimSpace(event.Item.Text); text != "" {
		delegation.PublishWorkerVisibleUpdate(ctx, boundCodexVisibleUpdate(text))
	}
}

func codexToolItem(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "command_execution", "file_change", "mcp_tool_call", "web_search", "computer_use":
		return true
	default:
		return false
	}
}

func (result codexStreamResult) metadata(version string) map[string]string {
	metadata := map[string]string{CodexMetadataContractKey: codexCommandContract}
	if version != "" {
		metadata[CodexMetadataVersionKey] = version
	}
	if result.threadID != "" {
		metadata[CodexMetadataThreadIDKey] = result.threadID
	}
	if result.inputTokens > 0 {
		metadata[CodexMetadataInputTokensKey] = strconv.FormatInt(result.inputTokens, 10)
	}
	if result.cachedTokens > 0 {
		metadata[CodexMetadataCachedTokensKey] = strconv.FormatInt(result.cachedTokens, 10)
	}
	if result.outputTokens > 0 {
		metadata[CodexMetadataOutputTokensKey] = strconv.FormatInt(result.outputTokens, 10)
	}
	if result.reasoningTokens > 0 {
		metadata[CodexMetadataReasoningTokensKey] = strconv.FormatInt(result.reasoningTokens, 10)
	}
	return metadata
}

func classifyCodexFailure(message, fallbackCode string) error {
	message = firstNonEmpty(message, "Codex CLI execution failed without a diagnostic")
	lower := strings.ToLower(message)
	switch {
	case codexAuthenticationRequired(lower):
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureUserActionRequired, false, CodexErrorCodeAuthRequired, errors.New(message))
	case strings.Contains(lower, "approval"), strings.Contains(lower, "requires confirmation"):
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureUserActionRequired, false, CodexErrorCodeApprovalRequired, errors.New(message))
	case strings.Contains(lower, "sandbox"), strings.Contains(lower, "read-only filesystem"), strings.Contains(lower, "permission denied"):
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureUserActionRequired, false, CodexErrorCodeSandboxRequired, errors.New(message))
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "rate_limit"), strings.Contains(lower, "429"), strings.Contains(lower, "overloaded"):
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, CodexErrorCodeRateLimited, errors.New(message))
	default:
		return delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, fallbackCode, errors.New(message))
	}
}

func codexAuthenticationRequired(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "not logged in") || strings.Contains(lower, "codex login") || strings.Contains(lower, "authentication required") || strings.Contains(lower, "login required")
}

func codexAuthenticationReady(message string) bool {
	return strings.Contains(strings.ToLower(message), "logged in")
}

func boundCodexVisibleUpdate(value string) string {
	runes := []rune(value)
	if len(runes) <= CodexVisibleUpdateLimit {
		return value
	}
	return string(runes[:CodexVisibleUpdateLimit])
}

func (adapter *codexCLIAdapter) setVersion(version string) {
	adapter.versionMu.Lock()
	adapter.version = version
	adapter.versionMu.Unlock()
}
func (adapter *codexCLIAdapter) getVersion() string {
	adapter.versionMu.RLock()
	defer adapter.versionMu.RUnlock()
	return adapter.version
}

func defaultCodexExecutable() string {
	if runtime.GOOS == "windows" {
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			candidate := filepath.Join(appData, "npm", "node_modules", "@openai", "codex", "node_modules", "@openai", "codex-win32-x64", "vendor", "x86_64-pc-windows-msvc", "bin", "codex.exe")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return "codex"
}
