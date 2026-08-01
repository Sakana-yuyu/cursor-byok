# 统计清除同步与 Cursor 静默启动设计

## 背景

首页的“会话统计”和“站点消耗”都读取统一的 `usage.json`，但清除入口目前直接重置磁盘文件，运行中的 forwarder 仍可能保留 debounce 队列并在稍后写回旧事件。清除后站点消耗卡也只按定时器刷新，不能立即与会话统计同步归零。

Windows 的 Cursor 启动入口直接调用 `exec.Command`，当解析到控制台型启动入口或子进程继承控制台时可能出现黑色窗口。

## 已确认范围

- 会话统计和站点消耗的展示范围保持现状，不改时间范围口径。
- 清除会话统计时，站点消耗必须同步清除并立即刷新。
- 清除后不能因内存中的延迟写入队列而恢复旧数据。
- 启动 Cursor 时只隐藏控制台窗口，Cursor 正常 GUI 窗口仍需显示。
- 不修改已安装的 Cursor 客户端，不改无关的代理、模型、缓存和设置逻辑。

## 架构

### 1. 统计清除由运行中的 UsageFileStore 负责

清除请求从 Wails `MetricsService` 进入现有 forwarder：

```text
HomeMetricsCard
  -> MetricsService.ResetUsageMetrics
  -> bridge.ProxyService.ResetUsageMetrics
  -> client.ProxyService.ResetUsageMetrics
  -> backend.Host.ResetUsageMetrics
  -> forwarder.Service.ResetUsageMetrics
  -> UsageFileStore.Reset
```

`UsageFileStore.Reset` 在自身互斥锁内停止并清空 debounce 状态，再通过既有 usage 文件锁原子写入空文档。这样磁盘数据、内存待写入事件和后续读取结果具有同一清除边界。若 backend 尚未装配 forwarder，则仅使用无活动 writer 的文件重置兜底。

`MetricsService` 通过 runner 注入 reset callback，避免 bridge 层直接依赖 forwarder 内部类型；各层只暴露一个职责明确的 `ResetUsageMetrics() error` 转发接口。

### 2. 前端使用现有组件事件完成即时刷新

`HomeMetricsCard` 清除成功后继续发出已有的 `refresh` 事件。`Home.vue` 接收该事件并调用 `StationSpendCard` 暴露的 `refresh()`，不增加全局事件、localStorage 标记或新的状态 store。

```text
清除成功
  -> HomeMetricsCard.loadEvents()
  -> emit("refresh")
  -> Home.vue
  -> StationSpendCard.refresh()
```

展示范围和两个组件的原有自动刷新周期不变。

### 3. Windows 启动配置平台隔离

`LaunchCursor` 保留现有路径解析和 workspace 参数，只在 Windows 平台通过独立文件设置：

- `syscall.SysProcAttr.HideWindow = true`
- `CREATE_NO_WINDOW` 创建标志

非 Windows 使用空实现，避免在跨平台构建中引用 Windows-only 字段。启动不经过 `cmd.exe` 或 shell。

## 错误处理

- 清除失败原样返回 Wails 错误，前端保持原有错误提示。
- reset callback 未配置时使用安全的文件重置兜底。
- 取消 debounce 定时器后将指针置空，避免后续回调再次刷新旧队列。
- Cursor 路径无效或进程启动失败时保持现有错误信息和路径状态。

## 测试与验收

- Go 单元测试验证：pending usage 事件在 reset 后被丢弃，等待 debounce 时间后仍为空。
- Go 单元测试验证：`MetricsService.ResetUsageMetrics` 调用注入 callback 并透传错误。
- Windows 单元测试验证：Cursor 命令包含 `HideWindow` 和 `CREATE_NO_WINDOW`。
- 前端 production build 验证组件事件、binding 和 Vue 模板编译通过。
- `go test ./...`、`go build ./...`、`npm --prefix frontend run build`、`git diff --check` 通过。
- 手工验证清除后两张首页卡片立即归零，等待 2 秒以上不回写；Windows 启动 Cursor 不出现黑色控制台窗口。