// router_timeout_test.go 验证流级上游看门狗时长解析：
// 调用方显式设置的 provider 流空闲超时必须被尊重（委派/子代理流放宽的通道），
// 仅当请求未显式设置时才回退到全局配置值（父代理交互流默认行为不变）。
package modeladapter

import (
	"testing"
	"time"
)

func TestResolveProviderStreamIdleTimeout(t *testing.T) {
	t.Parallel()

	t.Run("explicit request timeout wins", func(t *testing.T) {
		t.Parallel()
		got := resolveProviderStreamIdleTimeout(10*time.Minute, 90*time.Second)
		if got != 10*time.Minute {
			t.Fatalf("explicit timeout not respected: got %s want %s", got, 10*time.Minute)
		}
	})

	t.Run("zero request timeout falls back to configured", func(t *testing.T) {
		t.Parallel()
		got := resolveProviderStreamIdleTimeout(0, 90*time.Second)
		if got != 90*time.Second {
			t.Fatalf("fallback not applied: got %s want %s", got, 90*time.Second)
		}
	})

	t.Run("negative request timeout falls back to configured", func(t *testing.T) {
		t.Parallel()
		got := resolveProviderStreamIdleTimeout(-1, 5*time.Minute)
		if got != 5*time.Minute {
			t.Fatalf("negative treated as unset: got %s want %s", got, 5*time.Minute)
		}
	})
}
