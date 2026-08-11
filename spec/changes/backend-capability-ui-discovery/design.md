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

## Data Model

- 镜像状态为派生 DTO，不持久化。
- 记录路径固定为应用 history 根目录下的 `_debug/mirror/official.raw.jsonl`。
- 文件元数据只包括是否存在、字节大小和 Unix 毫秒级最后修改时间。
- 每条 JSONL 记录保留现有字段，并新增内部关联字段：`exchangeId`、`phase`、`model`。已有记录没有这些字段时仍可按旧格式读取。

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
