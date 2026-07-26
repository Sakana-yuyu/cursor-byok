// http_error.go 负责把非 2xx HTTP 响应整理成带响应体摘要的错误。
package modeladapter

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	// maxErrorBodyBytes 表示错误响应体最多读取的字节数。
	maxErrorBodyBytes = 8192
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
func buildHTTPStatusError(prefix string, resp *http.Response) error {
	if resp == nil {
		return fmt.Errorf("%s response is nil", strings.TrimSpace(prefix))
	}

	limitedBody, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
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
