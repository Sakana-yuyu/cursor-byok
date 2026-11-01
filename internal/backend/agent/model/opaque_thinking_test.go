package modeladapter

import (
	"strings"
	"testing"
)

func TestAnthropicStreamMarksSignatureOnlyThinkingOpaque(t *testing.T) {
	server := newAnthropicTestServer(t, nil)
	defer server.Close()
	adapter := &AnthropicAdapter{client: server.Client()}
	events, err := runAnthropicTestStream(t, adapter, StreamRequest{BaseURL: server.URL + "/v1", APIKey: "token", ProviderModelID: "claude-test", ModelCallID: "call-1"}, strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-test\"}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig-1\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"opaque\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"readable\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig-2\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":2}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, ""))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	var completed []ModelEvent
	for _, event := range events {
		if event.Kind == ModelEventKindThinkingCompleted {
			completed = append(completed, event)
		}
	}
	if len(completed) != 3 {
		t.Fatalf("ThinkingCompleted count = %d, want 3; events=%#v", len(completed), events)
	}
	if !completed[0].ThinkingOpaque || completed[0].ThinkingSignature != "sig-1" || completed[0].ThinkingSignatureSource != ReasoningSignatureSourceAnthropic {
		t.Fatalf("signature-only completion = %#v, want opaque with anthropic signature", completed[0])
	}
	if !completed[1].ThinkingOpaque || completed[1].ThinkingSignature != "" {
		t.Fatalf("redacted completion = %#v, want opaque without signature", completed[1])
	}
	if completed[2].ThinkingOpaque || completed[2].ThinkingSignature != "sig-2" {
		t.Fatalf("readable completion = %#v, want non-opaque with signature", completed[2])
	}
}

func TestOpenAIChatStreamRoutesGeminiThoughtDeltasToThinking(t *testing.T) {
	events, err := runOpenAIChatGateStream(t, strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant","content":"**Planning**\n\nthinking text","extra_content":{"google":{"thought":true}}}}]}`,
		`data: {"choices":[{"delta":{"content":"final answer"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}, "\n\n")+"\n\n", nil)
	if err != nil {
		t.Fatalf("stream error = %v", err)
	}
	var thinking, text strings.Builder
	for _, event := range events {
		switch event.Kind {
		case ModelEventKindThinkingDelta:
			thinking.WriteString(event.Text)
		case ModelEventKindTextDelta:
			text.WriteString(event.Text)
		}
	}
	if thinking.String() != "**Planning**\n\nthinking text" || text.String() != "final answer" {
		t.Fatalf("thinking=%q text=%q", thinking.String(), text.String())
	}
}

func TestApplyGeminiOpenAICompatThoughts(t *testing.T) {
	const geminiBase = "https://generativelanguage.googleapis.com/v1beta/openai"
	body := map[string]any{"reasoning_effort": "high"}
	applyGeminiOpenAICompatThoughts(body, geminiBase)
	config := body["extra_body"].(map[string]any)["google"].(map[string]any)["thinking_config"].(map[string]any)
	if config["include_thoughts"] != true || body["reasoning_effort"] != "high" {
		t.Fatalf("body = %#v, want include_thoughts=true and reasoning_effort kept", body)
	}

	userBody := map[string]any{"reasoning_effort": "low", "extra_body": map[string]any{"google": map[string]any{"thinking_config": map[string]any{"include_thoughts": false}, "cached_content": "c"}}}
	applyGeminiOpenAICompatThoughts(userBody, geminiBase)
	google := userBody["extra_body"].(map[string]any)["google"].(map[string]any)
	if google["thinking_config"].(map[string]any)["include_thoughts"] != false || google["cached_content"] != "c" {
		t.Fatalf("user extra_body = %#v, want user values preserved", userBody)
	}

	for _, tc := range []struct {
		name string
		base string
		body map[string]any
	}{
		{name: "disabled", base: geminiBase, body: map[string]any{"reasoning_effort": "none"}},
		{name: "no effort", base: geminiBase, body: map[string]any{}},
		{name: "other provider", base: "https://api.example.com/v1", body: map[string]any{"reasoning_effort": "high"}},
	} {
		applyGeminiOpenAICompatThoughts(tc.body, tc.base)
		if _, ok := tc.body["extra_body"]; ok {
			t.Fatalf("%s: body = %#v, want no extra_body", tc.name, tc.body)
		}
	}
}
