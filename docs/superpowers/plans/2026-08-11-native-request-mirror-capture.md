# 官方请求镜像记录与对比 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 MITM 代理上对模型 API 域名（`api.openai.com` 等）实现"解密 → 记录请求/响应明文 → 直通官方"的镜像记录能力，产出 `official.raw.jsonl`，为后续与 `provider.jsonl` 的出站请求对比提供数据（第一阶段只做抓取层 + 记录层 + 配置，分析修复与沉淀工具在后续阶段）。

**Architecture:** 复用现有 goproxy MITM（只劫持 `*.cursor.sh` relay 白名单）扩展"镜像记录"：`HandleConnect` 对镜像域名同样走 `ConnectMitm` 解密；`DoFunc` 在直通官方前把请求体复制一份写入记录器并重建 `req.Body`；新增 `OnResponse` 钩子把响应体 tee 一份逐 chunk 记录。镜像记录是旁路，任何记录失败只记日志、绝不阻断官方链路。配置新增 `config.yaml mirrorCapture` 字段（默认关闭），经 `serverconfig.Manager` 热加载，由 `NewProxyServer` 以接口注入。

**Tech Stack:** Go 1.25、`github.com/elazarl/goproxy v1.7.2`（`OnResponse().DoFunc` 在 MITM 路径同样触发，见 https.go:253/380 `filterResponse`）。

## Global Constraints

- 镜像记录默认关闭（`MirrorCapture.Enabled=false`）；`hosts` 为空时回落 `DefaultMirrorHosts`。
- 镜像记录是旁路：写入/截断/打开失败只 `logger.Errorf`，不得返回错误、不得阻断请求直通、不得影响 relay（`*.cursor.sh`）与非镜像域名的现有行为。
- 敏感头（`authorization`、`proxy-authorization`、`x-api-key`、`cookie`、`set-cookie`）记录时一律替换为 `[REDACTED]`。
- body 记录上限 `mirrorBodyMaxBytes = 128 * 1024`，超长截断并标记 `truncated: true`；截断在记录端做，不改原始请求语义。
- 记录文件路径固定为 `<historyRoot>/_debug/mirror/official.raw.jsonl`（追加写）。注意：官方直连请求（OpenAI/Anthropic JSON）**不含 conversationId**，故不使用 `history/<conversationId>/debug/` 目录。
- `NewProxyServer` 签名从 5 参数改为 6 参数：`(addr, baseURL, historyRoot string, mirror MirrorCaptureConfig, certManager *certs.Manager)`；`mirror` 可为 `nil`（不启用镜像记录）。
- 不引入新依赖；`mitm` 新增对 `cursor/internal/backend/server/config` 的依赖（无循环：config 包不依赖 mitm）。

---

### Task 1: config 层 — `mirrorCapture` 字段与接口方法

**Files:**
- Modify: `internal/backend/server/config/types.go`（`Config` struct 与 `DefaultConfig`）
- Modify: `internal/backend/server/config/manager.go`（两个接口方法）
- Test: `internal/backend/server/config/types_test.go`（新增用例，参考现有文件同包风格）

**Interfaces:**
- Produces: `config.Config.MirrorCapture` 字段；`config.DefaultMirrorHosts` 包级变量；`(*config.Manager).MirrorCaptureEnabled(ctx context.Context) bool` 与 `(*config.Manager).MirrorCaptureHosts() []string` —— 这两个方法共同满足 Task 3 定义的 `mitm.MirrorCaptureConfig` 接口。

- [ ] **Step 1: 写失败测试**

在 `internal/backend/server/config/types_test.go` 末尾追加：

```go
func TestDefaultConfigMirrorCaptureDisabledByDefault(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MirrorCapture.Enabled {
		t.Fatal("mirrorCapture must default to disabled")
	}
	if len(cfg.MirrorCapture.Hosts) != 3 {
		t.Fatalf("default mirror hosts = %d, want 3 (openai/anthropic/gemini)", len(cfg.MirrorCapture.Hosts))
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/backend/server/config/ -run TestDefaultConfigMirrorCaptureDisabledByDefault -v`
Expected: FAIL（`cfg.MirrorCapture` 不存在，编译错误）。

- [ ] **Step 3: 实现**

在 `internal/backend/server/config/types.go` 的 `Config` struct（约 :147）内、`Log` 字段后新增：

```go
	// MirrorCapture 控制 MITM 镜像记录：对模型 API 域名解密后记录请求/响应明文并直通官方。
	// 默认关闭；开启后仅记录不阻断，官方链路不受影响。
	MirrorCapture MirrorCaptureConfig `json:"mirrorCapture" yaml:"mirrorCapture"`
```

在 `types.go` 中 `Config` struct 之前新增类型定义：

```go
// MirrorCaptureConfig 是镜像记录配置。
type MirrorCaptureConfig struct {
	Enabled bool     `json:"enabled" yaml:"enabled"`
	Hosts   []string `json:"hosts,omitempty" yaml:"hosts,omitempty"`
}

// DefaultMirrorHosts 是 Cursor 官方 key 直连模式使用的模型 API 入口；Hosts 为空时回落。
var DefaultMirrorHosts = []string{
	"api.openai.com",
	"api.anthropic.com",
	"generativelanguage.googleapis.com",
}
```

在 `DefaultConfig()`（约 :169）的返回值中、`Log: false,` 后新增：

```go
		MirrorCapture: MirrorCaptureConfig{Enabled: false, Hosts: DefaultMirrorHosts},
```

在 `internal/backend/server/config/manager.go` 的 `DebugLogMaxBytes` 方法（约 :436）之后新增：

```go
// MirrorCaptureEnabled 返回镜像记录开关（热加载生效）。
func (manager *Manager) MirrorCaptureEnabled(ctx context.Context) bool {
	if manager == nil {
		return false
	}
	manager.reloadIfChanged(ctx)
	return manager.currentConfig().MirrorCapture.Enabled
}

// MirrorCaptureHosts 返回镜像记录域名列表；空配置回落默认列表。
func (manager *Manager) MirrorCaptureHosts() []string {
	if manager == nil {
		return nil
	}
	hosts := manager.currentConfig().MirrorCapture.Hosts
	if len(hosts) == 0 {
		return DefaultMirrorHosts
	}
	return hosts
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/backend/server/config/`
Expected: PASS（含既有用例）。

- [ ] **Step 5: 提交**

```bash
git add internal/backend/server/config/types.go internal/backend/server/config/manager.go internal/backend/server/config/types_test.go
git commit -m "feat(config): mirrorCapture 配置字段与热加载接口"
```

---

### Task 2: `internal/mitm/mirror.go` — 镜像记录器

**Files:**
- Create: `internal/mitm/mirror.go`
- Create: `internal/mitm/mirror_test.go`

**Interfaces:**
- Consumes: Task 1 的 `config.Manager`（实现本文件定义的 `MirrorCaptureConfig` 接口）；`logger.Errorf`；`requestURL(req)` 与 `hostFromHTTPRequest(req)`（已存在于 `internal/mitm/service.go`）。
- Produces: `MirrorCaptureConfig` 接口；`newMirrorRecorder(historyRoot string) *mirrorRecorder`；`(*mirrorRecorder).recordRequest(host string, req *http.Request)`（内部重建 `req.Body` 供直通）；`(*mirrorRecorder).recordResponseChunk(host string, chunk []byte)`；`sanitizeHeaders(h http.Header) map[string]string`；常量 `mirrorLogSubdir`/`mirrorLogFilename`/`mirrorBodyMaxBytes`。

- [ ] **Step 1: 写失败测试**

创建 `internal/mitm/mirror_test.go`：

```go
package mitm

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMirrorRecorderRequestSanitizesAndTruncates(t *testing.T) {
	rec := newMirrorRecorder(t.TempDir())
	body := strings.Repeat("x", mirrorBodyMaxBytes+1024)
	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer sk-secret")
	req.Header.Set("X-Api-Key", "sk-secret")
	req.Header.Set("Content-Type", "application/json")

	rec.recordRequest("api.openai.com", req)

	// 直通语义：req.Body 必须被重建且可读、内容完整（记录端截断不影响上游）。
	rebuilt, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(rebuilt) != len(body) {
		t.Fatalf("rebuilt body len = %d, want %d", len(rebuilt), len(body))
	}

	raw, err := os.ReadFile(filepath.Join(t.TempDir())) // placeholder replaced below
	_ = raw
	_ = err
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/mitm/ -run TestMirrorRecorderRequestSanitizesAndTruncates -v`
Expected: FAIL（`newMirrorRecorder` 未定义，编译错误）。

- [ ] **Step 3: 实现 mirror.go**

创建 `internal/mitm/mirror.go`：

```go
package mitm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cursor/internal/logger"
)

// MirrorCaptureConfig 提供镜像记录所需的配置。
type MirrorCaptureConfig interface {
	MirrorCaptureEnabled(ctx context.Context) bool
	MirrorCaptureHosts() []string
}

const (
	mirrorLogSubdir    = "_debug/mirror"
	mirrorLogFilename  = "official.raw.jsonl"
	mirrorBodyMaxBytes = 128 * 1024
)

// mirrorSensitiveHeaders 记录时一律抹掉的敏感头。
var mirrorSensitiveHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"x-api-key":           true,
	"cookie":              true,
	"set-cookie":          true,
}

// mirrorRecord 是 official.raw.jsonl 的一行。
type mirrorRecord struct {
	TS        time.Time         `json:"ts"`
	Host      string            `json:"host"`
	Method    string            `json:"method,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
}

// mirrorRecorder 把镜像请求/响应追加写入 <historyRoot>/_debug/mirror/official.raw.jsonl。
// 记录失败只记日志，绝不阻断代理直通。
type mirrorRecorder struct {
	historyRoot string
	mu          sync.Mutex
	file        *os.File
}

func newMirrorRecorder(historyRoot string) *mirrorRecorder {
	return &mirrorRecorder{historyRoot: historyRoot}
}

func (r *mirrorRecorder) ensureFile() error {
	if r == nil || r.historyRoot == "" {
		return nil
	}
	if r.file != nil {
		return nil
	}
	dir := filepath.Join(r.historyRoot, mirrorLogSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, mirrorLogFilename), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	r.file = f
	return nil
}

// recordRequest 记录一次镜像请求；读出的 body 会重建回 req.Body 供直通。
func (r *mirrorRecorder) recordRequest(host string, req *http.Request) {
	if r == nil || req == nil {
		return
	}
	rec := mirrorRecord{TS: time.Now(), Host: host, Method: req.Method, URL: requestURL(req), Headers: sanitizeHeaders(req.Header)}
	if req.Body != nil {
		body, _ := io.ReadAll(io.LimitReader(req.Body, mirrorBodyMaxBytes+1))
		if len(body) > mirrorBodyMaxBytes {
			body = body[:mirrorBodyMaxBytes]
			rec.Truncated = true
		}
		rec.Body = string(body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	r.write(rec)
}

// recordResponseChunk 记录一次镜像响应的一个流式 chunk。
func (r *mirrorRecorder) recordResponseChunk(host string, chunk []byte) {
	if r == nil || len(chunk) == 0 {
		return
	}
	r.write(mirrorRecord{TS: time.Now(), Host: host, Body: string(chunk)})
}

func (r *mirrorRecorder) write(rec mirrorRecord) {
	line, err := json.Marshal(rec)
	if err != nil {
		logger.Errorf("mirror record marshal failed: %v", err)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureFile(); err != nil {
		logger.Errorf("mirror record open failed: %v", err)
		return
	}
	if _, err := r.file.Write(append(line, '\n')); err != nil {
		logger.Errorf("mirror record write failed: %v", err)
	}
}

func sanitizeHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		if mirrorSensitiveHeaders[strings.ToLower(k)] {
			out[k] = "[REDACTED]"
			continue
		}
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}
```

> 注：`requestURL(req)` 已存在于 `service.go`（约 :422 使用），返回请求 URL 字符串。

- [ ] **Step 4: 补全测试并跑通**

把 Step 1 测试中 `placeholder replaced below` 两行替换为真实断言：

```go
	dir := filepath.Join(rec.historyRoot, mirrorLogSubdir)
	raw, err := os.ReadFile(filepath.Join(dir, mirrorLogFilename))
	if err != nil {
		t.Fatalf("read mirror log: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, `"authorization":"[REDACTED]"`) || !strings.Contains(content, `"x-api-key":"[REDACTED]"`) {
		t.Fatalf("sensitive headers not redacted: %s", content)
	}
	if !strings.Contains(content, `"truncated":true`) {
		t.Fatalf("truncated flag missing: %s", content)
	}
	if !strings.Contains(content, "api.openai.com") {
		t.Fatalf("host missing: %s", content)
	}
```

再追加响应 chunk 测试：

```go
func TestMirrorRecorderResponseChunksAppend(t *testing.T) {
	rec := newMirrorRecorder(t.TempDir())
	rec.recordResponseChunk("api.anthropic.com", []byte(`data: {"type":"content_block_delta"}`))
	rec.recordResponseChunk("api.anthropic.com", []byte("\n"))
	raw, err := os.ReadFile(filepath.Join(rec.historyRoot, mirrorLogSubdir, mirrorLogFilename))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
}
```

Run: `go test ./internal/mitm/ -run 'TestMirrorRecorder' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/mitm/mirror.go internal/mitm/mirror_test.go
git commit -m "feat(mitm): 镜像记录器（脱敏/截断/追加写 official.raw.jsonl）"
```

---

### Task 3: `internal/mitm/service.go` — MITM 镜像接线

**Files:**
- Modify: `internal/mitm/service.go`
- Modify: `internal/mitm/service_test.go`（更新 `NewProxyServer` 调用）

**Interfaces:**
- Consumes: Task 2 的 `MirrorCaptureConfig`、`newMirrorRecorder`、`mirrorRecorder`、`mirrorBodyMaxBytes`；现有 `isWhitelistedRelayHost`、`hostFromHTTPRequest`、`normalizeConnectHost`。
- Produces: `NewProxyServer(addr, baseURL, historyRoot string, mirror MirrorCaptureConfig, certManager *certs.Manager) (*ProxyServer, error)`；`(*ProxyServer).isMirrorHost(host string) bool`；`(*ProxyServer).mirrorEnabledForHost(host string) bool`；`(*ProxyServer).recordMirrorRequest(req *http.Request)`；`(*ProxyServer).wrapMirrorResponse(host string, body io.ReadCloser) io.ReadCloser`。Task 4 依赖此签名。

- [ ] **Step 1: 写失败测试**

在 `internal/mitm/service_test.go` 追加：

```go
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
```

同时把既有 `TestNewProxyServerAllowsNilCertManager`（service_test.go:10）的调用改为新签名：

```go
	proxy, err := NewProxyServer("127.0.0.1:0", "http://127.0.0.1:1", t.TempDir(), nil, nil)
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/mitm/ -run 'TestMirrorHostMatching|TestNewProxyServerAllowsNilCertManager' -v`
Expected: FAIL（`mirrorConfig`/`mirrorRec` 字段与 `NewProxyServer` 新签名不存在）。

- [ ] **Step 3: 实现**

在 `internal/mitm/service.go`：

(a) 在 `ProxyServer` struct（约 :34-58）末尾加字段：

```go
	// mirrorConfig 提供镜像记录配置；nil 表示不启用。
	mirrorConfig MirrorCaptureConfig
	// mirrorRec 负责把镜像请求/响应写入 official.raw.jsonl；nil 表示不可用。
	mirrorRec *mirrorRecorder
	// mirrorMu 保护 mirrorConfig/mirrorRec 的并发替换。
	mirrorMu sync.RWMutex
```

(b) 修改 `NewProxyServer` 签名与初始化（约 :182）：

```go
func NewProxyServer(addr, baseURL, historyRoot string, mirror MirrorCaptureConfig, certManager *certs.Manager) (*ProxyServer, error) {
	// ...保留现有校验与初始化...
	s := &ProxyServer{
		addr:          addr,
		baseURL:       baseURL,
		certManager:   certManager,
		upstreamClient: netproxy.NewTransport(&http.Transport{...}),
		// ...保留现有字段...
		mirrorConfig: mirror,
	}
	if historyRoot != "" {
		s.mirrorRec = newMirrorRecorder(historyRoot)
	}
	// ...保留现有 proxy handler 组装...
```

> 实现时对照现有 `NewProxyServer` 函数体逐字段补齐，不要漏掉 `serveErrCh` 等既有字段的初始化。

(c) `HandleConnect` 回调（约 :406-411）改为：

```go
	proxy.OnRequest().HandleConnect(goproxy.FuncHttpsHandler(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		if mitmAction == nil {
			return goproxy.OkConnect, host
		}
		if isWhitelistedRelayHost(host) || s.isMirrorHost(host) {
			return mitmAction, host
		}
		return goproxy.OkConnect, host
	}))
```

(d) `DoFunc`（约 :414-437）在 `isWhitelistedRelayHost` 分支之后、`return req, nil` 之前加镜像分支：

```go
		if s.mirrorEnabledForHost(host) {
			// 镜像记录：解密后记录明文，直通官方（不转发 backend）。
			s.recordMirrorRequest(req)
		}
		return req, nil
```

(e) 在 `DoFunc` 注册之后新增 OnResponse 钩子：

```go
	// 镜像记录响应流：SSE 逐 chunk tee 一份到记录器，客户端流不受影响。
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		if resp == nil || resp.Body == nil {
			return resp
		}
		host := ""
		if resp.Request != nil {
			host = hostFromHTTPRequest(resp.Request)
		}
		if s.mirrorEnabledForHost(host) {
			resp.Body = s.wrapMirrorResponse(host, resp.Body)
		}
		return resp
	})
```

(f) 在 `isWhitelistedRelayHost`（约 :769）附近新增方法：

```go
// isMirrorHost 判断 host（可含端口）是否命中镜像记录域名列表。
func (s *ProxyServer) isMirrorHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	for _, h := range s.mirrorHosts() {
		if h == host {
			return true
		}
	}
	return false
}

// mirrorHosts 返回当前镜像记录域名列表。
func (s *ProxyServer) mirrorHosts() []string {
	s.mirrorMu.RLock()
	defer s.mirrorMu.RUnlock()
	if s.mirrorConfig == nil {
		return nil
	}
	return s.mirrorConfig.MirrorCaptureHosts()
}

// mirrorEnabledForHost 判定镜像记录是否对该 host 生效（开关+列表+记录器三者齐备）。
func (s *ProxyServer) mirrorEnabledForHost(host string) bool {
	if s.mirrorConfig == nil || s.mirrorRec == nil {
		return false
	}
	if !s.mirrorConfig.MirrorCaptureEnabled(context.Background()) {
		return false
	}
	return s.isMirrorHost(host)
}

// recordMirrorRequest 记录一次镜像请求（内部重建 req.Body 供直通）。
func (s *ProxyServer) recordMirrorRequest(req *http.Request) {
	s.mirrorMu.RLock()
	defer s.mirrorMu.RUnlock()
	if s.mirrorRec == nil {
		return
	}
	s.mirrorRec.recordRequest(hostFromHTTPRequest(req), req)
}

// wrapMirrorResponse 包装响应体，使每个读出的 chunk 同时写入记录器。
func (s *ProxyServer) wrapMirrorResponse(host string, body io.ReadCloser) io.ReadCloser {
	s.mirrorMu.RLock()
	defer s.mirrorMu.RUnlock()
	if s.mirrorRec == nil {
		return body
	}
	return &mirrorTeeReadCloser{r: body, rec: s.mirrorRec, host: host}
}

// mirrorTeeReadCloser 读取时逐 chunk 记录。
type mirrorTeeReadCloser struct {
	r    io.ReadCloser
	rec  *mirrorRecorder
	host string
}

func (m *mirrorTeeReadCloser) Read(p []byte) (int, error) {
	n, err := m.r.Read(p)
	if n > 0 {
		m.rec.recordResponseChunk(m.host, p[:n])
	}
	return n, err
}

func (m *mirrorTeeReadCloser) Close() error { return m.r.Close() }
```

> `context` 若未在 `service.go` 引入则补 import；`io`/`strings` 应已存在（`forwardToServerStreaming` 用了 `io.Pipe`、`isWhitelistedRelayHost` 用了 `strings`）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/mitm/`
Expected: PASS（含既有用例与新用例）。

- [ ] **Step 5: 提交**

```bash
git add internal/mitm/service.go internal/mitm/service_test.go
git commit -m "feat(mitm): 镜像域名解密后记录明文并直通官方（OnResponse tee 响应流）"
```

---

### Task 4: 调用方接线 — runner 与 ProxyService

**Files:**
- Modify: `internal/app/runner.go`
- Modify: `internal/client/service.go`

**Interfaces:**
- Consumes: Task 1 的 `serverconfig.Manager` 方法；Task 3 的 `NewProxyServer` 新签名；`appdata.ConfigFilePath()`/`appdata.HistoryRootPath()`/`appdata.LogsRootPath()`（`internal/appdata/paths.go:32/:51/:82`）。

- [ ] **Step 1: runner.go 传配置**

在 `internal/app/runner.go`（约 :118，`defaultBackendBaseURL` 定义之后、`mitm.NewProxyServer` 调用之前）加：

```go
	mirrorCfg, _ := serverconfig.NewManager(runCtx, serverconfig.NewStore(appdata.ConfigFilePath(), appdata.LogsRootPath()))
```

> `runCtx` 用 runner 当前上下文变量名（实现时对照 `Run` 函数签名）；`appdata` 若未 import 则补 `"cursor/internal/appdata"`。

把 :119 改为：

```go
	proxyServer, err := mitm.NewProxyServer(serverconfig.DefaultProxyListenAddr, defaultBackendBaseURL, appdata.HistoryRootPath(), mirrorCfg, certManager)
```

- [ ] **Step 2: client/service.go 传配置**

(a) `ProxyService` struct（约 :35-67）加字段：

```go
	// configs 提供热加载配置（含镜像记录开关）。
	configs *serverconfig.Manager
```

(b) `NewProxyService`（约 :200 `service.store = serverconfig.NewStore(...)` 之后）加：

```go
	if m, err := serverconfig.NewManager(context.Background(), service.store); err == nil {
		service.configs = m
	}
```

(c) `ensureServer`（约 :264）改为：

```go
	proxyServer, err := mitm.NewProxyServer(listenAddr, baseURL, appdata.HistoryRootPath(), s.configs, s.certManager)
```

> `client/service.go` 已 import `appdata`（:13）与 `context`（:4）、`serverconfig`（:14）。

- [ ] **Step 3: 编译验证**

Run: `go build ./...`
Expected: 成功。

Run: `go test ./internal/client/ ./internal/app/ ./internal/mitm/ ./internal/backend/server/config/`
Expected: PASS。

- [ ] **Step 4: 提交**

```bash
git add internal/app/runner.go internal/client/service.go
git commit -m "feat: runner/ProxyService 注入 mirrorCapture 配置"
```

---

### Task 5: 文档修正与全量验证

**Files:**
- Modify: `docs/superpowers/specs/2026-08-11-native-request-mirror-capture-design.md`

- [ ] **Step 1: 修正设计文档的记录路径**

把设计文档中 `② 记录层` 一节的 `history/<conversationId>/debug/official.raw.jsonl` 改为 `history/_debug/mirror/official.raw.jsonl`，并补一句说明：官方直连请求（OpenAI/Anthropic JSON）不含 conversationId，故镜像记录独立组织，每条记录携带 ts/host/model 供人工按时间与 `provider.jsonl` 关联。

- [ ] **Step 2: 全量验证**

Run: `go test ./...`
Expected: PASS。

Run: `go vet ./...`
Expected: 无新告警。

Run: `git diff --check`
Expected: 无输出。

- [ ] **Step 3: 提交**

```bash
git add docs/superpowers/specs/2026-08-11-native-request-mirror-capture-design.md
git commit -m "docs: 镜像记录目录组织修正为 _debug/mirror"
```

---

## Self-Review 记录

- **Spec 覆盖**：① 抓取层 → Task 3（HandleConnect/DoFunc/OnResponse/isMirrorHost）；② 记录层 → Task 2（脱敏/截断/追加写）；③ 配置开关 → Task 1（`mirrorCapture` 字段 + Manager 热加载）与 Task 4（两处接线）；④ 一次性分析修复、⑤ 沉淀工具 → 明确列为后续阶段，不在本计划范围（规格已分交付）。设计文档中 `history/<conversationId>/debug/` 路径与"直连请求无 conversationId"矛盾 → Task 5 修正文档。错误处理要求"记录失败不阻断直通" → Task 2 `write` 只记日志、Task 3 `mirrorEnabledForHost` 在记录器/配置缺失时静默禁用。
- **占位符扫描**：无 TBD/TODO；所有代码步骤均含完整代码。Step 1 测试中的 placeholder 在 Step 4 显式替换。
- **类型一致性**：`MirrorCaptureConfig` 接口（Task 2 定义）与 `config.Manager`（Task 1 实现）签名一致；`NewProxyServer` 新签名在 Task 3 定义、Task 4 两处调用与 Task 3 测试使用一致；`mirrorRecorder`/`mirrorBodyMaxBytes` 跨 Task 2/3 名称一致。

## Execution Handoff

计划已保存。执行方式：**Subagent-Driven（推荐，每任务独立子代理 + 两阶段评审）** 或 **Inline（本会话内 executing-plans 批量执行 + 检查点）**。
