# 模型来源隔离 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让第三方 API 模型与 Cursor 账户模型具备明确、隔离且可见的来源边界，同时保持已有第三方模型功能兼容。

**Architecture:** 在配置和解析渠道上引入来源与凭据作用域。Router 根据来源把第三方请求交给既有协议适配器，将 Cursor 账户请求交给独立网关；网关未实现时显式拒绝，禁止跨来源失败切换。

**Tech Stack:** Go 1.25、Wails v3、Vue 3、现有 OpenAI/Anthropic/Gemini 模型适配层。

## 全局约束

- 不修改已安装 Cursor 客户端，不读取或输出任何明文凭据。
- 旧模型配置缺失 `source` 时必须等价于 `third_party`。
- `cursor_account` 不得复用 `apiKey`、`baseURL`、自定义请求头或第三方协议适配器。
- 故障切换、健康和缓存身份必须包含模型来源；禁止默认跨来源切换。
- 遵守项目 `IMPROVEMENT_TASKS.md`：不新增测试，改动后运行已有针对性测试、构建与静态检查。

---

### Task 1: 配置与渠道来源身份

**Files:**
- Modify: `internal/backend/server/config/types.go`
- Modify: `internal/backend/server/config/resolver.go`
- Modify: `internal/runtime/local_runtime.go`
- Modify: `frontend/src/utils/modelAdapter.js`

**Interfaces:**
- Produces: `ModelSource`、`CredentialScope` 与携带来源的 `ResolvedChannel`。

- [ ] 规范化 `source` 和 `credentialScope`，旧值回退为第三方来源。
- [ ] 对账户来源验证其不携带第三方连接或密钥字段。
- [ ] 把来源加入渠道身份、解析结果与轮询键。
- [ ] 运行 `gofmt`，再运行现有配置包测试或 `go test ./internal/backend/server/config`。

### Task 2: 来源感知路由与账户网关边界

**Files:**
- Create: `internal/backend/agent/model/account_gateway.go`
- Modify: `internal/backend/agent/model/router.go`
- Modify: `internal/backend/agent/model/types.go`

**Interfaces:**
- Consumes: `ResolvedChannel.Source`、`ResolvedChannel.CredentialScope`。
- Produces: `AccountModelGateway.Stream(context.Context, StreamRequest, func(ModelEvent) error) error`。

- [ ] 增加默认拒绝的账户模型网关，错误信息指向登录状态与协议验证边界。
- [ ] 在 Router 中按来源分派，禁止 Cursor 账户请求进入第三方协议适配器。
- [ ] 为健康状态和事件身份添加来源，确保同源故障切换。
- [ ] 运行 `gofmt`，再运行现有模型适配层测试或 `go test ./internal/backend/agent/model`。

### Task 3: 可见状态与编辑体验

**Files:**
- Modify: `frontend/src/views/ModelEditor.vue`
- Modify: `frontend/src/components/CursorAccountCard.vue`
- Modify: `frontend/src/utils/modelAdapter.js`
- Modify: `frontend/src/i18n/locales/*.json`（仅由扫描构建生成）

**Interfaces:**
- Consumes: `source` 与已有脱敏 `CursorAccountStatus`。
- Produces: 来源选择、账户模型不可执行状态和第三方凭据字段隔离。

- [ ] 在编辑器中增加来源选择，并根据来源隐藏或显示第三方字段。
- [ ] 在账户卡片中明确控制面已可用、账户模型执行通道尚待验证的界限。
- [ ] 运行 `npm run build`，核对各语言目录 key 与占位符一致。

### Task 4: 集成验证与文档收口

**Files:**
- Modify: `docs/superpowers/specs/2026-08-13-model-source-isolation-design.md`（仅在实现与设计不一致时）

- [ ] 运行 `go build ./...`、相关包测试、`npm run build` 与 `git diff --check`。
- [ ] 使用脱敏日志/运行结果确认账户源未进入第三方 adapter。
- [ ] 审查变更边界，确认没有覆盖主工作树的用户修改。
- [ ] 按独立主题提交，提交说明为中文。
