# Goal Workflow

## 当前状态

cursor-byok 不再实现自研 Goal 循环，也不再识别、剥离或改写 `/goal` / `/goal --strict`。

从本轮开始，`/goal` 作为普通用户输入原样进入 Cursor 请求链路，由 Cursor 客户端和官方 Agent 原生能力处理。forwarder 只保留协议透明转发与既有工具、上下文、委派、统计等通用能力，不维护本地 Goal 状态机。

## 官方依据

- 访问日期：2026-08-31
- 采用来源：Cursor 官方 changelog
- 采用原因：Cursor 官网是 `/goal` 行为和发布时间的权威来源；第三方文章或社区讨论只可作旁证。

Cursor 官方在 2026-08-19 发布 `/goal`：用于给 Agent 一个长期目标，让它持续推进直到完成。本机安装位置也已确认：

- Cursor：`D:\cursor\Cursor.exe`
- CLI：`D:\cursor\resources\app\bin\cursor.cmd`
- CLI 版本：`3.17.21`

## 本地已移除的能力

- forwarder 不再解析 `/goal` / `#goal` 前缀。
- `ActiveStream` 不再保存本地 `GoalState`。
- provider pass 收尾点不再触发本地 Goal 自检、校验子代理、预算停止或错误重试。
- checkpoint 投影不再附加本地 Goal 状态。
- 配置文件不再包含 `goal:` 配置段，也不再为旧配置补写该字段。
- 前端高级设置不再提供“Goal 命令”开关。
- 内置技能不再发布 `goal-loop`。
- 旧的 Rules/Command 近似方案模板已删除，避免覆盖 Cursor 原生命令。

## 保留边界

官方 Cursor 协议生成代码中的 Goal 类型仍然保留，例如 `CreateGoalToolCall`、`UpdateGoalToolCall`、`GoalState` 等。这些类型属于 Cursor `agentv1` 协议面，不是 cursor-byok 的自研 Goal 引擎，不能为了删除本地实现而移除。

委派监督里的 `Contract.Goal` 也继续保留。它表达“子任务目标/完成标准”，不是 `/goal` 命令能力。

## 使用方式

在 Cursor 原生聊天/Agent 中直接输入：

```text
/goal 跑通全部测试并修复失败用例
```

cursor-byok 会保持请求链路透明，不把该命令转换成本地循环。Goal 生命周期、停止语义、完成判定和可视化表现以 Cursor 官方客户端实际实现为准。

## 回滚思路

如需恢复旧的本地 Goal 实现，应作为独立功能重新评估，并至少恢复以下内容：

- forwarder 本地 Goal 状态机与测试
- config `goal:` 配置项和前端设置入口
- `goal-loop` 内置技能
- checkpoint/ToolCall 的本地状态投影

恢复前必须先确认不会与 Cursor 原生 `/goal` 双重接管同一输入。
