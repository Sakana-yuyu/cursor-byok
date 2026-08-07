# 委派 Worker 上下文压缩设计

- 日期：2026-08-07
- 状态：已批准
- 范围：`internal/backend/forwarder/delegation_local.go` + 新增 `internal/backend/forwarder/delegation_compaction.go`

## 背景与问题

本地委派 worker（`localDelegatedAgentAdapter.Execute`）的上下文（`messages []modeladapter.Message`）每轮 provider pass 后只增不减：assistant 工具调用与 tool result 原样 append，没有任何压缩、摘要或裁剪。实测（2026-08-07，conversation `e7a44c25`）：

- worker 起步 `input_tokens=85264`（继承父进程上下文），随后单调膨胀，`cache_read` 从 84480 涨到 110080；
- 超限后 `runProviderPass` 直接把 provider 错误抛回，`Execute` 立即 `return TaskResult{Error: err}` —— 子代理标记 failed；
- 唯一兜底是 `maxPasses=32`（`defaultLocalDelegationMaxProviderPasses`），撞上即失败 "exceeded provider pass limit"。

主进程 forwarder 的压缩体系（`compaction.go`、`context_overflow.go`、`tool_result_snip.go`）基于 `ConversationFile.Entries` + `compiler.Compile` 重编译 + hook 流程，结构与 worker 的 `messages` 列表不匹配，无法直接复用，需要为 worker 移植轻量压缩。

## 设计

### 1. 预算计算

新增 `delegatedContextBudget(service *Service, request delegation.TaskRequest, conversation *ConversationFile) (window int64, budget int64)`：

- `window`：优先 `service.resolver.SelectChannelForModel(request.ModelID)` 的 `ContextWindowTokens`；fallback `conversation.TokenDetailsMaxTokens`；两者都无则 `window=0`（不主动压缩，超限自救仍尝试 snip）。
- `budget = 0.8 * window - compactionAutoReserveTokens(10000)`；下限保护 `budget` 不小于 16k（窗口信息过小视为不可用）。

### 2. 主动阈值压缩

新增 `maybeCompactDelegatedMessages(messages []modeladapter.Message, budget int64, stats *delegatedCompactionStats) ([]modeladapter.Message, bool)`，在 `Execute` 每轮 provider pass 前调用：

- 用现成 `estimateModelMessagesTokens(messages)` 估算；`<= budget` 直接返回（无变化）。
- 超预算执行两级压缩，直到预算内或无可压缩：

  **a. snip 超长 tool result**：对 `role=="tool"` 且 `len(Content) > 16KB` 的消息，截断到 4KB 并追加占位文本（工具名 + "输出过长已省略"）。处理顺序：先按消息在列表中的位置从旧到新逐条检查，同一条内若仍超限再进一步截断，直到预算内为止；无更旧的可压时，允许继续压次旧消息。最近一轮（最后一个 assistant/tool 对）不 snip。

  **b. 丢弃早期消息**：仍超预算时，从最旧开始成对丢弃 `assistant`+紧随其后的 `tool` 消息（保证消息交替顺序合法），保留最近 4 轮；逐对丢弃直到预算内。保留规则：

  - `role=="system"` 永不丢弃；
  - worker 首条 `user`（任务 prompt，即 `Execute` 中 `request.Prompt` 追加的那条）尽力保留（丢弃到只剩它为止不再丢）。

- `changed=true` 且记录统计（snip 条数、丢弃条数、压缩前后 token/消息数）供日志使用。

### 3. 超限自救

- 新增 `delegatedContextOverflowError(err error) bool`：匹配 `context_too_large` / `context_length_exceeded` / `exceeds the context window`。
- 在 `Execute` 循环内：`runProviderPass` 返回 err 且匹配超限错误时，用局部计数器（每任务上限 2 次）压缩 `messages` 后重试当前 pass；若压缩无变化或重试后仍失败，走原失败路径。
- **不做** config.yaml 窗口减半持久化（worker 端触发会改动全局 channel 配置、影响主进程，副作用不可接受）。

### 4. 日志

压缩发生时打一条 INFO 日志，字段与现有风格一致：

```
forwarder delegated context compacted task_id=... provider_pass=... snip=3 dropped=4 msgs=120->87 tokens=210000->145000
```

### 5. 代码位置

- `delegation_compaction.go`：预算、snip、丢弃、错误匹配、统计 —— 全部纯函数（除预算中需要 `resolver`），便于单测。
- `delegation_local.go`：`Execute` 循环接入「pass 前主动压缩」与「超限重试」。

## 边界与失败语义

- `budget<=0`：不主动压缩；超限自救仍尝试 snip（snip 无预算依赖）。
- messages 仅剩 system + 首条 user（无可压）：snip/丢弃均 no-op，返回 unchanged。
- 压缩后仍超预算：接受现状继续（总比整轮失败好）。
- 超限自救重试 2 次仍失败：放弃，返回原错误（与现状失败路径一致）。

## 测试计划（`delegation_compaction_test.go`）

1. 未超预算：`maybeCompactDelegatedMessages` 返回 unchanged。
2. snip：超长 tool result 被截断且保留工具名占位；最近一轮不被 snip。
3. 丢弃：预算极小且无长 tool result 时，从最旧开始成对丢弃；system 与首条 user 保留。
4. 顺序合法性：压缩后任意 `tool` 消息之前仍是对应的 `assistant` 消息（或该对已被整体丢弃）。
5. 错误匹配：`delegatedContextOverflowError` 对 context_too_large / context_length_exceeded / exceeds the context window 返回 true，对普通超时/网络错误返回 false。
6. 重试上限：`Execute` 层面通过注入可控错误验证重试次数上限 2。

## 非目标

- 不做 LLM 摘要压缩（worker 内不再额外调用模型）。
- 不接主进程 `ConversationFile.Entries` 重编译流程。
- 不改 `maxPasses` 语义（压缩旨在减少膨胀，让 worker 在有限轮次内跑完）。
