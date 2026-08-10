package forwarder

import (
	"context"
	"testing"
	"time"

	"cursor/internal/backend/delegation"
)

func TestDelegationExecutorRuntimeExposesSanitizedAttemptTimeline(t *testing.T) {
	executor := func(context.Context, delegation.TaskRequest) delegation.TaskResult {
		return delegation.TaskResult{
			Output:     "done",
			ExecutorID: "codex-cli",
			Attempts: []delegation.ExecutorAttemptSnapshot{
				{
					ExecutorID: "claude-code", Attempt: 1, Status: delegation.ExecutorAttemptFailed,
					FailureClass: delegation.ExecutorFailureSwitchable, RetrySafe: true,
					DiagnosticCode: "rate_limited", Error: "rate limited",
					StartedAt:  time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC),
					FinishedAt: time.Date(2026, 8, 10, 15, 0, 2, 0, time.UTC),
					Metadata:   map[string]string{"secret": "must-not-be-public"},
				},
				{
					ExecutorID: "codex-cli", Attempt: 2, Status: delegation.ExecutorAttemptCompleted,
					StartedAt:  time.Date(2026, 8, 10, 15, 0, 2, 0, time.UTC),
					FinishedAt: time.Date(2026, 8, 10, 15, 0, 5, 0, time.UTC),
				},
			},
		}
	}
	service := &Service{}
	service.multitaskDelegation = &multitaskDelegationCoordinator{scheduler: delegation.NewScheduler(delegation.Config{MaxConcurrency: 1}, executor)}
	defer service.multitaskDelegation.Close()
	taskID, err := service.multitaskDelegation.scheduler.Submit(delegation.TaskRequest{ID: "runtime-executor", ExecutionMode: delegation.ExecutionModeAuto})
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.multitaskDelegation.scheduler.WaitForTerminal(ctx, []string{taskID}); err != nil {
		t.Fatalf("WaitForTerminal(): %v", err)
	}
	items := service.DelegationTaskSnapshots()
	if len(items) != 1 || items[0].ExecutorID != "codex-cli" || len(items[0].Attempts) != 2 {
		t.Fatalf("runtime snapshots = %#v", items)
	}
	first := items[0].Attempts[0]
	if first.ExecutorID != "claude-code" || first.Status != string(delegation.ExecutorAttemptFailed) || first.DiagnosticCode != "rate_limited" || !first.RetrySafe {
		t.Fatalf("first attempt = %#v", first)
	}
}
