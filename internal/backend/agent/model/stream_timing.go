// stream_timing.go 记录 provider 流式请求的关键耗时到 app.log，
// 用于定位首字（TTFB）与整体流式延迟，便于优化「首字速度」。
package modeladapter

import (
	"time"

	"cursor/internal/logger"
)

// logProviderStreamTiming 在每次 provider 流式请求结束时记录一条 INFO 日志：
//   - ttfb_ms: 请求发出 → 首个 SSE 事件回来的耗时（首字延迟）；未收到首字则记为 -1。
//   - total_ms: 整体流式耗时（含失败）。
//   - finish_reason / token 计数 / 失败原因。
//
// 该日志只读已计算的 startedAt/firstEventAt/finishedAt，不改变任何请求/响应行为，
// 因此对首字速度本身无影响（仅是结构化输出）。
func logProviderStreamTiming(
	provider string,
	model string,
	req StreamRequest,
	startedAt time.Time,
	firstEventAt time.Time,
	finishedAt time.Time,
	finishReason string,
	inputTokens int64,
	outputTokens int64,
	cacheReadTokens int64,
	cacheWriteTokens int64,
	streamErr error,
) {
	requestID := req.RequestID
	modelCallID := req.ModelCallID

	ttfbMS := int64(-1)
	if !firstEventAt.IsZero() && !startedAt.IsZero() {
		ttfbMS = firstEventAt.Sub(startedAt).Milliseconds()
	}
	totalMS := int64(-1)
	if !startedAt.IsZero() && !finishedAt.IsZero() {
		totalMS = finishedAt.Sub(startedAt).Milliseconds()
	}

	errText := ""
	if streamErr != nil {
		errText = streamErr.Error()
	}

	logger.Info("provider stream timing",
		"provider", provider,
		"model", model,
		"request_id", requestID,
		"model_call_id", modelCallID,
		"ttfb_ms", ttfbMS,
		"total_ms", totalMS,
		"finish_reason", finishReason,
		"input_tokens", inputTokens,
		"output_tokens", outputTokens,
		"cache_read_tokens", cacheReadTokens,
		"cache_write_tokens", cacheWriteTokens,
		"error", errText,
	)
}
