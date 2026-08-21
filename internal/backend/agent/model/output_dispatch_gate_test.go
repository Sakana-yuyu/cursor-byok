package modeladapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cursor/internal/modelchannel"
)

// output-dispatch gate 系列测试锁定：adapter 流解析阶段对「provider 返回的工具」执行
// 双向 admission —— 未 advertise 的 builtin/MCP 工具调用必须被 toolAdmissionError 阻止，
// 已 admit 的工具（含 Responses 侧 sanitize 过的 MCP 名）映射回 canonical 名再 dispatch。

func TestAnthropicOutputDispatchGateBlocksUnadvertisedTool(t *testing.T) {
	server := newAnthropicTestServer(t, nil)
	defer server.Close()
	adapter := &AnthropicAdapter{client: server.Client()}
	_, err := runAnthropicTestStream(t, adapter, StreamRequest{
		BaseURL: server.URL + "/v1", APIKey: "token", ProviderModelID: "claude-test", ModelCallID: "call-1",
		Tools: []json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`)},
	}, strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-test\"}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-1\",\"name\":\"GhostTool\"}}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n",
	}, ""))
	if !errors.Is(err, ErrToolAdmission) {
		t.Fatalf("Stream() error = %v, want ErrToolAdmission", err)
	}
}

func TestAnthropicOutputDispatchGateResolvesAdvertisedTool(t *testing.T) {
	server := newAnthropicTestServer(t, nil)
	defer server.Close()
	adapter := &AnthropicAdapter{client: server.Client()}
	events, err := runAnthropicTestStream(t, adapter, StreamRequest{
		BaseURL: server.URL + "/v1", APIKey: "token", ProviderModelID: "claude-test", ModelCallID: "call-1",
		Tools: []json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`)},
	}, strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-test\"}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-1\",\"name\":\"Read\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, ""))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if countDispatchedTools(events) != 1 {
		t.Fatalf("dispatched tool count = %d, events=%#v", countDispatchedTools(events), events)
	}
	for _, event := range events {
		if event.Kind == ModelEventKindToolLikeCompleted && (event.ToolInvocation == nil || event.ToolInvocation.ToolName != "Read") {
			t.Fatalf("anthropic tool dispatch name = %#v, want Read", event.ToolInvocation)
		}
	}
}

func TestGeminiOutputDispatchGateBlocksUnadvertisedTool(t *testing.T) {
	admission, err := admitGeminiTools(map[string]any{"tools": []any{map[string]any{"functionDeclarations": []any{
		map[string]any{"name": "lookup", "parametersJsonSchema": map[string]any{"type": "object"}},
	}}}})
	if err != nil {
		t.Fatalf("admitGeminiTools() error = %v", err)
	}
	events, err := runGeminiTestStreamWithReq(t, StreamRequest{ModelCallID: "call-1", ToolAdmission: admission},
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"GhostTool"}}]},"finishReason":"STOP"}]}`+"\n\n")
	if !errors.Is(err, ErrToolAdmission) {
		t.Fatalf("streamGeminiEvents() error = %v, want ErrToolAdmission, events=%#v", err, events)
	}
}

func TestGeminiOutputDispatchGateResolvesAdvertisedTool(t *testing.T) {
	admission, err := admitGeminiTools(map[string]any{"tools": []any{map[string]any{"functionDeclarations": []any{
		map[string]any{"name": "lookup", "parametersJsonSchema": map[string]any{"type": "object"}},
	}}}})
	if err != nil {
		t.Fatalf("admitGeminiTools() error = %v", err)
	}
	events, err := runGeminiTestStreamWithReq(t, StreamRequest{ModelCallID: "call-1", ToolAdmission: admission},
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{}}}]},"finishReason":"STOP"}]}`+"\n\n")
	if err != nil {
		t.Fatalf("streamGeminiEvents() error = %v", err)
	}
	if countDispatchedTools(events) != 1 {
		t.Fatalf("dispatched tool count = %d, events=%#v", countDispatchedTools(events), events)
	}
	for _, event := range events {
		if event.Kind == ModelEventKindToolLikeCompleted && (event.ToolInvocation == nil || event.ToolInvocation.ToolName != "lookup") {
			t.Fatalf("gemini tool dispatch name = %#v, want lookup", event.ToolInvocation)
		}
	}
}

func TestOpenAIChatOutputDispatchGateBlocksUnadvertisedTool(t *testing.T) {
	_, err := runOpenAIChatGateStream(t,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"GhostTool","arguments":""}}]},"finish_reason":"tool_calls"}]}`+"\n\n",
		[]any{map[string]any{"type": "function", "function": map[string]any{"name": "Read", "parameters": map[string]any{"type": "object"}}}})
	if !errors.Is(err, ErrToolAdmission) {
		t.Fatalf("streamChatCompletions() error = %v, want ErrToolAdmission", err)
	}
}

func TestOpenAIChatOutputDispatchGateResolvesAdvertisedTool(t *testing.T) {
	events, err := runOpenAIChatGateStream(t,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"Read","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`+"\n\n",
		[]any{map[string]any{"type": "function", "function": map[string]any{"name": "Read", "parameters": map[string]any{"type": "object"}}}})
	if err != nil {
		t.Fatalf("streamChatCompletions() error = %v", err)
	}
	if countDispatchedTools(events) != 1 {
		t.Fatalf("dispatched tool count = %d, events=%#v", countDispatchedTools(events), events)
	}
	for _, event := range events {
		if event.Kind == ModelEventKindToolLikeCompleted && (event.ToolInvocation == nil || event.ToolInvocation.ToolName != "Read") {
			t.Fatalf("openai chat tool dispatch name = %#v, want Read", event.ToolInvocation)
		}
	}
}

func TestOpenAIResponsesOutputDispatchGateBlocksUnadvertisedTool(t *testing.T) {
	events, err := runOpenAIResponsesGateStream(t, []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"in_progress","call_id":"call_1","name":"GhostTool"}}`,
	}, []json.RawMessage(nil), []any{map[string]any{"type": "function", "name": "Read", "parameters": map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}}})
	if !errors.Is(err, ErrToolAdmission) {
		t.Fatalf("streamResponses() error = %v, want ErrToolAdmission, events=%#v", err, events)
	}
}

func TestOpenAIResponsesOutputDispatchGateResolvesSanitizedMCPSourceName(t *testing.T) {
	events, err := runOpenAIResponsesGateStream(t, []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"in_progress","call_id":"call_1","name":"mcp_tool_unsafe"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"mcp_tool_unsafe","arguments":"{}"}}`,
		`{"type":"response.completed","response":{"id":"resp_complete","model":"gpt-test","status":"completed"}}`,
	}, []json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"mcp tool/unsafe","parameters":{"type":"object","properties":{},"required":[]}}}`)},
		[]any{map[string]any{"type": "function", "name": "mcp_tool_unsafe", "parameters": map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}}})
	if err != nil {
		t.Fatalf("streamResponses() error = %v", err)
	}
	if countDispatchedTools(events) != 1 {
		t.Fatalf("dispatched tool count = %d, events=%#v", countDispatchedTools(events), events)
	}
	for _, event := range events {
		if event.Kind == ModelEventKindToolLikeCompleted && (event.ToolInvocation == nil || event.ToolInvocation.ToolName != "mcp tool/unsafe") {
			t.Fatalf("responses MCP dispatch name = %#v, want canonical source name", event.ToolInvocation)
		}
	}
}

func TestOpenAIResponsesOutputDispatchGateRejectsUnadvertisedHostedImage(t *testing.T) {
	events, err := runOpenAIResponsesGateStream(t, []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"img_1","type":"image_generation_call","status":"in_progress"}}`,
	}, []json.RawMessage(nil), []any{map[string]any{"type": "function", "name": "Read", "parameters": map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}}})
	if !errors.Is(err, ErrToolAdmission) {
		t.Fatalf("streamResponses() error = %v, want ErrToolAdmission, events=%#v", err, events)
	}
}

func countDispatchedTools(events []ModelEvent) int {
	count := 0
	for _, event := range events {
		if event.Kind == ModelEventKindToolLikeCompleted {
			count++
		}
	}
	return count
}

func runGeminiTestStreamWithReq(t *testing.T, req StreamRequest, body string) ([]ModelEvent, error) {
	t.Helper()
	adapter := &GeminiAdapter{}
	response := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	watchdogCtx, watchdog := newProviderStreamIdleWatchdog(context.Background(), time.Minute)
	defer watchdog.Stop()
	_ = watchdogCtx
	var events []ModelEvent
	_, _, _, _, _, err := adapter.streamGeminiEvents(response, req, "gemini-test", time.Now(), watchdog, func(event ModelEvent) error {
		events = append(events, event)
		return nil
	})
	return events, err
}

func runOpenAIChatGateStream(t *testing.T, rawSSE string, tools []any) ([]ModelEvent, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, rawSSE)
	}))
	defer server.Close()
	adapter := &OpenAIAdapter{client: server.Client()}
	var events []ModelEvent
	err := adapter.streamChatCompletions(context.Background(), StreamRequest{
		RequestID:          "request-chat-gate",
		ModelCallID:        "call-chat-gate",
		OpenAIEndpoint:     modelchannel.OpenAIEndpointChatCompletions,
		OpenAIRequestGroup: modelchannel.ProtocolGroupChatCompletions,
		RequestBodyOverride: map[string]any{
			"model":    "gpt-test",
			"messages": []any{},
			"stream":   true,
			"tools":    tools,
		},
	}, server.URL, "test-key", "gpt-test", 0, false, func(event ModelEvent) error {
		events = append(events, event)
		return nil
	})
	return events, err
}

func runOpenAIResponsesGateStream(t *testing.T, rawEvents []string, reqTools []json.RawMessage, overrideTools []any) ([]ModelEvent, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range rawEvents {
			if event == "[DONE]" {
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
		}
	}))
	defer server.Close()
	adapter := &OpenAIAdapter{client: server.Client()}
	var events []ModelEvent
	err := adapter.streamResponses(context.Background(), StreamRequest{
		RequestID:      "request-responses-gate",
		ModelCallID:    "call-responses-gate",
		OpenAIEndpoint: "/responses",
		Tools:          reqTools,
		RequestBodyOverride: map[string]any{
			"model":  "gpt-test",
			"input":  []any{},
			"stream": true,
			"tools":  overrideTools,
		},
	}, server.URL, "test-key", "gpt-test", 0, false, func(event ModelEvent) error {
		events = append(events, event)
		return nil
	})
	return events, err
}
