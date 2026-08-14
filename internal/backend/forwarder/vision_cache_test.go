package forwarder

import (
	"context"
	"fmt"
	"testing"
)

func TestVisionCacheLRUEviction(t *testing.T) {
	service := &Service{
		visionCache:      make(map[string]visionCacheEntry),
		visionInflight:   make(map[string]*visionInflightCall),
	}
	limit := visionProxyCacheLimit
	visionProxyCacheLimit = 3
	t.Cleanup(func() { visionProxyCacheLimit = limit })

	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("key-%d", i)
		text, err := service.cachedVisionDescribe(context.Background(), key, func(context.Context) (string, error) {
			return key, nil
		})
		if err != nil || text != key {
			t.Fatalf("seed cache %q = (%q, %v)", key, text, err)
		}
	}

	// 访问 key-0 使其成为最近使用，随后写入 key-3 应淘汰 key-1。
	if _, err := service.cachedVisionDescribe(context.Background(), "key-0", func(context.Context) (string, error) {
		t.Fatal("key-0 should hit cache")
		return "", nil
	}); err != nil {
		t.Fatalf("cache hit key-0: %v", err)
	}

	if _, err := service.cachedVisionDescribe(context.Background(), "key-3", func(context.Context) (string, error) {
		return "key-3", nil
	}); err != nil {
		t.Fatalf("insert key-3: %v", err)
	}

	service.visionCacheMu.Lock()
	defer service.visionCacheMu.Unlock()
	for _, key := range []string{"key-0", "key-2", "key-3"} {
		if _, ok := service.visionCache[key]; !ok {
			t.Fatalf("expected %q to remain cached", key)
		}
	}
	if _, ok := service.visionCache["key-1"]; ok {
		t.Fatal("expected key-1 to be evicted by LRU")
	}
}
