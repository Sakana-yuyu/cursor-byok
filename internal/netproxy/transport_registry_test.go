package netproxy

import (
	"runtime"
	"testing"
	"time"
)

func proxyTransportCount() int {
	proxyTransportsMu.Lock()
	defer proxyTransportsMu.Unlock()
	return len(proxyTransports)
}

// TestProxyTransportRegistryReleasesDroppedTransports 锁定注册表只持弱引用：
// 调用方丢弃的 transport 被 GC 后应自动摘除，不再无界增长。
func TestProxyTransportRegistryReleasesDroppedTransports(t *testing.T) {
	baseline := proxyTransportCount()
	for i := 0; i < 64; i++ {
		_ = NewTransport(nil)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if proxyTransportCount() <= baseline {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("registry still holds %d transports, baseline %d", proxyTransportCount(), baseline)
}

// TestProxyTransportRegistryKeepsLiveTransports 确认存活 transport 仍会被代理变更时的
// CloseIdleConnections 覆盖到。
func TestProxyTransportRegistryKeepsLiveTransports(t *testing.T) {
	transport := NewTransport(nil)
	runtime.GC()
	found := false
	proxyTransportsMu.Lock()
	for pointer := range proxyTransports {
		if pointer.Value() == transport {
			found = true
		}
	}
	proxyTransportsMu.Unlock()
	if !found {
		t.Fatal("live transport missing from registry")
	}
	closeIdleProxyConnections()
	runtime.KeepAlive(transport)
}
