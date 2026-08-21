package modeladapter

import "testing"

func TestOpenAIExtraParamsCannotOverrideRuntimeOwnedFields(t *testing.T) {
	body := map[string]any{
		"model":      "runtime-model",
		"stream":     true,
		"messages":   []any{"runtime-message"},
		"tools":      []any{"runtime-tool"},
		"max_tokens": 128,
		"reasoning":  map[string]any{"effort": "disabled"},
	}
	err := ApplyOpenAIExtraParams(body, true, `{
		"MODEL":"attacker-model",
		"model":"other-model",
		"stream":false,
		"messages":["replacement"],
		"input":["replacement"],
		"tools":[],
		"max_tokens":999999,
		"max_completion_tokens":999999,
		"max_output_tokens":999999,
		"reasoning":{"effort":"high"},
		"reasoning_effort":"high",
		"thinking":{"type":"enabled"},
		"enable_thinking":true,
		"temperature":0.2,
		"parallel_tool_calls":false,
		"vendor_extension":"allowed"
	}`)
	if err != nil {
		t.Fatalf("ApplyOpenAIExtraParams() error = %v", err)
	}
	if body["model"] != "runtime-model" || body["stream"] != true || body["max_tokens"] != 128 {
		t.Fatalf("runtime-owned fields were overridden: %#v", body)
	}
	for _, absent := range []string{"MODEL", "input", "max_completion_tokens", "max_output_tokens", "reasoning_effort", "thinking", "enable_thinking"} {
		if _, exists := body[absent]; exists {
			t.Fatalf("protected field %q was introduced: %#v", absent, body)
		}
	}
	if body["temperature"] != 0.2 || body["parallel_tool_calls"] != false || body["vendor_extension"] != "allowed" {
		t.Fatalf("ordinary extension fields were not preserved: %#v", body)
	}
}

func TestAnthropicExtraParamsCannotOverrideRuntimeOwnedFields(t *testing.T) {
	body := map[string]any{
		"model":      "runtime-model",
		"stream":     true,
		"system":     []any{"runtime-system"},
		"messages":   []any{"runtime-message"},
		"tools":      []any{"runtime-tool"},
		"max_tokens": 512,
		"thinking":   map[string]any{"type": "disabled"},
	}
	err := ApplyAnthropicExtraParams(body, true, `{
		"model":"other-model",
		"stream":false,
		"system":"replacement",
		"messages":[],
		"tools":[],
		"max_tokens":999999,
		"thinking":{"type":"enabled"},
		"output_config":{"effort":"high"},
		"temperature":0.4,
		"top_p":0.9,
		"vendor_extension":"allowed"
	}`)
	if err != nil {
		t.Fatalf("ApplyAnthropicExtraParams() error = %v", err)
	}
	if body["model"] != "runtime-model" || body["stream"] != true || body["max_tokens"] != 512 {
		t.Fatalf("runtime-owned fields were overridden: %#v", body)
	}
	if _, exists := body["output_config"]; exists {
		t.Fatalf("protected output_config was introduced: %#v", body)
	}
	if body["temperature"] != 0.4 || body["top_p"] != 0.9 || body["vendor_extension"] != "allowed" {
		t.Fatalf("ordinary extension fields were not preserved: %#v", body)
	}
}
