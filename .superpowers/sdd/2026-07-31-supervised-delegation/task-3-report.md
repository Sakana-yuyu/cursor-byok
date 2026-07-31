# Task 3 Report: Non-Blocking Delegation Supervisor

## Scope completed

- Added a runtime supervision shape to `delegation.RuntimeConfig` with normalized safe defaults.
- Added a strict supervisor-provider adapter in `internal/backend/forwarder/supervisor_provider.go` that:
  - reuses the existing `ProviderGateway`
  - builds bounded review prompts from sanitized contract/checkpoint/result data
  - requires a strict JSON object with `kind`, `reason`, and `summary`
  - rejects malformed output, unknown decisions, missing reasons, and oversized corrections
- Added a non-blocking `SupervisorCoordinator` in `internal/backend/forwarder/supervisor_coordinator.go` that:
  - registers supervised aggregates before worker submission
  - starts workers immediately and returns without blocking the stream actor
  - waits for worker completion in background goroutines
  - runs supervisor reviews in background goroutines
  - validates aggregate/task/parent-exec/provider-pass/round identity before applying worker or supervisor events
  - preserves sibling isolation and per-task retries/corrections/reassign/escalate handling
  - cancels all child contexts/tasks when the aggregate is canceled or the coordinator closes
- Updated `internal/backend/forwarder/delegation_multitask.go` to:
  - keep the legacy path unchanged when supervision is disabled
  - fall back to legacy behavior when supervision is optional but unavailable
  - enforce strict-unavailable errors only when configured
  - include supervision counters/status fields in aggregate task results
- Updated `internal/backend/server/config/manager.go` to normalize the runtime delegation config before handing it to the forwarder.

## Implementation notes

- No second worker runtime was introduced. Worker execution still goes through the existing local/Cursor delegation adapters and the shared delegation scheduler.
- Supervised worker retries/reassignments/escalations create fresh scheduler task IDs and child request identities by resubmitting through the existing scheduler.
- Stale async events are ignored when:
  - the aggregate ID does not match
  - the logical/current task ID does not match
  - the parent exec ID does not match
  - the parent provider pass no longer matches the stream
  - the supervision round no longer matches the current task attempt
- When the supervisor provider is unavailable in non-strict mode, the path preserves legacy behavior by accepting the current worker result instead of blocking or creating a second provider pass.

## Files changed

- `internal/backend/delegation/config.go`
- `internal/backend/server/config/manager.go`
- `internal/backend/forwarder/delegation_multitask.go`
- `internal/backend/forwarder/supervisor_provider.go`
- `internal/backend/forwarder/supervisor_coordinator.go`

## Verification

Ran successfully on July 31, 2026:

- `gofmt -w internal/backend/delegation/config.go internal/backend/server/config/manager.go internal/backend/forwarder/supervisor_provider.go internal/backend/forwarder/supervisor_coordinator.go internal/backend/forwarder/delegation_multitask.go`
- `go build ./...`
- `go vet ./...`
- `git diff --check`

## Constraints respected

- No frontend/config persistence/UI work was added.
- No installed Cursor files were touched.
- No test files were added.
- Unrelated existing worktree edits were left untouched.

## Concerns / follow-up

- I did not find reusable existing delegation debug-log evidence in the local history/log roots to replay a real canceled aggregate or stale worker event end-to-end, so the stale-event/canceled-parent guarantees here are verified by code-path inspection plus `go build`/`go vet`, not by a fresh runtime replay trace.
- Task 4 still needs persisted supervision config fields and runtime snapshot exposure before this path can be configured from the normal settings/binding surface.
