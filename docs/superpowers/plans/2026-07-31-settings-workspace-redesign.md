# Settings Workspace Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the overloaded settings drawer with a responsive full-page settings workspace that preserves all existing settings behavior and adds reliable autosave feedback.

**Architecture:** Add a `/settings` route with a focused page shell, reusable settings layout primitives, and category components that adapt the existing `appState` and service APIs. A shared autosave coordinator owns debounce, revision ordering, retry, and aggregate status, while each category owns only its domain data and actions.

**Tech Stack:** Vue 3 Composition API, Vue Router 4, Tailwind CSS, existing Cursor-byok UI components, static i18n scanner, Wails runtime bindings.

## Global Constraints

- Preserve the current dark theme: canvas `#191919`, page surface `#202020`, interactive surface near `#292929`, divider near `#343434`, accent `#10AD5D`.
- Use a 192px desktop settings sidebar and a content maximum width of 760px to 820px.
- Below 640px, replace the sidebar with a top category selector and prevent horizontal overflow.
- Switches and selects save immediately; text and numeric inputs save after approximately 500ms and flush on blur or Enter.
- The page header displays stable `已保存`, `正在保存`, or `保存失败` state; domain errors remain inline with retry.
- Keep confirmations for enabling direct mode and deleting delegation groups.
- Keep existing persisted setting keys and backend payload formats compatible.
- Chinese is the only source locale; run the static i18n build and fully populate `en-US`, `ja-JP`, and `ru-RU` generated entries.
- Do not add test files. Verify with builds, static checks, i18n consistency scripts, and structured browser/manual checks.
- Do not modify the installed Cursor client or `.cursor-app-formatted` snapshot.
- Do not drop or overwrite `stash@{0}: pre-merge user floating-window state 2026-07-31`.

## File Structure

- Create `frontend/src/views/Settings.vue`: route shell, category selection, responsive navigation, save-state aggregation, and back behavior.
- Create `frontend/src/components/settings/settingsCategories.js`: stable category identifiers, labels, descriptions, and icons.
- Create `frontend/src/components/settings/SettingsSidebar.vue`: desktop navigation and narrow-window category selector.
- Create `frontend/src/components/settings/SettingsPageHeader.vue`: back action, title, current description, and save status.
- Create `frontend/src/components/settings/SettingsSection.vue`: unframed section heading and divider layout.
- Create `frontend/src/components/settings/SettingsRow.vue`: common label/control/error/retry row.
- Create `frontend/src/composables/useSettingsAutosave.js`: keyed debounce, revision ordering, pending state, retry, flush, and aggregate state.
- Create category files under `frontend/src/components/settings/categories/` for General, Cursor/Service, Overlay, Delegation, Skills/MCP, Prompts, and Advanced.
- Modify `frontend/src/router/index.js`: register `/settings`.
- Modify `frontend/src/layouts/MainLayout.vue`: navigate to settings and remove drawer state/mount.
- Modify existing delegation components only where reuse avoids duplicated runtime/configuration logic.
- Delete `frontend/src/components/SettingsDrawer.vue` after parity is complete.
- Delete `frontend/src/views/Config.vue` after its unique controls are migrated.
- Update generated locale files through the scanner, then translate all new keys.

---

### Task 1: Settings Route, Shell, And Autosave Foundation

**Files:**
- Create: `frontend/src/views/Settings.vue`
- Create: `frontend/src/components/settings/settingsCategories.js`
- Create: `frontend/src/components/settings/SettingsSidebar.vue`
- Create: `frontend/src/components/settings/SettingsPageHeader.vue`
- Create: `frontend/src/components/settings/SettingsSection.vue`
- Create: `frontend/src/components/settings/SettingsRow.vue`
- Create: `frontend/src/composables/useSettingsAutosave.js`
- Modify: `frontend/src/router/index.js`
- Modify: `frontend/src/layouts/MainLayout.vue`

**Interfaces:**
- Produces: `useSettingsAutosave()` returning `status`, `hasErrors`, `schedule(key, save, options)`, `run(key, save)`, `retry(key)`, `flush(key?)`, `setError(key, error)`, and `clearError(key)`.
- Produces: category IDs `general`, `cursor-service`, `overlay`, `delegation`, `skills-mcp`, `prompts`, and `advanced`.
- Produces: a slot-based `SettingsRow` contract with `label`, `description`, `busy`, `error`, and `retry` event.

- [ ] **Step 1: Add the autosave coordinator.**

  Implement keyed state with a 500ms default debounce. Each key stores a timer, local revision, last save callback, pending count, and error. `schedule` increments the revision and replaces the same key's timer; `run` ignores stale completion state; `flush` executes queued callbacks; `retry` reruns the latest callback. Derive `status` as `saving` while any operation is pending, `error` while any unresolved error exists, otherwise `saved`.

- [ ] **Step 2: Add settings layout primitives.**

  Implement an unframed section and stable two-column row. The desktop row uses a left description column and a right control column; below 640px it stacks. Inline error and retry render in the control column without changing the label column width.

- [ ] **Step 3: Add category metadata and navigation.**

  Define all seven approved categories in one array. `SettingsSidebar` uses accessible buttons with `aria-current="page"` on desktop and the existing `Select.vue` on narrow layouts. Persist the selected ID under `cursor-byok.settings.category`; invalid values fall back to `general`.

- [ ] **Step 4: Add the settings route shell.**

  Register `/settings` with title `设置`, `showIcon: false`, and normal main-window close behavior. Render the sidebar and one active category slot inside a full-height scrollable workspace. Add a 150ms opacity and 4px transition and a reduced-motion override.

- [ ] **Step 5: Replace drawer opening with route navigation.**

  Remove the `SettingsDrawer` import, `settingsDrawerVisible`, and drawer mount from `MainLayout.vue`. Use `useRouter()` and navigate to `/settings`; if already on settings, the button remains a no-op. The page back action uses browser history only when the prior route is a non-settings application route, otherwise routes to `/`.

- [ ] **Step 6: Verify and commit the foundation.**

  Run `npm --prefix frontend run build`, `git diff --check`, and browser-check `/settings` at desktop and 620px widths. Confirm category navigation, back behavior, stable content width, no horizontal overflow, and reduced-motion styles.

  Commit: `feat: add settings workspace foundation`

### Task 2: General, Cursor, Overlay, And Advanced Categories

**Files:**
- Create: `frontend/src/components/settings/categories/GeneralSettings.vue`
- Create: `frontend/src/components/settings/categories/CursorServiceSettings.vue`
- Create: `frontend/src/components/settings/categories/OverlaySettings.vue`
- Create: `frontend/src/components/settings/categories/AdvancedSettings.vue`
- Modify: `frontend/src/views/Settings.vue`
- Modify: `frontend/src/state/appState.js`

**Interfaces:**
- Consumes: `useSettingsAutosave()` and shared row/section components from Task 1.
- Consumes: `LocaleSelect`, `getCursorManualPath`, `setCursorManualPath`, `detectCursorPath`, `getStatsOverlayPreferences`, `setStatsOverlayPreferences`, `showStatsOverlay`, `hideStatsOverlay`, `saveRoutingMode`, `saveLocalResponseCacheEnabled`, `persistUserConfig`, and `openConfigWindow`.
- Produces: `saveLocalResponseCacheSettings(partial)` in `appState.js`, which merges and persists `enabled`, `ttlSeconds`, and `maxEntries` without changing unrelated configuration.
- Produces: four category panels that emit or register save state with the settings page coordinator.

- [ ] **Step 1: Implement General settings.**

  Add interface language, main-window close behavior, and the configuration-folder action. Language keeps its existing immediate persistence. Close behavior uses overlay preferences. Do not duplicate local response cache controls here.

- [ ] **Step 2: Implement Cursor and Service settings.**

  Load the manual Cursor path and run detection on mount. The text field debounces local persistence, blur/Enter flushes it, and detection remains explicit. Invalid manual paths show inline errors. Add the routing mode control and service/model configuration actions available from the current settings/config views.

- [ ] **Step 3: Implement Floating Window settings.**

  Migrate style, visibility, always-on-top, edge collapse, lock, and close behavior using immediate scoped saves. Keep the cross-window preference synchronization already provided by `appState.js`; do not create a second local state authority.

- [ ] **Step 4: Implement Advanced settings.**

  Place direct mode in this category with the existing billing warning confirmation. Add local response cache enablement, duration, and maximum-entry controls here. Implement `saveLocalResponseCacheSettings(partial)` so switch changes save immediately and numeric fields use 500ms debounce plus blur/Enter flush without rewriting unrelated configuration.

- [ ] **Step 5: Verify and commit the four categories.**

  Run `npm --prefix frontend run build` and `git diff --check`. In browser preview, exercise every control, force an invalid Cursor path, confirm the direct-mode modal, verify 500ms/blur/Enter behavior, and reload to confirm local preferences survive.

  Commit: `feat: migrate core settings categories`

### Task 3: Models And Delegation Category

**Files:**
- Create: `frontend/src/components/settings/categories/DelegationSettings.vue`
- Modify: `frontend/src/components/DelegationSettingsCard.vue` or replace it with focused subcomponents under `frontend/src/components/settings/delegation/`
- Modify: `frontend/src/components/DelegationRuntimePanel.vue` only to remove card-specific framing and support the workspace layout.
- Modify: `frontend/src/views/Settings.vue`

**Interfaces:**
- Consumes: `appState.delegation`, `appState.modelAdapters`, `persistUserConfig`, runtime delegation APIs, `showModal`, and the Task 1 autosave coordinator.
- Produces: autosaved delegation configuration and a separate runtime status section.

- [ ] **Step 1: Separate persistent configuration from runtime status.**

  Render global enablement and maximum concurrency first, editable model groups second, and `DelegationRuntimePanel` last. Remove outer `Card` framing so the category uses sections and dividers while preserving compact containers for individual groups/tasks.

- [ ] **Step 2: Add autosave to delegation edits.**

  Save switches and selects immediately. Debounce group names and concurrency by 500ms and flush on blur/Enter. Use one serialized configuration save queue so concurrent group edits cannot overwrite a newer `appState.delegation` snapshot.

- [ ] **Step 3: Complete group interactions.**

  Preserve add, enable, model selection, default model, execution mode, and permission toggles. Add icon buttons to move each group up or down by reordering `appState.delegation.groups`, disabling the unavailable direction at the list ends, then persist immediately. Add delete confirmation naming the group. Keep the group visible and show an inline error when persistence fails. Do not introduce drag-and-drop.

- [ ] **Step 4: Preserve runtime actions.**

  Keep task refresh/cancel and MCP runtime connect/disconnect/cancel actions functional with per-item busy state. Runtime polling and cancellation must stop on unmount and must not block configuration saves.

- [ ] **Step 5: Verify and commit delegation.**

  Run `npm --prefix frontend run build` and `git diff --check`. Manually add/edit/delete a group, toggle permissions/models, change concurrency, cancel a runtime task where available, and confirm one failed operation does not disable the entire page.

  Commit: `feat: add settings delegation workspace`

### Task 4: Skills, MCP, And Prompt Categories

**Files:**
- Create: `frontend/src/components/settings/categories/SkillsMcpSettings.vue`
- Create: `frontend/src/components/settings/categories/PromptSettings.vue`
- Modify: `frontend/src/views/Settings.vue`
- Reuse: `frontend/src/components/PromptPreviewModal.vue`

**Interfaces:**
- Consumes: `getSkillsMCPScanSnapshot`, `refreshSkillsMCPScan`, `saveSkillsMCPScanConfig`, `getPromptInjectionSettings`, `savePromptInjectionSettings`, `refreshPromptInjectionCatalog`, and `refreshPromptInjection`.
- Produces: filtered Skills/MCP lists and autosaved prompt settings with stale-response protection.

- [ ] **Step 1: Implement Skills and MCP tabs.**

  Add accessible `Skills` and `MCP` tabs, a shared search field, status filter, scan enable switch, and refresh icon button. Preserve tab, query, and filter during refresh. Skill rows show name, source, description, and enablement. MCP rows show name/identifier, transport, command or URL, tool count/connection state when supplied, and enablement.

- [ ] **Step 2: Add list states and scoped persistence.**

  Render stable loading, empty, and inline error states. Save per-item enablement immediately without disabling unrelated rows. A failed save restores the visible switch state for that item and offers retry.

- [ ] **Step 3: Implement Prompt settings.**

  Migrate global enablement, templates, mode, selected template, repository, ref, custom content, custom enablement, localization, refresh, cache status, and preview. Use list rows instead of nested cards. Text inputs and custom content use 500ms debounce plus blur/Enter flush; switches/selects save immediately.

- [ ] **Step 4: Protect prompt saves from stale responses.**

  Build each save payload from the latest reactive state. Use autosave revisions so an older backend response cannot replace a newer edit. Refresh remains explicit and updates the current state only after success; failures remain inline with retry/fallback behavior.

- [ ] **Step 5: Verify and commit Skills/MCP/prompts.**

  Run `npm --prefix frontend run build` and `git diff --check`. Manually verify both tabs, search, all filters, refresh retention, per-row toggles, prompt template toggles, custom content debounce, preview, and representative empty/error states.

  Commit: `feat: migrate skills mcp and prompt settings`

### Task 5: Remove Duplicates, Complete I18n, And Final Verification

**Files:**
- Delete: `frontend/src/components/SettingsDrawer.vue`
- Delete: `frontend/src/views/Config.vue`
- Modify: settings files from Tasks 1-4 for final parity and responsive fixes.
- Modify generated: `frontend/src/i18n/generated/catalog.json`
- Modify generated/source: `frontend/src/i18n/locales/zh-CN.json`
- Modify translations: `frontend/src/i18n/locales/en-US.json`
- Modify translations: `frontend/src/i18n/locales/ja-JP.json`
- Modify translations: `frontend/src/i18n/locales/ru-RU.json`

**Interfaces:**
- Consumes: all completed category panels.
- Produces: one canonical settings implementation with complete locale catalogs and verified builds.

- [ ] **Step 1: Audit feature parity and remove old implementations.**

  Compare every control and action in `SettingsDrawer.vue` and `Config.vue` against the seven categories. Move any missing unique control before deleting both files. Run `rg "SettingsDrawer|views/Config|Config.vue" frontend/src` and confirm no runtime reference remains.

- [ ] **Step 2: Run the i18n scanner and translate new entries.**

  Run `npm --prefix frontend run build` to generate catalog entries. Add non-empty English, Japanese, and Russian translations for every new key, preserving placeholders exactly. Run the build again and confirm generated files are stable.

- [ ] **Step 3: Run locale consistency checks.**

  Use a Node one-liner to compare every locale key set with `catalog.json`, reject empty non-source values, and compare placeholder sets such as `{0}` and `${1}` between source and translations. The command must exit non-zero on any mismatch.

- [ ] **Step 4: Perform visual and interaction regression.**

  Use browser preview at wide desktop, normal desktop, 620px, and a narrower mobile-like width. Check all category buttons, tab/search/filter states, save-status transitions, inline errors, confirmations, no nested page cards, no overlap, no horizontal scroll, stable control sizing, and reduced-motion behavior. Confirm home, model configuration, floating overlay preferences, and title-bar window controls still work.

- [ ] **Step 5: Run final repository verification.**

  Run `npm --prefix frontend run build`, `go build ./...`, `git diff --check`, and `git status --short`. Confirm the safety stash remains present with `git stash list`.

- [ ] **Step 6: Review and commit completion.**

  Review the full branch diff for duplicated state, oversized files, silent errors, stale async updates, hard-coded non-Chinese source text, and unrelated changes. Fix all important findings, rerun the covering verification, then commit.

  Commit: `chore: complete settings workspace redesign`
