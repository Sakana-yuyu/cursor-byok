package mitm

import (
	"testing"
)

// TestNewProxyServerAllowsNilCertManager 验证 CA 材料不完整时（certManager=nil）
// 代理服务仍可创建（MITM 自动禁用），支撑应用降级启动。
func TestNewProxyServerAllowsNilCertManager(t *testing.T) {
	proxy, err := NewProxyServer("127.0.0.1:0", "http://127.0.0.1:1", "", "", nil)
	if err != nil {
		t.Fatalf("NewProxyServer with nil certManager: %v", err)
	}
	if proxy == nil {
		t.Fatal("NewProxyServer returned nil server")
	}
}