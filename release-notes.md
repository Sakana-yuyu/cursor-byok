# v0.0.67 发布说明

## 🔧 Skills / MCP 调用可靠性修复

### 核心问题修复
- **修复 Skills 极少自主调用**：原 SkillStore 仅扫描 `~/.cursor-local-assistant-v2/skills/` 目录，而技能实际分布在 Cursor / Claude Code / Codex / ZCode / .agents 等多处，导致注入为空、模型无从得知可用技能。现已实现多源扫描合并。
- **修复 MCP 调用失败**：生产编译器从不读取 `RequestContext.McpFileSystemOptions`，模型只看到空泛的 `CallMcpTool` 壳子、不知道有哪些 server 可调。现已注入 server 清单到原生 `<mcp_file_system>` 提示。

### 跨工具自动扫描（还原原生用法）
- **多源 Skills 扫描**：覆盖 Cursor / Claude Code / Codex / ZCode / 共享 `.agents` / ZCode 插件 / 旧 BYOK 共 7 类来源，按名称去重，经 BM25 Top-K 稀疏激活注入（不全量注入，保护 prefix-cache）
- **多格式 MCP 配置扫描**：支持 JSON（Cursor/Claude/Cline/共享/ZCode 嵌套）+ 轻量 TOML（Codex）解析，归一化跨工具字段差异（`environment`→`env`、`http_headers`→`headers` 等）
- **原生注入链路复用**：扫描结果合成 descriptor 后在 turn 1 合并进 `RequestContext`，经现有 `request_context` → `projector` → `engine.go` 链路渲染为原生 `<agent_skills>` / `<mcp_file_system>` user message，不在系统提示另开第二条注入路径

### MCP 调用链捕获点（便于后续针对性修复）
- **Schema 缺失捕获**：记录哪些注入的 MCP server 缺少 tool schema（磁盘配置不含 `input_schema`），模型仅知 server 名
- **执行结果捕获**：记录 `CallMcpTool` 的终态结果（success/error/tool_not_found/server_not_found/rejected/permission_denied），失败模式额外打 app.log
- 两类捕获均走 debugRecorder 结构化 JSONL（`log: true` 开启，按 conversationId 查询）+ 无条件 app.log 一行

### 管理界面
- **设置面板新增「技能 & MCP（跨工具扫描）」卡片**：按工具分类展示已发现技能与 MCP server，支持逐项开关、重新扫描
- 新增 `SkillMCPScanConfig` 配置项（总开关 + 按分类/逐项禁用），默认开启

### 代码清理
- 删除死代码 `buildSkillDiscoveryMessage` 及其独有的 `escapeSkillPromptXML`（零调用者，造成格式分叉）

## 📝 注意事项
- MCP tool schema 自动获取（需连 server）及 MCP 自托管执行（脱离 Cursor 客户端）为后续议题，本次未包含
- `frontend/bindings/` 由 wails 工具链自动生成，正式 `wails build` 会从 Go 源码重新生成

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。
