# Protocol-fidelity capture tasks

- [x] 1. 保真记录数据模型与隔离开关
  - [x] 1.1 为记录器增加仅隔离模式启用的 body 编码、长度、摘要和 Base64 表示。
  - [x] 1.2 保持普通镜像记录字段和直通语义不变。
  - [x] 1.3 运行既有 MITM 定向测试、构建和静态检查。

- [x] 2. Bidi protobuf 摘要与关联
  - [x] 2.1 解码外层 `BidiAppendRequest`，并复用现有 Agent 消息解码器生成最小安全摘要。
  - [x] 2.2 为摘要和保真记录增加 request ID 哈希、append 序号与显式解析失败标记。
  - [x] 2.3 核验未知或畸形输入不阻断官方直通。

- [x] 3. RunSSE 协议帧解析
  - [x] 3.1 在响应 tee 中按 Connect/SSE 帧边界缓冲并保真记录，而非按底层读取分块记录。
  - [x] 3.2 尽力解析 Agent 服务端消息类型并保留无法解析的帧。
  - [x] 3.3 核验截断、断流和多个帧共用读取缓冲的边界。

- [x] 4. Multitask 时间线索引
  - [x] 4.1 以 `requestIdHash`、`exchangeId`、方向与局部序号写入隔离协议索引。
  - [x] 4.2 为 Multitask、子代理状态与终态事件生成安全摘要。
  - [x] 4.3 用用户控制的隔离 Multitask 请求做无正文结构化验收。

- [x] 5. 台账与独立提交
  - [x] 5.1 更新验证台账，区分构建、字节保真、协议解析和真实 Cursor E2E。
  - [x] 5.2 每个已验证节点单独提交，不混入用户未跟踪文件。

## 上下行完整结构化解析实施计划

> 目标：仅在 `CURSOR_E2E_MIRROR_CAPTURE=1` 的隔离镜像模式中，保留上下行原始字节，并为 Bidi/RunSSE 建立不含正文与凭据的完整已知协议结构索引。
> 约束：不修改已安装 Cursor、真实用户配置或官方转发字节；不新增测试文件；每个节点单独提交，且不暂存 `.playwright-cli/`、`frontend/.playwright-cli/`、`output/`。

- [x] 6. 下行 Connect 探测与服务端结构索引
  - [x] 6.1 在 `internal/mitm/mirror.go` 为 `text/event-stream` 增加“待判定”状态：收集 5 字节后只接受合法 Connect flags、受 `mirrorConnectFrameMaxBytes` 限制的长度与完整帧；判定成功后回放缓冲并按 Connect 重组，判定失败后才按现有 SSE 空行边界处理。
  - [x] 6.2 为 `mirrorProtocolFrame` 和 `mirrorTimelineRecord` 增加 Connect 压缩标识、服务端 Exec 二层 oneof、子代理事件与流式内容摘要字段；用 protobuf reflection 提取 `AgentServerMessage` 和 `ExecServerMessage` 的实际 oneof，不写入任意消息正文。
  - [x] 6.3 使 Connect 终态帧、未知压缩、畸形长度、protobuf 解码失败与真实 SSE 都产生稳定错误/状态摘要，并继续只观察 tee 的副本。
  - [x] 6.4 已运行 `go test ./internal/mitm ./internal/backend/agent/protocol ./cmd/isolated-cursor-e2e`、`go build ./cmd/isolated-cursor-e2e`、`go vet ./internal/mitm ./internal/backend/agent/protocol ./cmd/isolated-cursor-e2e` 与 `git diff --check`；未新增测试文件。

- [x] 7. 上行二层索引与安全时间线字段
  - [x] 7.1 为 `mirrorProtocol` 和 `mirrorTimelineRecord` 增加 Bidi payload 来源、字节长度、SHA-256、客户端二层 oneof 与标准化子代理动作字段；沿用 `DecodeBidiAppendAgentClientMessage`，仅记录结构名称与摘要。
  - [x] 7.2 对 `run_request`、`exec_client_message`、`exec_client_control_message`、`kv_client_message`、conversation action、interaction response 与 heartbeat 提取已知一层/二层 oneof；缺失、冲突、截断、压缩或解析失败写稳定错误码，不中断请求直通。
  - [x] 7.3 已复核 `protocol.timeline.jsonl` 不新增 `bodyBase64`、`frameBase64`、prompt、模型输出、token、Cookie、认证头、路径或完整 request ID；新增字段均为兼容的可选 JSON 字段。
  - [x] 7.4 已运行 `go test ./internal/mitm ./internal/backend/agent/protocol ./cmd/isolated-cursor-e2e`、`go build ./cmd/isolated-cursor-e2e`、`go vet ./internal/mitm ./internal/backend/agent/protocol ./cmd/isolated-cursor-e2e` 与 `git diff --check`；未新增测试文件。

- [x] 8. 新隔离实例真实 E2E 验收
  - [x] 8.1 保留正在运行的全部 Cursor 实例；已启动新的 `CURSOR_E2E_MIRROR_CAPTURE=1` 隔离实例，根目录为 `C:\Users\Administrator\AppData\Local\Temp\cursor-byok-e2e-2309467229`。
  - [x] 8.2 用户已在新实例中发送普通请求与 Multitask 请求；只读取新实例的 `official.raw.jsonl` 和 `protocol.timeline.jsonl` 元数据及结构字段。
  - [x] 8.3 已验收 724 条 `runsse_connect`、Bidi 上行结构、gzip/identity Connect、服务端顶层/Exec 内层 oneof、流式增量摘要与 `subagent_args -> create`；724 个原始帧的 Base64、长度和 SHA-256 全部一致，时间线未发现正文、凭据、路径或完整 request ID 字段。`force_background_subagent_args` 与 `subagent_await_args` 本次未触发，保留为未验证分支。
  - [x] 8.4 真实 E2E 数据仅保留在临时目录且未提交；最终报告分别列出代码检查、真实抓包证据和未触发分支。

## Multitask 结果与交互覆盖实施计划

> 目标：补齐子代理结果状态的安全二层索引，并以现有隔离实例真实验证 Multitask 生命周期与用户交互协议。
> 约束：不新增测试文件、不读取或落盘正文/参数/路径/完整 ID/凭据；不关闭或重启任何既有 Cursor 实例；仅暂存本计划明确列出的文件。

- [x] 9. 子代理结果的安全二层索引
  - [x] 9.1 在 `internal/mitm/mirror.go` 的 `mirrorProtocol` 和 `mirrorTimelineRecord` 增加可选 `clientResultKind`，仅由 `exec_client_message` 的 `subagent_result`、`force_background_subagent_result`、`subagent_await_result` 填写。
  - [x] 9.2 新增只读 helper：`SubagentResult.result` 映射 `success/error`；`SubagentAwaitResult.result` 映射 `complete/still_running/not_found/error`；`ForceBackgroundSubagentResult.status` 映射 `accepted/not_found/unspecified`。helper 不读取 agent ID、tool call ID、错误文本、转录路径、最终消息或状态数字。
  - [x] 9.3 保持 `interaction_query.query` 到 `serverDetailKind` 与 `interaction_response.result` 到 `clientDetailKind` 的现有通用 reflection 路径；不增加重复字段。nil、未知分支或 protobuf 解码失败保持空结果字段或既有错误码，且不影响代理直通。
  - [x] 9.4 已运行 `go test ./internal/mitm ./internal/backend/agent/protocol ./cmd/isolated-cursor-e2e`、`go build ./cmd/isolated-cursor-e2e`、`go vet ./internal/mitm ./internal/backend/agent/protocol ./cmd/isolated-cursor-e2e` 与 `git diff --check`；未新增测试文件。

- [~] 10. 当前隔离实例的真实矩阵验收
  - [~] 10.1 保持当前隔离实例运行；已真实验证单子代理取消、错误结果、父级 `Stop All`、Shell 审批和 MCP 审批/结果。后续真实操作继续捕获 3 次子代理创建、2 次成功回传、1 次错误回传、MCP/Shell 调用、流式状态与终态；后台化、等待、ComputerUse 和新的审批预检专属分支仍未触发：`force_background_subagent_*`、`subagent_await_*`、`computer_use_*`、`*_allowlist_precheck_*` 保持未验证。
  - [x] 10.2 只读检查 `protocol.timeline.jsonl`：已确认 `subagent_args/result`、`clientResultKind=success`、`interaction_query/response`、`step_completed`、`turn_ended` 和 `stream_close`；交互闭环按相同 `requestIdHash` 关联。
  - [x] 10.3 对已触发事件核对 `runsse_connect`、`bidi_append` 方向、requestIdHash 关联、终态与 `decodeError`；最近约 11,498 条时间线记录的正文、原始帧、凭据、路径、token 和完整 request ID 字段扫描均为 0。
  - [x] 10.4 已将汇总计数、实际 oneof 类型、取消/停止、审批和 IDE 内 Playwright MCP 证据、未触发分支和隐私检查写入 `verify.md` 与本任务清单；临时 JSONL 未提交，文档改动单独提交。
  - [x] 10.5 已针对 `cursor-ide-browser` 解析保真帧中的非内容 MCP 字段，记录 `browser_click`、`browser_cdp`、`browser_lock`、`browser_navigate`、`browser_snapshot`、`browser_tabs` 的实际调用次数；临时聚合器已删除，未读取或保存参数、页面内容、URL、坐标或结果正文。
- [x] 10.6 已记录 Cursor 对 IDE 浏览器流程的自述，并与已捕获 MCP 工具矩阵交叉核验；将可验证的工具类型和调用通道与未验证的 `viewId/ref`、DOM/CDP 实现细节、截图/解锁、标签可见性和认证流程严格分开。

## 安装版 Cursor 兼容适配实施

- [x] 11. 浏览器 profile 与生命周期状态投影
  - [x] 11.1 仅根据已连接 MCP 的 tools/list 描述符区分 `cursor_ide_browser` 与坐标型浏览器 profile；无有效 profile 或多个坐标型 profile 返回明确错误，不回退到桌面鼠标。
  - [x] 11.2 IDE 浏览器适配器在动作前列标签并锁定、点击前快照和截图、结束后解锁；不能稳定映射的拖拽、按下和抬起动作明确失败。
  - [x] 11.3 为后台化、等待与 allowlist precheck oneof 增加不含标识符、参数或正文的生命周期状态投影；保持既有 payload、watchdog 和终态语义。
  - [x] 11.4 已运行 `go test ./internal/computeruse ./internal/backend/forwarder -count=1` 与 `go test ./internal/backend/agent/bridge/exec ./internal/backend/forwarder -count=1`；真实 Cursor 尚未在本轮发出后台化、等待或 ComputerUse 专属 oneof，保持未验证。

- [x] 12. Shell 工具气泡流式增量兼容
  - [x] 12.1 仅对非空 stdout/stderr 将既有 Shell 输出投影为 `ToolCallDelta.ShellToolCallDelta`，保留原 `ShellOutputDelta` 发布与全部终态逻辑。
  - [x] 12.2 以 stdout、stderr、启动、退出与空输出的定向单元测试验证结构和忽略边界。
  - [x] 12.3 已运行 `go test ./internal/backend/forwarder -run 'TestBuildShellToolCallDeltaMessage' -count=1`、`go test ./internal/backend/agent/bridge/exec ./internal/backend/forwarder -count=1`、`go vet ./internal/backend/forwarder` 与 `git diff --check`。

## Cursor 协议历史对齐实施

> 目标：将隔离镜像已经生成的无正文 `protocol.timeline.jsonl` 以只读、安全、可用的方式接入历史页，供后续 Cursor 功能对接核对上下行、流式、子代理和终态结构。
> 约束：不读取或展示 `official.raw.jsonl`，不改变普通镜像模式、不新增自动抓包、不修改已安装 Cursor、真实登录态或原始协议转发；本地会话历史的删除和清理语义保持不变。

- [x] 13. 安全协议会话读取合同
  - [x] 13.1 以 `TestScanCursorProtocolSessionsIn` 与缺文件用例锁定固定时间线路径、按 `requestIdHash` 聚合、稳定排序、畸形行跳过和缺文件空结果。
  - [x] 13.2 增加 `ProxyService.GetCursorProtocolSessions()`，仅返回设计列出的安全字段；读取或扫描错误显式返回，绝不读取 `official.raw.jsonl`。
  - [x] 13.3 已生成 Wails bindings 并补齐浏览器预览 mock；已运行 bridge/MITM 测试、vet 与全仓 Go 构建。

- [x] 14. 历史页协议视图
  - [x] 14.1 以浏览器 E2E 锁定来源切换、协议摘要、事件展开与原始抓包字段不可见；缺文件由后端空数组和页面未采集状态表示。
  - [x] 14.2 在现有历史页增加只读“Cursor 协议”来源；“本地会话”保留现有图标/详细信息、选择、删除和清理行为，协议页仅允许刷新与展开安全事件。
  - [x] 14.3 已运行 i18n 扫描构建并补齐英日俄翻译；四个 locale 的键集、空值和占位符校验均通过，前端 lint、单元测试、构建和历史页 E2E 均通过。

- [ ] 15. 交付与发布准备
  - [x] 15.1 已对协议读取、MITM 与前端记录验证证据；真实 Cursor 已覆盖的上下行、流式、MCP、Shell、子代理与终态范围沿用 `verify.md`，后台化、等待与 ComputerUse 等未触发分支继续明确标记为未验证。
  - [ ] 15.2 单独提交协议历史功能，随后核对 detached 工作树、`main`、`fork/main` 与 `upstream/main` 的共同祖先，只并入可验证且兼容的改动。
  - [ ] 15.3 将版本升级为下一个 patch，按既有工作流构建并验证 Windows 安装包，推送 tag 并核验 GitHub Actions/Release 资产。
