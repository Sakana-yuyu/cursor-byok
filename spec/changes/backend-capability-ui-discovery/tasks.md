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

- [ ] 9. 子代理结果的安全二层索引
  - [ ] 9.1 在 `internal/mitm/mirror.go` 的 `mirrorProtocol` 和 `mirrorTimelineRecord` 增加可选 `clientResultKind`，仅由 `exec_client_message` 的 `subagent_result`、`force_background_subagent_result`、`subagent_await_result` 填写。
  - [ ] 9.2 新增只读 helper：`SubagentResult.result` 映射 `success/error`；`SubagentAwaitResult.result` 映射 `complete/still_running/not_found/error`；`ForceBackgroundSubagentResult.status` 映射 `accepted/not_found/unspecified`。helper 不读取 agent ID、tool call ID、错误文本、转录路径、最终消息或状态数字。
  - [ ] 9.3 保持 `interaction_query.query` 到 `serverDetailKind` 与 `interaction_response.result` 到 `clientDetailKind` 的现有通用 reflection 路径；不增加重复字段。nil、未知分支或 protobuf 解码失败保持空结果字段或既有错误码，且不影响代理直通。
  - [ ] 9.4 运行 `go test ./internal/mitm ./internal/backend/agent/protocol ./cmd/isolated-cursor-e2e`、`go build ./cmd/isolated-cursor-e2e`、`go vet ./internal/mitm ./internal/backend/agent/protocol ./cmd/isolated-cursor-e2e` 与 `git diff --check`；确认未新增测试文件后提交 `feat(mitm): index multitask result states`。

- [ ] 10. 当前隔离实例的真实矩阵验收
  - [ ] 10.1 保持当前隔离实例运行；用户依次尝试一个可后台化的长子任务、等待该任务结果的后续操作、取消或错误收口，以及一次会要求选择/确认的操作。用户界面未出现某项动作时跳过并记录未触发。
  - [ ] 10.2 只读检查 `protocol.timeline.jsonl`：Multitask 关注 `force_background_subagent_args/result`、`subagent_await_args/result`、`subagent_result` 与相同 requestIdHash 的顺序；交互关注 `interaction_query` 的 `serverDetailKind` 与 `interaction_response` 的 `clientDetailKind`。
  - [ ] 10.3 对每项已触发事件核对 `runsse_connect`、方向、requestIdHash 关联、终态与 `decodeError`；复扫时间线字段名，确认没有正文、Base64、prompt、输出、Cookie、认证头、路径、token 或完整 request ID。
  - [ ] 10.4 将汇总计数、实际 oneof 类型、未触发分支和隐私检查写入 `verify.md` 与本任务清单；不提交临时 JSONL，单独提交 `docs(verify): record multitask interaction coverage`。
