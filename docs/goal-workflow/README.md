# Goal Workflow

## 官方 Cursor 近似方案

如果不修改官方 Cursor 客户端代码，可以使用 Rules 与 Command 的组合，近似提供 Goal 循环执行体验：

1. 将 `docs/goal-workflow/.cursor/rules/goal.mdc` 复制到项目的 `.cursor/rules/goal.mdc`。
2. 将 `docs/goal-workflow/commands/goal.md` 复制到项目的 `.cursor/commands/goal.md`；也可以将同等内容制作成一个 skill。
3. 在 Cursor 中使用 `/goal 目标` 发起任务。

Agent 会按照规则持续推进目标、分析失败并重试，并在阶段完成时汇报进度。这个近似方案有明确限制：没有真实的预算上限，没有校验子代理的二次确认，进度主要依靠 Agent 自觉汇报，也没有内置的独立 Goal 状态面板与停止控制。

## 与 cursor-byok 内置 goal 的差异

| 能力 | 官方 Cursor Rules/Command 近似方案 | cursor-byok 内置 goal |
| --- | --- | --- |
| 预算 | 无真实的 provider pass、时长或费用上限，主要依赖提示词自控 | 可配置 provider pass、时长与费用预算，达到上限后停止 |
| 校验 | 没有独立校验子代理二次确认 | 支持只读校验子代理，以 `VERIFIED` / `NOT_VERIFIED` 证据确认完成 |
| 状态面板 | 没有独立状态面板，只能查看对话中的进度文字 | 前端 Goal 面板展示状态、进度、pass、工具调用、自检与结果 |
| 停止控制 | 没有 Goal 专用停止控制，依赖停止当前对话 | 前端 Goal 面板提供停止运行中的 Goal 操作 |

## cursor-byok goal 使用说明

### 通过命令发起

在对话中输入 `/goal 目标`，例如：

```text
/goal 跑通全部测试并修复失败用例
```

也可以使用 `#goal 目标`，或使用严格校验模式 `/goal --strict 目标`。Goal 会循环执行、在 provider 错误时有限重试，并在目标声明完成后进行完成校验。

### 通过前端 Goal 面板发起

打开前端 Goal 面板，填写目标并选择模型，然后点击开始执行。面板会轮询显示当前 Goal 的状态与进度，并可停止仍在运行的 Goal。

### 配置与状态查看

- 配置入口：`设置 → Goal`，可调整启用开关、provider pass 上限、时长、费用和自检相关参数。
- 状态查看：官方 Cursor 对话流中的 summary 与完成文本，以及前端 Goal 面板中的 Goal 卡片。
- 当前 MVP 的 Goal 状态仅保存在内存中；应用重启或重连后，旧 Goal 记录不会恢复。
