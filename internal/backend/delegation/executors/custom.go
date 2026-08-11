package executors

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cursor/internal/backend/delegation"
)

const (
	CustomOptionArguments        = "arguments"
	CustomOptionVersionArguments = "versionArguments"
	CustomOptionStdinMode        = "stdinMode"
	CustomOptionOutputMode       = "outputMode"
	CustomOptionFinalField       = "finalField"
	CustomOptionProgressField    = "progressField"
	CustomOptionErrorField       = "errorField"
	CustomOptionOutputLimitBytes = "outputLimitBytes"

	CustomDiagnosticReady        = "custom_ready"
	CustomDiagnosticNotInstalled = "custom_not_installed"
	CustomDiagnosticProbeFailed  = "custom_probe_failed"

	CustomErrorCodeInvalidConfig = "custom_invalid_config"
	CustomErrorCodeInvalidTask   = "custom_invalid_task"
	CustomErrorCodeProcessFailed = "custom_process_failed"
	CustomErrorCodeStreamInvalid = "custom_stream_invalid"
	CustomErrorCodeFinalMissing  = "custom_final_missing"

	customDefaultOutputLimit = 1024 * 1024
	customMaximumOutputLimit = 4 * 1024 * 1024
	customVisibleUpdateLimit = 4096
)

var customCapabilities = []delegation.ExecutorCapability{
	delegation.ExecutorCapabilityReadWorkspace,
	delegation.ExecutorCapabilityWriteWorkspace,
	delegation.ExecutorCapabilityShell,
	delegation.ExecutorCapabilityNetwork,
}

var customSecretLiteralPattern = regexp.MustCompile(`(?i)(?:\b(?:sk|rk|pk|ghp|github_pat|xox[baprs]-|AIza|AKIA)[a-z0-9_-]{8,}\b|\bBearer\s+[^\s]+|(?:api[_ -]?key|access[_ -]?token|secret|password)\s*[:=]\s*[^\s,;]+)`)

type customCLIContract struct {
	arguments, versionArguments []string
	stdinMode, outputMode       string
	finalField, progressField   string
	errorField                  string
	outputLimit                 int
}

type customCLIAdapter struct {
	runner       processRunner
	config       delegation.RuntimeExecutorConfig
	contract     customCLIContract
	probeTimeout time.Duration
	runTimeout   time.Duration
}

func NewCustomCLIRegistration(runner processRunner, config delegation.RuntimeExecutorConfig) (delegation.ExecutorRegistration, error) {
	if runner == nil {
		return delegation.ExecutorRegistration{}, errors.New("custom CLI process runner is required")
	}
	config.ID = delegation.ExecutorID(strings.TrimSpace(string(config.ID)))
	if config.ID == "" {
		return delegation.ExecutorRegistration{}, errors.New("custom CLI executor id is required")
	}
	if reservedCustomExecutorID(config.ID) {
		return delegation.ExecutorRegistration{}, fmt.Errorf("custom CLI executor id %q is reserved for a builtin executor", config.ID)
	}
	if strings.TrimSpace(config.Kind) != "custom" {
		return delegation.ExecutorRegistration{}, fmt.Errorf("custom CLI executor %q kind must be custom", config.ID)
	}
	config.Executable = strings.TrimSpace(config.Executable)
	if config.Executable == "" {
		return delegation.ExecutorRegistration{}, fmt.Errorf("custom CLI executor %q executable is required", config.ID)
	}
	contract, err := parseCustomCLIContract(config.Options)
	if err != nil {
		return delegation.ExecutorRegistration{}, fmt.Errorf("custom CLI executor %q: %w", config.ID, err)
	}
	adapter := &customCLIAdapter{runner: runner, config: config, contract: contract, probeTimeout: executorTimeout(config.ProbeTimeoutSeconds, 5*time.Second), runTimeout: executorTimeout(config.ExecutionTimeoutSeconds, 2*time.Minute)}
	displayName := firstNonEmpty(config.DisplayName, string(config.ID))
	if config.ID == "grok-cli" && strings.TrimSpace(config.DisplayName) == "" {
		displayName = "Grok Compatible"
	}
	return delegation.ExecutorRegistration{ID: config.ID, DisplayName: displayName, Enabled: config.Enabled, Priority: config.Priority, Capabilities: append([]delegation.ExecutorCapability{}, customCapabilities...), Probe: adapter.probe, Execute: adapter.execute}, nil
}

func parseCustomCLIContract(options map[string]string) (customCLIContract, error) {
	var contract customCLIContract
	allowed := map[string]struct{}{
		CustomOptionArguments: {}, CustomOptionVersionArguments: {}, CustomOptionStdinMode: {}, CustomOptionOutputMode: {},
		CustomOptionFinalField: {}, CustomOptionProgressField: {}, CustomOptionErrorField: {}, CustomOptionOutputLimitBytes: {},
	}
	for key, value := range options {
		if _, ok := allowed[key]; !ok {
			return contract, fmt.Errorf("custom option %q is not supported", key)
		}
		if customSecretLiteralPattern.MatchString(value) {
			return contract, fmt.Errorf("custom option %q contains a secret literal; use an environment variable name allowlist instead", key)
		}
	}
	var err error
	contract.arguments, err = parseCustomArgumentArray(options[CustomOptionArguments], true)
	if err != nil {
		return contract, fmt.Errorf("arguments: %w", err)
	}
	contract.versionArguments, err = parseCustomArgumentArray(options[CustomOptionVersionArguments], false)
	if err != nil {
		return contract, fmt.Errorf("versionArguments: %w", err)
	}
	for _, token := range []string{"{{prompt}}", "{{workspace}}", "{{readonly}}"} {
		if customArgumentsContain(contract.versionArguments, token) {
			return contract, errors.New("versionArguments cannot contain template variables")
		}
	}
	contract.stdinMode = firstNonEmpty(options[CustomOptionStdinMode], "none")
	if contract.stdinMode != "none" && contract.stdinMode != "prompt" {
		return contract, errors.New("stdinMode must be none or prompt")
	}
	contract.outputMode = firstNonEmpty(options[CustomOptionOutputMode], "text")
	if contract.outputMode != "text" && contract.outputMode != "jsonl" {
		return contract, errors.New("outputMode must be text or jsonl")
	}
	contract.finalField = strings.TrimSpace(options[CustomOptionFinalField])
	contract.progressField = strings.TrimSpace(options[CustomOptionProgressField])
	contract.errorField = strings.TrimSpace(options[CustomOptionErrorField])
	for key, value := range map[string]string{CustomOptionFinalField: contract.finalField, CustomOptionProgressField: contract.progressField, CustomOptionErrorField: contract.errorField} {
		if value != "" && !validCustomFieldPath(value) {
			return contract, fmt.Errorf("%s is invalid", key)
		}
	}
	if contract.outputMode == "jsonl" && contract.finalField == "" {
		return contract, errors.New("finalField is required for jsonl output")
	}
	contract.outputLimit = customDefaultOutputLimit
	if value := strings.TrimSpace(options[CustomOptionOutputLimitBytes]); value != "" {
		contract.outputLimit, err = strconv.Atoi(value)
		if err != nil || contract.outputLimit <= 0 || contract.outputLimit > customMaximumOutputLimit {
			return contract, fmt.Errorf("outputLimitBytes must be between 1 and %d", customMaximumOutputLimit)
		}
	}
	if contract.stdinMode != "prompt" && !customArgumentsContain(contract.arguments, "{{prompt}}") {
		return contract, errors.New("arguments must contain {{prompt}} when stdinMode is none")
	}
	return contract, nil
}

func reservedCustomExecutorID(id delegation.ExecutorID) bool {
	switch id {
	case ClaudeCodeExecutorID, CodexCLIExecutorID, GeminiCLIExecutorID, KiroCLIExecutorID, CursorExecutorID:
		return true
	default:
		return false
	}
}

func parseCustomArgumentArray(value string, required bool) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if required {
			return nil, errors.New("must be a JSON string array")
		}
		return nil, nil
	}
	var arguments []string
	if err := json.Unmarshal([]byte(value), &arguments); err != nil {
		return nil, fmt.Errorf("must be a JSON string array: %w", err)
	}
	for _, argument := range arguments {
		if strings.ContainsRune(argument, '\x00') {
			return nil, errors.New("contains a NUL byte")
		}
		if err := validateCustomTemplate(argument); err != nil {
			return nil, err
		}
	}
	return arguments, nil
}

func validateCustomTemplate(value string) error {
	for {
		start := strings.Index(value, "{{")
		if start < 0 {
			if strings.Contains(value, "}}") {
				return errors.New("template variable is malformed")
			}
			return nil
		}
		end := strings.Index(value[start+2:], "}}")
		if end < 0 {
			return errors.New("template variable is malformed")
		}
		token := value[start : start+2+end+2]
		switch token {
		case "{{prompt}}", "{{workspace}}", "{{readonly}}":
		default:
			return fmt.Errorf("unknown template variable %q", token)
		}
		value = value[start+2+end+2:]
	}
}

func customArgumentsContain(arguments []string, token string) bool {
	for _, argument := range arguments {
		if strings.Contains(argument, token) {
			return true
		}
	}
	return false
}

func validCustomFieldPath(value string) bool {
	for _, segment := range strings.Split(value, ".") {
		if segment == "" {
			return false
		}
		for _, char := range segment {
			valid := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-'
			if !valid {
				return false
			}
		}
	}
	return true
}

func (adapter *customCLIAdapter) probe(ctx context.Context) (delegation.ExecutorProbeResult, error) {
	probedAt := time.Now().UTC()
	if len(adapter.contract.versionArguments) == 0 {
		path, err := delegation.ResolveExecutable(adapter.config.Executable)
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				return adapter.notInstalledProbe(probedAt), nil
			}
			return adapter.unhealthyProbe("", probedAt, err), err
		}
		return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeReady, ExecutablePath: path, Installed: true, AuthState: delegation.ExecutorAuthUnknown, Capabilities: append([]delegation.ExecutorCapability{}, customCapabilities...), DiagnosticCode: CustomDiagnosticReady, ProbedAt: probedAt}, nil
	}
	result, err := adapter.runner.Run(ctx, delegation.ProcessRequest{Executable: adapter.config.Executable, Args: append([]string{}, adapter.contract.versionArguments...), InheritEnvironment: append([]string{}, adapter.config.EnvironmentVariables...), Timeout: adapter.probeTimeout, StdoutLimit: CLIProbeOutputLimit, StderrLimit: CLIProbeOutputLimit})
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || executorErrorCode(err) == delegation.ProcessErrorCodeNotFound {
			return adapter.notInstalledProbe(probedAt), nil
		}
		return adapter.unhealthyProbe(result.ExecutablePath, time.Now().UTC(), err), err
	}
	version := firstNonEmpty(result.Stdout, result.Stderr)
	if version == "" || result.StdoutTruncated || result.StderrTruncated {
		err = errors.New("custom CLI version output is empty or truncated")
		return adapter.unhealthyProbe(result.ExecutablePath, time.Now().UTC(), err), err
	}
	return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeReady, ExecutablePath: result.ExecutablePath, Version: version, Installed: true, AuthState: delegation.ExecutorAuthUnknown, Capabilities: append([]delegation.ExecutorCapability{}, customCapabilities...), DiagnosticCode: CustomDiagnosticReady, ProbedAt: time.Now().UTC()}, nil
}

func (adapter *customCLIAdapter) notInstalledProbe(probedAt time.Time) delegation.ExecutorProbeResult {
	return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeNotInstalled, AuthState: delegation.ExecutorAuthUnknown, Capabilities: append([]delegation.ExecutorCapability{}, customCapabilities...), DiagnosticCode: CustomDiagnosticNotInstalled, DiagnosticText: fmt.Sprintf("configured executable %q was not found", adapter.config.Executable), ProbedAt: probedAt}
}

func (adapter *customCLIAdapter) unhealthyProbe(path string, probedAt time.Time, err error) delegation.ExecutorProbeResult {
	return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeUnhealthy, ExecutablePath: path, Installed: path != "", AuthState: delegation.ExecutorAuthUnknown, Capabilities: append([]delegation.ExecutorCapability{}, customCapabilities...), DiagnosticCode: CustomDiagnosticProbeFailed, DiagnosticText: err.Error(), ProbedAt: probedAt}
}

func (adapter *customCLIAdapter) execute(ctx context.Context, request delegation.TaskRequest) delegation.TaskResult {
	prompt := strings.TrimSpace(request.Prompt)
	workspace := strings.TrimSpace(request.WorkspaceHint)
	if prompt == "" || workspace == "" {
		return delegation.TaskResult{Error: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureTerminal, false, CustomErrorCodeInvalidTask, errors.New("custom CLI task requires a prompt and workspace"))}
	}
	delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusDispatched, 1, nil, nil, "Custom CLI task dispatched", "")
	args := substituteCustomArguments(adapter.contract.arguments, prompt, workspace, request.Readonly)
	stdin := ""
	if adapter.contract.stdinMode == "prompt" {
		stdin = prompt
	}
	timeout := adapter.runTimeout
	if request.Timeout > 0 && request.Timeout < timeout {
		timeout = request.Timeout
	}
	processResult, processErr := adapter.runner.Run(ctx, delegation.ProcessRequest{Executable: adapter.config.Executable, Args: args, Stdin: stdin, Dir: workspace, InheritEnvironment: append([]string{}, adapter.config.EnvironmentVariables...), Timeout: timeout, StdoutLimit: adapter.contract.outputLimit, StderrLimit: adapter.contract.outputLimit, OnStdoutLine: func(line string) { publishCustomJSONLLine(ctx, adapter.contract, line) }})
	parsed, parseErr := adapter.parseOutput(processResult.Stdout, ctx, !processResult.StdoutStreamed)
	if processErr != nil {
		if errors.Is(processErr, context.Canceled) || errors.Is(processErr, context.DeadlineExceeded) {
			return delegation.TaskResult{Output: parsed.output, Error: processErr}
		}
		class, _ := delegation.ExecutorErrorClassification(processErr)
		if class != delegation.ExecutorFailureSwitchable {
			return delegation.TaskResult{Output: parsed.output, Error: processErr}
		}
		failure := firstNonEmpty(parsed.failure, processResult.Stderr, processErr.Error())
		return delegation.TaskResult{Output: parsed.output, Error: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, CustomErrorCodeProcessFailed, errors.New(failure))}
	}
	if parseErr != nil || processResult.StdoutTruncated {
		cause := parseErr
		if cause == nil {
			cause = errors.New("custom CLI output was truncated")
		}
		return delegation.TaskResult{Output: parsed.output, Error: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, CustomErrorCodeStreamInvalid, cause)}
	}
	if parsed.failure != "" {
		return delegation.TaskResult{Output: parsed.output, Error: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, CustomErrorCodeProcessFailed, errors.New(parsed.failure))}
	}
	if parsed.output == "" {
		return delegation.TaskResult{Error: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, CustomErrorCodeFinalMissing, errors.New("custom CLI returned no final output"))}
	}
	delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCompleted, 2, nil, nil, "Custom CLI task completed", "")
	return delegation.TaskResult{Output: parsed.output}
}

func substituteCustomArguments(arguments []string, prompt, workspace string, readonly bool) []string {
	result := make([]string, len(arguments))
	for index, argument := range arguments {
		argument = strings.ReplaceAll(argument, "{{prompt}}", prompt)
		argument = strings.ReplaceAll(argument, "{{workspace}}", workspace)
		argument = strings.ReplaceAll(argument, "{{readonly}}", strconv.FormatBool(readonly))
		result[index] = argument
	}
	return result
}

type customOutputResult struct{ output, failure string }

func (adapter *customCLIAdapter) parseOutput(output string, ctx context.Context, publishVisible bool) (customOutputResult, error) {
	if adapter.contract.outputMode == "text" {
		value := strings.TrimSpace(output)
		if publishVisible && value != "" {
			delegation.PublishWorkerVisibleUpdate(ctx, boundCustomVisibleUpdate(value))
		}
		return customOutputResult{output: value}, nil
	}
	var result customOutputResult
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), adapter.contract.outputLimit)
	line := 0
	for scanner.Scan() {
		line++
		value := strings.TrimSpace(scanner.Text())
		if value == "" {
			continue
		}
		var event any
		if err := json.Unmarshal([]byte(value), &event); err != nil {
			return result, fmt.Errorf("parse custom JSONL line %d: %w", line, err)
		}
		if publishVisible {
			if text, ok := customJSONPathString(event, adapter.contract.progressField); ok && text != "" {
				delegation.PublishWorkerVisibleUpdate(ctx, boundCustomVisibleUpdate(text))
			}
		}
		if text, ok := customJSONPathString(event, adapter.contract.errorField); ok && text != "" {
			result.failure = text
		}
		if text, ok := customJSONPathString(event, adapter.contract.finalField); ok && text != "" {
			result.output = text
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("scan custom JSONL: %w", err)
	}
	return result, nil
}

func publishCustomJSONLLine(ctx context.Context, contract customCLIContract, line string) {
	if contract.outputMode != "jsonl" {
		return
	}
	var event any
	if json.Unmarshal([]byte(strings.TrimSpace(line)), &event) != nil {
		return
	}
	for _, field := range []string{contract.progressField, contract.finalField} {
		if text, ok := customJSONPathString(event, field); ok && text != "" {
			delegation.PublishWorkerVisibleUpdate(ctx, boundCustomVisibleUpdate(text))
		}
	}
}

func customJSONPathString(root any, path string) (string, bool) {
	if path == "" {
		return "", false
	}
	current := root
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = object[segment]
		if !ok {
			return "", false
		}
	}
	value, ok := current.(string)
	return strings.TrimSpace(value), ok
}

func boundCustomVisibleUpdate(value string) string {
	runes := []rune(value)
	if len(runes) <= customVisibleUpdateLimit {
		return value
	}
	return string(runes[:customVisibleUpdateLimit])
}
