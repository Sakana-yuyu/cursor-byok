package modeladapter

import (
	"encoding/json"
	"strings"
	"testing"
)

func anthropicToolTurnSSE(stopReason string) string {
	return strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-test\"}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-1\",\"name\":\"Read\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"" + stopReason + "\"}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, "")
}

func runAnthropicToolTurn(t *testing.T, stopReason string) []ModelEvent {
	t.Helper()
	server := newAnthropicTestServer(t, nil)
	defer server.Close()
	adapter := &AnthropicAdapter{client: server.Client()}
	events, err := runAnthropicTestStream(t, adapter, StreamRequest{
		BaseURL: server.URL + "/v1", APIKey: "token", ProviderModelID: "claude-test", ModelCallID: "call-1",
		Tools: []json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"Read","parameters":{"type":"object","properties":{},"required":[]}}}`)},
	}, anthropicToolTurnSSE(stopReason))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	return events
}

func countToolLikeCompleted(events []ModelEvent) (count int, beforeFinish bool) {
	beforeFinish = true
	finished := false
	for _, event := range events {
		switch event.Kind {
		case ModelEventKindTurnFinished:
			finished = true
		case ModelEventKindToolLikeCompleted:
			count++
			if finished {
				beforeFinish = false
			}
		}
	}
	return count, beforeFinish
}

// stop_reason=refusal 的回合里流出的工具调用不得下发执行（leookun/cursor-byok#423/#500）。
func TestAnthropicRefusalDropsStreamedToolCalls(t *testing.T) {
	events := runAnthropicToolTurn(t, "refusal")
	if count, _ := countToolLikeCompleted(events); count != 0 {
		t.Fatalf("refused turn emitted %d tool calls, want 0", count)
	}
	if countAnthropicTurnFinished(events) != 1 {
		t.Fatalf("TurnFinished count = %d, want 1", countAnthropicTurnFinished(events))
	}
	for _, event := range events {
		if event.Kind == ModelEventKindTurnFinished && event.FinishReason != "refusal" {
			t.Fatalf("FinishReason = %q, want refusal", event.FinishReason)
		}
	}
}

// 正常 tool_use 回合：工具调用照常下发，且在 TurnFinished 之前。
func TestAnthropicToolUseStillEmitsToolCallsBeforeFinish(t *testing.T) {
	events := runAnthropicToolTurn(t, "tool_use")
	count, beforeFinish := countToolLikeCompleted(events)
	if count != 1 || !beforeFinish {
		t.Fatalf("tool calls = %d beforeFinish = %t, want 1 before TurnFinished", count, beforeFinish)
	}
}
