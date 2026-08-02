// retry.go 保留 provider HTTP 请求入口的历史命名；对建连阶段的瞬时错误做有界重试。
package modeladapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// providerRetryMaxAttempts 表示建连阶段的最大尝试次数（含首次）。
	// 已降为 1：429/5xx 等瞬时状态码不再在此层自行重试退避，
	// 而是直接把响应交回 router 外层统一调度，避免内外双层退避叠加放大等待时间。
	// 总重试上限由 routerMaxStreamAttempts（10）统管。
	providerRetryMaxAttempts = 1
	// providerRetryBaseDelay 表示指数退避的基准间隔。
	providerRetryBaseDelay = 200 * time.Millisecond
	// providerRetryMaxDelay 表示指数退避的最大间隔。
	providerRetryMaxDelay = 5 * time.Second
	// providerRetryMaxRetryAfter 表示 Retry-After 允许等待的上限，避免异常大的值阻塞请求。
	providerRetryMaxRetryAfter = 30 * time.Second
	// providerRetrySummaryHeader 是内部头，用于把重试摘要透传给 http_error.go；不会回发给上游或模型。
	providerRetrySummaryHeader = "X-Zcode-Provider-Retry-Summary"
)

// DoProviderRequestWithRetry 保留旧入口名；只在流式响应体开始消费前，对瞬时失败做有界重试。
func DoProviderRequestWithRetry(
	ctx context.Context,
	client *http.Client,
	provider string,
	requestID string,
	modelCallID string,
	buildRequest func(context.Context) (*http.Request, error),
) (*http.Response, error) {
	return doProviderRequestWithRetry(ctx, client, provider, requestID, modelCallID, buildRequest)
}

// doProviderRequestWithRetry 对建连阶段的瞬时错误（网络/传输错误、HTTP 429、5xx）做指数退避重试；
// 对 4xx（429 除外）与 ctx 取消/超时不重试。因为 buildRequest 每次都会重建请求体，且响应体在返回后才被消费，
// 所以这里的重试严格发生在任何响应字节流式回传给客户端之前，不存在重复发送已消费流的问题。
func doProviderRequestWithRetry(
	ctx context.Context,
	client *http.Client,
	provider string,
	requestID string,
	modelCallID string,
	buildRequest func(context.Context) (*http.Request, error),
) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}

	var lastErr error
	var lastStatus int
	var nextDelay time.Duration

	for attempt := 0; attempt < providerRetryMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}
		if attempt > 0 {
			if err := sleepWithContext(ctx, nextDelay); err != nil {
				if lastErr != nil {
					return nil, lastErr
				}
				return nil, err
			}
		}

		httpReq, err := buildRequest(ctx)
		if err != nil {
			// 构造请求失败属于本地错误，重试无意义。
			return nil, err
		}

		resp, err := client.Do(httpReq)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			// ctx 取消/超时导致的错误不属于瞬时错误，直接返回。
			if ctx.Err() != nil {
				return nil, err
			}
			lastErr = err
			lastStatus = 0
			nextDelay = providerRetryBackoff(attempt, 0)
			continue
		}

		if !isTransientProviderStatus(resp.StatusCode) {
			annotateProviderRetrySummary(resp, attempt)
			return resp, nil
		}

		lastErr = nil
		lastStatus = resp.StatusCode
		if attempt == providerRetryMaxAttempts-1 {
			// 最后一次尝试仍是瞬时状态码，交回响应让调用方生成带响应体的错误。
			annotateProviderRetrySummary(resp, attempt)
			return resp, nil
		}
		nextDelay = providerRetryBackoff(attempt, parseRetryAfter(resp))
		_ = resp.Body.Close()
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("provider %s request failed after %d attempts last_status=%d", strings.TrimSpace(provider), providerRetryMaxAttempts, lastStatus)
}

// isTransientProviderStatus 判断状态码是否属于可重试的瞬时错误：429 或任何 5xx。
func isTransientProviderStatus(status int) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	return status >= 500 && status <= 599
}

// providerRetryBackoff 计算下一次重试前的等待时长；优先遵循 Retry-After，否则指数退避加抖动。
func providerRetryBackoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > providerRetryMaxRetryAfter {
			return providerRetryMaxRetryAfter
		}
		return retryAfter
	}
	backoff := providerRetryBaseDelay << attempt
	if backoff <= 0 || backoff > providerRetryMaxDelay {
		backoff = providerRetryMaxDelay
	}
	jitter := time.Duration(rand.Int63n(int64(backoff)/2 + 1))
	return backoff/2 + jitter
}

// parseRetryAfter 解析 429 响应上的 Retry-After 头，支持秒数与 HTTP 日期两种形式。
func parseRetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	if ms := strings.TrimSpace(resp.Header.Get("retry-after-ms")); ms != "" {
		if milliseconds, err := strconv.Atoi(ms); err == nil && milliseconds > 0 {
			return time.Duration(milliseconds) * time.Millisecond
		}
	}
	raw := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		if delta := time.Until(when); delta > 0 {
			return delta
		}
	}
	return 0
}

// sleepWithContext 在等待退避时长期间尊重 ctx 取消/超时。
func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// annotateProviderRetrySummary 在发生过重试时，把摘要写入内部头，供 http_error.go 读取。
func annotateProviderRetrySummary(resp *http.Response, attempt int) {
	if resp == nil || attempt <= 0 {
		return
	}
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}
	resp.Header.Set(providerRetrySummaryHeader, fmt.Sprintf("retry_attempts=%d", attempt+1))
}

// ProviderRetryAttemptSummary 返回建连阶段的重试摘要（如 "retry_attempts=3"）；无重试时为空。
func ProviderRetryAttemptSummary(resp *http.Response) string {
	if resp == nil || resp.Header == nil {
		return ""
	}
	return strings.TrimSpace(resp.Header.Get(providerRetrySummaryHeader))
}

// maxStreamReconnects 表示流式阶段（已建连但尚未转发有效内容给客户端时）
// 因连接重置/EOF 等原因断开后的最大透明重连次数。
const maxStreamReconnects = 2

// IsStreamConnectionReset 判断流式读取阶段的错误是否属于连接级断开
// （可安全重连，因为尚未向客户端转发任何有效内容）。
// 排除 context.Canceled / DeadlineExceeded（用户主动取消不应重连）。
// 移植自 Reasonix IsConnReset。
func IsStreamConnectionReset(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// io.EOF / io.ErrUnexpectedEOF：连接在 SSE 流中途关闭
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	// 连接重置 / 中止
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) {
		return true
	}
	// net.Error 超时
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	// "body closed" / "connection reset" 等文本匹配（http 层包装的错误）
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "body closed") ||
		strings.Contains(lower, "use of closed") {
		return true
	}
	return false
}
