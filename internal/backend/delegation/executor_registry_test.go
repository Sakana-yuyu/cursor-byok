package delegation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func readyExecutorRegistration(id ExecutorID, priority int, capabilities ...ExecutorCapability) ExecutorRegistration {
	return ExecutorRegistration{
		ID:           id,
		DisplayName:  string(id),
		Enabled:      true,
		Priority:     priority,
		Capabilities: capabilities,
		Probe: func(context.Context) (ExecutorProbeResult, error) {
			return ExecutorProbeResult{State: ExecutorProbeReady}, nil
		},
		Execute: func(context.Context, TaskRequest) TaskResult {
			return TaskResult{Output: string(id)}
		},
	}
}

func TestExecutorRegistryRejectsDuplicateRegistration(t *testing.T) {
	registry := NewExecutorRegistry(ExecutorRegistryConfig{})
	registration := readyExecutorRegistration(ExecutorID("codex-cli"), 10, ExecutorCapabilityReadWorkspace)
	if err := registry.Register(registration); err != nil {
		t.Fatalf("Register() first error = %v", err)
	}
	if err := registry.Register(registration); err == nil {
		t.Fatal("Register() duplicate error = nil")
	}
}

func TestExecutorRegistryReplaceUpdatesPolicyAndClearsProbe(t *testing.T) {
	registry := NewExecutorRegistry(ExecutorRegistryConfig{})
	registration := readyExecutorRegistration("claude-code", 10, ExecutorCapabilityReadWorkspace)
	if err := registry.Register(registration); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := registry.Probe(t.Context(), registration.ID, false); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	replacement := readyExecutorRegistration("claude-code", 2, ExecutorCapabilityReadWorkspace, ExecutorCapabilityWriteWorkspace)
	replacement.Enabled = false
	if err := registry.Replace(replacement); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	snapshot, ok := registry.Snapshot("claude-code")
	if !ok {
		t.Fatal("Snapshot() missing replacement")
	}
	if snapshot.Enabled || snapshot.Priority != 2 || snapshot.Probe.State != ExecutorProbeUnknown || len(snapshot.Capabilities) != 2 {
		t.Fatalf("Snapshot() = %#v", snapshot)
	}
}

func TestExecutorRegistryReplaceDiscardsInFlightProbeResult(t *testing.T) {
	registry := NewExecutorRegistry(ExecutorRegistryConfig{})
	started := make(chan struct{})
	release := make(chan struct{})
	registration := readyExecutorRegistration("claude-code", 10, ExecutorCapabilityReadWorkspace)
	registration.Probe = func(context.Context) (ExecutorProbeResult, error) {
		close(started)
		<-release
		return ExecutorProbeResult{State: ExecutorProbeReady, Version: "stale"}, nil
	}
	if err := registry.Register(registration); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	probeResult := make(chan ExecutorProbeResult, 1)
	go func() {
		result, _ := registry.Probe(t.Context(), registration.ID, true)
		probeResult <- result
	}()
	<-started
	replacement := readyExecutorRegistration("claude-code", 1, ExecutorCapabilityReadWorkspace)
	if err := registry.Replace(replacement); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	close(release)
	returned := <-probeResult
	if returned.State != ExecutorProbeUnknown || returned.Version != "" {
		t.Fatalf("Probe() returned stale result = %#v", returned)
	}
	snapshot, ok := registry.Snapshot("claude-code")
	if !ok || snapshot.Probe.State != ExecutorProbeUnknown || snapshot.Probe.Version != "" || snapshot.Priority != 1 {
		t.Fatalf("Snapshot() = %#v, ok=%t", snapshot, ok)
	}
}

func TestExecutorRegistryUnregisterDiscardsInFlightProbeResult(t *testing.T) {
	registry := NewExecutorRegistry(ExecutorRegistryConfig{})
	started := make(chan struct{})
	release := make(chan struct{})
	registration := readyExecutorRegistration("custom-cli", 10, ExecutorCapabilityReadWorkspace)
	registration.Probe = func(context.Context) (ExecutorProbeResult, error) {
		close(started)
		<-release
		return ExecutorProbeResult{State: ExecutorProbeReady, Version: "stale"}, nil
	}
	if err := registry.Register(registration); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = registry.Probe(t.Context(), registration.ID, true)
		close(done)
	}()
	<-started
	if !registry.Unregister(registration.ID) {
		t.Fatal("Unregister() = false")
	}
	close(release)
	<-done
	if _, ok := registry.Snapshot(registration.ID); ok {
		t.Fatal("unregistered executor was restored by stale probe")
	}
}

func TestExecutorRegistryEligibleUsesStablePriorityAndFilters(t *testing.T) {
	now := time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)
	registry := NewExecutorRegistry(ExecutorRegistryConfig{Now: func() time.Time { return now }})

	registrations := []ExecutorRegistration{
		readyExecutorRegistration(ExecutorID("ready-b"), 10, ExecutorCapabilityReadWorkspace, ExecutorCapabilityWriteWorkspace),
		readyExecutorRegistration(ExecutorID("ready-a"), 10, ExecutorCapabilityReadWorkspace, ExecutorCapabilityWriteWorkspace),
		readyExecutorRegistration(ExecutorID("ready-low"), 20, ExecutorCapabilityReadWorkspace, ExecutorCapabilityWriteWorkspace),
		readyExecutorRegistration(ExecutorID("read-only"), 1, ExecutorCapabilityReadWorkspace),
		{
			ID: ExecutorID("disabled"), DisplayName: "disabled", Enabled: false, Priority: 0,
			Capabilities: []ExecutorCapability{ExecutorCapabilityReadWorkspace, ExecutorCapabilityWriteWorkspace},
			Probe: func(context.Context) (ExecutorProbeResult, error) {
				return ExecutorProbeResult{State: ExecutorProbeReady}, nil
			},
			Execute: func(context.Context, TaskRequest) TaskResult { return TaskResult{} },
		},
		{
			ID: ExecutorID("unhealthy"), DisplayName: "unhealthy", Enabled: true, Priority: 0,
			Capabilities: []ExecutorCapability{ExecutorCapabilityReadWorkspace, ExecutorCapabilityWriteWorkspace},
			Probe: func(context.Context) (ExecutorProbeResult, error) {
				return ExecutorProbeResult{State: ExecutorProbeUnhealthy, DiagnosticCode: "probe_failed"}, nil
			},
			Execute: func(context.Context, TaskRequest) TaskResult { return TaskResult{} },
		},
	}
	for _, registration := range registrations {
		if err := registry.Register(registration); err != nil {
			t.Fatalf("Register(%s): %v", registration.ID, err)
		}
		if registration.Enabled {
			if _, err := registry.Probe(context.Background(), registration.ID, true); err != nil {
				t.Fatalf("Probe(%s): %v", registration.ID, err)
			}
		}
	}

	eligible := registry.Eligible([]ExecutorCapability{ExecutorCapabilityWriteWorkspace})
	got := make([]ExecutorID, 0, len(eligible))
	for _, snapshot := range eligible {
		got = append(got, snapshot.ID)
	}
	want := []ExecutorID{"ready-a", "ready-b", "ready-low"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Eligible() IDs = %v, want %v", got, want)
	}
}

func TestExecutorRegistryProbeCacheAndRefresh(t *testing.T) {
	now := time.Date(2026, 8, 10, 15, 1, 0, 0, time.UTC)
	var calls atomic.Int32
	registry := NewExecutorRegistry(ExecutorRegistryConfig{
		Now:           func() time.Time { return now },
		ProbeCacheTTL: 30 * time.Second,
	})
	registration := readyExecutorRegistration(ExecutorID("claude-code"), 10, ExecutorCapabilityReadWorkspace)
	registration.Probe = func(context.Context) (ExecutorProbeResult, error) {
		call := calls.Add(1)
		return ExecutorProbeResult{
			State:          ExecutorProbeReady,
			Version:        "v" + string(rune('0'+call)),
			Capabilities:   []ExecutorCapability{ExecutorCapabilityReadWorkspace},
			DiagnosticCode: "ready",
		}, nil
	}
	if err := registry.Register(registration); err != nil {
		t.Fatalf("Register(): %v", err)
	}

	first, err := registry.Probe(context.Background(), registration.ID, false)
	if err != nil {
		t.Fatalf("Probe() first: %v", err)
	}
	second, err := registry.Probe(context.Background(), registration.ID, false)
	if err != nil {
		t.Fatalf("Probe() cached: %v", err)
	}
	if calls.Load() != 1 || first.Version != second.Version {
		t.Fatalf("cached probe calls = %d, versions = %q/%q", calls.Load(), first.Version, second.Version)
	}
	second.Capabilities[0] = ExecutorCapabilityWriteWorkspace

	now = now.Add(31 * time.Second)
	refreshed, err := registry.Probe(context.Background(), registration.ID, false)
	if err != nil {
		t.Fatalf("Probe() expired cache refresh: %v", err)
	}
	if calls.Load() != 2 || refreshed.Version == first.Version {
		t.Fatalf("refreshed probe calls = %d, versions = %q/%q", calls.Load(), first.Version, refreshed.Version)
	}
	if refreshed.Capabilities[0] != ExecutorCapabilityReadWorkspace {
		t.Fatalf("cached probe mutation leaked: %v", refreshed.Capabilities)
	}
	if !refreshed.ProbedAt.Equal(now) {
		t.Fatalf("refreshed ProbedAt = %v, want %v", refreshed.ProbedAt, now)
	}
}

func TestExecutorRegistryProbeCoalescesConcurrentCallsAndCachesErrors(t *testing.T) {
	probeStarted := make(chan struct{})
	releaseProbe := make(chan struct{})
	probeErr := errors.New("probe crashed")
	var calls atomic.Int32
	registry := NewExecutorRegistry(ExecutorRegistryConfig{})
	registration := readyExecutorRegistration(ExecutorID("shared-probe"), 10, ExecutorCapabilityReadWorkspace)
	registration.Probe = func(context.Context) (ExecutorProbeResult, error) {
		if calls.Add(1) == 1 {
			close(probeStarted)
		}
		<-releaseProbe
		return ExecutorProbeResult{State: ExecutorProbeUnhealthy, DiagnosticCode: "probe_crashed"}, probeErr
	}
	if err := registry.Register(registration); err != nil {
		t.Fatalf("Register(): %v", err)
	}

	const callers = 8
	results := make(chan error, callers)
	var started sync.WaitGroup
	started.Add(callers)
	for range callers {
		go func() {
			started.Done()
			_, err := registry.Probe(context.Background(), registration.ID, false)
			results <- err
		}()
	}
	started.Wait()
	<-probeStarted
	close(releaseProbe)
	for range callers {
		if err := <-results; !errors.Is(err, probeErr) {
			t.Fatalf("concurrent Probe() error = %v, want %v", err, probeErr)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent Probe() calls = %d, want 1", calls.Load())
	}

	if _, err := registry.Probe(context.Background(), registration.ID, false); !errors.Is(err, probeErr) {
		t.Fatalf("cached Probe() error = %v, want %v", err, probeErr)
	}
	if calls.Load() != 1 {
		t.Fatalf("cached failed Probe() calls = %d, want 1", calls.Load())
	}
}

func TestExecutorRegistrySnapshotsCloneCapabilities(t *testing.T) {
	registry := NewExecutorRegistry(ExecutorRegistryConfig{})
	registration := readyExecutorRegistration("snapshot-clone", 10, ExecutorCapabilityReadWorkspace)
	if err := registry.Register(registration); err != nil {
		t.Fatalf("Register(): %v", err)
	}
	if _, err := registry.Probe(context.Background(), registration.ID, false); err != nil {
		t.Fatalf("Probe(): %v", err)
	}

	snapshot, ok := registry.Snapshot(registration.ID)
	if !ok {
		t.Fatal("Snapshot() missing")
	}
	snapshot.Capabilities[0] = ExecutorCapabilityWriteWorkspace
	snapshot.Probe.Capabilities[0] = ExecutorCapabilityShell
	all := registry.Snapshots()
	all[0].Capabilities[0] = ExecutorCapabilityNetwork
	all[0].Probe.Capabilities[0] = ExecutorCapabilityMCP

	fresh, ok := registry.Snapshot(registration.ID)
	if !ok {
		t.Fatal("Snapshot() missing after mutation")
	}
	if got := fresh.Capabilities; !reflect.DeepEqual(got, []ExecutorCapability{ExecutorCapabilityReadWorkspace}) {
		t.Fatalf("Snapshot() capabilities after caller mutation = %v", got)
	}
	if got := fresh.Probe.Capabilities; !reflect.DeepEqual(got, []ExecutorCapability{ExecutorCapabilityReadWorkspace}) {
		t.Fatalf("Snapshot() probe capabilities after caller mutation = %v", got)
	}
}

func TestExecutorRegistrySnapshotUsesUnknownStateBeforeProbe(t *testing.T) {
	registry := NewExecutorRegistry(ExecutorRegistryConfig{})
	registration := readyExecutorRegistration("not-probed", 10, ExecutorCapabilityReadWorkspace)
	if err := registry.Register(registration); err != nil {
		t.Fatalf("Register(): %v", err)
	}

	snapshot, ok := registry.Snapshot(registration.ID)
	if !ok {
		t.Fatal("Snapshot() missing")
	}
	if snapshot.Probe.State != ExecutorProbeUnknown {
		t.Fatalf("Snapshot() unprobed state = %q, want %q", snapshot.Probe.State, ExecutorProbeUnknown)
	}
}

func TestExecutorRegistrySwitchableFailureCooldown(t *testing.T) {
	now := time.Date(2026, 8, 10, 15, 2, 0, 0, time.UTC)
	registry := NewExecutorRegistry(ExecutorRegistryConfig{
		Now:             func() time.Time { return now },
		FailureCooldown: 30 * time.Second,
	})
	registration := readyExecutorRegistration(ExecutorID("gemini-cli"), 10, ExecutorCapabilityReadWorkspace)
	if err := registry.Register(registration); err != nil {
		t.Fatalf("Register(): %v", err)
	}
	if _, err := registry.Probe(context.Background(), registration.ID, true); err != nil {
		t.Fatalf("Probe(): %v", err)
	}
	if got := registry.Eligible([]ExecutorCapability{ExecutorCapabilityReadWorkspace}); len(got) != 1 {
		t.Fatalf("Eligible() before failure = %d, want 1", len(got))
	}

	registry.RecordFailure(registration.ID, NewClassifiedExecutorError(
		ExecutorFailureSwitchable,
		true,
		"rate_limited",
		errors.New("rate limited"),
	))
	if got := registry.Eligible([]ExecutorCapability{ExecutorCapabilityReadWorkspace}); len(got) != 0 {
		t.Fatalf("Eligible() during cooldown = %v, want none", got)
	}
	snapshot, ok := registry.Snapshot(registration.ID)
	if !ok || !snapshot.CooldownUntil.Equal(now.Add(30*time.Second)) {
		t.Fatalf("Snapshot() cooldown = %v, ok=%t", snapshot.CooldownUntil, ok)
	}

	registry.RecordFailure(registration.ID, NewClassifiedExecutorError(
		ExecutorFailureUserActionRequired,
		false,
		"login_required",
		errors.New("login required"),
	))
	snapshot, ok = registry.Snapshot(registration.ID)
	if !ok || !snapshot.CooldownUntil.IsZero() {
		t.Fatalf("non-switchable failure retained cooldown = %v, ok=%t", snapshot.CooldownUntil, ok)
	}
	if got := registry.Eligible([]ExecutorCapability{ExecutorCapabilityReadWorkspace}); len(got) != 1 {
		t.Fatalf("Eligible() after non-switchable failure = %v, want one", got)
	}

	registry.RecordFailure(registration.ID, NewClassifiedExecutorError(
		ExecutorFailureTerminal,
		false,
		"unsafe_side_effect",
		errors.New("unsafe side effect"),
	))
	snapshot, ok = registry.Snapshot(registration.ID)
	if !ok || !snapshot.CooldownUntil.IsZero() {
		t.Fatalf("terminal failure created cooldown = %v, ok=%t", snapshot.CooldownUntil, ok)
	}

	now = now.Add(31 * time.Second)
	if got := registry.Eligible([]ExecutorCapability{ExecutorCapabilityReadWorkspace}); len(got) != 1 {
		t.Fatalf("Eligible() after cooldown = %v, want one", got)
	}
}

func TestExecutorAttemptStatusRecognizesWrappedContextErrors(t *testing.T) {
	if got := executorAttemptStatusForError(context.Background(), fmt.Errorf("wrapped: %w", context.Canceled)); got != ExecutorAttemptCanceled {
		t.Fatalf("wrapped cancellation status = %q", got)
	}
	if got := executorAttemptStatusForError(context.Background(), fmt.Errorf("wrapped: %w", context.DeadlineExceeded)); got != ExecutorAttemptTimedOut {
		t.Fatalf("wrapped deadline status = %q", got)
	}
}

func TestExecutorClassifiedErrorExposesFailureAndRetrySafety(t *testing.T) {
	cause := errors.New("login required")
	err := NewClassifiedExecutorError(ExecutorFailureUserActionRequired, false, "login_required", cause)
	if err.Error() != cause.Error() || !errors.Is(err, cause) {
		t.Fatalf("classified error wrapping = %q, errors.Is=%t", err.Error(), errors.Is(err, cause))
	}
	class, retrySafe := ExecutorErrorClassification(err)
	if class != ExecutorFailureUserActionRequired || retrySafe {
		t.Fatalf("classification = %q retry_safe=%t", class, retrySafe)
	}
	if err.Code() != "login_required" {
		t.Fatalf("Code() = %q", err.Code())
	}
}

func TestExecutorRegistryWrapperUsesExistingExecutorFunction(t *testing.T) {
	registry := NewExecutorRegistry(ExecutorRegistryConfig{})
	registration := readyExecutorRegistration(ExecutorID("local-provider"), 10, ExecutorCapabilityReadWorkspace)
	registration.Execute = func(_ context.Context, request TaskRequest) TaskResult {
		return TaskResult{Output: request.ID + ":ok"}
	}
	if err := registry.Register(registration); err != nil {
		t.Fatalf("Register(): %v", err)
	}
	executor, err := registry.Executor(registration.ID)
	if err != nil {
		t.Fatalf("Executor(): %v", err)
	}
	result := executor(context.Background(), TaskRequest{ID: "task-1"})
	if result.Output != "task-1:ok" || result.ExecutorID != registration.ID || result.Metadata[ExecutorMetadataIDKey] != string(registration.ID) {
		t.Fatalf("wrapped executor result = %#v", result)
	}
	if len(result.Attempts) != 1 || result.Attempts[0].ExecutorID != registration.ID || result.Attempts[0].Status != ExecutorAttemptCompleted {
		t.Fatalf("wrapped executor attempts = %#v", result.Attempts)
	}
}

func TestSchedulerRecordsRegistryExecutorAttempt(t *testing.T) {
	registry := NewExecutorRegistry(ExecutorRegistryConfig{})
	registration := readyExecutorRegistration(ExecutorID("codex-cli"), 10, ExecutorCapabilityReadWorkspace)
	if err := registry.Register(registration); err != nil {
		t.Fatalf("Register(): %v", err)
	}
	executor, err := registry.Executor(registration.ID)
	if err != nil {
		t.Fatalf("Executor(): %v", err)
	}
	scheduler := NewScheduler(Config{MaxConcurrency: 1}, executor)
	defer scheduler.Close()
	taskID, err := scheduler.Submit(TaskRequest{ID: "registry-task"})
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := scheduler.WaitForTerminal(ctx, []string{taskID}); err != nil {
		t.Fatalf("WaitForTerminal(): %v", err)
	}
	snapshot, ok := scheduler.Snapshot(taskID)
	if !ok {
		t.Fatal("Snapshot() missing")
	}
	if snapshot.ExecutorID != registration.ID || len(snapshot.Attempts) != 1 || snapshot.Attempts[0].ExecutorID != registration.ID {
		t.Fatalf("registry scheduler snapshot = %#v", snapshot)
	}
}

func TestSchedulerAttemptSnapshotClonesSafelyAndDefaultsEmpty(t *testing.T) {
	scheduler := NewScheduler(Config{MaxConcurrency: 1}, func(context.Context, TaskRequest) TaskResult {
		return TaskResult{Output: "done"}
	})
	defer scheduler.Close()

	taskID, err := scheduler.Submit(TaskRequest{ID: "legacy-task"})
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := scheduler.WaitForTerminal(ctx, []string{taskID}); err != nil {
		t.Fatalf("WaitForTerminal(): %v", err)
	}
	snapshot, ok := scheduler.Snapshot(taskID)
	if !ok {
		t.Fatal("Snapshot() missing")
	}
	if snapshot.ExecutorID != "" || len(snapshot.Attempts) != 0 {
		t.Fatalf("legacy snapshot executor fields = %q %#v", snapshot.ExecutorID, snapshot.Attempts)
	}

	original := TaskSnapshot{
		ExecutorID: ExecutorID("codex-cli"),
		Attempts: []ExecutorAttemptSnapshot{{
			ExecutorID: ExecutorID("codex-cli"),
			Attempt:    1,
			Status:     ExecutorAttemptFailed,
			Metadata:   map[string]string{"path": "codex"},
		}},
	}
	cloned := cloneTaskSnapshot(original)
	cloned.Attempts[0].ExecutorID = ExecutorID("mutated")
	cloned.Attempts[0].Metadata["path"] = "mutated"
	if original.Attempts[0].ExecutorID != ExecutorID("codex-cli") || original.Attempts[0].Metadata["path"] != "codex" {
		t.Fatalf("clone mutation leaked into original: %#v", original.Attempts)
	}
}

func TestSanitizeTaskResultSanitizesExecutorAttempts(t *testing.T) {
	workspace := t.TempDir()
	secret := "sk-task14-secret-value"
	result := TaskResult{
		Attempts: []ExecutorAttemptSnapshot{{
			ExecutorID: ExecutorID("codex-cli"),
			Status:     ExecutorAttemptFailed,
			Error:      "failed in " + workspace + " with token " + secret,
			Metadata: map[string]string{
				"cwd":   workspace,
				"token": secret,
			},
		}},
	}

	sanitized := SanitizeTaskResult(result, workspace)
	if len(sanitized.Attempts) != 1 {
		t.Fatalf("SanitizeTaskResult() attempts = %#v", sanitized.Attempts)
	}
	if sanitized.Attempts[0].Error == result.Attempts[0].Error {
		t.Fatalf("attempt error was not sanitized: %q", sanitized.Attempts[0].Error)
	}
	if sanitized.Attempts[0].Metadata["token"] == secret || sanitized.Attempts[0].Metadata["cwd"] == workspace {
		t.Fatalf("attempt metadata was not sanitized: %#v", sanitized.Attempts[0].Metadata)
	}
	sanitized.Attempts[0].Metadata["cwd"] = "mutated"
	if result.Attempts[0].Metadata["cwd"] != workspace {
		t.Fatalf("sanitized attempt mutation leaked into input: %#v", result.Attempts[0].Metadata)
	}
}
