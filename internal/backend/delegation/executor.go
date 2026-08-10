package delegation

import (
	"errors"
	"strings"
	"time"
)

const ExecutorMetadataIDKey = "executor_id"

type ExecutorID string

type ExecutorCapability string

const (
	ExecutorCapabilityReadWorkspace  ExecutorCapability = "read_workspace"
	ExecutorCapabilityWriteWorkspace ExecutorCapability = "write_workspace"
	ExecutorCapabilityShell          ExecutorCapability = "shell"
	ExecutorCapabilityNetwork        ExecutorCapability = "network"
	ExecutorCapabilityMCP            ExecutorCapability = "mcp"
	ExecutorCapabilityVision         ExecutorCapability = "vision"
)

type ExecutorProbeState string

const (
	ExecutorProbeUnknown        ExecutorProbeState = "unknown"
	ExecutorProbeReady          ExecutorProbeState = "ready"
	ExecutorProbeNotInstalled   ExecutorProbeState = "not_installed"
	ExecutorProbeIncompatible   ExecutorProbeState = "incompatible"
	ExecutorProbeActionRequired ExecutorProbeState = "action_required"
	ExecutorProbeUnhealthy      ExecutorProbeState = "unhealthy"
)

type ExecutorAuthState string

const (
	ExecutorAuthUnknown  ExecutorAuthState = "unknown"
	ExecutorAuthReady    ExecutorAuthState = "ready"
	ExecutorAuthRequired ExecutorAuthState = "required"
)

type ExecutorProbeResult struct {
	State          ExecutorProbeState
	ExecutablePath string
	Version        string
	Installed      bool
	AuthState      ExecutorAuthState
	Capabilities   []ExecutorCapability
	DiagnosticCode string
	DiagnosticText string
	ProbedAt       time.Time
}

type ExecutorFailureClass string

const (
	ExecutorFailureSwitchable         ExecutorFailureClass = "switchable"
	ExecutorFailureUserActionRequired ExecutorFailureClass = "user_action_required"
	ExecutorFailureTerminal           ExecutorFailureClass = "terminal"
)

type ClassifiedExecutorError struct {
	class     ExecutorFailureClass
	retrySafe bool
	code      string
	cause     error
}

func NewClassifiedExecutorError(class ExecutorFailureClass, retrySafe bool, code string, cause error) *ClassifiedExecutorError {
	if class == "" {
		class = ExecutorFailureTerminal
	}
	if cause == nil {
		cause = errors.New(strings.TrimSpace(code))
	}
	return &ClassifiedExecutorError{
		class:     class,
		retrySafe: retrySafe,
		code:      strings.TrimSpace(code),
		cause:     cause,
	}
}

func (err *ClassifiedExecutorError) Error() string {
	if err == nil || err.cause == nil {
		return ""
	}
	return err.cause.Error()
}

func (err *ClassifiedExecutorError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func (err *ClassifiedExecutorError) FailureClass() ExecutorFailureClass {
	if err == nil || err.class == "" {
		return ExecutorFailureTerminal
	}
	return err.class
}

func (err *ClassifiedExecutorError) RetrySafe() bool {
	return err != nil && err.retrySafe
}

func (err *ClassifiedExecutorError) Code() string {
	if err == nil {
		return ""
	}
	return err.code
}

type executorClassifiedError interface {
	error
	FailureClass() ExecutorFailureClass
	RetrySafe() bool
}

func ExecutorErrorClassification(err error) (ExecutorFailureClass, bool) {
	if err == nil {
		return "", false
	}
	var classified executorClassifiedError
	if errors.As(err, &classified) {
		return classified.FailureClass(), classified.RetrySafe()
	}
	return ExecutorFailureTerminal, false
}

func executorErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var coded interface{ Code() string }
	if errors.As(err, &coded) {
		return strings.TrimSpace(coded.Code())
	}
	return ""
}

type ExecutorAttemptStatus string

const (
	ExecutorAttemptRunning   ExecutorAttemptStatus = "running"
	ExecutorAttemptCompleted ExecutorAttemptStatus = "completed"
	ExecutorAttemptFailed    ExecutorAttemptStatus = "failed"
	ExecutorAttemptCanceled  ExecutorAttemptStatus = "canceled"
	ExecutorAttemptTimedOut  ExecutorAttemptStatus = "timed_out"
)

type ExecutorAttemptSnapshot struct {
	ExecutorID     ExecutorID
	Attempt        int
	Status         ExecutorAttemptStatus
	FailureClass   ExecutorFailureClass
	RetrySafe      bool
	DiagnosticCode string
	Error          string
	StartedAt      time.Time
	FinishedAt     time.Time
	Metadata       map[string]string
}

func cloneExecutorCapabilities(source []ExecutorCapability) []ExecutorCapability {
	if len(source) == 0 {
		return nil
	}
	return append([]ExecutorCapability(nil), source...)
}

func cloneExecutorAttempts(source []ExecutorAttemptSnapshot) []ExecutorAttemptSnapshot {
	if len(source) == 0 {
		return nil
	}
	cloned := make([]ExecutorAttemptSnapshot, len(source))
	for index, attempt := range source {
		cloned[index] = attempt
		cloned[index].Metadata = cloneStringMap(attempt.Metadata)
	}
	return cloned
}

func cloneExecutorProbeResult(result ExecutorProbeResult) ExecutorProbeResult {
	result.Capabilities = cloneExecutorCapabilities(result.Capabilities)
	return result
}
