package modeladapter

import (
	"testing"

	"cursor/internal/modelchannel"
)

func TestOpenAIEndpointURLUsesPlannedChatAndCustomURLs(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		endpoint string
		want     string
	}{
		{name: "versioned chat base", baseURL: "https://gateway.example/v1", endpoint: modelchannel.OpenAIEndpointChatCompletions, want: "https://gateway.example/v1/chat/completions"},
		{name: "unversioned chat base", baseURL: "https://gateway.example/proxy", endpoint: modelchannel.OpenAIEndpointChatCompletions, want: "https://gateway.example/proxy/v1/chat/completions"},
		{name: "query chat base", baseURL: "https://gateway.example/v4?api-version=1", endpoint: modelchannel.OpenAIEndpointChatCompletions, want: "https://gateway.example/v4/chat/completions?api-version=1"},
		{name: "complete custom URL", baseURL: "https://gateway.example/infer?api-version=1", endpoint: modelchannel.OpenAIEndpointCustom, want: "https://gateway.example/infer?api-version=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OpenAIEndpointURL(tt.baseURL, tt.endpoint); got != tt.want {
				t.Fatalf("OpenAIEndpointURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
