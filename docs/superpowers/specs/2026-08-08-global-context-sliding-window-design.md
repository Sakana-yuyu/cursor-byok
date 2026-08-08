# 全局上下文投影与滑动窗口设计

日期：2026-08-08
状态：已批准，按 Reasonix 最新实现校准

## 1. 结论

cursor-byok 不采用一套通用的无状态滑动窗口覆盖所有执行路径，而采用混合架构：

- **父代理 / 主会话**：使用 Reasonix 风格的持久、缓存感知 context projection。低压时继续发送 append-only 的完整 canonical projection；高压时发送“稳定前缀 + 一个滚动摘要 + 必保当前状态/工具链 + 最近尾部”。
- **本地 delegated worker**：使用无状态、结构安全的滑动窗口。它只处理 worker 内存消息，不生成或持久化摘要；provider overflow 时逐次缩小预算再重试。

两条路径共享 token 估算、工具批次结构校验和最终请求预算约束，但不共享持久化生命周期。

## 2. 事实源与边界

`history/<conversationId>/state.json` 与 `context.json` 仍是完整会话事实源。自动上下文压力处理必须满足：

1. 不删除、替换或重排 `ConversationFile.Entries`；
2. 不调用 `ReplaceEntries` 改写 canonical history；
3. 不改变 checkpoint、rewind、审计和 UI 完整历史语义；
4. projection sidecar 只是可丢弃的派生状态，失效时回退 canonical compile；
5. 用户显式 `/summarize` 的手动 compaction 保持现有可见行为，仍可替换 canonical history。

现有 stale tool-result snip/prune 会修改 canonical 工具结果，第一阶段保留为独立的显式历史维护机制；它不是 projection 本身。

## 3. 父代理持久 Projection

### 3.1 低压模式

完整编译结果在最终 provider-visible rewrite 后满足请求预算时：

- 发送完整 canonical projection；
- 不创建或更新滚动摘要；
- 保持最大 append-only 稳定前缀；
- 已存在但不再匹配的 sidecar 不参与本次请求。

### 3.2 高压模式

当完整请求超过投影压力线或无法为输出留出安全空间时，构造：

```text
stable prefix
+ one rolling <conversation_summary>
+ required current state / current turn / unresolved tool chain
+ recent complete turns and tool batches
```

选择必须直接基于 canonical `HistoryEntry` 的 turn/request/tool 归属，而不是从平铺 `[]Message` 猜 turn、request 或工具所属关系。projection 先生成只读的 entry-layer conversation clone，再交给现有 projector/compiler 重新投影；完整 turn、assistant tool calls、对应 results 和 reasoning carrier 不可拆分。

### 3.3 稳定前缀与滚动摘要

- 稳定前缀只覆盖 sidecar 指纹确认过的 canonical 前缀；其内容和相对顺序不能改变。
- 每个 projection 最多插入一个滚动摘要。更新时用旧摘要加新覆盖区间生成替代摘要，不级联堆叠多条摘要。
- 最近尾部按 canonical 顺序追加；后续请求优先仅延长尾部，直到再次达到压力线。
- 如果投影删除了原 stable frontier 之前的任意 provider-visible 消息，`StableMessageCount` 必须降为 `0`；不能仅按新长度 clamp。

### 3.4 Sidecar

父会话目录新增 `context-projection.json`，建议 schema：

```json
{
  "schema_version": 1,
  "conversation_id": "...",
  "root_conversation_id": "...",
  "parent_conversation_id": "...",
  "model_key": "...",
  "context_version": 42,
  "covered_entry_seq": 30,
  "covered_prefix_fingerprint": "sha256:...",
  "summary": "...",
  "created_at": "...",
  "updated_at": "..."
}
```

sidecar 使用现有 atomic JSON writer。读取后必须 fail-closed 校验：

- schema version；
- conversation ID；
- root/parent lineage；
- model key；
- `covered_entry_seq` 在当前 canonical 范围内；
- 当前 covered canonical prefix 的 fingerprint；
- sidecar `context_version <= conversation.ContextVersion`。

任一项失败时忽略 sidecar、记录原因，并从 canonical history 重建；不能尝试“修补”旧摘要。canonical history 发生 rewind、manual compaction、fork lineage 变化或前缀内容变化时会自然失效。

## 4. Entry-Layer Projection 契约

父 projection 不给扁平的 `CompiledConversation.Messages` 反向补 provenance，而使用 canonical entry 作为唯一选择边界：

- 按 `TurnSeq`、`RequestID`、entry kind 与 tool call/result ID 识别完整不可拆组；
- stable head、被摘要的中段和 recent tail 都从 canonical entries 直接选择；
- projection clone 清除旧请求 usage 与 cache-frontier 元数据，并且绝不回写 canonical conversation；
- synthetic `context_projection_summary` 只存在于 projection clone；
- clone 完成后重新调用现有 projector/compiler，由同一套 normalization 继续负责 provider-visible tool/reasoning 结构。

如果 entry 分组或重新编译无法保持完整工具批次与 reasoning 载体，则 fail-closed，不启用父 projection。不得从 provider-visible 平铺消息反推 canonical turn。

## 5. 本地 Delegated Worker 滑窗

worker 不读取或写入父 projection sidecar，也不调用 LLM 生成摘要。每个 pass：

1. clone 当前内存消息；
2. 可先截断旧的大型 tool result，最近批次不截断；
3. 将 system/task prompt、普通完整 turn、assistant tool-call + 连续 results 分组；
4. 永远保留 system、任务 prompt、当前未闭合链和最近完整批次；
5. 从最老的可选完整组开始滑出，直到满足本次预算；
6. 重建后严格验证 tool call/result 和 reasoning 结构，不调用 normalization 静默修复。

provider overflow 重试必须使用递减预算，例如 `100% -> 80% -> 64%`。同一确定性输入不得以相同窗口重复发送。重试次数继续受现有上限约束。

## 6. 最终请求预算与顺序

所有 provider-visible rewrite 必须在最终 token 估算和输出预算之前完成，包括：

- canonical compile；
- stale tool-result maintenance 后的 recompile；
- parent projection 或 delegated window；
- vision proxy 图片替换；
- `see_image` 等工具描述过滤；
- provider prompt replay suppression。

然后对最终 `Messages + Tools` 估算输入，并计算 `MaxTokens`。共享上下文窗口 provider 必须满足：

```text
estimated_final_input + max_output + reserve <= context_window
```

projection 后不得再用 `conversation.TokenDetailsUsedTokens` 作为输入 token 下限；该字段是旧请求 usage，可能对应完整历史，会把新投影的输出预算错误压到 1。

未知模型窗口继续使用现有 64K fallback。真实 provider overflow 仍可校准渠道窗口，但父代理应优先更新/收紧 projection，而不是自动替换 canonical entries。

## 7. 自动摘要与手动 Compaction

- `manual`：保持现有 pre-compact hook、Summary UI、`ReplaceEntries`、checkpoint 和 turn 结束语义。
- `auto_projection`：可复用现有异步摘要 provider 和 actor completion 生命周期，但摘要完成后写 `context-projection.json`，不追加 compaction request/failed/summary canonical entry，不调用 `applyCompactionPlan`。
- `context_overflow`：先缩小已知真实窗口和 projection/window 预算；只有用户显式手动 compaction 才改变 canonical history。

自动 projection 摘要失败时保留 canonical history，记录诊断，并尝试更小的无摘要 recent-tail 投影；仍无法装入时返回明确 context overflow。

## 8. 缓存

- response cache 已对最终 messages、stable count 和 max tokens 取指纹；projection 在请求构造前完成即可。
- request knobs 只用于诊断，不进入语义 cache key。
- 父 projection 的目标是保持稳定前缀和单摘要位置稳定；更新摘要会形成一次新的 cache frontier，之后尾部继续 append-only。
- delegated worker 是临时执行，不持久化 cache projection 状态。

## 9. 诊断

每个 parent/delegated pass 记录 `context_projection_applied`，至少包含：

- execution kind、mode（full/projection/window）；
- context window、reserve、input/output budget；
- before/after tokens 和 messages；
- covered entry seq、fingerprint 是否命中、sidecar invalidation reason；
- retained/dropped turns、snipped tool results；
- stable count before/after；
- overflow retry ordinal 和预算比例。

## 10. 验收标准

1. 低压父会话保持完整 append-only projection。
2. 高压父会话使用稳定前缀、单滚动摘要、必保当前状态和最近尾部。
3. 自动压力处理不修改 `state.json + context.json` canonical entries。
4. sidecar 前缀、模型、lineage 或版本不匹配时 fail-closed。
5. tool call/result、并行批次和 reasoning carrier 不被拆分。
6. delegated overflow 每次重试都实际缩小窗口。
7. 最终请求满足共享窗口输入、输出和 reserve 总和约束。
8. vision/tool rewrite 后重新估算，旧 provider usage 不覆盖最终 projection 估算。
9. 手动 `/summarize` 的现有用户可见语义无回归。
10. rewind、fork、checkpoint、UI 完整历史和缓存行为有回归测试。
