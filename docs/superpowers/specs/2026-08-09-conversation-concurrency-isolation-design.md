# 项目会话并发与上下文隔离设计

**日期：** 2026-08-09
**状态：** 已实施并通过并发、隔离与竞态测试（-race 因本机无 C 编译器无法执行，以 6 次重复并发测试替代压力验证）

## 1. 背景与问题

当两个独立项目会话同时使用同一供应商、同一模型和同一渠道时，日志中出现过同秒 `unexpected EOF`。现有 provider 层能够对尚未向客户端输出有效内容的断流进行透明重连，但并发行为还需要明确两条稳定规则：

1. 不同项目会话必须可以并发，不能因为共享供应商、模型、API key 或渠道而互相排队、取消或阻塞。
2. 同一会话内的 turn 必须严格串行，避免两个 request 同时修改同一份 history/context/checkpoint，造成上下文交叉或覆盖。

当前实现已具备部分基础：

- 活动流以 `requestID` 为键保存在 broker 中。
- 持久化历史按 `history/<conversationID>/state.json` 和 `context.json` 隔离。
- OpenAI `prompt_cache_key` 已使用 `cursor:<conversationID>`。
- 已有按 conversation 的 `runQueue`，但目前只在会话存在运行中子代理时排队。
- 普通新 turn 仍会调用 `cancelOtherConversationActors`，取消同一 conversation 的旧 request。

修复应扩展现有会话队列，而不是增加渠道级 semaphore。渠道级限流会让不同项目会话因为使用同一供应商/模型而相互等待，不符合本设计目标。

## 2. 目标与非目标

### 目标

1. 不同 `conversationID` 永远可以并发，包括同一项目目录下的不同会话。
2. 不同会话即使使用相同 provider、model、base URL、API key 和规范化 channel ID，也不能互相排队或取消。
3. 同一 `conversationID` 内，同一时刻最多运行一个非终态 turn。
4. 同一会话的新 request 进入 FIFO 队列，当前 turn 完整收口后再启动。
5. 当前 turn 正常完成、失败或被用户显式取消后，队列都能继续排空。
6. provider prompt、工具结果、checkpoint、history、debug artifact 和 cache key 都必须绑定所属 conversation/request，不能跨会话污染。
7. 保留现有 provider EOF/overload 重连、渠道健康和 HTTP 连接池复用逻辑。

### 非目标

- 不按供应商、模型、API key、host 或 channel ID 限制跨会话并发。
- 不增加 `maxConcurrentStreams` 或其他渠道级排队配置。
- 不禁止同一项目目录内的多个会话并发。
- 不实现同一 conversation 内多个 turn 的并行分支与历史合并。
- 不允许普通新消息隐式取消上一 turn。
- 不修改已安装 Cursor 客户端 bundle。
- 不把共享 HTTP client/HTTP2 连接池改造成会话状态容器。

## 3. 并发模型

### 3.1 隔离键

并发与上下文隔离键为 `conversationID`：

```text
conversation A + channel X  ─┐
conversation B + channel X  ─┼─ 可以并发
conversation C + channel Y  ─┘

conversation A:
  request/turn 1 → request/turn 2 → request/turn 3
  FIFO 串行
```

项目路径不作为互斥键。同一个项目目录可以存在多个独立 conversation，它们仍可同时运行。

### 3.2 同会话规则

- 一个 conversation 同时只能有一个非终态 turn owner。
- 收到新的 run intent 时，如果同一 conversation 已有活动 turn：
  - 不创建第二个可运行 actor/provider pass。
  - 不取消当前 turn。
  - 将完整 `InboundIntent` 按 FIFO 入队。
- 当前 turn 必须完整运行到 `completed`、`failed` 或 `canceled`。
- 终态收口后仅取出队首 request 启动；下一条继续等待新的终态。
- 重复提交同一个 `requestID` 的 run request 仍按现有 `shouldReuseActiveRun` 去重，不重复入队。

### 3.3 显式取消

- 用户显式 cancel 按目标 `requestID` 处理。
- 当前活动 request 被取消后进入终态并 drain 下一条。
- 如果目标 request 还在 conversation 队列中，应只删除该排队项并给该 request 可靠取消终态；不能取消当前 turn，也不能影响其他 conversation。
- 普通新消息不再产生 `Superseded by newer request`。

## 4. 会话队列设计

### 4.1 复用并扩展 `runQueue`

现有 `runQueue` 已按 conversation 保存 FIFO intent：

```text
map[conversationID][]InboundIntent
```

扩展职责：

- 从“子代理运行期间的新消息队列”变为“所有同会话活动 turn 的新消息队列”。
- 提供原子判定/入队能力，避免两个新 request 同时看到 conversation 空闲并同时启动。
- 提供按 `requestID` 删除排队项的能力，用于显式取消。
- 保持不同 conversation 的队列完全独立。

建议将“活动 owner + pending queue”统一为小型 conversation scheduler，而不是在多个函数中分散执行“先查 broker 再入队”。scheduler 对外提供：

```text
Submit(intent) → start-now | queued | duplicate
Finish(conversationID, requestID) → next intent | none
CancelQueued(conversationID, requestID) → removed | not-found
```

如果沿用 `runQueue` 名称，其内部也必须完成上述原子性，不能仅保留当前简单 slice API。

### 4.2 Owner 生命周期

- run intent 获得 conversation owner 后才能进入 `handleRunIntent` 的持久化和 provider 启动阶段。
- owner 从 request 启动前建立，防止并发提交绕过队列。
- owner 只在该 request 已完成 conversation 终态持久化、checkpoint/broker 终态发布后释放。
- 释放 owner 和取得下一条 intent 必须是同一个 scheduler 原子操作。
- 下一条 intent 的启动采用异步调度，不能阻塞当前 actor 的终态返回。
- 如果下一条启动失败，必须记录失败终态并继续 drain，不能永久占有 owner。

### 4.3 终态入口

所有终态路径必须调用统一的 `finishConversationTurn`/scheduler finish 逻辑，包括：

- 正常 `completeStream`
- `failStream`
- 用户显式 cancel
- checkpoint/blob sync 失败后的终态
- provider terminal error
- context overflow/compaction 失败
- actor panic 转失败终态

不能只在部分函数里手工调用 `drainRunQueue`，否则遗漏的错误路径会让队列永久卡住。

## 5. 上下文隔离

### 5.1 唯一数据流

每次 provider 请求必须沿当前 conversation 投影：

```text
requestID
  → ActiveStream.ConversationID
  → history/<conversationID>/state.json
  → history/<conversationID>/context.json
  → ProjectPromptReplay(conversation)
  → ProviderRequest{RequestID, ConversationID, ModelCallID}
```

严禁从其他 conversation 的 state/context/checkpoint 构造 prompt。

### 5.2 持久化隔离

- `ConversationFileStore` 的所有读写都使用当前 `conversationID`。
- `conversation.lock` 只保护对应 conversation 目录。
- `context.json.items` 的 `request_id` 必须属于该 conversation 的已启动 turn。
- 排队中的 intent 在真正获得 owner 前不能写 user/history entry，避免未运行 request 提前污染 prompt replay。
- turn 启动后才分配/确认 turn sequence，并追加该 request 的 user/request_context entries。
- 同一 conversation 的 `context_version` 和 entry sequence 必须单调递增。

### 5.3 运行态隔离

以下状态必须保留在所属 `ActiveStream` 或以 conversation/request 组成的复合键管理：

- provider cancel/context/token/pass
- pending execs/interactions
- tool call/result
- compaction state
- checkpoint snapshot/blob
- delegation/subagent state
- reasoning/text accumulator
- debug recorder correlation

迟到的 tool/interaction/provider event 必须通过 request/provider token 校验，不得写入当前 conversation 的下一 turn，更不能写入其他 conversation。

### 5.4 Provider 与缓存隔离

- `ProviderRequest.ConversationID` 必须来自当前 stream。
- OpenAI `prompt_cache_key` 保持 `cursor:<conversationID>`。
- Anthropic/Gemini provider-visible cache/replay 元数据不得使用 model/channel 作为会话上下文键。
- 本地 response cache 的命中键必须包含完整 provider-visible request 内容及会话相关字段；不能仅按 model/channel 命中。
- 共享 `http.Client` 和 HTTP/2 连接池只做连接复用，不保存 prompt/history/tool 状态。
- Router 的 `healthByChannel` 只保存渠道冷却状态，不保存任何会话内容。

## 6. 代码调整

### 6.1 Forwarder 入站调度

修改 `dispatchInboundIntent`：

- 对所有 `run` intent 执行 conversation scheduler submit。
- `start-now` 才创建/驱动 stream。
- `queued` 立即 ACK BidiAppend，但不写历史、不启动 provider。
- `duplicate` 沿用当前重复 run 的幂等行为。

当前只调用 `activeConversationHasSubagents` 的特殊分支将被泛化或移除。

### 6.2 移除普通 supersede

删除 `handleRunIntent` 中普通 run 对 `cancelOtherConversationActors` 的调用：

```text
[canceled] Superseded by newer request
```

该行为与同会话 FIFO 冲突。显式用户 cancel 仍保留。

### 6.3 统一终态 drain

将散落在以下路径中的 `drainRunQueue` 收口到统一终态辅助函数：

- cancel
- complete
- fail
- checkpoint terminal

辅助函数必须幂等：同一 request 多次收口只释放一次 owner，也只启动一个下一 request。

### 6.4 Broker 查询语义

`OtherConversationRequestIDs` 可以继续用于活动状态诊断，但不能用于跨 conversation 取消。

判断同 conversation 是否有活动 owner 应通过 conversation scheduler，而不是扫描 broker 后再做非原子决策。

## 7. 错误与恢复

- provider EOF、unexpected EOF、overload 和 timeout 只影响所属 request。
- 现有 pre-output reconnect 保持不变。
- 已经输出有效 ModelEvent 后的 mid-stream interruption 仍不自动重放，避免重复输出或工具执行。
- 当前 turn 失败后记录该 turn 的 provider_error/failed 终态，再启动队列下一项。
- 队列中的 request 不能继承上一 turn 的未完成 provider accumulator、pending exec、provider pass 或 checkpoint。
- 下一 turn 只能从上一 turn 已持久化的终态历史投影。
- 如果终态持久化失败，不能静默启动下一 turn；应先记录可诊断失败，并采用明确的安全恢复策略，避免从不完整 context 继续。

## 8. 日志与诊断

增加或统一这些安全字段：

- `conversation_id`
- `request_id`
- `owner_request_id`
- `queue_len`
- `queue_position`
- `turn_seq`
- `model_call_id`

建议事件：

- `forwarder conversation run queued`
- `forwarder conversation owner acquired`
- `forwarder conversation turn finished`
- `forwarder conversation queue drained`
- `forwarder queued run canceled`
- `forwarder stale cross-request event ignored`

日志不得记录 API key、完整 provider body 或模型可见私密上下文。

## 9. 测试验收

### 9.1 不同会话并发

1. 两个不同 `conversationID` 使用同一 channel 时都能同时进入 provider。
2. 同一项目路径下两个不同 conversation 仍能并发。
3. 一个 conversation 的新消息、取消、provider EOF 和工具结果不影响另一个 conversation。
4. 不同 conversation 不出现 `Superseded by newer request`。

### 9.2 同会话 FIFO

1. 同一 conversation 的第一个 request 活动时，第二个 request 入队且 provider 未启动。
2. 第一条正常完成后启动第二条。
3. 第一条失败后启动第二条。
4. 第一条被显式取消后启动第二条。
5. 第一条等待工具、交互或子代理时，后续消息仍排队。
6. 三条以上 request 保持 FIFO 顺序，每个终态只 drain 一条。
7. 相同 request 的重复 run 不重复入队。
8. 排队 request 被取消时只删除自身，不影响当前 owner 和其他 queued request。

### 9.3 上下文无污染

1. 两个 conversation 的 `ProjectPromptReplay` 只包含各自 context entries。
2. conversation A 的 tool result/model response/checkpoint 不出现在 conversation B。
3. 同 conversation 的第二 turn 只能在第一 turn 终态持久化后读取其结果。
4. 迟到的第一 turn provider/tool event 在第二 turn 启动后被 token/request 校验拒绝。
5. 两个 conversation 使用同一 model/channel 时生成不同的 `prompt_cache_key`。
6. debug/provider/runtime/runsse 记录中的 conversation/request 关联正确。
7. `context_version`、entry seq、turn seq 在每个 conversation 内独立单调递增。

### 9.4 竞态与可靠性

1. 并发提交两个同 conversation request 时，只有一个获得 owner。
2. 终态函数被重复调用时不会重复启动下一条。
3. 下一条启动失败时不会永久占有 owner，队列可以继续收口。
4. 使用 `go test -race` 验证 scheduler、broker 与 history 协调不存在数据竞争。

## 10. 实施范围

预计主要修改：

- `internal/backend/forwarder/actor.go`
- `internal/backend/forwarder/run_queue.go`（或拆出 conversation scheduler）
- `internal/backend/forwarder/service.go`
- `internal/backend/forwarder/blob_sync.go`
- 相关 forwarder/broker/history/projector 测试

按测试证据可能补充：

- provider request/context correlation 断言
- local response cache 会话隔离断言
- debug recorder correlation 测试

不需要修改：

- 模型渠道配置结构和设置界面
- `maxConcurrentStreams`（本设计明确不引入）
- 已安装 Cursor 客户端
- HTTP client 连接池配置，除非独立网络证据证明其本身存在会话状态泄漏
