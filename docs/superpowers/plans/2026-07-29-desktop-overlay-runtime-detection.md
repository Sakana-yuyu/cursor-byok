# 桌面浮窗与运行环境判定修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 Windows 打包程序被误判为浏览器预览的问题，并让统计面板以 240x120 桌面独立置顶窗口显示。

**Architecture:** 浏览器预览改为只受 Vite mode 或显式环境变量控制，桌面包默认使用真实 Wails bindings。App 根组件按路由隔离统计浮窗，使 `/stats-overlay` 不经过通用 MainLayout；后端窗口继续使用独立 WebviewWindow，并补全 Windows 无边框装饰参数。

**Tech Stack:** Go 1.x、Wails v3 alpha.74、Vue 3、Vite、Tailwind CSS

## Global Constraints

- 不新增测试文件，遵守仓库测试要求。
- 不修改已安装 Cursor 客户端。
- 不改代理后端启动行为，因为日志证明该路径正常。
- 桌面浮窗固定 240x120、无边框、不可缩放、始终置顶且不显示任务栏项。
- 仓库当前不是 Git 仓库，不执行提交步骤。

---

### Task 1: 修正桌面与浏览器预览判定

**Files:**
- Modify: `frontend/src/services/runtimeAdapter.js:3-16`

**Interfaces:**
- Consumes: Vite 的 `import.meta.env.MODE` 与 `VITE_BROWSER_PREVIEW`
- Produces: `isBrowserPreview: boolean`，桌面构建恒为 false，显式浏览器预览为 true

- [ ] **Step 1: 删除 `window.runtime` 探测**

将 `detectBrowserPreview` 保留为纯构建配置判定：

```js
function detectBrowserPreview() {
  if (import.meta.env?.MODE === "browser-preview") return true;
  return browserPreviewFlag === "true" || browserPreviewFlag === "1";
}
```

- [ ] **Step 2: 确认浏览器预览脚本显式传入 mode 或变量**

运行：`rg -n "browser-preview|VITE_BROWSER_PREVIEW" frontend package.json Taskfile.yml`

如果现有脚本未显式标记，仅调整对应 Vite 预览脚本为 `vite --mode browser-preview`；普通桌面构建保持默认 mode。

- [ ] **Step 3: 构建前端验证语法和模块解析**

运行：`cd frontend && npm run build`

预期：Vite 构建成功，无 `runtimeAdapter` 报错。

### Task 2: 将统计浮窗从通用主布局中隔离

**Files:**
- Modify: `frontend/src/App.vue:1-55`
- Modify: `frontend/src/views/StatsOverlay.vue:64-128`

**Interfaces:**
- Consumes: Vue Router 的 `route.path`
- Produces: `/stats-overlay` 直接渲染路由组件；其他路由继续使用 `MainLayout`

- [ ] **Step 1: App 根组件按浮窗路由选择容器**

在 `App.vue` 中增加 `isStatsOverlay` 计算属性，并将根模板改为：

```vue
<router-view v-if="isStatsOverlay" />
<MainLayout v-else />
```

通用 MessageProvider、Modal、InputModal 仅保留在非浮窗路径，避免 240x120 窗口挂载无关 UI。

- [ ] **Step 2: 保持浮窗内容充满独立窗口**

`StatsOverlay.vue` 根元素继续使用 `h-screen w-screen overflow-hidden` 和 `--wails-draggable: drag`；不加入通用标题栏或页面导航。保留 2x2 指标布局，确保 240x120 内不溢出。

- [ ] **Step 3: 构建前端验证路由组件**

运行：`cd frontend && npm run build`

预期：构建成功，StatsOverlay chunk 被包含。

### Task 3: 强化 Windows 独立浮窗参数

**Files:**
- Modify: `internal/bridge/window.go:286-350`

**Interfaces:**
- Consumes: `WindowService.app` 与 Wails `application.WebviewWindowOptions`
- Produces: `OpenStatsOverlayWindow()` 创建或切换独立桌面窗口

- [ ] **Step 1: 保持独立窗口核心参数**

确认并保留：

```go
Width:         240,
Height:        120,
DisableResize: true,
Frameless:     true,
AlwaysOnTop:   true,
URL:           "/#/stats-overlay",
```

- [ ] **Step 2: 禁用 Windows 无边框装饰**

将 Windows 参数改为：

```go
Windows: application.WindowsWindow{
    HiddenOnTaskbar:                    true,
    DisableFramelessWindowDecorations: true,
},
```

这会移除 Aero 阴影和系统圆角装饰，使窗口呈现桌面挂件形态。

- [ ] **Step 3: 格式化并构建 Go**

运行：`gofmt -w internal/bridge/window.go`

运行：`go build ./...`

预期：Go 构建成功。

### Task 4: 生成 bindings、构建并打包验证

**Files:**
- Generated: `frontend/bindings/**`
- Output: `bin/windows-64-overlay-fixed.zip`

**Interfaces:**
- Consumes: Tasks 1-3 的源码
- Produces: 可供用户测试的 Windows 压缩包

- [ ] **Step 1: 生成 Wails bindings**

运行仓库现有 bindings 生成命令，确保 `OpenStatsOverlayWindow` 可由前端调用。

- [ ] **Step 2: 完整构建 Windows exe**

使用仓库现有 Task/Wails 构建链生成新名称的 exe，避免覆盖正在运行的文件。

- [ ] **Step 3: 检查产物**

确认 exe 存在、文件大小非零，并压缩为 `bin/windows-64-overlay-fixed.zip`。

- [ ] **Step 4: 启动产物并核对日志**

运行新 exe 后检查 `C:\Users\Administrator\.cursor-local-assistant-v2\logs\app.log`，预期包含：

```text
start service completed
代理已自动启动
```

- [ ] **Step 5: GUI 核对**

确认主窗口服务状态来自真实 bridge；点击“浮窗”后主窗口不跳路由，桌面出现独立 240x120 无边框置顶窗口，重复点击可隐藏/显示。
