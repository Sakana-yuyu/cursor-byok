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
