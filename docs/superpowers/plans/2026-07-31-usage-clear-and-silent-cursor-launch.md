# 统计清除同步与 Cursor 静默启动 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让会话统计与站点消耗使用同一清除边界，并让 Windows 启动 Cursor 时不显示黑色控制台窗口。

**Architecture:** 由运行中的 `forwarder.UsageFileStore` 负责停止 debounce、丢弃 pending events 并原子清空 `usage.json`；Wails bridge 通过 callback 和分层转发接口调用它。前端沿用 `HomeMetricsCard` 的 `refresh` 事件刷新 `StationSpendCard`。Cursor 启动通过 Windows-only `SysProcAttr` 隐藏控制台，其他平台保持空实现。

**Tech Stack:** Go 1.25、Vue 3、Wails v3、Windows `os/exec`/`syscall`、Vite。

## Global Constraints

- 会话统计和站点消耗的展示范围保持现状。
- 不修改已安装的 Cursor 客户端或 bundle。
- 不修改代理服务、模型配置、本地响应缓存和无关设置。
- 不通过 shell 或 `cmd.exe` 启动 Cursor。
- 保留当前工作区中与本需求无关的修改。
- 不提交构建产物。

---

### Task 1: 用量清除必须收口 pending writer

**Files:**
- Modify: `internal/backend/forwarder/usage_store.go`
- Modify: `internal/backend/forwarder/service.go`
- Create: `internal/backend/forwarder/usage_store_reset_test.go`

**Interfaces:**
- Produces `(*UsageFileStore).Reset() error` 的线程安全语义。
- Produces `(*Service).ResetUsageMetrics() error`，只负责调用当前 `usageStore`。

- [ ] **Step 1: 写失败测试，覆盖 debounce 队列清除**

在 `internal/backend/forwarder/usage_store_reset_test.go` 中创建临时 history 目录，调用 `NewUsageFileStore`，通过同包测试构造一个 `usageFileEvent` 写入 pending 队列，然后调用 `Reset()`；立即读取 `usage.json`，再等待 `usageWriteDebounceMs + 200ms`，断言 `recent_events` 为空且 totals 为零。测试同时断言 `debounceTimer == nil`、`len(pendingEvents) == 0`。

```go
func TestUsageFileStoreResetDropsPendingEvents(t *testing.T) {
    root := t.TempDir()
    store := NewUsageFileStore(root)
    if err := store.UpsertEvent(usageFileEvent{
        EventID: "pending-event",
        Kind: usageEventKindProvider,
        At: time.Now().UTC(),
        InputTokens: 10,
        OutputTokens: 5,
    }); err != nil {
        t.Fatal(err)
    }
    if err := store.Reset(); err != nil {
        t.Fatal(err)
    }
    document, err := readUsageFileDocument(filepath.Join(root, usageFileName))
    if err != nil {
        t.Fatal(err)
    }
    if len(document.RecentEvents) != 0 || document.Totals.TotalTokens != 0 {
        t.Fatalf("reset document is not empty: %+v", document)
    }
    time.Sleep(time.Duration(usageWriteDebounceMs+200) * time.Millisecond)
    document, err = readUsageFileDocument(filepath.Join(root, usageFileName))
    if err != nil {
        t.Fatal(err)
    }
    if len(document.RecentEvents) != 0 || document.Totals.TotalTokens != 0 {
        t.Fatalf("pending event returned after reset: %+v", document)
    }
    store.mu.Lock()
    defer store.mu.Unlock()
    if store.debounceTimer != nil || len(store.pendingEvents) != 0 {
        t.Fatalf("reset state not cleared: timer=%v pending=%d", store.debounceTimer, len(store.pendingEvents))
    }
}
```

- [ ] **Step 2: 运行测试确认当前实现失败**

Run: `go test ./internal/backend/forwarder -run TestUsageFileStoreResetDropsPendingEvents -count=1`

Expected: FAIL because the existing `Reset` does not acquire `store.mu`, stop `debounceTimer`, or clear `pendingEvents`.

- [ ] **Step 3: 实现线程安全 reset**

修改 `UsageFileStore.Reset`：先获取 `store.mu`；停止非 nil 的 `debounceTimer` 并置 nil；将 `pendingEvents` 置 nil；创建目录；获取 `store.path + ".lock"`；用现有 `writeJSONFileAtomic` 写入 `usageFileDocument{SchemaVersion: usageFileSchemaVersion, UpdatedAt: time.Now().UTC()}`；释放文件锁后返回。`UpsertEvent` 和 debounce callback 继续复用同一把 `store.mu`。

在 `Service` 上增加：

```go
func (service *Service) ResetUsageMetrics() error {
    if service == nil || service.usageStore == nil {
        return nil
    }
    return service.usageStore.Reset()
}
```

- [ ] **Step 4: 运行测试确认核心行为通过**

Run: `go test ./internal/backend/forwarder -run TestUsageFileStoreResetDropsPendingEvents -count=1`

Expected: PASS。

---

### Task 2: 打通 bridge reset 调用链并保持无 backend 兜底

**Files:**
- Modify: `internal/backend/host.go`
- Modify: `internal/client/service.go`
- Modify: `internal/bridge/proxy.go`
- Modify: `internal/bridge/metrics.go`
- Modify: `internal/app/runner.go`
- Create: `internal/bridge/metrics_reset_test.go`

**Interfaces:**
- `(*backend.Host).ResetUsageMetrics() error`
- `(*client.ProxyService).ResetUsageMetrics() error`
- `(*bridge.ProxyService).ResetUsageMetrics() error`
- `NewMetricsService(includeCacheWrite func() bool, priceRates func() []historymetrics.PriceRate, resetUsage func() error) *MetricsService`
- `(*MetricsService).ResetUsageMetrics() error`

- [ ] **Step 1: 写失败测试，验证 MetricsService callback**

在 `internal/bridge/metrics_reset_test.go` 中构造 callback，断言成功调用一次；再构造返回 sentinel error 的 callback，断言错误透传。

```go
func TestMetricsServiceResetUsageMetricsUsesCallback(t *testing.T) {
    calls := 0
    service := NewMetricsService(nil, nil, func() error { calls++; return nil })
    if err := service.ResetUsageMetrics(); err != nil { t.Fatal(err) }
    if calls != 1 { t.Fatalf("callback calls = %d, want 1", calls) }
}

func TestMetricsServiceResetUsageMetricsPropagatesError(t *testing.T) {
    expected := errors.New("reset failed")
    service := NewMetricsService(nil, nil, func() error { return expected })
    if err := service.ResetUsageMetrics(); !errors.Is(err, expected) { t.Fatalf("error = %v", err) }
}
```

- [ ] **Step 2: 运行测试确认接口尚未存在**

Run: `go test ./internal/bridge -run TestMetricsServiceResetUsageMetrics -count=1`

Expected: FAIL to compile because the constructor has two arguments and `ResetUsageMetrics` is not defined.

- [ ] **Step 3: 实现分层转发**

`MetricsService` 增加 `resetUsage func() error` 字段。构造函数接收第三个 callback；nil callback 时 `ResetUsageMetrics` 直接调用 `historymetrics.ResetUsageFile(appdata.UsageFilePath())` 作为无活动 writer 的兜底，否则调用注入 callback。

在 forwarder 所在链路增加同名方法：

- `backend.Host` 读取 `agentModule.Service` 并调用其 `ResetUsageMetrics`；若 module/service 不存在，调用 `historymetrics.ResetUsageFile(appdata.UsageFilePath())`。
- `client.ProxyService` 在 `backendHost != nil` 时调用 host；否则调用 `historymetrics.ResetUsageFile`。
- `bridge.ProxyService` 转发到 `s.core.ResetUsageMetrics()`。

在 `internal/app/runner.go` 创建 MetricsService 时传入 `proxyService.ResetUsageMetrics`，确保 Wails 清除入口优先走活动 forwarder。

- [ ] **Step 4: 运行 bridge 与相关包测试**

Run: `go test ./internal/bridge ./internal/client ./internal/backend ./internal/backend/forwarder -count=1`

Expected: PASS。

---

### Task 3: 清除成功后立即刷新站点消耗

**Files:**
- Modify: `frontend/src/views/Home.vue`

**Interfaces:**
- `HomeMetricsCard` 保持已有 `refresh` emit。
- `StationSpendCard` 保持已有 `defineExpose({ refresh: load })`。
- `Home.vue` 新增 `stationSpendCard` ref 和 `handleMetricsRefresh()`，只负责父子组件刷新协调。

- [ ] **Step 1: 绑定已有 refresh 事件**

在 `Home.vue` 的 script 中添加：

```js
const stationSpendCard = ref(null);
function handleMetricsRefresh() {
  void stationSpendCard.value?.refresh?.();
}
```

模板中改为：

```vue
<HomeMetricsCard @refresh="handleMetricsRefresh" />
<div class="mt-4">
  <StationSpendCard ref="stationSpendCard" />
</div>
```

不修改站点消耗的查询参数和自动刷新周期。

- [ ] **Step 2: 运行前端构建验证事件绑定**

Run: `npm --prefix frontend run build`

Expected: exit code 0，Vue template 编译成功。

---

### Task 4: Windows Cursor 启动隐藏控制台

**Files:**
- Modify: `internal/bridge/window.go`
- Create: `internal/bridge/cursor_launch_windows.go`
- Create: `internal/bridge/cursor_launch_other.go`
- Create: `internal/bridge/cursor_launch_windows_test.go`

**Interfaces:**
- Windows 和非 Windows 都提供 `configureCursorCommand(*exec.Cmd)`。
- Windows 常量 `cursorCreateNoWindow uint32 = 0x08000000`。

- [ ] **Step 1: 写 Windows 失败测试**

在 `cursor_launch_windows_test.go` 中创建 `exec.Command("Cursor.exe")`，调用 `configureCursorCommand`，断言 `SysProcAttr.HideWindow == true` 且 `CreationFlags&cursorCreateNoWindow != 0`。

```go
func TestConfigureCursorCommandHidesWindowsConsole(t *testing.T) {
    command := exec.Command("Cursor.exe")
    configureCursorCommand(command)
    if command.SysProcAttr == nil { t.Fatal("SysProcAttr is nil") }
    if !command.SysProcAttr.HideWindow { t.Fatal("HideWindow is false") }
    if command.SysProcAttr.CreationFlags&cursorCreateNoWindow == 0 { t.Fatalf("CreationFlags = %#x", command.SysProcAttr.CreationFlags) }
}
```

- [ ] **Step 2: 运行测试确认 helper 尚未存在**

Run: `go test ./internal/bridge -run TestConfigureCursorCommandHidesWindowsConsole -count=1`

Expected: FAIL to compile because `configureCursorCommand` and `cursorCreateNoWindow` are not defined.

- [ ] **Step 3: 实现平台隔离 helper**

Windows 文件使用 `syscall.SysProcAttr`：

```go
const cursorCreateNoWindow uint32 = 0x08000000

func configureCursorCommand(command *exec.Cmd) {
    command.SysProcAttr = &syscall.SysProcAttr{
        HideWindow:    true,
        CreationFlags: cursorCreateNoWindow,
    }
}
```

非 Windows 文件提供空实现。`LaunchCursor` 在 `cmd.Start()` 前调用 `configureCursorCommand(cmd)`；不改变路径解析、workspace 参数和错误信息。

- [ ] **Step 4: 运行 Windows helper 测试**

Run: `go test ./internal/bridge -run TestConfigureCursorCommandHidesWindowsConsole -count=1`

Expected: PASS。

---

### Task 5: 全量验证和差异检查

**Files:**
- Verify: all files modified by Tasks 1-4

- [ ] **Step 1: 运行完整 Go 测试**

Run: `go test ./...`

Expected: exit code 0，所有测试通过。

- [ ] **Step 2: 运行 Go 构建**

Run: `go build ./...`

Expected: exit code 0。

- [ ] **Step 3: 运行前端 production build**

Run: `npm --prefix frontend run build`

Expected: exit code 0，不提交 `frontend/dist` 构建产物。

- [ ] **Step 4: 检查差异格式和范围**

Run: `git diff --check`，并检查 `git diff --stat` 与 `git status --short`。

Expected: 无空白错误；只包含本需求代码/测试/设计计划文档及原有无关脏改动，不回退任何无关文件。

- [ ] **Step 5: 手工验收行为**

启动应用后，在首页点击“清空”：会话统计和站点消耗同时立即刷新为空；等待至少 `usageWriteDebounceMs` 后再次刷新仍为空。点击“启动 Cursor”：Cursor GUI 正常打开，Windows 不出现额外黑色控制台窗口。