# 上游同步记录（2026-08-31）

## 背景

本次目标原本是查看 Cursor 官方 `/goal` 后如何适配，并检查 `upstream/main` 最近功能是否需要同步到当前 Go/Wails/Vue 主线。用户随后明确要求：去掉 cursor-byok 自身的 Goal 能力，使用 Cursor 原生 `/goal`。

本地环境确认：

- 当前仓库：`E:\MyProject\cursor-byok`
- 当前分支：`main`
- 本机 Cursor：`D:\cursor\Cursor.exe`
- CLI 入口：`D:\cursor\resources\app\bin\cursor.cmd`
- CLI 版本：`3.17.21`

官方 Cursor changelog 在 2026-08-19 发布 `/goal`，含义是给 Agent 一个长期目标并持续推进直到完成。因此本轮最终方案不是继续维护自研 Goal 循环，而是撤销本地 `/goal` 接管，让 Cursor 原生能力负责 Goal 生命周期。

## 本轮同步结论

### Cursor 原生 `/goal`

cursor-byok 当前采用透明交给 Cursor 的策略：

- `/goal` / `/goal --strict` 不再由 forwarder 解析、剥离或改写。
- forwarder 不再创建本地 `GoalState`，不再维护 Goal pass 计数、费用/时长预算、自检、校验子代理或 provider 错误重试。
- checkpoint 投影不再附加本地 Goal 状态。
- 前端不再提供“Goal 命令”本地开关。
- config 不再包含 `goal:` 段，也不再给旧配置补写本地 Goal 默认值。
- 内置技能不再发布 `goal-loop`，旧 Rules/Command 近似模板已删除。

保留项：

- `proto/agent_v1.proto` 与 `gen/agentv1/agent_v1.pb.go` 中的官方 Cursor Goal 类型属于协议面，继续保留。
- 委派监督的 `Contract.Goal` 表达子任务完成标准，和 `/goal` 命令能力无关，继续保留。
- capability map 仍可把官方 `create_goal_tool_call` / `update_goal_tool_call` 识别为协议支持项，但 cursor-byok 不再本地合成这些 ToolCall。

## 上游更新检查

已比较 `main...upstream/main`。上游当前可见主线包含：

- `v0.1.5` 发布提交。
- 模型分组名能力。
- 插件系统与 Grok OAuth 插件。
- provider stream idle timeout 与错误处理。
- 空 tools 字段省略修复。
- Rust/Tauri `server/` 与 `apps/desktop` 架构迁移。
- 文档站、README、工作流与 `.agents` 技能目录调整。

`git diff --stat main..upstream/main` 曾显示约 1910 个文件变化，涉及大规模架构替换；直接 merge 会覆盖当前 Go/Wails/Vue 主线的大量本地能力和未提交工作，因此本轮不做整条上游合并。

## 已覆盖或已确认不需要重复移植

### 空 tools 字段省略

上游提交 `e4515dc4 fix: omit tools field from provider requests when empty` 修改 Rust provider。当前 Go 主线已覆盖同类逻辑：

- OpenAI Chat/Responses 请求体使用 `json:"tools,omitempty"`。
- `normalizeOpenAIRequestToolSchemas` 在过滤后为空时删除 `tools`、`tool_choice`、`parallel_tool_calls`。
- `admitOpenAITools` 与 `admitAnthropicTools` 在无可用工具时删除 `tools`。
- Gemini 仅在 declarations 非空时写入 `tools`。

### provider stream idle timeout

上游提交 `43735075 feat: add provider stream idle timeout and error handling` 修改 Rust provider/router。当前 Go 主线已覆盖同类能力：

- `internal/backend/agent/model/stream_idle.go`
- `newProviderStreamIdleWatchdog`
- `providerStreamChunkTimeout`
- `IsProviderStreamIdleTimeout`
- router 层 idle retry，最多 2 次，并有独立测试覆盖。

### 模型分组名

上游提交 `e7a1cca4 feat: add group name functionality to models` 增加模型分组名。当前 Go/Wails/Vue 主线已覆盖同类能力：

- `frontend/src/views/ModelGroups.vue`
- `frontend/src/utils/supplierGrouping.js`
- `internal/runtime/local_runtime.go`
- `internal/backend/server/config/types.go`
- 用量统计、诊断和前端配置链路均携带 `GroupName`。

## 暂缓同步

以下上游内容本轮未直接同步，需要独立方案与验收：

- Rust/Tauri `server/`、`apps/desktop` 架构迁移：属于主架构切换，不能在当前脏工作树中直接替换。
- 插件系统与 Grok OAuth 插件：依赖上游 Rust 插件运行时，和当前 Go/Wails 插件/供应商模型体系不等价。
- 文档站、wrangler、README 大改：与本轮移除本地 Goal 接管无直接依赖。
- `.agents` 技能目录替换和 `server_backup` 删除：会影响本地协作约定与历史调试资产，需单独确认。

## 验证记录

本文件记录的是执行目标和决策依据。最终测试结果以本轮交付回复为准。

## 回滚思路

本轮修改集中在移除本地 Goal 能力与更新文档。紧急回滚时可按需还原：

- `internal/backend/forwarder/goal.go`
- `internal/backend/forwarder/goal_test.go`
- `internal/backend/forwarder/runtime_types.go`
- `internal/backend/forwarder/skill_goal_activation_test.go`
- `internal/backend/forwarder/service_intent.go`
- `internal/backend/forwarder/service_provider.go`
- `internal/backend/forwarder/service_turn.go`
- `internal/backend/server/config/types.go`
- `internal/backend/server/config/store.go`
- `internal/backend/server/config/manager.go`
- `frontend/src/state/appState.js`
- `frontend/src/components/settings/categories/AdvancedSettings.vue`
- `internal/skills/bundled/goal-loop/SKILL.md`
- `docs/goal-workflow/README.md`

恢复前必须先确认不会与 Cursor 原生 `/goal` 双重接管同一输入。
