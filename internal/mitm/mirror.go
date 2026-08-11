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
	TS        time.Time         `json:"ts"`
	Host      string            `json:"host"`
	Method    string            `json:"method,omitempty"`
	URL       string            `json:"url,omitempty"`
	Status    int               `json:"status,omitempty"`
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
	if r == nil || req == nil {
		return
	}
	rec := mirrorRecord{TS: time.Now(), Host: host, Method: req.Method, URL: requestURL(req), Headers: sanitizeHeaders(req.Header)}
	if req.Body != nil {
		body, err := io.ReadAll(io.LimitReader(req.Body, mirrorBodyMaxBytes+1))
		if err != nil {
			// 直通语义优先于记录：读失败时保持 req.Body 原样（不重建），
			// 避免把截断的部分 body 重建后直通给上游；不写记录也不动 body。
			logger.Errorf("mirror record read request body failed: %v", err)
			return
		}
		if len(body) > mirrorBodyMaxBytes {
			// 记录端截断不影响直通：继续读完剩余部分，重建完整 body 给上游。
			rest, restErr := io.ReadAll(req.Body)
			if restErr != nil {
				logger.Errorf("mirror record drain request body failed: %v", restErr)
			}
			body = append(body, rest...)
			rec.Truncated = true
			rec.Body = string(body[:mirrorBodyMaxBytes])
		} else {
			rec.Body = string(body)
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	r.write(rec)
}

// recordResponseStart 记录一次镜像响应的起始信息（ts/host/status/脱敏 headers），body 为空。
// 响应体内容由 recordResponseChunk 逐 chunk 追加。
func (r *mirrorRecorder) recordResponseStart(host string, resp *http.Response) {
	if r == nil || resp == nil {
		return
	}
	r.write(mirrorRecord{TS: time.Now(), Host: host, Status: resp.StatusCode, Headers: sanitizeHeaders(resp.Header)})
}

// recordResponseTruncated 写一条 truncated 收尾记录，标记该响应后续 body 因超限未记录。
func (r *mirrorRecorder) recordResponseTruncated(host string) {
	if r == nil {
		return
	}
	r.write(mirrorRecord{TS: time.Now(), Host: host, Truncated: true})
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
