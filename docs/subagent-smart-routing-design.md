# 子代理智能委派方案设计（需 Cursor 客户端配合）

## 目标

在子代理（Task 工具）运行期间收到新消息时，不中断正在运行的子代理，而是智能路由：
- 若新消息与正在运行的子代理**针对同一工作区** -> 委派给该子代理继续处理。
- 若**不冲突/不同工作区** -> 开启新子代理并行处理。

## 现状与约束（已查证）

cursor-byok 作为 Cursor 客户端与 provider 之间的转发层，**单独无法实现智能委派**，原因：

### 1. 无法自行创建子代理
子代理（Task 工具）只能由**模型**返回 tool_call 触发：
- `handleToolInvocation`（service.go:1782）只在 `applyProviderModelEvent` 收到 `ModelEventKindToolLikeCompleted` 时被调用（actor.go:599）。
- `openTask`（bridge.go:764）只负责把模型产出的 Task 意图打包成 `SubagentArgs` 发给 Cursor 客户端，不自发创建。
- cursor-byok 没有任何代码路径能自己注入一个 Task tool_call。

### 2. 无法向运行中的子代理注入消息
- 子代理跑在独立的**子会话**里，有自己的 requestID/ConversationID/RunSSE，由 **Cursor 客户端**驱动。
- cursor-byok 父 stream 只阻塞等待 `PendingExec{ExecKind:"subagent"}` 的结果（service.go:1906），**不持有子代理 stream 的句柄**。
- `StreamBroker` 按 requestID 索引 stream，子代理的 ConversationID 不同，broker 无法寻址子代理 stream。
- 没有 parent-side injection API。

### 3. 无子代理工作区结构化元数据
- cursor-byok 只能看到 Task 的 `subagent_type`/`prompt`/`description` 等文本参数。
- `SubagentArgs` 没有「文件路径/工作目录/仓库」字段。
- "同一工作区"判断只能靠解析自由文本，不可靠。

### 4. 同会话并行 turn 会破坏 checkpoint
- 架构是「一会话一活动 stream」，靠取消旧 stream 强制保证。
- 若不取消，两个 stream 各自的内存 checkpoint 会互相覆盖、序列号冲突（`NextEntrySeq`/`NextTurnSeq` 碰撞）。
- 文件锁（`acquireConversationLock`）能防 corruption，但语义会乱。

## Part A（已实现）：排队不中断保底

cursor-byok 侧已实现的保底行为：
- 子代理运行期间的新消息**入队**（`run_queue.go`），不取消旧 stream。
- 当前 turn 终态后自动排空队列，新建 stream 跑新 turn。
- **不杀子代理、不丢消息**，但**不做智能委派**（新消息仍作为父会话的新 turn 处理，不是塞进子代理）。

这解决了「中断子代理」的痛点，但没实现「委派给同一工作区的子代理 vs 开新子代理」的智能路由。

## 需要的 Cursor 客户端能力

智能委派的最终决策与执行必须在 **Cursor 客户端**完成，因为它：
- 持有子代理的 RunSSE（能 resume 子代理会话）。
- 能看到子代理实际读了哪些文件（工作区信息）。
- 能决定是向子代理会话追加 user message，还是在父会话发起新 Task。

### 客户端需要实现的能力

1. **父会话新消息不取消子代理**
   - 客户端在新消息时不向父会话发 cancel（cursor-byok Part A 已解决服务端取消；但客户端若有自己的取消逻辑需对齐）。
   - 客户端识别「父会话有运行中子代理」状态。

2. **路由决策器**
   - 客户端持有每个运行中子代理的 `prompt` + `description` + 实际工作文件（客户端能看到子代理读了哪些文件）。
   - 用一个轻量模型调用（或规则）判断新消息是否与某子代理「同一工作区/有上下文冲突」。
   - 决策输出：`delegate_to:<subagent_id>` 或 `spawn_new`。

3. **委派到现有子代理**
   - 客户端 resume 该子代理的 RunSSE，把新消息作为子代理会话的后续 user message 追加。
   - 子代理在已有上下文基础上继续工作。

4. **开新子代理**
   - 客户端在父会话发起新的 Task tool_call（让父 provider 决定子代理类型与 prompt），
   - 或客户端直接构造一个新的子代理会话（若客户端支持）。

## 推荐协议扩展（cursor-byok 可配合）

为辅助客户端做路由决策，cursor-byok 可在以下方面扩展（需后续实现）：

### 1. SubagentArgs 增加 workspace_hint
子代理 dispatch 时，cursor-byok 从父会话上下文推断工作目录/仓库，写入 `SubagentArgs.workspace_hint`，供客户端路由参考。

### 2. RouteHint 元数据消息
cursor-byok 在新消息入队/到达时，发一条包含「当前活跃子代理列表 + 其 description/prompt 摘要」的 hint 给客户端，辅助客户端决策。格式建议：
```json
{
  "type": "route_hint",
  "active_subagents": [
    {"exec_id": "...", "subagent_type": "...", "description": "...", "workspace_hint": "..."}
  ],
  "queued_message_preview": "..."
}
```

### 3. 不取消的显式信号
cursor-byok 入队时已记日志 `forwarder run queued behind subagent`；可同时给客户端发一条 metadata 消息，告知「消息已排队，未中断子代理」，让客户端 UI 给用户反馈。

## 为什么不能在 cursor-byok 做（总结）

| 能力 | cursor-byok | Cursor 客户端 |
|---|---|---|
| 创建子代理 | ✗ 只能转发模型产出的 Task | ✓ 可发起 Task |
| 向子代理注入消息 | ✗ 无句柄、无 API | ✓ 持有子代理 RunSSE |
| 工作区元数据 | ✗ 仅文本参数 | ✓ 能看到子代理读写文件 |
| 路由决策执行 | ✗ 决策了也无法执行 | ✓ 可 resume/新建 |
| 并行 turn 安全性 | ✗ checkpoint 冲突 | ✓ 各子代理独立会话 |

## 落地路径建议

1. **短期**：Part A 排队不中断（已实现），解决「子代理被杀」痛点。
2. **中期**：cursor-byok 实现 RouteHint + workspace_hint 协议扩展，给客户端提供路由参考信息。
3. **长期**：Cursor 客户端实现路由决策器 + 委派/新建执行，完成智能委派闭环。

## 相关代码位置

- 排队实现：`internal/backend/forwarder/run_queue.go`
- 拦截入口：`internal/backend/forwarder/actor.go` `dispatchInboundIntent`
- 终态排空：`service.go` `completeSuccessfulTurn` / `failActiveStream` / `handleCancelIntent`
- 子代理 dispatch：`internal/backend/agent/bridge/exec/bridge.go` `openTask`
- 子代理 pending exec 注册：`service.go:1906`
- Task -> subagent 映射：`service.go:3653` `execKindFromToolName`
