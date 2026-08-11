package mitm

import (
	"context"
	"testing"
)

// TestNewProxyServerAllowsNilCertManager 验证 CA 材料不完整时（certManager=nil）
// 代理服务仍可创建（MITM 自动禁用），支撑应用降级启动。
func TestNewProxyServerAllowsNilCertManager(t *testing.T) {
	proxy, err := NewProxyServer("127.0.0.1:0", "http://127.0.0.1:1", t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("NewProxyServer with nil certManager: %v", err)
	}
	if proxy == nil {
		t.Fatal("NewProxyServer returned nil server")
	}
}

// TestGoproxyLogAdapterSuppressesBenignClientCertificateRejection 验证 goproxy
// 的 "Cannot handshake client ... tls: unknown certificate" 警告被静默：Cursor 后台
// 子组件不信任本应用 CA，握手即拒，属预期噪音，不应进入限频器（避免日志刷屏）。
// 通过比对共享 proxyLogLimiter 条目数判断：被抑制的消息不应产生新 key。
func TestGoproxyLogAdapterSuppressesBenignClientCertificateRejection(t *testing.T) {
	adapter := &goproxyLogAdapter{}
	messages := []string{
		"[19042] WARN: Cannot handshake client api3.cursor.sh:443 remote error: tls: unknown certificate",
		"[19116] WARN: Cannot handshake client metrics.cursor.sh:443 remote error: tls: unknown certificate",
		"[19225] WARN: Cannot handshake client api2.cursor.sh:443 remote error: tls: unknown certificate",
	}
	before := proxyLogLimiter.snapshotKeyCount()

	for _, msg := range messages {
		// 调用 Printf：被抑制的消息应在限频器之前 return，不产生新条目。
		adapter.Printf("%s", msg)
	}

	if after := proxyLogLimiter.snapshotKeyCount(); after != before {
		t.Fatalf("benign handshake rejection leaked into rate limiter: before=%d after=%d (messages should be suppressed before ShouldLog)", before, after)
	}

	// 同一连接 ID 的非证书握手错误仍应进入限频器（确认抑制分支足够精确）。
	adapter.Printf("[19999] WARN: Cannot handshake client api3.cursor.sh:443 remote error: EOF")
	if proxyLogLimiter.snapshotKeyCount() == before {
		t.Fatal("non-certificate handshake error was suppressed; suppression branch is too broad")
	}
}

// snapshotKeyCount 返回当前限频器中的去重 key 数量，供测试断言「是否产生新日志 key」。
func (limiter *logLimiter) snapshotKeyCount() int {
	if limiter == nil {
		return 0
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	return len(limiter.entries)
}

type fakeMirrorConfig struct {
	enabled bool
	hosts   []string
}

func (f *fakeMirrorConfig) MirrorCaptureEnabled(ctx context.Context) bool { return f.enabled }
func (f *fakeMirrorConfig) MirrorCaptureHosts() []string                  { return f.hosts }

func TestMirrorHostMatching(t *testing.T) {
	s := &ProxyServer{mirrorConfig: &fakeMirrorConfig{enabled: true, hosts: []string{"api.openai.com"}}}
	if !s.isMirrorHost("api.openai.com") {
		t.Fatal("exact host should match")
	}
	if !s.isMirrorHost("api.openai.com:443") {
		t.Fatal("host with port should match")
	}
	if s.isMirrorHost("api.anthropic.com") {
		t.Fatal("non-listed host should not match")
	}
	if s.mirrorEnabledForHost("api.openai.com") {
		t.Fatal("mirrorRec is nil, must be disabled")
	}
	s.mirrorRec = newMirrorRecorder(t.TempDir())
	if !s.mirrorEnabledForHost("api.openai.com") {
		t.Fatal("enabled+listed+recorder should be active")
	}
	s.mirrorConfig = &fakeMirrorConfig{enabled: false, hosts: []string{"api.openai.com"}}
	if s.mirrorEnabledForHost("api.openai.com") {
		t.Fatal("disabled config must disable mirroring")
	}
}
