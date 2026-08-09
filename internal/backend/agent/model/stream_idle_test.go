// stream_idle_test.go 验证 SSE 逐块读超时派生：委派/子代理流（长空闲超时）
// 获得更大的块间隔容忍，父代理默认 90s 空闲超时保持原 30s 逐块阈值。
package modeladapter

import (
	"testing"
	"time"
)

func TestProviderStreamChunkTimeout(t *testing.T) {
	t.Parallel()

	t.Run("default idle timeout keeps original 30s chunk threshold", func(t *testing.T) {
		t.Parallel()
		got := providerStreamChunkTimeout(90 * time.Second)
		if got != 30*time.Second {
			t.Fatalf("default chunk timeout changed: got %s want %s", got, 30*time.Second)
		}
	})

	t.Run("zero idle timeout keeps original 30s chunk threshold", func(t *testing.T) {
		t.Parallel()
		got := providerStreamChunkTimeout(0)
		if got != 30*time.Second {
			t.Fatalf("zero idle chunk timeout changed: got %s want %s", got, 30*time.Second)
		}
	})

	t.Run("delegated idle timeout relaxes chunk threshold", func(t *testing.T) {
		t.Parallel()
		got := providerStreamChunkTimeout(10 * time.Minute)
		if got != 150*time.Second {
			t.Fatalf("delegated chunk timeout not relaxed: got %s want %s", got, 150*time.Second)
		}
	})

	t.Run("never drops below 30s floor", func(t *testing.T) {
		t.Parallel()
		got := providerStreamChunkTimeout(45 * time.Second)
		if got < 30*time.Second {
			t.Fatalf("chunk timeout below floor: got %s", got)
		}
	})
}
