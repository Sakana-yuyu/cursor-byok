package delegation

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecutorFailoverUsesPriorityAndRequiredCapabilities(t *testing.T) {
	registry := newReadyFailoverRegistry(t,
		failoverRegistration("read-only", 1, []ExecutorCapability{ExecutorCapabilityReadWorkspace}, TaskResult{Output: "wrong"}),
		failoverRegistration("writer", 2, []ExecutorCapability{ExecutorCapabilityReadWorkspace, ExecutorCapabilityWriteWorkspace}, TaskResult{Output: "selected"}),
	)
	executor := NewFailoverExecutor(FailoverExecutorConfig{
		Registry:    registry,
		MaxAttempts: func() int { return 3 },
		RequiredCapabilities: func(TaskRequest) []ExecutorCapability {
			return []ExecutorCapability{ExecutorCapabilityReadWorkspace, ExecutorCapabilityWriteWorkspace}
		},
	})

	result := executor(context.Background(), TaskRequest{ID: "task-1"})
	if result.Error != nil || result.Output != "selected" || result.ExecutorID != "writer" {
		t.Fatalf("failover result = %#v", result)
	}
	assertAttemptIDs(t, result.Attempts, "writer")
}

func TestExecutorFailoverAdvancesOnlyForRetrySafeSwitchableFailure(t *testing.T) {
	firstErr := NewClassifiedExecutorError(ExecutorFailureSwitchable, true, "rate_limited", errors.New("rate limited"))
	registry := newReadyFailoverRegistry(t,
		failoverRegistration("first", 1, nil, TaskResult{Output: "partial", Error: firstErr}),
		failoverRegistration("second", 2, nil, TaskResult{Output: "done"}),
	)
	executor := NewFailoverExecutor(FailoverExecutorConfig{Registry: registry, MaxAttempts: func() int { return 3 }})

	result := executor(context.Background(), TaskRequest{ID: "task-2"})
	if result.Error != nil || result.Output != "done" || result.ExecutorID != "second" {
		t.Fatalf("failover result = %#v", result)
	}
	assertAttemptIDs(t, result.Attempts, "first", "second")
	if result.Attempts[0].FailureClass != ExecutorFailureSwitchable || !result.Attempts[0].RetrySafe {
		t.Fatalf("first attempt classification = %#v", result.Attempts[0])
	}
	if result.Metadata[ExecutorMetadataPreviousOutputKey] != "partial" {
		t.Fatalf("partial output evidence = %#v", result.Metadata)
	}
}

func TestExecutorFailoverStopsForNonSwitchableOrUnsafeFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "user action", err: NewClassifiedExecutorError(ExecutorFailureUserActionRequired, false, "login_required", errors.New("login required"))},
		{name: "terminal", err: NewClassifiedExecutorError(ExecutorFailureTerminal, false, "unsafe_side_effect", errors.New("unsafe side effect"))},
		{name: "unsafe switchable", err: NewClassifiedExecutorError(ExecutorFailureSwitchable, false, "partial_side_effect", errors.New("partial side effect"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var secondCalls atomic.Int32
			second := failoverRegistration("second", 2, nil, TaskResult{Output: "must not run"})
			second.Execute = func(context.Context, TaskRequest) TaskResult {
				secondCalls.Add(1)
				return TaskResult{Output: "must not run"}
			}
			registry := newReadyFailoverRegistry(t,
				failoverRegistration("first", 1, nil, TaskResult{Error: test.err}),
				second,
			)
			result := NewFailoverExecutor(FailoverExecutorConfig{Registry: registry, MaxAttempts: func() int { return 3 }})(context.Background(), TaskRequest{})
			if !errors.Is(result.Error, test.err) || secondCalls.Load() != 0 {
				t.Fatalf("result=%#v second_calls=%d", result, secondCalls.Load())
			}
			assertAttemptIDs(t, result.Attempts, "first")
		})
	}
}

func TestExecutorFailoverRespectsMaxAttempts(t *testing.T) {
	switchable := func(code string) error {
		return NewClassifiedExecutorError(ExecutorFailureSwitchable, true, code, errors.New(code))
	}
	var thirdCalls atomic.Int32
	third := failoverRegistration("third", 3, nil, TaskResult{Output: "third"})
	third.Execute = func(context.Context, TaskRequest) TaskResult {
		thirdCalls.Add(1)
		return TaskResult{Output: "third"}
	}
	registry := newReadyFailoverRegistry(t,
		failoverRegistration("first", 1, nil, TaskResult{Error: switchable("first_failed")}),
		failoverRegistration("second", 2, nil, TaskResult{Error: switchable("second_failed")}),
		third,
	)
	result := NewFailoverExecutor(FailoverExecutorConfig{Registry: registry, MaxAttempts: func() int { return 2 }})(context.Background(), TaskRequest{})
	if result.Error == nil || thirdCalls.Load() != 0 || result.ExecutorID != "second" {
		t.Fatalf("result=%#v third_calls=%d", result, thirdCalls.Load())
	}
	assertAttemptIDs(t, result.Attempts, "first", "second")
}

func TestExecutorFailoverSnapshotsAttemptLimitOnce(t *testing.T) {
	registry := newReadyFailoverRegistry(t, failoverRegistration("only", 1, nil, TaskResult{Output: "done"}))
	var reads atomic.Int32
	executor := NewFailoverExecutor(FailoverExecutorConfig{
		Registry: registry,
		MaxAttempts: func() int {
			reads.Add(1)
			return 2
		},
	})
	result := executor(context.Background(), TaskRequest{})
	if result.Error != nil || reads.Load() != 1 {
		t.Fatalf("result=%#v max-attempt reads=%d", result, reads.Load())
	}
}

func TestExecutorFailoverCancellationStopsActiveAttempt(t *testing.T) {
	started := make(chan struct{})
	var secondCalls atomic.Int32
	first := failoverRegistration("first", 1, nil, TaskResult{})
	first.Execute = func(ctx context.Context, _ TaskRequest) TaskResult {
		close(started)
		<-ctx.Done()
		return TaskResult{Error: ctx.Err()}
	}
	second := failoverRegistration("second", 2, nil, TaskResult{Output: "must not run"})
	second.Execute = func(context.Context, TaskRequest) TaskResult {
		secondCalls.Add(1)
		return TaskResult{Output: "must not run"}
	}
	registry := newReadyFailoverRegistry(t, first, second)
	executor := NewFailoverExecutor(FailoverExecutorConfig{Registry: registry, MaxAttempts: func() int { return 2 }})
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan TaskResult, 1)
	go func() { results <- executor(ctx, TaskRequest{}) }()
	<-started
	cancel()
	select {
	case result := <-results:
		if !errors.Is(result.Error, context.Canceled) || secondCalls.Load() != 0 {
			t.Fatalf("result=%#v second_calls=%d", result, secondCalls.Load())
		}
		assertAttemptIDs(t, result.Attempts, "first")
		if result.Attempts[0].Status != ExecutorAttemptCanceled {
			t.Fatalf("attempt status = %q", result.Attempts[0].Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled executor did not return")
	}
}

func TestExecutorFailoverUsesFallbackWhenNoEligibleExecutor(t *testing.T) {
	registry := NewExecutorRegistry(ExecutorRegistryConfig{})
	fallback := func(_ context.Context, request TaskRequest) TaskResult {
		return TaskResult{Output: request.ID + ":local"}
	}
	executor := NewFailoverExecutor(FailoverExecutorConfig{
		Registry:    registry,
		MaxAttempts: func() int { return 3 },
		FallbackID:  ExecutorID("local-byok"),
		Fallback:    fallback,
	})

	result := executor(context.Background(), TaskRequest{ID: "task-3"})
	if result.Error != nil || result.Output != "task-3:local" || result.ExecutorID != "local-byok" {
		t.Fatalf("fallback result = %#v", result)
	}
	assertAttemptIDs(t, result.Attempts, "local-byok")
	if result.Attempts[0].Status != ExecutorAttemptCompleted {
		t.Fatalf("fallback attempt = %#v", result.Attempts[0])
	}
}

func TestExecutorFailoverPublishesRunningAttemptToSchedulerSnapshot(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	registration := failoverRegistration("blocking", 1, nil, TaskResult{})
	registration.Execute = func(context.Context, TaskRequest) TaskResult {
		close(started)
		<-release
		return TaskResult{Output: "done"}
	}
	registry := newReadyFailoverRegistry(t, registration)
	executor := NewFailoverExecutor(FailoverExecutorConfig{Registry: registry, MaxAttempts: func() int { return 1 }})
	scheduler := NewScheduler(Config{MaxConcurrency: 1}, executor)
	defer scheduler.Close()
	taskID, err := scheduler.Submit(TaskRequest{ID: "running-attempt"})
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	<-started
	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, ok := scheduler.Snapshot(taskID)
		if ok && snapshot.ExecutorID == "blocking" && len(snapshot.Attempts) == 1 && snapshot.Attempts[0].Status == ExecutorAttemptRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("running attempt snapshot = %#v, ok=%t", snapshot, ok)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := scheduler.WaitForTerminal(ctx, []string{taskID}); err != nil {
		t.Fatalf("WaitForTerminal(): %v", err)
	}
}

func TestExecutorFailoverPublishesRunningFallbackToSchedulerSnapshot(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	executor := NewFailoverExecutor(FailoverExecutorConfig{
		Registry:    NewExecutorRegistry(ExecutorRegistryConfig{}),
		MaxAttempts: func() int { return 1 },
		FallbackID:  "local-byok",
		Fallback: func(context.Context, TaskRequest) TaskResult {
			close(started)
			<-release
			return TaskResult{Output: "done"}
		},
	})
	scheduler := NewScheduler(Config{MaxConcurrency: 1}, executor)
	defer scheduler.Close()
	taskID, err := scheduler.Submit(TaskRequest{ID: "running-fallback"})
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	<-started
	snapshot, ok := scheduler.Snapshot(taskID)
	if !ok || snapshot.ExecutorID != "local-byok" || len(snapshot.Attempts) != 1 || snapshot.Attempts[0].Status != ExecutorAttemptRunning {
		t.Fatalf("running fallback snapshot = %#v, ok=%t", snapshot, ok)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := scheduler.WaitForTerminal(ctx, []string{taskID}); err != nil {
		t.Fatalf("WaitForTerminal(): %v", err)
	}
}

func newReadyFailoverRegistry(t *testing.T, registrations ...ExecutorRegistration) *ExecutorRegistry {
	t.Helper()
	registry := NewExecutorRegistry(ExecutorRegistryConfig{})
	for _, registration := range registrations {
		if err := registry.Register(registration); err != nil {
			t.Fatalf("Register(%s): %v", registration.ID, err)
		}
		if _, err := registry.Probe(context.Background(), registration.ID, true); err != nil {
			t.Fatalf("Probe(%s): %v", registration.ID, err)
		}
	}
	return registry
}

func failoverRegistration(id ExecutorID, priority int, capabilities []ExecutorCapability, result TaskResult) ExecutorRegistration {
	if len(capabilities) == 0 {
		capabilities = []ExecutorCapability{ExecutorCapabilityReadWorkspace, ExecutorCapabilityWriteWorkspace}
	}
	return ExecutorRegistration{
		ID: id, DisplayName: string(id), Enabled: true, Priority: priority, Capabilities: capabilities,
		Probe: func(context.Context) (ExecutorProbeResult, error) {
			return ExecutorProbeResult{State: ExecutorProbeReady, Capabilities: capabilities}, nil
		},
		Execute: func(context.Context, TaskRequest) TaskResult { return result },
	}
}

func assertAttemptIDs(t *testing.T, attempts []ExecutorAttemptSnapshot, want ...ExecutorID) {
	t.Helper()
	got := make([]ExecutorID, 0, len(attempts))
	for _, attempt := range attempts {
		got = append(got, attempt.ExecutorID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attempt IDs = %v, want %v", got, want)
	}
	for index, attempt := range attempts {
		if attempt.Attempt != index+1 {
			t.Fatalf("attempt[%d].Attempt = %d, want %d", index, attempt.Attempt, index+1)
		}
	}
}
