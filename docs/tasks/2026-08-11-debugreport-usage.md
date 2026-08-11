# Debug JSONL 链路报告

## 用途

`debugreport` 用于把一次 Cursor 请求的 `provider.jsonl`、`runtime.jsonl` 和 `runsse.jsonl` 汇总为脱敏报告，定位 TTFT、推理强度和流式正文链路问题。它只读 history，不修改会话历史、配置或已安装客户端。

## 运行

在仓库根目录执行：

```powershell
go run ./scripts/debugreport `
  -conversation <conversationId> `
  -request <requestId>
```

也可以通过 `-history-root` 指定只读的 history 根目录。报告只输出请求 ID、model call ID、推理强度、token、TTFT、总耗时，以及正文增量的数量、字节数和 SHA-256 摘要。不会输出正文、工具参数、完整 provider body、URL 或认证字段。

## 关键字段

- `effort.runtime`：客户端原始请求的推理强度。
- `effort.maximum`：渠道配置的推理强度上限。
- `effort.effective`：路由器最终采用的强度。
- `effort.provider`：最终 provider body 中的协议字段。
- `usage.ttftMs`：provider 首个有效事件到达前的首响应延迟口径。
- `usage.durationMs`：provider 流总耗时。
- `forwarderReceived`：adapter 模型事件进入 forwarder 时生成的正文摘要；它是 forwarder 接收点的独立证据，不等同于 provider 原始 SSE 抓取。
- `runSSE`：forwarder 实际发送给 RunSSE 订阅者的正文摘要。新日志只含增量字节数与 SHA-256，不在用户可见输出热路径构造完整 protobuf JSON；旧日志中的完整消息格式仍可读取。
- `textComparison`：
  - `match`：`forwarderReceived` 与 `runSSE` 的正文增量数量及摘要一致。
  - `mismatch`：两层都有证据但摘要不一致，需要继续追查 forwarder 接收与下发之间的链路。
  - `unavailable`：任一层缺少摘要事件；旧日志没有新增摘要事件时，不能据此判断正文是否丢失。

## 真实 E2E 验收

同一渠道、模型和短请求分别执行 `disabled`、`medium`、`max`，每次取得新的 `conversationId`/`requestId` 后运行报告。渠道上限为 `medium` 时，预期 `max` 的四个推理字段均能解释为：运行时 `max`、上限 `medium`、实际 `medium`、provider `medium`。

正文链路必须以新请求的 `textComparison=match` 为准，它证明 forwarder 接收与 RunSSE 下发一致，不能替代 provider 原始 SSE 的独立比对。旧请求若显示 `unavailable`，只能说明日志版本较旧或链路缺少摘要，不能推断发生了丢字或重复输出。

## 性能边界

正文摘要只在配置 `log: true` 时计算并异步落盘；关闭 debug 日志时不会计算 SHA-256，也不会为该诊断分配字段 map。正常 RunSSE 下行不再为日志执行完整 protobuf JSON 编码和反解码，发送失败时仍会保留完整错误诊断。debug 日志是尽力而为的证据层，队列满时可以丢弃事件，不能把日志缺失解释为业务流丢失。

## NewAPI TPS 口径对照

2026-08-11 以关键词 `NewAPI TPS generation_ms output_tokens` 检索并读取 [Calcium-Ion/new-api](https://github.com/Calcium-Ion/new-api) 最新 `9c97e78aced572d540f227007a675d7d007666ac` 的 `pkg/perf_metrics/metrics.go`。该实现把 `generation_ms` 记为首响应到完成的时长，并计算 `output_tokens / generation_ms`。选用其官方 GitHub 仓库而非第三方说明，原因是统计公式以源码为准。

因此对照时必须区分渠道返回的总 `output_tokens` 和用户可见正文 token：推理模型的总 TPS 可以高于正文 TPS。`debugreport` 的链路摘要只用于证明 forwarder 接收与 RunSSE 下发一致，不能替代同一 prompt、模型、推理强度下的端到端测速。
