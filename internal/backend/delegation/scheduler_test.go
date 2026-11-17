package delegation

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func waitSchedulerLifecycle(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitSchedulerExecutionStart(t *testing.T, started <-chan string, want string) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("executor started %q, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("executor %q did not start", want)
	}
}

func waitSchedulerLogicalTerminal(t *testing.T, scheduler *Scheduler, id string, want TaskStatus) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := scheduler.WaitForTerminal(ctx, []string{id}); err != nil {
		t.Fatalf("WaitForTerminal(%q): %v", id, err)
	}
	snapshot, ok := scheduler.Snapshot(id)
	if !ok || snapshot.Status != want {
		t.Fatalf("Snapshot(%q) = %+v, %t; want %s", id, snapshot, ok, want)
	}
}

func TestSchedulerCanceledExecutorRetainsSlotUntilActualExit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		timeout   bool
		cancelIf  bool
		panicExit bool
	}{
		{name: "cancel"},
		{name: "cancel_if_active", cancelIf: true},
		{name: "timeout", timeout: true},
		{name: "panic_after_cancel", panicExit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := make(chan string, 4)
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseExecutor := func() { releaseOnce.Do(func() { close(release) }) }
			scheduler := NewScheduler(Config{MaxConcurrency: 1}, func(_ context.Context, request TaskRequest) TaskResult {
				started <- request.ID
				if request.ID == "first" {
					// Deliberately ignore context cancellation.
					<-release
					if tc.panicExit {
						panic("late executor panic")
					}
				}
				return TaskResult{Output: request.ID}
			})
			t.Cleanup(func() {
				scheduler.Close()
				releaseExecutor()
				waitSchedulerLifecycle(t, "executor cleanup", func() bool {
					scheduler.mu.RLock()
					defer scheduler.mu.RUnlock()
					return len(scheduler.activeExecutions) == 0 && len(scheduler.slots) == 0
				})
			})
			request := TaskRequest{ID: "first"}
			if tc.timeout {
				request.Timeout = 100 * time.Millisecond
			}
			if _, err := scheduler.Submit(request); err != nil {
				t.Fatal(err)
			}
			waitSchedulerExecutionStart(t, started, "first")
			wantStatus := TaskCanceled
			if tc.timeout {
				wantStatus = TaskTimedOut
			} else {
				canceled := make(chan error, 1)
				go func() {
					if tc.cancelIf {
						if !scheduler.CancelIfActive("first") {
							canceled <- fmt.Errorf("CancelIfActive returned false")
							return
						}
						canceled <- nil
						return
					}
					canceled <- scheduler.Cancel("first")
				}()
				select {
				case err := <-canceled:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("cancellation waited for the uncooperative executor")
				}
			}
			waitSchedulerLogicalTerminal(t, scheduler, "first", wantStatus)
			waitSchedulerLifecycle(t, "logical runner exit", func() bool {
				scheduler.mu.RLock()
				defer scheduler.mu.RUnlock()
				return scheduler.tasks["first"].runnerDone
			})
			scheduler.mu.RLock()
			_, active := scheduler.activeExecutions["first"]
			scheduler.mu.RUnlock()
			if !active || len(scheduler.slots) != 1 {
				t.Fatalf("canceled executor lost ownership: active=%t slots=%d", active, len(scheduler.slots))
			}

			// Repeated queued cancellations must neither run nor consume slots.
			for index := range 8 {
				id := fmt.Sprintf("queued-canceled-%d", index)
				if _, err := scheduler.Submit(TaskRequest{ID: id}); err != nil {
					t.Fatal(err)
				}
				if err := scheduler.Cancel(id); err != nil {
					t.Fatal(err)
				}
				waitSchedulerLifecycle(t, "queued cancellation cleanup", func() bool {
					scheduler.mu.RLock()
					defer scheduler.mu.RUnlock()
					_, exists := scheduler.activeExecutions[id]
					return scheduler.tasks[id].runnerDone && !exists
				})
			}
			if _, err := scheduler.Submit(TaskRequest{ID: "queue-timeout", QueueTimeout: 10 * time.Millisecond}); err != nil {
				t.Fatal(err)
			}
			waitSchedulerLogicalTerminal(t, scheduler, "queue-timeout", TaskTimedOut)
			waitSchedulerLifecycle(t, "queue timeout cleanup", func() bool {
				scheduler.mu.RLock()
				defer scheduler.mu.RUnlock()
				_, exists := scheduler.activeExecutions["queue-timeout"]
				return scheduler.tasks["queue-timeout"].runnerDone && !exists
			})
			if _, err := scheduler.Submit(TaskRequest{ID: "next"}); err != nil {
				t.Fatal(err)
			}
			select {
			case id := <-started:
				t.Fatalf("executor %q started before first actually exited", id)
			case <-time.After(75 * time.Millisecond):
			}
			snapshot, _ := scheduler.Snapshot("next")
			if snapshot.Status != TaskQueued {
				t.Fatalf("next status = %s, want queued", snapshot.Status)
			}
			releaseExecutor()
			waitSchedulerExecutionStart(t, started, "next")
			waitSchedulerLogicalTerminal(t, scheduler, "next", TaskCompleted)
			waitSchedulerLogicalTerminal(t, scheduler, "first", wantStatus)
		})
	}
}

func TestSchedulerExecutorPanicReleasesOwnershipAndFailsTask(t *testing.T) {
	scheduler := NewScheduler(Config{MaxConcurrency: 1}, func(_ context.Context, request TaskRequest) TaskResult {
		if request.ID == "panic" {
			panic("executor exploded")
		}
		return TaskResult{Output: "ok"}
	})
	defer scheduler.Close()
	if _, err := scheduler.Submit(TaskRequest{ID: "panic"}); err != nil {
		t.Fatal(err)
	}
	waitSchedulerLogicalTerminal(t, scheduler, "panic", TaskFailed)
	snapshot, _ := scheduler.Snapshot("panic")
	if !strings.Contains(snapshot.Error, "executor exploded") {
		t.Fatalf("panic error = %q", snapshot.Error)
	}
	waitSchedulerLifecycle(t, "panic execution cleanup", func() bool {
		scheduler.mu.RLock()
		defer scheduler.mu.RUnlock()
		return len(scheduler.activeExecutions) == 0 && len(scheduler.slots) == 0
	})
	if _, err := scheduler.Submit(TaskRequest{ID: "after-panic"}); err != nil {
		t.Fatal(err)
	}
	waitSchedulerLogicalTerminal(t, scheduler, "after-panic", TaskCompleted)
}

func TestSchedulerCanceledBeforeSlotHandoffDoesNotLeak(t *testing.T) {
	var calls int
	scheduler := NewScheduler(Config{MaxConcurrency: 1}, func(context.Context, TaskRequest) TaskResult {
		calls++
		return TaskResult{}
	})
	defer scheduler.Close()
	// Both select branches are ready: cancellation may win before acquisition,
	// or acquisition may win and must return the runner-owned slot.
	for index := range 100 {
		id := fmt.Sprintf("already-canceled-%d", index)
		ctx, cancel := context.WithCancel(scheduler.ctx)
		cancel()
		state := &taskState{
			request: TaskRequest{ID: id}, snapshot: TaskSnapshot{ID: id, Status: TaskCanceled},
			ctx: ctx, cancel: cancel,
		}
		scheduler.mu.Lock()
		scheduler.tasks[id] = state
		scheduler.activeExecutions[id] = struct{}{}
		scheduler.mu.Unlock()
		scheduler.run(state)
		if len(scheduler.slots) != 0 {
			t.Fatalf("iteration %d leaked a slot", index)
		}
		scheduler.mu.RLock()
		remaining := len(scheduler.activeExecutions)
		scheduler.mu.RUnlock()
		if remaining != 0 || !state.runnerDone {
			t.Fatalf("iteration %d leaked runner ownership", index)
		}
	}
	if calls != 0 {
		t.Fatalf("canceled executors ran %d times", calls)
	}
}

func TestSchedulerPrunedCanceledExecutorKeepsIDReserved(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseExecutor := func() { releaseOnce.Do(func() { close(release) }) }
	scheduler := NewScheduler(Config{MaxConcurrency: 1, RetentionLimit: 1, RetentionAge: time.Nanosecond}, func(_ context.Context, request TaskRequest) TaskResult {
		started <- request.ID
		<-release
		return TaskResult{}
	})
	t.Cleanup(func() {
		scheduler.Close()
		releaseExecutor()
		waitSchedulerLifecycle(t, "pruned executor cleanup", func() bool {
			scheduler.mu.RLock()
			defer scheduler.mu.RUnlock()
			return len(scheduler.activeExecutions) == 0 && len(scheduler.slots) == 0
		})
	})
	if _, err := scheduler.Submit(TaskRequest{ID: "reserved"}); err != nil {
		t.Fatal(err)
	}
	waitSchedulerExecutionStart(t, started, "reserved")
	if err := scheduler.Cancel("reserved"); err != nil {
		t.Fatal(err)
	}
	waitSchedulerLifecycle(t, "canceled runner cleanup", func() bool {
		scheduler.mu.RLock()
		defer scheduler.mu.RUnlock()
		return scheduler.tasks["reserved"].runnerDone
	})
	if _, err := scheduler.Submit(TaskRequest{ID: "queued"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := scheduler.Snapshot("reserved"); ok {
		t.Fatal("retention did not prune the logical terminal task")
	}
	if _, err := scheduler.Submit(TaskRequest{ID: "reserved"}); err == nil || !strings.Contains(err.Error(), "still executing") {
		t.Fatalf("active executor ID was reusable after prune: %v", err)
	}
	if err := scheduler.Cancel("queued"); err != nil {
		t.Fatal(err)
	}
	waitSchedulerLifecycle(t, "queued runner cleanup", func() bool {
		scheduler.mu.RLock()
		defer scheduler.mu.RUnlock()
		return scheduler.tasks["queued"].runnerDone
	})
	releaseExecutor()
	waitSchedulerLifecycle(t, "reserved ID release", func() bool {
		scheduler.mu.RLock()
		defer scheduler.mu.RUnlock()
		return len(scheduler.activeExecutions) == 0 && len(scheduler.slots) == 0
	})
	if _, err := scheduler.Submit(TaskRequest{ID: "reserved"}); err != nil {
		t.Fatalf("ID remained reserved after actual exit: %v", err)
	}
	waitSchedulerExecutionStart(t, started, "reserved")
	waitSchedulerLogicalTerminal(t, scheduler, "reserved", TaskCompleted)
}

func TestSchedulerCloseDoesNotWaitForExecutorExit(t *testing.T) {
	started := make(chan string, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseExecutor := func() { releaseOnce.Do(func() { close(release) }) }
	scheduler := NewScheduler(Config{MaxConcurrency: 1}, func(_ context.Context, request TaskRequest) TaskResult {
		started <- request.ID
		<-release
		return TaskResult{}
	})
	t.Cleanup(func() {
		scheduler.Close()
		releaseExecutor()
		waitSchedulerLifecycle(t, "closed executor cleanup", func() bool {
			scheduler.mu.RLock()
			defer scheduler.mu.RUnlock()
			return len(scheduler.activeExecutions) == 0 && len(scheduler.slots) == 0
		})
	})
	if _, err := scheduler.Submit(TaskRequest{ID: "running"}); err != nil {
		t.Fatal(err)
	}
	waitSchedulerExecutionStart(t, started, "running")
	if _, err := scheduler.Submit(TaskRequest{ID: "queued"}); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() {
		scheduler.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close waited for an uncooperative executor")
	}
	waitSchedulerLogicalTerminal(t, scheduler, "running", TaskCanceled)
	waitSchedulerLogicalTerminal(t, scheduler, "queued", TaskCanceled)
	if _, err := scheduler.Submit(TaskRequest{ID: "after-close"}); err == nil {
		t.Fatal("closed scheduler accepted a new task")
	}
	if len(scheduler.slots) != 1 {
		t.Fatalf("Close released a live executor slot: %d", len(scheduler.slots))
	}
}
