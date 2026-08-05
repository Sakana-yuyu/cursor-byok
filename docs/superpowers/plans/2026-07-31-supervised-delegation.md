# Supervised Delegation Implementation Plan

> **状态：已实现（2026-08-05 标记）**。本计划全部 Task 已完成并合入，
> 验收记录见 `docs/tasks/2026-07-31-cursor-byok-capability-completion.md` 的
> 「监督式委派增量验收」。后续会话无需按本计划重新开工，仅做维护性修改时
> 参考对应文件位置。

> For agentic workers: REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Add an opt-in supervisor/consultant layer to Multitask so a stronger model plans, reviews, corrects, retries, reassigns, or stops worker agents without blocking the main agent.

**Architecture:** Extend the existing delegation scheduler with focused domain types for contracts, checkpoints, supervisor decisions, and loop detection. Add a SupervisorCoordinator around the current Multitask coordinator; it owns supervision state and asynchronous provider calls while the existing Cursor/local adapters remain worker executors. Expose only sanitized supervision snapshots to the existing runtime API and settings UI.

**Tech Stack:** Go backend, existing provider/model adapters, existing Cursor/local delegation adapters, existing config store, Vue 3 frontend, current Wails/browser bindings, existing Go build/static checks and manual protocol replay.

## Global Constraints

- First release is opt-in and applies only to AGENT_MODE_MULTITASK; ordinary Agent, Ask, Plan, Build, and Debug paths remain unchanged.
- Reuse configured model adapters and credentials; never duplicate API keys in delegation or supervision configuration.
- Worker execution remains non-blocking and sibling failures remain isolated.
- Every asynchronous result is validated by aggregate ID, task ID, parent exec ID, provider pass, and supervision round.
- Do not modify the installed Cursor client bundle.
- Do not add test files; use existing tests where present, builds, static checks, protocol replay, logs, and manual UI/e2e acceptance.
- Do not create a release, tag, or push while implementing this plan.
- Keep unrelated existing worktree changes untouched and commit each completed task separately.

---

## File Map

### Backend domain

- Create internal/backend/delegation/supervision.go for public supervision states, task contract, checkpoint, decision, counters, and sanitized domain helpers.
- Create internal/backend/delegation/loop_detector.go for deterministic repeated-action, no-progress, scope-drift, and missing-evidence detection.
- Modify internal/backend/delegation/scheduler.go to add checkpoint/event publication hooks without changing existing submit/cancel semantics.

### Backend orchestration

- Create internal/backend/forwarder/supervisor_coordinator.go for asynchronous supervisor lifecycle, worker checkpoint intake, decision application, retry/reassign/escalate/circuit-breaker logic.
- Create internal/backend/forwarder/supervisor_provider.go for provider request/response adaptation with strict JSON decision decoding and cancellation.
- Modify internal/backend/forwarder/delegation_multitask.go to choose supervised vs legacy execution, attach contracts, pass supervision identity, and keep legacy fallback intact.
- Modify internal/backend/forwarder/delegation_runtime.go to expose sanitized supervision fields and counters.
- Modify internal/backend/forwarder/service.go and internal/backend/forwarder/actor.go to close/cancel supervisor coordinators with the parent service and stream.

### Configuration and APIs

- Modify internal/backend/server/config/delegation.go to add normalized supervisor configuration and safe defaults.
- Modify internal/backend/server/config/manager.go to map persisted config to runtime supervision config.
- Modify the existing runtime binding/API files discovered by searching for DelegationTaskSnapshots and CancelDelegationTask to expose supervision configuration and snapshots through the same binding boundary.

### Frontend

- Modify frontend/src/components/settings/categories/DelegationSettings.vue to add opt-in supervision controls, supervisor/reviewer model selection, limits, and explicit save/error states.
- Modify frontend/src/components/DelegationTaskStrip.vue and frontend/src/components/DelegationRuntimePanel.vue to show phase, supervisor model, correction/retry counts, loop reason, and cancel action without exposing prompts or tool arguments.
- Modify frontend/src/services/runtimeControlApi.js and generated/browser bindings only when required by the existing API pattern.
- Update the existing locale source/catalog files through the repository i18n workflow for every new visible string.

### Documentation and acceptance

- Modify docs/tasks/2026-07-31-cursor-byok-capability-completion.md only with staged acceptance records after each implementation commit.
- Do not add test files.

---

## Task 1: Add Supervision Domain Contracts

**Files:**
- Create internal/backend/delegation/supervision.go
- Modify internal/backend/delegation/scheduler.go

**Interfaces:**
- Consumes existing delegation TaskRequest, TaskResult, TaskSnapshot, and scheduler event sequence.
- Produces SupervisionStatus, SupervisionDecisionKind, SupervisionTaskContract, WorkerCheckpoint, SupervisionDecision, SupervisionCounters, and clone/normalize helpers.

- [x] Step 1: Define stable enums and limits.

Use string types with explicit constants for planned, dispatched, running, checkpointing, reviewing, correcting, retrying, reassigning, escalated, completed, failed, canceled, and circuit_open. Define decision constants accept, continue, correct, retry, reassign, escalate, and circuit_open. Add defaults for corrections 2, retries 1, rounds 8, and checkpoint interval 1500 milliseconds.

- [x] Step 2: Define immutable task contract and checkpoint types.

SupervisionTaskContract must carry aggregate/task identity, round, goal, scope, role, allowed tools, expected output, done criteria, max steps, timeout, failure policy, workspace hint, and selected Skills/MCP identifiers. WorkerCheckpoint must carry task identity, round, phase, step, recent tool names, changed-file summaries, progress summary, blocker, effective-progress timestamp, and event sequence. Use clone functions for slices and maps so snapshots cannot mutate live state.

- [x] Step 3: Add supervisor fields to scheduler snapshots without leaking data.

Add contract/checkpoint/counter fields to internal task state and sanitized TaskSnapshot fields. Keep TaskRequest prompt, arguments, credentials, and absolute workspace paths out of the UI-facing snapshot. Existing callers that do not set a contract must serialize as ordinary delegation tasks.

- [x] Step 4: Add non-blocking checkpoint publication.

Expose Scheduler.PublishCheckpoint(taskID string, checkpoint WorkerCheckpoint) bool. It must validate task existence and terminal state under the scheduler mutex, update only the latest checkpoint, assign a monotonic sequence, and publish without waiting on consumers. Preserve existing terminal event retention and cancellation behavior.

- [x] Step 5: Run formatting and compile verification.

    gofmt -w internal/backend/delegation/supervision.go internal/backend/delegation/scheduler.go
    go build ./...
    git diff --check

Expected: build succeeds and no whitespace errors are reported.

- [x] Step 6: Commit the domain layer.

    git add internal/backend/delegation/supervision.go internal/backend/delegation/scheduler.go
    git commit -m "feat: add supervised delegation contracts"

## Task 2: Add Deterministic Progress and Loop Detection

**Files:**
- Create internal/backend/delegation/loop_detector.go
- Modify internal/backend/delegation/supervision.go

**Interfaces:**
- Consumes SupervisionTaskContract, current WorkerCheckpoint, previous checkpoint, worker tool signature, and worker result metadata.
- Produces SupervisionIssue, LoopDetector, and DetectCheckpointIssue with stable issue codes.

- [x] Step 1: Define issue codes and detector input.

Use tool_failure, no_progress, repeated_action, scope_drift, missing_evidence, timeout, and model_failure. Detector input must include the current checkpoint, previous checkpoint, recent tool signatures, changed files, error text, and whether the worker claimed completion.

- [x] Step 2: Implement repeated-action detection.

Normalize tool name and a bounded hash of its argument shape into a signature. Report repeated_action only after the configured consecutive threshold is reached; do not store raw arguments in the issue or snapshot.

- [x] Step 3: Implement no-progress and error recovery detection.

Compare effective-progress timestamps, changed-file summaries, step numbers, and output evidence. Report no_progress when the worker exceeds the checkpoint window without a meaningful change, and tool_failure when the same normalized error repeats without a changed strategy.

- [x] Step 4: Implement scope and completion-evidence checks.

Compare changed-file paths against the contract scope using the existing path normalization helpers. Report scope_drift for out-of-scope changes and missing_evidence when the worker claims completion without output or done-criteria evidence.

- [x] Step 5: Verify detector behavior with existing command-level checks.

    gofmt -w internal/backend/delegation/loop_detector.go
    go build ./...
    go vet ./...
    git diff --check

Manually inspect detector logs using synthetic checkpoint payloads through the existing debug/replay command path; do not add test files.

- [x] Step 6: Commit the detector.

    git add internal/backend/delegation/loop_detector.go internal/backend/delegation/supervision.go
    git commit -m "feat: detect delegated worker drift and loops"

## Task 3: Implement the Non-Blocking Supervisor Coordinator

**Files:**
- Create internal/backend/forwarder/supervisor_coordinator.go
- Create internal/backend/forwarder/supervisor_provider.go
- Modify internal/backend/forwarder/delegation_multitask.go
- Modify internal/backend/forwarder/service.go
- Modify internal/backend/forwarder/actor.go

**Interfaces:**
- Consumes existing Multitask base request, delegation.Scheduler, worker snapshots/events, runtime configuration, provider/model adapter interfaces, and stream command queue.
- Produces SupervisorCoordinator.Start, SupervisorCoordinator.Cancel, SupervisorCoordinator.Close, sanitized supervisor snapshots, and aggregate results with supervision counters.

- [x] Step 1: Add runtime supervisor configuration mapping.

Extend the runtime config with SupervisionEnabled, SupervisorModelID, ReviewerModelID, WorkerGroupID, MaxCorrections, MaxRetries, MaxRounds, AllowReassign, AllowEscalate, and StrictUnavailable. Normalize invalid values to safe defaults and preserve legacy behavior when supervision is disabled.

- [x] Step 2: Build a coordinator state machine around the existing scheduler.

SupervisorCoordinator.Start must register an aggregate before submitting workers, create one contract per selected worker, and return immediately with an aggregate ID. It must use goroutines and channels for worker completion and supervisor review. No call on the main stream goroutine may wait for a worker or supervisor provider response.

- [x] Step 3: Add strict supervisor provider calls.

Construct a bounded review prompt from the contract, sanitized checkpoint, worker output summary, detected issues, and done criteria. Decode only a strict JSON object matching SupervisionDecision; reject unknown decisions, oversized corrections, missing reasons, and malformed output. Use a child context with timeout and cancel it whenever the aggregate or parent stream is canceled.

- [x] Step 4: Apply decisions with isolation and circuit breaking.

Implement accept, continue, correct, retry, reassign, escalate, and circuit_open. Corrections update only the target worker contract and create a new supervision round. Retries use a fresh child request identity. Reassign selects an enabled configured model group. Escalation uses the configured supervisor/reviewer model only once per task. Exceeding correction/retry/round limits produces circuit_open with an issue code.

- [x] Step 5: Validate every asynchronous callback.

Before applying a worker or supervisor event, compare aggregate ID, task ID, parent exec ID, provider pass, and supervision round. If the parent stream is terminal or the event is stale, update no provider state and do not enqueue a new model pass. Preserve existing legacy Start, awaitAggregate, cancellation, and sibling-isolation behavior when supervision is disabled or unavailable in optional mode.

- [x] Step 6: Close the coordinator with the service lifecycle.

Call coordinator cancellation from CancelStream and Close before closing worker schedulers. Ensure all supervisor child contexts are canceled and no callback can write to a closed event channel.

- [x] Step 7: Compile and inspect lifecycle logs.

    gofmt -w internal/backend/forwarder/supervisor_coordinator.go internal/backend/forwarder/supervisor_provider.go internal/backend/forwarder/delegation_multitask.go internal/backend/forwarder/service.go internal/backend/forwarder/actor.go
    go build ./...
    go vet ./...

Use existing debug logs to verify a canceled aggregate cannot create a second provider pass and a stale worker result is ignored.

- [x] Step 8: Commit the coordinator.

    git add internal/backend/forwarder/supervisor_coordinator.go internal/backend/forwarder/supervisor_provider.go internal/backend/forwarder/delegation_multitask.go internal/backend/forwarder/service.go internal/backend/forwarder/actor.go
    git commit -m "feat: add nonblocking delegation supervisor"

## Task 4: Persist Supervision State and Expose Runtime Snapshots

**Files:**
- Modify internal/backend/server/config/delegation.go
- Modify internal/backend/server/config/manager.go
- Modify existing host/runtime binding files for delegation configuration and snapshots
- Modify internal/backend/forwarder/delegation_runtime.go

**Interfaces:**
- Consumes normalized config.DelegationConfig, existing model adapter catalog, and coordinator snapshots.
- Produces persisted supervision settings, DelegationRuntimeConfig supervision fields, and sanitized JSON snapshots containing phase, supervisor/reviewer IDs, counters, last issue code, and current round.

- [x] Step 1: Add persisted configuration fields with backward-compatible defaults.

Add a nested supervision configuration or equivalent fields under the existing delegation config. Defaults must disable supervision, follow the main model, use worker concurrency 4, corrections 2, retries 1, and rounds 8. Normalize model IDs against configured adapters and disable strict mode when no supervisor model can be resolved.

- [x] Step 2: Map persisted configuration to runtime config.

Update Manager.DelegationRuntimeConfig and its runtime model conversion so the forwarder receives cloned values and cannot mutate the persisted config. Do not include credentials or raw adapter settings in runtime snapshots.

- [x] Step 3: Extend the sanitized runtime DTO.

Add JSON fields for supervision status, phase, supervisor model name, reviewer model name, supervision round, correction count, retry count, reassign/escalate counts, last issue code, and last progress time. Mark tasks cancelable only while either worker or supervisor work remains active.

- [x] Step 4: Add binding methods using the existing API style.

Expose the normalized delegation/supervision config read and save operations through the same host/browser binding pattern already used by GetDelegationTaskSnapshots and CancelDelegationTask. Return explicit errors for invalid config and preserve generation checks on the frontend.

- [x] Step 5: Verify persistence and DTO safety.

    gofmt -w internal/backend/server/config/delegation.go internal/backend/server/config/manager.go internal/backend/forwarder/delegation_runtime.go
    go build ./...
    go vet ./...
    git diff --check

Restart the backend or reload config and confirm supervision settings restore, while serialized snapshots contain no prompt, credentials, raw tool arguments, or absolute workspace path.

- [x] Step 6: Commit runtime persistence.

    git add internal/backend/server/config/delegation.go internal/backend/server/config/manager.go internal/backend/forwarder/delegation_runtime.go internal/backend/host.go
    git commit -m "feat: persist supervised delegation runtime state"

## Task 5: Add Settings and Multitask Supervision UI

**Files:**
- Modify frontend/src/components/settings/categories/DelegationSettings.vue
- Modify frontend/src/components/DelegationTaskStrip.vue
- Modify frontend/src/components/DelegationRuntimePanel.vue
- Modify frontend/src/services/runtimeControlApi.js
- Modify the existing Wails/browser binding source and locale files required by the repository i18n workflow

**Interfaces:**
- Consumes persisted delegation/supervision config API and sanitized runtime snapshots.
- Produces opt-in supervision controls, model selectors, threshold inputs, status/error feedback, and cancel/refresh actions.

- [x] Step 1: Add supervision settings section.

Use the existing settings layout and controls. Add an opt-in switch, supervisor model selector with “follow main model”, reviewer selector with “follow supervisor”, worker group selector, numeric inputs for correction/retry/round limits, and switches for reassign/escalate/strict unavailable. Disable dependent selectors when supervision is off and show the persisted save state.

- [x] Step 2: Wire real load/save/error/busy states.

Load settings once per current settings generation, prevent stale saves from overwriting newer edits, disable save while pending, display backend validation errors, and show a success message only after persistence completes. Use existing i18n utilities for every visible string.

- [x] Step 3: Display supervision runtime state.

In the task strip and runtime panel, show supervisor phase, worker role/model, current round, correction/retry counters, last issue category, and a compact progress summary. Keep long text wrapped and omit prompt/tool argument data. Preserve existing cancel behavior and show busy/error feedback for every action.

- [x] Step 4: Verify UI through the running app.

    npm --prefix frontend run build
    git diff --check

Open the settings route and Multitask runtime panel in the existing local app. Verify supervision can be enabled, saved, reloaded, disabled, and canceled; verify no browser console errors and no layout overflow at desktop and narrow widths.

- [x] Step 5: Commit the UI.

    git add frontend/src/components/settings/categories/DelegationSettings.vue frontend/src/components/DelegationTaskStrip.vue frontend/src/components/DelegationRuntimePanel.vue frontend/src/services/runtimeControlApi.js frontend/src/i18n
    git commit -m "feat: add supervised delegation controls"

## Task 6: Full Verification and Acceptance Record

**Files:**
- Modify docs/tasks/2026-07-31-cursor-byok-capability-completion.md
- Do not create test files.

**Interfaces:**
- Consumes all prior task commits, existing build and debug tooling, running local app, configured model adapters, and optional MCP/Skills runtime.
- Produces a verified acceptance record and final review commit.

- [x] Step 1: Run backend verification.

    go test -count=1 ./...
    go vet ./...
    go build ./...

Record failures with their actual command output; do not call the feature complete if any required command fails.

- [x] Step 2: Run frontend and repository checks.

    npm --prefix frontend run build
    git diff --check

Confirm generated protocol/i18n files have no unexpected diff and only task-owned files changed.

- [x] Step 3: Exercise disabled-mode compatibility.

With supervision disabled, run an existing Multitask request with multiple selected models. Confirm it uses the existing scheduler path, worker cancellation remains isolated, and no supervisor provider call is created.

- [x] Step 4: Exercise supervised success and partial success.

With supervision enabled, run two or more workers. Confirm the main stream returns pending immediately, workers run concurrently, accepted results are aggregated, and one failed worker does not cancel its siblings.

- [x] Step 5: Exercise correction, reassignment, escalation, and circuit breaking.

Use existing replay/debug hooks or controlled provider responses to produce repeated actions, no progress, scope drift, missing evidence, and tool failure. Confirm each issue causes the configured decision, counters increment once, and the task stops with an explanatory circuit_open result after limits are exceeded.

- [x] Step 6: Exercise cancellation and stale-event safety.

Cancel an active aggregate, reload the stream, and deliver a delayed worker result through the existing replay path. Confirm the result updates no provider pass, does not re-open a completed stream, and does not duplicate history.

- [x] Step 7: Review code boundaries and secrets.

Inspect changed files for duplicated provider routing, synchronous waits on the main stream, raw prompt/tool argument logging, credentials in snapshots, unbounded correction loops, and writes to installed Cursor files. Fix any finding before final commit.

- [x] Step 8: Record acceptance and commit.

Append command results and manual acceptance evidence to the existing task checklist, then commit only the checklist change:

    git add docs/tasks/2026-07-31-cursor-byok-capability-completion.md
    git commit -m "chore: verify supervised delegation completion"

