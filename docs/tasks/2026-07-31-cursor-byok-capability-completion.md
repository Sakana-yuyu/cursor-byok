# Cursor-byok 能力完善任务清单

## 目标

逐步完善 Cursor-byok 的按钮交互、工具执行、Skills、MCP、Agent 调用链和 Multitask 委派能力。每个阶段独立验收并单独提交，不发版、不推送，保留任意提交作为回滚点。

## 当前基线

- Go 构建：`go build ./...` 通过。
- 前端构建：`npm --prefix frontend run build` 通过。
- 前端存在既有的 chunk 体积提示，不作为本任务失败条件。
- 当前未提交的浮窗相关文件和 `gui-test-screenshots/` 保留，不回滚、不混入本任务提交。
- 协议已包含 `AGENT_MODE_MULTITASK`、子代理模型字段、MCP 和 Skills 字段。
- 子代理当前主要通过 Cursor 客户端 `SubagentArgs` 执行，服务端已有排队不中断保底机制。
- Skills/MCP 已有扫描和设置 UI，但 MCP 工具 schema 与运行时闭环仍需补齐。

## 阶段清单

- [x] Task 1：审查并完善按钮交互（静态扫描确认事件处理器完整；无未定义按钮动作）。
- [x] Task 2：抽离统一 Tool Registry 和执行收口（工具 canonical kind 注册表已接入）。
- [x] Task 3：完善 Skills 注册、中文简介、开关和调用链（内置 skill 与现有扫描开关已接入）。
- [x] Task 4：完成 MCP 运行时发现、schema 注入和执行闭环（stdio、Streamable HTTP 和 legacy SSE 显式连接均已接入）。
- [x] Task 5：稳定 Agent 模式、provider pass、resume 和 checkpoint 调用链。
- [x] Task 6：增加委派模型组配置与持久化。
- [x] Task 7：增加非阻塞 DelegationScheduler。
- [x] Task 8：接入 Cursor 子代理适配器和 workspace hint。
- [x] Task 9：接入本地子代理适配器。
- [ ] Task 10：完成 Multitask 结果合并、失败隔离和取消。
- [x] Task 11：完成委派设置界面和配置持久化（运行状态面板待 Task 10 接入）。
- [ ] Task 12：完成 Cursor 原生工作流对齐审查。
- [ ] Task 13：全面构建、静态检查、协议回放和人工流程验收。

## 每阶段验收

每个阶段执行相关构建或静态检查，结合已有命令级工具和人工流程验证；仓库规则禁止新增测试文件，因此不新增测试目录。验收通过后使用独立 commit，commit 失败或验收失败时停止在当前阶段，不推进后续阶段。

## 提交约束

- 仅提交本任务阶段新增或修改的文件。
- 不提交前序浮窗任务遗留改动。
- 不修改已安装 Cursor 客户端 bundle。
- 不创建 release、tag 或执行 push。
