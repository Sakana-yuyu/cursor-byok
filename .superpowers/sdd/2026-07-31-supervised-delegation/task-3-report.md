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

## Revision after review

- Closed the supervised startup race by keeping `multitaskDelegationCoordinator.Start` in its `startMu` critical section until the supervised coordinator has registered the aggregate and launched bounded background startup, while `SupervisorCoordinator.Start` and `dispatchInitialWorkers` now re-check `parentExecStillCurrent` before registration and before every scheduler submission.
- Tightened review handling so `reviewTask` re-validates aggregate/task/provider-pass identity immediately before `provider.Review`, and stale/canceled/malformed review callbacks can no longer trigger another provider pass; non-optional review failures now route to `circuit_open`/failed task outcomes.
- Made supervisor decisions strict end to end: `decodeSupervisorDecision` rejects unknown kinds and kinds outside the dynamically computed `AllowedActions`, and the review prompt now reflects that same bounded action set.
- Added persisted delegation supervision config mapping in `internal/backend/server/config/{delegation.go,types.go,manager.go}` with JSON/YAML fields, safe normalization, and runtime defaults of disabled, main-model fallback, corrections `2`, retries `1`, rounds `8`, and concurrency `4`, without persisting API keys into delegation config.
- Preserved `modelID`, `groupID`, and `executionMode` as separate follow-up routing fields so reassignment sets `request.ExecutionMode` directly instead of overloading `ModelGroupID`.
- Represented circuit-open, correction-limit, retry-limit, round-limit, and strict review failures in task results via `circuit_open`/failed supervision state plus retained issue code/reason, while keeping the aggregate payload sanitized.

### Revision verification

Re-ran successfully on July 31, 2026:

- `gofmt -w internal/backend/delegation/supervision.go internal/backend/server/config/delegation.go internal/backend/server/config/types.go internal/backend/server/config/manager.go internal/backend/forwarder/supervisor_provider.go internal/backend/forwarder/supervisor_coordinator.go internal/backend/forwarder/delegation_multitask.go`
- `go build ./...`
- `go vet ./...`
- `git diff --check`

## 第二轮审查修复（中文）

- 修复了 `reviewTask` 在 stale/canceled 路径直接 `return` 导致 `task.reviewPending` 永远不清零的问题。
- 在 `SupervisorCoordinator` 内新增纯内部事件 `review_abandoned`，它不会触发新的 provider pass，也不会改动主 stream 或 provider 状态；事件处理只负责：
  - 清除对应 task 的 `reviewPending`
  - 用当前 worker 的终态 snapshot/result 将该 task 安全收口
- 该收口路径覆盖了 `provider.Review` 调用前后的两处 stale/cancel 提前退出分支，因此陈旧 aggregate 在父 exec/provider pass 失效后也能自然满足 `allTasksCompleted`，随后走 `finish()/remove` 的内部清理路径。

### 第二轮验证

已重新执行并通过：

- `gofmt -w internal/backend/delegation/supervision.go internal/backend/server/config/delegation.go internal/backend/server/config/types.go internal/backend/server/config/manager.go internal/backend/forwarder/supervisor_provider.go internal/backend/forwarder/supervisor_coordinator.go internal/backend/forwarder/delegation_multitask.go`
- `go build ./...`
- `go vet ./...`
- `git diff --check`

## 最终审查修复（中文）

- 修复了 `worker_terminal` / `review_result` 事件已入队、但在 handler 执行时 `parentExecStillCurrent()` 已失效的 stale-event race。
- 现在这两个 handler 在 identity 仍匹配但父 exec/provider pass 已陈旧时，不再直接 `return`；而是在 aggregate 内部：
  - 将对应 task 标记为已完成/abandoned
  - 清除 `reviewPending`
  - 写入经过裁剪的 stale/canceled 原因
  - 将 task 状态收口为 `TaskCanceled` + `SupervisionStatusCanceled`
  - 保留 issue code / reason，确保 `allTasksCompleted()` 可以成立，aggregate 能继续走内部 `finish()/remove`
- 这条 stale-event 收口路径不会发布新的 provider pass，也不会改动主 stream / provider history；`reviewTask` 现有的 stale handling 继续保留。

### 最终审查验证

已重新执行并通过：

- `gofmt -w internal/backend/delegation/supervision.go internal/backend/server/config/delegation.go internal/backend/server/config/types.go internal/backend/server/config/manager.go internal/backend/forwarder/supervisor_provider.go internal/backend/forwarder/supervisor_coordinator.go internal/backend/forwarder/delegation_multitask.go`
- `go build ./...`
- `go vet ./...`
- `git diff --check`
