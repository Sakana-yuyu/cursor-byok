package backend

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestRefreshDelegationExecutorProbesStartsIndependentProbesConcurrently(t *testing.T) {
	registry := delegation.NewExecutorRegistry(delegation.ExecutorRegistryConfig{})
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	for _, id := range []delegation.ExecutorID{"first-cli", "second-cli"} {
		id := id
		if err := registry.Register(delegation.ExecutorRegistration{
			ID:      id,
			Enabled: true,
			Probe: func(context.Context) (delegation.ExecutorProbeResult, error) {
				started <- struct{}{}
				<-release
				return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeReady}, nil
			},
			Execute: func(context.Context, delegation.TaskRequest) delegation.TaskResult { return delegation.TaskResult{} },
		}); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
	}

	host := &Host{executorRegistry: registry}
	completed := make(chan error, 1)
	go func() {
		_, err := host.RefreshDelegationExecutorProbes(context.Background())
		completed <- err
	}()
	<-started
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("second independent executor probe did not start concurrently")
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-completed; err != nil {
		t.Fatalf("RefreshDelegationExecutorProbes() error = %v", err)
	}
}

func TestRefreshDelegationExecutorProbesReturnsWhenContextCancelsDuringDispatch(t *testing.T) {
	registry := delegation.NewExecutorRegistry(delegation.ExecutorRegistryConfig{})
	started := make(chan struct{}, delegationExecutorProbeConcurrency)
	for index := 0; index <= delegationExecutorProbeConcurrency; index++ {
		id := delegation.ExecutorID(fmt.Sprintf("cancel-cli-%d", index))
		if err := registry.Register(delegation.ExecutorRegistration{
			ID:      id,
			Enabled: true,
			Probe: func(ctx context.Context) (delegation.ExecutorProbeResult, error) {
				started <- struct{}{}
				<-ctx.Done()
				return delegation.ExecutorProbeResult{}, ctx.Err()
			},
			Execute: func(context.Context, delegation.TaskRequest) delegation.TaskResult { return delegation.TaskResult{} },
		}); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	host := &Host{executorRegistry: registry}
	completed := make(chan error, 1)
	go func() {
		_, err := host.RefreshDelegationExecutorProbes(ctx)
		completed <- err
	}()
	for range delegationExecutorProbeConcurrency {
		<-started
	}
	cancel()
	select {
	case err := <-completed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RefreshDelegationExecutorProbes() error = %v, want context canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("RefreshDelegationExecutorProbes() did not return after context cancellation")
	}
}

type executorInstallRecorder struct {
	packages []string
	err      error
}

func (recorder *executorInstallRecorder) Install(_ context.Context, packageName string) error {
	recorder.packages = append(recorder.packages, packageName)
	return recorder.err
}

func TestInstallDelegationExecutorUsesFixedPackageAndReprobes(t *testing.T) {
	registry := delegation.NewExecutorRegistry(delegation.ExecutorRegistryConfig{})
	var probeCalls atomic.Int32
	if err := registry.Register(delegation.ExecutorRegistration{
		ID:      "codex-cli",
		Enabled: true,
		Probe: func(context.Context) (delegation.ExecutorProbeResult, error) {
			probeCalls.Add(1)
			return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeReady}, nil
		},
		Execute: func(context.Context, delegation.TaskRequest) delegation.TaskResult { return delegation.TaskResult{} },
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	installer := &executorInstallRecorder{}
	host := &Host{
		executorRegistry:  registry,
		executorInstaller: installer,
		executorInstalls:  make(map[delegation.ExecutorID]struct{}),
	}

	snapshot, err := host.InstallDelegationExecutor(t.Context(), "codex-cli")
	if err != nil {
		t.Fatalf("InstallDelegationExecutor() error = %v", err)
	}
	if got, want := installer.packages, []string{"@openai/codex"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("installer packages = %#v, want %#v", got, want)
	}
	if probeCalls.Load() != 1 || snapshot.Probe.State != delegation.ExecutorProbeReady {
		t.Fatalf("post-install probe = calls=%d snapshot=%#v", probeCalls.Load(), snapshot)
	}
}

func TestInstallDelegationExecutorRejectsUnsupportedOrUnknownTargets(t *testing.T) {
	installer := &executorInstallRecorder{}
	host := &Host{
		executorRegistry:  delegation.NewExecutorRegistry(delegation.ExecutorRegistryConfig{}),
		executorInstaller: installer,
		executorInstalls:  make(map[delegation.ExecutorID]struct{}),
	}
	for _, id := range []string{"cursor-agent", "kiro-cli", "arbitrary-command"} {
		if _, err := host.InstallDelegationExecutor(t.Context(), id); err == nil {
			t.Fatalf("InstallDelegationExecutor(%q) error = nil, want rejection", id)
		}
	}
	if len(installer.packages) != 0 {
		t.Fatalf("unsupported installation invoked packages %#v", installer.packages)
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

func TestHostRegistersGeminiExecutorAndAppliesSavedPolicy(t *testing.T) {
	store := serverconfig.NewStore(filepath.Join(t.TempDir(), "config.yaml"), "")
	host, err := NewHost(store, nil)
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	t.Cleanup(func() { _ = host.Stop(context.Background()) })

	snapshot, ok := host.ExecutorRegistry().Snapshot("gemini-cli")
	if !ok || snapshot.Enabled {
		t.Fatalf("default Gemini snapshot = %#v, ok=%t", snapshot, ok)
	}
	cfg, err := host.ConfigManager().GetDelegationConfig(t.Context())
	if err != nil {
		t.Fatalf("GetDelegationConfig() error = %v", err)
	}
	cfg.Executors = []serverconfig.DelegationExecutorConfig{{
		ID:                      "gemini-cli",
		Kind:                    serverconfig.DelegationExecutorKindBuiltin,
		Enabled:                 true,
		Priority:                2,
		Executable:              "C:/tools/gemini.exe",
		ProbeTimeoutSeconds:     5,
		ExecutionTimeoutSeconds: 35,
	}}
	if _, err := host.ConfigManager().SaveDelegationConfig(t.Context(), cfg); err != nil {
		t.Fatalf("SaveDelegationConfig() error = %v", err)
	}
	snapshot, ok = host.ExecutorRegistry().Snapshot("gemini-cli")
	if !ok || !snapshot.Enabled || snapshot.Priority != 2 || snapshot.Probe.State != delegation.ExecutorProbeUnknown {
		t.Fatalf("updated Gemini snapshot = %#v, ok=%t", snapshot, ok)
	}
	for _, id := range []delegation.ExecutorID{"claude-code", "codex-cli"} {
		other, exists := host.ExecutorRegistry().Snapshot(id)
		if !exists || other.Enabled {
			t.Fatalf("%s snapshot after Gemini save = %#v, ok=%t", id, other, exists)
		}
	}
}

func TestHostRegistersKiroExecutorAndAppliesSavedPolicy(t *testing.T) {
	store := serverconfig.NewStore(filepath.Join(t.TempDir(), "config.yaml"), "")
	host, err := NewHost(store, nil)
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	t.Cleanup(func() { _ = host.Stop(context.Background()) })

	snapshot, ok := host.ExecutorRegistry().Snapshot("kiro-cli")
	if !ok || snapshot.Enabled {
		t.Fatalf("default Kiro snapshot = %#v, ok=%t", snapshot, ok)
	}
	cfg, err := host.ConfigManager().GetDelegationConfig(t.Context())
	if err != nil {
		t.Fatalf("GetDelegationConfig() error = %v", err)
	}
	cfg.Executors = []serverconfig.DelegationExecutorConfig{{ID: "kiro-cli", Kind: serverconfig.DelegationExecutorKindBuiltin, Enabled: true, Priority: 2, Executable: "C:/tools/kiro-cli.exe"}}
	if _, err := host.ConfigManager().SaveDelegationConfig(t.Context(), cfg); err != nil {
		t.Fatalf("SaveDelegationConfig() error = %v", err)
	}
	snapshot, ok = host.ExecutorRegistry().Snapshot("kiro-cli")
	if !ok || !snapshot.Enabled || snapshot.Priority != 2 || snapshot.Probe.State != delegation.ExecutorProbeUnknown {
		t.Fatalf("updated Kiro snapshot = %#v, ok=%t", snapshot, ok)
	}
}

func TestHostRegistersCursorExecutorSeparatelyFromEditorLaunch(t *testing.T) {
	store := serverconfig.NewStore(filepath.Join(t.TempDir(), "config.yaml"), "")
	host, err := NewHost(store, nil)
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	t.Cleanup(func() { _ = host.Stop(context.Background()) })

	snapshot, ok := host.ExecutorRegistry().Snapshot("cursor-agent")
	if !ok || snapshot.Enabled {
		t.Fatalf("default Cursor snapshot = %#v, ok=%t", snapshot, ok)
	}
}

func TestHostRegistersAndRemovesConfiguredCustomExecutor(t *testing.T) {
	store := serverconfig.NewStore(filepath.Join(t.TempDir(), "config.yaml"), "")
	host, err := NewHost(store, nil)
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	t.Cleanup(func() { _ = host.Stop(context.Background()) })
	if _, ok := host.ExecutorRegistry().Snapshot("grok-cli"); ok {
		t.Fatal("unconfigured grok-cli must not be registered")
	}
	cfg, err := host.ConfigManager().GetDelegationConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Executors = []serverconfig.DelegationExecutorConfig{{
		ID: "grok-cli", Kind: serverconfig.DelegationExecutorKindCustom, DisplayName: "Grok Compatible", Enabled: true, Executable: "grok", Priority: 7,
		Options: map[string]string{"arguments": `["{{prompt}}"]`, "versionArguments": `["--version"]`, "stdinMode": "none", "outputMode": "text", "outputLimitBytes": "1024"},
	}}
	if _, err := host.ConfigManager().SaveDelegationConfig(t.Context(), cfg); err != nil {
		t.Fatalf("SaveDelegationConfig() error=%v", err)
	}
	snapshot, ok := host.ExecutorRegistry().Snapshot("grok-cli")
	if !ok || !snapshot.Enabled || snapshot.Priority != 7 || snapshot.DisplayName != "Grok Compatible" {
		t.Fatalf("custom snapshot=%#v ok=%t", snapshot, ok)
	}
	cfg.Executors = nil
	if _, err := host.ConfigManager().SaveDelegationConfig(t.Context(), cfg); err != nil {
		t.Fatalf("clear custom executor error=%v", err)
	}
	if _, ok := host.ExecutorRegistry().Snapshot("grok-cli"); ok {
		t.Fatal("removed custom executor remains registered")
	}
	for _, id := range []delegation.ExecutorID{"claude-code", "codex-cli", "gemini-cli"} {
		if _, ok := host.ExecutorRegistry().Snapshot(id); !ok {
			t.Fatalf("builtin executor %s disappeared", id)
		}
	}
}
