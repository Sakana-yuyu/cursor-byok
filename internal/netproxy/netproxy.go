package netproxy

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"cursor/internal/logger"

	"golang.org/x/net/http/httpproxy"
)

const (
	proxyCacheTTL     = time.Second
	alwaysNoProxyList = "localhost,127.0.0.1,::1"
)

var (
	installOnce             sync.Once
	initialDefaultTransport = cloneDefaultTransport()
	defaultResolver         = &proxyResolver{}
	proxyTransports         sync.Map
)

// InstallDefaultTransport makes clients with a nil Transport use the same
// proxy resolution as clients created through this package.
func InstallDefaultTransport() {
	installOnce.Do(func() {
		http.DefaultTransport = NewTransport(nil)
		defaultResolver.logCurrentSnapshot("default transport installed")
	})
}

// NewHTTPClient creates an HTTP client that follows environment and OS proxy
// settings while preserving the caller's timeout choice.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: NewTransport(nil),
		Timeout:   timeout,
	}
}

// defaultResponseHeaderTimeout 是模型请求 HTTP 客户端等待响应头的兜底上限。
// LLM 流式首 token 在排队/思考阶段可能较久，取 10 分钟：既能容忍正常首包延迟，
// 又避免对端“已连接但永不响应”的悬挂请求无限占用连接（配合各 adapter 的
// ProviderStreamIdleTimeout 流式空闲看门狗形成双层兜底）。
const defaultResponseHeaderTimeout = 10 * time.Minute

// NewTransport clones the given transport and installs proxy resolution on it.
// When base is nil, it clones Go's original default transport.
func NewTransport(base *http.Transport) *http.Transport {
	transport := initialDefaultTransport.Clone()
	if base != nil {
		transport = base.Clone()
	}
	transport.Proxy = ProxyForRequest
	if transport.ResponseHeaderTimeout <= 0 {
		transport.ResponseHeaderTimeout = defaultResponseHeaderTimeout
	}
	proxyTransports.Store(transport, struct{}{})
	return transport
}

// ProxyForRequest resolves the proxy URL for a single request.
func ProxyForRequest(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil
	}
	return defaultResolver.proxyForURL(req.URL)
}

// CurrentStatus returns the latest proxy resolver snapshot without exposing
// proxy credentials.
func CurrentStatus() Status {
	return statusFromSnapshot(defaultResolver.currentSnapshot())
}

type proxyResolver struct {
	mu       sync.Mutex
	snapshot proxySnapshot
}

type proxySnapshot struct {
	expiresAt      time.Time
	source         string
	active         bool
	description    string
	key            string
	httpProxy      string
	httpsProxy     string
	proxyFunc      func(*url.URL) (*url.URL, error)
	systemBypass   []string
	excludeSimple  bool
	pacIgnored     bool
	loadErrMessage string
}

// Status is a sanitized summary of the proxy resolver's current decision.
type Status struct {
	Source           string `json:"source"`
	Active           bool   `json:"active"`
	UsingSystemProxy bool   `json:"usingSystemProxy"`
	UsingEnvProxy    bool   `json:"usingEnvProxy"`
	HTTPProxy        string `json:"httpProxy"`
	HTTPSProxy       string `json:"httpsProxy"`
	Description      string `json:"description"`
	PACIgnored       bool   `json:"pacIgnored"`
	LoadError        string `json:"loadError"`
}

type systemProxyConfig struct {
	Source         string
	HTTPProxy      string
	HTTPSProxy     string
	SOCKSProxy     string
	Bypass         []string
	ExcludeSimple  bool
	PACEnabled     bool
	PACURL         string
	LoadErrMessage string
}

func (resolver *proxyResolver) proxyForURL(reqURL *url.URL) (*url.URL, error) {
	snapshot := resolver.currentSnapshot()
	if snapshot.proxyFunc == nil {
		return nil, nil
	}
	if shouldBypassSystemProxy(reqURL, snapshot.systemBypass, snapshot.excludeSimple) {
		return nil, nil
	}
	return snapshot.proxyFunc(reqURL)
}

func (resolver *proxyResolver) currentSnapshot() proxySnapshot {
	now := time.Now()
	resolver.mu.Lock()
	defer resolver.mu.Unlock()

	if !resolver.snapshot.expiresAt.IsZero() && now.Before(resolver.snapshot.expiresAt) {
		return resolver.snapshot
	}
	next := buildProxySnapshot(now)
	if next.key != resolver.snapshot.key {
		logProxySnapshot(next)
		closeIdleProxyConnections()
	}
	resolver.snapshot = next
	return next
}

func (resolver *proxyResolver) logCurrentSnapshot(prefix string) {
	resolver.mu.Lock()
	next := buildProxySnapshot(time.Now())
	if prefix != "" {
		next.description = strings.TrimSpace(prefix + "; " + next.description)
	}
	logProxySnapshot(next)
	resolver.snapshot = next
	closeIdleProxyConnections()
	resolver.mu.Unlock()
}

func buildProxySnapshot(now time.Time) proxySnapshot {
	if cfg, ok := envProxyConfig(); ok {
		return snapshotFromConfig(now, "env", cfg, nil, false, false, "")
	}

	systemConfig := loadSystemProxyConfig()
	httpProxy := systemConfig.HTTPProxy
	httpsProxy := systemConfig.HTTPSProxy
	if systemConfig.SOCKSProxy != "" {
		if httpProxy == "" {
			httpProxy = systemConfig.SOCKSProxy
		}
		if httpsProxy == "" {
			httpsProxy = systemConfig.SOCKSProxy
		}
	}
	if httpProxy == "" && httpsProxy == "" {
		return proxySnapshot{
			expiresAt:      now.Add(proxyCacheTTL),
			source:         directSource(systemConfig),
			description:    directDescription(systemConfig),
			key:            directKey(systemConfig),
			pacIgnored:     systemConfig.PACEnabled,
			loadErrMessage: systemConfig.LoadErrMessage,
		}
	}

	cfg := httpproxy.Config{
		HTTPProxy:  httpProxy,
		HTTPSProxy: httpsProxy,
		NoProxy:    joinNoProxy(alwaysNoProxyList, envNoProxy(), strings.Join(systemConfig.Bypass, ",")),
	}
	return snapshotFromConfig(now, "system", cfg, systemConfig.Bypass, systemConfig.ExcludeSimple, systemConfig.PACEnabled, systemConfig.LoadErrMessage)
}

func snapshotFromConfig(now time.Time, source string, cfg httpproxy.Config, bypass []string, excludeSimple bool, pacIgnored bool, loadErr string) proxySnapshot {
	proxyFunc := cfg.ProxyFunc()
	httpDesc := sanitizeProxyValue(cfg.HTTPProxy)
	httpsDesc := sanitizeProxyValue(cfg.HTTPSProxy)
	active := httpDesc != "" || httpsDesc != ""
	description := fmt.Sprintf("source=%s http=%s https=%s", source, displayProxyDesc(httpDesc), displayProxyDesc(httpsDesc))
	if cfg.NoProxy != "" {
		description += " no_proxy=configured"
	}
	if excludeSimple {
		description += " exclude_simple=true"
	}
	if pacIgnored {
		description += " pac=ignored"
	}
	if loadErr != "" {
		description += " load_error=" + loadErr
	}
	return proxySnapshot{
		expiresAt:      now.Add(proxyCacheTTL),
		source:         source,
		active:         active,
		description:    description,
		key:            strings.Join([]string{source, httpDesc, httpsDesc, cfg.NoProxy, fmt.Sprint(excludeSimple), fmt.Sprint(pacIgnored), loadErr}, "|"),
		httpProxy:      httpDesc,
		httpsProxy:     httpsDesc,
		proxyFunc:      proxyFunc,
		systemBypass:   cleanBypassRules(bypass),
		excludeSimple:  excludeSimple,
		pacIgnored:     pacIgnored,
		loadErrMessage: loadErr,
	}
}

func envProxyConfig() (httpproxy.Config, bool) {
	allProxy := firstEnv("ALL_PROXY", "all_proxy")
	httpProxy := firstEnv("HTTP_PROXY", "http_proxy")
	httpsProxy := firstEnv("HTTPS_PROXY", "https_proxy")
	if httpProxy == "" {
		httpProxy = allProxy
	}
	if httpsProxy == "" {
		httpsProxy = allProxy
	}
	hasProxy := httpProxy != "" || httpsProxy != "" || allProxy != ""
	return httpproxy.Config{
		HTTPProxy:  httpProxy,
		HTTPSProxy: httpsProxy,
		NoProxy:    joinNoProxy(alwaysNoProxyList, envNoProxy()),
		CGI:        os.Getenv("REQUEST_METHOD") != "",
	}, hasProxy
}

func envNoProxy() string {
	return firstEnv("NO_PROXY", "no_proxy")
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func directSource(cfg systemProxyConfig) string {
	if cfg.PACEnabled {
		return "system-pac-ignored"
	}
	if cfg.LoadErrMessage != "" {
		return "direct"
	}
	return "none"
}

func directDescription(cfg systemProxyConfig) string {
	parts := []string{"source=direct"}
	if cfg.PACEnabled {
		parts = append(parts, "pac=ignored")
	}
	if cfg.LoadErrMessage != "" {
		parts = append(parts, "load_error="+cfg.LoadErrMessage)
	}
	return strings.Join(parts, " ")
}

func directKey(cfg systemProxyConfig) string {
	return strings.Join([]string{"direct", fmt.Sprint(cfg.PACEnabled), cfg.PACURL, cfg.LoadErrMessage}, "|")
}

func logProxySnapshot(snapshot proxySnapshot) {
	if snapshot.description == "" {
		snapshot.description = "source=direct"
	}
	logger.Infof("net proxy: %s", snapshot.description)
}

func statusFromSnapshot(snapshot proxySnapshot) Status {
	source := strings.TrimSpace(snapshot.source)
	if source == "" || source == "none" || source == "system-pac-ignored" {
		source = "direct"
	}
	return Status{
		Source:           source,
		Active:           snapshot.active,
		UsingSystemProxy: snapshot.active && snapshot.source == "system",
		UsingEnvProxy:    snapshot.active && snapshot.source == "env",
		HTTPProxy:        snapshot.httpProxy,
		HTTPSProxy:       snapshot.httpsProxy,
		Description:      snapshot.description,
		PACIgnored:       snapshot.pacIgnored,
		LoadError:        snapshot.loadErrMessage,
	}
}

func closeIdleProxyConnections() {
	proxyTransports.Range(func(key any, _ any) bool {
		if transport, ok := key.(*http.Transport); ok && transport != nil {
			transport.CloseIdleConnections()
		}
		return true
	})
}

func cloneDefaultTransport() *http.Transport {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
		transport.Proxy = nil
	}
	// 首字优化：agent 会对同一中转站高频重复请求，Go 默认 MaxIdleConnsPerHost=2
	// 会导致并发请求频繁重建 TCP/TLS 连接、拖慢首字。提高每主机空闲连接复用上限，
	// 让后续请求命中已建立的 keep-alive 连接，减少握手延迟。
	tuneConnectionPool(transport)
	return transport
}

// tuneConnectionPool 调优连接池以复用同一中转站的 keep-alive 连接，降低首字延迟。
func tuneConnectionPool(transport *http.Transport) {
	if transport == nil {
		return
	}
	if transport.MaxIdleConns < 256 {
		transport.MaxIdleConns = 256
	}
	// 每主机空闲连接上限：默认仅 2，提升到 32 以覆盖 agent 的并发/连续调用。
	transport.MaxIdleConnsPerHost = 32
	if transport.IdleConnTimeout <= 0 {
		transport.IdleConnTimeout = 90 * time.Second
	}
	if transport.TLSHandshakeTimeout <= 0 {
		transport.TLSHandshakeTimeout = 10 * time.Second
	}
	transport.ForceAttemptHTTP2 = true
}

func joinNoProxy(parts ...string) string {
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		for _, value := range strings.FieldsFunc(part, func(r rune) bool {
			return r == ',' || r == ';'
		}) {
			value = strings.TrimSpace(value)
			if value == "" || strings.EqualFold(value, "<local>") {
				continue
			}
			items = append(items, value)
		}
	}
	return strings.Join(items, ",")
}

func cleanBypassRules(rules []string) []string {
	cleaned := make([]string, 0, len(rules))
	for _, rule := range rules {
		for _, value := range strings.FieldsFunc(rule, func(r rune) bool {
			return r == ',' || r == ';'
		}) {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "" {
				continue
			}
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func shouldBypassSystemProxy(reqURL *url.URL, rules []string, excludeSimple bool) bool {
	if reqURL == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(reqURL.Hostname()), "."))
	if host == "" {
		return false
	}
	if excludeSimple && isSimpleHostname(host) {
		return true
	}
	for _, rule := range rules {
		if ruleMatchesHost(rule, host) {
			return true
		}
	}
	return false
}

func isSimpleHostname(host string) bool {
	if host == "" || strings.Contains(host, ".") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return false
	}
	return !strings.Contains(host, ":")
}

func ruleMatchesHost(rule string, host string) bool {
	rule = strings.TrimSpace(strings.TrimSuffix(rule, "."))
	if rule == "" {
		return false
	}
	if rule == "*" {
		return true
	}
	if strings.EqualFold(rule, "<local>") {
		return isSimpleHostname(host)
	}
	if strings.Contains(rule, "://") {
		if parsed, err := url.Parse(rule); err == nil {
			rule = strings.TrimSpace(parsed.Hostname())
		}
	}
	if strings.Contains(rule, ":") {
		if ruleHost, _, err := net.SplitHostPort(rule); err == nil {
			rule = ruleHost
		}
	}
	rule = strings.ToLower(strings.TrimSuffix(rule, "."))
	if strings.ContainsAny(rule, "*?") {
		matched, err := path.Match(rule, host)
		return err == nil && matched
	}
	if strings.HasPrefix(rule, ".") {
		return strings.HasSuffix(host, rule)
	}
	if strings.HasPrefix(rule, "*.") {
		return strings.HasSuffix(host, strings.TrimPrefix(rule, "*"))
	}
	return host == rule || strings.HasSuffix(host, "."+rule)
}

func sanitizeProxyValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		parsed, err = url.Parse("http://" + raw)
	}
	if err != nil || parsed == nil || parsed.Host == "" {
		return "invalid"
	}
	scheme := strings.TrimSpace(parsed.Scheme)
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + parsed.Host
}

func displayProxyDesc(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

func normalizeProxyAddress(defaultScheme string, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	if defaultScheme == "" {
		defaultScheme = "http"
	}
	return defaultScheme + "://" + raw
}
