package interaction

import "testing"

func TestIsBlockedWebFetchHostCoversCGNATAndPrivate(t *testing.T) {
	blocked := []string{"localhost", "127.0.0.1", "10.1.2.3", "192.168.0.1", "100.64.0.1", "100.127.255.254", "[::1]", "169.254.1.1"}
	for _, host := range blocked {
		if !isBlockedWebFetchHost(host) {
			t.Errorf("isBlockedWebFetchHost(%q) = false, want true", host)
		}
	}
	allowed := []string{"8.8.8.8", "100.128.0.1", "example.com"}
	for _, host := range allowed {
		if isBlockedWebFetchHost(host) {
			t.Errorf("isBlockedWebFetchHost(%q) = true, want false", host)
		}
	}
}

func TestValidateWebFetchURLRejectsHostnameResolvingToLoopback(t *testing.T) {
	// localhost 子域经系统解析器回落到环回地址；验证域名解析层的拦截生效。
	if _, err := validateWebFetchURL("http://127.0.0.1/"); err == nil {
		t.Fatal("validateWebFetchURL(loopback literal) error = nil, want blocked")
	}
	if !resolvesToBlockedWebFetchIP("localhost") {
		t.Skip("system resolver does not resolve localhost; skip resolution check")
	}
}
