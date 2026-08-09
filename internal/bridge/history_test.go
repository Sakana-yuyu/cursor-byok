package bridge

import (
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

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
