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
