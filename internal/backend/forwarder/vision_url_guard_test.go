package forwarder

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestValidateImageURLTargetSchemeAndHost(t *testing.T) {
	lookup := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	tests := []struct {
		name    string
		rawURL  string
		wantErr string
	}{
		{name: "https allowed", rawURL: "https://example.com/a.png"},
		{name: "http allowed", rawURL: "http://example.com/a.png"},
		{name: "ftp rejected", rawURL: "ftp://example.com/a.png", wantErr: "unsupported scheme"},
		{name: "file rejected", rawURL: "file:///etc/passwd", wantErr: "unsupported scheme"},
		{name: "data rejected", rawURL: "data:image/png;base64,AAAA", wantErr: "unsupported scheme"},
		{name: "missing host rejected", rawURL: "https:///a.png", wantErr: "missing host"},
		{name: "localhost rejected", rawURL: "http://localhost/a.png", wantErr: "not allowed"},
		{name: "sub localhost rejected", rawURL: "http://foo.localhost/a.png", wantErr: "not allowed"},
		{name: "internal tld rejected", rawURL: "http://x.internal/a.png", wantErr: "not allowed"},
		{name: "metadata rejected", rawURL: "http://metadata.google.internal/computeMetadata/v1/", wantErr: "not allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatalf("url.Parse(%q) error = %v", tt.rawURL, err)
			}
			err = validateImageURLTarget(parsed, lookup)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateImageURLTarget(%q) error = %v, want nil", tt.rawURL, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateImageURLTarget(%q) error = %v, want containing %q", tt.rawURL, err, tt.wantErr)
			}
		})
	}
}

func TestValidateImageURLTargetIPAddresses(t *testing.T) {
	lookup := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return nil, nil // 不被调用：IP 字面量不走解析
	}
	tests := []struct {
		name    string
		rawURL  string
		wantErr string
	}{
		{name: "public ipv4 allowed", rawURL: "https://93.184.216.34/a.png"},
		{name: "loopback ipv4 rejected", rawURL: "https://127.0.0.1/a.png", wantErr: "not public"},
		{name: "private ipv4 rejected", rawURL: "https://10.0.0.1/a.png", wantErr: "not public"},
		{name: "private 172.16 rejected", rawURL: "https://172.16.5.5/a.png", wantErr: "not public"},
		{name: "private 192.168 rejected", rawURL: "https://192.168.1.1/a.png", wantErr: "not public"},
		{name: "link local rejected", rawURL: "https://169.254.169.254/a.png", wantErr: "not public"},
		{name: "unspecified rejected", rawURL: "https://0.0.0.0/a.png", wantErr: "not public"},
		{name: "multicast rejected", rawURL: "https://224.0.0.1/a.png", wantErr: "not public"},
		{name: "ipv6 loopback rejected", rawURL: "https://[::1]/a.png", wantErr: "not public"},
		{name: "ipv6 private rejected", rawURL: "https://[fd00::1]/a.png", wantErr: "not public"},
		{name: "ipv6 link local rejected", rawURL: "https://[fe80::1]/a.png", wantErr: "not public"},
		{name: "ipv4 mapped rejected", rawURL: "https://[::ffff:10.0.0.1]/a.png", wantErr: "not public"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatalf("url.Parse(%q) error = %v", tt.rawURL, err)
			}
			err = validateImageURLTarget(parsed, lookup)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateImageURLTarget(%q) error = %v, want nil", tt.rawURL, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateImageURLTarget(%q) error = %v, want containing %q", tt.rawURL, err, tt.wantErr)
			}
		})
	}
}

func TestValidateImageURLTargetDNSRebinding(t *testing.T) {
	// 域名解析出私网地址：必须拒绝（防 DNS 重绑定）。
	lookup := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if host == "evil.example.com" {
			return []net.IPAddr{{IP: net.ParseIP("10.0.0.7")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	parsed, err := url.Parse("https://evil.example.com/a.png")
	if err != nil {
		t.Fatalf("url.Parse error = %v", err)
	}
	err = validateImageURLTarget(parsed, lookup)
	if err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("validateImageURLTarget(evil.example.com) error = %v, want private address rejection", err)
	}
}

func TestFetchImageURLWithOptionsRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("fake-png-bytes"))
	}))
	defer server.Close()

	// httptest server 监听 127.0.0.1，必须注入"视为公开"的 resolver 才能通过校验。
	lookup := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	data, mediaType, err := fetchImageURLWithOptions(server.URL+"/a.png", imageURLFetchOptions{
		lookupHost: lookup,
	})
	if err != nil {
		t.Fatalf("fetchImageURLWithOptions() error = %v", err)
	}
	if string(data) != "fake-png-bytes" {
		t.Fatalf("data = %q, want fake-png-bytes", data)
	}
	if mediaType != "image/png" {
		t.Fatalf("mediaType = %q, want image/png", mediaType)
	}
}

func TestFetchImageURLWithOptionsRedirectToPrivate(t *testing.T) {
	// 重定向到 127.0.0.1 的回环地址：CheckRedirect 内应拦截。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:9/private", http.StatusFound)
	}))
	defer server.Close()

	lookup := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	_, _, err := fetchImageURLWithOptions(server.URL+"/start.png", imageURLFetchOptions{
		lookupHost: lookup,
	})
	if err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("redirect to private address error = %v, want not public rejection", err)
	}
}

func TestFetchImageURLWithOptionsRejectsPrivateLiteral(t *testing.T) {
	lookup := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return nil, nil
	}
	_, _, err := fetchImageURLWithOptions("http://169.254.169.254/latest/meta-data/", imageURLFetchOptions{
		lookupHost: lookup,
	})
	if err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("private literal error = %v, want not public rejection", err)
	}
}
