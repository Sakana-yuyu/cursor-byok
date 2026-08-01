package modeladapter

import (
	"encoding/json"
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
	// Body 保存去除首尾空白后的原始响应体；Message 可能已经提取了 JSON 中的 message。
	Body string
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
			return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d %s body_read_timeout=%v", strings.TrimSpace(prefix), resp.StatusCode, retrySummary, err), Body: strings.TrimSpace(string(limitedBody))}
		}
		return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d body_read_timeout=%v", strings.TrimSpace(prefix), resp.StatusCode, err), Body: strings.TrimSpace(string(limitedBody))}
	}
	if err != nil {
		if retrySummary := ProviderRetryAttemptSummary(resp); retrySummary != "" {
			return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d %s body_read_error=%v", strings.TrimSpace(prefix), resp.StatusCode, retrySummary, err), Body: strings.TrimSpace(string(limitedBody))}
		}
		return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d body_read_error=%v", strings.TrimSpace(prefix), resp.StatusCode, err), Body: strings.TrimSpace(string(limitedBody))}
	}
	retrySummary := ProviderRetryAttemptSummary(resp)
	rawBodyText := strings.TrimSpace(string(limitedBody))
	bodyText := rawBodyText

	// 尝试提取并美化 JSON 错误体中的关键信息
	friendlyError := extractFriendlyErrorMessage(bodyText)
	if friendlyError != "" {
		bodyText = friendlyError
	}

	if bodyText == "" {
		if retrySummary != "" {
			return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d %s", strings.TrimSpace(prefix), resp.StatusCode, retrySummary), Body: rawBodyText}
		}
		return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d", strings.TrimSpace(prefix), resp.StatusCode), Body: rawBodyText}
	}
	if retrySummary != "" {
		return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d %s body=%s", strings.TrimSpace(prefix), resp.StatusCode, retrySummary, bodyText), Body: rawBodyText}
	}
	return &HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s status=%d body=%s", strings.TrimSpace(prefix), resp.StatusCode, bodyText), Body: rawBodyText}
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

// extractFriendlyErrorMessage 尝试从 JSON 错误体中提取关键信息并转换为友好的错误消息。
func extractFriendlyErrorMessage(bodyText string) string {
	if bodyText == "" || !strings.HasPrefix(strings.TrimSpace(bodyText), "{") {
		return ""
	}

	var errorBody map[string]any
	if err := json.Unmarshal([]byte(bodyText), &errorBody); err != nil {
		return ""
	}

	// 提取嵌套的 error.message
	var message string
	if errorObj, ok := errorBody["error"].(map[string]any); ok {
		if msg, ok := errorObj["message"].(string); ok {
			message = strings.TrimSpace(msg)
		}
	}

	if message == "" {
		return ""
	}

	// 识别常见错误模式并提供友好的中文提示
	if strings.Contains(message, "Upstream returned HTTP 400") {
		return "上游服务器返回 400 错误，可能是请求参数不符合要求。请检查模型配置或联系中转站运营者"
	}

	if strings.Contains(message, "Upstream returned HTTP") {
		// 提取状态码
		return fmt.Sprintf("上游服务器错误: %s", message)
	}

	// 其他情况返回原始 message（比完整 JSON 更清晰）
	return message
}
