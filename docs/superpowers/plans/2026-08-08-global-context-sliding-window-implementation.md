# Global Context Projection Implementation Plan

> 实施时使用 TDD：每个生产行为先写失败测试并确认 RED，再做最小实现。

**Goal:** 实现父代理 Reasonix 风格持久 context projection，以及本地 delegated worker 的无状态结构滑窗与渐进 overflow retry；保持 canonical history、手动 compaction、工具/reasoning 结构和缓存语义正确。

**Tech Stack:** Go 1.25、现有 forwarder projector/compiler/token estimator/file store/actor/provider gateway。

## 全局约束

- 自动压力处理不得调用 `ReplaceEntries` 或删除 canonical entries。
- 手动 `/summarize` 保持现有 canonical compaction 行为。
- 父 projection 基于 canonical entry 分组；不得从平铺消息猜 turn。
- sidecar 失效必须 fail-closed。
- 所有 provider-visible rewrite 发生在最终 token/output budget 前。
- 最终共享窗口满足 `input + output + reserve <= context window`。
- delegated 每次 overflow retry 必须使用更小预算。
- 保留当前工作区已有未提交改动，不做无关重构。

## Task 1: Entry-Layer Projection 与 Sidecar 契约

**Files:**
- Modify: `internal/backend/forwarder/types.go` only for shared projection metadata if necessary
- Modify: `internal/backend/forwarder/projector.go` to recognize the synthetic projection summary
- Create: `internal/backend/forwarder/context_projection.go`
- Create: `internal/backend/forwarder/context_projection_store.go`
- Create: `internal/backend/forwarder/context_projection_test.go`

- [ ] 先写 canonical entry 分组、projection clone 不修改事实源的失败测试。
- [ ] 按 turn/request/tool 归属选择完整 entries，禁止从平铺 provider messages 反推 provenance。
- [ ] projection clone 清除旧 usage/cache-frontier 元数据，再调用现有 projector/compiler 统一完成 normalization。
- [ ] 定义 `context-projection.json` schema、atomic load/save 和 clone。
- [ ] 写 covered-prefix SHA-256 fingerprint，覆盖 canonical entry 的 seq/turn/request/role/kind/tool IDs/payload。
- [ ] 测试 schema、conversation/model/lineage/context version/fingerprint 任一不匹配均忽略 sidecar。

## Task 2: 父代理投影选择

**Files:**
- Modify: `internal/backend/forwarder/context_projection.go`
- Create: `internal/backend/forwarder/context_projection_groups.go`
- Extend: `internal/backend/forwarder/context_projection_test.go`

- [ ] 写低压返回完整 compile 的失败测试。
- [ ] 写高压保留 stable prefix、一个 summary、current state/tool chain、recent tail 的失败测试。
- [ ] 按 canonical entries 将同 turn 输入/assistant/tool batch 分为不可拆组。
- [ ] sidecar 命中时复用 summary 和 covered boundary；仅选择其后的 canonical tail。
- [ ] sidecar 缺失时选择可摘要的完整前缀和当前必保尾部，返回 `NeedsSummary`，不改 canonical history。
- [ ] required 内容超过硬预算时 fail-closed 并返回明确 overflow。
- [ ] 若删除原 stable frontier 之前的消息，将 stable count 设为 0。

## Task 3: 自动 Projection 摘要生命周期

**Files:**
- Modify: `internal/backend/forwarder/compaction.go`
- Modify: `internal/backend/forwarder/compaction_entries.go` only for shared plan helpers if necessary
- Modify: `internal/backend/forwarder/actor.go`
- Create: `internal/backend/forwarder/context_projection_service_test.go`

- [ ] 为 `auto_projection` plan 写测试：完成后 sidecar 更新，canonical entries 字节级不变。
- [ ] 复用现有 async summary provider/actor token lifecycle。
- [ ] `manual` completion 继续 `applyCompactionPlan`。
- [ ] `auto_projection` completion 只校验 covered prefix 并 atomic 写 sidecar，然后 resume provider。
- [ ] 自动 projection 不追加 compaction request/failed/summary entries，不发布会误导 UI 的 canonical compaction checkpoint。
- [ ] 摘要失败时保留 canonical history，并进入更小 recent-tail fallback 或明确 overflow。

## Task 4: 主 `driveProvider` 接入与最终请求顺序

**Files:**
- Modify: `internal/backend/forwarder/service.go`
- Modify: `internal/backend/forwarder/token_estimator.go`
- Extend: `internal/backend/forwarder/context_projection_service_test.go`

- [ ] 接入点放在 compile/stale maintenance/recompile 后。
- [ ] projection 后只处理保留消息的 vision proxy。
- [ ] tool filtering 后再估算最终 `Messages + Tools`。
- [ ] `resolveProviderOutputBudget` 以最终 projection 为准，不用旧 `TokenDetailsUsedTokens` 作下限。
- [ ] recovery cap 应用后再次保证共享窗口不变量。
- [ ] 最终 `ProviderRequest`、response cache key 和 debug artifact 使用同一份 messages/stable count/tools/max tokens。
- [ ] 测试 vision 替换和工具过滤能改变最终估算，且预算仍合法。

## Task 5: 自动 Compaction 分流与 Overflow

**Files:**
- Modify: `internal/backend/forwarder/compaction.go`
- Modify: `internal/backend/forwarder/context_overflow.go`
- Extend: `internal/backend/forwarder/context_projection_service_test.go`

- [ ] `maybeCompactBeforeProvider` 只对 manual 使用 canonical compaction；自动压力转给 projection manager。
- [ ] 保留 stale tool-result maintenance 和必要 recompile。
- [ ] parent provider overflow 首先减小渠道窗口并收紧 projection 预算。
- [ ] 不再因自动 overflow 调用 `ReplaceEntries`。
- [ ] 手动 compaction、hook、Summary UI、turn completion 原测试全部通过。

## Task 6: Delegated 结构滑窗

**Files:**
- Modify: `internal/backend/forwarder/delegation_compaction.go`
- Modify: `internal/backend/forwarder/delegation_local.go`
- Modify: `internal/backend/forwarder/delegation_compaction_test.go`
- Modify: `internal/backend/forwarder/delegation_local_compaction_test.go`

- [ ] 写完整并行 tool batch 不拆分的失败测试。
- [ ] 分组 system/task prompt、普通 turn、assistant tool-call + 全部连续 results。
- [ ] 孤立 result、重复 call ID、部分 result 均 fail-closed。
- [ ] 旧大 tool result 在 clone 上 snip；不得修改共享输入。
- [ ] 从最老完整可选组滑出，保留最近完整批次和未闭合链。
- [ ] 返回窗口后的 stable count；不能只 clamp 原 stable count。

## Task 7: Delegated 渐进 Retry 与预算

**Files:**
- Modify: `internal/backend/forwarder/delegation_local.go`
- Modify: `internal/backend/forwarder/delegation_local_compaction_test.go`

- [ ] retry 0/1/2 使用递减预算比例，例如 1.0/0.8/0.64。
- [ ] 测试连续 overflow 的每个请求 message/token 数严格递减。
- [ ] delegated `resolveBudget` 接收最终 worker messages/tools，不接收原 compile。
- [ ] child conversation 不继承父 `TokenDetailsUsedTokens` 作为预算依据。
- [ ] 达到现有 retry limit 后明确失败，无无限循环。

## Task 8: 缓存、持久化与回归

**Files:**
- Extend relevant forwarder cache/projector/store/compaction tests

- [ ] 相同 sidecar + canonical tail 产生确定性相同 projection/cache key。
- [ ] sidecar 更新后 cache frontier 变化一次，后续尾部保持 append-only。
- [ ] canonical `state.json/context.json` 在自动 projection 前后内容不变。
- [ ] rewind、fork lineage、manual compaction 使旧 sidecar 失效。
- [ ] OpenAI Responses reasoning、Anthropic signature、并行工具、GenerateImage suppression、取消 turn 均通过结构回归。
- [ ] UI/checkpoint 仍能看到 canonical 完整历史。

## Task 9: 诊断与验证

- [ ] 添加 `context_projection_applied` runtime/provider knobs。
- [ ] 记录 sidecar hit/miss/invalidation、covered seq、before/after、stable count、retry ratio。
- [ ] 使用工作区可写缓存运行：

```powershell
$env:GOCACHE = 'E:\MyProject\cursor-byok\.cache\go-build'
go test ./internal/backend/forwarder
go test -race ./internal/backend/forwarder
go test ./internal/backend/...
go build ./...
git diff --check
```

- [ ] 最终代码评审重点检查 canonical mutation、tool/reasoning 结构、stable frontier、预算顺序和 retry 是否真实收缩。
