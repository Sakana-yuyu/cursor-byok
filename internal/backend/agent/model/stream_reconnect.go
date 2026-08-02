package modeladapter

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
