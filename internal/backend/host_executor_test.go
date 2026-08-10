package backend

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"cursor/internal/backend/delegation"
	serverconfig "cursor/internal/backend/server/config"
)

func TestRefreshDelegationExecutorProbesForcesRegistryProbe(t *testing.T) {
	registry := delegation.NewExecutorRegistry(delegation.ExecutorRegistryConfig{})
	var calls atomic.Int32
	if err := registry.Register(delegation.ExecutorRegistration{
		ID:      "test-cli",
		Enabled: true,
		Probe: func(context.Context) (delegation.ExecutorProbeResult, error) {
			calls.Add(1)
			return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeReady}, nil
		},
		Execute: func(context.Context, delegation.TaskRequest) delegation.TaskResult { return delegation.TaskResult{} },
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	host := &Host{executorRegistry: registry}
	if _, err := host.RefreshDelegationExecutorProbes(t.Context()); err != nil {
		t.Fatalf("first refresh error = %v", err)
	}
	if _, err := host.RefreshDelegationExecutorProbes(t.Context()); err != nil {
		t.Fatalf("second refresh error = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("probe calls = %d, want 2 forced probes", calls.Load())
	}
}

func TestHostRegistersClaudeExecutorAndAppliesSavedPolicy(t *testing.T) {
	store := serverconfig.NewStore(filepath.Join(t.TempDir(), "config.yaml"), "")
	host, err := NewHost(store, nil)
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	t.Cleanup(func() { _ = host.Stop(context.Background()) })

	snapshot, ok := host.ExecutorRegistry().Snapshot("claude-code")
	if !ok || snapshot.Enabled {
		t.Fatalf("default Claude snapshot = %#v, ok=%t", snapshot, ok)
	}
	cfg, err := host.ConfigManager().GetDelegationConfig(t.Context())
	if err != nil {
		t.Fatalf("GetDelegationConfig() error = %v", err)
	}
	cfg.Executors = []serverconfig.DelegationExecutorConfig{{
		ID:                      "claude-code",
		Kind:                    serverconfig.DelegationExecutorKindBuiltin,
		Enabled:                 true,
		Priority:                4,
		Executable:              "C:/tools/claude.exe",
		ProbeTimeoutSeconds:     3,
		ExecutionTimeoutSeconds: 25,
	}}
	if _, err := host.ConfigManager().SaveDelegationConfig(t.Context(), cfg); err != nil {
		t.Fatalf("SaveDelegationConfig() error = %v", err)
	}
	snapshot, ok = host.ExecutorRegistry().Snapshot("claude-code")
	if !ok || !snapshot.Enabled || snapshot.Priority != 4 || snapshot.Probe.State != delegation.ExecutorProbeUnknown {
		t.Fatalf("updated Claude snapshot = %#v, ok=%t", snapshot, ok)
	}
}

func TestHostRegistersCodexExecutorAndAppliesSavedPolicy(t *testing.T) {
	store := serverconfig.NewStore(filepath.Join(t.TempDir(), "config.yaml"), "")
	host, err := NewHost(store, nil)
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	t.Cleanup(func() { _ = host.Stop(context.Background()) })

	snapshot, ok := host.ExecutorRegistry().Snapshot("codex-cli")
	if !ok || snapshot.Enabled {
		t.Fatalf("default Codex snapshot = %#v, ok=%t", snapshot, ok)
	}
	cfg, err := host.ConfigManager().GetDelegationConfig(t.Context())
	if err != nil {
		t.Fatalf("GetDelegationConfig() error = %v", err)
	}
	cfg.Executors = []serverconfig.DelegationExecutorConfig{{
		ID:                      "codex-cli",
		Kind:                    serverconfig.DelegationExecutorKindBuiltin,
		Enabled:                 true,
		Priority:                3,
		Executable:              "C:/tools/codex.exe",
		ProbeTimeoutSeconds:     4,
		ExecutionTimeoutSeconds: 40,
	}}
	if _, err := host.ConfigManager().SaveDelegationConfig(t.Context(), cfg); err != nil {
		t.Fatalf("SaveDelegationConfig() error = %v", err)
	}
	snapshot, ok = host.ExecutorRegistry().Snapshot("codex-cli")
	if !ok || !snapshot.Enabled || snapshot.Priority != 3 || snapshot.Probe.State != delegation.ExecutorProbeUnknown {
		t.Fatalf("updated Codex snapshot = %#v, ok=%t", snapshot, ok)
	}
	claude, ok := host.ExecutorRegistry().Snapshot("claude-code")
	if !ok || claude.Enabled {
		t.Fatalf("Claude snapshot after Codex save = %#v, ok=%t", claude, ok)
	}
}
