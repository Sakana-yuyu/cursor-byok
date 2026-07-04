package modelchannel

import "testing"

func TestNormalizeOpenAIEndpoint(t *testing.T) {
	cases := []struct {
		name         string
		providerType string
		endpoint     string
		want         string
	}{
		// 非 openai 类型始终返回空
		{"anthropic_ignored", "anthropic", "/v1/responses", ""},
		{"empty_type_ignored", "", "/v1/responses", ""},

		// 预设值
		{"empty_defaults_to_responses", "openai", "", OpenAIEndpointResponses},
		{"preset_responses", "openai", "/v1/responses", OpenAIEndpointResponses},
		{"preset_chat_completions", "openai", "/v1/chat/completions", OpenAIEndpointChatCompletions},
		{"preset_custom", "openai", "/custom", OpenAIEndpointCustom},

		// 非法值：不再允许任意自定义路径，只接受三个预设值
		{"arbitrary_path_rejected", "openai", "/v4/chat/completions", ""},
		{"arbitrary_path_rejected_2", "openai", "/chat/completions", ""},
		{"missing_slash", "openai", "v1/responses", ""},
		{"slash_only", "openai", "/", ""},
		{"whitespace", "openai", "   ", OpenAIEndpointResponses},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeOpenAIEndpoint(tc.providerType, tc.endpoint)
			if got != tc.want {
				t.Fatalf("NormalizeOpenAIEndpoint(%q, %q) = %q, want %q", tc.providerType, tc.endpoint, got, tc.want)
			}
		})
	}
}

func TestOpenAIEndpointShape(t *testing.T) {
	cases := []struct {
		endpoint string
		want     string
	}{
		// Responses 形态
		{"/v1/responses", "responses"},
		{"/v4/responses", "responses"},
		{"/responses", "responses"},

		// Chat Completions 形态
		{"/v1/chat/completions", "chat/completions"},
		{"/v4/chat/completions", "chat/completions"},
		{"/chat/completions", "chat/completions"},

		// /custom 不含已知后缀 → 兜底走 chat/completions
		{"/custom", "chat/completions"},

		// 兜底默认
		{"", "chat/completions"},
	}

	for _, tc := range cases {
		t.Run(tc.endpoint, func(t *testing.T) {
			got := OpenAIEndpointShape(tc.endpoint)
			if got != tc.want {
				t.Fatalf("OpenAIEndpointShape(%q) = %q, want %q", tc.endpoint, got, tc.want)
			}
		})
	}
}