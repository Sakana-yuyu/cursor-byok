// http_error.go 负责把非 2xx HTTP 响应整理成带响应体摘要的错误。
package modeladapter

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// maxErrorBodyBytes 表示错误响应体最多读取的字节数。
	maxErrorBodyBytes = 8192
	// errorBodyReadTimeout 是读取错误响应体的最大等待时间。
	// 在 502/524 网关风暴中，上游可能先发送 HTTP 头然后半开连接卡住 body，
	// 此时 http.Client.Timeout 和 ResponseHeaderTimeout 都已不再生效
	// （它们只管到头到达为止）。用定时器在超时后关闭 body 解除阻塞。
	errorBodyReadTimeout = 10 * time.Second
)

// HTTPStatusError 表示上游返回的非 2xx 响应，携带真实 HTTP 状态码，
// 便于调用方通过 errors.As 结构化读取状态码，而不必解析错误字符串。
type HTTPStatusError struct {
	// StatusCode 表示上游真实 HTTP 状态码。
	StatusCode int
	// Message 表示已格式化的可读错误信息（与历史行为保持一致）。
	Message string
}

// Error 返回可读错误信息，保持与历史错误字符串完全一致以兼容旧解析逻辑。
func (e *HTTPStatusError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Status 返回上游真实 HTTP 状态码。
func (e *HTTPStatusError) Status() int {
	if e == nil {
		return 0
	}
	return e.StatusCode
}

// buildHTTPStatusError 读取响应体摘要并生成带状态码的错误。
// 读取错误响应体时使用 errorBodyReadTimeout 超时保护，防止半开连接
// 导致 io.ReadAll 永久阻塞（移植自 Reasonix errorBodyReadTimeout 模式）。
func buildHTTPStatusError(prefix string, resp *http.Response) error {
	if resp == nil {
		return fmt.Errorf("%s response is nil", strings.TrimSpace(prefix))
	}

	// 在独立 goroutine 里设置超时关闭 body 的定时器。
	// 如果 ReadAll 在超时内完成，停止定时器；否则定时器关闭 body 解除阻塞。
	bodyClosed := false
	timer := time.AfterFunc(errorBodyReadTimeout, func() {
		bodyClosed = true
		_ = resp.Body.Close()
	})

	limitedBody, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	timer.Stop()

	if bodyClosed {
		// 超时关闭的 body：io.ReadAll 返回的错误通常是 "read on closed body" 之类，
		// 我们用明确的超时错误替换，让日志可诊断。
		if retrySummary := ProviderRetryAttemptSummary(resp); retrySummary != "" {
			return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d %s body_read_timeout=%v", strings.TrimSpace(prefix), resp.StatusCode, retrySummary, err)}
		}
		return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d body_read_timeout=%v", strings.TrimSpace(prefix), resp.StatusCode, err)}
	}
	if err != nil {
		if retrySummary := ProviderRetryAttemptSummary(resp); retrySummary != "" {
			return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d %s body_read_error=%v", strings.TrimSpace(prefix), resp.StatusCode, retrySummary, err)}
		}
		return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d body_read_error=%v", strings.TrimSpace(prefix), resp.StatusCode, err)}
	}
	retrySummary := ProviderRetryAttemptSummary(resp)
	bodyText := strings.TrimSpace(string(limitedBody))
	if bodyText == "" {
		if retrySummary != "" {
			return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d %s", strings.TrimSpace(prefix), resp.StatusCode, retrySummary)}
		}
		return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d", strings.TrimSpace(prefix), resp.StatusCode)}
	}
	if retrySummary != "" {
		return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d %s body=%s", strings.TrimSpace(prefix), resp.StatusCode, retrySummary, bodyText)}
	}
	return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d body=%s", strings.TrimSpace(prefix), resp.StatusCode, bodyText)}
}

// ChannelError 包装底层 provider 错误并携带命中渠道的身份信息，
// 供转发层在错误处理时定位「是哪个渠道/中转站」返回了该错误（如 max_tokens 超限），
// 从而能针对性地持久化该渠道的配置修正，而非全局修改。
// 通过 Unwrap 保留底层错误链，errors.As 仍可提取 *HTTPStatusError。
type ChannelError struct {
	Cause     error
	ChannelID string
	BaseURL   string
	GroupName string
	Provider  string
	Model     string
}

// Error 返回底层错误的字符串形式，保持与原错误一致以兼容既有解析逻辑。
func (e *ChannelError) Error() string {
	if e == nil || e.Cause == nil {
		return "channel error"
	}
	return e.Cause.Error()
}

// Unwrap 暴露底层错误，使 errors.As 能穿透到 *HTTPStatusError。
func (e *ChannelError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
