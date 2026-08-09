// Package apperror defines the small, stable error contract shared by runtime layers.
package apperror

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

type Code string

const (
	CodeCanceled            Code = "canceled"
	CodeTimeout             Code = "timeout"
	CodeNetwork             Code = "network_error"
	CodeAuthentication      Code = "authentication_required"
	CodePermission          Code = "permission_denied"
	CodeRateLimited         Code = "rate_limited"
	CodeQuotaExceeded       Code = "quota_exceeded"
	CodeNotFound            Code = "resource_not_found"
	CodeConfiguration       Code = "configuration_invalid"
	CodeProviderUnavailable Code = "provider_unavailable"
	CodeToolTimeout         Code = "tool_timeout"
	CodeStreamInterrupted   Code = "stream_interrupted"
	CodeInternal            Code = "internal_error"
)

type Kind string

const (
	KindCanceled       Kind = "canceled"
	KindTimeout        Kind = "timeout"
	KindNetwork        Kind = "network"
	KindAuthentication Kind = "authentication"
	KindPermission     Kind = "permission"
	KindRateLimit      Kind = "rate_limit"
	KindQuota          Kind = "quota"
	KindConfiguration  Kind = "configuration"
	KindProvider       Kind = "provider"
	KindTool           Kind = "tool"
	KindStream         Kind = "stream"
	KindInternal       Kind = "internal"
)

type Disposition string

const (
	DispositionRetryable Disposition = "retryable"
	DispositionDegraded  Disposition = "degraded"
	DispositionBlocked   Disposition = "blocked"
	DispositionFatal     Disposition = "fatal"
	DispositionCanceled  Disposition = "canceled"
)

type AppError struct {
	Operation        string
	Code             Code
	Kind             Kind
	Disposition      Disposition
	RetryAfter       time.Duration
	UserMessage      string
	TechnicalMessage string
	TraceID          string
	Cause            error
}

func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	if e.TechnicalMessage != "" {
		return e.TechnicalMessage
	}
	return e.UserMessage
}

func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *AppError) Status() int {
	if e == nil {
		return 0
	}
	var statusErr interface{ Status() int }
	if errors.As(e.Cause, &statusErr) {
		return statusErr.Status()
	}
	return 0
}

func (e *AppError) SafeMessage() string {
	if e == nil {
		return ""
	}
	return e.TechnicalMessage
}

type Option func(*classifyOptions)

type classifyOptions struct {
	traceID     string
	code        Code
	kind        Kind
	disposition Disposition
	userMessage string
}

func WithTraceID(traceID string) Option {
	return func(options *classifyOptions) {
		options.traceID = sanitizeTraceID(traceID)
	}
}

func WithCode(code Code) Option {
	return func(options *classifyOptions) {
		options.code = code
	}
}

func WithKind(kind Kind) Option {
	return func(options *classifyOptions) {
		options.kind = kind
	}
}

func WithDisposition(disposition Disposition) Option {
	return func(options *classifyOptions) {
		options.disposition = disposition
	}
}

func WithUserMessage(message string) Option {
	return func(options *classifyOptions) {
		options.userMessage = strings.TrimSpace(message)
	}
}

func Classify(operation string, cause error, options ...Option) *AppError {
	if cause == nil {
		return nil
	}
	settings := classifyOptions{}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}

	result := classifyCause(strings.TrimSpace(operation), cause)
	result.TraceID = settings.traceID
	if settings.code != "" {
		result.Code = settings.code
	}
	if settings.kind != "" {
		result.Kind = settings.kind
	}
	if settings.disposition != "" {
		result.Disposition = settings.disposition
	}
	if settings.userMessage != "" {
		result.UserMessage = settings.userMessage
	}
	return result
}

func Join(primary error, secondary ...error) error {
	causes := make([]error, 0, 1+len(secondary))
	if primary != nil {
		causes = append(causes, primary)
	}
	for _, cause := range secondary {
		if cause != nil {
			causes = append(causes, cause)
		}
	}
	switch len(causes) {
	case 0:
		return nil
	case 1:
		return causes[0]
	default:
		return errors.Join(causes...)
	}
}

type statusCoder interface{ Status() int }
type retryAfterCoder interface{ RetryAfter() time.Duration }

func classifyCause(operation string, cause error) *AppError {
	if errors.Is(cause, context.Canceled) {
		return newAppError(operation, CodeCanceled, KindCanceled, DispositionCanceled, "操作已取消", cause)
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return newAppError(operation, CodeTimeout, KindTimeout, DispositionRetryable, "请求超时，正在准备恢复", cause)
	}

	var netErr net.Error
	if errors.As(cause, &netErr) {
		if netErr.Timeout() {
			return newAppError(operation, CodeTimeout, KindTimeout, DispositionRetryable, "请求超时，正在准备恢复", cause)
		}
		return newAppError(operation, CodeNetwork, KindNetwork, DispositionRetryable, "暂时无法连接服务，正在准备恢复", cause)
	}

	status := 0
	var statusErr statusCoder
	if errors.As(cause, &statusErr) {
		status = statusErr.Status()
	}
	switch status {
	case 401, 402:
		return newAppError(operation, CodeAuthentication, KindAuthentication, DispositionBlocked, "密钥无效或暂无可用额度，请检查供应商配置", cause)
	case 403:
		return newAppError(operation, CodePermission, KindPermission, DispositionBlocked, "当前密钥没有访问权限，请检查供应商配置", cause)
	case 404:
		return newAppError(operation, CodeNotFound, KindConfiguration, DispositionBlocked, "模型或接口地址不存在，请检查配置", cause)
	case 408:
		return newAppError(operation, CodeTimeout, KindTimeout, DispositionRetryable, "请求超时，正在准备恢复", cause)
	case 429:
		result := newAppError(operation, CodeRateLimited, KindRateLimit, DispositionRetryable, "请求过于频繁，稍后会自动重试", cause)
		var retryErr retryAfterCoder
		if errors.As(cause, &retryErr) {
			result.RetryAfter = retryErr.RetryAfter()
		}
		return result
	case 500, 502, 503, 504, 524:
		return newAppError(operation, CodeProviderUnavailable, KindProvider, DispositionRetryable, "供应商暂时不可用，正在准备恢复", cause)
	case 400:
		return newAppError(operation, CodeConfiguration, KindConfiguration, DispositionBlocked, "请求参数或模型配置不正确，请检查配置", cause)
	}

	return newAppError(operation, CodeInternal, KindInternal, DispositionFatal, "服务发生异常，请重试或导出诊断信息", cause)
}

func newAppError(operation string, code Code, kind Kind, disposition Disposition, userMessage string, cause error) *AppError {
	return &AppError{
		Operation:        operation,
		Code:             code,
		Kind:             kind,
		Disposition:      disposition,
		UserMessage:      userMessage,
		TechnicalMessage: safeTechnicalMessage(cause),
		Cause:            cause,
	}
}

var (
	credentialPattern = regexp.MustCompile(`(?i)(api[_-]?key|token|password|secret)=([^\s&]+)`)
	urlQueryPattern   = regexp.MustCompile(`(?i)(https?://[^\s?]+)\?[^\s]+`)
)

func safeTechnicalMessage(cause error) string {
	message := strings.TrimSpace(fmt.Sprint(cause))
	message = credentialPattern.ReplaceAllString(message, "$1=[REDACTED]")
	message = urlQueryPattern.ReplaceAllString(message, "$1?[REDACTED]")
	if len(message) > 1024 {
		return message[:1024] + "…"
	}
	return message
}

func sanitizeTraceID(traceID string) string {
	traceID = strings.TrimSpace(traceID)
	if len(traceID) > 128 {
		return traceID[:128]
	}
	for _, r := range traceID {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("._:-", r) {
			return ""
		}
	}
	return traceID
}
