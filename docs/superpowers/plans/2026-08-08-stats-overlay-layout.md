# Stats Overlay Layout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the expanded engine statistics overlay readable without clipping or chart-legend overlap.

**Architecture:** Align the native engine window height with the Vue layout, reserve a header row inside the chart for its legend, and rebalance the engine panel's fixed dimensions. The existing data flow, preferences, docking, and collapsed state remain unchanged.

**Tech Stack:** Go/Wails window sizing, Vue 3, scoped CSS, ECharts.

## Global Constraints

- Preserve the existing metrics, action controls, preferences, and docking behavior.
- Do not add user-visible copy or change locale files.
- Keep the overlay non-resizable.

---

### Task 1: Reserve Expanded Engine Layout Space

**Files:**
- Modify: `internal/bridge/window.go:statsOverlayWindowSize`
- Modify: `frontend/src/views/StatsOverlay.vue:engine overlay styles`

**Interfaces:**
- Consumes: `statsOverlayStyleEngine` and the existing `layout|...|style|...` DSL.
- Produces: a 240px-wide, 220px-tall engine window whose content fits without clipping.

- [ ] **Step 1: Update the engine native window height**

```go
case statsOverlayStyleEngine:
    return 240, 220
```

- [ ] **Step 2: Rebalance the engine panel dimensions**

```css
.engine-panel {
  flex-basis: 88px;
  height: 88px;
  min-height: 88px;
}
```

### Task 2: Separate The Trend Legend From The Plot

**Files:**
- Modify: `frontend/src/components/charts/StatsOverlayChart.vue:chart layout and CSS`

**Interfaces:**
- Consumes: `StatsOverlayChart` and its existing `refreshKey` prop.
- Produces: a 66px chart with a fixed legend header and a plot canvas below it.

- [ ] **Step 1: Change the chart container to a two-row grid**

```css
.stats-overlay-chart {
  height: 66px;
  display: grid;
  grid-template-rows: 16px minmax(0, 1fr);
}
.stats-overlay-chart__canvas {
  position: static;
  grid-row: 2;
}
.stats-overlay-chart__legend {
  position: static;
  grid-row: 1;
}
```

- [ ] **Step 2: Verify the legend cannot overlay ECharts data**

Use an engine-style screenshot at `240 × 220`; confirm the legend occupies only the header row.

### Task 3: Build And Visual Verification

**Files:**
- Verify: `frontend/src/views/StatsOverlay.vue`
- Verify: `frontend/src/components/charts/StatsOverlayChart.vue`
- Verify: `internal/bridge/window.go`

- [ ] **Step 1: Run the frontend production build**

Run: `npm run build` from `frontend/`.

- [ ] **Step 2: Start browser preview and capture the stats-overlay route**

Run: `npm run dev:browser -- --port 4173` from `frontend/`, then capture `/#/stats-overlay` at `240 × 220`.

- [ ] **Step 3: Inspect the screenshot**

Confirm the control strip is within bounds, telemetry labels and values fit, the legend is above the plot, and the chart has no horizontal clipping.
