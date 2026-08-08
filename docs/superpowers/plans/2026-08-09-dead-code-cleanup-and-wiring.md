# 死代码清理 + 预留功能接线 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 删除 forwarder/agent/delegation 中所有已确认的死代码，接线 SubagentProfile.MaxSteps 限步和 ToolWhitelist 工具白名单两个预留功能。

**Architecture:** 分两阶段：先删除死代码（降低噪声、消除误用风险），再接线预留功能（增强子代理能力）。删除按依赖顺序从叶子到根，每步编译验证。接线在 `delegation_local.go` 的 `Execute` 循环和 `filterDelegatedTools` 中完成。

**Tech Stack:** Go 1.24, protobuf, Cursor agent forwarder

## Global Constraints

- 每个任务结束后必须 `go build ./...` 通过
- 每个任务结束后必须 `go test ./internal/backend/forwarder/...` 通过
- 删除函数前必须确认全仓库（含测试）零引用
- 不改动 forwarder 之外的测试文件，除非该测试直接引用被删符号
- gofmt 格式必须通过

---

### Task 1: 删除 tool_result_snip.go 死代码 + service.go 死分支 + types.go 字段

**Files:**
- Delete: `internal/backend/forwarder/tool_result_snip.go`（整个文件，374 行）
- Modify: `internal/backend/forwarder/service.go:1729`（删除 `stream.StaleToolResultSnipApplied = false` 重置）
- Modify: `internal/backend/forwarder/service.go:1830-1868`（删除 `staleToolResultSnipAppliedLocked` 死分支）
- Modify: `internal/backend/forwarder/types.go:238-241`（删除 `StaleToolResultSnipApplied` 字段）

**Interfaces:**
- Consumes: 无（被删代码无外部消费者）
- Produces: 无（被删代码不产出任何其他任务依赖的符号）

**背景**：`tool_result_snip.go` 整个文件是原型代码（commit `899400d`），写完后从未接线。`service.go:1830-1868` 的 `staleToolResultSnipAppliedLocked` 分支永远为 false（唯一写者 `markStaleToolResultSnipApplied` 从未被调用）。`compaction.go:187` 注释明确说"Canonical tool-result snipping is deliberately reserved for an explicit maintenance action"。

- [ ] **Step 1: 确认零引用**

Run: `grep -rn "StaleToolResultSnipApplied\|recoverBudgetBySnippingStaleToolResults\|maintainStaleToolResults\|staleToolResultSnipAppliedLocked\|markStaleToolResultSnipApplied\|collectStaleToolResultTargets\|rewriteStaleToolResultEntry\|archiveStaleToolResult\|sanitizeArchiveName\|snippedToolResultArchive\|staleToolResultMaintenanceOutcome\|staleToolResultProtectedTurnFloor\|entryTextHead\|toolResultEntrySize\|tryRecoverByPruningStaleToolResults\|staleToolResultSnipMinBytes\|staleToolResultSnipLimitBytes\|staleToolResultProtectedTailTurns\|snippedStaleToolResultPrefix\|prunedStaleToolResultPrefix" internal/ --include="*.go" | grep -v "tool_result_snip.go"`
Expected: 只剩 `service.go:1729`（重置）、`service.go:1833`（读取）、`types.go:241`（字段定义）三处引用

- [ ] **Step 2: 删除 tool_result_snip.go 整个文件**

```bash
rm internal/backend/forwarder/tool_result_snip.go
```

- [ ] **Step 3: 删除 service.go 中的重置和死分支**

在 `service.go` 中删除第 1729 行：
```go
stream.StaleToolResultSnipApplied = false
```

在 `service.go` 中删除第 1830-1868 行的整个 `if stream.staleToolResultSnipAppliedLocked()` 代码块（从注释 `// 陈旧工具结果 snip/prune 救回` 到对应的 `}`）。

- [ ] **Step 4: 删除 types.go 中的字段**

在 `types.go` 中删除第 238-241 行：
```go
// StaleToolResultSnipApplied 标记本 provider pass ...
// ...
// ...
StaleToolResultSnipApplied bool
```

- [ ] **Step 5: 编译验证**

Run: `go build ./...`
Expected: 编译通过（确认无传递引用）

- [ ] **Step 6: 测试验证**

Run: `go test ./internal/backend/forwarder/...`
Expected: 全部通过

- [ ] **Step 7: gofmt + go vet**

Run: `gofmt -l internal/backend/forwarder/service.go internal/backend/forwarder/types.go && go vet ./internal/backend/forwarder/...`
Expected: 无输出

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor: 删除 tool_result_snip.go 死代码及 service.go 不可达分支

tool_result_snip.go（374行）是原型代码，写完后从未接线。
service.go:1830-1868 的 staleToolResultSnipAppliedLocked 分支永远为 false
（唯一写者 markStaleToolResultSnipApplied 从未被调用）。
compaction.go 注释明确说 canonical snipping 是有意不接线的。"
```

---

### Task 2: 删除 compaction_algorithms.go 中的死函数

**Files:**
- Modify: `internal/backend/forwarder/compaction_algorithms.go`（删除 6 个死函数）

**Interfaces:**
- Consumes: 无
- Produces: 无

**背景**：以下函数被投影方案（commit `504b155`）取代，全仓库零引用：
- `selectTurnsForCompaction` (L19)
- `buildCompactionCandidates` (L76)
- `countCompactableContextTurns` (L232)
- `estimateCompactedTurnSummariesTokens` (L760)
- `decodeCompactedTurnSummaries`（仅被死的 `estimateCompactedTurnSummariesTokens` 调用）
- `buildCompactedConversationSummary` (L741)

- [ ] **Step 1: 确认零引用**

Run: `grep -rn "selectTurnsForCompaction\|buildCompactionCandidates\|countCompactableContextTurns\|estimateCompactedTurnSummariesTokens\|decodeCompactedTurnSummaries\|buildCompactedConversationSummary" internal/ --include="*.go" | grep -v "compaction_algorithms.go"`
Expected: 无输出（零外部引用）

- [ ] **Step 2: 删除 6 个死函数**

在 `compaction_algorithms.go` 中删除以下函数的完整定义（包括注释）：
- `selectTurnsForCompaction`
- `buildCompactionCandidates`
- `countCompactableContextTurns`
- `estimateCompactedTurnSummariesTokens`
- `decodeCompactedTurnSummaries`
- `buildCompactedConversationSummary`

- [ ] **Step 3: 编译验证**

Run: `go build ./...`
Expected: 编译通过

- [ ] **Step 4: 测试验证**

Run: `go test ./internal/backend/forwarder/...`
Expected: 全部通过

- [ ] **Step 5: gofmt**

Run: `gofmt -l internal/backend/forwarder/compaction_algorithms.go`
Expected: 无输出

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: 删除 compaction_algorithms.go 中被投影方案取代的死函数

selectTurnsForCompaction、buildCompactionCandidates、countCompactableContextTurns、
estimateCompactedTurnSummariesTokens、decodeCompactedTurnSummaries、
buildCompactedConversationSummary 均被投影方案(commit 504b155)取代，零引用。"
```

---

### Task 3: 删除 compaction.go 中的死函数和死常量

**Files:**
- Modify: `internal/backend/forwarder/compaction.go`（删除 3 个死符号）

**Interfaces:**
- Consumes: 无
- Produces: 无

**背景**：
- `buildAutoCompactionPlanFromHistory` (L366) - 被 `buildAutoCompactionPlan` 取代，零调用
- `compactionSummaryUserMessage` (L35) - 从未被引用的中文常量
- `compactionRequestSourceCurrentTurn` (L33) - 仅被死的 `buildAutoCompactionPlanFromHistory` 引用

- [ ] **Step 1: 确认零引用**

Run: `grep -rn "buildAutoCompactionPlanFromHistory\|compactionSummaryUserMessage\|compactionRequestSourceCurrentTurn" internal/ --include="*.go" | grep -v "compaction.go"`
Expected: 无输出

注意：`compactionRequestSourceCurrentTurn` 在 `compaction.go` 内部被 `buildAutoCompactionPlanFromHistory` 引用，删除函数后常量也变为零引用。确认 `compactionRequestSourcePromptAsset`（L32）**不是**死代码（被 `buildLegacyCompactionPlan` 使用），不要删。

- [ ] **Step 2: 删除死函数和死常量**

在 `compaction.go` 中删除：
- `compactionSummaryUserMessage` 常量定义（L35）
- `compactionRequestSourceCurrentTurn` 常量定义（L33）
- `buildAutoCompactionPlanFromHistory` 函数完整定义（L366 起）

- [ ] **Step 3: 编译验证**

Run: `go build ./...`
Expected: 编译通过

- [ ] **Step 4: 测试验证**

Run: `go test ./internal/backend/forwarder/...`
Expected: 全部通过

- [ ] **Step 5: gofmt**

Run: `gofmt -l internal/backend/forwarder/compaction.go`
Expected: 无输出

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: 删除 compaction.go 中被投影方案取代的死函数和死常量

buildAutoCompactionPlanFromHistory、compactionSummaryUserMessage、
compactionRequestSourceCurrentTurn 均被投影方案取代，零引用。"
```

---

### Task 4: 删除 agent/step/ 孤儿包和 delegation 死代码

**Files:**
- Delete: `internal/backend/agent/step/recorder.go`（整个文件）
- Delete: `internal/backend/agent/step/doc.go`（整个文件）
- Modify: `internal/backend/delegation/scheduler.go`（删除 `Events()` 方法和 events channel 相关死代码）
- Modify: `internal/backend/delegation/cursor_adapter.go`（删除 `ExecuteToolAsync` 死方法）
- Modify: `internal/backend/agent/model/content_parts.go`（删除 `CountImageParts`、`ResolveImageContent` 死函数）

**Interfaces:**
- Consumes: 无
- Produces: 无

**背景**：
- `agent/step/` 整个包零导入者
- `Scheduler.Events()` (scheduler.go:679) 零调用者，events channel 及其支撑机制（`purgeBufferedEventsLocked`/`evictOldestNonTerminalEventLocked`/`eventsClosed`）均为死代码
- `CursorAdapter.ExecuteToolAsync` (cursor_adapter.go:216) 零调用者
- `CountImageParts` (content_parts.go:294) 和 `ResolveImageContent` (content_parts.go:307) 零调用者

- [ ] **Step 1: 确认零引用**

```bash
grep -rn "agent/step" internal/ --include="*.go"
grep -rn "\.Events()" internal/backend/delegation/ --include="*.go"
grep -rn "ExecuteToolAsync" internal/ --include="*.go" | grep -v "cursor_adapter.go"
grep -rn "CountImageParts\|ResolveImageContent" internal/ --include="*.go" | grep -v "content_parts.go"
```
Expected: 全部无输出（零引用）

- [ ] **Step 2: 删除 agent/step/ 包**

```bash
rm -rf internal/backend/agent/step/
```

- [ ] **Step 3: 删除 Scheduler.Events() 和 events channel 死代码**

在 `scheduler.go` 中删除：
- `events` 字段定义（在 Scheduler 结构体中）
- `Events()` 方法 (L679)
- `purgeBufferedEventsLocked` 方法
- `evictOldestNonTerminalEventLocked` 方法
- `eventsClosed` 字段（如果有）
- `publishLocked` 中向 events channel 写入的代码

注意：`publishLocked` 仍需保留 `stateChanged` 信号（`WaitForTaskUpdate`/`WaitForTerminal` 依赖它），只删除 events channel 相关部分。

- [ ] **Step 4: 删除 ExecuteToolAsync 死方法**

在 `cursor_adapter.go` 中删除 `ExecuteToolAsync` 方法完整定义（L216 起）。

- [ ] **Step 5: 删除 CountImageParts 和 ResolveImageContent**

在 `content_parts.go` 中删除 `CountImageParts` (L294) 和 `ResolveImageContent` (L307) 两个函数。

- [ ] **Step 6: 编译验证**

Run: `go build ./...`
Expected: 编译通过

- [ ] **Step 7: 测试验证**

Run: `go test ./internal/backend/...`
Expected: 全部通过

- [ ] **Step 8: gofmt + go vet**

Run: `gofmt -l internal/backend/delegation/scheduler.go internal/backend/delegation/cursor_adapter.go internal/backend/agent/model/content_parts.go && go vet ./internal/backend/delegation/... ./internal/backend/agent/...`
Expected: 无输出

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "refactor: 删除 agent/step 孤儿包、Scheduler.Events、ExecuteToolAsync 等死代码

- agent/step/ 整个包零导入者，删除
- Scheduler.Events() 及 events channel 死代码（被 WaitForTaskUpdate 取代）
- CursorAdapter.ExecuteToolAsync 零调用者
- CountImageParts/ResolveImageContent 零调用者"
```

---

### Task 5: 接线 SubagentProfile.ToolWhitelist 到 filterDelegatedTools

**Files:**
- Modify: `internal/backend/forwarder/delegation_local.go:99`（`filterDelegatedTools` 调用点）
- Modify: `internal/backend/forwarder/delegation_local.go:576`（`filterDelegatedTools` 函数签名）
- Modify: `internal/backend/forwarder/delegation_cursor.go:119`（`buildDelegatedCursorTaskRequest` 传入 profile）
- Modify: `internal/backend/delegation/scheduler.go`（`TaskRequest` 增加 `ToolWhitelist` 字段）

**Interfaces:**
- Consumes: `runtimecore.LookupSubagentProfile` / `runtimecore.SubagentProfile.ToolWhitelist`
- Produces: `filterDelegatedTools` 新签名（增加 `toolWhitelist []string` 参数）

**背景**：`SubagentProfile.ToolWhitelist` 标注"预留：当前未强制"。接线方式：在 `buildDelegatedCursorTaskRequest` 中通过 `LookupSubagentProfile` 查找 profile，把 `ToolWhitelist` 存入 `TaskRequest`；在 `filterDelegatedTools` 中如果 whitelist 非空，只保留 whitelist 中的工具。

- [ ] **Step 1: TaskRequest 增加 ToolWhitelist 字段**

在 `internal/backend/delegation/scheduler.go` 的 `TaskRequest` 结构体中增加：
```go
// ToolWhitelist 可选工具白名单（空 = 不限制）。来自 SubagentProfile，由 filterDelegatedTools 强制。
ToolWhitelist []string
```

- [ ] **Step 2: buildDelegatedCursorTaskRequest 注入 ToolWhitelist**

在 `internal/backend/forwarder/delegation_cursor.go` 的 `buildDelegatedCursorTaskRequest` 中，通过 `LookupSubagentProfile` 查找 profile 并设置 `ToolWhitelist`：
```go
var toolWhitelist []string
if profile, ok := runtimecore.LookupSubagentProfile(subagentType); ok {
    toolWhitelist = profile.ToolWhitelist
}
```
在返回的 `TaskRequest` 中增加 `ToolWhitelist: toolWhitelist`。

- [ ] **Step 3: filterDelegatedTools 增加 whitelist 参数**

修改 `filterDelegatedTools` 签名：
```go
func filterDelegatedTools(tools []json.RawMessage, permissions map[string]bool, mcpToolNames map[string]struct{}, toolWhitelist []string) ([]json.RawMessage, error) {
```

在函数体中，工具名通过 `extractToolName` 提取后，增加白名单过滤：
```go
if len(toolWhitelist) > 0 {
    allowed := false
    for _, whitelisted := range toolWhitelist {
        if trimmedName == whitelisted {
            allowed = true
            break
        }
    }
    if !allowed {
        continue
    }
}
```

- [ ] **Step 4: 更新调用点**

在 `delegation_local.go:99` 更新 `filterDelegatedTools` 调用：
```go
compiled.Tools, err = filterDelegatedTools(compiled.Tools, request.ToolPermission, mcpToolNames, request.ToolWhitelist)
```

- [ ] **Step 5: 编译验证**

Run: `go build ./...`
Expected: 编译通过

- [ ] **Step 6: 测试验证**

Run: `go test ./internal/backend/forwarder/...`
Expected: 全部通过（如果有测试直接调用 `filterDelegatedTools` 需更新签名）

- [ ] **Step 7: gofmt**

Run: `gofmt -l internal/backend/forwarder/delegation_local.go internal/backend/forwarder/delegation_cursor.go internal/backend/delegation/scheduler.go`
Expected: 无输出

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "feat(delegation): 接线 SubagentProfile.ToolWhitelist 工具白名单

filterDelegatedTools 增加白名单参数，TaskRequest 携带 ToolWhitelist，
buildDelegatedCursorTaskRequest 通过 LookupSubagentProfile 注入。
非空白名单时只保留白名单中的工具。"
```

---

### Task 6: 接线 SubagentProfile.MaxSteps 到 Execute 循环

**Files:**
- Modify: `internal/backend/forwarder/delegation_local.go:109-180`（`Execute` 循环）
- Modify: `internal/backend/forwarder/delegation_cursor.go:119`（`buildDelegatedCursorTaskRequest` 传入 MaxSteps）
- Modify: `internal/backend/delegation/scheduler.go`（`TaskRequest` 增加 `MaxSteps` 字段）

**Interfaces:**
- Consumes: `runtimecore.LookupSubagentProfile` / `runtimecore.SubagentProfile.MaxSteps`
- Produces: 无

**背景**：`SubagentProfile.MaxSteps` 标注"预留：当前未强制"。接线方式：在 `buildDelegatedCursorTaskRequest` 中查找 profile 设置 `MaxSteps`；在 `Execute` 循环中如果 `toolCallCount >= maxSteps` 则优雅停止（返回部分结果，与 pass 超限处理一致）。

- [ ] **Step 1: TaskRequest 增加 MaxSteps 字段**

在 `internal/backend/delegation/scheduler.go` 的 `TaskRequest` 结构体中增加：
```go
// MaxSteps 可选最大步数上限（0 = 不限制）。来自 SubagentProfile，由 Execute 循环强制。
MaxSteps int
```

- [ ] **Step 2: buildDelegatedCursorTaskRequest 注入 MaxSteps**

在 `internal/backend/forwarder/delegation_cursor.go` 的 `buildDelegatedCursorTaskRequest` 中：
```go
var maxSteps int
if profile, ok := runtimecore.LookupSubagentProfile(subagentType); ok {
    maxSteps = profile.MaxSteps
}
```
在返回的 `TaskRequest` 中增加 `MaxSteps: maxSteps`。

- [ ] **Step 3: Execute 循环增加步数限制检查**

在 `internal/backend/forwarder/delegation_local.go` 的 `Execute` 方法中，`toolCallCount` 累加后（约 L162 `toolCallCount++` 之后）增加检查：
```go
// SubagentProfile.MaxSteps 限步：超过时优雅停止，返回部分结果（与 pass 超限处理一致）。
if request.MaxSteps > 0 && toolCallCount >= request.MaxSteps {
    delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCompleted, providerPass, nil, nil, "delegated worker reached step limit, returning partial results", fmt.Sprintf("step limit: %d", request.MaxSteps))
    logger.Infof("forwarder local delegated worker reached step limit task_id=%s max_steps=%d tool_calls=%d", strings.TrimSpace(request.ID), request.MaxSteps, toolCallCount)
    return delegation.TaskResult{
        Output:        lastOutputText,
        ToolCallCount: toolCallCount,
        Metadata:      identity.metadata(providerPass),
    }
}
```

- [ ] **Step 4: 编译验证**

Run: `go build ./...`
Expected: 编译通过

- [ ] **Step 5: 测试验证**

Run: `go test ./internal/backend/forwarder/...`
Expected: 全部通过

- [ ] **Step 6: gofmt**

Run: `gofmt -l internal/backend/forwarder/delegation_local.go internal/backend/forwarder/delegation_cursor.go internal/backend/delegation/scheduler.go`
Expected: 无输出

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(delegation): 接线 SubagentProfile.MaxSteps 步数限制

Execute 循环增加 toolCallCount >= MaxSteps 检查，超限时优雅停止
返回部分结果（与 pass 超限处理一致）。TaskRequest 携带 MaxSteps，
buildDelegatedCursorTaskRequest 通过 LookupSubagentProfile 注入。"
```

---

## Self-Review

### 1. Spec coverage
- ✅ tool_result_snip.go 死代码删除 → Task 1
- ✅ compaction_algorithms.go 死函数删除 → Task 2
- ✅ compaction.go 死函数/死常量删除 → Task 3
- ✅ agent/step/ 孤儿包删除 → Task 4
- ✅ Scheduler.Events() 死代码删除 → Task 4
- ✅ ExecuteToolAsync 死方法删除 → Task 4
- ✅ CountImageParts/ResolveImageContent 死函数删除 → Task 4
- ✅ SubagentProfile.ToolWhitelist 接线 → Task 5
- ✅ SubagentProfile.MaxSteps 接线 → Task 6
- ⏭️ tool_result_snip 不接线（设计有意不接，会破坏 prefix cache）→ 正确决策，不在计划中
- ⏭️ 4 个 record-only CommandKind 不接线（设计性 no-op）→ 正确决策，不在计划中
- ⏭️ SubagentProfile.DefaultModelID 不接线（当前模型覆盖走 LookupSubagentModelOverride）→ 正确决策，不在计划中
- ⏭️ SupervisionTaskContract.MaxSteps 不接线（子代理路径已覆盖，监督路径单独处理）→ 正确决策，不在计划中

### 2. Placeholder scan
- 无 TBD/TODO/placeholder
- 所有步骤都有具体代码

### 3. Type consistency
- `filterDelegatedTools` 签名在 Task 5 Step 3 定义，在 Task 5 Step 4 调用，签名一致
- `TaskRequest.ToolWhitelist` 在 Task 5 Step 1 定义，在 Task 5 Step 2/4 使用，类型一致（`[]string`）
- `TaskRequest.MaxSteps` 在 Task 6 Step 1 定义，在 Task 6 Step 2/3 使用，类型一致（`int`）
