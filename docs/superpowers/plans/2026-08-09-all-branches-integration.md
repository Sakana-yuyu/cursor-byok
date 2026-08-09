# All Branches Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Merge every local feature branch into one verified `main` history and remove the obsolete local branches without losing worktree data.

**Architecture:** Use a clean integration worktree based on the latest `main`. Preserve the current conversation scheduler as the lifecycle authority and layer the error-resilience contract around its existing provider, forwarder, logging, and frontend boundaries.

**Tech Stack:** Go 1.26, Vue 3, Vite, Node test runner, ESLint, Git worktrees

## Global Constraints

- Do not modify installed Cursor application bundles.
- Keep model-visible conversation history append-only and conversation-scoped.
- Use Chinese literals as frontend source messages and regenerate catalogs with the static i18n scanner.
- Do not delete a local branch until its tip is reachable from final `main`.
- Do not remove a dirty worktree until every untracked file is proven identical to a final `main` file or separately preserved.

---

### Task 1: Preserve Conversation-Isolation History

**Files:**
- Merge: `fix/conversation-concurrency-isolation-impl`
- Verify: `internal/backend/forwarder/run_queue.go`
- Verify: `internal/backend/forwarder/run_queue_test.go`
- Verify: `internal/backend/forwarder/conversation_isolation_test.go`

**Interfaces:**
- Consumes: current `main` conversation scheduler implementation
- Produces: merge history in which the implementation branch tip is an ancestor

- [x] **Step 1: Run scheduler tests on current main**

Run: `go test ./internal/backend/forwarder -run 'RunQueue|Conversation|Cache' -count=1`
Expected: PASS, establishing the current scheduler baseline.

- [x] **Step 2: Merge the implementation branch**

Run: `git merge --no-ff fix/conversation-concurrency-isolation-impl`
Expected: clean merge or conflicts limited to equivalent scheduler changes and documentation.

- [x] **Step 3: Resolve equivalent scheduler conflicts**

Keep the current `main` owner/FIFO implementation when both sides implement the same behavior, while retaining non-duplicate tests and design documentation from the source branch.

- [x] **Step 4: Verify scheduler behavior**

Run: `go test ./internal/backend/forwarder -run 'RunQueue|Conversation|Cache' -count=1`
Expected: PASS.

### Task 2: Merge Error Resilience

**Files:**
- Merge: `feat/error-resilience`
- Modify on conflict: `internal/backend/agent/model/router.go`
- Modify on conflict: `internal/backend/forwarder/actor.go`
- Modify on conflict: `internal/backend/forwarder/service.go`
- Modify on conflict: `internal/backend/forwarder/compaction.go`
- Modify on conflict: `internal/backend/forwarder/broker.go`
- Modify on conflict: `frontend/src/main.js`
- Regenerate: `frontend/src/i18n/generated/catalog.json`
- Regenerate: `frontend/src/i18n/locales/*.json`

**Interfaces:**
- Consumes: scheduler lifecycle and existing provider/forwarder error paths
- Produces: `apperror.AppError`, frontend error contract, runtime health state, and retry/diagnostic behavior

- [x] **Step 1: Merge the feature branch**

Run: `git merge --no-ff feat/error-resilience`
Expected: conflicts in files independently changed by the scheduler and the error feature.

- [x] **Step 2: Compose forwarder behavior**

For each conflict, preserve scheduler terminalization and exact owner release, then retain error classification, trace recording, and retry metadata around that lifecycle.

- [x] **Step 3: Regenerate localization catalogs**

Run: `npm run i18n:scan` from `frontend/`
Expected: generated catalog references match the integrated source tree.

- [x] **Step 4: Run focused error tests**

Run: `go test ./internal/apperror ./internal/backend/agent/model ./internal/backend/forwarder ./internal/safego -count=1`
Expected: PASS.

Run: `node --test src/services/runtimeHealthModel.test.js src/utils/errorContract.test.js` from `frontend/`
Expected: PASS.

### Task 3: Full Verification

**Files:**
- Verify: entire repository

**Interfaces:**
- Consumes: integrated source tree
- Produces: evidence that the combined branch is buildable and test-clean

- [x] **Step 1: Check merge integrity**

Run: `git diff --check && git grep -n -e '^<<<<<<<' -e '^=======$' -e '^>>>>>>>'`
Expected: no whitespace errors and no conflict markers.

- [x] **Step 2: Run complete Go tests**

Run: `go test ./... -count=1`
Expected: PASS.

- [x] **Step 3: Run frontend validation**

Run: `npm run lint` from `frontend/`
Expected: PASS.

Run: `npm run build` from `frontend/`
Expected: PASS and stable generated catalogs.

- [x] **Step 4: Verify locale completeness**

Compare every locale key set and placeholder set against `frontend/src/i18n/generated/catalog.json`; require non-empty translations.

### Task 4: Promote and Clean Branches

**Files:**
- Update branch: `main`
- Remove worktrees associated with merged local branches
- Delete local branches other than `main`

**Interfaces:**
- Consumes: verified integration branch
- Produces: one local `main` branch containing every feature branch tip

- [x] **Step 1: Verify branch reachability**

Run `git merge-base --is-ancestor <branch> codex/integrate-all-branches` for every local source branch.
Expected: exit code 0 for all branches.

- [x] **Step 2: Fast-forward main**

Run: `git merge --ff-only codex/integrate-all-branches` from the primary worktree.
Expected: `main` advances without rewriting or stashing user changes.

- [x] **Step 3: Verify dirty worktree payloads**

Hash each untracked file in old worktrees and its counterpart on final `main`; preserve any mismatch before removal.
Expected: every file is identical or explicitly preserved.

- [x] **Step 4: Remove obsolete worktrees and branches**

Remove the source-branch worktrees, then delete every local branch except `main`.
Expected: `git branch` lists only `main`, and `git status --short --branch` is clean.
