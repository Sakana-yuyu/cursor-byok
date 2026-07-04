package modeladapter

import "testing"

func TestTrailingVersionSegment(t *testing.T) {
	cases := []struct {
		base    string
		wantSeg string
		wantOK  bool
	}{
		{"https://api.openai.com/v1", "v1", true},
		{"https://api.z.ai/api/coding/paas/v4", "v4", true},
		{"https://example.com/v2", "v2", true},
		{"https://example.com/v12", "v12", true},

		// 非版本段
		{"https://api.openai.com", "", false},
		{"https://example.com/chat", "", false},
		{"https://example.com/api", "", false},
		{"https://example.com/v", "", false},
		{"https://example.com/vx", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.base, func(t *testing.T) {
			gotSeg, gotOK := trailingVersionSegment(tc.base)
			if gotSeg != tc.wantSeg || gotOK != tc.wantOK {
				t.Fatalf("trailingVersionSegment(%q) = (%q, %v), want (%q, %v)", tc.base, gotSeg, gotOK, tc.wantSeg, tc.wantOK)
			}
		})
	}
}

func TestOpenAIEndpointURL(t *testing.T) {
	cases := []struct {
		name     string
		baseURL  string
		endpoint string
		want     string
	}{
		// === issue #166 核心场景：选"自定义路径"，baseURL 填完整地址 ===
		{"custom_zai_full_chat", "https://api.z.ai/api/coding/paas/v4/chat/completions", "/custom", "https://api.z.ai/api/coding/paas/v4/chat/completions"},
		{"custom_zai_full_responses", "https://api.z.ai/api/coding/paas/v4/responses", "/custom", "https://api.z.ai/api/coding/paas/v4/responses"},

		// === 存量场景回归：OpenAI 官方 /v1 ===
		{"openai_v1_responses_dedup", "https://api.openai.com/v1", "/v1/responses", "https://api.openai.com/v1/responses"},
		{"openai_v1_chat_dedup", "https://api.openai.com/v1", "/v1/chat/completions", "https://api.openai.com/v1/chat/completions"},
		// baseURL 不带版本号
		{"openai_noversion_chat", "https://api.openai.com", "/v1/chat/completions", "https://api.openai.com/v1/chat/completions"},

		// === baseURL 已含完整 endpoint 后缀 → 直接用 base ===
		{"baseurl_has_chat_suffix", "https://api.openai.com/v1/chat/completions", "/v1/chat/completions", "https://api.openai.com/v1/chat/completions"},
		{"baseurl_has_responses_suffix", "https://api.openai.com/v1/responses", "/v1/responses", "https://api.openai.com/v1/responses"},

		// === 空 endpoint 默认 responses ===
		{"empty_endpoint_defaults", "https://api.openai.com/v1", "", "https://api.openai.com/v1/responses"},

		// === 通用版本段去重：/v2 /v3 ===
		{"v2_dedup", "https://example.com/v2", "/v2/chat/completions", "https://example.com/v2/chat/completions"},
		{"v3_dedup", "https://example.com/v3", "/v3/responses", "https://example.com/v3/responses"},

		// === 不同版本号不去重：base /v4 + endpoint /v1 ===
		{"different_version_no_dedup", "https://example.com/v4", "/v1/chat/completions", "https://example.com/v4/v1/chat/completions"},

		// === 尾部斜杠清理 ===
		{"trailing_slash_base", "https://api.openai.com/v1/", "/v1/responses", "https://api.openai.com/v1/responses"},

		// === /custom + baseURL 不含已知后缀 → 直接返回 base ===
		{"custom_no_suffix", "https://api.example.com/some/path", "/custom", "https://api.example.com/some/path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := OpenAIEndpointURL(tc.baseURL, tc.endpoint)
			if got != tc.want {
				t.Fatalf("OpenAIEndpointURL(%q, %q)\n  got  %s\n  want %s", tc.baseURL, tc.endpoint, got, tc.want)
			}
		})
	}
}