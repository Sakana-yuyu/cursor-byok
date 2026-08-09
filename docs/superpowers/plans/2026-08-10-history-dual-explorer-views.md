# History Dual Explorer Views Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the existing History details table and add a persistent Windows-style folder icon view as the default.

**Architecture:** `HistorySettings.vue` owns a small local view state (`icons` or `details`) and an icon-view directory path. Both renderers consume the existing `groupedTree` and `selectedIDs`; history APIs and mutation flows remain unchanged.

**Tech Stack:** Vue 3, Tailwind CSS utilities, Iconify MDI icons, Playwright, Vite static i18n scanner.

## Global Constraints

- Preserve the existing full-height history panel and batch operations.
- Preserve the current details table without removing columns or tree controls.
- Default to Icon view only when no stored preference exists.
- Persist the chosen mode under `cursor-byok.history.view-mode`.
- Do not clear selected sessions when switching views or directories.
- Write new source UI text in Chinese and regenerate all locale catalogs.

---

### Task 1: Define Dual-View Behavior With Failing E2E Tests

**Files:**
- Modify: `frontend/e2e/history-settings-layout.spec.mjs`

**Interfaces:**
- Consumes: `/settings?category=history`, browser preview history sessions, localStorage.
- Produces: assertions for default mode, directory navigation, shared selection, details mode, and mode persistence.

- [ ] **Step 1: Replace the details-only default assertion**

Assert that `历史图标视图` is visible by default, with `2026` and `2025` folder buttons and no details column headers.

- [ ] **Step 2: Add folder drill-down assertions**

Double-click `2026`, then `2026年7月`, then `2026年7月31日`; assert breadcrumbs update and session tiles appear. Use the Up button to return one level.

- [ ] **Step 3: Add shared selection and persistence assertions**

Select a session tile, switch to Details view, assert its row remains selected, reload, and assert Details view remains active.

- [ ] **Step 4: Run the focused test and verify red**

Run `npx playwright test e2e/history-settings-layout.spec.mjs`. Expected: FAIL because icon mode controls and folder navigation do not exist.

### Task 2: Implement Shared View State And Icon Navigation

**Files:**
- Modify: `frontend/src/components/settings/categories/HistorySettings.vue`

**Interfaces:**
- Consumes: `groupedTree`, `selectedIDs`, `toggleSession`, `openInDiagnostics`.
- Produces: `historyViewMode`, `historyPath`, `currentIconItems`, `enterHistoryFolder`, `navigateHistoryPath`, and localStorage persistence.

- [ ] **Step 1: Add mode persistence helpers**

Read `cursor-byok.history.view-mode`, accept only `icons` or `details`, default to `icons`, and write changes through a Vue watcher.

- [ ] **Step 2: Add directory path projection**

Represent the current path as an array of `{ type, key, label }`. Resolve the active year/month/day from `groupedTree`, and project the next folders or sessions into `currentIconItems`.

- [ ] **Step 3: Add segmented mode controls and navigation bar**

Place icon-only mode buttons with tooltips in the batch toolbar. In Icon view, show Up and breadcrumb controls above the item viewport.

- [ ] **Step 4: Render folder and session tiles**

Use a responsive two-column desktop grid with stable tile dimensions. Double-click folders to enter, single-click sessions to select, and double-click debug sessions to open Diagnostics.

- [ ] **Step 5: Keep the existing details table behind Details mode**

Wrap the current grid in the Details branch without changing its data, selection, diagnostic, or overflow behavior.

- [ ] **Step 6: Run focused E2E and verify green**

Run `npx playwright test e2e/history-settings-layout.spec.mjs`. Expected: PASS.

### Task 3: Regenerate Internationalization And Verify

**Files:**
- Generated: `frontend/src/i18n/generated/catalog.json`
- Generated/translated: `frontend/src/i18n/locales/*.json`

**Interfaces:**
- Consumes: new Chinese source labels.
- Produces: complete English, Japanese, Russian, and Chinese catalogs.

- [ ] **Step 1: Run the production scan build**

Run `npm run build` from `frontend`.

- [ ] **Step 2: Translate new empty entries**

Provide non-empty translations for the view names, breadcrumb root, Up action, folder/session accessible labels, and item-count labels.

- [ ] **Step 3: Run the build again**

Confirm scanner output is stable and all locale files have matching keys with no empty values.

- [ ] **Step 4: Run final verification**

Run history and settings navigation E2E tests, unit tests, lint, `git diff --check`, and visually inspect desktop and narrow viewports in the live browser.
