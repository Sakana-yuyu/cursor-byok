package forwarder

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
)

// imageURLFetchOptions 允许测试注入解析器与传输层，避免真实 DNS/网络访问。
type imageURLFetchOptions struct {
	// lookupHost 解析主机名到 IP；默认 net.DefaultResolver.LookupIPAddr。
	lookupHost func(ctx context.Context, host string) ([]net.IPAddr, error)
	// transport 实际发起请求；默认 http.DefaultTransport 克隆。
	transport http.RoundTripper
	// timeout 覆盖默认 visionProxyCallTimeout。
	timeout time.Duration
}

// fetchImageURL 只允许 http/https，且目标主机解析后不得落在私网/保留地址；
// 重定向每一跳都重新校验。限制响应大小并保留原超时语义。
func fetchImageURL(rawURL string) ([]byte, string, error) {
	return fetchImageURLWithOptions(rawURL, imageURLFetchOptions{})
}

func fetchImageURLWithOptions(rawURL string, options imageURLFetchOptions) ([]byte, string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, "", fmt.Errorf("fetch image url failed: empty url")
	}
	timeout := options.timeout
	if timeout <= 0 {
		timeout = visionProxyCallTimeout
	}
	lookupHost := options.lookupHost
	if lookupHost == nil {
		lookupHost = func(ctx context.Context, host string) ([]net.IPAddr, error) {
			return net.DefaultResolver.LookupIPAddr(ctx, host)
		}
	}
	transport := options.transport
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// 逐跳复检在 CheckRedirect 内完成：任何一跳指向非公开地址即中止。
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("fetch image url failed: too many redirects")
			}
			if err := validateImageURLTarget(request.URL, lookupHost); err != nil {
				return err
			}
			return nil
		},
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("fetch image url failed: %w", err)
	}
	if err := validateImageURLTargetWithLiteralLookup(parsed, lookupHost, options.lookupHost != nil); err != nil {
		return nil, "", err
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("fetch image url failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("fetch image url failed: status %s", resp.Status)
	}
	data, err := readImageBodyLimited(resp.Body, 20*1024*1024)
	if err != nil {
		return nil, "", fmt.Errorf("read image url body failed: %w", err)
	}
	mediaType := modeladapter.NormalizeImageMIMEType("", rawURL, data)
	return data, mediaType, nil
}

// validateImageURLTarget 校验单个 URL 目标：scheme 白名单 + 主机名解析后的地址集
// 不得包含私网/保留地址。localhost 类主机名直接拒绝（解析结果通常是回环）。
func validateImageURLTarget(target *url.URL, lookupHost func(ctx context.Context, host string) ([]net.IPAddr, error)) error {
	return validateImageURLTargetWithLiteralLookup(target, lookupHost, false)
}

func validateImageURLTargetWithLiteralLookup(target *url.URL, lookupHost func(ctx context.Context, host string) ([]net.IPAddr, error), resolveLiteral bool) error {
	if target == nil {
		return fmt.Errorf("fetch image url failed: nil target")
	}
	scheme := strings.ToLower(target.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("fetch image url failed: unsupported scheme %q", target.Scheme)
	}
	host := strings.TrimSpace(target.Hostname())
	if host == "" {
		return fmt.Errorf("fetch image url failed: missing host")
	}
	// 快速拒绝常见回环主机名（IPv6 字面量已在括号内由 Hostname 剥离）。
	lowerHost := strings.ToLower(host)
	if lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".localhost") ||
		lowerHost == "metadata.google.internal" || strings.HasSuffix(lowerHost, ".internal") {
		return fmt.Errorf("fetch image url failed: host %q is not allowed", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		// 链路本地地址可直达云元数据服务，不能因测试注入的解析器而
		// 放行；生产路径对所有裸 IP 都执行同一拒绝逻辑。
		if !resolveLiteral || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			if !isPublicImageIP(ip) {
				return fmt.Errorf("fetch image url failed: address %q is not public", host)
			}
		}
		if !resolveLiteral {
			return nil
		}
	}
	// 域名（以及测试显式要求模拟解析时的裸 IP）：解析后必须所有地址
	// 都公开（任一私网地址都拒绝，防 DNS 重绑定）。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	addresses, err := lookupHost(ctx, host)
	if err != nil {
		return fmt.Errorf("fetch image url failed: resolve %q: %w", host, err)
	}
	if len(addresses) == 0 {
		return fmt.Errorf("fetch image url failed: no addresses for %q", host)
	}
	for _, addr := range addresses {
		ip := addr.IP
		if ip == nil {
			// 解析条目缺失可用 IP 视为不可信：与"无地址"分支对齐，
			// 不能静默放行（否则任一私网地址可能借解析器异常绕过校验）。
			return fmt.Errorf("fetch image url failed: no usable address for %q", host)
		}
		if !isPublicImageIP(ip) {
			return fmt.Errorf("fetch image url failed: address %q is not public", ip.String())
		}
	}
	return nil
}

// isPublicImageIP 判定 IP 是否为允许对外访问的公开地址。
func isPublicImageIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return ipv4.IsGlobalUnicast() && !ipv4.IsPrivate() &&
			!ipv4.IsLoopback() && !ipv4.IsLinkLocalUnicast() &&
			!ipv4.IsLinkLocalMulticast() && !ipv4.IsMulticast() && !ipv4.IsUnspecified()
	}
	// Go 的 net.IP.IsPrivate 在部分 Go 版本中不会把 IPv6 ULA (fc00::/7)
	// 视为私网，因此在此显式拒绝。
	if len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc {
		return false
	}
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified() &&
		// 拒绝 IPv4 映射到 IPv6 的形式（::ffff:10.0.0.1），To4 已处理，此处兜底。
		!isIPv4Mapped(ip)
}

// isIPv4Mapped 判断 IPv6 地址是否由 IPv4 映射而来。
func isIPv4Mapped(ip net.IP) bool {
	if ip == nil || len(ip) != net.IPv6len {
		return false
	}
	for i := 0; i < 10; i++ {
		if ip[i] != 0 {
			return false
		}
	}
	return ip[10] == 0xff && ip[11] == 0xff
}

// readImageBodyLimited 读取最多 limit 字节后停止，避免超大响应打爆内存。
func readImageBodyLimited(reader io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(reader, limit))
}
