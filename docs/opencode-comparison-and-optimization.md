# opencode vs cursor-byok：差异对比与优化建议

> 版本：2026-08 · 调研基线：opencode `dev` 分支（`github.com/anomalyco/opencode`，TS/Bun monorepo）、cursor-byok 当前工作区（Go）。
> 引用格式：cursor-byok 用 `internal/...` 相对路径；opencode 用 `packages/opencode/src/...` 相对路径（下文简写 `src/...`）。**路径映射约定**：cursor-byok 未标目录的引用按小节语境归属——§2.1/2.2 的协议适配器文件（router/openai/anthropic/gemini/retry/http_error/stream_idle/provider_compatibility 等）在 `internal/backend/agent/model/`；§2.2-2.4 的 forwarder 业务文件（service/tool_catalog/tool_error_completion/rewind/max_tokens_recovery/context_overflow/compaction/shell_recovery/turn_stale/supervisor_*/delegation_multitask/tool_result_* 等）在 `internal/backend/forwarder/`；delegation 体系（scheduler/supervision/loop_detector）在 `internal/backend/delegation/`。opencode 未标目录时：prompt/processor/message-v2/compaction/revert/overflow 在 `src/session/`，provider/transform/error 在 `src/provider/`，tool/invalid 在 `src/tool/`，agent 在 `src/agent/`。
> 范围：模型转发、错误重试与降级、工具系统与错误反馈、Agent 调用/调度四个维度；优化建议聚焦后三个方向（用户确认范围）。

---

## 1. 项目定位与架构总览

| | cursor-byok | opencode |
|---|---|---|
| 语言/形态 | Go，本地代理进程（CA 证书 + MITM + 桥接） | TypeScript/Bun，CLI + desktop，monorepo（AI SDK + effect） |
| 定位 | **Cursor 客户端的 BYOK 网关/中间层**：把 Cursor 的私有协议转成任意 OpenAI/Anthropic/Gemini 兼容端点 | **独立开源 coding agent**：自带会话、工具、插件、provider 生态 |
| 硬约束 | 必须保持与已安装 Cursor 客户端的协议兼容（改 prompt/history/replay 需遵守 prefix-cache-stability） | 无外部协议约束，自定消息/事件格式 |
| 核心链路 | forwarder.Service → ProviderGateway → `Router.Stream`（router.go:78）→ 三适配器（OpenAI/Anthropic/Gemini）→ 统一 `ModelEvent` | session 主循环（prompt.ts runLoop）→ `LLM.stream`（AI SDK / native 双运行时）→ provider 工厂 |
| 最大优势 | 深度容错 + 供应商特化兼容（面向中文中转站生态打磨最深） | provider 生态（~30 预置）+ 声明式工具/agent 抽象 + 插件体系 |

**结论**：两者不是同类竞品——cursor-byok 是"网关 + 容错引擎"，opencode 是"agent 平台"。对比意义在于把 opencode 的抽象能力平移进 cursor-byok 的网关约束内。

---

## 2. 四维度逐项对比

### 2.1 模型转发 / Provider

| 维度 | cursor-byok | opencode | 谁更强 |
|---|---|---|---|
| 架构形态 | 统一 canonical 中间结构（`Message`/`ToolCallDescriptor`/`ModelEvent`，agent/model/types.go）+ 3 个内置协议适配器（openai.go 110KB / anthropic.go 56KB / gemini.go 15KB） | AI SDK 工厂表 `BUNDLED_PROVIDERS`（约 24 个 npm 包，provider.ts:107-134）+ 按 provider 的 `custom()` 加载器（provider.ts:168-680） | opencode（生态广） |
| 协议转换深度 | 手写协议细节：thinking 字段（`applyAnthropicThinkingConfig` anthropic.go:1545、`applyOpenAIThinkingDisable` openai.go:2575）、Anthropic cache_control 断点（`buildAnthropicCacheFrontier` anthropic.go:773、`applyAnthropicCacheBreakpoints` :892）、reasoning signature、tool schema 归一化（protocol_transform.go） | 协议文本翻译交给 AI SDK；transform 层只做 options 映射/缓存（`message()` transform.ts:464-517、`sdkKey()` :42-96） | cursor-byok（协议细节可控） |
| 供应商兼容性 | 强：`classifyProviderCompatibility`（provider_compatibility.go:20）识别 Copilot/DeepSeek/xAI/Kimi/OpenRouter/硅基/智谱/通义/MiMo/MiniMax/StepFun，产出缓存 key 开关、私有字段剥离（:154）、thinking 禁用策略（:68）、xAI 专属裁剪（openai.go:2308-2310，删 safety_identifier/external_web_access） | 弱：主要依赖 AI SDK 各家封装 + OpenAI 系 404 也重试（error.ts:23-28） | **cursor-byok 明显更强**（BYOK 场景核心） |
| 自定义 provider | 配置文件 `ModelAdapterConfig`（type/baseURL/APIKey/modelID/参数注入），Go 内置，无运行时加载 | `opencode.json` provider 配置（provider.ts:1425-1520），默认 `@ai-sdk/openai-compatible`，可动态 `import` 自定义 npm 工厂（:1781-1801） | opencode（可扩展性） |
| 协议自动升降级 | claude-on-openai 自动升级 anthropic 原生协议（`upgradeOpenAIClaudeToAnthropic` provider_compatibility.go:117），上游 400/404/405 时降级回 OpenAI 重试一次（router.go:391/408） | 无 | cursor-byok（独有） |
| 渠道级路由 | 模型 id → 配置渠道三级匹配（adapter.ID → legacy → providerModelID）+ 轮询负载均衡 + 健康冷却（router.go:200-214）+ 最多 10 次跨渠道（routerMaxStreamAttempts=10） | 单 provider 直连，无渠道池 | cursor-byok（独有） |
| 模型目录 | 内置 `go:embed models.json` 正则规则 + 运行时探测（client/model_probe.go） | `getModel/closest/defaultModel` 按 provider 查询 | 相当 |

**结论**：模型转发放置 cursor-byok 的网关场景下**整体更强**——供应商兼容体系、协议升降级、渠道 failover 都是 opencode 没有的；opencode 值得借鉴的是"自定义 provider 生态化加载"，但对 Go 网关意义有限，不建议照搬。

### 2.2 错误重试与降级

| 维度 | cursor-byok | opencode | 谁更强 |
|---|---|---|---|
| 重试层次 | 多层：建连阶段有界重试（retry.go，`providerRetryMaxAttempts=1` 实为直通）→ 渠道级 failover（router.go 最多 10 次、健康冷却）→ OpenAI 流式透明重连 → forwarder 业务级自救 | 单层：`Effect.retry(SessionRetry.policy)`（processor.ts:660-674） | cursor-byok（层次多、覆盖广） |
| 错误分类 | `*HTTPStatusError`（http_error.go:52）+ `isTransientProviderStatus`（retry.go:125）+ `isPermanentProviderError`（router.go:424）；两个 400 特例视为可恢复（router.go:449、forwarder/provider.go:20） | `parseAPICallError`（error.ts:165-186）与 `parseStreamError`（error.ts:102），context overflow 单独识别，OpenAI 系 404 也重试（error.ts:23-28） | 相当；cursor-byok 更细 |
| 退避策略 | 内层 200ms<<attempt + 抖动、上限 5s（retry.go:25-27，因 `providerRetryMaxAttempts=1` 实为直通，该退避实际生效于 OpenAI 流式重连路径 openai.go:595）；router 层 150ms<<attempt、上限 2s；Retry-After 支持秒/HTTP 日期、上限 30s（retry.go:149） | 2s×2^n、无头封顶 30s、Retry-After 优先（retry.ts:26-29,35-66） | 相当 |
| 流式重连 | **仅 OpenAI 适配器**：`runOpenAIStreamWithReconnect`（openai.go:558），pre-output 断流最多重连 2 次（maxStreamReconnects=2，retry.go:207），已转发任何事件绝不重连；**Anthropic/Gemini 无重连** | 无自动重连；靠 SSE 逐块超时（chunkTimeout，provider.ts:1743-1768）+ abort 组合保证不卡死 | cursor-byok 部分更强，但有缺口 |
| 流静默/逐块超时 | 90s idle 看门狗（`providerStreamIdleWatchdog` stream_idle.go:32，默认 90s :15，只测"有无有效内容"） | headerTimeout + 逐块超时（每块都有 deadline），更快失败 | **opencode 更细** |
| max_tokens 超限 | 强：`max_tokens_recovery.go` —— 2 次机会（:22）、正则解析中转站真实限制（:52）、失败按已有 cap 减半或 2048 兜底（:87-95）、cap 持久化到渠道后自动续跑（:108-124） | 无专门机制（OutputLengthError 归一化为错误） | **cursor-byok 明显更强** |
| 上下文溢出 | 窗口减半 + 持久化（context_overflow.go:107）+ 强制压缩（compaction.go:213）+ 挂起/自动续跑（compaction.go:340,370），2 次上限（context_overflow.go:15） | ContextOverflowError → 自动 compaction（compaction.ts:289-511），无窗口减半 | cursor-byok 更强 |
| 工具/shell/turn 恢复 | 完整：shell 超时注入合成 `<shell-incomplete>` 结果继续驱动（shell_recovery.go:146-196）；turn_stale 两阶段恢复（turn_stale.go:24-55,132-198） | abort 时工具标 `interrupted`，重放跳过（message-v2.ts:248-256,351-360） | cursor-byok 更强 |
| 限额/计费错误 | 渠道冷却区分 401/402/403（10min）与 429/5xx（1min） | Free/Go 限额产出 upsell action（retry.ts:68-152） | 各有千秋（场景不同） |

**结论**：错误重试整体 **cursor-byok 更强**（渠道 failover、max_tokens/溢出/工具恢复是 opencode 没有的）。可借鉴缺口：① Anthropic/Gemini 补齐 pre-output 流式重连；② SSE 逐块超时（更快失败、更快重试，比单纯 idle watchdog 精确）；③ OpenAI 系 404 重试的容错评估。

### 2.3 工具系统与错误反馈

| 维度 | cursor-byok | opencode | 谁更强 |
|---|---|---|---|
| 定义方式 | schema 来自**静态 prompt 资产**（prompt/embed.go ReadTools，OpenAI function-call JSON），按 mode 白名单过滤（`isToolAllowedInMode` tool_catalog.go:231） | **声明式 `Def`**：zod schema + `execute` 返回 `Effect<ExecuteResult>`，`ExecuteResult` 区分 title/metadata/output/attachments（tool.ts），注册进 ToolRegistry | **opencode 更现代、可编程** |
| 参数校验 | 适配器内提前校验 tool args JSON（`completedOpenAIToolArgsJSON` openai.go:77、`completedAnthropicToolArgsJSON` anthropic.go:1437） | decode 失败 → `InvalidArgumentsError`（tool.ts:24-34），文案自带**修复指令**："Please rewrite the input so it satisfies the expected schema" | opencode（带引导） |
| 错误反馈 | `formatPreDispatchToolError` 回填 `<Tool> error: <原因>`（tool_error_completion.go:101-111），**不注入修复指令**；`recoverableToolInvocationError` 标记可恢复（:29） | 错误经 `error-text` ToolResultPart 回传；权限拒绝带 typed error（DeniedError/RejectedError/CorrectedError，后者含 feedback） | opencode 略优 |
| 未知工具兜底 | 有：`unsupported tool invocation` 错误回填（service.go:2327-2328），但无引导文案 | 专用 `invalid` 兜底工具（invalid.ts:9-21）返回清晰提示，防崩溃 | opencode 更友好 |
| 重复动作检测 | **无**（主循环无；loop_detector 仅用于子代理 checkpoint） | **doom loop 检测**：连续 3 次同工具同输入 → 权限询问打断（processor.ts:29,358-380） | **opencode 独有，cursor-byok 缺失** |
| 结果截断 | 三层：投影截断（tool_result_replay_truncation.go:340）、持久化 snip（tool_result_snip.go:22-34，≥8KB 裁至 4KB）、交互桥截断 | `truncateToolOutput`（message-v2.ts:295）+ compaction 清理旧结果 | cursor-byok 更精细 |
| 执行管线 | Write/PatchEdit 多阶段隐藏 Read→Write→PostRead（execBridge）、交互型走 interactionBridge、MCP 捕获/注册（mcp_registry.go） | 工具直接 `ctx.execute`，MCP 作为工具注入 | 相当 |

**结论**：工具系统 **opencode 更先进**。cursor-byok 值得借鉴：① 主循环 doom loop/重复动作检测（最大价值）；② 参数校验失败注入修复指令（低成本、高收益）；③ 未知工具兜底文案增强（低成本）。

### 2.4 Agent 调用/调度

| 维度 | cursor-byok | opencode | 谁更强 |
|---|---|---|---|
| agent 定义 | proto 枚举 mode（AGENT/ASK/PLAN/DEBUG/MULTITASK，agent/core/types.go:345）+ 各 mode 工具白名单（tool_catalog.go:51 起）；子代理只有 `subagent_type` 字符串（explore/browserUse/shell/custom 等）+ 模型覆盖 `LookupSubagentModelOverride`（agent/core/types.go:27） | 声明式 `Agent.Info`（agent.ts:35-56）：name/description/mode(subagent|primary|all)/temperature/model{modelID,providerID}/variant/prompt/options/steps/permission，可配置覆盖 | **opencode（per-agent 独立 model/prompt/steps/权限）** |
| 主循环 | forwarder actor 驱动（driveProvider 循环、provider pass、max_tokens 恢复、compaction 挂起/续跑） | `runLoop`（prompt.ts:1081-1341）：终止条件判定（:1111-1130）、`maxSteps` + 最后一步注入 MAX_STEPS_PROMPT（:1178,1281）、`handle.process` 返回 compact/stop/continue（:1272-1286） | 相当 |
| 子代理调度 | 强：`Scheduler`（delegation/scheduler.go:126，并发槽 + 结果回收）+ 三种派发器（cursor exec 子会话 / local child conversation / multitask 聚合 delegation_multitask.go:685，另有 prewarm 预启动与 forwarder/delegation_native_runtime.go 原生运行时派发路径） | general subagent + task 工具 + 并行执行 | cursor-byok 更复杂强大 |
| 监督机制 | **强**：`SupervisorCoordinator`（forwarder/supervisor_coordinator.go:19）+ 监督模型 `Review`（forwarder/supervisor_provider.go:87），决策 accept/correct/retry/reassign/escalate/circuit_open（delegation/supervision.go:29-35） | 无独立监督层（doom loop 权限询问最接近） | **cursor-byok 明显更强** |
| 循环检测 | `LoopDetector`（internal/backend/delegation/loop_detector.go:35；构造函数 NewLoopDetector :62）5 类问题：scope_drift/repeated_action/repeated_failure/no_progress/missing_evidence（DetectCheckpointIssue :74）——但仅作用于子代理 checkpoint | 主循环 doom loop（3 次同工具同输入） | 互补（cursor-byok 缺主循环侧） |
| 中断/恢复 | interrupt.go、turn_stale、exec_watchdog、重放跳过中断内容 | AbortController + Fiber.interrupt + 重放跳过（message-v2.ts:248-256）+ 天然续跑 | 相当 |
| 快照/回滚 | turn 级会话回退（rewind.go：`decideRunRewind` :38、`applyRunRewindToConversation` :220） | step 级：git 仓库 checkpoint（snapshot/index.ts:318-540，track/patch/restore/revert）+ step-finish 生成 patch part（processor.ts:435-484）+ 按 patch 反向回滚（revert.ts:38-88） | opencode（粒度细） |

**结论**：Agent 调度整体 **cursor-byok 更强**（supervisor 监督、loop_detector、multitask 聚合都是 opencode 没有的）。opencode 值得借鉴：① 声明式 per-agent 定义（独立 model/prompt/steps）；② 主循环 doom loop 检测（与 2.3 合并）；③ step 级快照/回滚（成本高，谨慎评估）。

---

## 3. 综合结论（哪个更好）

**不能简单说哪个更好，二者定位不同**：

- **在 BYOK 网关场景**（多中转站、自定义模型、协议不兼容、额度不稳），cursor-byok 明显更强：渠道 failover、协议自动升降级、供应商特化兼容、max_tokens/上下文溢出自动恢复、shell/turn 恢复、supervisor 监督——这些 opencode 都没有或很弱。
- **在 agent 平台抽象**（声明式工具、per-agent 定义、doom loop、step 级快照、provider/插件生态），opencode 更现代。

**对 cursor-byok 真正值得借鉴的 6 点**（按优先级）：

1. 主循环 doom loop/重复动作检测（opencode processor.ts，防死循环，高价值）
2. Anthropic/Gemini 补齐 pre-output 流式重连（对齐 OpenAI 适配器，消除不对称）
3. 参数校验失败注入修复指令（低成本高收益）
4. SSE 逐块超时（更快失败、更快重试）
5. 未知工具兜底文案增强
6. 轻量声明式 per-agent 定义（对齐 Agent.Info；收益中等，改动面较大）

已在 IMPROVEMENT_TASKS.md 阶段 4 完成的（retry/router failover/tool args 校验）不重复建议。

后续可选调研：opencode 的 permission arity（permission/arity.ts，按参数模板精确匹配的权限规则）、编辑自修复策略（code-mode/apply_patch）、工具执行 abort 类型化等差异点，可作为下一轮对比对象。

---

## 4. 优化建议清单（按方向）

### 4.1 错误重试与降级

| # | opencode 机制（参考） | cursor-byok 现状 | 建议 | 目标文件 | 优先级 |
|---|---|---|---|---|---|
| A1 | —（对齐自身 OpenAI 路径） | Anthropic/Gemini 无 pre-output 流式重连（仅 90s idle watchdog） | 抽出 `runOpenAIStreamWithReconnect` 的重连骨架（emitted 标记 + maxStreamReconnects=2 + `IsStreamConnectionReset` 判定）为通用 helper，接入 `AnthropicAdapter.Stream` 与 `GeminiAdapter.Stream` | agent/model/{retry,anthropic,gemini}.go | 高 |
| A2 | SSE 逐块超时（provider.ts:1743-1768 chunkTimeout/headerTimeout） | 只有整流 idle watchdog（90s 无有效内容），半死连接最坏等 90s | 为三个适配器的 SSE 解析循环加"单块/单事件 deadline"（如 30s 无新块即判断流），失败走 A1 重连或 router failover | agent/model/{openai,anthropic,gemini}.go、stream_idle.go | 中 |
| A3 | OpenAI 系 404 也重试（error.ts:23-28） | 4xx 除 429 视为永久（router.go:424），仅 claude-on-openai 降级覆盖 404/405 | 评估：对 OpenAI 兼容自定义端点，404（模型名/路径未就绪）是否纳入有限重试或提示"模型不存在"的可读错误 | agent/model/router.go、http_error.go | 低 |
| A4 | Retry-After 优先（retry.ts:35-66，含 retry-after-ms） | 已支持秒/HTTP 日期、上限 30s（retry.go:149） | 可选增强：补充 `retry-after-ms` 头解析（部分中转站使用） | agent/model/retry.go | 低 |

### 4.2 工具系统与错误反馈

| # | opencode 机制（参考） | cursor-byok 现状 | 建议 | 目标文件 | 优先级 |
|---|---|---|---|---|---|
| B1 | doom loop 检测（processor.ts:29,358-380，连续 3 次同工具同输入 → 权限询问） | 主循环无重复动作检测（LoopDetector 仅作用于子代理 checkpoint，internal/backend/delegation/loop_detector.go:74） | 在 forwarder 侧维护 (toolName, argsHash, 连续次数) 状态：同工具同 args 连续 N 次（建议 3）时注入明确提示（"检测到重复调用，请先阅读上次结果并改变策略"），超过阈值可中断轮次；argsHash 可复用 `NormalizeToolSignature` 思路（internal/backend/delegation/loop_detector.go:96） | forwarder/service.go（handleToolInvocation 附近）、tool_error_completion.go | 高 |
| B2 | InvalidArgumentsError 引导文案（tool.ts:24-34："Please rewrite the input so it satisfies the expected schema"） | `formatPreDispatchToolError` 仅回填 `<Tool> error: <原因>`（tool_error_completion.go:101-111） | 对**参数解析失败**类错误追加引导句（"请修正参数后重试"），对业务错误保持原文；在 `newRecoverableToolInvocationError` 处区分错误类别 | forwarder/tool_error_completion.go、agent/core/tool_args.go | 中 |
| B3 | `invalid` 兜底工具（invalid.ts:9-21） | 未知工具回填 "unsupported tool invocation"（service.go:2327-2328） | 文案增强为可执行提示（如"工具 X 不存在，请从可用工具中选择：..."），可复用 `selectToolsByOrderedNames`/catalog 名称列表 | forwarder/service.go、tool_catalog.go | 低 |

### 4.3 Agent 调用/调度

| # | opencode 机制（参考） | cursor-byok 现状 | 建议 | 目标文件 | 优先级 |
|---|---|---|---|---|---|
| C1 | 声明式 `Agent.Info`（agent.ts:35-56：name/mode/model/prompt/steps/permission） | 子代理仅 `subagent_type` 字符串 + `LookupSubagentModelOverride`（agent/core/types.go:27）；mode 为 proto 枚举（:345） | ✅ 已实施：代码声明式注册表（`agent/core/subagent_registry.go`，explore/generalPurpose/browserUse 内置角色）+ 配置驱动覆盖（`DelegationConfig.SubagentProfiles`，读时合并：覆盖 > 内置，空片段=禁用注入）+ 委派设置页「子代理角色」区块（增删/编辑/自动保存）；仅作用于本地委派路径，Cursor 原生子代理由客户端管理；工具白名单/maxSteps 仍为预留字段 | backend/agent/core/subagent_registry.go、server/config/delegation.go、forwarder/delegation_cursor.go、DelegationSettings.vue | 已完成（白名单/maxSteps 预留） |
| C2 | step 级 checkpoint + patch 回滚（snapshot/index.ts:36-45、revert.ts:38-88、processor.ts:435-484） | turn 级会话回退（rewind.go:38,220） | 评估是否将现有 rewind 升级为 step 粒度（每个 provider pass 记录文件变更集）；成本高，建议作为远期项，先出设计再决定 | forwarder/rewind.go、step/recorder.go | 低（远期） |

---

## 5. 实施约束与后续建议

- **沿用仓库既有约束**（IMPROVEMENT_TASKS.md）：不写任何测试；改 prompt/history/replay 相关逻辑须遵守 prefix-cache-stability（A1-A4 为流式/建连层改动，不涉历史重放；B2 修改 tool_result 文案属一次性前缀失效，保持文案结构稳定即可；B1 若需在重放中注入提示需评估缓存稳定性；C2 的更细粒度历史回退会扩大重放变化范围，实施前须单独评估缓存稳定性）；不改已安装 Cursor 客户端。
- **验证方式**：`go build ./...` 全量编译 + 手动联调（BYOK 网关场景：真实中转站 + claude-on-openai 升降级路径回归）。
- **建议实施顺序**：B1（最高价值）→ A1（消除不对称）→ B2/A2（低成本收益）→ B3/A3/A4 → C1（改动面大，单独排期）→ C2（远期）。
- 本文档仅作分析与建议，不包含代码改动；批准后按上述顺序分阶段实施，每阶段独立验证。
