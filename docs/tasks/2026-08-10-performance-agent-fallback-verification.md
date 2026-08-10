# 性能、多 Agent 兜底与 Cursor 兼容性最终验证

## 前置说明

- 任务目标：验证性能优化、多 Agent 执行器、自动故障转移、Cursor 协议映射和紧凑 UI 的实现均有真实入口、可回退，并明确目标机无法完成的外部验证。
- 关键假设：本任务不涉及支付、资金、分润或生产数据；`D:\cursor` 仅作为只读协议和安装状态证据，不修改已安装 Cursor；不重启或终止用户已有 Cursor/助手进程。
- 证据来源：当前分支代码与测试、生产构建产物、Wails 绑定、目标机 CLI 探测和 harmless smoke、只读 Cursor bundle 严格提取、本地服务健康检查。
- 数据时效：运行态与安装状态采集于 2026-08-10（Asia/Shanghai）；版本、登录状态和 Cursor 活动 subscriber 会随环境变化。
- 降级方案：用户明确禁止子代理，审查由主会话直接完成；目标机缺少 gcc，Go race detector 无法运行；Claude 执行和 Cursor 已连接 Task 往返没有取得成功终态，均按未验证记录。
- 风险与回滚：每个任务均为独立提交，可从末尾按提交逆序 `git revert <commit>`；本轮仅新增文档，回滚可直接 revert 最终文档提交。未执行 push、部署、进程重启或持久数据迁移。

## 提交与回滚点

本分支从设计、计划到功能实现均保留独立提交。主要回滚点如下：

| 范围 | 提交 | 作用 |
| --- | --- | --- |
| 设计与计划 | `4fdea0e`, `72fcedc` | 固化范围、验收标准和逐任务执行顺序 |
| 前端启动与历史性能 | `2258daf`..`4951a23` | 编辑器懒加载、单次历史遍历、防重叠轮询、有界渲染、系统字体 |
| Workspace Skills/MCP | `c1ee9b2`..`b4e926a` | 保留 workspace 来源与编辑、workspace 优先级、MCP 信任和规范化指纹 |
| Cursor 可靠性与协议 | `6ea5e5c`..`88dcea1` | 终态重放、Bidi 去重、晚到工具终态、严格 proto 提取和能力图 |
| Executor 基础 | `f8ed841`..`0d9fdd7` | registry、安全进程 runner、持久化策略和公开 API |
| 多 Agent | `ff253ce`..`30a61dd` | Claude、Codex、Gemini、custom/Grok-compatible、Cursor executor |
| 故障转移与 UI | `c533d6d`, `e270218` | 有界自动 failover、运行态 attempt、紧凑设置和取消入口 |

回滚时应从最新提交逆序执行，避免先移除底层 registry/runner 而保留上层 executor 或 UI。

## 性能验证矩阵

| 项目 | 实现与证据 | 结果 |
| --- | --- | --- |
| Markdown 编辑器 | 编辑器资源仅在需要时动态加载；生产 `index.html` 无 Markdown editor preload | 通过 |
| 字体 | 移除打包字体入口，改用系统字体；生产 `index.html` 无 font preload | 通过 |
| 入口资源 | `index.html` 只有 6 个 JS/CSS 入口引用；最大入口 JS 为 921,472 bytes | 已记录 |
| 历史扫描 | `TestHistoryDirectoryStats` 与 `TestHistoryDirectoryStatsWalksOnce` 验证同一次遍历复用统计 | 通过 |
| 历史渲染 | 详情视图使用有界窗口；`history-performance.spec.mjs` 验证大量记录路径 | 通过 |
| 轮询 | `usePolling.test.js` 验证上次任务 settle 后才安排下一次、start 幂等、stop 后不重排 | 通过 |

生产构建保留既有 chunk-size 与 router 动静态 import 提示，但构建 exit 0，lazy editor assertion 通过。上述提示不表示入口重新预加载 Markdown editor 或字体。

## Workspace Skills 与 MCP 信任

- 设置初始化保留 workspace asset source 和用户正在编辑的 workspace root，不被异步初始化结果覆盖。
- workspace Skills/agents 先于全局同名资产，避免全局定义静默覆盖工作区约束。
- MCP 外部命令必须经过 workspace scope、identifier 与命令指纹匹配后才能连接。
- 指纹对可解析命令路径执行规范化，降低同一可执行文件因表示形式不同导致的误判。
- 已知边界：bare/relative MCP 命令仍基于助手进程 CWD 规范化；这是后续若引入 PATH 语义时需要统一的设计点，不影响当前绝对路径信任流程。

## Cursor 协议与能力图

### 严格提取

- 只读源：`D:\cursor\resources\app\extensions\cursor-always-local\dist\main.js`
- 源 SHA256：`17C57A32DE56399C119DD8FEE7733A8BB86E74A4EC798B318A7926A721CE1963`
- 两次运行 `proto/ext_tool/cmd/verify-extraction.ps1` 均 exit 0。
- 每次均提取 19,060/19,060 fields、5,541 messages、487 enums、22 services。
- `skipped=0`、`unresolved=0`、`placeholders=0`。
- 两次得到的 6 个 proto 文件逐文件 SHA256 完全一致。
- checked-in 的 `agent_v1.proto`、`aiserver_v1.proto`、`anyrun_v1.proto`、`internapi_v1.proto` 与提取结果一致。
- 当前 bundle 另含 `git_forge_v1.proto`、`origin_v1.proto` 两个尚未纳入生成代码范围的新域；本任务没有在缺少调用需求时扩大协议生成面。

### 能力图

连续两次运行 `go run ./cmd/sync-tool-catalog --write` 后无 diff，`docs/cursor-capability-map.md` SHA256 均为：

```text
FA0E5039FD5C1468CA54C810A1563C00E4572C411AB763BCB1C3BBDE0E1116E9
```

能力图使用具体 `ExecServerMessage`、`ToolCall` oneof arm 与项目 handler/语义测试建立映射，不以仅出现符号名作为“已支持”证据。

### 目标机 Cursor 状态

- Cursor 版本：3.15.6；已安装进程来自 `D:\cursor\Cursor.exe`。
- Cursor 助手 WebView app version：0.0.77。
- backend `127.0.0.1:18090` 与 proxy `127.0.0.1:18080` 正在监听。
- `http://127.0.0.1:18090/healthz` 返回 `200 ok`。
- Cursor executor probe 能区分“编辑器已安装”和“Agent 通道可执行”：没有活动 subscriber 时返回 editor ready、agent unavailable，不把可启动编辑器误报为可执行 Agent。
- 当前日志未出现本轮真实 RunSSE/Bidi subscriber，因此没有宣称已完成 Cursor 客户端 Task 往返。

## Executor 探测与 Smoke

| Executor | 目标机状态 | Smoke 结论 |
| --- | --- | --- |
| Claude Code | 2.1.226；`auth status` 显示 first-party OAuth 已登录 | probe/auth ready；harmless execution 在 90 秒内无终态，未验证成功；超时进程按本轮 PID 与 token 精确清理 |
| Codex CLI | 0.147.0；API key 登录 | 15.7 秒内返回精确 `TASK24_CODEX_OK`，含 `thread.started`、`item.completed`、`turn.completed` 和 usage |
| Gemini CLI | 未安装 | probe 返回 not-installed 诊断和安装提示；适配器契约由单元测试覆盖，目标机成功执行未验证 |
| custom / Grok-compatible | 未配置 Grok CLI | custom contract、模板变量、输出边界、取消和敏感信息清洗由测试覆盖；目标机真实 Grok 执行未验证 |
| Cursor Agent | editor 已安装，当前无活动 subscriber | 正确返回 action-required/editor-only；真实已连接 Task 往返未验证 |

CLI 输出中可能出现本机鉴权状态或脱敏凭据片段，本文件不记录任何凭据内容。

## 故障转移、取消与 UI

- scheduler 按 enabled、priority、capability 和健康状态选择 executor。
- transient/retry-safe 失败可进入下一个合格 executor；鉴权、配置、权限、用户取消等不可重试错误不会盲目切换。
- `executorFailoverLimit` 对尝试次数设上限，并在一次执行开始时读取一致的策略快照。
- 每次 attempt 公开 executor ID、序号、状态、耗时、失败分类、retry-safe 和清洗诊断；不公开 metadata、完整命令路径、进程参数或原始敏感输出。
- 取消通过稳定 task ID 传到 forwarder，只取消目标 worker，不停止 sibling workers。
- 设置页提供 Claude、Codex、Gemini、Cursor 和 custom/Grok-compatible 的状态、版本、诊断、开关、优先级、刷新与 custom 配置。
- 运行态面板显示当前 executor、故障转移路径和逐次 attempt；390px 窄屏仍可操作优先级、开关和 custom 配置。
- Playwright 覆盖刷新、连续保存、custom 空 executable 拒绝、failover 时间线、取消和桌面/窄屏无横向溢出。

## API 可达性与死代码审计

| API | Bridge / client | 前端调用 | 可见入口与测试 |
| --- | --- | --- | --- |
| `GetDelegationExecutorSnapshots` | `internal/bridge/proxy.go` → `internal/client/delegation.go` → Host registry | `clientApi.js` → `DelegationSettings.vue` | Agent 执行器列表；executor E2E |
| `RefreshDelegationExecutorProbes` | 同上，Host 强制 probe 每个 registry item | `clientApi.js` → `DelegationSettings.vue` | 刷新按钮；E2E 断言真实调用名 |
| `SaveDelegationConfig` | bridge → client 串行保存 → config manager | `runtimeControlApi.js` → `DelegationSettings.vue` | 开关、优先级、custom 对话框；连续保存 E2E |
| `GetDelegationTaskSnapshots` | bridge → client → Host → forwarder runtime | `runtimeControlApi.js` → runtime panel/task strip | 当前任务、executor/attempt 时间线；E2E |
| `CancelDelegationTask` | bridge → client → Host → forwarder runtime | `runtimeControlApi.js` → runtime panel/task strip | 取消按钮；E2E 断言调用 |

Wails 生成绑定包含上述 5 个方法；Task 23 生成统计为 576 packages、4 services、111 methods、117 models。Claude、Codex、Gemini、Cursor 与 custom factory 均在 `internal/backend/host.go` 注册，`host_executor_test.go` 分别覆盖注册和配置应用。`rg` 审计没有发现本任务新增的仅定义、无调用后端 API。

## 全量门禁

| 命令 | 结果 |
| --- | --- |
| `npm --prefix frontend run i18n:scan` | 通过；4 个 locale 各 1,387 keys，missing/extra/empty/placeholder mismatch 均为 0 |
| `npm --prefix frontend run test:unit` | 23/23 通过 |
| `npm --prefix frontend run lint` | exit 0 |
| `npm --prefix frontend run build` | exit 0；lazy editor assertion 通过 |
| `PLAYWRIGHT_PORT=4181 npm --prefix frontend run test:e2e` | 41/41 通过 |
| `go test -p 1 -count=1 ./...` | exit 0 |
| executor/Cursor/failover 聚焦测试 `-count=20` | 20 次通过 |
| `go vet -p 1 ./...` | exit 0 |
| `go build -p 1 -buildvcs=false ./...` | exit 0 |
| `git diff --check` | exit 0 |
| Go race detector | 未执行成功：`cgo: C compiler "gcc" not found` |

完整 E2E 曾在与 i18n scan/build 并行时出现一次 Vite HMR 干扰导致 40/41；目标用例独立重复 3 次通过，随后隔离运行全量为 41/41，因此没有据此修改产品代码。

## 未验证项与风险

- Claude Code：登录与 probe 可用，但目标机模型执行在 90 秒内没有终态；不能宣称 Claude real smoke 成功。
- Gemini/Grok：目标机未安装或未配置，真实成功执行仍依赖用户提供相应 CLI 和账号环境。
- Cursor：没有活动 RunSSE/Bidi subscriber，无法在不打断用户现有会话的前提下完成已连接 Task 往返。
- race detector：缺少 gcc/CGO 工具链；并发风险由连续 20 次聚焦测试、全仓串行测试和锁/状态审计补充，但不能等价替代 race detector。
- Host 配置监听 unsubscribe 当前与 Host 生命周期一致；`Stop` 只是可重启的服务停止点，不能直接解绑。若以后引入 Host 销毁/替换，应在明确的 `Close` 生命周期中保存并调用 unsubscribe。
- 协议观察项：`git_forge_v1.proto`、`origin_v1.proto` 已能严格提取，但尚无本项目调用需求和生成代码，不将其误写为已接入功能。
- 低优先级遗留：Markdown modal 关闭过渡、font artifact 非递归扫描、workspace root 全操作 E2E、relative MCP CWD 语义、placeholder 窄并发窗口、旧注释、delayed-success 时间边界仍可在后续聚焦任务中继续收紧。

## 验收结论

代码层面的性能优化、executor 注册与安全执行、自动故障转移、取消、Cursor editor/agent 能力区分、协议严格提取、能力图、紧凑 UI 和 API 可达性均有实现与自动化证据。Codex real smoke 成功；Claude、Gemini、Grok 与 Cursor active-subscriber 的外部环境限制已明确记录，没有把未取得的运行结果写成成功。
