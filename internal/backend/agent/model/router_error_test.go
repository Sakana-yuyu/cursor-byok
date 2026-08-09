package modeladapter

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"cursor/internal/apperror"
)

func TestChannelFailureCooldownDoesNotPanicWithoutStructuredStatus(t *testing.T) {
	got := channelFailureCooldown(errors.New("provider status=429"))
	if got != time.Minute {
		t.Fatalf("channelFailureCooldown() = %s, want 1m", got)
	}
}

func TestClassifyRouterErrorPreservesProviderCauseAndRequestTrace(t *testing.T) {
	cause := &HTTPStatusError{StatusCode: http.StatusTooManyRequests, Message: "rate limited"}
	got := classifyRouterError(StreamRequest{RequestID: "request-123"}, cause)

	var appErr *apperror.AppError
	if !errors.As(got, &appErr) {
		t.Fatalf("classifyRouterError() = %T, want *apperror.AppError", got)
	}
	if appErr.Code != apperror.CodeRateLimited || appErr.TraceID != "request-123" {
		t.Fatalf("classified error = code %q trace %q", appErr.Code, appErr.TraceID)
	}
	var statusErr *HTTPStatusError
	if !errors.As(got, &statusErr) || statusErr != cause {
		t.Fatal("classified router error must preserve HTTPStatusError cause")
	}
}

func TestClassifyRouterErrorMarksMidStreamInterruptionFatal(t *testing.T) {
	cause := midStreamInterruptedError(errors.New("connection reset"))
	got := classifyRouterError(StreamRequest{RequestID: "request-456"}, cause)
	var appErr *apperror.AppError
	if !errors.As(got, &appErr) {
		t.Fatalf("classifyRouterError() = %T, want *apperror.AppError", got)
	}
	if appErr.Code != apperror.CodeStreamInterrupted || appErr.Kind != apperror.KindStream || appErr.Disposition != apperror.DispositionFatal {
		t.Fatalf("mid-stream classification = code %q kind %q disposition %q", appErr.Code, appErr.Kind, appErr.Disposition)
	}
}
