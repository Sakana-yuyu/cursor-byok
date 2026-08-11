# 官方请求镜像记录与对比 Design

## Problem

Cursor 客户端在配置官方 OpenAI/Anthropic key（直连模式）时，会直接构造 JSON 请求发给模型 API。cursor-byok 在 BYOK 模式下由 forwarder 自行构造出站请求（`provider.jsonl` 的 `llm_request`），两者在 messages 结构、tools 描述、thinking 字段、cache_control 等关键维度上存在差异，导致推理输出、工具调用/子代理、长上下文/缓存等行为与官方不一致。

当前 MITM 只劫持 `*.cursor.sh` 白名单域名（`internal/mitm/service.go` `isWhitelistedRelayHost`），模型 API 域名（`api.openai.com`、`api.anthropic.com`、`generativelanguage.googleapis.com` 等）直接透传不解密，无法看到官方客户端构造的请求明文，因此无法做请求形态对比。

目标：先做一次性分析修复（抓官方直连请求 → 对比我们构造的请求 → 修转换逻辑），再沉淀为内置对比工具。

## Design

### ① 抓取层：MITM 镜像记录

在现有 MITM 上新增**镜像记录域名列表**，与现有 relay 白名单（`*.cursor.sh`）互不干扰：

- 默认列表：`api.openai.com`、`api.anthropic.com`、`generativelanguage.googleapis.com`；支持用户追加第三方网关域名。
- `HandleConnect`（service.go:406）：镜像域名走 `ConnectMitm` 解密（复用现有 CA 与 `mitmCertStore`）。
- `DoFunc`（service.go:414）：镜像域名请求体复制一份写入记录器后 `return req, nil` 直通官方——**不转发 backend，不影响官方 key 正常使用**。
- 新增 `OnResponse` 钩子：响应体（SSE 流）逐 chunk tee 一份到记录器，流式直通客户端。
- 非镜像域名行为完全不变；`*.cursor.sh` 仍走现有 relay 转发。

### ② 记录层

复用 `debug_recorder` 体系，新增 `history/_debug/mirror/official.raw.jsonl`，每行一个事件：

> 官方直连请求（OpenAI/Anthropic JSON）不含 conversationId，故镜像记录独立组织（不按会话分目录）；每条记录携带 ts/host/exchangeId/phase，并尽力携带 model。`exchangeId` 把请求、响应起始、流式片段和截断事件稳定归为同一次 HTTP 交换，供后续与 `provider.jsonl` 进行可靠对比。

- request：method、url、脱敏后的 headers、request body
- response：status、脱敏后的 headers、逐 chunk 响应片段

大 body 截断 + 记录限流，防止写爆磁盘。`Authorization`、`x-api-key` 等敏感头一律脱敏（与现有脱敏逻辑一致）。

### ③ 开关与配置

- `config.yaml` 新增 `mirrorCapture: false`（默认关）+ 镜像域名列表；`log: true` 时才落盘（与现有 debug 记录开关一致）。
- 第一阶段可先走配置项；前端设置页开关在沉淀阶段（②交付）实现。

### ④ 一次性分析修复（第一交付）

抓取后对比两端：

| 端点 | 数据来源 |
| --- | --- |
| 官方直连请求 | `official.raw.jsonl`（新） |
| 我们转出的请求 | `provider.jsonl` 的 `llm_request`（现有） |

对比重点四类：**messages 结构**（system/roles/content 类型）、**tools 描述**（name/description/input_schema）、**thinking/reasoning 字段**、**cache_control/缓存断点**。

差异定位后以官方请求形态为基准修复转换逻辑：`internal/backend/agent/model/anthropic_request.go`、`openai_request.go`、`anthropic_stream.go`、`openai.go` 等。

### ⑤ 沉淀工具（第二交付）

前端"请求对比"页：选择会话 → 官方请求 vs 我们构造的请求并列展示 → 差异高亮。或先出 CLI 对比脚本（从两个 jsonl 提取并 diff）。

## Error Handling

- 镜像记录失败（写入/截断错误）只记日志，**不得阻断请求直通**——镜像记录是旁路，官方链路必须始终可用。
- 响应 tee 失败同样只记日志，客户端流不受影响。
- 大 body 截断在记录端做，不修改原始请求/响应。
- 配置解析失败时回退默认列表，并保持 `mirrorCapture` 为 false。

## Testing

- MITM 单测：镜像域名"解密+记录+直通"，非镜像域名零行为变化，`*.cursor.sh` relay 回归。
- 记录器单测：脱敏生效、截断生效、SSE 流 chunk 记录完整。
- 全量：`go test ./...`、`go vet ./...`、`go build ./...`、`git diff --check`。
- 真实 Cursor 手工 e2e：官方 key 直连模式下发请求，验证 `official.raw.jsonl` 记录完整且使用不受影响。
