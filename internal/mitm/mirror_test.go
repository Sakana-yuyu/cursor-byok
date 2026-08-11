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
