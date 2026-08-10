// router_idle_retry_test.go 验证「上游静默卡死」（provider 流空闲看门狗到期仍无
// 有效内容）被识别为 pre-output 失败并在 router 层做有界透明重试：首次卡死后重试
// 一次即可成功；超过重试上限才把错误交给上层（保持原有终态行为）。
package modeladapter

import (
	"context"
	"errors"
	"testing"
	"time"

	legacyruntime "cursor/internal/runtime"
)

func TestProviderStreamIdleTimeoutErrorDetectable(t *testing.T) {
	t.Parallel()

	t.Run("message preserved", func(t *testing.T) {
		t.Parallel()
		base := providerStreamIdleTimeoutError(240 * time.Second)
		if got, want := base.Error(), "provider stream idle timeout after 240s without effective content"; got != want {
			t.Fatalf("Error() = %q, want %q", got, want)
		}
	})

	t.Run("detected directly and through wrapping", func(t *testing.T) {
		t.Parallel()
		base := providerStreamIdleTimeoutError(240 * time.Second)
		if !IsProviderStreamIdleTimeout(base) {
			t.Fatal("base idle error not detected")
		}
		// router 层会把渠道失败包装进 ChannelError（保留 Unwrap 链）。
		if !IsProviderStreamIdleTimeout(&ChannelError{Cause: base}) {
			t.Fatal("idle error through ChannelError wrap not detected")
		}
	})

	t.Run("unrelated errors not detected", func(t *testing.T) {
		t.Parallel()
		if IsProviderStreamIdleTimeout(errors.New("connection reset")) {
			t.Fatal("unrelated error misdetected")
		}
		if IsProviderStreamIdleTimeout(nil) {
			t.Fatal("nil misdetected")
		}
	})
}

// idleStallAdapter 前 stallsLeft 次调用返回空闲看门狗超时错误（模拟上游静默卡死），
// 之后成功返回。记录每次收到的请求空闲阈值，供断言重试降级。
type idleStallAdapter struct {
	stallsLeft  int
	calls       int
	sawTimeouts []time.Duration
}

func (a *idleStallAdapter) Stream(_ context.Context, req StreamRequest, _ func(ModelEvent) error) error {
	a.calls++
	a.sawTimeouts = append(a.sawTimeouts, req.ProviderStreamIdleTimeout)
	if a.stallsLeft > 0 {
		a.stallsLeft--
		return &providerStreamIdleTimeoutErr{timeout: 240 * time.Second}
	}
	return nil
}

type idleRetryResolver struct {
	channel *legacyruntime.ResolvedChannel
}

func (r *idleRetryResolver) SelectChannelForModel(context.Context, string) (*legacyruntime.ResolvedChannel, error) {
	return r.channel, nil
}

func (r *idleRetryResolver) ProviderStreamIdleTimeout(context.Context) time.Duration {
	return 90 * time.Second
}

func (r *idleRetryResolver) TurnStaleTimeout(context.Context) time.Duration {
	return 0
}

func (r *idleRetryResolver) NativeDelegationProgressTimeout(context.Context) time.Duration {
	return 0
}

func newIdleRetryRouter(adapter ModelAdapter) *Router {
	return &Router{
		openai:    adapter,
		anthropic: NewAnthropicAdapter(),
		gemini:    NewGeminiAdapter(),
		resolver: &idleRetryResolver{channel: &legacyruntime.ResolvedChannel{
			ID:             "test-channel",
			Name:           "test",
			GroupName:      "test-group",
			Provider:       "openai",
			ProtocolMode:   "auto",
			ProtocolGroup:  "responses",
			BaseURL:        "http://127.0.0.1:1",
			APIKey:         "test-key",
			Model:          "gpt-5.6-sol",
			OpenAIEndpoint: "/responses",
		}},
		healthByChannel: make(map[string]channelHealth),
	}
}

func idleRetryStreamRequest() StreamRequest {
	return StreamRequest{
		RequestID:                 "req-idle-retry",
		ModelCallID:               "call-idle-retry",
		ModelID:                   "gpt-5.6-sol",
		ProviderStreamIdleTimeout: 240 * time.Second,
	}
}

func TestRouterStreamRetriesIdleTimeoutThenSucceeds(t *testing.T) {
	adapter := &idleStallAdapter{stallsLeft: 1}
	router := newIdleRetryRouter(adapter)

	err := router.Stream(context.Background(), idleRetryStreamRequest(), func(ModelEvent) error { return nil })
	if err != nil {
		t.Fatalf("Stream() = %v, want nil after transparent idle-timeout retry", err)
	}
	if adapter.calls != 2 {
		t.Fatalf("adapter calls = %d, want 2 (initial + 1 retry)", adapter.calls)
	}
	if len(adapter.sawTimeouts) != 2 || adapter.sawTimeouts[0] != 240*time.Second {
		t.Fatalf("initial idle timeout not honored: saw %v", adapter.sawTimeouts)
	}
	if adapter.sawTimeouts[1] != providerStreamIdleRetryTimeout {
		t.Fatalf("retry idle timeout = %s, want reduced %s", adapter.sawTimeouts[1], providerStreamIdleRetryTimeout)
	}
}

func TestRouterStreamStopsAfterIdleTimeoutRetryCap(t *testing.T) {
	adapter := &idleStallAdapter{stallsLeft: 3}
	router := newIdleRetryRouter(adapter)

	err := router.Stream(context.Background(), idleRetryStreamRequest(), func(ModelEvent) error { return nil })
	if err == nil {
		t.Fatal("Stream() = nil, want idle timeout error after retry cap")
	}
	if !IsProviderStreamIdleTimeout(err) {
		t.Fatalf("final error not an idle timeout: %v", err)
	}
	if want := 1 + maxProviderStreamIdleRetries; adapter.calls != want {
		t.Fatalf("adapter calls = %d, want %d (initial + cap)", adapter.calls, want)
	}
}

func TestRouterStreamSkipsBudgetGateAfterIdleTimeout(t *testing.T) {
	// 关键回归：单渠道下，一次 240s 静默后 elapsed 已远超 routerRetryTotalBudget(45s)。
	// 常规瞬时错误会被预算门槛拒绝；静默卡死必须绕过该门槛继续重试。
	adapter := &idleStallAdapter{stallsLeft: 1}
	router := newIdleRetryRouter(adapter)
	if err := router.Stream(context.Background(), idleRetryStreamRequest(), func(ModelEvent) error { return nil }); err != nil {
		t.Fatalf("Stream() = %v, want nil", err)
	}
	if adapter.calls != 2 {
		t.Fatalf("adapter calls = %d, want 2: budget gate must not block the idle-timeout retry", adapter.calls)
	}
}
