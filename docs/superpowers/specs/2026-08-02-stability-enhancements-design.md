# cursor-byok 稳定性增强：A1-A4 错误重试 + B1-B3 工具反馈 设计文档

> 日期：2026-08-02 · 状态：待审阅 · 关联：`docs/opencode-comparison-and-optimization.md` §4 优化建议
> 范围：A1-A4（错误重试与降级）+ B1-B3（工具系统与错误反馈），共 7 条。C1/C2 暂缓。
> 约束：不写测试（仓库既有约束）；参数先硬编码默认值、本轮不做界面；不改已安装 Cursor 客户端；尽量在现有代码上做加法，保证现有功能不被破坏。

---

## 1. 背景与目标

对比分析文档（docs/opencode-comparison-and-optimization.md）指出 cursor-byok 与 opencode 的差异，并给出 9 条优化建议。本轮实施其中 7 条，聚焦：

- **错误重试与降级**：补齐 Anthropic/Gemini 流式重连（A1）、SSE 逐块超时（A2）、OpenAI 兼容端点 404 容错（A3）、`retry-after-ms` 头支持（A4）
- **工具系统与错误反馈**：主循环 doom loop 检测（B1）、参数校验失败修复指令（B2）、未知工具兜底文案增强（B3）

目标：消除流式链路不对称（OpenAI 有重连而 Anthropic/Gemini 没有）、加快失败检测、给模型更有效的工具错误反馈、防死循环。全部为增量改动，不改变正常路径行为。

## 2. 非目标（明确不做）

- C1 声明式 per-agent 注册表、C2 step 级快照（远期单独排期）
- 参数配置界面（本轮全部硬编码默认值）
- 新增任何测试代码；不修改已安装的 Cursor 客户端
- 不重构既有重试体系（router 渠道 failover、max_tokens 恢复等保持现状）

## 3. 实施批次与设计

按风险梯度分 5 批，每批独立验证（go build + 现有测试 + 针对性回归）。

### 批次 1：B2 + B3（工具错误反馈文案，零风险）

**B2 参数校验失败注入修复指令**
- 现状：`formatPreDispatchToolError`（internal/backend/forwarder/tool_error_completion.go:101-111）对所有分发前错误统一回填 `<Tool> error: <原因>`，无修复引导；`newRecoverableToolInvocationError`（:29）只标记可恢复，不区分错误类别
- 改动：
  1. 在参数解析/校验失败路径引入错误类别标记——优先在 `internal/backend/agent/core/tool_args.go` 的参数解析处（或复用 `newRecoverableToolInvocationError` 包装时）给"参数非法"类错误加可识别类型（如新增 `invalidToolArgumentsError` 或扩展 `recoverableToolInvocationError` 带类别字段）
  2. `formatPreDispatchToolError` 对该类错误追加引导句：「请修正参数后重试（参考该工具的参数结构说明）」；业务错误保持原文
- 落点：`internal/backend/agent/core/tool_args.go`、`internal/backend/forwarder/tool_error_completion.go`
- 验证：构造参数非法的工具调用，确认模型收到的 tool_result 含引导句；业务错误（如文件不存在）不含引导句
- 缓存注意：tool_result 文案变更属一次性前缀失效，保持文案结构稳定即可

**B3 未知工具兜底文案增强**
- 现状：service.go:2328 回填 `unsupported tool invocation: <名>`
- 改动：文案改为「工具 <名> 不存在或当前模式不可用，请从可用工具中选择：<按 mode 过滤后的工具名列表>」；工具名列表复用已加载 catalog（`selectToolsByOrderedNames` / `DefaultToolCatalog.Load` 结果，按 `isToolAllowedInMode` 过滤）
- 落点：`internal/backend/forwarder/service.go`（:2327-2328）、`internal/backend/forwarder/tool_catalog.go`
- 验证：模型调用不存在工具，确认返回的 tool_result 含可用工具列表且列表与 mode 白名单一致

### 批次 2：B1 主循环 doom loop 检测

- 检测点：`handleToolInvocation`（internal/backend/forwarder/service.go:2253），每次工具调用必经，已有 `stream.mu` 保护
- 状态：挂在 `ActiveStream`（internal/backend/forwarder/types.go），新增字段如 `doomLoopCounts map[string]int`，key = toolName + ":" + argsSHA256
- 复用 `NormalizeToolSignature`（internal/backend/delegation/loop_detector.go:96，toolName + sha256(args)）计算签名
- 逻辑：
  - 同 tool 同 args **连续**出现 → 计数 +1；出现不同 tool/args → 该 key 清零
  - 计数达 `doomLoopThreshold = 3`（常量）→ 该次工具调用结果注入提示：「检测到重复调用 <tool>（相同参数 N 次），请先阅读上次工具结果并改变策略」
  - 计数达 `doomLoopHardLimit = 5` → 中断本轮（返回可恢复错误，终止 provider 循环）
  - 工具调用**成功**后清零该 key（避免把"成功收敛的重复调用"误判）；仅连续失败/无进展的重复才累计
- 落点：`internal/backend/forwarder/service.go`（handleToolInvocation 内）、`internal/backend/forwarder/types.go`（ActiveStream 字段）
- 验证：模拟模型连续 3 次相同工具调用，确认第 3 次结果含提示；正常对话流无此提示；工具成功后计数清零
- 风险：不改变正常路径——仅在检测到重复时注入增量提示；中断仅在硬阈值，且返回可恢复错误（模型可继续）

### 批次 3：A4 + A3（retry/router 小改）

**A4 `retry-after-ms` 支持**
- 现状：`parseRetryAfter`（internal/backend/agent/model/retry.go:149-169）只解析 `Retry-After` 头（秒数 / HTTP 日期）
- 改动：优先读 `retry-after-ms` 头（毫秒正整数），再读 `Retry-After`；超出 `providerRetryMaxRetryAfter`（30s）仍封顶
- 落点：`internal/backend/agent/model/retry.go`（parseRetryAfter）
- 验证：构造含 `retry-after-ms` 的 429 响应（单元级手动验证），确认退避按毫秒值

**A3 OpenAI 兼容端点 404 容错**
- 现状：`isPermanentProviderError`（router.go:424）把 404 视为永久错误，单渠道立即返回；claude-on-openai 降级路径 `shouldFallbackToOpenAI`（router.go:391）已处理 404/405（降级优先，不冲突）
- 改动：仅对 OpenAI 兼容端点（`ProtocolGroup` 为 openai）且**多渠道**时，404 不立即判永久，尝试下一个渠道；全部渠道 404 后返回可读错误：`model <X> not found at <baseURL>（请检查模型名或中转站是否支持该模型）`
- Anthropic 协议 404 保持现状（不重试）
- 落点：`internal/backend/agent/model/router.go`（isPermanentProviderError 或 Stream 循环内 404 分支）
- 验证：单渠道 404 仍快速返回（不重试）；多渠道首渠道 404 会换渠道；返回错误含模型名与 baseURL

### 批次 4：A1 Anthropic/Gemini pre-output 流式重连

- 抽取通用 helper：`streamWithReconnect(ctx, sink, adapt func(int, func(ModelEvent) error) error) error`，骨架取自 `runOpenAIStreamWithReconnect`（openai.go:558-603）：
  - `emitted` 标记：已向 sink 转发任何事件则绝不再重连（避免重复输出）
  - `IsStreamConnectionReset`（retry.go:213）判定可重连错误；ctx 取消不重连
  - 重连次数上限 `maxStreamReconnects = 2`（retry.go:207）；退避 `providerRetryBaseDelay << (attempt-1)` 上限 `providerRetryMaxDelay`，`sleepWithContext` 尊重 ctx
- OpenAI 专属的 prompt_cache_key 适配（openai.go:578-584）不并入通用 helper——保留在 OpenAI 适配器内（闭包或保留原函数）
- 接入：`AnthropicAdapter.Stream`（anthropic.go:230）、`GeminiAdapter.Stream`（gemini.go:30）——把「构造请求 → 发送 → SSE 解析」部分抽成闭包，外层包 `streamWithReconnect`；注意闭包内变量捕获（baseURL/apiKey/body/req 等）
- 落点：`internal/backend/agent/model/retry.go`（helper，或新建 stream_reconnect.go）、`anthropic.go`、`gemini.go`；openai.go 保持行为不变（或复用 helper 重构，属可选，避免引入回归优先不动）
- 验证：模拟 Anthropic/Gemini 流式连接在输出前断开（如 mock 上游中途断连），确认透明重连 ≤2 次；输出后断开不重连；退避生效

### 批次 5：A2 SSE 逐块超时

- 三适配器 `scanner.Scan()` 循环（anthropic.go:700、gemini.go:308、openai.go chat/responses 两处）加单块 deadline：
  - 每次 `Scan()` 前 `SetReadDeadline(now + chunkTimeout)`，`chunkTimeout = 30s`（常量，代码内）
  - 超时 → 报错，交由批次 4 重连（若未输出）或 router failover / idle watchdog 语义
  - 底层连接不支持 `SetReadDeadline`（如非 net.Conn 包装）时忽略设置失败，保持现状（fallback 不破坏）
- 实现：`http.NewResponseController(resp).SetReadDeadline(...)`（Go 标准库）；在 scanner 循环外包装，或在循环内每次迭代设置
- 与现有 90s idle watchdog（stream_idle.go:32）关系：逐块超时粒度更细（30s），更快失败；watchdog 保留作最后兜底
- 落点：`internal/backend/agent/model/anthropic.go`、`gemini.go`、`openai.go`
- 验证：模拟上游每块间隔 >30s（mock 慢流），确认在 ~30s 报错而非等 90s；正常流不受影响

## 4. 常量默认值清单（本轮全部代码内硬编码）

| 常量 | 值 | 位置 |
|---|---|---|
| `maxStreamReconnects` | 2（已有） | retry.go:207 |
| `chunkTimeout` | 30s（新增） | agent/model 新增常量 |
| `doomLoopThreshold` | 3（新增） | forwarder 新增常量 |
| `doomLoopHardLimit` | 5（新增） | forwarder 新增常量 |
| 404 多渠道重试 | 全部渠道各试 1 次（新增，仅 openai 协议组） | router.go |
| `retry-after-ms` | 毫秒解析，封顶 30s（新增） | retry.go |

## 5. 风险与约束

- **不破坏现有功能**：全部为增量；A1 只补 anthropic/gemini（openai 行为不变）；A2 超时失败走既有恢复链路；B1 仅检测到重复才注入提示、工具成功即清零；B2/B3 仅改错误文案
- **不写测试**：验证用 `go build ./...` + `go vet ./...` + `go test ./...`（仅现有测试，必须保持绿）+ 手动联调清单（见 §6）
- **prefix-cache-stability**：B2 文案变更属一次性前缀失效（保持文案结构稳定）；其余不涉历史重放
- **不改已安装 Cursor 客户端**；C1/C2 不在本轮
- 每批完成后独立提交，提交信息标注批次，便于回退

## 6. 验证清单（每批通用 + 专项）

通用：`go build ./...`、`go vet ./...`、`go test ./...`（现有测试绿）
专项：
- B2：参数非法调用 → tool_result 含引导句；业务错误 → 原文
- B3：未知工具 → 含可用工具列表且与 mode 白名单一致
- B1：连续 3 次同 tool+args → 第 3 次含提示；第 5 次中断；工具成功后清零；正常流无提示
- A4：`retry-after-ms` 429 → 按毫秒退避且封顶
- A3：多渠道首渠道 404 → 换渠道；单渠道 404 → 快速返回可读错误
- A1：anthropic/gemini 输出前断流 → 透明重连 ≤2 次；输出后断流 → 不重连
- A2：上游块间隔 >30s → ~30s 报错；正常流无影响
- 回归：正常对话流、claude-on-openai 升降级、max_tokens 恢复、上下文溢出压缩、shell/turn 恢复（不回归）

## 7. 后续（不在本轮）

- C1 声明式 per-agent 注册表、C2 step 级快照
- 参数配置界面化（硬编码值 → config + 前端）
- opencode 其他可借鉴点（permission arity、编辑自修复策略等）
