package bridge

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadHistoryTitle 覆盖新旧两种 context.json 消息格式。
func TestReadHistoryTitle(t *testing.T) {
	tests := []struct {
		name    string
		ctx     string
		want    string
		wantPre string // 存在时按前缀断言（兼容标题截断）
	}{
		{
			name: "新格式 payload.text",
			ctx: `{
				"items": [
					{"seq":1,"role":"user","kind":"request_context","payload":{"env":{"osVersion":"win32"}}},
					{"seq":2,"role":"user","kind":"user_message","payload":{"text":"生成技能 aisongcreaterupload简介失败: 模型返回异常状态 404:","messageId":"m1"}},
					{"seq":3,"role":"assistant","kind":"message","payload":{"text":"我在排查"}}
				]
			}`,
			wantPre: "生成技能 aisongcreaterupload简介失败",
		},
		{
			name: "旧格式 content 字符串",
			ctx: `{
				"items": [
					{"role":"user","kind":"message","content":"修复 MCP 连接失败"},
					{"role":"assistant","kind":"message","content":"好的"}
				]
			}`,
			want: "修复 MCP 连接失败",
		},
		{
			name: "旧格式 content parts 数组",
			ctx: `{
				"items": [
					{"role":"user","kind":"message","content":[{"type":"text","text":"多行"},{"type":"text","text":"标题"}]}
				]
			}`,
			want: "多行标题",
		},
		{
			name: "仅注入条目无用户文本",
			ctx: `{
				"items": [
					{"role":"user","kind":"request_context","payload":{"env":{"osVersion":"win32"}}}
				]
			}`,
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "context.json")
			if err := os.WriteFile(path, []byte(tt.ctx), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := readHistoryTitle(path); got != tt.want && !(tt.wantPre != "" && strings.HasPrefix(got, tt.wantPre)) {
				t.Fatalf("readHistoryTitle = %q, want %q or prefix %q", got, tt.want, tt.wantPre)
			}
		})
	}
}

// TestHistoryDebugDirsIn 覆盖统计/清理共用的 debug 目录遍历：
// UUID 会话、非 UUID 会话、孤儿日志目录都要收录，普通文件要跳过。
func TestHistoryDebugDirsIn(t *testing.T) {
	root := t.TempDir()
	const uuidSession = "11111111-2222-3333-4444-555555555555"
	mustMkdirAll(t, filepath.Join(root, uuidSession, "debug"))
	mustMkdirAll(t, filepath.Join(root, "legacy-conversation", "debug"))
	mustMkdirAll(t, filepath.Join(root, historyOrphanDebugDirName, "orphan", "req-1"))
	if err := os.WriteFile(filepath.Join(root, "usage.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := historyDebugDirsIn(root)
	if err != nil {
		t.Fatalf("historyDebugDirsIn: %v", err)
	}
	got := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		got[dir] = true
	}
	want := []string{
		filepath.Join(root, uuidSession, "debug"),
		filepath.Join(root, "legacy-conversation", "debug"),
		filepath.Join(root, historyOrphanDebugDirName),
	}
	for _, dir := range want {
		if !got[dir] {
			t.Fatalf("missing debug dir %q in %v", dir, dirs)
		}
	}
	if got[filepath.Join(root, "usage.json", "debug")] {
		t.Fatalf("usage.json must not be treated as a session dir: %v", dirs)
	}
}

// TestHistoryDebugDirsInMissingRoot 未初始化的 history 目录不应报错。
func TestHistoryDebugDirsInMissingRoot(t *testing.T) {
	dirs, err := historyDebugDirsIn(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("historyDebugDirsIn on missing root: %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("want no dirs, got %v", dirs)
	}
}

func TestHistoryDirectoryStats(t *testing.T) {
	tests := []struct {
		name         string
		files        map[string]string
		emptyDirs    []string
		wantSession  int64
		wantDebug    int64
		wantHasDebug bool
	}{
		{
			name: "nested ordinary and debug files",
			files: map[string]string{
				"state.json":                                       "abc",
				filepath.Join("nested", "data"):                    "12345",
				filepath.Join("debug", "runtime.jsonl"):            "debug",
				filepath.Join("debug", "nested", "bidi.raw.jsonl"): "events",
			},
			wantSession:  8,
			wantDebug:    11,
			wantHasDebug: true,
		},
		{
			name:         "empty debug directory",
			files:        map[string]string{"context.json": "content"},
			emptyDirs:    []string{"debug"},
			wantSession:  7,
			wantDebug:    0,
			wantHasDebug: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for _, relative := range tt.emptyDirs {
				mustMkdirAll(t, filepath.Join(root, relative))
			}
			for relative, content := range tt.files {
				path := filepath.Join(root, relative)
				mustMkdirAll(t, filepath.Dir(path))
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			sessionBytes, debugBytes, hasDebug := historyDirectoryStats(root)
			if sessionBytes != tt.wantSession || debugBytes != tt.wantDebug || hasDebug != tt.wantHasDebug {
				t.Fatalf("historyDirectoryStats() = (%d, %d, %v), want (%d, %d, %v)", sessionBytes, debugBytes, hasDebug, tt.wantSession, tt.wantDebug, tt.wantHasDebug)
			}
		})
	}
}

func TestHistoryDirectoryStatsWalksOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "state.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	originalWalkDir := historyDirectoryWalkDir
	t.Cleanup(func() { historyDirectoryWalkDir = originalWalkDir })
	walks := 0
	historyDirectoryWalkDir = func(root string, fn fs.WalkDirFunc) error {
		walks++
		return filepath.WalkDir(root, fn)
	}

	historyDirectoryStats(root)
	if walks != 1 {
		t.Fatalf("historyDirectoryStats walker calls = %d, want 1", walks)
	}
}

func TestNormalizeHistoryStatusMarksInactiveActiveStatesInterrupted(t *testing.T) {
	if got := normalizeHistoryStatus("running", false); got != "interrupted" {
		t.Fatalf("normalizeHistoryStatus(running, inactive) = %q, want interrupted", got)
	}
	if got := normalizeHistoryStatus("waiting_tool", false); got != "interrupted" {
		t.Fatalf("normalizeHistoryStatus(waiting_tool, inactive) = %q, want interrupted", got)
	}
	if got := normalizeHistoryStatus("provider_error", false); got != "provider_error" {
		t.Fatalf("normalizeHistoryStatus(provider_error, inactive) = %q, want provider_error", got)
	}
	if got := normalizeHistoryStatus("running", true); got != "running" {
		t.Fatalf("normalizeHistoryStatus(running, active) = %q, want running", got)
	}
}

// TestNormalizeHistoryStatusPassesThroughPersistedInterrupted 钉死后端新增的
// 可持久化 interrupted 终态在桥接层原样透传：它既不是 running 也不是
// waiting_tool，不能再被二次归一化。
func TestNormalizeHistoryStatusPassesThroughPersistedInterrupted(t *testing.T) {
	if got := normalizeHistoryStatus("interrupted", false); got != "interrupted" {
		t.Fatalf("normalizeHistoryStatus(interrupted, inactive) = %q, want interrupted", got)
	}
	if got := normalizeHistoryStatus("interrupted", true); got != "interrupted" {
		t.Fatalf("normalizeHistoryStatus(interrupted, active) = %q, want interrupted", got)
	}
}

// TestScanCursorProtocolSessionsIn 只从安全时间线聚合协议结构，忽略畸形行，
// 且不得让原始抓包可能携带的正文或凭据字段进入对外 DTO。
func TestScanCursorProtocolSessionsIn(t *testing.T) {
	root := t.TempDir()
	timelineDir := filepath.Join(root, "_debug", "mirror")
	mustMkdirAll(t, timelineDir)
	timeline := strings.Join([]string{
		`{"ts":"2026-08-12T09:00:02Z","requestIdHash":"hash-alpha","exchangeId":"private-exchange","direction":"response","sequence":2,"eventKind":"runsse_connect","serverMessageKind":"exec_server_message","streamDeltaBytes":42,"terminal":true,"body":"must-not-leak","token":"must-not-leak"}`,
		`not-json`,
		`{"ts":"2026-08-12T09:00:01Z","requestIdHash":"hash-alpha","exchangeId":"private-exchange","direction":"request","sequence":1,"eventKind":"bidi_append","clientMessageKind":"exec_client_message","multitask":true,"subagentAction":"create"}`,
		`{"ts":"2026-08-12T10:00:00Z","requestIdHash":"hash-bravo","direction":"request","sequence":1,"eventKind":"bidi_append","decodeError":"connect_frame_incomplete"}`,
		`{"ts":"2026-08-12T10:01:00Z","direction":"response","sequence":2,"eventKind":"runsse_connect"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(timelineDir, cursorProtocolTimelineFileName), []byte(timeline), 0o644); err != nil {
		t.Fatal(err)
	}

	sessions, err := scanCursorProtocolSessionsIn(root)
	if err != nil {
		t.Fatalf("scanCursorProtocolSessionsIn: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("session count = %d, want 2", len(sessions))
	}
	if sessions[0].RequestIDHash != "hash-bravo" || sessions[0].EventCount != 1 || sessions[0].UpstreamCount != 1 {
		t.Fatalf("first session = %#v, want newest hash-bravo aggregate", sessions[0])
	}
	alpha := sessions[1]
	if alpha.RequestIDHash != "hash-alpha" || alpha.EventCount != 2 || alpha.UpstreamCount != 1 || alpha.DownstreamCount != 1 {
		t.Fatalf("alpha aggregate = %#v", alpha)
	}
	if !alpha.Multitask || !alpha.Terminal || len(alpha.SubagentActions) != 1 || alpha.SubagentActions[0] != "create" {
		t.Fatalf("alpha state = %#v", alpha)
	}
	if len(alpha.Events) != 2 || alpha.Events[0].Sequence != 1 || alpha.Events[1].Sequence != 2 {
		t.Fatalf("alpha events = %#v, want timestamp/sequence order", alpha.Events)
	}
	encoded, err := json.Marshal(alpha)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"must-not-leak", "private-exchange", "body", "token"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("safe DTO leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestScanCursorProtocolSessionsInMissingTimeline(t *testing.T) {
	sessions, err := scanCursorProtocolSessionsIn(t.TempDir())
	if err != nil {
		t.Fatalf("scan missing timeline: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("missing timeline sessions = %#v, want empty", sessions)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
