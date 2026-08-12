# Multitask 子代理引用对齐实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 让 Cursor Multitask 回复中的蓝色子代理引用通过真实的 Task 工具调用身份定位到对应子代理卡片，并保持并行任务、状态、阶段和结果不串线。

**Architecture:** 复用现有 tool_call_id -> parent_tool_call_id -> subagent_runs_by_parent_tool_call_id 协议链路。父级 Task 调用使用稳定的 agent_id，checkpoint 使用同值的 SubagentRunState.subagent_id，状态更新继续由现有 checkpoint 发布路径完成。测试覆盖并行任务、完成顺序变化、失败/取消和缺失状态，不根据标题或数组顺序猜测归属。

**Tech Stack:** Go 1.25、Protocol Buffers、现有 forwarder checkpoint、Go table-driven tests、隔离 Cursor Multitask E2E。

## Global Constraints

- 所有身份关联必须基于真实 tool_call_id 与 parent_tool_call_id。
- 不使用标题、任务顺序、完成顺序或 requestIdHash 推断子代理归属。
- 不记录 prompt、完整响应、Cookie、Authorization、token 或其他凭据。
- 保持普通代理、非 Multitask 和既有登录配置行为不变。
- 每个阶段必须先验证后单独提交，临时抓包 JSONL 不得提交。

---

### Task 1: 身份关联测试

**Files:**
- Create: internal/backend/forwarder/subagent_reference_test.go
- Modify: internal/backend/forwarder 仅在测试需要暴露现有行为时修改

**Interfaces:**
- Consumes: 现有 delegationSubagentID(toolCallID string) string、attachDelegationRunStates 和 Task tool-call 构造路径。
- Produces: 可证明两个并行 tool_call_id 各自映射到唯一 parent_tool_call_id/subagent_id 的回归测试。

- [x] Step 1: Write the failing test：构造两个不同的 Task 工具调用和对应运行状态，断言生成的 subagent_id 与父工具调用逐一一致；故意让标题相同，证明不能按标题匹配。
- [x] Step 2: Run test to verify it fails：先以原生完成态回归测试复现 agent_id 为空，再运行 forwarder 定向测试。
- [x] Step 3: Write minimal implementation：修正本地聚合完成态与原生 Task 完成态，使 agent_id、parent_tool_call_id 和 subagent_id 使用同一个稳定映射。
- [x] Step 4: Run test to verify it passes：定向及完整 forwarder/exec bridge 测试均通过。
- [x] Step 5: Commit：已拆分为 `da63c77`、`d8b7a68` 和 `c21cae7` 三笔独立提交。

### Task 2: 协议身份修正

**Files:**
- Modify: internal/backend/forwarder/service.go
- Modify: internal/backend/forwarder/service_runtime_state.go
- Test: internal/backend/forwarder/subagent_reference_test.go

- [x] 验证 Task agent_id 与 checkpoint SubagentRunState.subagent_id 使用相同值。
- [~] 覆盖 running、backgrounded、success、error、aborted 状态：running/success/error/aborted 已有代码路径和本地状态测试；backgrounded 仍需真实 Cursor oneof 验证。
- [x] 运行 forwarder 定向测试、go vet 和格式检查。
- [x] 单独提交原生身份修复 `d8b7a68`。

### Task 3: 蓝色引用链路验证

**Files:**
- Modify: internal/backend/forwarder/subagent_reference_test.go
- Modify: spec/changes/backend-capability-ui-discovery/verify.md
- Modify: spec/changes/backend-capability-ui-discovery/tasks.md

- [x] 验证两个并行任务标题相同、完成顺序相反时仍按 parent_tool_call_id 定位。
- [x] 验证缺失任务状态不会回退到其他子代理。
- [x] 单独提交 `c21cae7 test(forwarder): verify parallel subagent reference routing`。

### Task 4: 隔离 Cursor 实际验证

**Files:**
- Modify: spec/changes/backend-capability-ui-discovery/research.md
- Modify: spec/changes/backend-capability-ui-discovery/verify.md
- Modify: spec/changes/backend-capability-ui-discovery/tasks.md

- [x] 已有用户控制的隔离 Cursor Multitask 证据覆盖 4 个并行审查子任务的界面形态、流式阶段和成功回传；该证据未包含逐卡片完整身份映射。
- [~] 只读检查上下行协议和 checkpoint 结构：本地协议链路已通过代码/测试确认；真实时间线仍不能安全地把匿名抓包事件逐一映射到截图标题。
- [ ] 用户点击蓝色引用，验证滚动、高亮和状态详情；当前仓库没有 Cursor 聊天渲染入口，必须在隔离 Cursor 客户端中人工验证。
- [x] 研究与验证台账已更新；真实客户端点击 E2E 仍保持未验证。
