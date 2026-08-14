package netproxy

import (
	"net/url"
	"testing"
)

func TestJoinNoProxyDedupesAndSkipsEmpty(t *testing.T) {
	got := joinNoProxy("localhost,127.0.0.1", "127.0.0.1,example.com", " , ; <local> ")
	want := "localhost,127.0.0.1,127.0.0.1,example.com"
	if got != want {
		t.Fatalf("joinNoProxy() = %q, want %q", got, want)
	}
}

func TestRuleMatchesHost(t *testing.T) {
	tests := []struct {
		rule string
		host string
		want bool
	}{
		{rule: "localhost", host: "localhost", want: true},
		{rule: "*.example.com", host: "api.example.com", want: true},
		{rule: ".example.com", host: "sub.example.com", want: true},
		{rule: "other.test", host: "example.com", want: false},
	}
	for _, tt := range tests {
		if got := ruleMatchesHost(tt.rule, tt.host); got != tt.want {
			t.Fatalf("ruleMatchesHost(%q, %q) = %v, want %v", tt.rule, tt.host, got, tt.want)
		}
	}
}

func TestShouldBypassSystemProxySimpleHost(t *testing.T) {
	reqURL, err := url.Parse("http://intranet/path")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if !shouldBypassSystemProxy(reqURL, nil, true) {
		t.Fatal("expected simple hostname bypass with excludeSimple=true")
	}
}

func TestSanitizeProxyValue(t *testing.T) {
	if got := sanitizeProxyValue("127.0.0.1:8080"); got != "http://127.0.0.1:8080" {
		t.Fatalf("sanitizeProxyValue() = %q", got)
	}
	if got := sanitizeProxyValue("socks5://127.0.0.1:1080"); got != "socks5://127.0.0.1:1080" {
		t.Fatalf("sanitizeProxyValue(socks5) = %q", got)
	}
}
