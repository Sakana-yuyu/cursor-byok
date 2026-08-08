# 子代理 Thinking 转发与委派错误修复记录

> 日期：2026-08-09
> 涉及文件：`actor.go`、`delegation_local.go`、`delegation_multitask.go`、`events.go`、`service.go`、`service_runtime_state.go`、`types.go`
> 触发场景：子代理 Task 卡片显示 "Stopped with error"、thinking 内容重复嵌套/不显示文本、子代理卡片下方出现父级流 UUID、未显示真实模型名

---

## 一、问题背景

用户反馈三个子代理相关问题：

1. **thinking 没有还原 Cursor 体验**：子代理执行时看不到模型的思考内容
2. **子代理直接报错 "stopped with error"**：子代理任务直接失败
3. **thinking 全是重复文本**：每个词都被 "【委派任务：xxx】" 前缀重复嵌套

通过对比上游 fork 项目（leookun/cursor-byok）和分析服务端日志逐步定位，最终发现并修复了 **6 个关联问题**。

---

## 二、问题诊断与修复过程

### 问题 1：父代理 encrypted-only thinking 被抑制

**现象**：使用 OpenAI Responses API 的模型（如 gpt-5.3-codex-spark）时，模型返回加密签名但无可读 thinking 文本，用户看不到任何 thinking 指示。

**根因**：本仓库在 commit `ce601b8` 中把上游 fork 项目原本的 encrypted-only 合成占位文本**完全移除**了。`actor.go:710-715` 把 `shouldEmitSyntheticThinking` 设回 false + `suppressThinkingCompleted=true`，导致首次也不发布任何 thinking 指示。

**上游对比**：fork 项目（leookun/cursor-byok）在 `actor.go` 中，首次遇到 encrypted-only 场景时会发布一条合成占位文本 "The reasoning process is encrypted. Please wait a moment..."，后续才抑制。

**修复**（`actor.go`）：
- 恢复首次发布逻辑：当 `ProviderSyntheticThinkingPublished=false` 时发布合成占位文本 "模型正在加密推理中，请稍候……" 并标记已发布
- 后续 encrypted-only 事件才抑制（`suppressThinkingCompleted=true`）

**关键代码**：
```go
// 修复前（完全抑制）：
shouldEmitSyntheticThinking = false
suppressThinkingCompleted = true

// 修复后（首次发布，后续抑制）：
if !stream.ProviderSyntheticThinkingPublished {
    stream.ProviderSyntheticThinkingPublished = true
} else {
    shouldEmitSyntheticThinking = false
    suppressThinkingCompleted = true
}
```

---

### 问题 2：子代理 thinking 完全缺失

**现象**：子代理执行时模型的思考内容被静默丢弃，不转发给客户端显示。

**根因**：`delegation_local.go` 的 `runProviderPass` 方法中，`StartStream` 的事件回调 switch **只有 4 个 case**（`TextDelta`、`ToolLikeCompleted`、`ProviderError`、`TurnFinished`），完全忽略了 `ThinkingDelta` 和 `ThinkingCompleted` 事件。模型的思考内容被静默丢弃，`ToolInvocation` 的 `ReasoningContent` 等字段从未被填充。

**对比父代理**：父代理 `actor.go:661-679` 正确处理了 `ThinkingDelta`（累积到 `ProviderAccumulatedReasoning`），`actor.go:806-811` 在 `ToolLikeCompleted` 时把累积的 reasoning 设置到 invocation 上。委派 worker 路径完全漏了这套逻辑。

**修复**（`delegation_local.go` `runProviderPass`）：
- 增加本地 reasoning 累加器（`reasoningBuilder` + 5 个签名/载体变量）
- 增加 `ThinkingDelta` case：累积 reasoning 文本
- 增加 `ThinkingCompleted` case：捕获签名/载体供 replay
- 修改 `ToolLikeCompleted` case：把累积的 reasoning 设置到 invocation 上
- 在工具调用边界和 pass 结束时清空累加器

---

### 问题 3：子代理 "stopped with error"

**现象**：日志 `app.log` 显示 `detail="local delegated agent exceeded 32 provider passes"`，子代理 worker 跑满 32 pass 后超限，整个 Task 被标记为 `SUBAGENT_RUN_STATUS_ERROR`（客户端渲染 "Stopped with error"）。

**根因**：`delegation_local.go:175-180` 在 pass 超限时返回 `TaskResult{Error: "exceeded 32 provider passes"}` 且 **Output 为空**（丢弃了 31 个 pass 的累积成果）。scheduler 把带 Error 的结果标记为 `TaskFailed`，`collectAggregate` 走 `failed` 分支，`delegationResultRunStatus` 映射为 `SUBAGENT_RUN_STATUS_ERROR`。

**上游对比**：上游 Cursor proto 无子代理 turn/step 上限，也无 partial result 概念。这是 byok 自主的扩展。

**修复**（`delegation_local.go`）：
- pass 上限从 32 提高到 50（`defaultLocalDelegationMaxProviderPasses = 50`）
- 超限时不再返回 Error，改为返回带部分结果的 `TaskResult{Output: lastOutputText}`（不带 Error）
- checkpoint 状态改为 `SupervisionStatusCompleted`，detail 标注 "已达 pass 上限，返回部分结果"
- 效果：scheduler 标记为 `TaskCompleted` -> `SUBAGENT_RUN_STATUS_SUCCESS` -> 客户端显示成功

---

### 问题 4：thinking 全是重复嵌套文本

**现象**：子代理 thinking 每个 chunk 都被 "【委派任务：全量项目审查与风险识别】" 前缀重复嵌套，完全不可读。

**根因**：`ThinkingDelta` case 调用了 `delegation.PublishWorkerVisibleUpdate(ctx, text)`，该函数经过 `publishLocalWorkerProgress`，它给**每个 thinking chunk** 都加上 "【委派任务：xxx】" 前缀并发布完整的 thinkingDelta 消息。`publishLocalWorkerProgress` 是为**节流后的完整进度摘要**设计的，不是为**高频流式 delta** 设计的。

**修复**（`delegation_local.go`）：
- 在 `localDelegatedAgentAdapter` 增加 `broker *StreamBroker` 字段
- `ThinkingDelta` case 不再调用 `PublishWorkerVisibleUpdate`，改为直接通过 broker 发布原始 delta

---

### 问题 5：子代理只显示 thinking 不输出内容

**现象**：修复问题 4 后，子代理一直显示 thinking，但没有文本内容输出。

**根因**：子代理的 `TextDelta` 事件只走 `flushVisibleText` -> `PublishWorkerVisibleUpdate` -> `publishLocalWorkerProgress`，而后者把所有文本都包装成 `buildThinkingDeltaMessage`（thinkingDelta）发布。子代理的**文本输出从未作为 textDelta 发布到客户端**——全部变成了 thinking。

**修复**（`delegation_local.go` + `events.go`）：
- `TextDelta` case 改为直接通过 broker 发布 `buildTaskToolCallDeltaMessage` + 新增的 `buildTextDeltaInteraction`
- `flushVisibleText`/`PublishWorkerVisibleUpdate` 仍保留用于 checkpoint detail 进度摘要
- 新增 `buildTextDeltaInteraction` 函数（`events.go`），与 `buildThinkingDeltaInteraction` 对称

---

### 问题 6：子代理卡片下方显示父级流 UUID

**现象**：子代理 Task 卡片下方显示了父代理的 request_id UUID（如 `3e74dcc4-62d8-42eb-85e5-2dbda107c6b1`）。

**根因**：子代理的 `TextDelta` 和 `ThinkingDelta` 通过 `adapter.broker.Publish(requestID, ...)` 发布了**裸** `buildTextDeltaMessage` / `buildThinkingDeltaMessage` 到父级流。这些裸消息没有关联到子代理的 `toolCallID`，混入了父代理主对话流，Cursor 客户端把父级流的 request_id 标识显示在了子代理卡片区域。

同样，`publishLocalWorkerProgress` 也发布了裸 `buildThinkingDeltaMessage`。

**修复**（`delegation_local.go` + `delegation_multitask.go`）：
- `runProviderPass` 的 `TextDelta`/`ThinkingDelta` case：移除裸 `buildTextDeltaMessage`/`buildThinkingDeltaMessage` 发布，**只通过** `buildTaskToolCallDeltaMessage`（关联到 `toolCallID`）转发到子代理 composer
- `publishLocalWorkerProgress`：移除裸 `buildThinkingDeltaMessage` 发布，只保留 `buildTaskToolCallDeltaMessage` 路径

**核心原则**：子代理的所有 delta 消息都**只通过 `task_tool_call_delta` 通道**（关联到 `toolCallID`）发布，**绝不发布裸 delta 到父级流**。

---

### 附加修复：Task 卡片未显示真实模型名

**现象**：Task 卡片显示模型别名 "fast" 而非真实模型名 "gpt-5.3-codex-spark"。

**根因**：`buildTaskArgsFromJSON` 原样保留了 Task 工具参数中的 `model: "fast"` 别名。`ensureTaskToolCallModel` 之前只在 `args.Model` 为空时填充，但 args 里已有 "fast"，不会被覆盖。

**修复**（`delegation_multitask.go` + `service.go`）：
- 新增 `ensureTaskToolCallModel` 函数：**总是覆盖** `args.Model`，用 `stream.ModelName`（用户可读名称）替换别名
- 在 `service.go` 的 `buildStartedToolCall` 调用后，对 Task 类型工具调用调用此函数

---

## 三、架构要点（防再次踩坑）

### 子代理 delta 发布的唯一正确通道

```
子代理 provider SSE 事件
  └─ runProviderPass 回调
       ├─ ThinkingDelta → 累积到 reasoningBuilder + 通过 buildTaskToolCallDeltaMessage 转发
       ├─ TextDelta → 累积到 textBuilder + 通过 buildTaskToolCallDeltaMessage 转发
       ├─ ThinkingCompleted → 捕获签名/载体（不转发到客户端）
       └─ ToolLikeCompleted → 把累积的 reasoning 设置到 invocation 上
```

**关键约束**：
1. 子代理的 delta **只通过 `buildTaskToolCallDeltaMessage`**（关联到 `toolCallID`）发布，**绝不发布裸 `buildTextDeltaMessage`/`buildThinkingDeltaMessage` 到父级流**
2. `publishLocalWorkerProgress` 是为**节流后的进度摘要**设计的，不是为**高频流式 delta** 设计的——流式 delta 必须直接通过 broker 发布
3. 子代理的 reasoning 累积逻辑必须与父代理 `actor.go` 对齐（`ThinkingDelta` 累积 -> `ToolLikeCompleted` 挂到 invocation -> 边界清空）

### pass 超限处理原则

- 超限时**保留部分结果**优雅返回（`TaskResult{Output: lastOutputText}`，不带 Error），而非直接 ERROR
- scheduler 标记为 `TaskCompleted` -> `SUBAGENT_RUN_STATUS_SUCCESS` -> 客户端显示成功
- detail 字段标注 "已达 pass 上限"，信息透明

### encrypted-only thinking 处理

- 首次发布合成占位文本（让用户看到 thinking 指示）
- 后续抑制（避免重复噪音）
- 签名保留供下一轮 tool-call 请求 replay

---

## 四、涉及文件清单

| 文件 | 改动内容 |
|---|---|
| `internal/backend/forwarder/actor.go` | 恢复 encrypted-only thinking 首次合成占位文本发布 |
| `internal/backend/forwarder/delegation_local.go` | 增加 thinking 转发 + reasoning 累积 + pass 超限保留部分结果 + 提高 pass 上限到 50 + broker 字段 |
| `internal/backend/forwarder/delegation_multitask.go` | `publishLocalWorkerProgress` 移除裸 thinkingDelta 发布 + `ensureTaskToolCallModel` 函数 |
| `internal/backend/forwarder/events.go` | 新增 `buildTextDeltaInteraction` 函数 |
| `internal/backend/forwarder/service.go` | Task 工具调用用真实模型名覆盖别名 |
| `internal/backend/forwarder/service_runtime_state.go` | SubagentId/DelegationRunProgress（此前已提交） |
| `internal/backend/forwarder/types.go` | DelegationRunProgress 字段（此前已提交） |

---

## 五、调试方法

### 查看子代理错误原因
```bash
grep "delegation run terminal recorded" ~/.cursor-local-assistant-v2/logs/app.log | tail -5
# 查看 detail= 字段，如 "exceeded 32 provider passes" 或 "已达 pass 上限"
```

### 查看 thinking delta 是否正常转发
```bash
grep "thinking delta\|text delta" ~/.cursor-local-assistant-v2/logs/app.log | tail -10
# 确认 thinking_delta_count 和 text delta 是否有值
```

### 查看子代理 pass 执行情况
```bash
grep "local delegated provider completed\|local delegated provider failed" ~/.cursor-local-assistant-v2/logs/app.log | tail -10
# 查看 provider_pass 和 tool_calls 数量
```

### 查看 SubagentRunState 状态
```bash
grep "delegation checkpoint run" ~/.cursor-local-assistant-v2/logs/app.log | tail -10
# 查看 status=SUBAGENT_RUN_STATUS_* 和 subagent_id
```
