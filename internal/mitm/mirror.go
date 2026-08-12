package mitm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
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
	mirrorLogSubdir        = "_debug/mirror"
	mirrorLogFilename      = "official.raw.jsonl"
	mirrorBodyMaxBytes     = 128 * 1024
	mirrorResponseMaxBytes = 1024 * 1024
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
	TS           time.Time         `json:"ts"`
	ExchangeID   string            `json:"exchangeId,omitempty"`
	Phase        mirrorPhase       `json:"phase"`
	Host         string            `json:"host"`
	Model        string            `json:"model,omitempty"`
	Method       string            `json:"method,omitempty"`
	URL          string            `json:"url,omitempty"`
	Status       int               `json:"status,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Body         string            `json:"body,omitempty"`
	BodyEncoding string            `json:"bodyEncoding,omitempty"`
	BodyBytes    *int              `json:"bodyBytes,omitempty"`
	BodySHA256   string            `json:"bodySHA256,omitempty"`
	BodyBase64   *string           `json:"bodyBase64,omitempty"`
	Truncated    bool              `json:"truncated,omitempty"`
}

type mirrorPhase string

const (
	mirrorPhaseRequest           mirrorPhase = "request"
	mirrorPhaseResponseStart     mirrorPhase = "response_start"
	mirrorPhaseResponseChunk     mirrorPhase = "response_chunk"
	mirrorPhaseResponseTruncated mirrorPhase = "response_truncated"
)

// mirrorExchange 只在代理进程内关联同一镜像 HTTP 交换的各条记录。
type mirrorExchange struct {
	id    string
	model string
}

// mirrorRecorder 把镜像请求/响应追加写入 <historyRoot>/_debug/mirror/official.raw.jsonl。
// 记录失败只记日志，绝不阻断代理直通。
type mirrorRecorder struct {
	historyRoot      string
	protocolFidelity bool
	mu               sync.Mutex
	file             *os.File
}

func newMirrorRecorder(historyRoot string) *mirrorRecorder {
	return &mirrorRecorder{historyRoot: historyRoot}
}

func newProtocolFidelityMirrorRecorder(historyRoot string) *mirrorRecorder {
	return &mirrorRecorder{historyRoot: historyRoot, protocolFidelity: true}
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

// Close 关闭底层日志文件，释放句柄。用于服务关闭或测试清理。
func (r *mirrorRecorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

// recordRequest 记录一次镜像请求；读出的 body 会重建回 req.Body 供直通。
func (r *mirrorRecorder) recordRequest(host string, req *http.Request) {
	r.recordExchangeRequest(host, nil, req)
}

// recordExchangeRequest 记录带本地交换关联信息的镜像请求。
func (r *mirrorRecorder) recordExchangeRequest(host string, exchange *mirrorExchange, req *http.Request) {
	if r == nil || req == nil {
		return
	}
	rec := mirrorRecord{
		TS:         time.Now(),
		ExchangeID: mirrorExchangeID(exchange),
		Phase:      mirrorPhaseRequest,
		Host:       host,
		Model:      mirrorExchangeModel(exchange),
		Method:     req.Method,
		URL:        sanitizeMirrorRequestURL(req),
		Headers:    sanitizeHeaders(req.Header),
	}
	var body []byte
	if req.Body != nil {
		readBody, err := io.ReadAll(io.LimitReader(req.Body, mirrorBodyMaxBytes+1))
		if err != nil {
			// 直通语义优先于记录：读失败时保持 req.Body 原样（不重建），
			// 避免把截断的部分 body 重建后直通给上游；不写记录也不动 body。
			logger.Errorf("mirror record read request body failed: %v", err)
			return
		}
		body = readBody
		recordedBody := body
		if len(body) > mirrorBodyMaxBytes {
			// 记录端截断不影响直通：继续读完剩余部分，重建完整 body 给上游。
			rest, restErr := io.ReadAll(req.Body)
			if restErr != nil {
				logger.Errorf("mirror record drain request body failed: %v", restErr)
			}
			body = append(body, rest...)
			rec.Truncated = true
			recordedBody = body[:mirrorBodyMaxBytes]
		}
		r.setRecordBody(&rec, recordedBody)
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	if rec.Model == "" {
		rec.Model = mirrorRequestModel(host, req, body)
	}
	if exchange != nil {
		exchange.model = rec.Model
	}
	r.write(rec)
}

// recordResponseStart 记录一次镜像响应的起始信息（ts/host/status/脱敏 headers），body 为空。
// 响应体内容由 recordResponseChunk 逐 chunk 追加。
func (r *mirrorRecorder) recordResponseStart(host string, resp *http.Response) {
	r.recordExchangeResponseStart(host, nil, resp)
}

func (r *mirrorRecorder) recordExchangeResponseStart(host string, exchange *mirrorExchange, resp *http.Response) {
	if r == nil || resp == nil {
		return
	}
	r.write(mirrorRecord{
		TS:         time.Now(),
		ExchangeID: mirrorExchangeID(exchange),
		Phase:      mirrorPhaseResponseStart,
		Host:       host,
		Model:      mirrorExchangeModel(exchange),
		Status:     resp.StatusCode,
		Headers:    sanitizeHeaders(resp.Header),
	})
}

// recordResponseTruncated 写一条 truncated 收尾记录，标记该响应后续 body 因超限未记录。
func (r *mirrorRecorder) recordResponseTruncated(host string) {
	r.recordExchangeResponseTruncated(host, nil)
}

func (r *mirrorRecorder) recordExchangeResponseTruncated(host string, exchange *mirrorExchange) {
	if r == nil {
		return
	}
	r.write(mirrorRecord{
		TS:         time.Now(),
		ExchangeID: mirrorExchangeID(exchange),
		Phase:      mirrorPhaseResponseTruncated,
		Host:       host,
		Model:      mirrorExchangeModel(exchange),
		Truncated:  true,
	})
}

// recordResponseChunk 记录一次镜像响应的一个流式 chunk。
func (r *mirrorRecorder) recordResponseChunk(host string, chunk []byte) {
	r.recordExchangeResponseChunk(host, nil, chunk)
}

func (r *mirrorRecorder) recordExchangeResponseChunk(host string, exchange *mirrorExchange, chunk []byte) {
	if r == nil || len(chunk) == 0 {
		return
	}
	rec := mirrorRecord{
		TS:         time.Now(),
		ExchangeID: mirrorExchangeID(exchange),
		Phase:      mirrorPhaseResponseChunk,
		Host:       host,
		Model:      mirrorExchangeModel(exchange),
	}
	r.setRecordBody(&rec, chunk)
	r.write(rec)
}

// setRecordBody 仅在隔离保真模式中使用 Base64，避免 protobuf 非 UTF-8 字节经 JSON 字符串替换。
func (r *mirrorRecorder) setRecordBody(rec *mirrorRecord, body []byte) {
	if rec == nil {
		return
	}
	if r == nil || !r.protocolFidelity {
		rec.Body = string(body)
		return
	}
	sum := sha256.Sum256(body)
	bodyBytes := len(body)
	bodyBase64 := base64.StdEncoding.EncodeToString(body)
	rec.BodyEncoding = "base64"
	rec.BodyBytes = &bodyBytes
	rec.BodySHA256 = hex.EncodeToString(sum[:])
	rec.BodyBase64 = &bodyBase64
}

func mirrorExchangeID(exchange *mirrorExchange) string {
	if exchange == nil {
		return ""
	}
	return exchange.id
}

func mirrorExchangeModel(exchange *mirrorExchange) string {
	if exchange == nil {
		return ""
	}
	return exchange.model
}

// mirrorRequestModel 从已读取的请求体或 Gemini URL 尽力提取模型名。
// 解析失败属于不可信请求输入，保留空元数据但不影响官方请求直通。
func mirrorRequestModel(host string, req *http.Request, body []byte) string {
	var payload struct {
		Model string `json:"model"`
	}
	if len(body) > 0 && json.Unmarshal(body, &payload) == nil {
		if model := strings.TrimSpace(payload.Model); model != "" {
			return model
		}
	}
	if !strings.EqualFold(strings.TrimSpace(host), "generativelanguage.googleapis.com") || req == nil || req.URL == nil {
		return ""
	}
	parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	for index := 0; index+1 < len(parts); index++ {
		if parts[index] != "models" {
			continue
		}
		model := strings.SplitN(parts[index+1], ":", 2)[0]
		return strings.TrimSpace(model)
	}
	return ""
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

// sanitizeMirrorRequestURL 只生成用于本地镜像记录的 URL 副本。
// 直通请求继续使用 req.URL，避免记录脱敏意外改变官方 API 语义。
func sanitizeMirrorRequestURL(req *http.Request) string {
	if req == nil || req.URL == nil {
		return ""
	}
	copyURL := *req.URL
	values := copyURL.Query()
	for key := range values {
		if isMirrorSensitiveQueryKey(key) {
			values[key] = []string{"[REDACTED]"}
		}
	}
	copyURL.RawQuery = values.Encode()
	copyURL.Fragment = ""
	copyURL.RawFragment = ""
	return mirrorURLString(&copyURL)
}

func isMirrorSensitiveQueryKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "key", "api_key", "apikey", "token", "access_token", "refresh_token", "secret", "signature", "sig", "password", "pass":
		return true
	default:
		return false
	}
}

func mirrorURLString(value *url.URL) string {
	if value == nil {
		return ""
	}
	text := value.String()
	if strings.TrimSpace(text) == "" {
		return "/"
	}
	return text
}

func sanitizeHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		lower := strings.ToLower(k)
		if mirrorSensitiveHeaders[lower] {
			out[lower] = "[REDACTED]"
			continue
		}
		if len(vs) > 0 {
			out[lower] = vs[0]
		}
	}
	return out
}
