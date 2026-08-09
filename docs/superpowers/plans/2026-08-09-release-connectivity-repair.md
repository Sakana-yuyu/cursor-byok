# Release Connectivity Repair Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore every confirmed data path that is implemented but unreachable or incorrectly terminated on the current post-0.0.84 main line.

**Architecture:** Keep the existing frontend-to-Wails-to-host-to-forwarder boundaries. Repair the two backend state predicates at their source, give long-running frontend operations explicit budgets, and restore a lazy-loaded Goal operations page that consumes the already exported bindings.

**Tech Stack:** Go, Protocol Buffers, Vue 3, Wails bindings, Node test runner, Playwright, static i18n scanner.

## Global Constraints

- Treat `e68af9fe4ffcb2ccda65d5153e74ae70d0e409fb` as the current main-line 0.0.84 release commit; do not merge the unrelated `v0.0.85` history.
- Preserve append-only conversation history and existing prompt projection order.
- Write frontend source messages as Chinese literals and regenerate all locale catalogs.
- Preserve the existing legacy Goal config migration in `internal/backend/server/config/store.go` and its test.
- Do not change the application version in this repair.

---

### Task 1: Preserve Realtime Non-File Rules

**Files:**
- Modify: `internal/backend/forwarder/request_context.go`
- Test: `internal/backend/forwarder/request_context_test.go`

**Interfaces:**
- Consumes: `normalizeRealtimeRequestContextForStorage(*agentv1.RequestContext)`.
- Produces: a non-nil normalized context when `NonFileRules` is the only realtime field.

- [ ] Add a regression test using a context whose only content is one guarded `NonFileRules` entry.
- [ ] Run the focused test and confirm it fails because normalization returns nil.
- [ ] Include `GetNonFileRules()` in `hasRealtimeRequestContextContent`.
- [ ] Run the focused forwarder tests and confirm they pass.

### Task 2: Keep Stream Terminal States Idempotent

**Files:**
- Modify: `internal/backend/forwarder/broker.go`
- Test: `internal/backend/forwarder/failure_test.go`

**Interfaces:**
- Consumes: `(*StreamBroker).FailWithDetails(string, TerminalFailure) error`.
- Produces: no status mutation and no second terminal event after completed, canceled, or failed.

- [ ] Add completed-then-failed and canceled-then-failed regression tests.
- [ ] Run the focused tests and confirm terminal status/event assertions fail.
- [ ] Add the terminal-state guard while holding `stream.mu`.
- [ ] Run the focused forwarder tests and race-sensitive repetitions.

### Task 3: Align Frontend Operation Budgets

**Files:**
- Modify: `frontend/src/services/clientApi.js`
- Test: `frontend/src/services/clientApi.contract.test.js`

**Interfaces:**
- Consumes: `withApiLogging(name, payload, runner, options)`.
- Produces: model tests and automatic context matching with explicit budgets greater than the backend 45-second benchmark budget.

- [ ] Add a source-contract test asserting both operations pass explicit timeout options.
- [ ] Run the unit test and confirm it fails on the current wrappers.
- [ ] Add named timeout constants and pass them to the two wrappers.
- [ ] Run all frontend unit tests.

### Task 4: Restore the Goal Operations Surface

**Files:**
- Create: `frontend/src/views/Goal.vue`
- Modify: `frontend/src/services/clientApi.js`
- Modify: `frontend/src/services/browserBindings.js`
- Modify: `frontend/src/router/index.js`
- Modify: `frontend/src/views/Home.vue`
- Test: `frontend/e2e/goal.spec.mjs`

**Interfaces:**
- Consumes: generated `GetGoals`, `StartGoal`, and `StopGoal` Wails bindings.
- Produces: `getGoals()`, `startGoal(goalText, modelID)`, `stopGoal(conversationID)`, and a lazy `/goal` route.

- [ ] Add a Playwright test that opens `/goal`, starts a Goal, observes it in the list, and stops it.
- [ ] Run the focused E2E test and confirm the route/page is missing.
- [ ] Register the bindings and wrappers, preserving desktop error normalization.
- [ ] Extend browser-preview mocks with in-memory Goal lifecycle behavior.
- [ ] Add the compact operations page and Home action-menu entry; keep configuration in Advanced Settings.
- [ ] Run the focused Playwright test and frontend lint.

### Task 5: Verify the Integrated Repair

**Files:**
- Generated: `frontend/src/i18n/generated/catalog.json`
- Generated: `frontend/src/i18n/locales/*.json`

**Interfaces:**
- Consumes: all repaired paths.
- Produces: a buildable, tested tree with stable generated catalogs.

- [ ] Run `gofmt` on changed Go files.
- [ ] Run `go test ./... -count=1`, `go vet ./...`, and `go build ./...`.
- [ ] Run `npm run lint`, `npm run test:unit`, and `npm run build` twice from `frontend`.
- [ ] Verify locale key parity, non-empty translations, and placeholder parity.
- [ ] Run `npx playwright test --reporter=line`.
- [ ] Run `git diff --check`, conflict-marker scan, and inspect final status.
