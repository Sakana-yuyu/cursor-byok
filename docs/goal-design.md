# cursor-byok Goal 循环执行（codex-style goal）设计文档

> 状态：已废弃。2026-08-31 起，cursor-byok 已移除自研 Goal 循环、配置、前端开关和内置 `goal-loop` 技能；`/goal` 原样交给 Cursor 官方原生能力处理。本文仅作为历史方案与回滚评估参考，不代表当前实现。
>
> 配套实施计划：`docs/superpowers/plans/2026-08-07-goal-loop.md`（10 个任务，TDD 拆解）
> 借鉴来源：Reasonix Goal 模式（`docs/GUIDE.md::s022`、`docs/GOAL_ENFORCEMENT.zh-CN.md`）

---

## 1. 目标与背景

让 cursor-byok 支持 **goal 模式**：用户给 Agent 一个明确目标，Agent 带目标**循环执行**（失败自动重试、自主决策、预算保护）直到任务**真正完成**，进度与结果在官方 Cursor 对话流和前端 Goal 面板双端可见。

### 需求（用户选定）

| 需求 | 说明 |
| --- | --- |
| 失败/出错自动重试 | 中间步骤失败不放弃，自动换策略/重试，而不是停下来报错 |
| 前台持续执行 | 发起后同一对话循环执行，进度实时显示，可随时发消息干预或停止 |
| 自主决策少问用户 | 尽量自己判断推进，只在真正卡死时才问 |
| 明确完成判定与汇报 | 有清晰的任务完成判定（自检 + 校验子代理二次确认），完成后汇总汇报 |
| 双端可见 | 官方 Cursor 对话流（SSE 注入）+ cursor-byok 前端 Goal 面板 |
| 可配置预算上限 | max provider passes / 时长 / 费用，超限自动收尾 |

---

## 2. 整体架构

复用现有 provider pass 循环：`handleRunIntent → driveProvider → handleProviderDoneEvent → providerActionResume`。

在 `ActiveStream` 上挂**可选** `Goal *GoalState`：

- `Goal == nil` → 非 goal 会话，全路径旁路，原行为零改动
- `Goal != nil` → 在"无工具调用的 pass 收尾点"接管循环

```
用户 /goal 目标
    │
    ▼
handleRunIntent ── 识别 /goal 前缀、初始化 GoalState、登记 goals map
    │
    ▼
driveProvider ── 编译时把 goal 指令拼进 system prompt（前缀稳定）
    │
    ▼
handleProviderDoneEvent（每 pass 结束）
    ├─ 有工具调用 ──► providerActionResume（现有循环，继续）
    ├─ provider 错误 ──► 注入错误摘要 + resume（有限次 ErrorMaxRetries）
    └─ 无工具调用 ──► handleGoalPassFinished
         ├─ 预算检查（passes/时长/费用）超限 ──► 收口（budget_exceeded）
         ├─ 模型声明 [goal:complete] ──► 校验子代理二次确认
         │     ├─ VERIFIED ──► 收口（completed）+ 完成汇报
         │     └─ NOT_VERIFIED ──► 注入未达成反馈 + resume（RetryCount++，超限 failed）
         └─ 未声明完成（停顿）──► 注入 idle 提醒 + resume（连续多轮升级为换策略提示）
```

---

## 3. 核心机制

### 3.1 完成声明拦截（借鉴 Reasonix `[goal:complete]`）

**不轻信"无工具调用"**：模型必须显式输出 `[goal:complete]` 标记（单独一行开头）才进入完成判定。

- goal 指令要求：完成时输出 `[goal:complete]` + 最终完成报告（做了什么、验证了什么、结果如何）
- 后端在无工具调用 pass 时检查 `accumulatedText` 是否含该标记
- 含标记 → 触发校验子代理；不含标记 → 视为停顿，注入 idle 提醒继续

### 3.2 完成判定：自检 + 校验子代理（证据审计门控）

校验子代理是**只读**任务，复用现有 `delegation.Scheduler` 同步跑（`Submit → WaitForTerminal → Result`）：

```
校验任务：
  SubagentType: generalPurpose
  Readonly:     true
  Prompt:       "检查 GOAL 是否真正达成，输出首行 VERIFIED / NOT_VERIFIED + 理由"
  Contract.DoneCriteria: [goal 文本]
```

- **VERIFIED** → `GoalStatusCompleted`，推送完成汇报
- **NOT_VERIFIED** → 注入未达成反馈（列出理由）+ resume 继续执行，`RetryCount++`
- `RetryCount >= VerifyMaxRetries`（默认 3）→ `GoalStatusFailed`
- 校验子代理不可用时：普通模式退化为"模型自检通过"（保证可收口）；**strict 模式不兜底**，按未通过处理

### 3.3 Idle 停顿检测（借鉴 Reasonix）

无工具调用且未声明完成 → 注入 idle 提醒（"继续推进或说明卡点"），文案随轮数升级：

- 第 1 轮：继续调用工具 / 说明卡点
- 连续 ≥2 轮（`goalStalePivotThreshold`）：**结构性换策略**（改变入口点、任务分解或验证方式），不重复同一做法（借鉴 AutoResearch 的 stale pivot）

### 3.4 失败自动重试

- **工具失败**：goal 指令要求模型分析原因、换方式重试
- **provider 错误**：注入错误摘要 prompt_context + resume 续跑，`ErrorRetries >= ErrorMaxRetries`（默认 3）才 failStream

### 3.5 预算保护

| 预算项 | 字段 | 默认 |
| --- | --- | --- |
| 最大 provider passes | `MaxProviderPasses` | 30 |
| 最大时长 | `MaxDurationSeconds` | 0（不限） |
| 费用上限 | `MaxCostUSD` | 0（不限，需 pricing provider，缺失时跳过检查） |

超限 → `GoalStatusBudgetExceeded`，推送收尾汇报。

### 3.6 进度与结果推送（Cursor 客户端可见）

- **进度**：每 `ProgressInterval`（默认 5）个 pass 推 summary 事件（`SummaryStarted → Summary → SummaryCompleted`，Cursor 左侧摘要区消费）
- **收口**：推 assistant 文本（`buildTextDeltaMessage`）作为完成/失败/预算汇报

### 3.7 状态机

```
running ──┬── 校验通过 ──────────────► completed
          ├── 预算超限 ──────────────► budget_exceeded
          ├── 校验重试超限 ──────────► failed
          ├── 用户手动停止 ──────────► stopped
          └── provider 错误重试超限 ─► failed
```

---

## 4. 触发与使用

### 4.1 Cursor 对话内

```
/goal 跑通全部测试并修复失败用例
/goal --strict 实现支付流程      # strict：校验不通过不允许兜底
#goal 目标                      # 兼容前缀
```

### 4.2 前端 Goal 面板（双入口）

- 新视图 `frontend/src/views/Goal.vue`（路由 `/goal`，Home 顶栏入口）
- 发起表单：目标文本 + 模型选择 + 开始按钮
- Goal 列表：状态、pass 数、工具调用、自检数、费用估算、进度、完成汇报
- 运行中可"停止"
- 后端新 wails bindings：`GetGoals()` / `StartGoal(goalText, modelID)` / `StopGoal(conversationID)`

### 4.3 设置页

`设置 → Goal` 新分类：启用前端面板、max passes、时长、费用上限、自检轮数。

---

## 5. 配置项

`config.yaml` 新增 `goal` 段：

```yaml
goal:
  enabled: false              # 前端面板入口开关；/goal 命令不受限
  max_provider_passes: 30     # 0 = 不限
  max_duration_seconds: 0     # 0 = 不限
  max_cost_usd: 0             # 0 = 不限（需定价来源）
  self_check_passes: 2
  verify_max_retries: 3
  error_max_retries: 3
  progress_interval: 5
```

forwarder 不直接依赖 server/config：运行时配置经 `goalConfigProvider` 接口注入（仿现有 `delegation.RuntimeConfigProvider` 模式）。

---

## 6. Reasonix 借鉴对照

| Reasonix 机制 | cursor-byok 落地 | 位置 |
| --- | --- | --- |
| `[goal:complete]` 完成声明拦截 | `goalCompletionMarker` 常量 + 声明检测 | Task 3/4 |
| todo 拦截 + 列未完成项 | 校验子代理 NOT_VERIFIED 反馈（列出理由） | Task 4/5 |
| `/goal --strict` 不允许覆盖 | `GoalState.Strict` + 校验不可用不兜底 | Task 1/2/5 |
| Idle 检测（连续 2 轮提醒） | `goalIdleReminder`（随轮数升级） | Task 4 |
| stale pivot 结构性换策略 | `StaleCount` + 校验反馈升级提示 | Task 4 |
| 完成声明 + 自检 | 模型声明 + 校验子代理证据审计 | Task 3/4/5 |

**未移植**（记录理由）：todo 状态机（cursor-byok 无独立 todo 工具，用"步骤拆解 + 校验子代理证据审计"替代，中远期可在面板展示步骤）；`parallel_tasks` 并行子任务（cursor-byok 已有 multitask 委派体系）；AutoResearch 持久化状态目录（goal 状态 MVP 仅内存，中远期可加 `.goal/` 状态文件）。

---

## 7. 官方 Cursor 组合方案（不改代码的近似）

交付模板 `docs/goal-workflow/`：

- `.cursor/rules/goal.mdc`：GOAL 循环执行规则（循环执行、失败重试、自主决策、进度汇报、`[goal:complete]` 完成声明、完成报告）
- `commands/goal.md`：`/goal` 斜杠命令模板
- `README.md`：使用说明 + 与 cursor-byok 内置 goal 的能力差异表

限制：无真实预算上限、无校验子代理二次确认、进度仅靠 Agent 自觉汇报。

---

## 8. 取舍与后续

**MVP 取舍**：Ask 工具未强制拦截（靠指令引导自决）；goal 状态仅内存（重启丢失）；费用估算依赖 pricing provider；桌面独立窗口可后续加 binding。

**后续候选**：goal 步骤清单持久化与面板展示（对应 Reasonix todo）；`.goal/` 状态目录（跨重启恢复）；Ask 工具在 goal 模式降级/禁用；并行子任务聚合。
