package modelchannel

import "testing"

func TestNormalizeOpenAIRequestGroupDefaultsByEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{name: "responses endpoint", endpoint: OpenAIEndpointResponses, want: OpenAIRequestGroupResponses},
		{name: "chat endpoint", endpoint: OpenAIEndpointChatCompletions, want: OpenAIRequestGroupChatCompletions},
		{name: "custom endpoint defaults to chat", endpoint: OpenAIEndpointCustom, want: OpenAIRequestGroupChatCompletions},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeOpenAIRequestGroup("openai", tc.endpoint, "")
			if got != tc.want {
				t.Fatalf("NormalizeOpenAIRequestGroup() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOpenAIRequestGroupSupportsAdvancedFields(t *testing.T) {
	if OpenAIRequestGroupSupportsAdvancedFields(OpenAIRequestGroupChatCompletionsCompat) {
		t.Fatal("compat request group should disable advanced fields")
	}
	if !OpenAIRequestGroupSupportsAdvancedFields(OpenAIRequestGroupChatCompletions) {
		t.Fatal("standard chat request group should allow advanced fields")
	}
}
