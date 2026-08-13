package mitm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMirrorRecorderRequestSanitizesAndTruncates(t *testing.T) {
	_ = context.Background() // context 导入保留：MirrorCaptureConfig 接口签名使用 context.Context
	rec := newMirrorRecorder(t.TempDir())
	defer rec.Close()
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
}

// TestMirrorRecorderRequestRedactsProviderCredentialHeaders 覆盖本仓库各 provider 协议实际
// 使用的鉴权头：gemini 原生的 x-goog-api-key、Azure OpenAI 风格的 api-key、new-api 网关的
// new-api-user（取值可能直接是 apiKey），以及 URL 上的 ?key=（Gemini 支持 query 传 key）。
func TestMirrorRecorderRequestRedactsProviderCredentialHeaders(t *testing.T) {
	rec := newMirrorRecorder(t.TempDir())
	defer rec.Close()
	req, err := http.NewRequest(
		http.MethodPost,
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse&key=AIzaSecret",
		strings.NewReader(`{"contents":[]}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-goog-api-key", "AIzaSecret")
	req.Header.Set("api-key", "azure-secret")
	req.Header.Set("New-Api-User", "secret-user-token")
	req.Header.Set("X-Auth-Token", "gateway-secret")
	req.Header.Set("Content-Type", "application/json")

	rec.recordRequest("generativelanguage.googleapis.com", req)

	raw, err := os.ReadFile(filepath.Join(rec.historyRoot, mirrorLogSubdir, mirrorLogFilename))
	if err != nil {
		t.Fatalf("read mirror log: %v", err)
	}
	content := string(raw)
	for _, header := range []string{"x-goog-api-key", "api-key", "new-api-user", "x-auth-token"} {
		if !strings.Contains(content, `"`+header+`":"[REDACTED]"`) {
			t.Fatalf("%s not redacted: %s", header, content)
		}
	}
	for _, secret := range []string{"AIzaSecret", "azure-secret", "secret-user-token", "gateway-secret"} {
		if strings.Contains(content, secret) {
			t.Fatalf("credential %q leaked into mirror log: %s", secret, content)
		}
	}
	if !strings.Contains(content, `"content-type":"application/json"`) {
		t.Fatalf("content-type must be preserved: %s", content)
	}
	if !strings.Contains(content, "alt=sse") {
		t.Fatalf("non-sensitive query must be preserved: %s", content)
	}
}

// TestIsMirrorSensitiveHeader 固定脱敏策略：已知鉴权头 + 名字自报家门的未知头一律抹掉，
// 同时保证排查抓包所依赖的诊断头（限流计数、鉴权挑战、幂等键）不被过度脱敏。
func TestIsMirrorSensitiveHeader(t *testing.T) {
	sensitive := []string{
		"Authorization",
		"Proxy-Authorization",
		"X-Api-Key",
		"x-goog-api-key",
		"api-key",
		"New-API-User",
		"Cookie",
		"Set-Cookie",
		"X-Auth-Token",
		"X-Access-Token",
		"X-Session-Token",
		"X-Refresh-Token",
		"X-Client-Secret",
		"X-Signature",
		"X-Some-Future-Gateway-Credential",
	}
	for _, name := range sensitive {
		if !isMirrorSensitiveHeader(name) {
			t.Errorf("header %q must be redacted", name)
		}
	}

	safe := []string{
		"Content-Type",
		"User-Agent",
		"anthropic-version",
		"anthropic-beta",
		"WWW-Authenticate",
		"Proxy-Authenticate",
		"Idempotency-Key",
		"Sec-WebSocket-Key",
		"x-ratelimit-remaining-tokens",
		"anthropic-ratelimit-input-tokens-limit",
		"x-rate-limit-remaining-tokens",
	}
	for _, name := range safe {
		if isMirrorSensitiveHeader(name) {
			t.Errorf("header %q must not be redacted (mirror capture would lose diagnostic value)", name)
		}
	}
}

// TestIsMirrorSensitiveQueryKey 覆盖 URL 侧脱敏：Gemini 的 ?key=、常见 token 参数以及
// 名字含鉴权词的未知参数都要抹掉，协议诊断参数（alt/api-version）保留。
func TestIsMirrorSensitiveQueryKey(t *testing.T) {
	sensitive := []string{"key", "api_key", "apiKey", "api-key", "token", "access_token", "refresh_token", "secret", "signature", "sig", "password", "pass", "auth", "x-goog-api-key"}
	for _, key := range sensitive {
		if !isMirrorSensitiveQueryKey(key) {
			t.Errorf("query key %q must be redacted", key)
		}
	}
	safe := []string{"alt", "api-version", "model", "stream"}
	for _, key := range safe {
		if isMirrorSensitiveQueryKey(key) {
			t.Errorf("query key %q must not be redacted", key)
		}
	}
}

func TestMirrorRecorderResponseChunksAppend(t *testing.T) {
	rec := newMirrorRecorder(t.TempDir())
	defer rec.Close()
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

// TestMirrorRecorderResponseStartRecordsStatusAndSanitizedHeaders 验证响应起始记录
// 含 status 与脱敏后的 headers（Authorization/X-Api-Key 抹掉）。
func TestMirrorRecorderResponseStartRecordsStatusAndSanitizedHeaders(t *testing.T) {
	rec := newMirrorRecorder(t.TempDir())
	defer rec.Close()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":  {"application/json"},
			"Authorization": {"Bearer sk-secret"},
			"X-Api-Key":     {"sk-secret"},
		},
	}
	rec.recordResponseStart("api.openai.com", resp)

	raw, err := os.ReadFile(filepath.Join(rec.historyRoot, mirrorLogSubdir, mirrorLogFilename))
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	if !strings.Contains(content, `"status":200`) {
		t.Fatalf("status missing: %s", content)
	}
	if !strings.Contains(content, `"authorization":"[REDACTED]"`) {
		t.Fatalf("authorization not redacted: %s", content)
	}
	if !strings.Contains(content, `"x-api-key":"[REDACTED]"`) {
		t.Fatalf("x-api-key not redacted: %s", content)
	}
	if !strings.Contains(content, `"content-type":"application/json"`) {
		t.Fatalf("content-type missing: %s", content)
	}
}

// failingReadCloser 每次 Read 都返回固定错误，用于模拟请求体读取失败。
type failingReadCloser struct{ err error }

func (f *failingReadCloser) Read(p []byte) (int, error) { return 0, f.err }
func (f *failingReadCloser) Close() error               { return nil }

// TestMirrorRecorderRequestReadFailureKeepsBodyIntact 验证请求体读失败时：
// 保持 req.Body 原样（不重建、不截断直通），且不写任何记录。
func TestMirrorRecorderRequestReadFailureKeepsBodyIntact(t *testing.T) {
	rec := newMirrorRecorder(t.TempDir())
	defer rec.Close()
	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	failing := &failingReadCloser{err: errors.New("simulated read failure")}
	req.Body = failing

	rec.recordRequest("api.openai.com", req)

	if req.Body != failing {
		t.Fatal("request body must be left untouched (same reader) when reading fails")
	}
	logPath := filepath.Join(rec.historyRoot, mirrorLogSubdir, mirrorLogFilename)
	if _, statErr := os.Stat(logPath); !os.IsNotExist(statErr) {
		t.Fatalf("no record must be written on read failure, stat err=%v", statErr)
	}
}

// fixedChunkReader 每次 Read 返回固定大小的 chunk，直到耗尽 remaining 字节。
type fixedChunkReader struct {
	remaining int
	chunk     []byte
}

func (r *fixedChunkReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(r.chunk)
	if n > len(p) {
		n = len(p)
	}
	if n > r.remaining {
		n = r.remaining
	}
	copy(p, r.chunk[:n])
	r.remaining -= n
	return n, nil
}

// TestMirrorTeeReadCloserTruncatesRecording 验证响应记录限流：累计超过
// mirrorResponseMaxBytes 后停止写记录（数据仍完整透传给客户端），
// 并以一条 truncated:true 收尾记录标记截断。
func TestMirrorTeeReadCloserTruncatesRecording(t *testing.T) {
	rec := newMirrorRecorder(t.TempDir())
	defer rec.Close()
	rec.recordResponseStart("api.anthropic.com", &http.Response{StatusCode: http.StatusOK, Header: http.Header{}})

	const chunkSize = 256 * 1024
	const totalBytes = 3 * 1024 * 1024
	tee := &mirrorTeeReadCloser{
		r:    io.NopCloser(&fixedChunkReader{remaining: totalBytes, chunk: make([]byte, chunkSize)}),
		rec:  rec,
		host: "api.anthropic.com",
	}

	buf := make([]byte, chunkSize)
	var readTotal int
	for {
		n, readErr := tee.Read(buf)
		readTotal += n
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
	}
	if readTotal != totalBytes {
		t.Fatalf("client must still receive all %d bytes, got %d", totalBytes, readTotal)
	}

	raw, err := os.ReadFile(filepath.Join(rec.historyRoot, mirrorLogSubdir, mirrorLogFilename))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	// 1 行 recordResponseStart + 4 行 chunk（256KiB*4 = mirrorResponseMaxBytes 内）+ 1 行 truncated 收尾。
	wantLines := 6
	if len(lines) != wantLines {
		t.Fatalf("got %d recorded lines, want %d (bounded by mirrorResponseMaxBytes): %s", len(lines), wantLines, raw)
	}
	if last := string(lines[len(lines)-1]); !strings.Contains(last, `"truncated":true`) {
		t.Fatalf("last line must carry truncated marker: %s", last)
	}
}

// fakeFidelityMirrorConfig 实现可选的 protocolFidelityProvider，用于验证保真开关的接线与热切换。
type fakeFidelityMirrorConfig struct {
	fakeMirrorConfig
	fidelity bool
}

func (f *fakeFidelityMirrorConfig) MirrorCaptureProtocolFidelity() bool { return f.fidelity }

// TestMirrorProtocolFidelityFuncOptIn 固定「未实现 provider 的配置一律关闭保真」这条兜底，
// 保证默认路径与既有实现零行为差异。
func TestMirrorProtocolFidelityFuncOptIn(t *testing.T) {
	if mirrorProtocolFidelityFunc(nil) != nil {
		t.Fatal("nil config must not expose a fidelity func")
	}
	if mirrorProtocolFidelityFunc(&fakeMirrorConfig{}) != nil {
		t.Fatal("config without the optional provider must not expose a fidelity func")
	}
	fidelityFunc := mirrorProtocolFidelityFunc(&fakeFidelityMirrorConfig{fidelity: true})
	if fidelityFunc == nil || !fidelityFunc() {
		t.Fatal("config implementing the provider must expose its value")
	}
}

// TestMirrorRecorderProtocolFidelityHotSwitch 验证保真开关是每次记录时求值的：
// 关闭时 body 走既有明文字段，开启后同一个记录器立即改写 Base64 保真字段，无需重建代理。
func TestMirrorRecorderProtocolFidelityHotSwitch(t *testing.T) {
	fidelity := false
	rec := newConfiguredMirrorRecorder(t.TempDir(), func() bool { return fidelity })
	defer rec.Close()

	rec.recordResponseChunk("api2.cursor.sh", []byte("plain-chunk"))
	fidelity = true
	rec.recordResponseChunk("api2.cursor.sh", []byte("fidelity-chunk"))

	raw, err := os.ReadFile(filepath.Join(rec.historyRoot, mirrorLogSubdir, mirrorLogFilename))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatalf("got %d recorded lines, want 2: %s", len(lines), raw)
	}
	if first := string(lines[0]); !strings.Contains(first, `"body":"plain-chunk"`) || strings.Contains(first, `"bodyEncoding"`) {
		t.Fatalf("fidelity-off line must keep the plain body field: %s", first)
	}
	if second := string(lines[1]); !strings.Contains(second, `"bodyEncoding":"base64"`) || !strings.Contains(second, `"bodySHA256"`) {
		t.Fatalf("fidelity-on line must carry base64 body fields: %s", second)
	}
}

// TestNewProxyServerProtocolFidelityFollowsConfig 覆盖构造接线：默认构造关闭保真，
// 配置打开后同一个代理立即改用保真记录，隔离入口则无条件保真。
func TestNewProxyServerProtocolFidelityFollowsConfig(t *testing.T) {
	config := &fakeFidelityMirrorConfig{}
	proxy, err := NewProxyServer("127.0.0.1:0", "http://127.0.0.1:1", t.TempDir(), config, nil)
	if err != nil {
		t.Fatalf("NewProxyServer() error = %v", err)
	}
	defer proxy.mirrorRec.Close()
	if proxy.mirrorRec.fidelityEnabled() {
		t.Fatal("protocol fidelity must be off while the config disables it")
	}
	config.fidelity = true
	if !proxy.mirrorRec.fidelityEnabled() {
		t.Fatal("protocol fidelity must follow the config without rebuilding the proxy")
	}

	plain, err := NewProxyServer("127.0.0.1:0", "http://127.0.0.1:1", t.TempDir(), &fakeMirrorConfig{}, nil)
	if err != nil {
		t.Fatalf("NewProxyServer(plain) error = %v", err)
	}
	defer plain.mirrorRec.Close()
	if plain.mirrorRec.fidelityEnabled() {
		t.Fatal("a config without the fidelity provider must stay in non-fidelity mode")
	}

	isolated, err := NewIsolatedMirrorCaptureProxyServer("127.0.0.1:0", "http://127.0.0.1:1", t.TempDir(), &fakeMirrorConfig{}, nil)
	if err != nil {
		t.Fatalf("NewIsolatedMirrorCaptureProxyServer() error = %v", err)
	}
	defer isolated.mirrorRec.Close()
	if !isolated.mirrorRec.fidelityEnabled() {
		t.Fatal("the isolated entrypoint must stay unconditionally fidelity-recording")
	}
}
