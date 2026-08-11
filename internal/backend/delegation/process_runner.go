package delegation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultProcessTimeout               = 2 * time.Minute
	DefaultProcessOutputLimit           = 256 * 1024
	MaximumProcessOutputLimit           = 4 * 1024 * 1024
	MaximumProcessStdinBytes            = 4 * 1024 * 1024
	MaximumProcessEnvironmentEntries    = 128
	MaximumProcessEnvironmentValueBytes = 32 * 1024
	MaximumProcessEnvironmentBytes      = 256 * 1024
	DefaultProcessWaitDelay             = time.Second
	processOutputRedactionLookahead     = 4 * 1024

	ProcessErrorCodeInvalidRequest    = "process_invalid_request"
	ProcessErrorCodeNotFound          = "process_not_found"
	ProcessErrorCodeStartFailed       = "process_start_failed"
	ProcessErrorCodeExitFailed        = "process_exit_failed"
	ProcessErrorCodeTimeout           = "process_timeout"
	ProcessErrorCodeCanceled          = "process_canceled"
	ProcessErrorCodeTreeSetupFailed   = "process_tree_setup_failed"
	ProcessErrorCodeTreeCleanupFailed = "process_tree_cleanup_failed"
)

var defaultProcessEnvironmentAllowlist = []string{
	"APPDATA",
	"HOME",
	"LANG",
	"LC_ALL",
	"LOCALAPPDATA",
	"PATH",
	"PATHEXT",
	"SYSTEMROOT",
	"TEMP",
	"TMP",
	"TMPDIR",
	"USERPROFILE",
	"WINDIR",
	"XDG_CACHE_HOME",
	"XDG_CONFIG_HOME",
	"XDG_DATA_HOME",
}

type ProcessRunnerConfig struct {
	DefaultTimeout     time.Duration
	DefaultStdoutLimit int
	DefaultStderrLimit int
	WaitDelay          time.Duration
}

type ProcessRequest struct {
	Executable         string
	Args               []string
	Stdin              string
	Dir                string
	Env                map[string]string
	InheritEnvironment []string
	Timeout            time.Duration
	StdoutLimit        int
	StderrLimit        int
	OnStdoutLine       func(string)
}

type ProcessResult struct {
	ExecutablePath  string
	Dir             string
	Stdout          string
	Stderr          string
	StdoutTruncated bool
	StderrTruncated bool
	ExitCode        int
	StartedAt       time.Time
	FinishedAt      time.Time
	StdoutStreamed  bool
}

type processTreeController interface {
	Attach(*os.Process) error
	Kill() error
	Close() error
}

type ProcessRunner struct {
	config ProcessRunnerConfig
}

func NewProcessRunner(config ProcessRunnerConfig) *ProcessRunner {
	if config.DefaultTimeout <= 0 {
		config.DefaultTimeout = DefaultProcessTimeout
	}
	if config.DefaultStdoutLimit <= 0 {
		config.DefaultStdoutLimit = DefaultProcessOutputLimit
	}
	if config.DefaultStderrLimit <= 0 {
		config.DefaultStderrLimit = DefaultProcessOutputLimit
	}
	if config.WaitDelay <= 0 {
		config.WaitDelay = DefaultProcessWaitDelay
	}
	return &ProcessRunner{config: config}
}

func ResolveExecutable(executable string) (string, error) {
	executable = strings.TrimSpace(executable)
	if executable == "" || strings.ContainsRune(executable, '\x00') {
		return "", fmt.Errorf("executable is empty or invalid")
	}
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return "", fmt.Errorf("resolve executable %q: %w", executable, err)
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve executable path %q: %w", resolved, err)
	}
	return filepath.Clean(absolute), nil
}

func (runner *ProcessRunner) Run(ctx context.Context, request ProcessRequest) (ProcessResult, error) {
	if runner == nil {
		return ProcessResult{}, processInvalidRequestError("process runner is nil")
	}
	if ctx == nil {
		return ProcessResult{}, processInvalidRequestError("process context is nil")
	}
	resolved, err := ResolveExecutable(request.Executable)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ProcessResult{}, NewClassifiedExecutorError(
				ExecutorFailureSwitchable,
				true,
				ProcessErrorCodeNotFound,
				err,
			)
		}
		return ProcessResult{}, processInvalidRequestError(err.Error())
	}
	directory, err := resolveProcessDirectory(request.Dir)
	if err != nil {
		return ProcessResult{}, processInvalidRequestError(err.Error())
	}
	environment, redactions, err := buildProcessEnvironment(request)
	if err != nil {
		return ProcessResult{}, processInvalidRequestError(err.Error())
	}
	stdoutLimit, err := normalizeProcessOutputLimit(request.StdoutLimit, runner.config.DefaultStdoutLimit)
	if err != nil {
		return ProcessResult{}, processInvalidRequestError(fmt.Sprintf("stdout limit: %v", err))
	}
	stderrLimit, err := normalizeProcessOutputLimit(request.StderrLimit, runner.config.DefaultStderrLimit)
	if err != nil {
		return ProcessResult{}, processInvalidRequestError(fmt.Sprintf("stderr limit: %v", err))
	}
	if err := validateProcessArguments(request.Args); err != nil {
		return ProcessResult{}, processInvalidRequestError(err.Error())
	}
	if len(request.Stdin) > MaximumProcessStdinBytes {
		return ProcessResult{}, processInvalidRequestError(fmt.Sprintf("stdin exceeds %d bytes", MaximumProcessStdinBytes))
	}

	timeout := request.Timeout
	if timeout <= 0 {
		timeout = runner.config.DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	redactionLookahead := processRedactionLookahead(redactions)
	stdout := newLimitedProcessBuffer(stdoutLimit, redactionLookahead)
	stderr := newLimitedProcessBuffer(stderrLimit, redactionLookahead)
	if request.OnStdoutLine != nil {
		stdout.onLine = newSanitizedProcessLinePublisher(request.OnStdoutLine, redactions)
	}
	command := exec.CommandContext(runCtx, resolved, request.Args...)
	command.Dir = directory
	command.Env = environment
	if request.Stdin != "" {
		command.Stdin = strings.NewReader(request.Stdin)
	}
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = runner.config.WaitDelay

	tree, err := prepareProcessTree(command)
	if err != nil {
		return ProcessResult{}, NewClassifiedExecutorError(
			ExecutorFailureSwitchable,
			true,
			ProcessErrorCodeTreeSetupFailed,
			fmt.Errorf("prepare process tree: %w", err),
		)
	}
	defer tree.Close()

	result := ProcessResult{
		ExecutablePath: resolved,
		Dir:            directory,
		ExitCode:       -1,
		StartedAt:      time.Now().UTC(),
	}
	if err := command.Start(); err != nil {
		result.FinishedAt = time.Now().UTC()
		populateProcessOutput(&result, stdout, stderr, redactions)
		if ctxErr := runCtx.Err(); ctxErr != nil {
			return result, classifiedProcessContextError(ctxErr)
		}
		return result, NewClassifiedExecutorError(
			ExecutorFailureSwitchable,
			true,
			ProcessErrorCodeStartFailed,
			fmt.Errorf("start executable %q: %w", filepath.Base(resolved), err),
		)
	}
	if err := tree.Attach(command.Process); err != nil {
		killTreeErr := tree.Kill()
		killProcessErr := command.Process.Kill()
		waitErr := command.Wait()
		closeErr := tree.Close()
		result.FinishedAt = time.Now().UTC()
		populateProcessOutput(&result, stdout, stderr, redactions)
		return result, NewClassifiedExecutorError(
			ExecutorFailureSwitchable,
			true,
			ProcessErrorCodeTreeSetupFailed,
			errors.Join(
				fmt.Errorf("attach process tree: %w", err),
				ignoreProcessDoneError("kill process tree", killTreeErr),
				ignoreProcessDoneError("kill process", killProcessErr),
				ignoreProcessExitError(waitErr),
				ignoreProcessDoneError("close process tree", closeErr),
			),
		)
	}

	waitErr := command.Wait()
	closeErr := tree.Close()
	result.FinishedAt = time.Now().UTC()
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	populateProcessOutput(&result, stdout, stderr, redactions)
	result.StdoutStreamed = stdout.PublishedLineCount() > 0
	if ctxErr := runCtx.Err(); ctxErr != nil {
		return result, classifiedProcessContextError(errors.Join(
			ctxErr,
			ignoreProcessDoneError("close process tree", closeErr),
		))
	}
	if waitErr != nil {
		return result, NewClassifiedExecutorError(
			ExecutorFailureSwitchable,
			true,
			ProcessErrorCodeExitFailed,
			errors.Join(
				fmt.Errorf("executable %q exited with code %d: %w", filepath.Base(resolved), result.ExitCode, waitErr),
				ignoreProcessDoneError("close process tree", closeErr),
			),
		)
	}
	if closeErr != nil {
		return result, NewClassifiedExecutorError(
			ExecutorFailureSwitchable,
			true,
			ProcessErrorCodeTreeCleanupFailed,
			fmt.Errorf("close process tree: %w", closeErr),
		)
	}
	return result, nil
}

func processInvalidRequestError(message string) error {
	return NewClassifiedExecutorError(
		ExecutorFailureTerminal,
		false,
		ProcessErrorCodeInvalidRequest,
		errors.New(strings.TrimSpace(message)),
	)
}

func classifiedProcessContextError(err error) error {
	code := ProcessErrorCodeCanceled
	if errors.Is(err, context.DeadlineExceeded) {
		code = ProcessErrorCodeTimeout
	}
	return NewClassifiedExecutorError(ExecutorFailureTerminal, false, code, err)
}

func ignoreProcessDoneError(operation string, err error) error {
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func ignoreProcessExitError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return fmt.Errorf("wait for process cleanup: %w", err)
}

func resolveProcessDirectory(directory string) (string, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		current, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve current working directory: %w", err)
		}
		directory = current
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve working directory %q: %w", directory, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect working directory %q: %w", absolute, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory %q is not a directory", absolute)
	}
	return filepath.Clean(absolute), nil
}

func validateProcessArguments(arguments []string) error {
	for index, argument := range arguments {
		if strings.ContainsRune(argument, '\x00') {
			return fmt.Errorf("argument %d contains a NUL byte", index)
		}
	}
	return nil
}

func normalizeProcessOutputLimit(requested, fallback int) (int, error) {
	limit := requested
	if limit <= 0 {
		limit = fallback
	}
	if limit <= 0 || limit > MaximumProcessOutputLimit {
		return 0, fmt.Errorf("must be between 1 and %d bytes", MaximumProcessOutputLimit)
	}
	return limit, nil
}

type processEnvironmentEntry struct {
	name  string
	value string
}

func buildProcessEnvironment(request ProcessRequest) ([]string, []string, error) {
	entries := make(map[string]processEnvironmentEntry)
	inheritNames := append([]string{}, defaultProcessEnvironmentAllowlist...)
	inheritNames = append(inheritNames, request.InheritEnvironment...)
	for _, name := range inheritNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if err := validateProcessEnvironmentName(name); err != nil {
			return nil, nil, err
		}
		if value, exists := os.LookupEnv(name); exists {
			entries[processEnvironmentKey(name)] = processEnvironmentEntry{name: name, value: value}
		}
	}
	for name, value := range request.Env {
		name = strings.TrimSpace(name)
		if err := validateProcessEnvironmentName(name); err != nil {
			return nil, nil, err
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, nil, fmt.Errorf("environment variable %q contains a NUL byte", name)
		}
		if len(value) > MaximumProcessEnvironmentValueBytes {
			return nil, nil, fmt.Errorf(
				"environment variable %q exceeds %d bytes",
				name,
				MaximumProcessEnvironmentValueBytes,
			)
		}
		entries[processEnvironmentKey(name)] = processEnvironmentEntry{name: name, value: value}
	}
	if len(entries) > MaximumProcessEnvironmentEntries {
		return nil, nil, fmt.Errorf(
			"environment contains %d entries, limit is %d",
			len(entries),
			MaximumProcessEnvironmentEntries,
		)
	}

	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	redactions := make([]string, 0, len(request.Env))
	totalBytes := 0
	for _, key := range keys {
		entry := entries[key]
		totalBytes += len(entry.name) + len(entry.value) + 2
		if totalBytes > MaximumProcessEnvironmentBytes {
			return nil, nil, fmt.Errorf(
				"environment exceeds %d bytes",
				MaximumProcessEnvironmentBytes,
			)
		}
		environment = append(environment, entry.name+"="+entry.value)
		if isSensitiveProcessEnvironmentName(entry.name) && len(entry.value) >= 4 {
			redactions = append(redactions, entry.value)
		}
	}
	sort.Slice(redactions, func(i, j int) bool { return len(redactions[i]) > len(redactions[j]) })
	return environment, redactions, nil
}

func validateProcessEnvironmentName(name string) error {
	if name == "" || strings.ContainsAny(name, "=\x00") {
		return fmt.Errorf("environment variable name %q is invalid", name)
	}
	return nil
}

func processEnvironmentKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func isSensitiveProcessEnvironmentName(name string) bool {
	upper := strings.ToUpper(name)
	for _, token := range []string{"AUTH", "COOKIE", "CREDENTIAL", "KEY", "PASSWORD", "PRIVATE", "SECRET", "SESSION", "TOKEN"} {
		if strings.Contains(upper, token) {
			return true
		}
	}
	return false
}

func populateProcessOutput(
	result *ProcessResult,
	stdout *limitedProcessBuffer,
	stderr *limitedProcessBuffer,
	redactions []string,
) {
	if result == nil {
		return
	}
	stdoutBytes, stdoutTruncated := stdout.Snapshot()
	stderrBytes, stderrTruncated := stderr.Snapshot()
	result.Stdout = boundProcessOutput(sanitizeProcessOutput(string(stdoutBytes), redactions), stdout.limit)
	result.Stderr = boundProcessOutput(sanitizeProcessOutput(string(stderrBytes), redactions), stderr.limit)
	result.StdoutTruncated = stdoutTruncated
	result.StderrTruncated = stderrTruncated
	result.StdoutStreamed = stdout.PublishedLineCount() > 0
}

func sanitizeProcessOutput(value string, redactions []string) string {
	for _, secret := range redactions {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "<redacted>")
		}
	}
	value = sensitiveHeaderPattern.ReplaceAllString(value, `${1}<redacted>`)
	value = sensitiveAuthorizationPattern.ReplaceAllString(value, `${1}<redacted>`)
	value = sensitiveSchemePattern.ReplaceAllString(value, "<redacted>")
	return credentialLikePattern.ReplaceAllString(value, "<redacted>")
}

func processRedactionLookahead(redactions []string) int {
	lookahead := processOutputRedactionLookahead
	for _, value := range redactions {
		if len(value) > lookahead {
			lookahead = len(value)
		}
	}
	return lookahead
}

func boundProcessOutput(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

type limitedProcessBuffer struct {
	mu           sync.Mutex
	buffer       bytes.Buffer
	limit        int
	captureLimit int
	seen         int64
	truncated    bool
	onLine       *sanitizedProcessLinePublisher
}

func newLimitedProcessBuffer(limit, lookahead int) *limitedProcessBuffer {
	captureLimit := limit + lookahead
	if captureLimit < limit {
		captureLimit = limit
	}
	return &limitedProcessBuffer{limit: limit, captureLimit: captureLimit}
}

func (buffer *limitedProcessBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	originalLength := len(data)
	buffer.seen += int64(originalLength)
	remaining := buffer.captureLimit - buffer.buffer.Len()
	if remaining > 0 {
		captured := data
		if len(captured) > remaining {
			captured = captured[:remaining]
		}
		_, _ = buffer.buffer.Write(captured)
	}
	if buffer.seen > int64(buffer.limit) {
		buffer.truncated = true
	}
	publisher := buffer.onLine
	buffer.mu.Unlock()
	if publisher != nil {
		publisher.Write(data)
	}
	return originalLength, nil
}

func (buffer *limitedProcessBuffer) Snapshot() ([]byte, bool) {
	if buffer == nil {
		return []byte{}, false
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte{}, buffer.buffer.Bytes()...), buffer.truncated
}

func (buffer *limitedProcessBuffer) PublishedLineCount() int64 {
	if buffer == nil || buffer.onLine == nil {
		return 0
	}
	return buffer.onLine.Count()
}

type sanitizedProcessLinePublisher struct {
	mu                  sync.Mutex
	pending             []byte
	discardUntilNewline bool
	redactions          []string
	publish             func(string)
	count               int64
}

func newSanitizedProcessLinePublisher(publish func(string), redactions []string) *sanitizedProcessLinePublisher {
	return &sanitizedProcessLinePublisher{redactions: append([]string{}, redactions...), publish: publish}
}

func (publisher *sanitizedProcessLinePublisher) Write(data []byte) {
	if publisher == nil || len(data) == 0 || publisher.publish == nil {
		return
	}
	publisher.mu.Lock()
	lines := make([]string, 0)
	for len(data) > 0 {
		index := bytes.IndexByte(data, '\n')
		if index < 0 {
			if publisher.discardUntilNewline {
				break
			}
			if len(publisher.pending)+len(data) > MaximumProcessOutputLimit {
				publisher.pending = nil
				publisher.discardUntilNewline = true
				break
			}
			publisher.pending = append(publisher.pending, data...)
			break
		}
		if publisher.discardUntilNewline {
			publisher.discardUntilNewline = false
			data = data[index+1:]
			continue
		}
		if len(publisher.pending)+index <= MaximumProcessOutputLimit {
			publisher.pending = append(publisher.pending, data[:index]...)
			line := strings.TrimSuffix(string(publisher.pending), "\r")
			publisher.pending = nil
			lines = append(lines, sanitizeProcessOutput(line, publisher.redactions))
		} else {
			publisher.pending = nil
		}
		data = data[index+1:]
	}
	publisher.mu.Unlock()
	for _, line := range lines {
		publisher.publish(line)
		publisher.mu.Lock()
		publisher.count++
		publisher.mu.Unlock()
	}
}

func (publisher *sanitizedProcessLinePublisher) Count() int64 {
	if publisher == nil {
		return 0
	}
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	return publisher.count
}

var _ io.Writer = (*limitedProcessBuffer)(nil)
