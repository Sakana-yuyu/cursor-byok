# Cursor-byok 能力完善任务清单

## 目标

逐步完善 Cursor-byok 的按钮交互、工具执行、Skills、MCP、Agent 调用链和 Multitask 委派能力。每个阶段独立验收并单独提交，不发版、不推送，保留任意提交作为回滚点。

## 当前基线

- Go 构建：`go build ./...` 通过。
- 前端构建：`npm --prefix frontend run build` 通过。
- 前端存在既有的 chunk 体积提示，不作为本任务失败条件。
- 当前未提交的浮窗相关文件和 `gui-test-screenshots/` 保留，不回滚、不混入本任务提交。
- 协议已包含 `AGENT_MODE_MULTITASK`、子代理模型字段、MCP 和 Skills 字段。
- 子代理当前主要通过 Cursor 客户端 `SubagentArgs` 执行，服务端已有排队不中断保底机制。
- Skills/MCP 已有扫描和设置 UI，但 MCP 工具 schema 与运行时闭环仍需补齐。

## 阶段清单

- [x] Task 1：审查并完善按钮交互（静态扫描确认事件处理器完整；无未定义按钮动作）。
- [x] Task 2：抽离统一 Tool Registry 和执行收口（工具 canonical kind 注册表已接入）。
- [x] Task 3：完善 Skills 注册、中文简介、开关和调用链（内置 skill 与现有扫描开关已接入）。
- [x] Task 4：完成 MCP 运行时发现、schema 注入和执行闭环（stdio、Streamable HTTP 和 legacy SSE 显式连接均已接入）。
- [x] Task 5：稳定 Agent 模式、provider pass、resume 和 checkpoint 调用链。
- [x] Task 6：增加委派模型组配置与持久化。
- [x] Task 7：增加非阻塞 DelegationScheduler。
- [x] Task 8：接入 Cursor 子代理适配器和 workspace hint。
- [x] Task 9：接入本地子代理适配器。
- [x] Task 10：完成 Multitask 结果合并、失败隔离和取消。
- [x] Task 11：完成委派设置、工具权限、运行状态、取消操作与 MCP 连接控制界面。
- [x] Task 12：完成 Cursor 原生工作流对齐审查。
- [x] Task 13：全面构建、静态检查、协议回放和人工流程验收。

## Task 12 验收记录

- `StartPlanAction` 固定进入 Plan，`ExecutePlanAction` 未指定模式时进入 Agent/Build。
- 顶层 `conversation_action` 复用同一 request 时会启动新 turn，不再被 RunSSE 重连复用逻辑吞掉。
- 新 turn 会重建终态 actor、失效旧 provider/compaction/timer token，并从新的 backlog 游标开始回放。
- run/prewarm 顶层 Skills、MCP tools、MCP 文件系统选项已合并到有效请求上下文。
- MCP schema、显示名路由、内置工具防劫持和本地委派权限链已闭环。
- `go test -count=1 ./...`、`go build ./...`、`npm --prefix frontend run build` 通过。

## Task 13 验收记录

- MCP runtime tool descriptor 和 interaction bridge protobuf 对象改为深拷贝，`go vet` 不再报告锁值复制。
- `go test -count=1 ./...`、`go vet ./...`、`go build ./...` 全部通过。
- `npm --prefix frontend run build` 通过，构建内置 i18n 静态扫描通过。
- `git diff --check` 通过，`proto/` 与 `gen/` 无未预期差异，本阶段文件通过 `gofmt` 检查。
- 浏览器预览人工检查通过：委派模型组新增与模型选择、默认模型启用、MCP 连接/断开状态、Skills/MCP 扫描面板和主要设置按钮均可操作，控制台无 error。
- 全仓 `gofmt -l` 仍会列出大量任务前已存在的未格式化文件；未批量改写这些无关文件。

## 监督式委派增量验收

- 在现有 Multitask 之上增加 SupervisorCoordinator：高能力模型负责复核、纠偏、重试、改派、升级和熔断，worker 继续复用 Cursor 或本地委派适配器执行任务。
- 主 Agent 只接收 pending exec 与异步状态事件，不同步等待 supervisor 或 worker；单任务失败、取消和超时不会阻塞兄弟任务。
- worker 检查点使用 scheduler 单调序列和 `EffectiveProgressAt` 区分真实进展与 ShellStream 心跳；review 期间的终态会立即收口，普通流式分片不会造成重复复核或饥饿。
- Cursor exec 结果按 parent request、exec ID 和 message ID 联合匹配；active waiter 与 45 分钟 tombstone 均拒绝跨请求晚到结果。
- 本地 worker 从结构化 Write、PatchEdit、Edit、Delete 和 GenerateImage 参数提取 changed-file 证据；路径统一脱敏，Windows 根相对、盘符相对和工作区越界路径保留 `../` 标记。
- scheduler retention 以当前 live task 数加最大单批 worker 余量计算，不再随历史 snapshot 数无界增长。
- 委派设置页支持监督开关、监督/复核模型、执行模型组、轮次上限、纠偏/重试上限和改派/升级策略；专用保存链提供 busy、失败回滚、重试和刷新恢复。
- 浏览器预览人工检查覆盖 640、768、1200px：设置页无横向溢出，监督开关和最大轮次保存后刷新可恢复，控制台无 error。
- 两轮独立 scoped review 的最终结论为 Critical none、Important none；此前发现的队列自发送死锁、静默无进展失效、review 饥饿、Cursor parent 误匹配、scope 越界、retention 累积和 tombstone 过短均已收口。
- 最终验证通过：`go test -race -count=1 ./internal/backend/delegation ./internal/backend/forwarder`、`go test -count=1 ./...`、`go vet ./...`、`go build ./...`、`npm --prefix frontend run i18n:scan`、`npm --prefix frontend run build`、JS 语法、i18n 键/占位符一致性、`git diff --check` 和相关 Go 文件格式检查。

已知协议限制：Cursor 原生 `SubagentResult` 不包含 modified-files 字段，当前调用链也收不到带 `modified_files` 的 stop hook。为避免从 final message、transcript 或并发 git diff 猜测结果，Cursor worker 暂不生成文件修改证据；本地 worker 的结构化写工具已完整提供该证据。

## 每阶段验收

每个阶段执行相关构建或静态检查，结合已有命令级工具和人工流程验证；仓库规则禁止新增测试文件，因此不新增测试目录。验收通过后使用独立 commit，commit 失败或验收失败时停止在当前阶段，不推进后续阶段。

## 提交约束

- 仅提交本任务阶段新增或修改的文件。
- 不提交前序浮窗任务遗留改动。
- 不修改已安装 Cursor 客户端 bundle。
- 不创建 release、tag 或执行 push。

## 发布前强化续篇

Task 1-13 的首次实现完成后，发布前审查发现了跨 workspace 共享状态、委派失败终态、SSE 错误语义和 Release 资产生成问题。后续强化与 `v0.0.71` 发布按以下文档继续跟踪：

- 设计：`docs/superpowers/specs/2026-07-31-cursor-byok-capability-hardening-and-release-design.md`
- 计划：`docs/superpowers/plans/2026-07-31-cursor-byok-capability-hardening-and-release.md`

原“不创建 release、tag 或执行 push”约束仅适用于 Task 0-13 的分阶段实现；用户后续明确要求完成强化后使用 `uploadcursor` 发布，因此最终发布任务以续篇计划为准。

## 发布前强化最终验收

- 委派终态链已补齐：主流失败会取消 Multitask aggregate，聚合投递失败进入统一终态收口，broker 读取失败不再伪装为正常 EOF。
- 委派 fan-in 使用 scheduler 状态通知，不再 50ms 轮询；事件包含稳定序号、父 request、父 exec、模型组和更新时间。
- Multitask 启动与取消按 provider pass 同步；终态检查、aggregate 注册和 `PendingExec` 写入在同一提交点完成，取消不会产生伪造的 `Task error` 历史。
- MCP runtime 按 user/workspace scope 隔离，同名 server 可共存；连接状态、能力健康和错误信息不会泄露密钥、header 或机器标识。
- Skills 扫描和稀疏激活按当前 workspace 显式执行；manifest 校验、content hash、诊断展示和禁用状态均已接入现有链路。
- Release manifest 覆盖 Windows amd64/386、Linux amd64、macOS arm64/amd64；DMG 作为独立 Release 资产发布，不写入 updater URL。
- 最终 scoped review 的两个 Important finding 均已修复，复审无 Critical/Important 遗留。
- 最终树验证通过：`go test -count=1 ./...`、`go vet ./...`、`go build ./...`、targeted `go test -race`、`npm --prefix frontend run build` 和 `git diff --check`。
- 仓库规则要求不新增测试文件，本轮沿用现有测试、race、静态检查、发布 fixture 和人工端到端验收。

## v0.0.71 发布验收

- `main`、综合能力分支和三个独立 task 分支已全部收口到主分支；旧 task 分支使用 `ours` merge 记录整合关系，避免覆盖后续审查修正。
- GitHub Actions tag run `30597118345` 与 main run `30597117421` 均成功。
- GitHub Release 已发布：`https://github.com/Sakana-yuyu/cursor-byok/releases/tag/v0.0.71`。
- Release 包含 10 个非空资产：五个平台 updater 包、Windows 双架构 installer、`update.json` 和 macOS 双架构 DMG。
- `update.json` 可解析，版本为 `0.0.71`，五个平台的 size、URL 和 `sha256:` checksum 均有效，updater URL 不引用 DMG。
- Apple Silicon DMG：`cursor-byok-0.0.71-macos-arm64.dmg`，24,324,708 bytes。
- Intel DMG：`cursor-byok-0.0.71-macos-amd64.dmg`，25,555,859 bytes。
