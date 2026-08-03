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