# Assistant Goal Removal And Sidebar Spacing Implementation Plan

> **状态：部分过时（2026-08-31）。** 当时目标是移除独立 Goal 面板但保留 cursor-byok 自研 `/goal` 执行链。当前实现已进一步移除自研 Goal 能力，`/goal` 交给 Cursor 官方原生能力处理。本文仅作历史参考。
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the standalone assistant Goal panel while retaining Cursor `/goal`, and loosen normal settings sidebar item spacing.

**Architecture:** Treat the Goal panel as a removable presentation/API branch and leave the active-stream Goal execution chain untouched. Change one sidebar spacing class and verify the visual distinction geometrically in Playwright.

**Tech Stack:** Vue 3, Vue Router, Tailwind CSS, Playwright, Go.

## Global Constraints

- Preserve the Advanced Settings Goal command and all Cursor `/goal` execution behavior.
- Do not change spacing inside `更多设置` or the collapsed sidebar.
- Do not revert unrelated dirty-worktree changes.

---

### Task 1: Add Browser Regressions

**Files:**
- Modify: `frontend/e2e/settings-navigation.spec.mjs`
- Modify: `frontend/e2e/goal.spec.mjs`

**Interfaces:**
- Consumes: browser-preview routes and accessible button labels.
- Produces: regressions for sidebar geometry and absence of the assistant Goal panel.

- [ ] Add a geometry assertion that the normal group sibling gap is larger than the `更多设置` sibling gap.
- [ ] Replace the Goal panel workflow test with assertions that Home has no `目标执行`, `/goal` has no Goal panel, and Advanced Settings retains `启用 Goal 命令`.
- [ ] Run both specs and confirm they fail for the missing production changes.

### Task 2: Apply Frontend Changes

**Files:**
- Modify: `frontend/src/components/settings/SettingsSidebar.vue`
- Modify: `frontend/src/views/Home.vue`
- Modify: `frontend/src/router/index.js`
- Delete: `frontend/src/views/Goal.vue`
- Modify: `frontend/src/services/clientApi.js`
- Modify: `frontend/src/services/browserBindings.js`
- Modify: generated Wails bindings under `frontend/bindings/`

**Interfaces:**
- Consumes: existing category markup and desktop bridge APIs.
- Produces: normal item spacing of `0.5rem`, no assistant Goal navigation or client API.

- [ ] Change only the expanded normal-group item container from `space-y-1` to `space-y-2`.
- [ ] Remove the Home button, route, view, preview state, client methods, and generated panel bindings.
- [ ] Run focused browser tests and confirm they pass.

### Task 3: Remove Backend Panel APIs

**Files:**
- Delete: `internal/client/goal.go`
- Delete: `internal/client/goal_test.go`
- Modify: `internal/bridge/proxy.go`
- Modify: `internal/backend/host.go`
- Modify: `internal/backend/forwarder/goal.go`
- Modify: `internal/backend/forwarder/service.go`
- Modify: `internal/backend/forwarder/goal_test.go`

**Interfaces:**
- Consumes: Cursor request execution through `handleRunIntent`.
- Produces: the same `/goal` execution behavior without panel snapshots or direct panel start/stop methods.

- [ ] Remove bridge, client, host, snapshot, registry, and direct start/stop APIs used only by the panel.
- [ ] Keep `applyGoalCommandIfEnabled`, active-stream Goal state, retry/verification/progress/completion behavior, and regular cancellation unchanged.
- [ ] Run forwarder Goal tests and relevant package tests.

### Task 4: Refresh Catalogs And Verify

**Files:**
- Modify: generated i18n catalog and locale JSON files.

**Interfaces:**
- Consumes: remaining Chinese UI source strings.
- Produces: synchronized catalogs without panel-only text and with intact Goal settings translations.

- [ ] Run the frontend build to rescan catalogs.
- [ ] Confirm every locale has identical non-empty keys and matching placeholders.
- [ ] Run unit tests, E2E tests, lint, build, Go tests, `git diff --check`, and live browser inspection.
