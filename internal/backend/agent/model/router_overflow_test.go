package modeladapter

import "testing"

// TestIsContextOverflowStreamErrorRecognizesAnthropicPhrasing 锁定流式溢出判定口径
// （与 forwarder 侧 isContextLengthExceededError 保持一致，移植自上游 fdae9c41 的
// 溢出恢复思路）：Anthropic 原生措辞必须命中，结构性 prefill 问题不能误判。
func TestIsContextOverflowStreamErrorRecognizesAnthropicPhrasing(t *testing.T) {
	for _, message := range []string{
		"400 {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"prompt is too long: 258615 tokens > 200000 maximum\"}}",
		"model_context_window_exceeded",
		"code=context_too_large",
		"maximum context length is 200000 tokens",
	} {
		if !isContextOverflowStreamError(message) {
			t.Fatalf("isContextOverflowStreamError(%q) = false, want true", message)
		}
	}
	if isContextOverflowStreamError("this model does not support assistant message prefill") {
		t.Fatal("isContextOverflowStreamError(prefill unsupported) = true, want false")
	}
}
