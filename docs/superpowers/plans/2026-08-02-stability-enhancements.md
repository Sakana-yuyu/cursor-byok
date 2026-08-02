# cursor-byok 稳定性增强（A1-A4 + B1-B3）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 cursor-byok 落地 7 条稳定性优化——错误重试（A1 Anthropic/Gemini 流式重连、A2 SSE 逐块超时、A3 OpenAI 404 容错、A4 retry-after-ms）与工具反馈（B1 doom loop 检测、B2 参数错误修复指令、B3 未知工具文案），全部为增量改动、不破坏现有功能。

**Architecture:** 错误重试域改动集中在 `internal/backend/agent/model/`（三适配器 + retry/router），工具反馈域集中在 `internal/backend/forwarder/`（service/tool_catalog）与 `internal/backend/agent/core/`（tool_args）。核心：把 openai.go 的 pre-output 重连骨架抽成通用 helper 供 anthropic/gemini 复用（A1），在统一工具入口 `handleToolInvocation` 与统一结果出口 `appendToolResult` 处做 doom loop 检测（B1），在 `DecodeArgsMap` 一处包装参数错误类型（B2）。

**Tech Stack:** Go（无新增依赖；`http.ResponseController.SetReadDeadline` 为标准库）。

## Global Constraints

- **不写任何测试**（仓库约束，`go test` 仅用于跑现有测试，必须保持全绿）
- **参数全部代码内硬编码**：`maxStreamReconnects=2`（已有）、`chunkTimeout=30s`（新增）、`doomLoopThreshold=3`、`doomLoopHardLimit=5`、404 多渠道各试 1 次；本轮不做界面
- **不改已安装 Cursor 客户端**
- **openai.go 既有重连 + prompt_cache_key 适配逻辑不动**（避免回归）；A1 只新增 helper 供 anthropic/gemini 使用
- **prefix-cache-stability**：B2 的 tool_result 文案变更属一次性前缀失效，文案结构保持稳定（前缀「<Tool> error: 」不变，仅追加引导句）
- 每批（Task）独立验证：`go build ./...`、`go vet ./...`、`go test ./...`（现有测试绿）+ 手动回归；每 Task 独立 commit

---

### Task 1: B3 未知工具兜底文案增强

**Files:**
- Modify: `internal/backend/forwarder/service.go:285-316`（Service struct）、`:383`（NewService）、`:2327-2328`（unsupported 分支）
- Modify: `internal/backend/forwarder/tool_catalog.go`（无需改逻辑，仅确认 `DefaultToolCatalog.Load` 签名）

**Interfaces:**
- Consumes: `DefaultToolCatalog.Load(mode agentv1.AgentMode, subagentTypeName string) ([]json.RawMessage, []string, error)`（tool_catalog.go:22，已存在）
- Produces: Service 新增字段 `toolCatalog *DefaultToolCatalog`；unsupported 错误文案含可用工具列表

- [ ] **Step 1: Service struct 增加 toolCatalog 字段并在 NewService 注入**

`internal/backend/forwarder/service.go:285` struct 内（compiler 字段附近）加：

```go
	toolCatalog              *DefaultToolCatalog
```

`NewService`（service.go:338）顶部创建实例，随后同时传给 `NewPromptCompiler`（:383）与 service 字段：

```go
	toolCatalog := NewToolCatalog()
```

（NewPromptCompiler 调用处把 `NewToolCatalog()` 替换为 `toolCatalog`；service 字面量构造处补 `toolCatalog: toolCatalog`。）

- [ ] **Step 2: unsupported 分支文案增强**

`service.go:2327-2328` 改为：

```go
	if !isExecInvocation && !isInteractionInvocation && !isLocalStateInvocation && !isImmediateNativeInvocation {
		available := ""
		if service.toolCatalog != nil {
			if _, names, loadErr := service.toolCatalog.Load(mode, subagentTypeName); loadErr == nil && len(names) > 0 {
				available = fmt.Sprintf("（可用工具：%s）", strings.Join(names, ", "))
			}
		}
		return service.completePreDispatchToolError(stream, invocation, nil, false, false, fmt.Errorf("unsupported tool invocation: %s%s", invocation.ToolName, available))
	}
```

- [ ] **Step 3: 编译验证**

Run: `go build ./...`
Expected: 编译通过，无未使用符号错误。

- [ ] **Step 4: 手动验证**

验证方法：临时在日志或联调中触发未知工具调用（模型调用一个 catalog 外工具名），确认 tool_result 文案形如 `unsupported tool invocation: Foo（可用工具：Read, Write, ...）`，且列表与 `isToolAllowedInMode` 白名单一致。验证后恢复真实调用路径。

- [ ] **Step 5: Commit**

```bash
git add internal/backend/forwarder/service.go
git commit -m "feat(forwarder): 未知工具兜底文案包含可用工具列表 (B3)"
```

---

### Task 2: B2 参数校验失败注入修复指令

**Files:**
- Modify: `internal/backend/agent/core/tool_args.go`（新增错误类型 + DecodeArgsMap 包装）
- Modify: `internal/backend/forwarder/tool_error_completion.go:101-111`（formatPreDispatchToolError 按错误类别追加引导句）

**Interfaces:**
- Produces: `type InvalidToolArgumentsError struct{ Err error }`（含 `Error()`/`Unwrap()`，`errors.As` 可识别）；`DecodeArgsMap` 的 JSON 解析错误全部包装为该类型
- Consumes: `runtimecore.InvalidToolArgumentsError`（tool_error_completion.go 通过 `errors.As` 检查）

- [ ] **Step 1: tool_args.go 新增错误类型并包装 DecodeArgsMap 错误**

`internal/backend/agent/core/tool_args.go` 文件头（import 已含 errors 则复用，否则加 `"errors"`）：

```go
// InvalidToolArgumentsError 标记模型产生的工具参数无法解析（JSON 语法错误、
// 多顶层值等），用于错误反馈层对模型注入"请修正参数"的引导。
type InvalidToolArgumentsError struct{ Err error }

func (err *InvalidToolArgumentsError) Error() string {
	if err == nil || err.Err == nil {
		return "invalid tool arguments"
	}
	return err.Err.Error()
}

func (err *InvalidToolArgumentsError) Unwrap() error { return err.Err }
```

`DecodeArgsMap`（:16-37）内三处错误返回改为包装（保留原始错误链）：

```go
	if err := decoder.Decode(&result); err != nil {
		return nil, &InvalidToolArgumentsError{Err: fmt.Errorf("invalid tool arguments JSON: %w", err)}
	}
	...
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, &InvalidToolArgumentsError{Err: errors.New("invalid JSON arguments: multiple top-level values")}
		}
		return nil, &InvalidToolArgumentsError{Err: err}
	}
```

- [ ] **Step 2: formatPreDispatchToolError 按类别追加引导句**

`internal/backend/forwarder/tool_error_completion.go:101-111` 改为：

```go
func formatPreDispatchToolError(invocation runtimecore.ToolInvocation, cause error) string {
	toolName := strings.TrimSpace(invocation.ToolName)
	if toolName == "" {
		toolName = "Tool"
	}
	message := "unknown error"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message = strings.TrimSpace(cause.Error())
	}
	var invalid *runtimecore.InvalidToolArgumentsError
	if errors.As(cause, &invalid) {
		return fmt.Sprintf("%s error: %s。请修正参数后重试（参考该工具的参数结构说明）", toolName, message)
	}
	return fmt.Sprintf("%s error: %s", toolName, message)
}
```

（`runtimecore` 已导入于本文件 :11，无需新增 import。）

- [ ] **Step 3: 编译验证**

Run: `go build ./...`
Expected: 编译通过。

- [ ] **Step 4: 手动验证**

验证方法：构造带非法 JSON 参数的模型工具调用（如 args 为 `{bad json`），确认 tool_result 文案含「请修正参数后重试」；再触发业务错误（如文件不存在），确认**不含**引导句（保持 `<Tool> error: 原文`）。

- [ ] **Step 5: Commit**

```bash
git add internal/backend/agent/core/tool_args.go internal/backend/forwarder/tool_error_completion.go
git commit -m "feat(forwarder): 参数解析失败注入修复引导 (B2)"
```

---

### Task 3: B1 主循环 doom loop 检测

**Files:**
- Modify: `internal/backend/forwarder/types.go`（ActiveStream 增加 doom loop 状态字段）
- Modify: `internal/backend/forwarder/service.go`（handleToolInvocation 检测 + appendToolResult 注入提示）

**Interfaces:**
- Consumes: `delegation.NormalizeToolSignature(toolName string, argsJSON []byte) string`（internal/backend/delegation/loop_detector.go:96，已存在）；`Service.appendToolResult`（service.go:2680，统一结果出口）
- Produces: ActiveStream 新增 `doomLoopCounts map[string]int`、`lastDoomLoopSignature string`、`pendingDoomLoopNotice string`（`stream.mu` 保护）；常量 `doomLoopThreshold=3`、`doomLoopHardLimit=5`

- [ ] **Step 1: ActiveStream 增加状态字段与常量**

`internal/backend/forwarder/types.go` ActiveStream struct（`stream.mu sync.Mutex` 所在处）加：

```go
	doomLoopCounts         map[string]int
	lastDoomLoopSignature  string
	pendingDoomLoopNotice  string
```

forwarder 包常量区（如 service.go 顶部或 types.go 常量块）加：

```go
const (
	// doomLoopThreshold 连续相同工具调用达到该次数时，向模型注入"请改变策略"提示。
	doomLoopThreshold = 3
	// doomLoopHardLimit 连续相同工具调用达到该次数时，中断本轮（返回可恢复错误）。
	doomLoopHardLimit = 5
)
```

- [ ] **Step 2: handleToolInvocation 内检测与计数**

`service.go:2253` handleToolInvocation 开头（`stream.mu.Lock()` 块内，:2260-2269 附近）增加：

```go
	signature := delegation.NormalizeToolSignature(trimmedToolName, invocation.ArgsJSON)
	doomLoopCount := 0
	stream.mu.Lock()
	if stream.lastDoomLoopSignature != signature {
		stream.doomLoopCounts = map[string]int{}
		stream.lastDoomLoopSignature = signature
	}
	if stream.doomLoopCounts == nil {
		stream.doomLoopCounts = map[string]int{}
	}
	stream.doomLoopCounts[signature]++
	doomLoopCount = stream.doomLoopCounts[signature]
	stream.mu.Unlock()
	if doomLoopCount >= doomLoopHardLimit {
		return service.completePreDispatchToolError(stream, invocation, nil, false, false,
			fmt.Errorf("检测到 %s 以相同参数连续调用 %d 次，已中断本轮：请先阅读之前的工具结果并改变策略", trimmedToolName, doomLoopCount))
	}
	if doomLoopCount == doomLoopThreshold {
		stream.mu.Lock()
		stream.pendingDoomLoopNotice = fmt.Sprintf("[检测到 %s 以相同参数连续调用 %d 次，请先阅读上次工具结果并改变策略]", trimmedToolName, doomLoopCount)
		stream.mu.Unlock()
	}
```

（`delegation` 包已 import，service.go:30 `"cursor/internal/backend/delegation"`；`NormalizeToolSignature` 为其导出函数。）

- [ ] **Step 3: appendToolResult 注入提示并清零**

`service.go:2680` appendToolResult 函数体开头（写 history 之前）：

```go
	stream.mu.Lock()
	notice := stream.pendingDoomLoopNotice
	stream.pendingDoomLoopNotice = ""
	stream.mu.Unlock()
	if notice != "" && strings.TrimSpace(resultText) != "" {
		resultText = strings.TrimSpace(resultText) + "\n" + notice
	} else if notice != "" {
		resultText = notice
	}
```

- [ ] **Step 4: 编译验证**

Run: `go build ./...`
Expected: 编译通过。

- [ ] **Step 5: 手动验证**

验证方法：模拟模型连续 3 次以完全相同参数调用同一工具 → 第 3 次 tool_result 尾部出现 `[检测到 ... 请先阅读上次工具结果并改变策略]`；连续 5 次 → 第 5 次直接返回中断错误（不执行工具）；中间插入不同工具/参数 → 计数清零；正常对话流无任何提示。

- [ ] **Step 6: Commit**

```bash
git add internal/backend/forwarder/types.go internal/backend/forwarder/service.go
git commit -m "feat(forwarder): 主循环 doom loop 检测与提示注入 (B1)"
```

---

### Task 4: A4 retry-after-ms 支持

**Files:**
- Modify: `internal/backend/agent/model/retry.go:149-169`（parseRetryAfter）

**Interfaces:**
- Produces: `parseRetryAfter` 优先解析 `retry-after-ms`（毫秒），再回退 `Retry-After`（秒/HTTP 日期），封顶 `providerRetryMaxRetryAfter`

- [ ] **Step 1: parseRetryAfter 增加 retry-after-ms 分支**

`internal/backend/agent/model/retry.go:149` 函数开头（`raw := ...` 之前）插入：

```go
	if ms := strings.TrimSpace(resp.Header.Get("retry-after-ms")); ms != "" {
		if milliseconds, err := strconv.Atoi(ms); err == nil && milliseconds > 0 {
			return time.Duration(milliseconds) * time.Millisecond
		}
	}
```

（`strconv`、`strings`、`time` 均已 import；`providerRetryMaxRetryAfter` 封顶由调用方 `providerRetryBackoff` 处理，本函数不重复封顶——与现有 Retry-After 行为一致。）

- [ ] **Step 2: 编译验证**

Run: `go build ./...`
Expected: 编译通过。

- [ ] **Step 3: 手动验证**

验证方法：临时以含 `retry-after-ms: 1500` 的 429 响应触发重试（联调或本地小脚本直接调 `parseRetryAfter`），确认返回 1500ms；无该头时原 `Retry-After` 行为不变。

- [ ] **Step 4: Commit**

```bash
git add internal/backend/agent/model/retry.go
git commit -m "feat(model): 支持 retry-after-ms 头解析 (A4)"
```

---

### Task 5: A3 OpenAI 兼容端点 404 多渠道容错

**Files:**
- Modify: `internal/backend/agent/model/router.go:78-136`（Stream 循环）、`:422-453`（isPermanentProviderError 附近新增判断）

**Interfaces:**
- Produces: `isOpenAINotFoundError(err error) bool`；Stream 循环对 openai 协议组 404 不提前返回、全部渠道 404 后返回可读错误

- [ ] **Step 1: 新增 404 判定函数**

`router.go` isPermanentProviderError 附近新增：

```go
// isOpenAINotFoundError 判断是否为 404（模型名/路径未就绪）。仅对 OpenAI 兼容
// 端点放宽为可继续尝试其他渠道；Anthropic 协议 404 仍视为永久。
func isOpenAINotFoundError(err error) bool {
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr == nil || statusErr.StatusCode != http.StatusNotFound {
		return false
	}
	return true
}
```

（`errors`、`http` 已在 router.go import。）

- [ ] **Step 2: Stream 循环放宽 404 提前返回**

`router.go:104-115` 处：

```go
		if attempt > 0 {
			// 游标回到已尝试过的渠道，说明没有其它可用端点。
			// OpenAI 兼容端点的 404 视为可换渠道（模型名/路径未就绪），不提前返回。
			if _, seen := tried[channelID]; seen && lastErrPermanent && !isOpenAINotFoundError(streamErr) {
				return firstErr
			}
			...
```

- [ ] **Step 3: 全部渠道 404 后返回可读错误**

`router.go:132-135` 处：

```go
	if firstErr != nil {
		if isOpenAINotFoundError(firstErr) {
			return fmt.Errorf("model %q not found at provider (404)：请检查模型名或中转站是否支持该模型（%w）", req.ModelID, firstErr)
		}
		return firstErr
	}
```

- [ ] **Step 4: 编译验证**

Run: `go build ./...`
Expected: 编译通过。

- [ ] **Step 5: 手动验证**

验证方法：配置多渠道（首渠道模型名错误返回 404），确认会切换第二渠道；单渠道 404 → 快速返回含模型名与 404 的可读错误；claude-on-openai 降级路径（shouldFallbackToOpenAI）行为不变（降级优先于 404 换渠道）。

- [ ] **Step 6: Commit**

```bash
git add internal/backend/agent/model/router.go
git commit -m "feat(model): OpenAI 兼容端点 404 多渠道容错与可读错误 (A3)"
```

---

### Task 6: A1 Anthropic/Gemini pre-output 流式重连

**Files:**
- Create: `internal/backend/agent/model/stream_reconnect.go`（通用 helper）
- Modify: `internal/backend/agent/model/anthropic.go:230-555`（Stream 改走 helper）
- Modify: `internal/backend/agent/model/gemini.go:30-106`（Stream 改走 helper）
- 注意：**openai.go 不动**

**Interfaces:**
- Produces: `streamWithReconnect(ctx context.Context, sink func(ModelEvent) error, stream func(int, func(ModelEvent) error) error) error`（重连语义与 openai.go:558 一致：emitted 标记 + `IsStreamConnectionReset` + `maxStreamReconnects` + 指数退避）
- Consumes: `IsStreamConnectionReset`（retry.go:213）、`maxStreamReconnects`（retry.go:207）、`providerRetryBaseDelay`/`providerRetryMaxDelay`（retry.go）、`sleepWithContext`（retry.go:172）

- [ ] **Step 1: 新建 stream_reconnect.go**

```go
package model

import (
	"context"
	"fmt"
)

// streamWithReconnect 提供通用的 pre-output 流式透明重连：
// 已向 sink 转发任何事件后绝不重连（避免重复输出）；仅连接重置类错误可重连，
// 最多 maxStreamReconnects 次，退避复用 providerRetryBaseDelay 指数递增。
// OpenAI 适配器保留其专属的 prompt_cache_key 适配版本（openai.go），本 helper
// 供 Anthropic/Gemini 使用。
func streamWithReconnect(ctx context.Context, sink func(ModelEvent) error, stream func(int, func(ModelEvent) error) error) error {
	var connectionAttempt int
	for {
		emitted := false
		wrappedSink := func(event ModelEvent) error {
			emitted = true
			return sink(event)
		}
		err := stream(0, wrappedSink)
		if err == nil {
			return nil
		}
		if emitted {
			if IsStreamConnectionReset(err) {
				return fmt.Errorf("upstream stream interrupted mid-response (already forwarded partial content, will not reconnect to avoid duplicates): %w", err)
			}
			return err
		}
		if !IsStreamConnectionReset(err) {
			return err
		}
		if ctx.Err() != nil {
			return err
		}
		connectionAttempt++
		if connectionAttempt > maxStreamReconnects {
			return fmt.Errorf("stream reconnect exhausted after %d attempts: %w", maxStreamReconnects, err)
		}
		backoff := providerRetryBaseDelay << (connectionAttempt - 1)
		if backoff > providerRetryMaxDelay {
			backoff = providerRetryMaxDelay
		}
		if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
			return sleepErr
		}
	}
}
```

- [ ] **Step 2: gemini.go Stream 接入 helper**

`gemini.go:65-106`：把「watchdog 创建 → 请求 → 状态码检查 → streamGeminiEvents」整段（:70-105）抽为闭包，外层包 `streamWithReconnect`。注意 watchdog 创建移入闭包内（每轮重连新建）：

```go
	err := streamWithReconnect(ctx, sink, func(_ int, wrappedSink func(ModelEvent) error) error {
		streamCtx, streamIdle := newProviderStreamIdleWatchdog(ctx, req.ProviderStreamIdleTimeout)
		defer streamIdle.Stop()
		resp, reqErr := doProviderRequestWithGzipFallback(streamCtx, adapter.client, "gemini", req.RequestID, req.ModelCallID, payload, requestURL, func(httpReq *http.Request) error {
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("User-Agent", ClaudeCodeUserAgent)
			httpReq.Header.Set("x-goog-api-key", apiKey)
			if err := ApplyCustomHeaders(httpReq, req.CustomHeadersEnabled, req.CustomHeadersJSON); err != nil {
				return err
			}
			return nil
		})
		if reqErr != nil {
			if idleErr := streamIdle.Err(); idleErr != nil {
				reqErr = idleErr
			}
			finishedAt = time.Now().UTC()
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "gemini", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, reqErr))
			return reqErr
		}
		streamIdle.AttachBody(resp.Body)
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			reqErr = buildHTTPStatusError("gemini adapter", resp)
			finishedAt = time.Now().UTC()
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "gemini", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, reqErr))
			return reqErr
		}
		inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, parseErr := adapter.streamGeminiEvents(resp, req, modelID, startedAt, streamIdle, wrappedSink)
		finishedAt = time.Now().UTC()
		if parseErr != nil {
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "gemini", modelID, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, 0, parseErr))
			return parseErr
		}
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "gemini", modelID, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, 0, nil))
		return nil
	})
	if err != nil {
		return err
	}
	return nil
```

- [ ] **Step 3: anthropic.go Stream 接入 helper**

`anthropic.go` 的 SSE 解析**内联在 `Stream`（:230，唯一方法）内**（scanner 循环自 :553 起，无独立解析函数）。接入方式：把「watchdog 创建（:334）→ 请求发送（:337-354）→ 状态码检查（:358-363）→ 内联 SSE 解析（:365 至 Stream 函数末尾）」整段抽为闭包，外层包 `streamWithReconnect`。要点：

- watchdog 创建移入闭包内（每轮重连新建）：`streamCtx, streamIdle := newProviderStreamIdleWatchdog(ctx, req.ProviderStreamIdleTimeout)`，`defer streamIdle.Stop()`
- 闭包签名 `func(_ int, wrappedSink func(ModelEvent) error) error`；解析循环中所有向 `sink` 的调用改为 `wrappedSink`
- anthropic 专属头保留：`ApplyAnthropicCompatibleAuthHeaders`、`anthropic-version: 2023-06-01`、`AnthropicClaudeCodeUserAgent`
- 闭包外保留 Stream 的签名与校验段（baseURL/apiKey/modelID 检查、body 构造 :247-332、`payload` marshal），`startedAt`/`finishedAt` 由闭包捕获更新

- [ ] **Step 4: 编译验证**

Run: `go build ./...`
Expected: 编译通过；`go vet ./...` 无新告警。

- [ ] **Step 5: 手动验证**

验证方法：模拟 anthropic/gemini 上游在输出前断开连接（如联调时 kill 连接/断网重连），确认透明重连 ≤2 次且最终成功；已输出内容后断流 → 不重连、报 mid-response 错误；openai 路径行为不变（回归 claude-on-openai 升降级）。

- [ ] **Step 6: Commit**

```bash
git add internal/backend/agent/model/stream_reconnect.go internal/backend/agent/model/anthropic.go internal/backend/agent/model/gemini.go
git commit -m "feat(model): Anthropic/Gemini 补齐 pre-output 流式重连 (A1)"
```

---

### Task 7: A2 SSE 逐块超时

**Files:**
- Modify: `internal/backend/agent/model/stream_idle.go`（新增 `chunkTimeout` 常量）
- Modify: `internal/backend/agent/model/anthropic.go:700`（scanner 循环）
- Modify: `internal/backend/agent/model/gemini.go:308`（scanner 循环）
- Modify: `internal/backend/agent/model/openai.go:1011、:1836`（chat/responses 两处 scanner 循环）

**Interfaces:**
- Produces: `chunkTimeout = 30 * time.Second`；`setStreamReadDeadline(resp *http.Response) (func(), bool)` 小工具（循环内每次 Scan 前调用；返回的 reset 函数在 Scan 成功后调用，避免 deadline 累积）——或直接在循环内调用 `http.NewResponseController(resp).SetReadDeadline`，失败忽略
- Consumes: `http.NewResponseController`（标准库）

- [ ] **Step 1: 新增常量与 deadline 工具函数**

`stream_idle.go` 常量区加：

```go
	// chunkTimeout 是 SSE 单块（单次读）之间的最大间隔；超过即视为流卡死。
	chunkTimeout = 30 * time.Second
```

`stream_idle.go` 末尾（或同包新函数）加：

```go
// resetStreamReadDeadline 在每次 SSE 块读取前设置读超时，块到达后清除。
// 底层连接不支持时静默忽略（fallback，不改变原有行为）。
func resetStreamReadDeadline(resp *http.Response) (reset func(), ok bool) {
	if resp == nil || resp.Body == nil {
		return func() {}, false
	}
	controller := http.NewResponseController(resp)
	if err := controller.SetReadDeadline(time.Now().Add(chunkTimeout)); err != nil {
		return func() {}, false
	}
	return func() {
		_ = controller.SetReadDeadline(time.Time{})
	}, true
}
```

（需确认 stream_idle.go 已 import `net/http`、`time`；若无则补。）

- [ ] **Step 2: 三个适配器 scanner 循环接入**

每个 `for scanner.Scan()` 循环体开头加：

```go
		if reset, ok := resetStreamReadDeadline(resp); ok {
			reset()
		}
```

`for` 循环**外、scanner 创建后**加一次预置：

```go
		scanner := bufio.NewScanner(resp.Body)
		if reset, ok := resetStreamReadDeadline(resp); ok {
			reset()
		}
```

落点：`anthropic.go:700`（循环）、`gemini.go:308`、`openai.go:1011、:1836`。各解析函数签名均持有 `resp *http.Response`（anthropic 的解析函数、`streamGeminiEvents(resp, ...)`、openai 两处）。

- [ ] **Step 3: 编译验证**

Run: `go build ./...`
Expected: 编译通过。

- [ ] **Step 4: 手动验证**

验证方法：模拟上游每块间隔 >30s（联调时挂起流），确认在 ~30s 报流超时错误（走批次 4 重连或 router failover）；正常流不受影响；`resetStreamReadDeadline` 不支持的环境（非 net.Conn）静默 fallback。

- [ ] **Step 5: Commit**

```bash
git add internal/backend/agent/model/stream_idle.go internal/backend/agent/model/anthropic.go internal/backend/agent/model/gemini.go internal/backend/agent/model/openai.go
git commit -m "feat(model): SSE 逐块超时防护 (A2)"
```

---

## 验证清单汇总（每 Task 均执行）

1. `go build ./...` — 全量编译通过
2. `go vet ./...` — 无新告警
3. `go test ./...` — 现有测试全绿（不新增测试）
4. 手动回归（Task 完成后各跑一次）：正常对话流、claude-on-openai 升降级、max_tokens 恢复、上下文溢出压缩、shell/turn 恢复

## 执行顺序说明

Task 1-2（文案，零风险）→ Task 3（doom loop，纯增量）→ Task 4-5（retry/router 小改）→ Task 6（流式重连，核心）→ Task 7（逐块超时）。每 Task 独立 commit，任意 Task 出问题可单独回退。
