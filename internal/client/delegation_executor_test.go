package client

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cursor/internal/backend/delegation"
)

func TestPublicDelegationExecutorSnapshotOmitsUnknownTimestamps(t *testing.T) {
	items := publicDelegationExecutorSnapshots([]delegation.ExecutorSnapshot{{ID: "claude-code"}})
	payload, err := json.Marshal(items[0])
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(payload), "probedAt") || strings.Contains(string(payload), "cooldownUntil") {
		t.Fatalf("unknown timestamps must be omitted: %s", payload)
	}
}

func TestPublicDelegationExecutorSnapshotsOmitSecrets(t *testing.T) {
	secret := "sk-delegation-executor-secret"
	items := publicDelegationExecutorSnapshots([]delegation.ExecutorSnapshot{{
		ID:           "claude-code",
		DisplayName:  "Claude Code",
		Enabled:      true,
		Priority:     1,
		Capabilities: []delegation.ExecutorCapability{delegation.ExecutorCapabilityReadWorkspace},
		Probe: delegation.ExecutorProbeResult{
			State:          delegation.ExecutorProbeUnhealthy,
			ExecutablePath: "C:/Tools/claude.exe",
			Version:        "2.1.226",
			Installed:      true,
			DiagnosticCode: "probe_failed",
			DiagnosticText: "token=" + secret,
			ProbedAt:       time.Unix(10, 0).UTC(),
		},
	}})

	if len(items) != 1 {
		t.Fatalf("snapshots = %#v", items)
	}
	got := items[0]
	if got.ID != "claude-code" || got.State != "unhealthy" || got.ExecutablePath != "C:/Tools/claude.exe" {
		t.Fatalf("public snapshot = %#v", got)
	}
	if strings.Contains(got.DiagnosticText, secret) || !strings.Contains(got.DiagnosticText, "<redacted>") {
		t.Fatalf("diagnostic was not sanitized: %q", got.DiagnosticText)
	}
}

func TestPublicCursorCapabilityMapsEditorAndAgentStates(t *testing.T) {
	items := publicDelegationExecutorSnapshots([]delegation.ExecutorSnapshot{{
		ID: "cursor-agent",
		Probe: delegation.ExecutorProbeResult{
			State:                   delegation.ExecutorProbeActionRequired,
			EditorAvailable:         true,
			AgentExecutionAvailable: false,
		},
	}})
	if len(items) != 1 || !items[0].EditorAvailable || items[0].AgentExecutionAvailable {
		t.Fatalf("public Cursor snapshot = %#v", items)
	}
}

// TestPublicDelegationExecutorSnapshotExposesInstallURL 验证官方安装入口经过桌面绑定传递，
// 并且不依赖诊断文本解析。
func TestPublicDelegationExecutorSnapshotExposesInstallURL(t *testing.T) {
	items := publicDelegationExecutorSnapshots([]delegation.ExecutorSnapshot{{
		ID:         "gemini-cli",
		InstallURL: "https://github.com/google-gemini/gemini-cli",
	}})
	if len(items) != 1 || items[0].InstallURL != "https://github.com/google-gemini/gemini-cli" {
		t.Fatalf("public install URL = %#v", items)
	}
}
