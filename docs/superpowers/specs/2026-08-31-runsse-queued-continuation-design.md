# RunSSE 排队续跑修复设计

## 目标

修复 Cursor 当前回合完成后，下一条排队消息已经进入对话区，却没有建立后续模型请求，并长期显示 `Taking longer than expected...` 的问题。

## 事实依据

- 故障会话为 `f0ae5214-560d-413f-a569-1dc076546b9d`，旧请求为 `0c3f0ab5-6564-4205-8fd2-5eeeee9f7fbe`。
- 后端已完成 provider、checkpoint、`turnEnded` 和成功终态，且没有待处理工具或交互。
- Cursor 在 `turnEnded` 处理期间尝试通过当前 `ControlledConversationActionManager` 提交排队消息的 `userMessageAction`。
- RunSSE 已在此之前完成关闭，客户端抛出 `WriteIterableClosedError: ControlledConversationActionManager is not accepting actions`。
- 异常发生在 composer 清理 `status`、`generatingBubbleIds` 和 `chatGenerationUUID` 之前，因此旧请求残留为 `generating`。
- 后续请求 `90e5c50b-b5a7-496b-9c0b-f14c727cfb0a` 是用户手动重新发送产生的，不属于自动续跑链路。
- 仓库已有 `reopenTerminalStreamForNewTurn`，能够在同一 request 收到新的 `conversation_action` 后重开终态流；缺失的是客户端提交该 action 所需的短暂传输窗口。

## 修改范围

只修改 forwarder 的 RunSSE 成功终态处理和对应聚焦测试：

- 成功 End 到达后，不立即关闭 RunSSE，而是进入一个有上限的短暂 continuation 宽限。
- 宽限期间如果同一 request 被 `reopenTerminalStreamForNewTurn` 重开，RunSSE 忽略旧回合 End，继续读取新回合 backlog。
- 宽限结束仍未出现 continuation 时，保持现有行为，正常返回成功终态。
- 失败、取消、provider error 等非成功终态不进入宽限，继续立即返回。

不修改已安装 Cursor、客户端 bundle、签名、feature gate 或工作区已有业务改动。

## 数据流

1. 后端完成旧回合并发布最终 checkpoint。
2. 后端发布 `turnEnded`，随后 broker 发布成功 End。
3. RunSSE 先把 `turnEnded` 发送给 Cursor，读取到成功 End 后进入短暂等待。
4. Cursor 在 `turnEnded` handler 中提交排队消息的 `conversation_action`。
5. forwarder 通过既有 `ForceNewTurn` 和 `reopenTerminalStreamForNewTurn` 重开同一 request。
6. RunSSE 检测到新 backlog 或流已恢复为非终态，继续原连接并发送下一回合事件。
7. 没有排队消息时，等待到期并按原逻辑关闭成功流。

## 并发与边界

- continuation 判定必须同时检查当前 cursor 后是否出现新 backlog，以及流是否已从终态恢复，避免只依赖定时器或单次 signal。
- signal 可能包含旧终态留下的唤醒通知；等待逻辑需要重新检查状态，不能把一次 signal 直接视为 continuation。
- 如果新回合在宽限内快速完成，新 backlog 仍应被继续消费，不能因流再次进入终态而返回旧回合结果。
- 客户端上下文取消时立即退出等待，不延长已断开的连接。
- 宽限只作用于无错误的成功 End，不能延迟真实失败和用户取消反馈。

## 测试

先添加聚焦失败测试，证明当前实现会在成功 End 后立即返回，无法承接同 request 的新回合。修复后覆盖：

1. `turnEnded + success End` 后，在宽限内重开同一 request，RunSSE 不退出并继续发送第二回合事件。
2. 没有 continuation 时，RunSSE 在有界等待后正常成功结束。
3. 失败或取消 End 不等待，保持原错误语义。
4. continuation 发生时不重复发送旧回合消息或旧 End。
5. 运行 forwarder 聚焦测试、相关 Go 包测试和 `git diff --check`。
6. 使用真实 Cursor 连续排队两条消息，确认第二条自动建立执行链路，不需要手动重新发送。

## 风险与回滚

- 普通成功回合的连接释放会增加一个很短且有上限的等待；模型执行和历史写入不受影响。
- 若 continuation 判定错误，可能延迟成功流关闭或重复消费 backlog，因此测试必须锁定 cursor 和 backlog 边界。
- 回滚只需撤销 RunSSE continuation 宽限和对应测试，不涉及配置、数据库或历史迁移。
