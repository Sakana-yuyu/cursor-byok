# 监督式委派与顾问模式设计

日期：2026-07-31

## 目标

在现有 Cursor-byok Multitask 委派能力之上增加监督式协作：由高能力模型担任顾问/监工，负责拆解任务、选择执行模型、检查子代理进展与结果，并在发现循环、偏离目标或执行失败时发出纠偏、重试、换模型或熔断决策。低成本、高速度模型继续负责具体执行。

第一阶段只接管 Multitask 委派，不改变普通 Agent、Ask、Plan、Build 和 Debug 的默认执行路径。现有 Cursor 客户端只读，现有模型适配器、工具、Skills、MCP 和浮窗/设置能力保持兼容。

## 设计依据

- CrewAI 的 supervisor 与 worker 分层，以及 Crews/Flows 分离。
- LangGraph Supervisor 的结构化 handoff、结果决策和 supervisor 工具化调用。
- AutoGen 的事件驱动消息与终止条件。
- MetaGPT 的角色、目标和预期输出约束。
- OpenHands 的执行后端与编排层解耦、独立运行上下文和运行时观测。

不引入上述项目的完整运行时，只吸收其边界、事件和终止条件设计。

参考项目及 2026-07-31 调研时的 GitHub 星标量级：

- [OpenHands](https://github.com/OpenHands/OpenHands)：约 82k stars，参考运行时与编排解耦、事件观测和可取消执行。
- [MetaGPT](https://github.com/FoundationAgents/MetaGPT)：约 69k stars，参考角色、目标、预期输出和验收约束。
- [AutoGen](https://github.com/microsoft/autogen)：约 60k stars，参考异步消息、终止条件和多代理协作。
- [CrewAI](https://github.com/crewAIInc/crewAI)：约 56k stars，参考 supervisor/worker 分层与任务路由。
- [LangGraph Supervisor](https://github.com/langchain-ai/langgraph-supervisor-py)：约 1.6k stars，参考结构化 handoff 和监督决策接口。

这些项目只作为设计依据；Cursor-byok 继续复用自己的模型适配器、Scheduler、工具、Skills、MCP 和 Cursor 子会话协议，避免引入第二套代理运行时。

## 总体架构

```text
Multitask 请求
    |
    v
SupervisorCoordinator
    |  1. 生成任务契约
    |  2. 选择 worker 模型组
    |  3. 接收检查点
    |  4. 评估并发出决策
    |
    +--> DelegationScheduler --> CursorWorkerAdapter
    |                         --> LocalWorkerAdapter
    |
    +--> SupervisorAdapter（高级模型）
    |
    +--> Runtime snapshots/events --> UI 与主 Agent
```

监督器只负责编排和判断，不直接代替 worker 执行工具。worker 继续复用现有 `delegation.Scheduler`、Cursor 会话、本地 provider pass、工具权限、Skills 和 MCP 权限。

核心模块边界：

- `internal/backend/delegation/`：监督状态、任务契约、检查点、决策和检测器等领域类型。
- `internal/backend/forwarder/`：把现有 Multitask 请求转换为监督任务，调用 supervisor/provider，发布运行时事件。
- `internal/backend/server/config/`：保存监督模型、阈值、开关和兼容配置。
- 前端设置和 Multitask 状态视图：只消费脱敏快照，不能直接操作 provider 会话。

## 监督生命周期

每个监督聚合任务使用以下状态：

```text
planned -> dispatched -> running -> checkpointing -> reviewing
                                      |              |
                                      |              +--> completed
                                      |              +--> continue -> running
                                      |              +--> correcting -> running
                                      |              +--> retrying -> running
                                      |              +--> reassigning -> dispatched
                                      |              +--> escalated -> running
                                      |              +--> circuit_open
                                      |
                                      +--> failed / canceled
```

状态转换只能由 `SupervisorCoordinator` 触发，并且带有单调递增的监督轮次和事件序号。晚到的 worker 事件必须按任务 ID、聚合 ID、监督轮次和事件序号校验，不能启动下一轮 provider pass 或污染主代理历史。

默认参数：

- 最大 worker 并发数：4，复用现有调度器配置。
- 单任务最大纠偏次数：2。
- 单任务最大重试次数：1。
- 单任务最大监督轮次：8。
- 检查点超时：沿用 worker 超时的剩余时间，不额外阻塞主请求。
- supervisor 默认使用主代理当前模型，也可以在委派设置中单独指定。
- reviewer 默认复用 supervisor 模型，允许单独指定。

## 任务契约

监督器派发给 worker 的任务不是裸 prompt，而是结构化契约：

- `task_id`、`aggregate_id`、`supervision_round`
- `goal`：必须完成的目标
- `scope`：允许操作的文件、目录或资源范围
- `role`：`implementer`、`researcher`、`reviewer` 等有限角色
- `allowed_tools`：继承主任务权限后可进一步收紧
- `expected_output`：结果格式和必要证据
- `done_criteria`：可检查的完成条件
- `max_steps`、`timeout`：执行边界
- `failure_policy`：重试、换模型、升级或熔断
- `workspace_hint`、Skills/MCP 选择和模型路由元数据

契约与现有 `delegation.TaskRequest` 组合使用。旧调用方未提供监督字段时，仍按普通 worker 请求执行。

## 检查点与进度

worker 通过事件上报轻量检查点，不把完整工具参数或敏感内容复制到 UI：

- 当前步骤和阶段
- 最近工具名称及调用次数
- 修改文件摘要
- 进度摘要
- 阻塞原因
- 最近一次有效进展时间
- 当前契约版本和监督轮次

检查点可由 worker 适配器定时产生，也可在工具调用、文件写入、provider pass 完成等已有事件点产生。检查点发布必须是非阻塞的；事件缓冲满时保留终态和最新状态，丢弃过旧的中间进度。

## 监督判断与纠偏

监督器把 worker 结果和检查点交给 supervisor/reviewer，要求返回严格结构化决策：

- `accept`：满足完成标准，收纳结果。
- `continue`：目标明确且有进展，允许继续。
- `correct`：目标未偏离但执行策略有问题，附带下一步指令。
- `retry`：当前执行失败且可重试，保留失败原因摘要。
- `reassign`：当前模型不适合，切换启用的模型组或模型。
- `escalate`：交给更高能力模型处理当前子任务。
- `circuit_open`：达到轮次、纠偏、重试或无进展阈值，停止该任务并返回可解释失败。

纠偏指令必须包含原因、禁止重复的动作、下一步目标和新的完成条件。纠偏只作用于对应 worker，不影响兄弟任务。

## 无进展与循环检测

检测器采用可解释的启发式指标，不依赖单一模型判断：

- 连续重复相同工具、相同命令或相同参数签名。
- 连续检查点没有新增文件、输出证据或状态变化。
- 多次恢复同一错误但没有改变策略。
- 工具调用超出契约范围。
- 修改文件与 scope 不匹配。
- worker 报告完成但完成条件缺少证据。

检测结果记录为分类原因：`tool_failure`、`no_progress`、`repeated_action`、`scope_drift`、`missing_evidence`、`timeout`、`model_failure`。达到阈值后先触发一次纠偏，再根据上限执行重试、换模型或熔断。

## 非阻塞调用链

主代理提交监督任务后立即返回 pending exec，主进程不等待 worker 或 supervisor 完成。监督器通过独立 goroutine、事件通道和现有 stream command 回传状态。每次异步回调都校验：

- `aggregate_id`
- `task_id`
- `parent_exec_id`
- `provider_pass`
- `supervision_round`

如果主流已完成、取消或进入新 provider pass，旧事件只更新可丢弃的运行时快照，不再触发模型调用。取消聚合任务时，先标记监督状态，再取消 worker 和 supervisor 请求，保证晚到结果不会重新唤醒任务。

## 结果汇总

监督器只收纳通过审查的 worker 结果，同时保留失败和被熔断任务的原因。汇总结果包含：

- 聚合任务 ID 和最终状态
- 每个子任务的模型、角色、状态、耗时和输出摘要
- 纠偏、重试、换模型和升级次数
- 工具调用摘要和检测到的问题
- 部分成功时的可用结果与未完成项
- 主代理继续工作所需的后续建议

完整 prompt、工具参数、凭据、工作区绝对路径不进入 UI 快照或主代理历史，避免泄漏和上下文膨胀。

## 配置与界面

在现有委派模型组配置上追加监督配置，不重复保存 API Key：

- 监督模式开关，默认关闭以保护现有行为。
- supervisor 模型或“跟随主模型”。
- reviewer 模型或“跟随 supervisor”。
- 默认 worker 模型组。
- 最大纠偏次数、最大重试次数、最大监督轮次。
- 是否允许换模型、升级和人工确认。
- 检查点刷新频率和是否显示工具摘要。

Multitask 运行面板增加当前阶段、监督模型、worker 状态、纠偏次数、循环原因和取消操作。所有状态均来自脱敏快照；配置保存成功、校验失败和运行时错误都必须有明确反馈。

## 错误处理与兼容性

- supervisor 不可用时：如果监督模式是强制开启，聚合任务失败并说明原因；如果是可选模式，降级为现有 Multitask 调度。
- 单个 worker 失败：隔离失败，其他 worker 继续运行。
- reviewer 超时：保守进入重试或熔断，不把未经审查的结果标记为完成。
- 监督器自身取消：取消未完成的监督和 worker，保留已完成结果。
- 配置字段缺失：使用安全默认值；未知枚举值归一化为兼容模式。
- 不修改已有 protobuf 字段编号；需要传输的新字段只追加字段。
- 不修改已安装 Cursor bundle，不改变现有普通 Agent/Ask/Plan/Build/Debug 的默认调用链。

## 验收策略

仓库规则禁止新增测试文件，因此采用现有构建、静态检查、协议回放、日志证据和人工流程验收：

1. `go build ./...` 和前端构建通过。
2. 监督开关关闭时，旧 Multitask 行为保持不变。
3. 开启监督后，多个 worker 可并发运行且主代理不阻塞。
4. 模拟重复工具、无进展、scope 偏离和工具失败，分别观察纠偏、重试、换模型和熔断。
5. 一个 worker 失败时，兄弟 worker 仍能完成并正确汇总。
6. 取消、超时、重连、历史恢复和晚到事件不会触发重复 provider pass。
7. 设置界面可保存并恢复监督模型、阈值和启停状态。
8. UI 不展示 prompt、工具参数、凭据或绝对路径。

## 分阶段实施边界

1. 领域类型、监督状态机和结构化任务契约。
2. worker 检查点、循环/无进展检测和事件校验。
3. supervisor/reviewer 调用、纠偏、重试、换模型和熔断。
4. 运行时快照、主 Agent 汇总和取消链路。
5. 配置持久化、设置界面和 Multitask 状态展示。
6. 构建、静态检查、协议回放和人工端到端验收。
