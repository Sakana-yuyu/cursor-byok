# cursor-byok 功能清单与可达性报告

> 生成日期：2026-08-09　范围：代码可用性审查、功能盘点、死代码清理、协议接线、健壮性兜底、前端性能优化后的现状快照。
> 对应目标：全部功能可达、非死代码、agent 请求有完整试错兜底、原子性/幂等性、UI 直觉、流畅度。

## 一、系统概览

cursor-byok 是 Cursor 客户端的本地代理（MITM + 后端 agent + 前端管理面板）：

- `internal/mitm`：HTTPS MITM 代理，拦截/转发 Cursor 客户端请求，注入本地配置与模型路由。
- `internal/backend`：核心 agent 运行时（forwarder 驱动 provider 循环、工具执行、委派、goal 循环）。
- `frontend`：Wails 桌面管理面板（12 个页面），通过 @bindings 与后端通信。
- `internal/client`：模型渠道探测/基准测试/余额查询；`internal/updater`：自更新。
- `cmd`：可执行入口（服务端、CLI、安装器辅助等）。

## 二、前端功能清单（按路由，全部可达）

| 路由 | 页面 | 功能 | 关键依赖 |
| --- | --- | --- | --- |
| `/` | Home 首页 | 总览指标、快速入口、模型测试卡片、统计浮窗入口 | clientApi / chart.js |
| `/model-config` | 模型配置 | 模型适配器列表管理（增删改、启停、协议模式） | clientApi |
| `/model-editor` | 模型编辑器 | 单模型详细配置（端点、密钥、请求组、额外参数、思考/预算） | clientApi |
| `/model-catalog` | 拉取模型 | 从供应商目录拉取模型列表（检索/导入） | clientApi / supplierCatalog |
| `/model-groups` | 模型分组 | 分组与路由策略管理 | clientApi |
| `/supplier` | 供应商详情 | 供应商余额、健康状态、价格信息 | clientApi / supplier 组件 |
| `/metrics-detail` | 会话分析 | 会话级用量/费用/事件时间线（echarts 图表） | echarts |
| `/request-metrics` | 请求明细 | 请求级指标列表、异常/降级筛选 | clientApi |
| `/stats-overlay` | 实时统计浮窗 | 独立小窗口：实时指标、本地缓存统计、置顶/布局 | echarts |
| `/diagnostics` | 诊断 | 模型适配器诊断与一键修复 | clientApi |
| `/settings` | 设置 | 分节设置：通用/委派/Goal/Skills/MCP/更新等 | md-editor-v3（按需） |

共享能力：`@wailsio/runtime` 绑定层、`clientApi`/`runtimeControlApi` 统一服务封装、i18n（zh/en 等）、供应商目录/分组工具链、错误人性化提示、输入模态框、Markdown 渲染（marked）与 Markdown 编辑器（md-editor-v3，异步加载）。

## 三、后端核心模块功能清单

| 包 | 功能 | 备注 |
| --- | --- | --- |
| `internal/backend/forwarder` | agent 主循环：RunSSE/BidiAppend 协议、provider 循环、工具派发、交互等待、上下文压缩/投影、运行队列、goal 循环、委派聚合、摘要/用量/费用记录、响应缓存 | 核心枢纽 |
| `internal/backend/agent/model` | 模型适配器：OpenAI chat/responses、Anthropic messages、Gemini native；路由、流式解析、错误分类、思考块转发 | 三协议 |
| `internal/backend/agent/core` | agent 核心类型/工具参数校验 | |
| `internal/backend/agent/prompt` | 提示词编译、摘要延续、上下文投影 | |
| `internal/backend/agent/bridge` | 工具执行桥（exec/interaction） | |
| `internal/backend/delegation` | 子代理/多任务委派：调度器、本地委派 worker、Cursor 协议适配、goal 自检子代理 | |
| `internal/backend/server` | 配置管理（适配器/委派/Goal/代理）、upstream HTTP 转发、Bidi 服务 | |
| `internal/backend/promptsync` | 云端提示词同步（多 provider 拉取 + 缓存） | |
| `internal/mitm` | MITM 代理：证书、CONNECT 隧道、请求改写、广告/UA 处理 | |
| `internal/client` | 模型探测、基准测试、供应商余额、自动匹配上下文 | |
| `internal/modelcontext` | 内置模型能力目录（窗口/输出/视觉/价格） | |
| `internal/historymetrics` | 请求历史指标聚合（SQLite） | |
| `internal/runtime` | 运行时配置快照/渠道解析（legacy ResolvedChannel） | |
| `internal/certs` / `internal/netproxy` | 本地 CA 管理 / 代理感知 HTTP 客户端 | |
| `internal/updater` / `internal/buildinfo` | 自更新清单与构建信息 | |
| `internal/i18n` / `internal/logger` | 本地化与结构化日志 | |

## 四、Cursor 客户端协议接线状态（均已对接）

- `run_request` / `conversation_action`（含 `cancel`）→ `handleRunIntent` / 取消链路，含超驰取消、运行复用、run-rewind 幂等重写。
- 工具调用（`exec_client_message` / `exec_control`）→ 工具执行桥 → 结果回注 provider 循环。
- 用户输入等待（interaction）→ `interaction_response` 异步结果回注；15 分钟无响应超时兜底（`recoverStaleInteractionWithoutResponse`）。
- 异步问题（`AsyncAskQuestionCompletionAction`）→ 复用 InteractionResponse 路径，awaiting-user 收口。
- 白名单预检（allowlist precheck）→ denied 终态收口。
- `redacted_read_result` 读取结果回注。
- TurnFinished / TurnEnded / checkpoint 事件序列：EOF 正常收口补发；中断/错误有终态码。
- 思考块（thinking delta/completed/suppressed）转发；synthetic thinking 幂等（每 turn 一次）。

## 五、模型请求链路与健壮性兜底（本轮修复后）

| 机制 | 说明 |
| --- | --- |
| 流中断不整体重试 | 已转发部分内容后中断（`ErrMidStreamInterrupted`）跳过整体重试，防重复输出/工具重复执行 |
| 错误收口前落盘 | 错误分支先把已实时推送的部分输出写回 history（可回溯、可续接） |
| 上下文超限恢复 | `context_length_exceeded` 强制压缩后重试；失败也落盘部分输出 |
| max_tokens 超限降级 | 从错误文本解析真实上限降级重试（仅无输出时） |
| 400 恢复 | 首次 pass、无输出、无工具调用时注入提示续跑（每回合一次幂等） |
| 流空闲看门狗 | `ProviderStreamIdleTimeout`（默认 90s）防“连接后无数据”悬挂 |
| 响应头超时 | netproxy 客户端 `ResponseHeaderTimeout` 10 分钟，防“永不响应”悬挂 |
| 回合预算兜底 | 非 goal 回合 provider pass 上限 200 / 时长 3 小时，防死循环无限空转 |
| 响应缓存安全 | 含工具调用的回合不写入响应缓存（防回放重复派发工具）；TTL/LRU/落盘 |
| goal 预算 | passes/时长/费用三重预算；`[goal:complete]` 显式声明 + 子代理校验审计 |
| 截断恢复 | `max_output_tokens` 截断且零输出时注入提示续写一轮（每回合一次幂等） |
| 交互等待超时 | 等待用户输入 15 分钟兜底收口 |
| 视觉委派 | 主模型不支持图片时自动委派识图模型；同步 pass 挂接 stream 取消句柄 + 内置超时 |

## 六、健壮性加固（第二轮 2026-08-09）

- 新增 `internal/safego`：统一 panic 兜底封装，覆盖全部 15 处后台 goroutine（MITM 转发、delegation executor、checkpoint 心跳、历史维护、native 委派看门狗、shutdown 取消、广告刷新、自动启动等），未捕获 panic 不再拖垮整个进程。
- 前端 `npm run lint` 恢复全绿：清理 `clientApi.js` 残留死导入（`GetGoals/StartGoal/StopGoal/EnableReaderMCP`，对应包装函数已在前轮删除）。

## 七、健壮性加固（第三轮 2026-08-09）

- MITM 流式转发：上游请求上下文由 `context.Background()` 改为派生自客户端请求上下文，客户端断线立即停止拉取 backend 流，防 goroutine/上游连接悬挂。
- promptsync 拉取：统一 `fetchHTTPClient`（30s 总超时）替换无超时的 `http.DefaultClient`；`cmd/fetch-native-prompt` 增加 30s 截止时间。
- native 委派看门狗：恢复失败时记录 error 日志，不再静默吞错（与 turn_stale 路径一致）。
- 审计结论：`fmt.Errorf` 已普遍使用 `%w` 包裹（无丢失错误链）；HTTP 调用已普遍带超时/派生上下文；v-for 均带 `:key`；图表组件均已 dispose。

## 八、验证结果

- `go build ./...`、`go vet ./...`、`go test ./...`：全绿（2026-08-09）。
- 前端 `npm run build`：通过；入口主包 4.25MB → 797KB（路由懒加载 + 供应商分包 + md-editor 按需加载）。
- 死代码清理：前端删除孤儿组件/死导出，后端 34 文件删除 977 行未使用导出，全仓库零引用确认。
- 每步独立提交（中文 message），历史可回溯；并行子代理只改文件、提交由主代理统一完成。

## 九、遗留说明（非阻塞）

- `@iconify/json` 未在 src 直接引用（tailwind 构建期使用），不进入运行时 bundle。
- md-editor 异步块仍被 vite 预加载提示引用（本地磁盘读取成本极低），解析/执行已按需延迟。
- `PingFang-Medium.ttf` 约 10.9MB 随字体加载（浏览器按需，不影响启动解析）。
- 响应缓存命中率受工具调用回合排除影响略有下降，但避免了副作用重放，安全性优先。
