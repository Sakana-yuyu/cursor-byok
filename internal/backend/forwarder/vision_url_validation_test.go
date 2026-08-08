package forwarder

import (
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestValidateImageURLSchemeAndShape(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "https allowed", raw: "https://example.com/a.png"},
		{name: "http allowed", raw: "http://example.com/a.png"},
		{name: "ftp rejected", raw: "ftp://example.com/a.png", wantErr: true},
		{name: "file rejected", raw: "file:///etc/passwd", wantErr: true},
		{name: "empty scheme rejected", raw: "example.com/a.png", wantErr: true},
		{name: "data url rejected", raw: "data:image/png;base64,AAAA", wantErr: true},
		{name: "userinfo rejected", raw: "https://user:pass@example.com/a.png", wantErr: true},
		{name: "empty host rejected", raw: "https:///a.png", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateImageURL(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateImageURL(%q) error = %v, wantErr %t", tt.raw, err, tt.wantErr)
			}
		})
	}
}

func TestValidateImageURLRejectsPrivateAndReservedHosts(t *testing.T) {
	// 裸 IP 直接判定；域名不做预解析（由连接层复检）。
	tests := []struct {
		name string
		raw  string
	}{
		{name: "loopback ipv4", raw: "http://127.0.0.1:8080/img.png"},
		{name: "loopback ipv6", raw: "http://[::1]/img.png"},
		{name: "private 10", raw: "http://10.0.0.5/img.png"},
		{name: "private 192.168", raw: "http://192.168.1.10/img.png"},
		{name: "private 172.16", raw: "http://172.16.0.1/img.png"},
		{name: "link local", raw: "http://169.254.169.254/latest/meta-data/"},
		{name: "unspecified", raw: "http://0.0.0.0/img.png"},
		{name: "multicast", raw: "http://224.0.0.1/img.png"},
		{name: "public ipv4 allowed", raw: "http://8.8.8.8/img.png"},
		{name: "public ipv6 allowed", raw: "http://[2606:4700:4700::1111]/img.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := url.Parse(tt.raw)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.raw, err)
			}
			ip := net.ParseIP(strings.TrimSpace(parsed.Hostname()))
			if ip == nil {
				t.Fatalf("test case %q is not a bare-IP URL; adjust the test", tt.raw)
			}
			err = validateImageURL(tt.raw)
			private := isPrivateOrReservedIP(ip)
			if tt.name == "public ipv4 allowed" || tt.name == "public ipv6 allowed" {
				if private {
					t.Fatalf("isPrivateOrReservedIP(%v) = true, want false for %q", ip, tt.raw)
				}
				if err != nil {
					t.Fatalf("validateImageURL(%q) error = %v, want nil", tt.raw, err)
				}
				return
			}
			if !private {
				t.Fatalf("isPrivateOrReservedIP(%v) = false, want true for %q", ip, tt.raw)
			}
			if err == nil {
				t.Fatalf("validateImageURL(%q) error = nil, want rejection", tt.raw)
			}
			if !strings.Contains(err.Error(), "not allowed") && !strings.Contains(err.Error(), "private or reserved") {
				t.Fatalf("validateImageURL(%q) error = %v, want scheme/host rejection", tt.raw, err)
			}
		})
	}
}

func TestIsPrivateOrReservedIP(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.255.255.255", true},
		{"::1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"172.31.255.254", true},
		{"192.168.0.1", true},
		{"169.254.0.1", true},
		{"0.0.0.0", true},
		{"224.0.0.1", true},
		{"::ffff:127.0.0.1", true},
		{"::ffff:10.0.0.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
	}
	for _, tt := range cases {
		t.Run(tt.host, func(t *testing.T) {
			ip := net.ParseIP(tt.host)
			if ip == nil {
				t.Fatalf("ParseIP(%q) failed", tt.host)
			}
			if got := isPrivateOrReservedIP(ip); got != tt.want {
				t.Fatalf("isPrivateOrReservedIP(%q) = %t, want %t", tt.host, got, tt.want)
			}
		})
	}
}
