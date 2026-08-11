package executors

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"cursor/internal/backend/delegation"
)

const (
	CursorExecutorID delegation.ExecutorID = "cursor-agent"

	CursorDiagnosticReady          = "cursor_agent_ready"
	CursorDiagnosticEditorOnly     = "cursor_editor_only"
	CursorDiagnosticEditorNotFound = "cursor_editor_not_found"

	CursorErrorCodeAgentUnavailable = "cursor_agent_unavailable"
	CursorInstallURL                = "https://www.cursor.com/downloads"
)

var cursorCapabilities = []delegation.ExecutorCapability{
	delegation.ExecutorCapabilityReadWorkspace,
	delegation.ExecutorCapabilityWriteWorkspace,
	delegation.ExecutorCapabilityShell,
	delegation.ExecutorCapabilityNetwork,
	delegation.ExecutorCapabilityMCP,
	delegation.ExecutorCapabilityVision,
}

var cursorLauncherExecutablePattern = regexp.MustCompile(`(?i)%~dp0([^"\r\n]*\.exe)`)

type CursorEditorDetector func(context.Context) (string, error)
type CursorAgentCapability func(parentRequestID string) bool

func NewCursorRegistration(
	config delegation.RuntimeExecutorConfig,
	detectEditor CursorEditorDetector,
	agentAvailable CursorAgentCapability,
	execute delegation.Executor,
) (delegation.ExecutorRegistration, error) {
	if config.ID == "" {
		config.ID = CursorExecutorID
	}
	if config.ID != CursorExecutorID {
		return delegation.ExecutorRegistration{}, fmt.Errorf("Cursor executor id %q is invalid", config.ID)
	}
	if detectEditor == nil {
		detectEditor = NewCursorEditorDetector(config.Executable)
	}
	if agentAvailable == nil {
		return delegation.ExecutorRegistration{}, errors.New("Cursor agent capability provider is required")
	}
	if execute == nil {
		return delegation.ExecutorRegistration{}, errors.New("Cursor agent execute function is required")
	}

	probe := func(ctx context.Context) (delegation.ExecutorProbeResult, error) {
		path, detectErr := detectEditor(ctx)
		path = strings.TrimSpace(path)
		editorAvailable := path != ""
		agentExecutionAvailable := agentAvailable("")
		result := delegation.ExecutorProbeResult{
			ExecutablePath:          path,
			Installed:               editorAvailable,
			EditorAvailable:         editorAvailable,
			AgentExecutionAvailable: agentExecutionAvailable,
			AuthState:               delegation.ExecutorAuthUnknown,
			Capabilities:            append([]delegation.ExecutorCapability{}, cursorCapabilities...),
			ProbedAt:                time.Now().UTC(),
		}
		switch {
		case agentExecutionAvailable:
			result.State = delegation.ExecutorProbeReady
			result.DiagnosticCode = CursorDiagnosticReady
			result.DiagnosticText = "Cursor agent bridge has an active client subscriber"
		case editorAvailable:
			result.State = delegation.ExecutorProbeActionRequired
			result.DiagnosticCode = CursorDiagnosticEditorOnly
			result.DiagnosticText = "Cursor editor is available, but no active agent client is connected"
		default:
			result.State = delegation.ExecutorProbeNotInstalled
			result.DiagnosticCode = CursorDiagnosticEditorNotFound
			result.DiagnosticText = "Cursor editor was not found and no active agent client is connected"
		}
		if detectErr != nil && !agentExecutionAvailable {
			result.DiagnosticText = strings.TrimSpace(detectErr.Error())
		}
		return result, nil
	}

	guardedExecute := func(ctx context.Context, request delegation.TaskRequest) delegation.TaskResult {
		parentRequestID := strings.TrimSpace(request.ParentRequest)
		if parentRequestID == "" || !agentAvailable(parentRequestID) {
			err := delegation.NewClassifiedExecutorError(
				delegation.ExecutorFailureSwitchable,
				true,
				CursorErrorCodeAgentUnavailable,
				errors.New("Cursor agent client is not connected to the parent request"),
			)
			return delegation.TaskResult{Error: err, Output: err.Error()}
		}
		return execute(ctx, request)
	}

	return delegation.ExecutorRegistration{
		ID:           CursorExecutorID,
		DisplayName:  firstNonEmpty(config.DisplayName, "Cursor Agent"),
		InstallURL:   CursorInstallURL,
		Enabled:      config.Enabled,
		Priority:     config.Priority,
		Capabilities: append([]delegation.ExecutorCapability{}, cursorCapabilities...),
		Probe:        probe,
		Execute:      guardedExecute,
	}, nil
}

func NewCursorEditorDetector(configuredPath string) CursorEditorDetector {
	configuredPath = strings.TrimSpace(configuredPath)
	return func(context.Context) (string, error) {
		candidates := []string{configuredPath}
		if runtime.GOOS == "windows" {
			candidates = append(candidates,
				filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Cursor", "Cursor.exe"),
				filepath.Join(os.Getenv("LOCALAPPDATA"), "Cursor", "Cursor.exe"),
				filepath.Join(os.Getenv("PROGRAMFILES"), "Cursor", "Cursor.exe"),
				filepath.Join(os.Getenv("PROGRAMFILES(X86)"), "Cursor", "Cursor.exe"),
			)
		}
		for _, candidate := range candidates {
			if path := existingCursorExecutable(candidate); path != "" {
				return path, nil
			}
		}
		for _, command := range []string{"cursor", "Cursor"} {
			if path, err := delegation.ResolveExecutable(command); err == nil {
				if executable := cursorExecutableFromLauncher(path); executable != "" {
					return executable, nil
				}
				if executable := existingCursorExecutable(path); executable != "" {
					return executable, nil
				}
			}
		}
		return "", nil
	}
}

func cursorExecutableFromLauncher(path string) string {
	extension := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	if extension != ".cmd" && extension != ".bat" {
		return ""
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	match := cursorLauncherExecutablePattern.FindSubmatch(content)
	if len(match) < 2 {
		return ""
	}
	return existingCursorExecutable(filepath.Join(filepath.Dir(path), filepath.Clean(string(match[1]))))
}

func existingCursorExecutable(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(absolute)
}
