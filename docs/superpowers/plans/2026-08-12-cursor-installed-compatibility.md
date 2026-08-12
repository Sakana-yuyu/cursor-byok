# Cursor Installed Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让本地转发器只读识别实际安装版 Cursor 的能力，并在浏览器模式下可靠区分 IDE 内置浏览器 MCP 与既有坐标型 Playwright MCP。

**Architecture:** 安装版扫描仅生成脱敏的本地诊断报告，不参与运行时执行。ComputerUse 继续经既有执行桥处理；浏览器模式先由已连接 MCP 的真实工具描述符解析 profile，再分派到 IDE 浏览器或坐标浏览器适配器，最终沿用既有 pending、合成结果、watchdog 与取消收口。

**Tech Stack:** Go 1.25、Protocol Buffers、MCP Go SDK、Windows、现有 `internal/computeruse` 与 `internal/backend/forwarder`。

## Global Constraints

- 不修改 `D:\cursor` 或任何已安装 Cursor 文件、签名、配置、登录数据和运行进程。
- 不在扫描报告、日志、镜像 JSONL、测试输出或 Git 中保存完整安装路径、用户目录、Cookie、Token、URL、请求/响应正文、MCP 参数、DOM/ref/viewId。
- `computerUse.mode=desktop`、当前 Playwright 坐标型 MCP 行为、现有 pending/watchdog、单任务取消与 Stop All 不改变。
- 未由真实 Cursor 发送的 `force_background_subagent`、`subagent_await`、`computer_use`、allowlist precheck oneof 保持 `not_observed`，不伪造协议消息。
- 使用 `apply_patch` 做文件编辑；每个任务只暂存本任务列出的文件，绝不暂存 `.playwright-cli/`、`frontend/.playwright-cli/`、`output/`。

---

### Task 1: 安装版 Cursor 只读能力扫描

**Files:**
- Create: `internal/cursorcapabilities/scanner.go`
- Create: `internal/cursorcapabilities/scanner_test.go`
- Create: `cmd/cursor-capability-scan/main.go`

**Interfaces:**
- Produces: `cursorcapabilities.Scan(root string) (Report, error)`。
- `Report` 包含 `ScannerVersion`、`CursorVersion`、`InstallRootHash`、`Extensions`、`ProtocolMarkers`、`BrowserToolMarkers`、`Warnings`；不含输入 root 或文件内容。
- `Scan` 只读取 `Cursor.exe` 的版本元数据、目标扩展的 `package.json` 和 `dist` bundle 中的固定 marker。

- [ ] **Step 1: 写入失败测试，定义报告不会暴露路径且可从最小假安装目录识别能力。**

```go
func TestScanReportsMarkersWithoutExposingRoot(t *testing.T) {
    root := t.TempDir()
    writeFixtureCursorInstall(t, root, map[string]string{
        "cursor-browser-automation": `cursor-ide-browser browser_tabs browser_lock`,
        "cursor-agent-exec": `computer_use force_background_subagent subagent_await`,
    })

    report, err := Scan(root)
    if err != nil { t.Fatal(err) }
    if report.InstallRootHash == "" || strings.Contains(mustJSON(t, report), root) {
        t.Fatal("report must hash, not expose, the installation root")
    }
    if !contains(report.BrowserToolMarkers, "browser_tabs") || !contains(report.ProtocolMarkers, "subagent_await") {
        t.Fatal("expected installed capability markers")
    }
}
```

- [ ] **Step 2: 运行失败测试，确认失败原因是 `Scan` 尚不存在。**

Run: `go test ./internal/cursorcapabilities -run TestScanReportsMarkersWithoutExposingRoot -count=1`

Expected: FAIL，错误指出 package 或 `Scan` 尚不存在。

- [ ] **Step 3: 写入最小扫描器与命令入口。**

```go
type Report struct {
    ScannerVersion string `json:"scannerVersion"`
    CursorVersion string `json:"cursorVersion,omitempty"`
    InstallRootHash string `json:"installRootHash"`
    Extensions []Extension `json:"extensions"`
    ProtocolMarkers []string `json:"protocolMarkers"`
    BrowserToolMarkers []string `json:"browserToolMarkers"`
    Warnings []string `json:"warnings"`
}

func Scan(root string) (Report, error)
```

只查固定扩展目录和固定 marker，排序去重；命令通过 `--root` 接受显式根目录，stdout 输出 JSON report。

- [ ] **Step 4: 运行定向测试与命令冒烟检查。**

Run: `go test ./internal/cursorcapabilities -count=1`

Expected: PASS。

Run: `go run ./cmd/cursor-capability-scan --root D:\cursor`

Expected: 退出码 0；输出含版本和 marker，不含 `D:\cursor` 字符串。

- [ ] **Step 5: 检查并提交。**

```powershell
git diff --check
git add -- internal/cursorcapabilities/scanner.go internal/cursorcapabilities/scanner_test.go cmd/cursor-capability-scan/main.go
git diff --cached --check
git commit -m "feat(compat): scan installed cursor capabilities"
```

### Task 2: MCP 浏览器 profile 解析与 IDE 内置浏览器适配器

**Files:**
- Modify: `internal/computeruse/browser_mcp.go`
- Modify: `internal/computeruse/browser_mcp_test.go`
- Create: `internal/computeruse/ide_browser_mcp.go`
- Create: `internal/computeruse/ide_browser_mcp_test.go`
- Modify: `internal/backend/forwarder/computeruse_bridge.go`
- Create: `internal/backend/forwarder/computeruse_bridge_test.go`

**Interfaces:**
- Produces: `BrowserMCPResolution { Identifier string; Profile BrowserMCPProfile; ToolNames []string }`。
- Produces: `MCPCaller.ResolveBrowserServer(scope string) (BrowserMCPResolution, error)`。
- Produces: `NewIDEBrowserExecutor(caller MCPCaller, scope string, startURL string, resolution BrowserMCPResolution) Executor`。
- `BrowserMCPProfile` 仅允许 `cursor_ide_browser` 与 `coordinate_browser`。

- [ ] **Step 1: 写入失败测试，定义 profile 只能依赖已连接 runtime descriptor。**

```go
func TestResolveBrowserServerPrefersCursorIDEProfile(t *testing.T) {
    caller := newProfileFakeCaller("cursor-ide-browser", []string{
        "browser_tabs", "browser_lock", "browser_snapshot",
        "browser_mouse_click_xy", "browser_take_screenshot",
    })
    got, err := caller.ResolveBrowserServer("user")
    if err != nil { t.Fatal(err) }
    if got.Profile != CursorIDEBrowserProfile { t.Fatalf("profile = %s", got.Profile) }
}
```

- [ ] **Step 2: 运行失败测试，确认缺少 profile 解析 API。**

Run: `go test ./internal/computeruse ./internal/backend/forwarder -run TestResolveBrowserServerPrefersCursorIDEProfile -count=1`

Expected: FAIL，错误指出 `ResolveBrowserServer` 或 profile 类型不存在。

- [ ] **Step 3: 实现 profile 解析与两种适配器分流。**

```go
type BrowserMCPProfile string
const (
    CursorIDEBrowserProfile BrowserMCPProfile = "cursor_ide_browser"
    CoordinateBrowserProfile BrowserMCPProfile = "coordinate_browser"
)

func ResolveBrowserProfile(identifier string, toolNames []string) (BrowserMCPProfile, error)
```

`cursor-ide-browser` 必须有 `browser_tabs`、`browser_lock`、`browser_snapshot`、`browser_take_screenshot` 与 `browser_mouse_click_xy`；坐标型 profile 必须具备现有适配器调用的点击、键盘、等待和截图工具。不能按名称模糊匹配。

IDE 适配器在有初始 URL 时先导航；执行前列标签并锁定，坐标点击前获取新截图，结束时以 defer 解锁。`drag`、`mouse_down`、`mouse_up` 等不能稳定映射的动作返回 `ide_browser_action_unmappable`；不得猜测 ref、改用桌面鼠标或切换标签。

- [ ] **Step 4: 写入并运行行为测试。**

```go
func TestIDEBrowserExecutorLocksClicksAndUnlocks(t *testing.T) {
    caller := newIDEBrowserFakeCaller()
    result := NewIDEBrowserExecutor(caller, "user", "about:blank", caller.resolution()).Execute([]Action{{Type: "click", X: 10, Y: 20}})
    if !result.Success { t.Fatal(result.Error) }
    assertToolOrder(t, caller.calls(), []string{
        "browser_tabs", "browser_lock", "browser_take_screenshot",
        "browser_mouse_click_xy", "browser_take_screenshot", "browser_lock",
    })
}
```

Run: `go test ./internal/computeruse ./internal/backend/forwarder -count=1`

Expected: PASS。

- [ ] **Step 5: 检查并提交。**

```powershell
git diff --check
git add -- internal/computeruse/browser_mcp.go internal/computeruse/browser_mcp_test.go internal/computeruse/ide_browser_mcp.go internal/computeruse/ide_browser_mcp_test.go internal/backend/forwarder/computeruse_bridge.go internal/backend/forwarder/computeruse_bridge_test.go
git diff --cached --check
git commit -m "feat(browser): adapt cursor ide browser mcp"
```

### Task 3: 子代理与审批生命周期状态投影

**Files:**
- Create: `internal/backend/agent/bridge/exec/lifecycle_state.go`
- Create: `internal/backend/agent/bridge/exec/lifecycle_state_test.go`
- Modify: `internal/backend/agent/bridge/exec/bridge.go`
- Modify: `spec/changes/backend-capability-ui-discovery/tasks.md`
- Modify: `spec/changes/backend-capability-ui-discovery/verify.md`

**Interfaces:**
- Produces: `LifecycleState { Kind string; Phase string; Terminal bool }`。
- Produces: `ClassifyExecLifecycle(msg *agentv1.ExecClientMessage, pending runtimecore.PendingExec) LifecycleState`。
- 允许 phase：`background_accepted`、`background_not_found`、`await_still_running`、`await_complete`、`await_not_found`、`await_error`、`allowlist_allowed`、`allowlist_denied`、`not_observed`。

- [ ] **Step 1: 写入失败测试，锁定等待与审批的终态语义。**

```go
func TestClassifyExecLifecycleKeepsAwaitStillRunningOpen(t *testing.T) {
    state := ClassifyExecLifecycle(stillRunningMessage(), runtimecore.PendingExec{ExecKind: "subagent_await"})
    if state.Phase != "await_still_running" || state.Terminal {
        t.Fatalf("unexpected state: %#v", state)
    }
}

func TestClassifyExecLifecycleClosesDeniedAllowlist(t *testing.T) {
    state := ClassifyExecLifecycle(deniedShellPrecheck(), runtimecore.PendingExec{ExecKind: "shell"})
    if state.Phase != "allowlist_denied" || !state.Terminal {
        t.Fatalf("unexpected state: %#v", state)
    }
}
```

- [ ] **Step 2: 运行失败测试，确认状态投影 API 缺失。**

Run: `go test ./internal/backend/agent/bridge/exec -run TestClassifyExecLifecycle -count=1`

Expected: FAIL，错误指出 `ClassifyExecLifecycle` 未定义。

- [ ] **Step 3: 实现纯分类 helper，并在既有结果处理处使用其 terminal 判断。**

```go
type LifecycleState struct {
    Kind string
    Phase string
    Terminal bool
}

func ClassifyExecLifecycle(msg *agentv1.ExecClientMessage, pending runtimecore.PendingExec) LifecycleState
```

分类器不得读取或返回 agent ID、tool call ID、错误正文、参数或转录路径。`ApplyExecClientMessage` 保留现有 payload 与 ToolCall 构造，仅用分类结果消除重复的终态判断。

- [ ] **Step 4: 运行执行桥与目录覆盖测试。**

Run: `go test ./internal/backend/agent/bridge/exec ./internal/backend/forwarder -count=1`

Expected: PASS，既有 `SubagentAwait`、allowlist 与工具目录测试不回归。

- [ ] **Step 5: 更新真实抓包台账，检查并提交。**

```powershell
git diff --check
git add -- internal/backend/agent/bridge/exec/lifecycle_state.go internal/backend/agent/bridge/exec/lifecycle_state_test.go internal/backend/agent/bridge/exec/bridge.go spec/changes/backend-capability-ui-discovery/tasks.md spec/changes/backend-capability-ui-discovery/verify.md
git diff --cached --check
git commit -m "feat(protocol): project exec lifecycle states"
```

### Task 4: 全量定向验证与真实运行时边界复核

**Files:**
- Modify: `spec/changes/backend-capability-ui-discovery/verify.md`

**Interfaces:**
- Consumes: Tasks 1-3 的已提交实现。
- Produces: 区分静态安装版扫描、单元协议验证、运行时 MCP profile 验证与真实 Cursor E2E 的台账结论。

- [ ] **Step 1: 运行全量定向检查。**

Run: `go test ./internal/cursorcapabilities ./internal/computeruse ./internal/backend/agent/bridge/exec ./internal/backend/forwarder ./internal/mitm ./cmd/isolated-cursor-e2e -count=1`

Expected: PASS。

Run: `go vet ./internal/cursorcapabilities ./internal/computeruse ./internal/backend/agent/bridge/exec ./internal/backend/forwarder ./internal/mitm ./cmd/isolated-cursor-e2e`

Expected: 退出码 0。

Run: `go build ./cmd/cursor-capability-scan ./cmd/isolated-cursor-e2e`

Expected: 退出码 0。

- [ ] **Step 2: 对实际安装版运行只读扫描。**

Run: `go run ./cmd/cursor-capability-scan --root D:\cursor`

Expected: 退出码 0；报告声明 `3.15.6` 和已知 marker，且不含安装根路径。

- [ ] **Step 3: 记录边界与提交。**

在 `verify.md` 记录：安装版扫描证明声明能力；单元测试证明本地 profile 和生命周期映射；只有用户实际触发时，`force_background_subagent_*`、`subagent_await_*`、`computer_use_*` 和 allowlist precheck 才能升级为真实 Cursor E2E。

```powershell
git diff --check
git add -- spec/changes/backend-capability-ui-discovery/verify.md
git diff --cached --check
git commit -m "docs(verify): record compatibility adapter evidence"
```

## Plan Self-Review

- 设计中的安装版报告、运行时 profile、IDE/坐标双适配器、生命周期状态投影和兼容边界均有对应任务。
- 每个生产变更任务均按 Red -> Green -> 验证 -> 独立提交执行；Task 4 只聚合已实现内容的验证证据。
- 计划中不使用空白占位项；接口名称与 `design.md` 一致，且静态扫描与真实 E2E 证据明确分离。
