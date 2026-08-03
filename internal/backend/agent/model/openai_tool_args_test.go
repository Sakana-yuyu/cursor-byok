package modeladapter

import (
	"strings"
	"testing"
)

func TestCompletedOpenAIToolArgsJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		wantSub string // 期望输出中包含的片段（为空则不检查）
	}{
		{
			name: "正常对象",
			input: `{"paths": ["e:\\MyProject\\a.go", "b.go"]}`,
		},
		{
			name: "空参数",
			input: ``,
		},
		{
			name:    "非对象",
			input:   `null`,
			wantErr: true,
		},
		{
			name:    "残缺JSON",
			input:   `{"paths": ["e:\MyProject`,
			wantErr: true,
		},
		{
			// 真实故障样本：上游模型重复拼接多份对象草稿，前三份 Windows 路径反斜杠漏转义
			name: "重复对象草稿+漏转义",
			input: `{"paths": ["e:\MyProject\cursor-byok\frontend\src\components\DelegationTaskStrip.vue"]}` +
				`{"paths": ["e:\MyProject\cursor-byok\frontend\src\components\DelegationTaskStrip.vue"]}` +
				`{"paths": ["e:\MyProject\cursor-byok\frontend\src\components\DelegationTaskStrip.vue"]}` +
				`{"paths": ["e:\\MyProject\\cursor-byok\\frontend\\src\\components\\DelegationTaskStrip.vue"]}`,
			wantSub: `e:\\MyProject\\cursor-byok\\frontend\\src\\components\\DelegationTaskStrip.vue`,
		},
		{
			// 单个漏转义路径：修复反斜杠后应可解析
			name:    "单个漏转义路径",
			input:   `{"pattern": "e:\MyProject\x"}`,
			wantSub: `e:\\MyProject\\x`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := &openAIToolAccumulator{Name: "ReadLints"}
			_, _ = acc.Args.WriteString(tt.input)
			out, err := completedOpenAIToolArgsJSON(acc)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望解析失败，实际成功: %s", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if tt.wantSub != "" && !strings.Contains(string(out), tt.wantSub) {
				t.Fatalf("输出缺少期望片段 %q，实际: %s", tt.wantSub, out)
			}
		})
	}
}

func TestRepairOpenAIToolArgsEscapes(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`e:\MyProject\x`, `e:\\MyProject\\x`},   // 非法转义补斜杠
		{`e:\\MyProject`, `e:\\MyProject`},       // 合法转义不变
		{`line\nbreak`, `line\nbreak`},           // 合法 \n 不变
		{`\u4e2d\u6587`, `\u4e2d\u6587`},         // unicode 转义不变
		{`a\\b\M`, `a\\b\\M`},                     // 合法对 + 非法单
	}
	for _, c := range cases {
		if got := repairOpenAIToolArgsEscapes(c.in); got != c.want {
			t.Fatalf("repair(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}