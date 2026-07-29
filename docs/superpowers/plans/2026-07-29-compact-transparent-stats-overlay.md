# 紧凑透明统计浮窗 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将统计浮窗调整为 240x104 的透明桌面挂件，并保持无边框、不可缩放和始终置顶。

**Architecture:** Vue 页面移除整块不透明背景并收紧垂直间距，四个指标盒保留半透明深色承载内容。Wails 原生窗口使用透明背景类型和 alpha 0 初始化色，同时保留现有置顶及 Windows 挂件参数。

**Tech Stack:** Go、Wails v3 alpha.74、Vue 3、Vite、Tailwind CSS

## Global Constraints

- 不新增测试文件。
- 不修改已安装 Cursor 客户端。
- 不改统计数据来源和 10 秒刷新周期。
- 不提交或推送 Git 变更。
- 原生窗口固定为 240x104。

---

### Task 1: 紧凑透明 Vue 内容

**Files:**
- Modify: `frontend/src/views/StatsOverlay.vue:64-128`

**Interfaces:**
- Consumes: 现有统计 computed 数据
- Produces: 透明、无溢出的 240x104 内容布局

- [ ] 将根容器的 `bg-[#191919]` 改为 `bg-transparent`，外边距从 `p-2.5` 收紧为 `px-2 py-1.5`，纵向间距从 `gap-1.5` 收紧为 `gap-1`。
- [ ] 将网格间距从 `gap-1.5` 收紧为 `gap-1`。
- [ ] 四个指标盒改为 `bg-[#242424]/90`，保留边框、圆角、字号和现有 tooltip。
- [ ] 运行 `npm --prefix frontend run build`，确认 production 构建成功。

### Task 2: 透明置顶原生窗口

**Files:**
- Modify: `internal/bridge/window.go:308-342`

**Interfaces:**
- Consumes: Wails `application.WebviewWindowOptions`
- Produces: 240x104 透明置顶 WebviewWindow

- [ ] 将 `Height` 从 120 改为 104。
- [ ] 增加 `BackgroundType: application.BackgroundTypeTransparent`。
- [ ] 将 `BackgroundColour` 改为 `application.RGBA{Alpha: 0}`。
- [ ] 保留 `AlwaysOnTop: true`、`Frameless: true`、`DisableResize: true`、`HiddenOnTaskbar: true` 和 `DisableFramelessWindowDecorations: true`。
- [ ] 运行 `gofmt -w internal/bridge/window.go` 和 `go build ./...`。

### Task 3: 视觉验证与重新打包

**Files:**
- Output: `gui-test-screenshots/stats-overlay-240x104-transparent.png`
- Output: `bin/windows-64-overlay-fixed.zip`

**Interfaces:**
- Consumes: Tasks 1-2 的最终源码
- Produces: GUI 证据和 Windows 测试包

- [ ] 启动 `npm --prefix frontend run dev:browser -- --port 5174`。
- [ ] 在浏览器中检查 `/stats-overlay`，读取 DOM 并截取左上角 240x104 区域，确认状态行和四个盒子完整、无页面底板。
- [ ] 停止临时 5174 服务。
- [ ] 重新执行 bindings、production 前端构建、Go production 构建和 Windows 压缩打包。
- [ ] 计算最终 zip 的 SHA-256 并报告路径、大小和验证结果。
