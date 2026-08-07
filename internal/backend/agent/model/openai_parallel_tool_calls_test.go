package modeladapter

import "testing"

// TestOpenAIModelSupportsParallelToolCalls 验证只有已确认的 gpt-5.6 系列模型
// 才开启 Responses parallel_tool_calls；其余模型保持原行为。
func TestOpenAIModelSupportsParallelToolCalls(t *testing.T) {
	tests := []struct {
		modelID string
		want    bool
	}{
		{modelID: "gpt-5.6", want: true},
		{modelID: "gpt-5.6-codex", want: true},
		{modelID: "GPT-5.6", want: true},
		{modelID: "  gpt-5.6-mini  ", want: true},
		{modelID: "gpt-5.1", want: false},
		{modelID: "gpt-5", want: false},
		{modelID: "gpt-6", want: false},
		{modelID: "claude-sonnet-5", want: false},
		{modelID: "", want: false},
	}
	for _, tc := range tests {
		got := openAIModelSupportsParallelToolCalls(tc.modelID)
		if got != tc.want {
			t.Fatalf("openAIModelSupportsParallelToolCalls(%q) = %v, want %v", tc.modelID, got, tc.want)
		}
	}
}

// TestApplyOpenAIParallelToolCalls 验证 body 写入规则：
//   - 仅 gpt-5.6 + Responses 请求（含 input）+ 非空 tools 才写入 parallel_tool_calls:true
//   - 显式 extra params 已存在的值不被覆盖
//   - 非 Responses 请求 / 空 tools / 非 gpt-5.6 模型均不写入
func TestApplyOpenAIParallelToolCalls(t *testing.T) {
	responsesBody := func() map[string]any {
		return map[string]any{
			"model": "gpt-5.6",
			"input": []any{},
			"tools": []map[string]any{{"type": "function"}},
		}
	}

	// gpt-5.6 Responses + 非空 tools → 写入
	body := responsesBody()
	applyOpenAIParallelToolCalls(body, "gpt-5.6")
	if body["parallel_tool_calls"] != true {
		t.Fatalf("parallel_tool_calls = %v, want true", body["parallel_tool_calls"])
	}

	// 显式 extra params 已设置 → 不覆盖
	body = responsesBody()
	body["parallel_tool_calls"] = false
	applyOpenAIParallelToolCalls(body, "gpt-5.6")
	if body["parallel_tool_calls"] != false {
		t.Fatalf("parallel_tool_calls = %v, want explicit false preserved", body["parallel_tool_calls"])
	}

	// 非 Responses 请求（无 input）→ 不写入
	body = map[string]any{"model": "gpt-5.6", "messages": []any{}, "tools": []map[string]any{}}
	applyOpenAIParallelToolCalls(body, "gpt-5.6")
	if _, ok := body["parallel_tool_calls"]; ok {
		t.Fatal("parallel_tool_calls written on non-Responses request")
	}

	// 空 tools → 不写入
	body = map[string]any{"model": "gpt-5.6", "input": []any{}, "tools": []map[string]any{}}
	applyOpenAIParallelToolCalls(body, "gpt-5.6")
	if _, ok := body["parallel_tool_calls"]; ok {
		t.Fatal("parallel_tool_calls written with empty tools")
	}

	// 非 gpt-5.6 模型 → 不写入
	body = responsesBody()
	applyOpenAIParallelToolCalls(body, "gpt-6")
	if _, ok := body["parallel_tool_calls"]; ok {
		t.Fatal("parallel_tool_calls written for non-gpt-5.6 model")
	}
}
