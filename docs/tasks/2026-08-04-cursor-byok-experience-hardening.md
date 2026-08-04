# Cursor-byok 体验加固任务清单

## 目标

在阶段一「可靠性优先」的基础上，继续两件事：让排查证据在界面里可定位（诊断工作台），让共享交互组件可键盘操作、可被读屏识别（交互与无障碍）。

每个阶段独立验收、单独本地提交，保留任意提交作为回滚点。

## 约束

- 只做本地提交，commit message 用中文。未获授权不 push。
- 不覆盖部署产物 `D:\Cursor助手\Cursor助手.exe`。
- 工作区内 5 个非本任务改动必须保持隔离，不得混入提交：`docs/promotion/linuxdo-post.md`、`frontend/src/components/settings/categories/PromptSettings.vue`、`frontend/src/components/ui/ActionMenu.vue`（阶段三会正式改，届时单独说明）、`internal/backend/forwarder/commit_message.go`、未跟踪的 `internal/backend/forwarder/commit_language_test.go`。
- 新增 bridge 方法后必须重新生成绑定：`wails3 generate bindings`（`frontend/bindings` 被 gitignore，不进提交）。
- `go test -race` 在本环境不可用（`-race requires cgo`），并发相关测试在无 race detector 下运行，需在验收记录里说明。
- `gofmt -l` 因 `core.autocrlf=true` 会无条件列出文件；判断真实格式违规前先做 LF 归一化比对。仓库内已存在任务前的未格式化文件，不批量改写。

## 阶段一：可靠性优先（已完成，`c6eb836`）

作为后续阶段的基线记录，不再改动。

- [x] Task 1.1：`internal/backend/forwarder/debug_purge.go` 用世代号 + 读写锁协调 debug 落盘与清理。落盘 worker 持读锁并携带入队时的世代号，清理持写锁并递增世代号，世代号落后的事件被丢弃。修复「清理后旧事件把目录重建回来」导致的清理无效。四个删除入口（按会话删日志、清理全部、删会话、清空历史）全部走闸门。
- [x] Task 1.2：`historyDebugDirsIn` 成为唯一的 debug 目录遍历实现，统计与清理共用同一口径，覆盖 UUID 会话目录、非 UUID 会话目录与 `_debug/orphan`。此前两者各自只认 UUID 目录，孤儿日志既统计不到也清理不掉。
- [x] Task 1.3：`serviceRunning` 改为「backend 与代理同时在跑」，半启动态由 `servicePartiallyRunning` 表达，状态点三色，半启动态可关闭。此前它只等于 `proxyRunning`，backend 挂掉时首页仍显示绿灯。
- [x] Task 1.4：失败不再伪装成成功。占用统计失败保留上次有效值并单独暴露错误态；代理修复依据 `settingsApplied` 给结论；日志导出空路径不再报成功；浏览器预览 mock 改为可变数据，启停/删除/清理真实生效。
- [x] Task 1.5：Home、历史页、请求明细的错误横幅统一带就地重试。
- [x] 验收：`go build ./...`、`go vet ./internal/...`、`go test ./internal/...` 通过；`npm --prefix frontend run build` 通过；四语言各 1199 keys、0 空值。

## 阶段二：诊断工作台

### 问题

- `internal/bridge/window.go:701` 的 `ExportLogs` 只打包 `client.ResolveLogsRootPath()`，**不含** `history/<conversationId>/debug/*.jsonl`。用户按提示「导出日志 ZIP 反馈」时，恰好缺少 bidi/provider/runsse 这些最关键的原始记录。
- 没有任何 bridge 方法能读取单个会话的 debug 文件。阶段一给了「复制会话 ID / 请求 ID」，但复制完用户仍然只能自己去文件系统里翻目录、grep。
- `frontend/src/views/Diagnostics.vue` 只有 53 行，做的是模型协议诊断（适配器 issue 扫描 + 一键修复），与会话/请求级排查无关。路由 `/diagnostics` 标题「模型协议诊断」。

### 任务

- [ ] Task 2.1：后端按会话导出证据包。新增 `ExportSessionDebugBundle(sessionID string) (string, error)`，打包 `history/<id>/` 下的 `state.json`、`context.json` 与 `debug/*`。沿用 `historySessionIDPattern` 的 UUID 校验拒绝路径穿越，路径口径复用阶段一的 `historyDebugDirsIn`。只读遍历，不进 `forwarder.PurgeDebugLogs` 闸门（导出不该阻塞落盘）。目标目录为空时返回明确错误而不是空路径，沿用阶段一的诚实失败约定。
- [ ] Task 2.2：后端 debug 文件清单与尾部读取。`ListSessionDebugFiles(sessionID)` 返回文件名、大小、mtime；`ReadSessionDebugTail(sessionID, filename string, maxBytes int64)` 只读文件尾部（最新的部分最可能含错误，与 `rotateIfNeeded` 保留尾部的策略一致）。`filename` 必须按白名单校验（`bidi.raw.jsonl`、`bidi.decoded.jsonl`、`runtime.jsonl`、`runsse.jsonl`、`provider.jsonl`），拒绝任意路径拼接。
- [ ] Task 2.3：诊断工作台承接会话排查。`Diagnostics.vue` 拆成两块（模型协议诊断 / 会话证据），或改为 tab 结构，保留现有协议诊断能力不回退。历史页在「复制会话 ID」旁增加「在诊断中打开」入口，带 sessionID 跳转。
- [ ] Task 2.4：debug 文件查看器。列出文件与大小、按 requestID 过滤、查看尾部内容、导出单会话证据包。错误横幅带就地重试（沿用阶段一样式）。浏览器预览 mock 必须是可变的真实行为，不能假成功（沿用 Task 1.4 的约定）。
- [ ] Task 2.5：i18n 扫描补齐 en-US / ja-JP / ru-RU，全量验证。

### 验收标准

- 导出的 ZIP 内确实包含会话的 debug jsonl；空会话导出报明确错误。
- 越界 sessionID 与非白名单 filename 被拒绝，有测试覆盖。
- 协议诊断原有能力无回退。
- 四语言 key 数一致、0 空值。

## 阶段三：交互与无障碍

### 问题

两个共享组件缺基础语义，改动面覆盖全部调用方，所以单独成一个阶段。

`frontend/src/components/ui/ActionMenu.vue`（63 行）：
- 触发器是裸 `<div @click="toggle">`（:49），不可 Tab 聚焦、不能用键盘打开。
- 无 `aria-haspopup`、`aria-expanded`、`aria-controls`，读屏无法感知这是一个可展开菜单。
- 无 Escape 关闭、无方向键导航。菜单容器有 `role="menu"`（:59），但菜单项由 slot 提供，没有 `role="menuitem"` 约束。

`frontend/src/components/ui/Modal.vue`（124 行）：
- 无 `role="dialog"`、`aria-modal`、`aria-labelledby`。
- 无焦点陷阱，焦点仍可 Tab 到被遮罩的背景内容；Teleport 到 body 后背景对读屏依然可达。
- 无 Escape 关闭；关闭后不恢复焦点到触发元素。
- 用 `v-show` 而非 `v-if`，焦点相关逻辑必须 watch `visible` 而不是依赖挂载时机。

### 任务

- [ ] Task 3.1：Modal 语义与焦点管理。补 `role="dialog"` / `aria-modal="true"` / `aria-labelledby`（指向标题）；Escape 触发 cancel；打开时焦点移入对话框；关闭后恢复到打开前的焦点元素；Tab 在对话框内循环。保留现有遮罩 `@click.self` 关闭与过渡动画。
- [ ] Task 3.2：ActionMenu 键盘可达。触发器改为可聚焦元素并补 `aria-haspopup` / `aria-expanded` / `aria-controls`；Enter/Space 打开、方向键在菜单项间移动、Escape 关闭并把焦点还给触发器。保留现有 document click 外部关闭行为与 `matchTrigger` 宽度逻辑。
- [ ] Task 3.3：回归调用方。枚举 Modal 与 ActionMenu 的全部调用点逐个验证，确认 confirm/cancel 语义、遮罩关闭、菜单项点击关闭都没有被改坏。浏览器预览下人工过一遍键盘操作。
- [ ] Task 3.4：i18n 补齐 + 全量验证。

### 验收标准

- 两个组件可纯键盘完成打开、选择、关闭，焦点不逸出对话框。
- 全部调用方行为无回退（含 `confirmDisabled`、`showCancel`、markdown 模式）。
- 完整无障碍结论需要辅助技术实测与专家评审，本阶段只覆盖可静态验证与可键盘验证的部分，不声称 WCAG 全量合规。

## 通用验证命令

```
go build ./...
go vet ./internal/...
go test ./internal/...
npm --prefix frontend run i18n:scan
npm --prefix frontend run build
```

新增 bridge 方法后额外执行 `wails3 generate bindings`。
