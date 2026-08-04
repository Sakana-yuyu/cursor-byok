package bridge

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const debugTestUUID = "11111111-2222-3333-4444-555555555555"

// writeDebugSession 构造一个带 debug 文件的会话目录用于测试。
func writeDebugSession(t *testing.T, root, id string) string {
	t.Helper()
	sessionDir := filepath.Join(root, id)
	debugDir := filepath.Join(sessionDir, "debug")
	mustMkdirAll(t, debugDir)
	if err := os.WriteFile(filepath.Join(sessionDir, "state.json"), []byte(`{"created_at":"2026-08-04T10:00:00Z","current_request_id":"req-123"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "context.json"), []byte(`{"items":[{"role":"user","kind":"message","content":"hi"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(debugDir, "runtime.jsonl"), []byte("{\"event\":\"start\"}\n{\"event\":\"error\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(debugDir, "provider.jsonl"), []byte("{\"ok\":true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return sessionDir
}

// TestExportSessionDebugBundleIn_OK 正常打包：zip 内含 state.json/context.json/debug 文件。
func TestExportSessionDebugBundleIn_OK(t *testing.T) {
	historyRoot := t.TempDir()
	logsRoot := t.TempDir()
	writeDebugSession(t, historyRoot, debugTestUUID)

	zipPath, err := exportSessionDebugBundleIn(historyRoot, logsRoot, debugTestUUID)
	if err != nil {
		t.Fatalf("exportSessionDebugBundleIn: %v", err)
	}
	if zipPath == "" {
		t.Fatal("want non-empty zip path")
	}
	if info, statErr := os.Stat(zipPath); statErr != nil || info.Size() == 0 {
		t.Fatalf("zip file missing or empty: %v", statErr)
	}

	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer func() { _ = reader.Close() }()

	names := make(map[string]bool, len(reader.File))
	for _, f := range reader.File {
		names[f.Name] = true
	}
	want := []string{"state.json", "context.json", "debug/runtime.jsonl", "debug/provider.jsonl"}
	for _, name := range want {
		if !names[name] {
			t.Fatalf("zip missing entry %q; got %v", name, names)
		}
	}
	if !strings.HasPrefix(filepath.Base(zipPath), "session-11111111-") {
		t.Fatalf("zip name should start with short id prefix, got %q", filepath.Base(zipPath))
	}
}

// TestExportSessionDebugBundleIn_NonUUID 非 UUID sessionID 被拒绝。
func TestExportSessionDebugBundleIn_NonUUID(t *testing.T) {
	historyRoot := t.TempDir()
	logsRoot := t.TempDir()
	_, err := exportSessionDebugBundleIn(historyRoot, logsRoot, "../../../etc/passwd")
	if err == nil {
		t.Fatal("want error for path-traversal sessionID, got nil")
	}
	if _, statErr := os.Stat(filepath.Join(logsRoot, "session-.zip")); statErr == nil {
		t.Fatal("no zip should be created for invalid sessionID")
	}
}

// TestExportSessionDebugBundleIn_MissingSession 不存在的会话报错。
func TestExportSessionDebugBundleIn_MissingSession(t *testing.T) {
	historyRoot := t.TempDir()
	logsRoot := t.TempDir()
	_, err := exportSessionDebugBundleIn(historyRoot, logsRoot, "99999999-2222-3333-4444-555555555555")
	if err == nil {
		t.Fatal("want error for missing session, got nil")
	}
}

// TestExportSessionDebugBundleIn_MissingDebugDir debug 子目录不存在时报错。
func TestExportSessionDebugBundleIn_MissingDebugDir(t *testing.T) {
	historyRoot := t.TempDir()
	logsRoot := t.TempDir()
	sessionDir := filepath.Join(historyRoot, debugTestUUID)
	mustMkdirAll(t, sessionDir)
	if err := os.WriteFile(filepath.Join(sessionDir, "state.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := exportSessionDebugBundleIn(historyRoot, logsRoot, debugTestUUID)
	if err == nil {
		t.Fatal("want error for missing debug dir, got nil")
	}
}

// TestListSessionDebugFilesIn_OK 列出文件元信息。
func TestListSessionDebugFilesIn_OK(t *testing.T) {
	historyRoot := t.TempDir()
	writeDebugSession(t, historyRoot, debugTestUUID)

	files, err := listSessionDebugFilesIn(historyRoot, debugTestUUID)
	if err != nil {
		t.Fatalf("listSessionDebugFilesIn: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d: %+v", len(files), files)
	}
	byName := make(map[string]SessionDebugFile, len(files))
	for _, f := range files {
		byName[f.Name] = f
	}
	if f, ok := byName["runtime.jsonl"]; !ok || f.SizeBytes <= 0 || f.ModTimeUnix <= 0 {
		t.Fatalf("bad runtime.jsonl entry: %+v ok=%v", f, ok)
	}
	if f, ok := byName["provider.jsonl"]; !ok || f.SizeBytes <= 0 {
		t.Fatalf("bad provider.jsonl entry: %+v ok=%v", f, ok)
	}
	// 结果按文件名升序。
	if files[0].Name > files[1].Name {
		t.Fatalf("files not sorted by name: %+v", files)
	}
}

// TestListSessionDebugFilesIn_EmptyDir 空目录返回空切片。
func TestListSessionDebugFilesIn_EmptyDir(t *testing.T) {
	historyRoot := t.TempDir()
	writeDebugSession(t, historyRoot, debugTestUUID)
	// 清空 debug 目录内容制造空目录。
	debugDir := filepath.Join(historyRoot, debugTestUUID, "debug")
	entries, _ := os.ReadDir(debugDir)
	for _, e := range entries {
		_ = os.Remove(filepath.Join(debugDir, e.Name()))
	}
	files, err := listSessionDebugFilesIn(historyRoot, debugTestUUID)
	if err != nil {
		t.Fatalf("listSessionDebugFilesIn: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("want empty slice, got %d", len(files))
	}
}

// TestListSessionDebugFilesIn_MissingDir debug 目录不存在返回空切片 + nil。
func TestListSessionDebugFilesIn_MissingDir(t *testing.T) {
	historyRoot := t.TempDir()
	// 会话目录存在但没有 debug 子目录。
	mustMkdirAll(t, filepath.Join(historyRoot, debugTestUUID))
	files, err := listSessionDebugFilesIn(historyRoot, debugTestUUID)
	if err != nil {
		t.Fatalf("listSessionDebugFilesIn on missing debug dir: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("want empty slice, got %d", len(files))
	}
}

// TestListSessionDebugFilesIn_NonUUID 非 UUID 被拒绝。
func TestListSessionDebugFilesIn_NonUUID(t *testing.T) {
	historyRoot := t.TempDir()
	_, err := listSessionDebugFilesIn(historyRoot, "not-a-uuid")
	if err == nil {
		t.Fatal("want error for non-uuid sessionID, got nil")
	}
}

// TestReadSessionDebugTailIn_OK 读尾部，文件小于阈值时整体返回。
func TestReadSessionDebugTailIn_OK(t *testing.T) {
	historyRoot := t.TempDir()
	writeDebugSession(t, historyRoot, debugTestUUID)

	got, err := readSessionDebugTailIn(historyRoot, debugTestUUID, "runtime.jsonl", 0)
	if err != nil {
		t.Fatalf("readSessionDebugTailIn: %v", err)
	}
	if !strings.Contains(got, "\"event\":\"start\"") || !strings.Contains(got, "\"event\":\"error\"") {
		t.Fatalf("tail content mismatch, got %q", got)
	}
}

// TestReadSessionDebugTailIn_TailLarge 文件大于阈值时只返回尾部 maxBytes。
func TestReadSessionDebugTailIn_TailLarge(t *testing.T) {
	historyRoot := t.TempDir()
	writeDebugSession(t, historyRoot, debugTestUUID)
	// 用一个较大的 runtime.jsonl 覆盖，构造 > maxBytes 的场景。
	large := strings.Repeat("x", 200) + "TAIL_MARKER"
	if err := os.WriteFile(filepath.Join(historyRoot, debugTestUUID, "debug", "runtime.jsonl"), []byte(large), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readSessionDebugTailIn(historyRoot, debugTestUUID, "runtime.jsonl", 50)
	if err != nil {
		t.Fatalf("readSessionDebugTailIn: %v", err)
	}
	if !strings.Contains(got, "TAIL_MARKER") {
		t.Fatalf("tail should contain the last bytes incl marker, got %q", got)
	}
	if len(got) > 50 {
		t.Fatalf("tail should be <= maxBytes, got %d", len(got))
	}
}

// TestReadSessionDebugTailIn_NonWhitelist 非白名单 filename 被拒绝。
func TestReadSessionDebugTailIn_NonWhitelist(t *testing.T) {
	historyRoot := t.TempDir()
	writeDebugSession(t, historyRoot, debugTestUUID)
	for _, bad := range []string{"../../etc/passwd", "state.json", "", "unknown.jsonl"} {
		if _, err := readSessionDebugTailIn(historyRoot, debugTestUUID, bad, 0); err == nil {
			t.Fatalf("want error for non-whitelist filename %q, got nil", bad)
		}
	}
}

// TestReadSessionDebugTailIn_MissingFile 文件不存在报错。
func TestReadSessionDebugTailIn_MissingFile(t *testing.T) {
	historyRoot := t.TempDir()
	writeDebugSession(t, historyRoot, debugTestUUID)
	// runsse.jsonl 未写入，应报错。
	if _, err := readSessionDebugTailIn(historyRoot, debugTestUUID, "runsse.jsonl", 0); err == nil {
		t.Fatal("want error for missing debug file, got nil")
	}
}

// TestReadSessionDebugTailIn_NonUUID 非 UUID 被拒绝。
func TestReadSessionDebugTailIn_NonUUID(t *testing.T) {
	historyRoot := t.TempDir()
	if _, err := readSessionDebugTailIn(historyRoot, "not-a-uuid", "runtime.jsonl", 0); err == nil {
		t.Fatal("want error for non-uuid sessionID, got nil")
	}
}
