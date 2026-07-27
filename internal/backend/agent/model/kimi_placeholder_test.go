package modeladapter

import "testing"

// TestReasoningOnlyAssistantFilteredAsPlaceholder 验证 Kimi 思考-only 残缺回合
// （仅有 reasoning_content、无正文、无工具调用）被 sanitizeProviderMessages 过滤。
func TestReasoningOnlyAssistantFilteredAsPlaceholder(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "读取"},
		{Role: "assistant", Content: "", ReasoningContent: "用户说读取，意思是要我读取 skill.md。"}, // 残缺回合，应被过滤
		{Role: "user", Content: "继续"},
	}
	out := sanitizeProviderMessages(msgs)
	for _, m := range out {
		if m.Role == "assistant" && m.Content == "" && len(m.ToolCalls) == 0 && len(m.ContentParts) == 0 {
			t.Fatalf("reasoning-only 空 assistant 消息未被过滤: %+v", m)
		}
	}
	// 应保留两条 user 消息
	if len(out) != 2 {
		t.Fatalf("期望过滤后剩 2 条 user 消息，实际 %d 条: %+v", len(out), out)
	}
}

// TestAssistantWithToolCallsNotFiltered 确保带工具调用的 assistant 消息不被误删。
func TestAssistantWithToolCallsNotFiltered(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "读取文件"},
		{Role: "assistant", Content: "", ReasoningContent: "我需要读取", ToolCalls: []ToolCallDescriptor{{ID: "c1", Type: "function", Function: ToolCallFunctionShape{Name: "Read"}}}},
		{Role: "tool", ToolCallID: "c1", Content: "file contents"},
	}
	out := sanitizeProviderMessages(msgs)
	hasToolCall := false
	for _, m := range out {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			hasToolCall = true
		}
	}
	if !hasToolCall {
		t.Fatalf("带工具调用的 assistant 消息被误删: %+v", out)
	}
}

// TestAssistantWithRealContentNotFiltered 确保有正文的 assistant 消息不被误删。
func TestAssistantWithRealContentNotFiltered(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: "你好！有什么可以帮你的？", ReasoningContent: "用户在打招呼"},
		{Role: "user", Content: "帮我读取文件"}, // 末尾必须是 user，否则 sanitize 会删 assistant prefill
	}
	out := sanitizeProviderMessages(msgs)
	found := false
	for _, m := range out {
		if m.Role == "assistant" && m.Content != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("有正文的 assistant 消息被误删: %+v", out)
	}
}

// TestTransientKimi400IsRetryable 验证 daoxe 偶发 400 被判定为可重试。
func TestTransientKimi400IsRetryable(t *testing.T) {
	transient := errBuildString(`openai adapter status=400 body={"error":{"message":"Invalid request for the selected model.","type":"invalid_request_error"}}`)
	if isPermanentProviderError(transient) {
		t.Fatalf("daoxe 偶发 400 'Invalid request for the selected model' 应可重试")
	}
	// 普通 400 仍应为永久错误
	normal := errBuildString(`openai adapter status=400 body={"error":{"message":"missing field foo"}}`)
	if !isPermanentProviderError(normal) {
		t.Fatalf("普通 400 应为永久错误，不重试")
	}
	// 401 仍永久
	unauth := errBuildString(`openai adapter status=401 body={"error":"unauthorized"}`)
	if !isPermanentProviderError(unauth) {
		t.Fatalf("401 应为永久错误")
	}
}

type stringErr struct{ s string }

func (e stringErr) Error() string { return e.s }

func errBuildString(s string) error { return stringErr{s} }

// TestNormalizeToolSchemaRequiredNull 验证 required:null → required:[] 的修复，
// 覆盖顶层与嵌套两种情况（Anthropic/OpenAI 路径共用此逻辑）。
func TestNormalizeToolSchemaRequiredNull(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{
			"nested": map[string]any{
				"type":     "object",
				"required": nil, // 嵌套的 null required
			},
		},
		"required": nil, // 顶层 null required
	}
	normalizeOpenAIToolSchemaRequired(schema)

	top, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("顶层 required 未被修复为数组: %#v", schema["required"])
	}
	if len(top) != 0 {
		t.Fatalf("顶层 required 应为空数组, 实际 %#v", top)
	}
	nested := schema["properties"].(map[string]any)["nested"].(map[string]any)
	if _, ok := nested["required"].([]any); !ok {
		t.Fatalf("嵌套 required 未被修复为数组: %#v", nested["required"])
	}
}

// TestNormalizeToolSchemaMissingRequired 验证 type:object 但完全缺失 required 字段时，
// 主动补空数组。防止 daoxe 等中转网关转发给上游时补 required:null 触发严格校验 400。
func TestNormalizeToolSchemaMissingRequired(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"arguments": map[string]any{
				"type": "object", // 缺失 required 的嵌套 object
				"properties": map[string]any{},
			},
		},
		// 顶层也缺失 required
	}
	normalizeOpenAIToolSchemaRequired(schema)

	if _, ok := schema["required"].([]any); !ok {
		t.Fatalf("顶层缺失的 required 未被补为数组: %#v", schema["required"])
	}
	nested := schema["properties"].(map[string]any)["arguments"].(map[string]any)
	if _, ok := nested["required"].([]any); !ok {
		t.Fatalf("嵌套 object 缺失的 required 未被补为数组: %#v", nested["required"])
	}
}

// TestNormalizeNonObjectSchemaNoRequired 确保非 object 类型不会被误加 required。
func TestNormalizeNonObjectSchemaNoRequired(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"}, // string 类型不应有 required
			"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
	normalizeOpenAIToolSchemaRequired(schema)

	props := schema["properties"].(map[string]any)
	nameSchema := props["name"].(map[string]any)
	if _, ok := nameSchema["required"]; ok {
		t.Fatalf("string 类型被误加 required: %#v", nameSchema)
	}
	arrSchema := props["tags"].(map[string]any)
	if _, ok := arrSchema["required"]; ok {
		t.Fatalf("array 类型被误加 required: %#v", arrSchema)
	}
}
