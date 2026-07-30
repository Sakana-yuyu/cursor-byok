# Cursor-byok Capability Hardening And Release Design

## Goal

在已完成 Task 1-13 的基础上，修复并发隔离、终态收口、MCP workspace 串扰、Skills workspace 串扰和发布链缺陷；吸收高星 Agent/MCP/Skills 项目的成熟机制，完成 `v0.0.71` 发布，并确保 GitHub Release 包含 macOS ARM64 与 Intel DMG。

## Current Evidence

- Task 1-13 已有独立提交和验收记录，主要能力链已经存在。
- `DelegationScheduler` 当前提供并发槽位、取消、超时、失败隔离和快照，但聚合器通过 50ms ticker 轮询完成状态，事件缺少稳定序号与父子关联字段。
- 主请求失败时 `failActiveStream` 不会取消 Multitask aggregate；聚合结果投递错误被忽略。
- MCP runtime 使用进程级 registry，并仅以 server identifier 作为 key；workspace 扫描会通过全量 `Replace` 删除其他 workspace 的 session。
- `SkillStore` 保存单个共享 `workspaceRoot`，并发请求可能在 prompt 编译时扫描错误项目。
- RunSSE broker 读取错误被转换为正常 EOF，客户端无法区分失败和正常结束。
- Release workflow 生成的 `update.json` 尾部包含字面量 `\\n`，且 DMG 未复制进 `publish/`。

## External Design References

- LangGraph：显式 fan-out/fan-in 和可等待的任务状态，借鉴为 scheduler 原生终态等待，移除轮询。
- Microsoft AutoGen：带 identity、parent、sequence、type 的消息事件，借鉴为委派快照的结构化事件元数据。
- MCP Registry / SDK：registry identity 与 runtime capability 分层，借鉴为 workspace scope、配置指纹和能力状态。
- Anthropic Skills：可验证 skill manifest 和渐进披露，借鉴为显式 workspace 扫描、frontmatter 校验和稳定 content hash。
- OpenAI Codex / VS Code Copilot Chat：执行策略和生命周期分层；本轮只修复现有执行终态，不引入新的运行时依赖。

## Architecture

### Delegation Lifecycle

`Scheduler` 保持执行器无关，只增加结构化快照元数据和 `WaitForTerminal`。每次状态变化分配单调递增 sequence，快照包含 event id、event type、parent request、parent exec 和 model group。Multitask coordinator 使用 scheduler 等待 API 完成 fan-in，不再固定频率轮询。

所有流失败终态统一调用 `CancelStream`。聚合结果无法投递回 actor 时，错误进入 `failStreamIfNonTerminal`，使 pending exec、checkpoint 和 broker 终态沿已有路径收口。

### MCP Workspace Isolation

MCP runtime entry 使用 `scopeKey + identifier` 作为内部身份。scopeKey 由规范化 workspace root 生成；用户级配置使用固定 user scope。公开快照增加 runtime scope、config fingerprint、capability status 和 last checked time。

扫描同步只替换目标 scope。UI 的连接、断开和取消操作携带 workspace root。Local delegated agent 使用任务的 workspace hint 调用该 scope 下的 MCP session。模型可见 server identifier 保持不变，不修改现有 protobuf 字段。

状态映射为 `disconnected`、`connecting`、`connected`、`degraded`、`error`。握手完成且工具 schema 可用时为 connected；调用失败会记录 last error 和检查时间，但单次工具业务错误不自动销毁 session。

### Skills Isolation And Manifest Validation

`SkillStore` 不再保存“当前 workspace”。扫描与激活 API 显式接收 workspace root，编译器从当前 conversation 持久化的 request context 中解析 workspace。用户设置仍由 store 统一快照。

Skill manifest 至少要求非空 `name` 和 `description`；name 经过长度和控制字符检查。扫描结果计算稳定 content hash，并保留 source、path 和可选 version。无效 manifest 被跳过并形成可查询诊断，不进入 prompt。Prompt 仍只注入名称、描述和路径，保持现有稀疏 Top-K 与 prefix cache 规则。

### SSE Error Semantics

RunSSE 读取 broker 失败时返回明确 Connect error，并保留 debug 证据。正常 terminal backlog 仍按原路径结束，不改变重连宽限策略。

### Release Pipeline

Release 资产清单收敛到 `scripts/release`：workflow 负责重命名原始构建产物，Go release tool 负责生成合法 `update.json`。manifest 保持 updater 使用 tar.gz/zip；DMG 作为额外人工安装资产发布。

发布前在 CI 中比较 tag 与 `build/config.yml`。生成后解析验证 JSON，并检查所有预期资产非空。最终 Release 至少包含：Windows amd64/386 ZIP 与 installer、Linux amd64 tar.gz、macOS arm64/amd64 tar.gz、macOS arm64/amd64 DMG、`update.json`。

## Error Handling

- 取消和超时只影响目标委派任务；父流失败会取消其所有 aggregate，不影响其他流。
- workspace scope 不存在时 MCP 返回带 workspace 和 identifier 的明确错误。
- Skill manifest 错误只隔离目标 skill，不阻断其他技能或主请求。
- CI 缺资产、版本不一致或 manifest 非法时立即失败，不创建不完整 Release。

## Compatibility

- 不修改已安装 Cursor bundle。
- 不引入 LangGraph、AutoGen、CrewAI 等运行时依赖。
- 不修改已有 protobuf 字段编号；本轮优先使用内部 Go/JSON 桌面接口扩展。
- 保留现有模型适配器、API Key、配置和浮窗持久化行为。
- 仓库禁止新增测试文件；使用现有测试、构建、静态检查、协议回放和人工验收。

## Acceptance

- 父流失败、取消或被新请求替代后，没有残留委派任务或 pending aggregate。
- Multitask fan-in 不使用 ticker 轮询，快照具有稳定事件序号与父子关联。
- 两个 workspace 可同时保留同名或不同名 MCP server，扫描任一 workspace 不会断开另一 workspace。
- Local delegated agent 只调用其 workspace scope 下的 MCP。
- 并发 prompt 编译不会读取其他 workspace 的 Skills；无效 manifest 不进入 prompt。
- RunSSE broker 读取失败对客户端可见为错误。
- `go test -count=1 ./...`、`go vet ./...`、`go build ./...`、`npm --prefix frontend run build` 全部成功。
- `v0.0.71` GitHub Actions 成功，Release 的 `update.json` 可解析，两个版本化 `.dmg` 均存在且非空。
