package forwarder

import (
	"errors"
	"testing"
)

// TestIsContextLengthExceededErrorRecognizesAnthropicPhrasing 锁定溢出措辞识别面
// （移植自上游 fdae9c41 的溢出恢复思路）：Anthropic 原生 400 的
// "prompt is too long: N tokens > M maximum" 必须命中，否则 Anthropic 渠道超限时
// 无法触发强制压缩恢复，只能走失败终态。
func TestIsContextLengthExceededErrorRecognizesAnthropicPhrasing(t *testing.T) {
	for _, message := range []string{
		"400: prompt is too long: 258615 tokens > 200000 maximum",
		"error: model_context_window_exceeded",
		"code=context_length_exceeded",
		"maximum context length is 200000 tokens",
		"input exceeds the context window",
	} {
		if !isContextLengthExceededError(errors.New(message)) {
			t.Fatalf("isContextLengthExceededError(%q) = false, want true", message)
		}
	}
	// 结构性问题（assistant prefill 不受支持）不是上下文超限，压缩救不了，不能误判。
	for _, message := range []string{
		"this model does not support assistant message prefill",
		"invalid api key",
		"rate limit exceeded",
	} {
		if isContextLengthExceededError(errors.New(message)) {
			t.Fatalf("isContextLengthExceededError(%q) = true, want false", message)
		}
	}
}
