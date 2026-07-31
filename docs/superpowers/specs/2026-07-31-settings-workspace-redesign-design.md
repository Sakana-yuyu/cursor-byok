# Cursor-byok Settings Workspace Redesign

## Status

- Date: 2026-07-31
- Decision: approved visual direction A
- Scope: replace the overloaded settings drawer with a full settings workspace inside the main window
- Theme: preserve the current dark Cursor-byok visual language

## Problem

The current settings experience is concentrated in `frontend/src/components/SettingsDrawer.vue`, a roughly 440px-wide side drawer that contains unrelated controls for floating windows, Cursor launch, delegation, prompt injection, Skills, MCP, routing, and configuration access. The narrow surface forces long scrolling, weakens information hierarchy, and makes dense controls difficult to scan and operate.

There is also an unused `frontend/src/views/Config.vue` view with overlapping settings behavior. Keeping both implementations would create duplicated state handling and inconsistent save behavior.

## Goals

1. Make settings a comfortable, full-page workspace inside the existing main application window.
2. Organize settings by user intent so each page remains focused and scannable.
3. Save ordinary changes automatically and expose clear `saving`, `saved`, and `failed` states.
4. Preserve all existing settings capabilities and persisted values.
5. Keep risky operations explicit and confirmed without interrupting routine changes.
6. Provide a responsive layout that remains usable in narrow desktop windows.
7. Establish clear component and state boundaries so later settings features do not return to one oversized file.

## Non-goals

- Redesigning the home dashboard, model editor, diagnostics, or statistics overlay.
- Changing backend configuration formats or existing persisted preference semantics.
- Modifying the installed Cursor client or its bundle.
- Introducing a new visual theme, navigation shell, or component framework.
- Adding test files, which is prohibited by the repository's test requirements.
- Expanding the functionality of Skills, MCP, delegation, or prompts beyond presenting their existing controls more clearly.

## Information Architecture

The settings workspace contains seven categories in this order:

1. **通用**: interface language, main-window close behavior, configuration folder, and other application-wide preferences.
2. **Cursor 与服务**: Cursor path detection and manual fallback, local/direct routing mode, service-related controls, and links to model configuration where appropriate.
3. **浮窗**: visibility, style, always-on-top, edge collapse, lock state, and floating-window behavior.
4. **模型与委派**: delegation enablement, concurrency, model groups, execution mode, default models, and runtime status.
5. **Skills 与 MCP**: registry enablement, refresh, search, status filters, per-item switches, source/status details, and errors.
6. **提示词**: prompt injection mode, templates, repository/ref settings, localization, custom content, refresh, and preview.
7. **高级**: direct mode and other high-impact or low-frequency settings that do not belong in the primary workflows.

The category labels are Chinese source literals and must flow through the existing static i18n scanner.

## Route And Navigation

- Add a normal application route at `/settings` using the existing router history mode.
- The title-bar settings button navigates to `/settings` instead of opening a teleported drawer.
- The existing title bar remains visible and identifies the page as settings through route metadata.
- A back button at the start of the settings workspace returns to the previous non-settings route when history is available, otherwise it returns to `/`.
- Direct navigation and reload on `/settings` must restore the settings page without requiring drawer state.
- The footer remains governed by the existing route rules; it is not duplicated inside settings.
- Settings category selection is local page state, not a separate route per category in this iteration. This avoids unnecessary history entries while preserving fast navigation.
- The selected category is persisted locally so reopening settings returns to the last section. Invalid or removed category values fall back to `通用`.

## Desktop Layout

The settings page fills the main content area below the existing title bar.

- A page header contains the back action, `设置` title, a short current-category description, and save status.
- A local settings sidebar is approximately 192px wide and remains visually attached to the page rather than floating as a card.
- The content column uses a readable maximum width between 760px and 820px, centered within the remaining area when extra width is available.
- Content is presented as continuous groups separated by spacing and divider lines. Page sections are not wrapped in decorative cards.
- Each setting row uses a stable two-column structure: label and description on the left, control and local status on the right.
- Repeated domain items such as delegation model groups, skill entries, and MCP servers may use compact item containers because they are discrete records.
- Long values such as executable paths, repository names, and MCP identifiers wrap or truncate with accessible full-value affordances without resizing the layout.

## Visual Language

The redesign preserves the current product theme while improving hierarchy.

- Application canvas: `#191919`
- Primary page surface: `#202020`
- Interactive and elevated surface: approximately `#292929`
- Borders and dividers: approximately `#343434`
- Primary accent and positive status: `#10AD5D`
- Primary text: near-white matching the current interface
- Secondary text: neutral gray matching the current interface
- Error state: existing restrained red palette
- Border radius: 6px to 8px for controls and repeated item containers
- Typography: retain the current PingFang-oriented body stack and HFKos numeric treatment
- Letter spacing remains zero; type sizes stay appropriate to a dense settings workspace

Icons use the existing icon system for back, search, refresh, retry, open-folder, add, delete, and status actions. Icon-only buttons require `aria-label`, title/tooltips where the meaning is not obvious, keyboard focus styles, and stable dimensions.

## Responsive Behavior

At widths of 640px and above, the settings sidebar and content column appear side by side.

Below 640px:

- The sidebar becomes a compact top category selector.
- The selector remains visible near the page header and does not consume permanent horizontal space.
- Setting rows stack their label and control areas when the right control would become cramped.
- Repeated model, Skill, and MCP items use a single-column layout.
- Text and controls must remain inside the viewport without horizontal scrolling.

The page should tolerate both normal and maximized Wails window sizes. Stable grid tracks, min/max widths, and explicit control dimensions prevent layout shifts when labels, busy text, or errors change.

## Motion

- Category content transitions use a restrained 150ms opacity transition with at most 4px vertical movement.
- Save-state changes may fade without moving surrounding layout.
- No sliding drawer animation remains.
- Repeated items do not animate their dimensions during normal autosave.
- `prefers-reduced-motion: reduce` disables non-essential movement and shortens or removes transitions.

## Autosave Model

Settings save immediately by default. The page exposes a single header-level save indicator while retaining field-level errors.

### Save triggers

- Switches, selects, segmented controls, and discrete actions save immediately after a valid change.
- Text and numeric inputs save after approximately 500ms of inactivity.
- Blur and Enter flush a pending text save immediately.
- Refresh, detect, open-folder, preview, retry, add, delete, and cancel remain explicit commands rather than autosaved values.

### State machine

Each settings domain tracks its own pending operation and error, while the page derives one aggregate header state:

```text
idle/saved -> dirty -> saving -> saved
                         |        |
                         v        v
                       failed <- retry
```

- `已保存`: no dirty values or active saves remain.
- `正在保存`: at least one domain has an active save.
- `保存失败`: the most recent save failed and requires a retry or another edit.
- A successful later save clears only the corresponding domain error; it must not hide unrelated failures.
- The indicator occupies stable space so status changes do not move the page header.

### Concurrency and stale responses

- Debounced saves are keyed by setting or domain.
- A newer edit supersedes an older queued edit for the same key.
- Responses carry or are associated with a monotonically increasing local revision. An older response must not overwrite newer local state or clear its error.
- Controls that cannot safely overlap disable only for their own operation, not the entire settings page.
- Leaving the page flushes valid pending text edits where possible; an in-flight save is allowed to complete through the existing state/service layer.

## Errors And Confirmation

- Routine save failures appear inline beside the affected setting or section with a retry action.
- The header also displays `保存失败` while any unresolved domain error exists.
- Ordinary failures do not open blocking modals.
- Successful routine autosaves do not produce repeated toast notifications.
- Explicit commands such as rescanning Skills/MCP or detecting Cursor may use one concise success message when the result is not otherwise visible.
- Existing confirmation remains for high-impact actions, including enabling direct mode and deleting a delegation model group.
- Destructive confirmation text states the object being changed and the effect of the action.
- Backend error details are normalized through the existing `toUserError` path before display.

## Skills And MCP Interaction

The `Skills 与 MCP` category uses two tabs: `Skills` and `MCP`.

- A shared toolbar contains search, status filter, and refresh.
- Status filters cover all, enabled, disabled, and error states when error information is available.
- Search matches visible name, identifier, source, and concise description.
- Each row shows the primary name, source, compact status, and an enable switch.
- MCP rows additionally show transport, discovered tool count, and connection state when supplied by the runtime snapshot.
- Empty, loading, and failure states occupy the content area without changing the page shell.
- Refresh does not discard the current tab, query, or filter.

## Models And Delegation Interaction

- Global delegation enablement and maximum concurrency appear before the group list.
- Delegation model groups remain repeated compact items because each group is an editable record.
- Add, delete, reorder, enable, model selection, default model, execution mode, and tool permission controls retain real actions and scoped busy states.
- Runtime task status is visually separated from persistent group configuration, while remaining in the same category.
- Deleting a group requires confirmation. A failed delete leaves the item visible and displays an inline error.

## Prompts Interaction

- Global injection enablement and a concise current status appear at the top.
- Template selection is a scannable list, not nested expandable cards.
- Repository, ref, selected template, mode, localization, and custom prompt settings use the common setting-row layout.
- Preview and refresh remain explicit commands.
- Template content is not fully rendered in the primary list; preview opens the existing dedicated modal.

## Component And Module Boundaries

The implementation should create focused modules rather than move the current drawer into one larger page.

- `views/Settings.vue`: route-level shell, selected category, responsive navigation, aggregate save status, and back behavior.
- `components/settings/SettingsSidebar.vue`: desktop category navigation and narrow-width category selector.
- `components/settings/SettingsPageHeader.vue`: title, description, back button, and save status.
- `components/settings/SettingsSection.vue`: common unframed section heading and divider structure.
- `components/settings/SettingsRow.vue`: common label, description, control, busy, error, and retry layout.
- Category panels such as `GeneralSettings.vue`, `CursorServiceSettings.vue`, `OverlaySettings.vue`, `DelegationSettings.vue`, `SkillsMcpSettings.vue`, `PromptSettings.vue`, and `AdvancedSettings.vue` own only their domain interaction logic.
- A settings autosave composable coordinates debounce, revisions, pending state, retry, flush, and aggregate status without knowing backend-specific payloads.
- Existing state and API modules remain the source of persistence behavior. Category components adapt them rather than duplicate backend calls.

Names may be adjusted to match repository conventions, but each module must retain a single clear responsibility.

## Migration Strategy

1. Add the `/settings` route and route-level workspace shell.
2. Extract behavior from `SettingsDrawer.vue` by domain into focused category components.
3. Redirect the title-bar settings action to the new route.
4. Preserve existing service calls and stored values while introducing the shared autosave lifecycle.
5. Remove the drawer mount and its transient visibility state after parity is verified.
6. Retire `SettingsDrawer.vue` once no caller remains.
7. Fold the useful, non-duplicated behavior from `Config.vue` into the appropriate categories.
8. Remove `Config.vue` if it remains unreferenced after migration; do not keep a second hidden settings implementation.

Migration must not reset user preferences. Existing keys for floating-window behavior, Cursor manual path, routing, delegation, prompt injection, Skills/MCP, locale, and local response cache continue to be read and written through their current persistence paths unless an implementation blocker requires a separately reviewed backend change.

## Accessibility

- All controls have programmatic labels and clear keyboard focus treatment.
- Category navigation exposes current selection with `aria-current` or the appropriate tab/listbox semantics.
- Tabs follow keyboard-accessible tab behavior.
- Save status and command results use a restrained live region without repeatedly announcing every keystroke.
- Errors are associated with their fields and remain available until resolved.
- Color is not the only indicator for save, connection, enabled, or error state.
- Touch and pointer targets retain stable usable dimensions.

## Internationalization

- Chinese remains the source locale for all new user-visible text under `frontend/src/`.
- Components do not branch on locale and do not hand-write generated message IDs.
- The frontend build runs the static i18n scan and updates generated catalogs.
- Every non-source locale receives non-empty translations with placeholders preserved exactly.
- Category labels and descriptions are concise enough to fit the responsive selector in supported locales.

## Verification

No new test files will be added. Verification consists of repository builds, static checks already present in the project, and structured manual checks.

### Automated

- Run `npm --prefix frontend run build` and confirm the i18n scan succeeds.
- Run `go build ./...` to detect integration regressions.
- Run `git diff --check`.
- Compare generated locale key sets and confirm non-source translations are non-empty with matching placeholders.

### Manual desktop checks

- Open settings from the title bar, navigate every category, return home, and reopen at the last category.
- Exercise every visible switch, select, text input, icon button, menu, tab, retry, refresh, add, delete, and external/configuration action.
- Confirm immediate saves, 500ms text debounce, blur/Enter flush, scoped busy states, and stable header status.
- Force representative failures and confirm inline recovery without blocking unrelated controls.
- Verify direct-mode and group-delete confirmations.
- Verify Skills/MCP search, tabs, filters, refresh, empty states, and per-item enablement.
- Verify prompt preview, refresh, template enablement, and custom content persistence.
- Verify Cursor auto-detection, manual path fallback, floating-window preferences, close behavior, locale, local cache, and delegation settings survive restart.

### Responsive and visual checks

- Inspect common desktop window sizes, maximized mode, and widths below 640px.
- Confirm no horizontal overflow, clipped controls, nested-card appearance, overlapping text, or layout shifts caused by save/error labels.
- Confirm restrained transitions and reduced-motion behavior.
- Compare the implementation against the approved visual companion screens:
  - `settings-workspace-layout.html`
  - `settings-interaction-responsive.html`

## Acceptance Criteria

- Settings open as a full page within the main window; the old side drawer is no longer used.
- All existing settings capabilities remain available under the seven approved categories.
- Every visible control performs a real action and exposes disabled, busy, success, or failure feedback as appropriate.
- Ordinary settings autosave according to their control type and never silently discard a newer edit.
- Routine errors are inline and retryable; risky actions retain confirmation.
- Desktop and narrow layouts remain clear, stable, and free from horizontal overflow.
- The visual result remains consistent with the existing dark theme and approved direction A.
- `SettingsDrawer.vue` and the unused duplicate `Config.vue` do not remain as competing settings implementations after migration.
- Existing persisted user values continue to work without migration loss.
