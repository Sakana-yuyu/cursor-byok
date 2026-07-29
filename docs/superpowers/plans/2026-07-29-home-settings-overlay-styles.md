# 主页设置收束与三种浮窗样式 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 保留现有主页统计展示，将低频配置移入标题栏设置抽屉，并实现可持久化切换、带对应动效的卡片式、引擎仪表式和球形式统计浮窗。

**Architecture:** Vue 主页继续负责运行状态、服务控制、直连模式、请求明细、导出日志、模型配置和现有统计组件。`MainLayout` 的 Windows 标题栏增加三条杠设置入口，设置内容以主窗口内的右侧抽屉呈现并复用现有业务状态。`StatsOverlay.vue` 根据持久化样式状态渲染三种视觉分支，`WindowService` 维护单例原生浮窗并提供尺寸、置顶和样式更新能力。

**Tech Stack:** Go, Wails v3 alpha.74, Vue 3, Vue Router, Vite, Tailwind CSS, CSS animations, existing Wails generated bindings.

## Global Constraints

- 保留现有 `HomeMetricsCard` 的结构、缓存命中率样式和交互。
- 保留现有 `StationSpendCard` 站点消耗展示。
- 请求明细、导出日志、模型配置、服务开启 / 关闭、直连模式保留在主页。
- 浮窗、提示词与本地化、高级连接、设置文件夹移入统一设置入口。
- 不新增主页趋势图，不重构指标计算与后端统计接口。
- 本仓库禁止写任何测试文件；使用构建、静态检查和浏览器视觉验证。
- 不修改已安装的 Cursor 客户端代码、bundle 或 app 副本。
- 不引入新的动画依赖；使用 CSS 动画和过渡。
- 动效必须遵守 `prefers-reduced-motion: reduce`，不改变布局尺寸、不遮挡文字。

---

### Task 1: Extend Native Overlay Window Controls

**Files:**
- Modify: `internal/bridge/window.go:286-363`
- Regenerate: `frontend/bindings/cursor/internal/bridge/windowservice.js`
- Modify: `frontend/src/services/browserBindings.js`

**Interfaces:**
- Produces native bridge methods `OpenStatsOverlayWindow`, `CloseStatsOverlayWindow`, and new `UpdateStatsOverlayWindow(style string, alwaysOnTop bool)` or the repository-equivalent generated method signature.
- `UpdateStatsOverlayWindow` changes the existing singleton window's size/positioning flags without creating a second window.

- [ ] **Step 1: Read the existing Wails window API usage and generated binding conventions**

Confirm how `SetSize`, `SetAlwaysOnTop` or equivalent methods are exposed in the current Wails v3 alpha.74 API and mirror existing window service methods. Do not infer method names without checking the installed module or generated bindings.

- [ ] **Step 2: Add a style-to-window-size mapping in `WindowService`**

Use stable sizes that fully contain each style, for example:

```go
const (
    statsOverlayStyleCard  = "card"
    statsOverlayStyleEngine = "engine"
    statsOverlayStyleOrb    = "orb"
)

func statsOverlayWindowSize(style string) (width, height int) {
    switch style {
    case statsOverlayStyleEngine:
        return 240, 104
    case statsOverlayStyleOrb:
        return 196, 196
    default:
        return 240, 112
    }
}
```

Keep the native background transparent, frameless, taskbar-hidden, and optionally always-on-top.

- [ ] **Step 3: Add update behavior for an existing singleton window**

When the window exists, update its size and topmost state; when it does not exist, retain the current create-on-open behavior. Ensure closing clears the singleton as it does today.

- [ ] **Step 4: Regenerate bindings and add browser-preview no-op exports**

Run the repository binding generation command. If the new method is exposed to the frontend, add the matching no-op browser export so `vite --mode browser-preview` can resolve all imports.

- [ ] **Step 5: Verify the native package compiles**

Run:

```text
task common:generate:bindings
go build ./...
```

Expected: both commands exit successfully.

---

### Task 2: Add Persisted Overlay Preferences and Client API

**Files:**
- Modify: `frontend/src/services/clientApi.js`
- Modify: `frontend/src/state/appState.js`
- Modify: `frontend/src/services/browserBindings.js` if generated bridge imports require it
- Modify: existing local-storage preference module if one is already used for app preferences

**Interfaces:**
- `getStatsOverlayPreferences()` returns `{ style: "card" | "engine" | "orb", alwaysOnTop: boolean, visible: boolean }` with defaults `{ style: "card", alwaysOnTop: true, visible: false }`.
- `setStatsOverlayPreferences(next)` persists the normalized object and calls the native update bridge when desktop mode is active.
- `openStatsOverlay()` continues to toggle the singleton native window.

- [ ] **Step 1: Locate the existing local-storage helper pattern**

Reuse the existing `appState` or utility storage helpers instead of introducing another generic storage abstraction.

- [ ] **Step 2: Implement normalization and persistence**

Accept only `card`, `engine`, and `orb`; fall back to `card`. Persist a single JSON object under a namespaced key such as `cursor-byok.stats-overlay.preferences`.

- [ ] **Step 3: Wire desktop and browser-preview behavior**

Desktop calls the generated native update method. Browser preview updates local state only and keeps the route preview usable.

- [ ] **Step 4: Verify imports and production/browser builds at the API level**

Run `rg` to confirm every imported bridge function is present in generated bindings or browser mocks before building.

---

### Task 3: Refactor MainLayout Titlebar and Settings Drawer

**Files:**
- Modify: `frontend/src/layouts/MainLayout.vue`
- Create or modify: `frontend/src/components/SettingsDrawer.vue` if a focused component is preferable
- Modify: `frontend/src/services/clientApi.js` only for existing action wrappers required by the drawer

**Interfaces:**
- Titlebar exposes a Windows-only button with `aria-label="设置"`, tooltip text, and `--wails-draggable: no-drag`.
- Settings drawer emits `close` and contains settings sections without duplicating backend state logic.

- [ ] **Step 1: Add the hamburger settings button immediately left of minimize**

Keep existing minimize, maximize/restore, and close controls. The new button opens the drawer and does not alter window drag behavior.

- [ ] **Step 2: Implement drawer open/close behavior**

Use a fixed right-side panel with an overlay/backdrop. Close on explicit close button and backdrop click; keep focus and controls usable at the minimum supported window size.

- [ ] **Step 3: Add the floating-window settings group**

Expose show/hide, three style choices, and always-on-top. Style changes update persistence and call the native window update bridge.

- [ ] **Step 4: Move prompt/localization configuration into the drawer**

Move the existing prompt injection controls and custom injection sections out of the Home template into the drawer or a drawer-owned subview. Preserve existing methods, busy states, save behavior, preview modal, and error rendering.

- [ ] **Step 5: Move advanced connection and settings-folder actions into the drawer**

Reuse existing `handleDirectModeChange` logic only for the homepage control; the drawer's connection section must expose the advanced connection configuration and settings-folder action without deleting the underlying functionality.

- [ ] **Step 6: Verify titlebar and drawer structure in browser preview**

Run the browser preview and inspect the DOM/screenshot at desktop and narrow viewports. Confirm the drawer does not cover the titlebar controls and the homepage remains readable when closed.

---

### Task 4: Simplify Home Without Changing Existing Metrics

**Files:**
- Modify: `frontend/src/views/Home.vue`
- Modify: `frontend/src/components/HomeMetricsCard.vue` only if required to preserve existing visual behavior; avoid otherwise
- Modify: `frontend/src/components/StationSpendCard.vue` only if required to preserve existing visual behavior; avoid otherwise

**Interfaces:**
- Home directly renders service status, local/upstream mode switch, request metrics, export logs, model config, service toggle, `HomeMetricsCard`, and `StationSpendCard`.
- Home no longer renders advanced connection, prompt injection, localization, custom injection, or their expanded cards.

- [ ] **Step 1: Move prompt state ownership out of Home**

Transfer the existing prompt-loading, saving, refresh, template, custom content, and preview state to the drawer-owned component without changing payloads or method names.

- [ ] **Step 2: Remove the Home advanced connection and prompt cards**

Delete only their template usage and local expansion refs. Keep the direct-mode handler available for the top control area.

- [ ] **Step 3: Rebuild only the top control area**

Keep the existing service status, listener address, request details, export logs, model config, and service toggle buttons. Add the non-collapsed local service / direct Cursor control in the same area.

- [ ] **Step 4: Confirm the existing metrics cards are unchanged**

Compare the rendered `HomeMetricsCard` and `StationSpendCard` structure before and after the edit. Do not add new charts or replace the current cache-hit presentation.

- [ ] **Step 5: Verify browser preview behavior**

Run the preview and check that direct mode can be changed, request details and export logs remain directly reachable, and the homepage has no prompt/advanced-connection expansion cards.

---

### Task 5: Implement Three Overlay Render Modes and Motion

**Files:**
- Modify: `frontend/src/views/StatsOverlay.vue`
- Modify: `frontend/src/style/global.css` only if reduced-motion or shared animation rules belong there
- Create: no new test files

**Interfaces:**
- `StatsOverlay.vue` reads the normalized preference and renders one of `card`, `engine`, or `orb` modes.
- Existing metric loading and 10-second refresh behavior remains shared across all modes.

- [ ] **Step 1: Preserve the shared data and formatting functions**

Keep `getHomeMetricsSummary`, `fetchLocalCacheStats`, the 10-second refresh interval, and existing metric formatting. Add only style-state and animation-state refs/computed values.

- [ ] **Step 2: Add card mode with actual transparent outer spacing**

Use a transparent root sized to the native window and an inner panel inset by 6-8px. Keep the current 2x2 metric arrangement and labels. Ensure the panel border cannot touch the native window edge.

- [ ] **Step 3: Add engine mode**

Render the cache hit rate as a ring/gauge and the remaining metrics as compact telemetry cells. Use CSS `conic-gradient` or border techniques only inside the component; do not add an SVG hero or new dependency.

- [ ] **Step 4: Add orb mode**

Render a stable square transparent canvas with a central orb and four compact satellite metric labels. Keep all satellites within the native window bounds.

- [ ] **Step 5: Add mode-specific motion**

Implement:

```css
.overlay-card .status-dot { animation: overlay-breathe 2.4s ease-in-out infinite; }
.overlay-engine .gauge { transition: stroke-dashoffset 600ms ease-out; }
.overlay-engine .scanline { animation: overlay-scan 3.8s linear infinite; }
.overlay-orb .orb-core { animation: overlay-float 4.2s ease-in-out infinite; }
.overlay-orb .orb-glow { animation: overlay-pulse 2.8s ease-in-out infinite; }
```

Adapt implementation to the final DOM and avoid layout-affecting transforms. Add a `@media (prefers-reduced-motion: reduce)` block that disables infinite animations and reduces transitions to immediate changes.

- [ ] **Step 6: Verify each mode at its native size**

Capture browser-preview clips for card, engine, and orb. Confirm no clipping, no border-to-edge contact in card mode, complete labels, and readable contrast.

---

### Task 6: Integrate, Build, and Package

**Files:**
- Modify: build/package script only if a new output filename is required because the old package is locked
- Create: visual verification screenshots under `gui-test-screenshots/`

**Interfaces:**
- Final artifact is a Windows zip containing one production executable with real Wails bindings.

- [ ] **Step 1: Run binding generation**

```text
task common:generate:bindings
```

- [ ] **Step 2: Run backend and frontend builds**

```text
go build ./...
npm --prefix frontend run build
```

Expected: exit code 0. Existing Vite chunk-size warnings may remain; no errors are acceptable.

- [ ] **Step 3: Run browser-preview build**

```text
npm --prefix frontend exec vite -- build frontend --mode browser-preview
```

Confirm browser mocks export every bridge method imported by the production client API.

- [ ] **Step 4: Visually verify the full workflow**

Check:

- Homepage direct mode is visible without expanding a card.
- Request details and export logs are direct homepage actions.
- Existing cache-hit presentation remains recognizable and unchanged.
- Hamburger settings button is immediately left of minimize.
- Drawer contains floating-window, prompt/localization, advanced-connection, and settings-folder controls.
- Each overlay mode opens, switches, animates, refreshes, and remains within its native bounds.

- [ ] **Step 5: Build a non-conflicting Windows package**

If `bin/windows-64-overlay-fixed.zip` is locked, use a new filename such as `bin/windows-64-home-settings.zip` rather than terminating the running application or deleting a locked file.

- [ ] **Step 6: Record package metadata**

Record zip size, executable name, last write time, and SHA-256. Report any warnings separately from successful build results.
