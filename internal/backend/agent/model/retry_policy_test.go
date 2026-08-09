package modeladapter

import (
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"
)

func transientNetworkError() error {
	return &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
}

func retryHeaders(name, value string) http.Header {
	headers := make(http.Header)
	headers.Set(name, value)
	return headers
}

func TestRouterRetryDelayFailsOverImmediatelyToUntriedChannel(t *testing.T) {
	delay, ok := routerRetryDelay(1, false, transientNetworkError(), 0)
	if !ok || delay != 0 {
		t.Fatalf("routerRetryDelay() = (%s, %v), want (0, true)", delay, ok)
	}
}

func TestRouterRetryDelayStopsForPermanentFailure(t *testing.T) {
	delay, ok := routerRetryDelay(1, true, &HTTPStatusError{StatusCode: http.StatusUnauthorized, Message: "unauthorized"}, 0)
	if ok || delay != 0 {
		t.Fatalf("routerRetryDelay() = (%s, %v), want (0, false)", delay, ok)
	}
}

func TestRouterRetryDelayUsesBoundedSchedule(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second}
	for nextAttempt, expected := range want {
		delay, ok := routerRetryDelay(nextAttempt+1, true, transientNetworkError(), 0)
		if !ok || delay != expected {
			t.Fatalf("attempt %d: routerRetryDelay() = (%s, %v), want (%s, true)", nextAttempt+1, delay, ok, expected)
		}
	}
	if delay, ok := routerRetryDelay(len(want)+1, true, transientNetworkError(), 0); ok || delay != 0 {
		t.Fatalf("exhausted retry = (%s, %v), want (0, false)", delay, ok)
	}
}

func TestRouterRetryDelayDoesNotHideLongRateLimit(t *testing.T) {
	err := &HTTPStatusError{
		StatusCode: http.StatusTooManyRequests,
		Message:    "rate limited",
		Headers:    retryHeaders("Retry-After", "120"),
	}
	delay, ok := routerRetryDelay(1, true, err, 0)
	if ok || delay != 0 {
		t.Fatalf("long Retry-After = (%s, %v), want (0, false)", delay, ok)
	}
}

func TestRouterRetryDelayHonorsShortRetryAfterWithinTotalBudget(t *testing.T) {
	err := &HTTPStatusError{
		StatusCode: http.StatusTooManyRequests,
		Message:    "rate limited",
		Headers:    retryHeaders("retry-after-ms", "3000"),
	}
	delay, ok := routerRetryDelay(1, true, err, 0)
	if !ok || delay != 3*time.Second {
		t.Fatalf("short Retry-After = (%s, %v), want (3s, true)", delay, ok)
	}
	if delay, ok := routerRetryDelay(5, true, err, 43*time.Second); ok || delay != 0 {
		t.Fatalf("total budget exhausted = (%s, %v), want (0, false)", delay, ok)
	}
}
