# Global Context Sliding Window Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为主代理和本地 delegated worker 引入统一、结构安全、token-aware 的 provider projection 滑动窗口，使长会话输入稳定在目标预算附近，同时保持完整持久化历史、工具配对、reasoning metadata 和缓存行为正确。

**Architecture:** 新增纯内存 `ContextWindowManager`，在完整 `CompiledConversation` 与 `ProviderRequest` 之间按 system/summary/turn/tool-batch/runtime-state 结构分组并选择窗口；主代理无法装入硬预算时再触发现有持久化 compaction，本地 delegated worker 复用相同管理器但使用更低目标比例。窗口决策在 response cache key 计算前完成，并返回修正后的 `StableMessageCount` 与统一诊断统计。

**Tech Stack:** Go 1.26、现有 `modeladapter.Message` / `CompiledConversation`、现有 token estimator、forwarder runtime/provider debug JSONL、Go testing。

## Global Constraints

- 稳定性优先；不得为了命中目标预算拆开 tool call/result batch 或 reasoning carrier。
- 滑动窗口只影响 provider projection，不删除或替换 `ConversationFile.Entries`、`context.json`、checkpoint、summary archive。
- 主代理目标窗口为上下文的 `65%`，本地 delegated worker 目标窗口为 `55%`；硬窗口为上下文的 `80%` 并扣除现有 reserve。
- 当前 turn、最新 `compacted_summary`、未闭合工具链、当前有效 plan/Todo/runtime state 必须保留。
- 相同输入、窗口和策略必须产生确定性相同结果。
- `0 <= StableMessageCount <= len(Messages)`；不能指向已滑出的消息。
- 原生 Cursor 子代理第一阶段不直接裁剪 provider 上下文。
- manager 分组/校验失败时必须安全退回现有 compaction/overflow 流程，不得发送残缺消息。
- 不新增第三方依赖，不新增用户设置项，不修改 provider 协议。

---

## File Structure

### 新建文件

- `internal/backend/forwarder/context_window.go`
  - 定义窗口输入、策略、结果、统计与 `ContextWindowManager.Apply` 主流程。
- `internal/backend/forwarder/context_window_groups.go`
  - 将 `[]modeladapter.Message` 解析为不可拆分的结构组；识别 system、summary、turn、tool batch、runtime state。
- `internal/backend/forwarder/context_window_validate.go`
  - 重建后结构校验、tool call/result 配对验证、reasoning carrier 安全检查。
- `internal/backend/forwarder/context_window_test.go`
  - 纯算法与结构安全测试。
- `internal/backend/forwarder/context_window_service_test.go`
  - 主代理接入、持久化不变、compaction fallback、debug knobs 测试。
- `internal/backend/forwarder/context_window_delegation_test.go`
  - delegated worker 复用、最近工具批次、overflow retry 测试。

### 修改文件

- `internal/backend/forwarder/types.go`
  - 在 `Service` 增加 manager 依赖；定义或引用公共窗口类型。
- `internal/backend/forwarder/service.go`
  - 初始化 manager；在 `driveProvider` 中应用窗口；修正 `StableMessageCount`；写入 request knobs/debug。
- `internal/backend/forwarder/compaction.go`
  - 拆分轻量历史维护与持久化摘要 compaction 触发职责。
- `internal/backend/forwarder/delegation_local.go`
  - delegated provider pass 前调用统一 manager。
- `internal/backend/forwarder/delegation_compaction.go`
  - 保留通用 tool-result snip 能力；删除被 manager 取代的独立 drop 选择算法。
- `internal/backend/forwarder/delegation_compaction_test.go`
  - 将旧删除式窗口测试迁移为统一 manager 行为测试。
- `internal/backend/forwarder/delegation_local_compaction_test.go`
  - 更新 worker 接入测试和日志统计断言。
- `internal/backend/forwarder/token_estimator.go`
  - 暴露/补充 manager 需要的单消息、工具描述和预算估算 helper。
- `internal/backend/forwarder/provider_cache_fingerprint_test.go`
  - 验证窗口后的 messages 与 stable count 进入 cache key。
- `internal/backend/forwarder/prefix_cache_test.go`（若仓库无此文件则新建）
  - 验证稳定前缀 frontier 不越界且确定。

---

### Task 1: 定义窗口类型、预算策略与最小纯算法骨架

**Files:**
- Create: `internal/backend/forwarder/context_window.go`
- Test: `internal/backend/forwarder/context_window_test.go`
- Modify: `internal/backend/forwarder/types.go`

**Interfaces:**
- Produces:

```go
type contextWindowExecutionKind string

const (
    contextWindowExecutionParent    contextWindowExecutionKind = "parent"
    contextWindowExecutionDelegated contextWindowExecutionKind = "delegated"
)

type contextWindowPolicy struct {
    TargetRatio        float64
    HardRatio          float64
    ReserveTokens      int64
    MinimumRecentTurns int
}

type contextWindowInput struct {
    Messages            []modeladapter.Message
    StableMessageCount  int
    ToolTokens          int64
    ContextWindowTokens int64
    ExecutionKind       contextWindowExecutionKind
    Policy              contextWindowPolicy
}

type contextWindowStats struct {
    Applied                  bool
    BeforeTokens             int64
    AfterTokens              int64
    BeforeMessages           int
    AfterMessages            int
    RequiredGroupTokens      int64
    TargetBudgetTokens       int64
    HardBudgetTokens         int64
    RetainedTurns            int
    DroppedTurns             int
    SnippedToolResults       int
    StableMessageCountBefore int
    StableMessageCountAfter  int
    NeedsCompaction          bool
    FallbackReason           string
}

type contextWindowResult struct {
    Messages              []modeladapter.Message
    StableMessageCount    int
    Stats                 contextWindowStats
    NeedsCompaction       bool
}

type ContextWindowManager struct{}

func NewContextWindowManager() *ContextWindowManager
func (manager *ContextWindowManager) Apply(input contextWindowInput) contextWindowResult
func parentContextWindowPolicy(reserveTokens int64) contextWindowPolicy
func delegatedContextWindowPolicy(reserveTokens int64) contextWindowPolicy
```

- Consumers: Tasks 2–8.

- [ ] **Step 1: 写预算与 no-op 行为的失败测试**

在 `context_window_test.go` 添加：

```go
func TestContextWindowBudgetPolicies(t *testing.T) {
    parent := parentContextWindowPolicy(10_000)
    if parent.TargetRatio != 0.65 || parent.HardRatio != 0.80 || parent.MinimumRecentTurns != 2 {
        t.Fatalf("parent policy = %#v", parent)
    }
    delegated := delegatedContextWindowPolicy(10_000)
    if delegated.TargetRatio != 0.55 || delegated.HardRatio != 0.80 || delegated.MinimumRecentTurns != 2 {
        t.Fatalf("delegated policy = %#v", delegated)
    }
}

func TestContextWindowNoopWhenUnderTarget(t *testing.T) {
    manager := NewContextWindowManager()
    messages := []modeladapter.Message{
        {Role: "system", Content: "system"},
        {Role: "user", Content: "hello"},
    }
    result := manager.Apply(contextWindowInput{
        Messages: messages,
        StableMessageCount: 2,
        ContextWindowTokens: 64_000,
        Policy: parentContextWindowPolicy(10_000),
    })
    if result.Stats.Applied {
        t.Fatal("small input unexpectedly windowed")
    }
    if !reflect.DeepEqual(result.Messages, messages) {
        t.Fatalf("messages changed: %#v", result.Messages)
    }
    if result.StableMessageCount != 2 {
        t.Fatalf("stable count = %d", result.StableMessageCount)
    }
}
```

- [ ] **Step 2: 运行测试并确认 RED**

Run:

```bash
go test ./internal/backend/forwarder -run 'TestContextWindowBudgetPolicies|TestContextWindowNoopWhenUnderTarget' -v
```

Expected: FAIL，提示 `NewContextWindowManager` / policy 类型未定义。

- [ ] **Step 3: 实现最小类型和预算计算**

在 `context_window.go`：

```go
const (
    parentContextWindowTargetRatio    = 0.65
    delegatedContextWindowTargetRatio = 0.55
    contextWindowHardRatio            = 0.80
    contextWindowMinimumRecentTurns   = 2
)

func NewContextWindowManager() *ContextWindowManager {
    return &ContextWindowManager{}
}

func parentContextWindowPolicy(reserveTokens int64) contextWindowPolicy {
    return contextWindowPolicy{
        TargetRatio: parentContextWindowTargetRatio,
        HardRatio: contextWindowHardRatio,
        ReserveTokens: reserveTokens,
        MinimumRecentTurns: contextWindowMinimumRecentTurns,
    }
}

func delegatedContextWindowPolicy(reserveTokens int64) contextWindowPolicy {
    policy := parentContextWindowPolicy(reserveTokens)
    policy.TargetRatio = delegatedContextWindowTargetRatio
    return policy
}

func contextWindowBudgets(window int64, policy contextWindowPolicy) (target int64, hard int64) {
    if window <= 0 {
        return 0, 0
    }
    target = int64(float64(window)*policy.TargetRatio) - policy.ReserveTokens
    hard = int64(float64(window)*policy.HardRatio) - policy.ReserveTokens
    if target < 1 { target = 1 }
    if hard < target { hard = target }
    return target, hard
}
```

`Apply` 第一版只计算 before tokens；低于目标时 clone 并原样返回。

- [ ] **Step 4: 运行测试并确认 GREEN**

Run:

```bash
go test ./internal/backend/forwarder -run 'TestContextWindowBudgetPolicies|TestContextWindowNoopWhenUnderTarget' -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/backend/forwarder/context_window.go internal/backend/forwarder/context_window_test.go internal/backend/forwarder/types.go
git commit -m "feat(context): add sliding window policy types"
```

---

### Task 2: 实现结构分组与 tool batch 配对

**Files:**
- Create: `internal/backend/forwarder/context_window_groups.go`
- Modify: `internal/backend/forwarder/context_window_test.go`

**Interfaces:**
- Consumes: Task 1 的 `contextWindowInput`。
- Produces:

```go
type contextWindowGroupKind string

const (
    contextWindowGroupSystem       contextWindowGroupKind = "system_anchor"
    contextWindowGroupSummary      contextWindowGroupKind = "summary_anchor"
    contextWindowGroupTurn         contextWindowGroupKind = "turn"
    contextWindowGroupToolBatch    contextWindowGroupKind = "tool_batch"
    contextWindowGroupRuntimeState contextWindowGroupKind = "runtime_state"
    contextWindowGroupUnknown      contextWindowGroupKind = "unknown"
)

type contextWindowGroup struct {
    Kind          contextWindowGroupKind
    Start         int
    End           int // inclusive
    Tokens        int64
    Required      bool
    TurnOrdinal   int
    Stable        bool
    ToolCallIDs   []string
}

func groupContextWindowMessages(messages []modeladapter.Message, stableCount int) ([]contextWindowGroup, error)
```

- [ ] **Step 1: 写 tool batch 不拆分的失败测试**

```go
func TestGroupContextWindowMessagesKeepsParallelToolBatchTogether(t *testing.T) {
    messages := []modeladapter.Message{
        {Role: "system", Content: "system"},
        {Role: "user", Content: "inspect"},
        {Role: "assistant", ToolCalls: []modeladapter.ToolCall{
            {ID: "call-a", Type: "function", Function: modeladapter.ToolCallFunction{Name: "Read", Arguments: `{}`}},
            {ID: "call-b", Type: "function", Function: modeladapter.ToolCallFunction{Name: "Grep", Arguments: `{}`}},
        }},
        {Role: "tool", ToolCallID: "call-a", Name: "Read", Content: "a"},
        {Role: "tool", ToolCallID: "call-b", Name: "Grep", Content: "b"},
    }
    groups, err := groupContextWindowMessages(messages, len(messages))
    if err != nil { t.Fatal(err) }
    batch := findGroup(groups, contextWindowGroupToolBatch)
    if batch.Start != 2 || batch.End != 4 {
        t.Fatalf("tool batch = %#v", batch)
    }
    if !reflect.DeepEqual(batch.ToolCallIDs, []string{"call-a", "call-b"}) {
        t.Fatalf("tool ids = %#v", batch.ToolCallIDs)
    }
}
```

再添加：

- tool result 无匹配 call → 分组返回 error；
- assistant call 缺 result → 该 batch `Required=true`；
- reasoning signature 位于 assistant call 上时保留在同一 batch；
- system message 独立 required group；
- `<conversation_summary>` user message识别为 summary required group。

- [ ] **Step 2: 运行测试并确认 RED**

```bash
go test ./internal/backend/forwarder -run 'TestGroupContextWindowMessages' -v
```

Expected: FAIL，分组函数未定义。

- [ ] **Step 3: 实现确定性分组**

实现规则：

1. 连续 `role=system` 各自形成 system required group；
2. `role=user` 且内容包含 `<conversation_summary>` 形成 summary required group；
3. assistant 含 `ToolCalls` 时，向后收集连续 tool messages；
4. tool IDs 必须属于该 assistant calls；并行调用按 assistant 中顺序保存；
5. 缺少任一 result 时 group required；孤立 tool result 返回错误；
6. 普通 user 开启新 turn；后续普通 assistant 文本属于该 turn；
7. 未识别结构形成 required unknown group；
8. group token 使用 `estimateModelMessagesTokens(messages[start:end+1])`；
9. `Stable=true` 仅当 `End < stableCount`。

不得对消息内容做任何修改。

- [ ] **Step 4: 运行分组测试**

```bash
go test ./internal/backend/forwarder -run 'TestGroupContextWindowMessages' -v
```

Expected: PASS。

- [ ] **Step 5: 运行 projector 工具序列回归**

```bash
go test ./internal/backend/forwarder -run 'Test.*Replay.*Tool|Test.*Tool.*Replay|Test.*Reasoning' -v
```

Expected: PASS；若正则未命中任何测试，改为运行整个包，不得将“无测试”视为通过。

- [ ] **Step 6: 提交**

```bash
git add internal/backend/forwarder/context_window_groups.go internal/backend/forwarder/context_window_test.go
git commit -m "feat(context): group replay messages safely"
```

---

### Task 3: 实现 token-aware 选择、重建和 StableMessageCount 修正

**Files:**
- Modify: `internal/backend/forwarder/context_window.go`
- Create: `internal/backend/forwarder/context_window_validate.go`
- Modify: `internal/backend/forwarder/context_window_test.go`

**Interfaces:**
- Consumes: `groupContextWindowMessages`。
- Produces:

```go
func selectContextWindowGroups(groups []contextWindowGroup, target int64, hard int64, minimumRecentTurns int) contextWindowSelection
func rebuildContextWindowMessages(messages []modeladapter.Message, groups []contextWindowGroup, selected map[int]struct{}) []modeladapter.Message
func validateContextWindowMessages(messages []modeladapter.Message) error
func recomputeWindowStableMessageCount(groups []contextWindowGroup, selected map[int]struct{}, originalStable int) int
```

- [ ] **Step 1: 写滑动顺序和必保内容测试**

构造至少 6 个完整 turn，每个 turn 内容足够大，断言：

```go
func TestContextWindowDropsOldestOptionalTurnsFirst(t *testing.T) {
    // system + six user/assistant turns; target only fits system + last two turns.
    // Assert first four turns are absent, last two remain, original order unchanged.
}
```

增加测试：

- current/dangling tool batch 超目标仍保留；
- summary anchor 永远保留；
- required tokens 超 hard → `NeedsCompaction=true`，返回未裁剪消息；
- 至少保留最近 2 个可装入的完整 turn；
- 输出 token 不超过 hard；
- `StableMessageCount` 等于窗口中从开头连续保留的原稳定消息数量，且不越界；
- 相同输入执行两次结果完全相同。

- [ ] **Step 2: 运行测试并确认 RED**

```bash
go test ./internal/backend/forwarder -run 'TestContextWindow(Drops|Preserves|Requires|Stable|Deterministic)' -v
```

Expected: FAIL，manager 尚未选择窗口。

- [ ] **Step 3: 实现选择算法**

`Apply` 实现顺序必须是：

```go
groups, err := groupContextWindowMessages(...)
if err != nil {
    return fallbackResult(input, "grouping_failed")
}
required := requiredGroupIndexes(groups)
if tokenSum(required) > hard {
    return compactionNeededResult(input, stats, "required_groups_exceed_hard_budget")
}
selected := clone(required)
addRecentTurnsNewestFirst(selected, groups, policy.MinimumRecentTurns, hard)
addOptionalGroupsNewestFirstUntilTarget(selected, groups, target, hard)
messages := rebuildContextWindowMessages(...)
messages = normalizeReplayMessageSequence(messages)
if err := validateContextWindowMessages(messages); err != nil {
    return fallbackResult(input, "validation_failed")
}
stable := recomputeWindowStableMessageCount(...)
```

关键要求：

- 选择使用原 group index，最终按原 index 排序重建；
- optional group 只从新到旧加入；
- 目标预算是停止继续加入旧内容的点，不是删除必保内容的上限；
- 不修改输入 slice 或 message 内部 ToolCalls；使用 clone helper；
- manager stats 写全 before/after、retained/dropped turn、stable count。

- [ ] **Step 4: 实现结构验证**

`validateContextWindowMessages` 必须验证：

- tool result 的 `ToolCallID` 已在此前 assistant tool call 中出现；
- assistant tool call ID 不重复；
- tool batch 后不存在只保留部分 results；
- `normalizeReplayMessageSequence` 后仍无 dangling tool result；
- reasoning signature 不单独出现在没有 assistant/tool carrier 的消息上。

遇到未知 provider-specific 情形返回错误并回退，不自动修补。

- [ ] **Step 5: 运行纯算法测试并确认 GREEN**

```bash
go test ./internal/backend/forwarder -run 'TestContextWindow|TestGroupContextWindowMessages' -v
```

Expected: PASS。

- [ ] **Step 6: 运行 race 测试**

```bash
go test -race ./internal/backend/forwarder -run 'TestContextWindow|TestGroupContextWindowMessages'
```

Expected: PASS；证明 manager 不修改共享输入。

- [ ] **Step 7: 提交**

```bash
git add internal/backend/forwarder/context_window.go internal/backend/forwarder/context_window_validate.go internal/backend/forwarder/context_window_test.go
git commit -m "feat(context): implement token-aware window selection"
```

---

### Task 4: 将主代理 compaction 拆成维护层与硬兜底层

**Files:**
- Modify: `internal/backend/forwarder/compaction.go`
- Modify: `internal/backend/forwarder/tool_result_snip.go`
- Create: `internal/backend/forwarder/context_window_service_test.go`

**Interfaces:**
- Produces:

```go
func (service *Service) maintainHistoryBeforeProvider(stream *ActiveStream, conversation *ConversationFile, compiled CompiledConversation) (changed bool, err error)
func (service *Service) compactHistoryForWindow(stream *ActiveStream, conversation *ConversationFile, compiled CompiledConversation, force bool) (started bool, err error)
```

- `maybeCompactBeforeProvider` 可保留为兼容 wrapper，直到 Task 5 接入完成；之后删除或仅用于手动 compaction。

- [ ] **Step 1: 写维护层不会生成摘要的失败测试**

测试构造：

- compiled tokens 超过旧 80% 阈值；
- 有可 snip 的陈旧 tool result；
- `maintainHistoryBeforeProvider` 只执行 snip/prune；
- 不设置 `PendingCompaction`，不调用 summary provider；
- 返回 `changed=true`。

另写：无可维护历史时返回 `changed=false`。

- [ ] **Step 2: 运行测试并确认 RED**

```bash
go test ./internal/backend/forwarder -run 'TestMaintainHistoryBeforeProvider' -v
```

Expected: FAIL，函数未定义。

- [ ] **Step 3: 从 `buildAutoCompactionPlan` 提取维护逻辑**

将当前：

```go
if service.recoverBudgetBySnippingStaleToolResults(...) {
    return nil, nil
}
```

移动到 `maintainHistoryBeforeProvider`。该函数仅根据当前 estimated/context tokens 与旧预算判断是否尝试 snip，不创建 summary plan。

`compactHistoryForWindow(..., force=false)` 只负责构造并启动现有 summary plan；`force=true` 复用 forced compaction 语义。

手动 compaction 路径不改变。

- [ ] **Step 4: 写硬兜底触发测试**

断言 `compactHistoryForWindow`：

- manager 标记需要 compaction 时生成 pending plan；
- 无可压缩历史时返回现有 `compactionOverflowTerminalCode` 错误；
- manual compaction 不受影响。

- [ ] **Step 5: 运行 compaction 相关测试**

```bash
go test ./internal/backend/forwarder -run 'Test(MaintainHistoryBeforeProvider|.*Compaction.*)' -v
```

Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/backend/forwarder/compaction.go internal/backend/forwarder/tool_result_snip.go internal/backend/forwarder/context_window_service_test.go
git commit -m "refactor(context): separate window maintenance from compaction"
```

---

### Task 5: 在主代理 `driveProvider` 接入 ContextWindowManager

**Files:**
- Modify: `internal/backend/forwarder/types.go`
- Modify: `internal/backend/forwarder/service.go`
- Modify: `internal/backend/forwarder/context_window_service_test.go`

**Interfaces:**
- Consumes: Tasks 1–4。
- `Service` 增加：

```go
contextWindow *ContextWindowManager
```

- 新增 helper：

```go
func (service *Service) applyParentContextWindow(
    stream *ActiveStream,
    compiled CompiledConversation,
    contextWindowTokens int64,
    reserveTokens int64,
) (CompiledConversation, contextWindowStats, bool)
```

最后一个 bool 表示 `NeedsCompaction`。

- [ ] **Step 1: 写最终 ProviderRequest 使用窗口消息的失败测试**

使用 recording provider gateway：

- 构造 6+ 个大 turn；
- 配置 context window 使完整 compile 超目标但最近 turn 可装入；
- 调用一次 `driveProvider`；
- 断言 provider 收到的 `Messages` 少于完整 compile；
- 当前 turn、summary、最近 2 turn 存在；
- `StableMessageCount <= len(Messages)`；
- `stream.CheckpointConversation.Entries` 数量和内容未变化。

- [ ] **Step 2: 运行测试并确认 RED**

```bash
go test ./internal/backend/forwarder -run 'TestDriveProviderAppliesContextWindow' -v
```

Expected: FAIL，provider 仍收到完整消息。

- [ ] **Step 3: 初始化 manager**

在 `newServiceWithDependencies` 中：

```go
contextWindow: NewContextWindowManager(),
```

测试直接构造 `Service{}` 时，`applyParentContextWindow` 若 manager nil，应懒初始化或安全 no-op；推荐 helper 内懒初始化，避免大量旧测试修改。

- [ ] **Step 4: 修改 `driveProvider` 顺序**

将现有流程改为：

```go
compiled := compiler.Compile(...)
changed, err := service.maintainHistoryBeforeProvider(...)
if changed { snapshot + recompile }
contextWindowTokens := int64(service.resolveContextWindowTokens(modelID))
reserve := service.resolveCompactionReserveTokens(modelID)
windowed, stats, needsCompaction := service.applyParentContextWindow(...)
if needsCompaction {
    started, err := service.compactHistoryForWindow(...)
    if started { return nil }
    // no plan/error follows existing failure semantics
}
compiled = windowed
maxTokens, requestKnobs := service.resolveProviderOutputBudget(..., compiled)
```

不要在窗口后再次使用完整 `compiled.Messages`。

- [ ] **Step 5: 修正 request prefix frontier**

调用现有 `applyRequestPrefixFrontier` / request knobs 构造前，保证使用窗口后的 `compiled.StableMessageCount`。添加断言：窗口删除了稳定前缀中的消息后，frontier 不大于新 stable count。

- [ ] **Step 6: 运行主代理接入测试**

```bash
go test ./internal/backend/forwarder -run 'TestDriveProviderAppliesContextWindow|Test.*RequestPrefix|Test.*StableMessage' -v
```

Expected: PASS。

- [ ] **Step 7: 运行完整 forwarder 测试**

```bash
go test ./internal/backend/forwarder
```

Expected: PASS。

- [ ] **Step 8: 提交**

```bash
git add internal/backend/forwarder/types.go internal/backend/forwarder/service.go internal/backend/forwarder/context_window_service_test.go
git commit -m "feat(context): apply sliding window to parent requests"
```

---

### Task 6: 增加 runtime/provider debug 与 request knobs 观测

**Files:**
- Modify: `internal/backend/forwarder/service.go`
- Modify: `internal/backend/forwarder/context_window.go`
- Modify: `internal/backend/forwarder/context_window_service_test.go`

**Interfaces:**
- Produces:

```go
func contextWindowStatsPayload(stats contextWindowStats, executionKind contextWindowExecutionKind) map[string]any
func attachContextWindowRequestKnob(knobs map[string]any, stats contextWindowStats) map[string]any
```

- [ ] **Step 1: 写 debug payload 失败测试**

断言 payload 精确包含：

```text
execution_kind
model_context_tokens
target_budget_tokens
hard_budget_tokens
before_tokens
after_tokens
before_messages
after_messages
required_group_tokens
retained_turns
dropped_turns
snipped_tool_results
stable_message_count_before
stable_message_count_after
needs_persistent_compaction
fallback_reason
```

断言 request knob 为：

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

- [ ] **Step 2: 运行测试并确认 RED**

```bash
go test ./internal/backend/forwarder -run 'TestContextWindow(StatsPayload|RequestKnob)' -v
```

- [ ] **Step 3: 实现 payload helper**

要求：

- 不覆盖已有 `requestKnobs`；
- 无窗口应用时仍记录 `applied=false`，但 request knob 可省略以减少请求 artifact；
- fallback/needs compaction 必须记录 runtime 事件；
- 不写入消息正文或工具参数。

- [ ] **Step 4: 在主代理写日志**

在 `provider_request_prepared` 之前：

```go
service.debug.LogRuntime(ctx, requestID, conversationID, "context_window_applied", payload)
requestKnobs = attachContextWindowRequestKnob(requestKnobs, stats)
```

`provider_request_prepared` 自动带上最终 knobs。

- [ ] **Step 5: 验证 debug recorder**

```bash
go test ./internal/backend/forwarder -run 'TestContextWindow(StatsPayload|RequestKnob|Debug)' -v
```

Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/backend/forwarder/context_window.go internal/backend/forwarder/service.go internal/backend/forwarder/context_window_service_test.go
git commit -m "feat(context): record sliding window diagnostics"
```

---

### Task 7: 本地 delegated worker 复用统一窗口管理器

**Files:**
- Modify: `internal/backend/forwarder/delegation_local.go`
- Modify: `internal/backend/forwarder/delegation_compaction.go`
- Modify: `internal/backend/forwarder/delegation_compaction_test.go`
- Modify: `internal/backend/forwarder/delegation_local_compaction_test.go`
- Create: `internal/backend/forwarder/context_window_delegation_test.go`

**Interfaces:**
- Consumes: `ContextWindowManager.Apply`。
- Produces:

```go
func (adapter *localDelegatedAgentAdapter) applyDelegatedContextWindow(
    request delegation.TaskRequest,
    messages []modeladapter.Message,
    compiledStable int,
    providerPass int,
) ([]modeladapter.Message, int, contextWindowStats)
```

- [ ] **Step 1: 写 worker 多 pass 滑动失败测试**

构造：

- system + task prompt；
- 8 个 assistant tool-call + tool-result batch；
- 大型旧 tool result；
- context window 只允许最近 2–3 batch。

断言：

- system 与 task prompt 保留；
- 最近 2 个完整 batch 保留；
- 最旧 batch 滑出；
- 任何 tool result 都有匹配 call；
- 父 `ConversationFile.Entries` 未改变；
- delegated policy 使用 `55%` target。

- [ ] **Step 2: 运行测试并确认 RED**

```bash
go test ./internal/backend/forwarder -run 'TestDelegatedContextWindow' -v
```

- [ ] **Step 3: 接入统一 manager**

替换 `compactDelegatedMessagesBeforePass` 内部实现：

1. 先复用 `snipDelegatedOversizedToolResults` 对旧大工具结果做 clone 后截断；
2. 使用 `delegatedContextWindowPolicy(compactionAutoReserveTokens)`；
3. 调用 manager；
4. 返回窗口后的 messages 和 stable count；
5. stats 记录到现有 delegated log 和统一 `context_window_applied` debug，`execution_kind=delegated`。

不得再调用 `dropDelegatedEarlyMessages`。

- [ ] **Step 4: 删除或收缩旧独立选择逻辑**

- 删除 `dropDelegatedEarlyMessages` 和 `delegatedCompactionKeepTurns`；
- 保留 `delegatedContextOverflowError`、tool-result snip、retry limit；
- 将旧测试改为 manager 结构组测试，不保留双实现。

- [ ] **Step 5: 更新 provider request stable count**

`runProviderPass` 接收窗口返回的 stable count，不再使用：

```go
delegatedStableMessageCount(compiled.StableMessageCount, len(messages))
```

如果 `delegatedStableMessageCount` 无其他调用，删除并迁移测试。

- [ ] **Step 6: 验证 overflow retry**

增加测试：第一次 provider 返回 `context_length_exceeded`，窗口收缩/重试后成功；同一 pass 最多重试现有 2 次，不能无限循环。

- [ ] **Step 7: 运行 delegated 测试**

```bash
go test ./internal/backend/forwarder -run 'Test(DelegatedContextWindow|.*Delegated.*Compaction|.*Delegated.*Overflow)' -v
```

Expected: PASS。

- [ ] **Step 8: 提交**

```bash
git add internal/backend/forwarder/delegation_local.go internal/backend/forwarder/delegation_compaction.go internal/backend/forwarder/delegation_compaction_test.go internal/backend/forwarder/delegation_local_compaction_test.go internal/backend/forwarder/context_window_delegation_test.go
git commit -m "feat(context): unify delegated worker windowing"
```

---

### Task 8: Prefix cache 与 local response cache 回归

**Files:**
- Modify: `internal/backend/forwarder/provider_cache_fingerprint_test.go`
- Create: `internal/backend/forwarder/prefix_cache_test.go`（若已有同用途文件则合并）
- Modify: `internal/backend/forwarder/context_window_test.go`

**Interfaces:**
- Consumes: 最终 `ProviderRequest.Messages` 与 `StableMessageCount`。
- 不新增生产接口，除非测试证明现有 helper 不可直接调用。

- [ ] **Step 1: 写 response cache key 失败测试**

测试：

1. 完整消息与窗口消息必须产生不同 key；
2. 两次相同窗口产生相同 key；
3. 仅 `StableMessageCount` 不同必须产生不同 key；
4. manager stats/request knobs 不应影响 key（当前 key 不含 RequestKnobs）。

- [ ] **Step 2: 运行 cache 测试**

```bash
go test ./internal/backend/forwarder -run 'TestProviderCache.*ContextWindow|TestContextWindow.*CacheKey' -v
```

如果现有实现已满足，应 PASS；若失败，只修改 production key 逻辑以满足设计，不把 stats 加入 key。

- [ ] **Step 3: 写 prefix frontier 测试**

构造原 stable count 覆盖 10 条消息，窗口移除最老可选 turn 后只保留 6 条。断言：

- stable count <= 6；
- frontier request knob <= stable count；
- 再次相同窗口 frontier 不变化；
- 窗口进一步滑动时 frontier 单调不超过实际稳定前缀。

- [ ] **Step 4: 运行 prefix cache 测试**

```bash
go test ./internal/backend/forwarder -run 'Test(ContextWindow.*Prefix|RequestPrefix.*Window)' -v
```

Expected: PASS。

- [ ] **Step 5: 运行 provider cache 全包测试**

```bash
go test ./internal/backend/forwarder -run 'TestProviderCache|TestResponseCache|Test.*Prefix' -v
```

Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/backend/forwarder/provider_cache_fingerprint_test.go internal/backend/forwarder/prefix_cache_test.go internal/backend/forwarder/context_window_test.go
git commit -m "test(context): verify window cache stability"
```

---

### Task 9: Compaction、reasoning、tool replay 与持久化回归测试

**Files:**
- Modify: `internal/backend/forwarder/context_window_service_test.go`
- Modify: `internal/backend/forwarder/context_window_test.go`
- Modify: existing relevant projector/compaction tests only if assertions need extension.

**Interfaces:**
- 不新增生产接口。

- [ ] **Step 1: 写持久化不变测试**

在真实临时 history store 中：

1. 创建长 `ConversationFile` 并落盘；
2. 执行窗口应用/一次 provider pass；
3. 重新从 store 加载；
4. 比较窗口前后的 `context.json` entries 数量、kind、turn、tool call ID 和 payload hash 完全相同。

允许新增 runtime debug 文件，不允许修改 context entries。

- [ ] **Step 2: 写 compaction fallback 测试**

构造 required groups 本身超过 hard budget，断言：

- manager 不拆 required groups；
- 主代理创建 pending compaction；
- compaction 成功后重新 compile；
- 第二次 manager 不再 needs compaction；
- provider 最终收到合法请求。

- [ ] **Step 3: 写 reasoning/tool 结构回归**

覆盖：

- OpenAI Responses encrypted reasoning signature；
- reasoning carrier 位于 tool call 之前；
- 并行 2+ tool calls/results；
- GenerateImage 抑制/过滤；
- canceled turn replay；
- 未识别 provider message → required fallback。

每个测试最终调用 `validateContextWindowMessages` 并通过 provider adapter request conversion。

- [ ] **Step 4: 运行聚焦回归**

```bash
go test ./internal/backend/forwarder -run 'Test(ContextWindow.*Persistence|ContextWindow.*Compaction|ContextWindow.*Reasoning|ContextWindow.*Parallel|ContextWindow.*GenerateImage|ContextWindow.*Canceled)' -v
```

Expected: PASS。

- [ ] **Step 5: 运行完整 forwarder race 测试**

```bash
go test -race ./internal/backend/forwarder
```

Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/backend/forwarder/context_window_service_test.go internal/backend/forwarder/context_window_test.go internal/backend/forwarder/*relevant_test.go
git commit -m "test(context): cover sliding window regressions"
```

---

### Task 10: 长会话压力验证与文档收尾

**Files:**
- Create: `internal/backend/forwarder/context_window_bench_test.go`
- Modify: `docs/superpowers/specs/2026-08-08-global-context-sliding-window-design.md` only if implementation differs from approved names without semantic change.
- Create: `docs/context-window-runtime.md`（用户运行与日志诊断说明）。

**Interfaces:**
- 不新增生产接口。

- [ ] **Step 1: 添加 benchmark 和 100-turn 压力测试**

Benchmark：

```go
func BenchmarkContextWindowApply100Turns(b *testing.B)
func BenchmarkContextWindowApplyParallelToolBatches(b *testing.B)
```

压力测试构造 100 turns、每 turn 2 个工具结果，断言：

- after tokens <= hard budget；
- after tokens 接近或小于 target，除非 required groups 超 target；
- 执行时间无随请求次数累积的共享状态；
- 输入 messages 未被修改。

- [ ] **Step 2: 运行 benchmark**

```bash
go test ./internal/backend/forwarder -run '^$' -bench 'BenchmarkContextWindow' -benchmem
```

记录基线到计划执行日志；不得设拍脑袋的硬性能阈值，但若单次 100-turn Apply 达到明显的百毫秒级，应先 profile 再合并。

- [ ] **Step 3: 编写运行诊断文档**

`docs/context-window-runtime.md` 必须说明：

- 滑动窗口只影响 provider 请求，不删除历史；
- 主代理 65%、delegated 55%、硬窗口 80%-reserve；
- 如何在 `runtime.jsonl` 搜索 `context_window_applied`；
- 如何在 `provider.jsonl` 查看 `request_knobs.context_window`；
- `needs_persistent_compaction` 与 `fallback_reason` 的含义；
- 原生 Cursor 子代理暂不直接覆盖。

- [ ] **Step 4: 运行全量验证**

```bash
go test ./internal/backend/...
go test ./internal/...
go build ./...
git diff --check
```

Expected: 全部 PASS/无输出错误。

- [ ] **Step 5: 运行代码评审**

使用项目 review 工具审查当前分支 diff，重点：

- 工具配对/Reasoning 安全；
- 持久化历史是否被误改；
- StableMessageCount/prefix cache；
- manager 是否修改输入 slice；
- compaction fallback 是否可能循环。

修复所有 correctness/high/medium 问题后重新运行 Task 9/10 验证。

- [ ] **Step 6: 最终提交**

```bash
git add internal/backend/forwarder/context_window_bench_test.go docs/context-window-runtime.md docs/superpowers/specs/2026-08-08-global-context-sliding-window-design.md
git commit -m "docs(context): document sliding window runtime"
```

---

## Final Verification Checklist

- [ ] 主代理 provider 请求在长会话中稳定应用 65% 目标窗口。
- [ ] delegated worker 使用 55% 目标窗口并复用同一 manager。
- [ ] 硬窗口为 80%-reserve，required groups 超限时进入现有 compaction。
- [ ] `context.json`、checkpoint 和 summary archive 未被滑动窗口删除。
- [ ] tool call/result、并行工具 batch、reasoning signature/carrier 完整。
- [ ] `StableMessageCount` 与 prefix frontier 不越界且确定。
- [ ] response cache key 基于最终窗口 messages，stats 不污染 key。
- [ ] runtime/provider debug 均可观察窗口决策。
- [ ] manager 失败安全回退旧流程，不产生残缺 provider 请求。
- [ ] 原生 Cursor 子代理行为无回归。
- [ ] `go test -race ./internal/backend/forwarder` 通过。
- [ ] `go test ./internal/backend/...`、`go test ./internal/...`、`go build ./...` 通过。
