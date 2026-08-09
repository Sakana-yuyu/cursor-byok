package apperror

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

type testStatusError struct {
	status     int
	retryAfter time.Duration
}

func (e testStatusError) Error() string             { return "upstream failed" }
func (e testStatusError) Status() int               { return e.status }
func (e testStatusError) RetryAfter() time.Duration { return e.retryAfter }

type testTimeoutError struct{}

func (testTimeoutError) Error() string   { return "i/o timeout" }
func (testTimeoutError) Timeout() bool   { return true }
func (testTimeoutError) Temporary() bool { return true }

var _ net.Error = testTimeoutError{}

func TestClassifyKnownFailures(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		code        Code
		kind        Kind
		disposition Disposition
	}{
		{name: "canceled", err: context.Canceled, code: CodeCanceled, kind: KindCanceled, disposition: DispositionCanceled},
		{name: "deadline", err: context.DeadlineExceeded, code: CodeTimeout, kind: KindTimeout, disposition: DispositionRetryable},
		{name: "network timeout", err: testTimeoutError{}, code: CodeTimeout, kind: KindTimeout, disposition: DispositionRetryable},
		{name: "authentication", err: testStatusError{status: 401}, code: CodeAuthentication, kind: KindAuthentication, disposition: DispositionBlocked},
		{name: "model missing", err: testStatusError{status: 404}, code: CodeNotFound, kind: KindConfiguration, disposition: DispositionBlocked},
		{name: "rate limited", err: testStatusError{status: 429, retryAfter: 3 * time.Second}, code: CodeRateLimited, kind: KindRateLimit, disposition: DispositionRetryable},
		{name: "provider unavailable", err: testStatusError{status: 503}, code: CodeProviderUnavailable, kind: KindProvider, disposition: DispositionRetryable},
		{name: "internal", err: errors.New("boom"), code: CodeInternal, kind: KindInternal, disposition: DispositionFatal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify("provider.stream", tt.err)
			if got.Code != tt.code || got.Kind != tt.kind || got.Disposition != tt.disposition {
				t.Fatalf("Classify() = code %q kind %q disposition %q", got.Code, got.Kind, got.Disposition)
			}
			if !errors.Is(got, tt.err) {
				t.Fatalf("classified error must preserve cause: %v", got)
			}
		})
	}
}

func TestClassifyPreservesRetryAfterAndTrace(t *testing.T) {
	got := Classify("provider.stream", testStatusError{status: 429, retryAfter: 5 * time.Second}, WithTraceID("trace-123"))
	if got.RetryAfter != 5*time.Second {
		t.Fatalf("RetryAfter = %s, want 5s", got.RetryAfter)
	}
	if got.TraceID != "trace-123" {
		t.Fatalf("TraceID = %q, want trace-123", got.TraceID)
	}
	if got.UserMessage == "" || got.TechnicalMessage == "" {
		t.Fatal("classified error must contain user and technical messages")
	}
}

func TestClassifyAllowsBoundarySpecificOverrides(t *testing.T) {
	got := Classify(
		"provider.stream",
		errors.New("stream ended after output"),
		WithCode(CodeStreamInterrupted),
		WithKind(KindStream),
		WithDisposition(DispositionFatal),
		WithUserMessage("响应中断，请重试本轮请求"),
	)
	if got.Code != CodeStreamInterrupted || got.Kind != KindStream || got.Disposition != DispositionFatal {
		t.Fatalf("override result = code %q kind %q disposition %q", got.Code, got.Kind, got.Disposition)
	}
	if got.UserMessage != "响应中断，请重试本轮请求" {
		t.Fatalf("UserMessage = %q", got.UserMessage)
	}
}

func TestJoinKeepsPrimaryAndSecondaryFailures(t *testing.T) {
	primary := errors.New("provider failed")
	secondary := errors.New("checkpoint failed")
	joined := Join(primary, secondary)
	if !errors.Is(joined, primary) || !errors.Is(joined, secondary) {
		t.Fatalf("Join() must preserve both failures: %v", joined)
	}
}
