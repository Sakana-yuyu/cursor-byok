// provider_balance_negative_cache_test.go 验证余额查询负缓存：
// 上游不支持余额查询时，确定性失败结果进入负缓存，重复轮询不再打上游；
// 瞬时传输失败不进负缓存；ForceRefresh 显式刷新可绕过。
package client

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestQueryProviderBalanceNegativeCacheForUnsupportedUpstream(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	service := newProviderBalanceTestService(t, server.Client())
	request := ProviderBalanceRequest{
		Type:           "openai",
		BaseURL:        server.URL + "/v1",
		APIKey:         "test-key",
		BalanceProfile: "general",
	}

	first := service.QueryProviderBalance(request)
	if first.Supported {
		t.Fatalf("expected unsupported balance, got %#v", first)
	}
	if first.Transient {
		t.Fatalf("expected deterministic failure, got transient: %#v", first)
	}
	firstHits := hits.Load()
	if firstHits == 0 {
		t.Fatal("expected the first query to hit the upstream")
	}

	// 第二次查询应命中负缓存，不再向上游发请求。
	second := service.QueryProviderBalance(request)
	if second.Supported {
		t.Fatalf("expected unsupported balance, got %#v", second)
	}
	if second.Message != first.Message {
		t.Fatalf("expected cached result to match first result, got %#v vs %#v", second, first)
	}
	if got := hits.Load(); got != firstHits {
		t.Fatalf("expected the second query to be served from negative cache, upstream hits %d -> %d", firstHits, got)
	}

	// ForceRefresh 绕过负缓存，重新打上游。
	refresh := request
	refresh.ForceRefresh = true
	third := service.QueryProviderBalance(refresh)
	if third.Supported {
		t.Fatalf("expected unsupported balance after refresh, got %#v", third)
	}
	if got := hits.Load(); got <= firstHits {
		t.Fatalf("expected ForceRefresh to bypass the negative cache, upstream hits %d -> %d", firstHits, got)
	}
}

func TestQueryProviderBalanceNegativeCacheSkipsTransientFailures(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		// 掐断连接模拟传输层失败（网络抖动/上游宕机），属瞬时失败，不应进负缓存。
		if hijacker, ok := w.(http.Hijacker); ok {
			if conn, _, err := hijacker.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	service := newProviderBalanceTestService(t, server.Client())
	request := ProviderBalanceRequest{
		Type:           "openai",
		BaseURL:        server.URL + "/v1",
		APIKey:         "test-key",
		BalanceProfile: "general",
	}

	first := service.QueryProviderBalance(request)
	if first.Supported {
		t.Fatalf("expected unsupported balance, got %#v", first)
	}
	if !first.Transient {
		t.Fatalf("expected transient failure for connection reset, got %#v", first)
	}
	firstHits := hits.Load()
	if firstHits == 0 {
		t.Fatal("expected the first query to hit the upstream")
	}

	// 瞬时失败未进负缓存：第二次查询应重新打上游。
	second := service.QueryProviderBalance(request)
	if second.Supported {
		t.Fatalf("expected unsupported balance, got %#v", second)
	}
	if got := hits.Load(); got <= firstHits {
		t.Fatalf("expected transient failures to bypass the negative cache, upstream hits %d -> %d", firstHits, got)
	}
}
