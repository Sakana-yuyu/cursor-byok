// bench_test.go 承载模型适配层请求构造热点的性能基线基准：
// 1) OpenAI Responses / Chat 请求体构造 → requestBodyToMap → apply 变换 → 最终 marshal 的整条序列化管线；
// 2) Anthropic prefix-cache canonical hash（marshal + sha256）。
// 用于量化「JSON 双序列化 + 反射变换」开销，并验证后续优化效果。
package modeladapter

import (
	"encoding/json"
	"testing"
)

// benchStreamRequestMessages 构造一份接近真实规模的对话消息列表：
// system 指令 + 多轮 user/assistant 文本 + 一次 tool 调用结果。
func benchStreamRequestMessages() []Message {
	msgs := make([]Message, 0, 9)
	msgs = append(msgs, Message{Role: "system", Content: "You are Cursor Assistant, a helpful coding agent running in a local IDE. Follow the user's instructions carefully and use the provided tools when appropriate."})
	msgs = append(msgs, Message{Role: "user", Content: "Refactor the HTTP handler in internal/server/handler.go to extract error handling into a reusable middleware. Keep the public API stable and add unit tests."})
	msgs = append(msgs, Message{Role: "assistant", Content: "Let me look at the current handler implementation first, then I'll propose the middleware structure and apply the changes."})
	msgs = append(msgs, Message{Role: "assistant", Content: "", ToolCalls: []ToolCallDescriptor{
		{ID: "call_01JZ", Type: "function", Function: ToolCallFunctionShape{Name: "read_file", Arguments: `{"path":"internal/server/handler.go"}`}},
	}})
	msgs = append(msgs, Message{Role: "tool", ToolCallID: "call_01JZ", Content: "package server\n\nfunc (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {\n\tif err := s.auth(r); err != nil {\n\t\thttp.Error(w, \"unauthorized\", http.StatusUnauthorized)\n\t\treturn\n\t}\n\t// ... 200 行业务代码\n}"})
	msgs = append(msgs, Message{Role: "assistant", Content: "I've reviewed the handler. Here's my plan: create a new middleware package, move the auth and logging concerns there, then rewrite handleGet to compose them. Let me apply the edits."})
	msgs = append(msgs, Message{Role: "user", Content: "Sounds good, go ahead. Also make sure request IDs are propagated through the middleware chain."})
	msgs = append(msgs, Message{Role: "assistant", Content: "Applied the refactor. Summary of changes:\n- new internal/server/middleware.go with auth+logging+requestID\n- handleGet now 20 lines instead of 200\n- added handler_test.go covering unauthorized and happy path"})
	msgs = append(msgs, Message{Role: "user", Content: "Great, thanks!"})
	return msgs
}

// benchOpenAIResponsesRequest 构造一个带 tools 与 reasoning 的真实 OpenAI Responses 请求。
func benchOpenAIResponsesRequest() StreamRequest {
	return StreamRequest{
		ReasoningEffort: "high",
		MaxTokens:       8192,
		OpenAIEndpoint:  "responses",
		RequestKnobs:    map[string]any{},
		Messages:        benchStreamRequestMessages(),
		Tools:           benchToolJSONMessages(),
	}
}

// benchOpenAIChatRequest 构造一个带 tools 的真实 OpenAI Chat 请求。
func benchOpenAIChatRequest() StreamRequest {
	return StreamRequest{
		ReasoningEffort:  "high",
		MaxTokens:        8192,
		OpenAIEndpoint:   "chat",
		OpenAIRequestGroup: "chat-completions",
		RequestKnobs:     map[string]any{},
		Messages:         benchStreamRequestMessages(),
		Tools:            benchToolJSONMessages(),
	}
}

// benchToolJSONMessages 构造一组真实的工具 schema JSON。
func benchToolJSONMessages() []json.RawMessage {
	raws := []string{
		`{"type":"function","function":{"name":"read_file","description":"Read a file from the workspace.","parameters":{"type":"object","properties":{"path":{"type":"string","description":"Absolute or workspace-relative path"}},"required":["path"]}}}`,
		`{"type":"function","function":{"name":"edit_file","description":"Apply an exact-match edit to a file.","parameters":{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"}},"required":["path","old_string","new_string"]}}}`,
		`{"type":"function","function":{"name":"bash","description":"Run a shell command.","parameters":{"type":"object","properties":{"command":{"type":"string"},"timeout_seconds":{"type":"integer","default":30}},"required":["command"]}}}`,
		`{"type":"function","function":{"name":"grep","description":"Search a directory for a regex.","parameters":{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"},"case_sensitive":{"type":"boolean"}},"required":["pattern"]}}}`,
	}
	out := make([]json.RawMessage, 0, len(raws))
	for _, raw := range raws {
		out = append(out, json.RawMessage(raw))
	}
	return out
}

// benchOpenAIResponsesBodyPipeline 复刻 openai_stream_responses.go 的请求体构造与
// 序列化管线（不含网络 IO）：直接构造 map → apply 变换 → 最终 marshal。
func benchOpenAIResponsesBodyPipeline(req StreamRequest, modelID string, baseURL string, manualPromptCacheKey bool) ([]byte, error) {
	overrideBody := cloneRequestBodyOverride(req.RequestBodyOverride)
	var bodyMap map[string]any
	if len(overrideBody) == 0 {
		built, err := buildOpenAIResponsesBodyMap(req, modelID, 0)
		if err != nil {
			return nil, err
		}
		bodyMap = built
	} else {
		applyOpenAIPromptCacheKeyOverride(overrideBody, req, modelID, 0)
		bodyMap = overrideBody
	}
	applyOpenAIThinkingDisable(bodyMap, req, baseURL, modelID, req.OpenAIEndpoint)
	applyOpenAIParallelToolCalls(bodyMap, modelID)
	if err := ApplyOpenAIExtraParams(bodyMap, req.OpenAIExtraParamsEnabled, req.OpenAIExtraParamsJSON); err != nil {
		return nil, err
	}
	normalizeOpenAIResponsesRequestToolSchemas(bodyMap)
	applyOpenAIResponsesCompatibility(bodyMap, baseURL, modelID, manualPromptCacheKey)
	return json.Marshal(bodyMap)
}

// benchOpenAIChatBodyPipeline 复刻 openai_stream_chat.go 的请求体构造与序列化管线。
func benchOpenAIChatBodyPipeline(req StreamRequest, modelID string, baseURL string, manualPromptCacheKey bool) ([]byte, error) {
	overrideBody := cloneRequestBodyOverride(req.RequestBodyOverride)
	var bodyMap map[string]any
	if len(overrideBody) == 0 {
		built, err := buildOpenAIChatBodyMap(req, baseURL, modelID, 0)
		if err != nil {
			return nil, err
		}
		bodyMap = built
	} else {
		if !openAIChatRequestGroupUsesCompatShape(req.OpenAIRequestGroup) {
			applyOpenAIPromptCacheKeyOverride(overrideBody, req, modelID, 0)
		}
		bodyMap = overrideBody
	}
	applyOpenAIThinkingDisable(bodyMap, req, baseURL, modelID, req.OpenAIEndpoint)
	if err := ApplyOpenAIExtraParams(bodyMap, req.OpenAIExtraParamsEnabled, req.OpenAIExtraParamsJSON); err != nil {
		return nil, err
	}
	normalizeOpenAIRequestToolSchemas(bodyMap)
	applyOpenAIChatCompletionsCompatibility(bodyMap, baseURL, modelID, manualPromptCacheKey)
	return json.Marshal(bodyMap)
}

func BenchmarkOpenAIResponsesBodyPipeline(b *testing.B) {
	req := benchOpenAIResponsesRequest()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		payload, err := benchOpenAIResponsesBodyPipeline(req, "gpt-5.6", "https://api.openai.com/v1", false)
		if err != nil {
			b.Fatalf("pipeline error: %v", err)
		}
		if len(payload) == 0 {
			b.Fatal("empty payload")
		}
	}
}

func BenchmarkOpenAIChatBodyPipeline(b *testing.B) {
	req := benchOpenAIChatRequest()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		payload, err := benchOpenAIChatBodyPipeline(req, "gpt-5.6", "https://api.openai.com/v1", false)
		if err != nil {
			b.Fatalf("pipeline error: %v", err)
		}
		if len(payload) == 0 {
			b.Fatal("empty payload")
		}
	}
}

// benchAnthropicBody 构造一份接近真实规模的 Anthropic 请求体 map。
func benchAnthropicBody() map[string]any {
	messages := make([]any, 0, len(benchStreamRequestMessages()))
	for _, msg := range benchStreamRequestMessages() {
		entry := map[string]any{"role": msg.Role}
		if len(msg.ToolCalls) > 0 {
			blocks := make([]any, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": call.ID, "name": call.Function.Name,
					"input": map[string]any{"path": "internal/server/handler.go"},
				})
			}
			entry["content"] = blocks
		} else {
			entry["content"] = []any{map[string]any{"type": "text", "text": msg.Content}}
		}
		messages = append(messages, entry)
	}
	return map[string]any{
		"model":      "claude-sonnet-4-5",
		"max_tokens": 8192,
		"system":     []any{map[string]any{"type": "text", "text": "You are Cursor Assistant."}},
		"messages":   messages,
		"tools": []any{
			map[string]any{"name": "read_file", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}},
			map[string]any{"name": "edit_file", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "old_string": map[string]any{"type": "string"}, "new_string": map[string]any{"type": "string"}}}},
			map[string]any{"name": "bash", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}}},
		},
		"stream": true,
	}
}

func BenchmarkAnthropicCanonicalHash(b *testing.B) {
	body := benchAnthropicBody()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if hash := anthropicCanonicalHash(body); len(hash) != 16 {
			b.Fatalf("unexpected hash length: %d", len(hash))
		}
	}
}

func BenchmarkAnthropicCanonicalPrefixHash(b *testing.B) {
	body := benchAnthropicBody()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if hash := anthropicCanonicalPrefixHash(body, "messages[6].content[0]"); len(hash) != 16 {
			b.Fatalf("unexpected hash length: %d", len(hash))
		}
	}
}
