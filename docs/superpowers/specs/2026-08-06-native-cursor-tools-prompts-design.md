# cursor-byok 完全对接 Cursor 原生工具与内置提示词 设计文档

> 日期：2026-08-06 · 状态：B 期已实现（A 期进行中）
> 范围：B 期（接线缺口 5 子项）+ A 期（本地逆向资产 + 云端提示词拉取）
> 约束：不修改已安装的 Cursor 客户端；prompt 渲染改动遵守 prefix-cache-stability（新增内容为空时不输出，保持既有前缀不变）；尽量在现有代码上做加法。

---

## 1. 背景与目标

cursor-byok 已实现"仿 Cursor 原生工具名 + 静态 schema + 执行委托客户端（exec bridge）"的工具体系，系统提示词为自编中文版。目标：**让 cursor-byok 使用到 Cursor 原生工具定义与内置提示词**。

### 逆向调查结论（2026-08-06，已穷尽搜索）

- **主 agent 系统提示词不在本地客户端**：主 bundle（43 个 js / 113MB）+ 全部 26 个扩展均无明文模板；协议证实由服务端 `AiService` RPC 下发（`GetSimplePrompt`/`GetPassthroughPrompt` 等，`GetChatPromptResponse` 消息存在但 RPC 提取不完整）
- **本地可提取资产**：`d:/cursor/resources/app/extensions/cursor-agent-exec/dist/main.js` 与 `cursor-local-agent-runtime` 含完整 **Auto-review 安全分类器提示词**（约 2000 字）+ 4 个本地执行工具定义（ReadFile/ListDir/Grep/Glob）
- **工具描述已对齐**：cursor-byok `tools.json` 29 个工具描述与官方扩展描述高度一致（Grep 等逐字相同）
- **协议字段缺口**：`custom_system_prompt` 为死字段；`custom_subagents`/`non_file_rules` 未消费；AGENTS.md 后端无扫描；磁盘扫描 MCP 无 `input_schema`

## 2. 非目标

- 不抓取/伪造 Cursor 服务端认证；不修改已安装客户端
- 不做提示词版本热更新 UI（A 期完成后由配置开关控制）
- 不新增测试代码（仓库既有约束）

## 3. B 期：接线缺口补齐（5 子项）

### B1. `custom_system_prompt` 接线

- 现状：`agent/core/types.go:189` 定义字段、`prompt/engine.go:139-140` 有消费（追加到 system prompt 尾部），但**无赋值点**
- 改动：
  1. `decodeInboundIntent`/`extractEffectiveRunRequestContext`（`internal/backend/forwarder/request_context.go`）读取 `runRequest.GetCustomSystemPrompt()`
  2. `InboundIntent` 新增字段，`handleRunIntent` 传递到 `Compile` 的 `input.CustomSystemPrompt`
  3. 文本过 `prompt_guard.go` 护栏（防注入），截断上限复用现有 guard 常量
- 前缀稳定性：仅当客户端携带非空自定义提示词时追加，不影响正常路径

### B2. `custom_subagents` 渲染

- 现状：`RequestContext.custom_subagents`（name/description/tools/model/prompt/permission_mode）已随 request_context 存储但 prompt 中不可见
- 改动：`prompt/engine.go` 新增 `<custom_subagents>` 段（仿 Cursor `<subagents>` 段格式：名称 + 描述 + 可用工具），空时不输出；供模型配合 Task 工具选择子代理
- 落点：`internal/backend/agent/prompt/engine.go`、`internal/backend/forwarder/request_context.go`（normalize 保留字段）

### B3. `non_file_rules` 消费

- 现状：`buildRequestContextRulesSection`（engine.go:427）只渲染 `rules` 字段
- 改动：`non_file_rules`（用户全局规则等非文件规则）合并进 `<user_rules>` 块，与 `rules` 按 full_path 去重
- 落点：`internal/backend/agent/prompt/engine.go`

### B4. AGENTS.md 扫描兜底

- 现状：完全依赖客户端附带 rules，后端无扫描
- 改动：新增 `internal/backend/forwarder/agentsmd.go`：
  - workspace root 取 `McpFileSystemOptions.WorkspaceProjectDir`（已有）
  - 扫描 `<root>/AGENTS.md`、`CLAUDE.md`、`GEMINI.md`（仅顶层，`.cursor/rules` 由客户端负责不重复）
  - 策略：**客户端 rules 优先，扫描仅兜底**——`rules_info_complete == false` 或客户端 rules 为空时注入；按文件名去重
  - 注入位置：`<rules>` 段补充块
- 前缀稳定性：正常场景（客户端带 rules）不产生输出

### B5. 磁盘扫描 MCP 补 `input_schema`（已实现）

- **实现结论**：schema 拉取核心已存在（`mcp_registry.go` 的 `Connect` → `connectMCPRuntime` → `listBoundedMCPTools`，含 InputSchema），但只在用户手动"连接"时触发，磁盘扫描发现的 server 仍缺 schema
- 本次改动：
  1. `mcpRuntimeEntry` 新增 `lastConnectAttemptAt`；`Connect` 记录尝试时间
  2. 新增 `TryAutoConnect`（幂等 + 30s 失败冷却，已连接且有 tools 时跳过）
  3. `enrichRequestContextWithScannedAssets` 对磁盘发现的 stdio server 自动预连接（同步、失败静默、不影响请求主流程），连接成功后 `mcpDescriptorsWithRuntime` 注入完整工具 schema
- 落点：`internal/backend/forwarder/mcp_registry.go`、`asset_enrichment.go`

## 4. A 期：原生资产（本地逆向 + 云端拉取）

### A1. 本地逆向资产提取（零成本，立即可做）

- 新增 `scripts/extract-cursor-assets.cjs`：从 `d:/cursor/resources/app/extensions/cursor-agent-exec/dist/main.js` 提取：
  1. Auto-review 安全分类器提示词全文 → `prompt/native/auto_review_classifier.md`
  2. 4 个本地工具定义（ReadFile/ListDir/Grep/Glob）→ 与 `tools.json` 做差异校准
- 产出资产后续用于：shell 审批策略校准（`shell_circuit.go` 对齐官方 auto-run 策略）

### A2. 协议验证（一次性，确认云端调用细节）

- 用现有 MITM 能力记录 Cursor 客户端对 `api2.cursor.sh` 的提示词 RPC（`GetSimplePrompt`/`GetPassthroughPrompt`/`GetChatPrompt`）实际请求：RPC 路径、请求字段、认证头
- 产出：`docs/native-prompts/protocol-notes.md`（请求样例 + 认证格式）

### A3. 后端集成（云端提示词自动拉取）

- 新增 `internal/backend/promptsync/` 模块：
  - 输入：官方账号 token（复用 `internal/cursoraccount`）
  - 调用：connect-rpc → `api2.cursor.sh`（`cursor.aiserver.v1.AiService/<Method>`，路径以 A2 验证结果为准）
  - 输出：`prompt/native/cloud/<mode>.md` 模板缓存 + 元数据（token_count、拉取时间、版本）
  - 触发：配置开关 `promptSync.enabled` + 手动刷新按钮（前端暂缓，先 CLI/配置）
- 渲染引擎支持"原生模板渲染模式"：模板 + 动态段替换（`<rules>`/`<request_context>`/`<tools>`/`<user_message>` 占位符），与 B 期新增段对齐
- 降级：拉取失败/未配置账号时回退自编提示词，行为不变

## 5. 实施顺序与验证

1. **B 期**（B1→B3→B2→B4→B5，风险递增）：每子项 `go build` + `go test ./internal/backend/...` + 针对性回归
2. **A1**：提取脚本产出资产，diff 校准工具描述
3. **A2**：协议验证（需用户配合打开一次 Cursor 对话）
4. **A3**：promptsync 模块（依赖 A2 确认的协议细节）

## 6. 验收标准

- B1：客户端携带自定义提示词时，模型 system prompt 尾部出现其内容（截断/护栏生效）
- B2：客户端带 custom_subagents 时，prompt 出现 `<custom_subagents>` 段且内容正确
- B3：客户端带 non_file_rules 时，`<user_rules>` 块包含其内容且与 rules 去重
- B4：客户端 rules 为空 + workspace 存在 AGENTS.md 时，prompt 出现其内容；客户端带 rules 时无重复
- B5：磁盘扫描的 MCP server 工具在 prompt 中带完整参数 schema
- A1：提取脚本输出资产与官方扩展内容一致
- A3：配置官方账号 + 开启 promptSync 后，`prompt/native/cloud/` 生成模板，渲染引擎切换到原生模板