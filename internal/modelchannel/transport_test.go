package modelchannel

import "testing"

func TestResolveTransportPlanPreservesQueriesAndEndpointShape(t *testing.T) {
	tests := []struct {
		name  string
		input TransportPlanInput
		want  string
	}{
		{
			name:  "openai responses versioned query",
			input: TransportPlanInput{Provider: "openai", BaseURL: "https://HOST.example/v4?api-version=2026-01-01&route=a%2Fb", ModelID: "gpt-5", ProtocolMode: "auto", ProtocolGroup: ProtocolGroupResponses, OpenAIEndpoint: OpenAIEndpointResponses, Stream: true},
			want:  "https://host.example/v4/responses?api-version=2026-01-01&route=a%2Fb",
		},
		{
			name:  "openai replaces chat with responses",
			input: TransportPlanInput{Provider: "openai", BaseURL: "https://host.example/proxy/v1/chat/completions?x=1", ModelID: "gpt-5", ProtocolMode: "fixed", ProtocolGroup: ProtocolGroupResponses, OpenAIEndpoint: OpenAIEndpointResponses, Stream: true},
			want:  "https://host.example/proxy/v1/responses?x=1",
		},
		{
			name:  "openai custom is complete URL",
			input: TransportPlanInput{Provider: "openai", BaseURL: "https://host.example/v2/infer?api-version=1#ignored", ModelID: "custom", ProtocolMode: "fixed", ProtocolGroup: ProtocolGroupChatCompletions, OpenAIEndpoint: OpenAIEndpointCustom, Stream: true},
			want:  "https://host.example/v2/infer?api-version=1",
		},
		{
			name:  "anthropic versioned query",
			input: TransportPlanInput{Provider: "anthropic", BaseURL: "https://host.example/v2?route=a%2Fb", ModelID: "claude", ProtocolMode: "auto", Stream: true},
			want:  "https://host.example/v2/messages?route=a%2Fb",
		},
		{
			name:  "anthropic complete messages URL",
			input: TransportPlanInput{Provider: "anthropic", BaseURL: "https://host.example/gateway/messages?x=1", ModelID: "claude", Stream: true},
			want:  "https://host.example/gateway/messages?x=1",
		},
		{
			name:  "gemini synthesized stream with encoded model",
			input: TransportPlanInput{Provider: "gemini", BaseURL: "https://host.example/v1beta?key=value%2Fpart", ModelID: "publisher/model", Stream: true},
			want:  "https://host.example/v1beta/models/publisher%2Fmodel:streamGenerateContent?key=value%2Fpart&alt=sse",
		},
		{
			name:  "gemini enforces SSE alt",
			input: TransportPlanInput{Provider: "gemini", BaseURL: "https://host.example/v1beta?alt=json&x=1", ModelID: "gemini-3", Stream: true},
			want:  "https://host.example/v1beta/models/gemini-3:streamGenerateContent?alt=sse&x=1",
		},
		{
			name:  "gemini complete custom URL adds SSE alt",
			input: TransportPlanInput{Provider: "gemini", BaseURL: "https://host.example/custom/models/x:streamGenerateContent?x=1", ModelID: "ignored", Stream: true},
			want:  "https://host.example/custom/models/x:streamGenerateContent?x=1&alt=sse",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := ResolveTransportPlan(tt.input)
			if err != nil {
				t.Fatalf("ResolveTransportPlan() error = %v", err)
			}
			if plan.RequestURL != tt.want {
				t.Fatalf("RequestURL = %q, want %q", plan.RequestURL, tt.want)
			}
		})
	}
}

func TestBaseURLWithoutKnownOpenAIEndpointPreservesQuery(t *testing.T) {
	if got := BaseURLWithoutKnownOpenAIEndpoint("https://gateway.example/v1/responses?route=claude"); got != "https://gateway.example/v1?route=claude" {
		t.Fatalf("Responses base root = %q", got)
	}
	if got := BaseURLWithoutKnownOpenAIEndpoint("https://gateway.example/v1/chat/completions?route=claude"); got != "https://gateway.example/v1?route=claude" {
		t.Fatalf("Chat base root = %q", got)
	}
}

func TestSameEffectiveOriginUsesDefaultPorts(t *testing.T) {
	if !SameEffectiveOrigin("https://EXAMPLE.com/v1", "https://example.com:443/models") {
		t.Fatal("expected default HTTPS port to match explicit 443")
	}
	if SameEffectiveOrigin("https://example.com", "http://example.com") {
		t.Fatal("different schemes must not match")
	}
	if SameEffectiveOrigin("https://example.com", "https://other.example.com") {
		t.Fatal("different hosts must not match")
	}
}

func TestClassifyProtocolGroupUsesURLPathWithoutQuery(t *testing.T) {
	if got := ClassifyProtocolGroup("openai", "custom", "https://host.example/v1/responses?api-version=1", "", ""); got != ProtocolGroupResponses {
		t.Fatalf("ClassifyProtocolGroup() = %q, want responses", got)
	}
	if got := ClassifyProtocolGroup("openai", "fine-tuned-custom-id", "https://api.openai.com/v1", "", ""); got != ProtocolGroupResponses {
		t.Fatalf("official OpenAI host classification = %q, want responses", got)
	}
}
