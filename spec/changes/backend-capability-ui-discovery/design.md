# Design: backend-capability-ui-discovery

## Architecture

```mermaid
flowchart LR
  U[高级设置
显示抓包状态] -->|查询状态或打开目录| F[前端 API 包装层]
  F -->|Wails 只读调用| B[代理桥接服务
组合运行态与文件元数据]
  B -->|读取| P[本地代理运行态]
  B -->|检查| M[镜像记录目录]
  B -->|显式打开| D[系统文件管理器]
  P -->|代理已接入后转发| C[Cursor]
  C -->|官方模型请求| X[镜像交换关联]
  X -->|含 exchangeId/phase/model 的本地 JSONL| M
```

状态查询只暴露运行条件和记录文件元数据。用户主动点击后，系统文件管理器打开镜像目录；记录正文始终停留在本地文件，不经过前端。

路由模式与传输路径必须分开表达：`upstream` 选择业务请求的官方上游，并在未启动本地服务时允许 Cursor 直接访问官方服务；但只要用户启动本地服务且 Cursor 代理设置仍生效，请求仍会先经过本地 MITM。此时镜像域名只旁路记录并直通官方模型 API，Cursor relay 域名则须由显式的路由策略决定是否交给 backend。

## Interfaces

- `ProxyService.GetMirrorCaptureStatus()`
  - Input: 无。
  - Output: `{ enabled: boolean, backendRunning: boolean, proxyRunning: boolean, cursorSettingsApplied: boolean, ready: boolean, recordPath: string, fileExists: boolean, sizeBytes: number, modifiedAtUnixMs: number }`。
  - Error codes: 无；目录或文件不存在时以 `fileExists: false`、`sizeBytes: 0`、`modifiedAtUnixMs: 0` 表示，不创建文件。其他文件系统读取错误返回 Wails 调用错误。
  - Invariants: `ready` 仅在 `enabled && backendRunning && proxyRunning && cursorSettingsApplied` 时为真；绝不返回记录正文、URL、请求头、请求体或响应体。

- `WindowService.OpenMirrorCaptureDirectory()`
  - Input: 无。
  - Output: 无。
  - Error codes: 无；确保目录存在后调用现有跨平台目录打开机制。
  - Invariants: 只打开 `<historyRoot>/_debug/mirror`，不读取、不导出、不复制记录文件。

- 本地镜像记录关联
  - Input: 解密后的 `http.Request` 和其共享的 `goproxy.ProxyCtx`；后续响应与同一 `ProxyCtx`。
  - Output: JSONL 记录新增 `{ exchangeId, phase, model? }`。`phase` 仅允许 `request`、`response_start`、`response_chunk`、`response_truncated`。
  - Invariants: `exchangeId` 只在本进程内生成并写入本地 JSONL，不进入任何官方 HTTP 头、URL 或请求体；同一 `ProxyCtx` 的所有记录使用同一 ID；`model` 从已有请求 JSON 或 Gemini URL 尽力解析，解析失败留空且不影响转发。

- 本地镜像请求 URL 脱敏
  - Input: 原始 `http.Request.URL`。
  - Output: 仅用于 JSONL 的 URL 字符串；保留路径和非敏感查询参数。
  - Invariants: `key`、`api_key`、`apikey`、`token`、`access_token`、`refresh_token`、`secret`、`signature`、`sig`、`password` 与 `pass` 等参数值写为 `[REDACTED]`；实际 `http.Request.URL` 不被修改，Gemini 模型路径提取继续使用原始 URL。

- 路由模式与 MITM 传输状态
  - Input: 已归一化的 `routing.mode`、本地服务运行态和 Cursor 代理设置状态。
  - Output: 页面使用“官方上游”与“本地服务”描述业务去向，并单独显示是否仍经本地 MITM；镜像状态只在本地代理实际运行且已写入 Cursor 设置时宣称可捕获。
  - Invariants: 不以 `routing.mode === upstream` 推断 Cursor 已绕过本地代理；不得因更正界面文案而改变用户已启动服务时的镜像记录能力；未启动服务时不能显示“等待官方模型请求”。

- 隔离协议保真记录
  - Input: 仅 `CURSOR_E2E_MIRROR_CAPTURE=1` 运行的隔离代理中，已脱敏 HTTP 元数据和原始请求/响应 bytes。
  - Output: 镜像 JSONL 新增 `bodyEncoding`、`bodyBytes`、`bodySHA256`、可选 `bodyBase64`，以及仅用于隔离模式的 `protocol` 摘要。二进制内容只使用 Base64，文本内容可保留既有 `body` 字段。
  - Invariants: 每份 bytes 只能由一种载荷字段表示；Base64 解码必须等于原输入；认证头、Cookie、URL 凭据一律不进入载荷或摘要；普通镜像模式不写上述保真字段。

- Bidi 与 Agent 摘要
  - Input: `application/proto` 的 `BidiAppendRequest` 原始 bytes，及其可选 `data_binary`。
  - Output: `{ requestIdHash, appendSeqno, clientMessageKind, agentMode, multitask, subagentAction?, decodeError? }`。
  - Invariants: 外层 `request_id` 只写哈希；不写 prompt、完整 ID、文件路径、凭据或原始 protobuf 的 JSON 展开；解析失败不影响保真 bytes 落盘或官方直通。

- RunSSE 帧与时间线
  - Input: `application/connect+proto` 或 SSE 响应流 bytes。
  - Output: 每个协议帧一条记录 `{ exchangeId, direction, sequence, frameEncoding, frameBytes, frameSHA256, serverMessageKind?, decodeError? }`，并在独立索引中以 `requestIdHash` 聚合。
  - Invariants: 协议帧不是底层读取块；同一 `requestIdHash` 的事件按局部序号递增；不同并发请求不共享序号或索引桶；响应上限命中必须以 `response_truncated` 明示。

## Data Model

- 镜像状态为派生 DTO，不持久化。
- 记录路径固定为应用 history 根目录下的 `_debug/mirror/official.raw.jsonl`。
- 文件元数据只包括是否存在、字节大小和 Unix 毫秒级最后修改时间。
- 每条 JSONL 记录保留现有字段，并新增内部关联字段：`exchangeId`、`phase`、`model`。已有记录没有这些字段时仍可按旧格式读取。
- 隔离保真字段为向后兼容的可选字段；没有 `bodyEncoding` 的旧记录保持原解释，禁止把旧 `body` 重新解释为完整原始二进制。
- 协议时间线与原镜像记录分文件保存，均位于隔离临时根目录下的 `_debug/mirror`，不进入普通 history。

## Key Decisions

- Problem: 用户已启用抓包时，服务运行状态无法说明 Cursor 是否真正经由本地代理，也无法说明是否已有官方请求命中，排障仍依赖手工检查配置和文件。
  Solution: 将运行条件与记录文件元数据汇总成一个只读状态，在开关附近展示当前阻塞点或已命中结果。
  Cost: 增加一个前后端合同和一次轻量文件元数据读取。
  Why not the alternatives: 只保留开关不能定位链路断点；把 JSONL 原文嵌入应用会扩大敏感内容暴露范围。

- Problem: 同一官方请求的响应起始、流式片段与截断事件没有事务归属；并发请求下按时间匹配会把响应正文关联到错误请求，误导后续 Cursor 对接分析。
  Solution: 请求过滤器生成一枚内部交换 ID 并保存在 `ProxyCtx.UserData`，响应过滤器读取同一 ID；记录器为每行写入阶段枚举与可选模型元数据。
  Cost: JSONL 每行增加少量元数据，并需要维护四类事件阶段的一致性。
  Why not the alternatives: 仅按时间戳无法可靠区分并发流；将关联 ID 插入上游请求会改变官方 API 交互，风险不可接受。

## Migration / Compatibility

- 不迁移配置或历史文件；缺少记录文件按“等待官方模型请求”展示。
- 既有开关、代理修复和日志目录入口保持原语义。
- 现有 `upstream` 配置不迁移；只更正其运行态解释，并让代理分流按同一配置源作出明确决策。
