package forwarder

import (
	"strings"
	"testing"
)

// TestCommitLanguageHardPromptFallback 验证 commit 消息语言的强制指令回退规则：
// 空语言（开关未开启/auto 未解析）、未知语言、auto 都必须回退到简体中文，
// 否则静态 prompt 的 "by default" 弱约束会被英文历史提交示例带偏。
func TestCommitLanguageHardPromptFallback(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		language string
		wantLang string
	}{
		{name: "empty language falls back to zh-cn", language: "", wantLang: "zh-cn"},
		{name: "auto falls back to zh-cn", language: "auto", wantLang: "zh-cn"},
		{name: "unknown language falls back to zh-cn", language: "fr-fr", wantLang: "zh-cn"},
		{name: "explicit en-us is preserved", language: "en-us", wantLang: "en-us"},
		{name: "explicit ja-jp is preserved", language: "ja-jp", wantLang: "ja-jp"},
		{name: "explicit zh-cn is preserved", language: "zh-cn", wantLang: "zh-cn"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prompt := commitLanguageHardPrompt(tc.language)
			if strings.TrimSpace(prompt) == "" {
				t.Fatalf("commitLanguageHardPrompt(%q) 不应返回空字符串（必须注入强制语言指令）", tc.language)
			}
			want := commitLanguageHardPrompts[tc.wantLang]
			if prompt != want {
				t.Fatalf("commitLanguageHardPrompt(%q) 期望回退到 %s 的强制指令，实际返回其他内容", tc.language, tc.wantLang)
			}
		})
	}
}

// TestCommitLanguageHardPromptContainsChinese 验证 zh-cn 强制指令确实包含中文要求关键词，
// 防止未来改写指令时丢失简体中文约束。
func TestCommitLanguageHardPromptContainsChinese(t *testing.T) {
	t.Parallel()
	prompt := commitLanguageHardPrompt("")
	if !strings.Contains(prompt, "Simplified Chinese") && !strings.Contains(prompt, "简体中文") {
		t.Fatalf("zh-cn 强制指令应包含简体中文要求，实际：%s", prompt)
	}
}