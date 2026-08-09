package modeladapter

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestHTTPStatusErrorExposesRetryAfter(t *testing.T) {
	err := &HTTPStatusError{
		StatusCode: http.StatusTooManyRequests,
		Headers:    http.Header{"Retry-After": []string{"3"}},
	}
	if got := err.RetryAfter(); got != 3*time.Second {
		t.Fatalf("RetryAfter() = %s, want 3s", got)
	}
}

func TestChannelErrorPreservesRetryAfter(t *testing.T) {
	cause := &HTTPStatusError{
		StatusCode: http.StatusTooManyRequests,
		Headers:    http.Header{"Retry-After-Ms": []string{"2500"}},
	}
	wrapped := &ChannelError{Cause: cause}
	var retryErr interface{ RetryAfter() time.Duration }
	if !errors.As(wrapped, &retryErr) {
		t.Fatal("ChannelError must preserve RetryAfter through errors.As")
	}
	if got := retryErr.RetryAfter(); got != 2500*time.Millisecond {
		t.Fatalf("RetryAfter() = %s, want 2.5s", got)
	}
}
