package modeladapter

import "testing"

func TestResponsesEOFFallbackUsesOfficialOpenAIPolicy(t *testing.T) {
	tests := []struct {
		name          string
		baseURL       string
		allowFallback bool
	}{
		{name: "official api", baseURL: "https://api.openai.com/v1", allowFallback: false},
		{name: "official codex", baseURL: "https://chatgpt.com/backend-api/codex", allowFallback: false},
		{name: "third party relay", baseURL: "https://gateway.example/v1", allowFallback: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyProviderCompatibility(tt.baseURL, "gpt-test").AllowResponsesEOFFallback; got != tt.allowFallback {
				t.Fatalf("AllowResponsesEOFFallback = %v, want %v", got, tt.allowFallback)
			}
		})
	}
}
