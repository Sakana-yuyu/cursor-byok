# 浮窗控制与 Cursor 启动实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 完善统计浮窗控制、关闭策略、偏好与坐标恢复，并恢复 Windows Cursor 自动检测、手动路径和一键启动能力。

**Architecture:** 前端继续负责浮窗偏好和 Cursor 路径的持久化，原生 WindowService 提供运行时关闭策略、浮窗生命周期、坐标恢复和 Cursor 进程启动桥接。主窗口托盘逻辑集中在 runner，前端设置变更通过 binding 同步到原生服务，避免原生关闭事件直接读取 localStorage。

**Tech Stack:** Vue 3、Wails v3 bindings、Go、Windows `exec`/注册表检测、localStorage、现有 i18n 扫描构建。

## Global Constraints

- 不改变代理服务、模型配置、统计数据来源和既有贴边动画的行为。
- 不新增测试文件，使用 `npm --prefix frontend run build`、`go build ./...`、静态检查和手工状态场景验证。
- 浮窗旧偏好缺失字段时使用兼容默认值：关闭策略为 `tray`，浮窗显示为关闭，Cursor 手动路径为空。
- UI 文本使用中文源文本并通过现有静态 i18n 扫描生成目录和翻译文件。
- 不提交构建产物。

---

### Task 1: 统一浮窗偏好和控制动作

**Files:**
- Modify: `frontend/src/state/appState.js`
- Modify: `frontend/src/services/clientApi.js`
- Modify: `frontend/src/views/StatsOverlay.vue`
- Modify: `frontend/src/components/SettingsDrawer.vue`

**Interfaces:**
- `statsOverlayPreferences` 增加 `closeAction: "tray" | "quit"`，保留 `style`、`alwaysOnTop`、`visible`、`x`、`y`、`snapCollapse`、`dockLocked`。
- 前端调用 `setMainWindowCloseAction(action)` 同步主窗口关闭策略。
- 前端提供 `hideStatsOverlay()` 和 `closeApplication()` 两个明确动作，分别更新 `visible` 后调用原生 binding。

- [ ] **Step 1: 扩展偏好规范化和持久化字段**
  在 `normalizeStatsOverlayPreferences` 中将 `closeAction` 限制为 `tray` 或 `quit`，默认 `tray`。保留合法的 `x=0`、`y=0`，只把非有限值归一化为 `null`。设置变更时，`closeAction` 调用原生同步方法，其他字段继续走现有浮窗同步逻辑。

- [ ] **Step 2: 增加浮窗生命周期 API**
  在 `clientApi.js` 暴露 `SetMainWindowCloseAction`、`CloseApplication` 和 `OpenStatsOverlayWindow` binding，并在 `appState.js` 集中实现：隐藏浮窗将 `visible=false` 后关闭窗口；显示浮窗恢复保存的坐标和样式；关闭应用调用 `CloseApplication`。

- [ ] **Step 3: 改造浮窗控制区**
  在 `StatsOverlay.vue` 移除标题和状态点占位，主体内容直接布局。增加右上角图标按钮：贴边收缩、隐藏浮窗、关闭应用；按钮必须使用 `--wails-draggable: no-drag`、tooltip 和 `@click.stop`，不干扰拖动与贴边逻辑。关闭应用按钮按 `closeAction` 执行。

- [ ] **Step 4: 补齐设置面板偏好项**
  在 `SettingsDrawer.vue` 增加关闭策略选择、贴边收缩开关和浮窗锁定状态的完整编辑项。切换后调用统一偏好 API，并在组件挂载时从规范化偏好恢复。

- [ ] **Step 5: 扫描并核对 i18n 生成文件**
  运行 `npm --prefix frontend run build`，保留扫描器对 `catalog.json` 和四个语言文件的真实更新；确认所有 locale key 与 catalog 一致。

### Task 2: 原生关闭策略和托盘浮窗入口

**Files:**
- Modify: `internal/bridge/window.go`
- Modify: `internal/app/runner.go`
- Modify: `frontend/src/services/clientApi.js`

**Interfaces:**
- `WindowService.SetMainWindowCloseAction(action string)` 只接受 `tray`/`quit`，未知值回退 `tray`。
- `WindowService.GetMainWindowCloseAction() string` 返回当前运行时关闭策略。
- `WindowService.CloseApplication()` 调用 `app.Quit()`，由应用的 `OnShutdown` 执行统一服务清理。
- `WindowService.OpenStatsOverlayWindow(x, y int, hasPosition bool)` 显示浮窗并能区分未设置坐标与合法 `(0, 0)`。

- [ ] **Step 1: 保存原生运行时关闭策略**
  在 `WindowService` 增加受 mutex 保护的关闭策略字段，默认 `tray`。实现 `SetMainWindowCloseAction`、`GetMainWindowCloseAction` 和 `CloseApplication`；退出只调用 `app.Quit()`，复用 runner 已注册的 `OnShutdown` 清理流程。

- [ ] **Step 2: 修改主窗口关闭 hook**
  在 `runner.go` 的 `WindowClosing` hook 中读取 WindowService 策略：`tray` 时隐藏并取消关闭事件，`quit` 时允许统一退出流程。确保托盘退出仍然无条件执行完整清理。

- [ ] **Step 3: 增加统计浮窗托盘菜单项**
  在系统托盘菜单增加“显示统计浮窗”，调用 WindowService 的显示方法；显示后恢复前端保存的样式、置顶和坐标。菜单刷新时同步显示项状态，避免浮窗已显示仍重复创建。

- [ ] **Step 4: 修复坐标恢复边界**
  修改 `OpenStatsOverlayWindow(x, y, hasPosition)` binding 和后端定位逻辑，使用显式 `hasPosition` 区分 null 与 `(0, 0)`。对恢复位置按当前屏幕工作区和窗口尺寸做边界钳制，保留多屏负坐标。

- [ ] **Step 5: 运行 Go 格式化与构建**
  运行 `gofmt` 处理修改的 Go 文件，再执行 `go build ./...`；检查 binding 方法名与前端调用一致。

### Task 3: Cursor 自动检测、人工路径和一键启动

**Files:**
- Modify: `internal/bridge/window.go`
- Modify: `frontend/src/services/clientApi.js`
- Modify: `frontend/src/state/appState.js` 或新增 `frontend/src/utils/cursorPath.js`
- Modify: `frontend/src/views/Home.vue`
- Modify: `frontend/src/components/SettingsDrawer.vue`

**Interfaces:**
- `DetectCursorPath(manualPath string) string`：人工路径有效时优先返回人工路径，否则执行自动检测。
- `LaunchCursor(workspaceDir string, manualPath string) error`：使用统一解析路径启动 Cursor。
- 前端 Cursor 偏好键保存 `manualPath`，清空后回退自动检测。

- [ ] **Step 1: 扩展 Go 路径解析**
  将 `DetectCursorPath(manualPath string) string` 作为统一解析入口，增加路径清理和可执行文件校验；Windows 依次检查手动路径、`where cursor`、`LOCALAPPDATA`/`PROGRAMFILES`/`PROGRAMFILES(X86)` 的常见 Cursor.exe 路径，以及卸载注册表中的安装位置。macOS/Linux 保留现有检测作为后备。

- [ ] **Step 2: 扩展启动 binding 参数**
  将 `LaunchCursor(workspaceDir string, manualPath string) error` 接入统一解析结果；启动失败返回包含实际路径的可读错误，不清空用户输入。更新 `clientApi.js` 的 desktop/mock 调用签名。

- [ ] **Step 3: 恢复首页启动入口**
  `Home.vue` 不再以 `cursorDetected` 控制按钮是否渲染；始终显示“一键启动 Cursor”。点击时使用保存路径优先的解析结果，失败时提示用户去设置填写路径。

- [ ] **Step 4: 增加设置中的 Cursor 路径兜底**
  设置面板显示当前自动检测路径和手动路径输入，提供自动检测、保存、清空操作。保存后立即刷新首页可用状态，手动路径为空时恢复自动检测。

- [ ] **Step 5: 扫描 i18n 并验证路径场景**
  使用有效路径、空路径、无效路径和检测失败四个场景做手工验证；运行前端构建并确认所有 locale JSON 仍与 catalog 对齐。

### Task 4: 集成验证和交付构建

**Files:**
- Verify: `frontend/src/views/StatsOverlay.vue`
- Verify: `frontend/src/components/SettingsDrawer.vue`
- Verify: `frontend/src/views/Home.vue`
- Verify: `internal/bridge/window.go`
- Verify: `internal/app/runner.go`

- [ ] **Step 1: 静态检查用户流程**
  核对浮窗三种样式无标题占位，控制按钮不参与拖动，隐藏/关闭动作更新 `visible`，托盘菜单可重新显示浮窗，主窗口关闭策略与设置一致。

- [ ] **Step 2: 构建验证**
  运行 `npm --prefix frontend run build`、`go build ./...` 和 `git diff --check`。如果构建脚本因旧 zip 被占用失败，保留成功生成的新 exe，并明确记录输出路径。

- [ ] **Step 3: 生成独立 Windows 产物**
  使用独立文件名生成 `bin/windows-64-overlay-controls.exe`，不覆盖正在运行的旧 exe，不提交二进制产物。
