# 全局上下文滑动窗口设计

日期：2026-08-08
状态：已批准

## 1. 背景

cursor-byok 当前的主代理上下文管理主要依赖：

1. provider replay 阶段对旧工具结果做内容级截断；
2. 上下文压力达到阈值后，对陈旧工具结果执行持久化 snip/prune；
3. 仍超预算时生成 `compacted_summary`，并通过正式 compaction 替换持久化历史；
4. 根据剩余上下文动态压低 provider 的最大输出 token；
5. provider 返回上下文超限时，自适应降低模型窗口配置并重试。

该机制没有持续生效的通用 token-aware 滑动窗口。正常请求会持续携带所有可回放历史，直到接近上限后突然进入摘要 compaction。长时间会话、多 provider pass 和大量工具调用因此会出现：

- 输入 token 线性增长；
- 请求延迟和成本持续上升；
- 上下文接近上限时才发生大幅历史改写；
- 本地 delegated worker 需要依赖独立的简化删除逻辑；
- 不同执行路径的上下文策略不一致。

本设计引入统一的全局上下文管理器，在不删除完整历史的前提下，为每次可控 provider 请求生成结构安全、token-aware、确定性的模型可见滑动窗口。

## 2. 目标

### 2.1 产品目标

采用“稳定性优先”策略：

- 长时间会话的 provider 输入不再无限增长；
- 当前任务、最近完整轮次、未闭合工具链和结构化任务状态始终可见；
- 历史滑出窗口时不破坏恢复、checkpoint、审计和 UI 历史；
- 滑动窗口全自动工作，不新增复杂用户配置；
- 主代理与本地 delegated worker 使用同一套核心选择算法；
- 现有 compaction 和 context overflow 恢复继续作为硬兜底。

### 2.2 非目标

第一阶段不包含：

- 每次滑动时调用模型生成滚动摘要；
- 删除或直接裁剪 `context.json` 的完整历史；
- 直接控制 Cursor 原生子代理的 provider 请求上下文；
- 为用户暴露目标比例、轮数和工具上限等高级参数；
- 重写现有 checkpoint、rewind 或 summary archive 协议。

## 3. 关键原则

1. **完整历史是事实源**：`ConversationFile.Entries`、`context.json` 和 checkpoint 不被滑动窗口删除。
2. **窗口只作用于 provider projection**：每次请求基于完整 compile 结果生成模型可见子集。
3. **按结构组裁剪，不按单条消息裁剪**：完整 turn、tool call/result batch 和 reasoning carrier 不可拆分。
4. **确定性**：相同输入、模型窗口和策略必须生成相同窗口。
5. **保留顺序稳定**：只从最老的可选结构组滑出，不改写仍保留消息的内容与相对顺序。
6. **安全回退**：分组或预算判断失败时，不冒险裁剪，退回现有 compaction/overflow 流程。
7. **滑动与摘要分层**：日常平稳滑动；必保内容无法装入硬窗口时才执行持久化摘要 compaction。

## 4. 总体架构

新增统一组件 `ContextWindowManager`：

```text
ConversationFile（完整历史）
        ↓
DefaultPromptCompiler / ProjectPromptReplay
        ↓
CompiledConversation（完整模型投影）
        ↓
ContextWindowManager（新增）
        ↓
Windowed CompiledConversation
        ↓
ProviderRequest
```

`ContextWindowManager` 不依赖磁盘存储，不修改会话。输入为已编译消息、稳定前缀信息、模型上下文窗口和策略；输出为窗口后的消息、修正后的 `StableMessageCount`、预算统计和是否需要持久化 compaction 的判定。

建议接口：

```go
type ContextWindowManager interface {
    Apply(input WindowInput) WindowResult
}

type WindowInput struct {
    Messages             []modeladapter.Message
    StableMessageCount   int
    ContextWindowTokens  int
    TargetRatio          float64
    HardRatio            float64
    ReserveTokens        int
    MinimumRecentTurns   int
    ExecutionKind        WindowExecutionKind
}

type WindowResult struct {
    Messages                 []modeladapter.Message
    StableMessageCount       int
    Stats                    ContextWindowStats
    NeedsPersistentCompaction bool
    FallbackReason           string
}
```

具体类型名可在实施阶段按仓库约定调整，但职责边界必须保持。

## 5. 预算模型

### 5.1 主代理默认预算

- 目标窗口：模型上下文窗口的约 `65%`；
- 硬窗口：模型上下文窗口的约 `80%`，并扣除现有安全 reserve；
- 当前 `resolveProviderOutputBudget` 继续计算最终输出 token；
- manager 使用与 provider 请求一致的 token 估算函数，避免两套口径。

目标窗口是软约束。必保内容和最近完整轮次可以突破目标窗口，但不能突破硬窗口。若必保内容超过硬窗口，则返回 `NeedsPersistentCompaction=true`。

### 5.2 本地 delegated worker 默认预算

本地 worker 使用相同 manager 和结构算法，但策略更保守地控制增长：

- 目标窗口约 `55%`；
- 硬窗口与主代理一致，约 `80% - reserve`；
- system、任务 prompt 和最近工具批次必保；
- 仅裁剪 worker 内存 messages，不修改父会话。

### 5.3 未知模型窗口

模型未配置 `contextWindowTokens` 时继续使用现有 `64,000` fallback。真实 provider overflow 仍走现有窗口减半、持久化配置和重试机制。

## 6. 结构分组

窗口管理器不能直接按单条 message 删除。输入消息必须先转换为不可拆分的语义组。

### 6.1 `system_anchor`

包含：

- system prompt；
- 用户规则；
- 激活 skills；
- MCP/tool schema 相关稳定上下文。

该组永远保留。

### 6.2 `summary_anchor`

包含最新 `compacted_summary` 投影出的 `<conversation_summary>` 消息。存在时永远保留，因为旧原文可能已经被正式 compaction 替换。

### 6.3 `turn_input`

包含同一 turn 的：

- request context；
- user message；
- prompt context；
- mode/run metadata 的模型可见投影。

当前 turn 的输入永远保留。历史 turn 的输入随完整 turn 一起选择。

### 6.4 `assistant_text`

普通 assistant 回复。它属于所在完整 turn，不单独跨 turn 保留。

### 6.5 `tool_batch`

包含一个完整工具批次：

- assistant tool calls；
- 对应的全部 tool results；
- 并行调用批次的所有成员；
- 必需的 reasoning content/signature；
- provider reasoning item ID/status/summary；
- 必要的 reasoning carrier。

该组不可拆分。无法验证调用与结果配对时，该组退化为必保，不冒险裁剪。

### 6.6 `runtime_state`

包含当前有效的：

- plan；
- Todo；
- 模式与任务状态；
- 结构化阻塞或完成状态。

当前有效状态永远保留。

## 7. 窗口选择算法

### 7.1 必保阶段

先按原消息顺序加入：

1. 所有 `system_anchor`；
2. 最新 `summary_anchor`；
3. 当前 turn 的全部稳定输入；
4. 当前未闭合工具链；
5. 当前 turn 最近完成的完整工具批次；
6. 当前有效 `runtime_state`。

统计必保 token。若已经超过硬预算，返回 `NeedsPersistentCompaction=true`，不拆分结构。

### 7.2 最近轮保护

从最新向最旧加入完整 turn，至少保留最近 2 个可用完整 turn。最近轮保护是软目标：如果第二个最近 turn 会突破硬预算，则只保留能安全装入的部分，但不能拆分其中的结构组。

### 7.3 可选组滑动

剩余历史从最新向最旧加入，直到达到目标预算。达到目标预算后，不再加入更旧组。

旧内容的淘汰优先级：

1. 已有 projection 截断的旧大型 tool result；
2. 更旧的完整工具 turn；
3. 更旧的普通 assistant/user turn；
4. 永不淘汰 summary、当前任务和当前工具链。

### 7.4 消息重建

选择完成后：

- 按原始索引恢复消息顺序；
- 再运行现有工具序列 normalization；
- 校验不存在 orphan tool result、dangling call 或 reasoning carrier 缺失；
- 重新估算 token；
- 若结构校验失败，安全回退完整 compile 并请求持久化 compaction。

## 8. 主代理接入

主代理 `driveProvider` 调整为：

1. 快照完整 `ConversationFile`；
2. 同步模型 context window；
3. 持久化派生 prompt context；
4. 完整 compile；
5. 运行现有陈旧工具结果维护；
6. 若历史维护发生修改，重新快照并 compile；
7. 调用 `ContextWindowManager.Apply`；
8. 若 manager 判定无法装入硬窗口，运行现有摘要 compaction；
9. compaction 完成后重新 compile 和应用窗口；
10. 使用窗口结果计算输出预算并构造 `ProviderRequest`。

建议将当前 `maybeCompactBeforeProvider` 的职责拆分为：

- `maintainHistoryBeforeProvider`：陈旧 tool result snip/prune 等轻量维护；
- `compactHistoryIfWindowCannotFit`：窗口无法满足硬约束时执行摘要 compaction。

该拆分避免窗口正常工作时过早改写持久化历史。

## 9. 本地 delegated worker 接入

本地 worker 当前拥有独立的 `compactDelegatedMessagesBeforePass`。第一阶段将其核心选择逻辑迁移到统一 manager：

- worker 每个 provider pass 前调用 manager；
- 输入为 worker 内存 messages；
- system 与任务 prompt 必保；
- assistant tool calls 和连续 tool results 分为完整 batch；
- 最近工具批次与最近完整轮次优先保留；
- 旧大 tool result 仍可先做内容级截断；
- overflow retry 继续存在；
- worker 临时上下文仍不写入父 `context.json`。

原 `delegation_compaction.go` 中可复用的 token 估算、tool-result snip 和 batch 识别逻辑应迁移或封装为 manager 的通用能力，避免双实现漂移。

## 10. Cursor 原生子代理范围

Cursor 原生子代理的真实 provider 请求由 Cursor 客户端管理，后端目前无法访问其完整消息列表。因此第一阶段不承诺直接裁剪原生子代理窗口。

可做的间接优化：

- 避免 Task prompt 重复注入父历史已有说明；
- 控制父侧聚合结果长度；
- 在任务派发 metadata 中记录期望的上下文策略，为未来协议支持预留接口；
- 原生子代理失败不阻塞主代理窗口管理落地。

## 11. Prefix cache 与 response cache

### 11.1 Prefix cache

窗口算法必须只从最老可选组滑出，保持仍保留消息内容和顺序不变。

`StableMessageCount` 必须限制为窗口实际保留的稳定消息数量，不能指向已滑出的消息。窗口结果应返回修正后的稳定消息计数，并保证：

```text
0 <= StableMessageCount <= len(Messages)
```

相同输入和预算生成相同窗口，从而保持稳定前缀可预测。

### 11.2 Local response cache

窗口在构造 `ProviderRequest` 和计算 cache key 前应用。当前 response cache key 已包含最终 messages，因此：

- 不需要为窗口新增独立 key 维度；
- 不同窗口自然产生不同 key；
- 相同窗口可以正常复用缓存；
- 窗口统计只放入诊断 knobs，不参与语义区分。

## 12. 与现有 compaction 的关系

滑动窗口不替代 compaction。

- 滑动窗口：每个请求的非持久化模型视图，平稳控制输入增长；
- stale tool snip/prune：维护过大的持久化工具结果；
- summary compaction：必保上下文无法放入硬窗口时，持久化折叠旧历史；
- overflow recovery：真实 provider 上限与配置不一致时的最后恢复。

推荐顺序：

```text
projection truncation
→ history maintenance
→ sliding window
→ persistent summary compaction（必要时）
→ provider overflow recovery（最后兜底）
```

## 13. 观测与诊断

每个 provider pass 记录 `context_window_applied` runtime 事件：

- `request_id`
- `conversation_id`
- `model_call_id`
- `provider_pass`
- `execution_kind`
- `model_context_tokens`
- `target_budget_tokens`
- `hard_budget_tokens`
- `before_tokens`
- `after_tokens`
- `before_messages`
- `after_messages`
- `required_group_tokens`
- `retained_turns`
- `dropped_turns`
- `snipped_tool_results`
- `stable_message_count_before`
- `stable_message_count_after`
- `needs_persistent_compaction`
- `fallback_reason`

`provider_request_prepared.request_knobs` 增加精简诊断：

```json
{
  "context_window": {
    "applied": true,
    "before_tokens": 120000,
    "after_tokens": 72000,
    "dropped_turns": 6,
    "retained_turns": 3
  }
}
```

请求明细 UI 可在后续迭代展示这些字段，但不属于第一阶段的必要功能。

## 14. 错误处理与安全退化

1. 分组失败：不裁剪，记录 `fallback_reason=grouping_failed`，交给现有 compaction。
2. 结构校验失败：不发送残缺窗口，退回完整 compile 并请求 compaction。
3. 必保内容超过硬预算：返回 `NeedsPersistentCompaction=true`。
4. compaction 后仍超限：沿用 context overflow 自动窗口减半和重试。
5. token 估算误差：硬窗口保留安全余量，真实 overflow 继续校准。
6. manager 内部错误不得直接令正常请求失败；必须记录日志并安全回退旧流程。
7. 未识别消息结构默认必保，避免未来新增 provider 消息类型被静默删除。

## 15. 测试设计

### 15.1 纯算法测试

- 当前 turn 永远保留；
- 最新 summary 永远保留；
- 最近完整 turn 优先保留；
- 最旧 turn 优先滑出；
- tool call/result batch 不拆；
- 并行 tool batch 不拆；
- reasoning signature/carrier 不丢；
- runtime state 保留；
- `StableMessageCount` 不越界；
- 必保内容超硬预算时返回 compaction-needed；
- 相同输入得到确定性相同结果。

### 15.2 主代理集成测试

- 长会话每个 provider pass 控制在目标预算附近；
- 最终 `ProviderRequest.Messages` 为窗口结果；
- `context.json` 和 `ConversationFile.Entries` 不被窗口修改；
- manager 正常工作时不触发持久化 compaction；
- manager 无法装入硬预算时触发现有 compaction；
- compaction 后重新应用窗口并成功请求。

### 15.3 本地 delegated worker 测试

- 多 pass 工具轨迹稳定滑动；
- 最近工具 batch 完整；
- 旧大 tool result 先截断；
- overflow retry 可恢复；
- worker 窗口不修改父历史。

### 15.4 回归测试

- OpenAI Responses reasoning signature；
- 并行工具调用；
- GenerateImage 调用/result 过滤；
- prefix cache frontier；
- response cache key；
- compaction、rewind、checkpoint、取消 turn；
- 未识别工具消息安全退化。

## 16. 验收标准

1. 长会话正常请求输入稳定在目标预算附近，不再线性增长到硬上限。
2. 不出现 orphan tool result、dangling tool call 或 reasoning signature 错误。
3. 滑动窗口不会删除或替换 `context.json` 原始历史。
4. 当前任务、当前轮、未闭合工具链、最新 summary 和结构化状态始终可见。
5. `StableMessageCount` 和最终消息列表一致，缓存行为确定且可诊断。
6. 本地 delegated worker 不再因多 pass 工具结果持续增长而频繁 context overflow。
7. Cursor 原生子代理不受第一阶段变更破坏。
8. 窗口管理失败时，现有 compaction/overflow 流程仍可完成请求。
9. 每个 provider pass 都有可查询的窗口统计。

## 17. 实施边界与阶段

建议拆成四个实施阶段，但保留在同一设计范围内：

1. **核心 manager 与纯算法测试**：消息分组、预算选择、结构校验、统计。
2. **主代理接入**：窗口应用、compaction 分层、请求 knobs、runtime 日志。
3. **本地 delegated worker 接入**：替换独立选择逻辑，保留 overflow 恢复。
4. **缓存与回归验证**：prefix cache、response cache、reasoning/tool 序列、长期会话压力测试。

每一阶段均需保持现有流程可运行，不允许在中间状态破坏 provider 请求结构。
