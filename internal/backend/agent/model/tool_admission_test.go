package modeladapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestAdmitOpenAIToolsQuarantinesOnlyMalformedDescriptors(t *testing.T) {
	body := map[string]any{
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": "good",
					"parameters": map[string]any{
						"type": "object", "properties": map[string]any{}, "required": []any{},
					},
				},
			},
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": "bad",
					"parameters": map[string]any{
						"type": "object", "required": "path",
					},
				},
			},
		},
		"tool_choice": "auto",
	}
	admission, err := admitOpenAITools(body, false, nil)
	if err != nil {
		t.Fatalf("admitOpenAITools() error = %v", err)
	}
	tools := body["tools"].([]any)
	if len(tools) != 1 || admission.admitted != 1 || admission.quarantined != 1 {
		t.Fatalf("admission result tools=%#v admitted=%d quarantined=%d", tools, admission.admitted, admission.quarantined)
	}
	if source, ok := admission.ResolveFunction("good"); !ok || source != "good" {
		t.Fatalf("good tool mapping = %q/%v", source, ok)
	}
}

func TestAdmitOpenAIToolsRejectsForcedQuarantinedTool(t *testing.T) {
	body := map[string]any{
		"tools": []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "bad", "parameters": map[string]any{"type": "object", "required": "path"},
			},
		}},
		"tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": "bad"}},
	}
	_, err := admitOpenAITools(body, false, nil)
	if !errors.Is(err, ErrToolAdmission) {
		t.Fatalf("forced quarantined tool error = %v, want ErrToolAdmission", err)
	}
}

func TestAdmitOpenAIToolsDowngradesInvalidStrictSchemaInsteadOfDropping(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []any{"path"}}
	body := map[string]any{"tools": []any{map[string]any{"type": "function", "name": "strict_tool", "parameters": schema, "strict": true}}}
	admission, err := admitOpenAITools(body, true, nil)
	if err != nil {
		t.Fatalf("admitOpenAITools() error = %v", err)
	}
	if admission.admitted != 1 || admission.quarantined != 0 || admission.downgradedStrict != 1 {
		t.Fatalf("invalid strict schema was not downgraded: %#v", admission)
	}
	tool := body["tools"].([]any)[0].(map[string]any)
	if _, exists := tool["strict"]; exists {
		t.Fatalf("strict flag was not removed on downgrade: %#v", tool)
	}
	if _, exists := schema["additionalProperties"]; exists {
		t.Fatalf("admission semantically modified schema parameters: %#v", schema)
	}
}

func TestAdmitOpenAIToolsDowngradesStrictSchemaWithOptionalProperties(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
			"mode": map[string]any{"type": "string"},
		},
		"required":            []any{"path"},
		"additionalProperties": false,
	}
	body := map[string]any{"tools": []any{map[string]any{"type": "function", "name": "strict_tool", "parameters": schema, "strict": true}}}
	admission, err := admitOpenAITools(body, true, nil)
	if err != nil {
		t.Fatalf("admitOpenAITools() error = %v", err)
	}
	if admission.admitted != 1 || admission.downgradedStrict != 1 {
		t.Fatalf("optional-property strict schema admission = %#v", admission)
	}
}

func TestAdmitOpenAIToolsDowngradesStrictSchemaMissingRequiredKey(t *testing.T) {
	schema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"path": map[string]any{"type": "string"}},
		"additionalProperties": false,
	}
	body := map[string]any{"tools": []any{map[string]any{"type": "function", "name": "strict_tool", "parameters": schema, "strict": true}}}
	admission, err := admitOpenAITools(body, true, nil)
	if err != nil {
		t.Fatalf("admitOpenAITools() error = %v", err)
	}
	if admission.admitted != 1 || admission.downgradedStrict != 1 {
		t.Fatalf("missing-required strict schema admission = %#v", admission)
	}
}

func TestValidateToolSchemaStructureRejectsOversizedRequiredArray(t *testing.T) {
	required := make([]any, maxSchemaRequiredEntries+1)
	for i := range required {
		required[i] = fmt.Sprintf("property-%d", i)
	}
	if err := validateToolSchemaStructure(map[string]any{"type": "object", "properties": map[string]any{}, "required": required}); err == nil {
		t.Fatal("oversized required array was accepted")
	}
}

func TestAdmitOpenAIResponsesToolsMapsSanitizedNameToSource(t *testing.T) {
	raw := []json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"mcp tool/unsafe","parameters":{"type":"object","properties":{},"required":[]}}}`)}
	body := map[string]any{"tools": []any{map[string]any{"type": "function", "name": "mcp_tool_unsafe", "parameters": map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}}}}
	admission, err := admitOpenAITools(body, true, raw)
	if err != nil {
		t.Fatalf("admitOpenAITools() error = %v", err)
	}
	if source, ok := admission.ResolveFunction("mcp_tool_unsafe"); !ok || source != "mcp tool/unsafe" {
		t.Fatalf("sanitized mapping = %q/%v", source, ok)
	}
}

func TestAdmitAnthropicToolsQuarantinesInvalidDescriptors(t *testing.T) {
	body := map[string]any{"tools": []any{
		map[string]any{"name": "good", "description": "ok", "input_schema": map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}},
		map[string]any{"name": "bad", "input_schema": map[string]any{"type": "object", "required": "path"}},
	}}
	admission, err := admitAnthropicTools(body)
	if err != nil {
		t.Fatalf("admitAnthropicTools() error = %v", err)
	}
	tools := body["tools"].([]any)
	if len(tools) != 1 || admission.admitted != 1 || admission.quarantined != 1 {
		t.Fatalf("anthropic admission result tools=%#v admitted=%d quarantined=%d", tools, admission.admitted, admission.quarantined)
	}
}

func TestAdmitGeminiToolsQuarantinesInvalidDeclarations(t *testing.T) {
	body := map[string]any{"tools": []any{map[string]any{"functionDeclarations": []any{
		map[string]any{"name": "good", "parametersJsonSchema": map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}},
		map[string]any{"name": "bad", "parametersJsonSchema": map[string]any{"type": "object", "required": "path"}},
	}}}}
	admission, err := admitGeminiTools(body)
	if err != nil {
		t.Fatalf("admitGeminiTools() error = %v", err)
	}
	group := body["tools"].([]any)[0].(map[string]any)
	declarations := group["functionDeclarations"].([]any)
	if len(declarations) != 1 || admission.admitted != 1 || admission.quarantined != 1 {
		t.Fatalf("gemini admission result declarations=%#v admitted=%d quarantined=%d", declarations, admission.admitted, admission.quarantined)
	}
}

func TestAdmitOpenAIToolsRewritesNamedChoiceToAlias(t *testing.T) {
	body := map[string]any{
		"tools": []any{map[string]any{
			"type": "function", "name": "mcp_tool_unsafe",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}},
		}},
		"tool_choice": map[string]any{"type": "function", "name": "mcp_tool_unsafe"},
	}
	raw := []json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"mcp tool/unsafe","parameters":{"type":"object","properties":{},"required":[]}}}`)}
	if _, err := admitOpenAITools(body, true, raw); err != nil {
		t.Fatalf("admitOpenAITools() error = %v", err)
	}
	choice := body["tool_choice"].(map[string]any)
	if strings.TrimSpace(fmt.Sprint(choice["name"])) != "mcp_tool_unsafe" {
		t.Fatalf("named choice was not rewritten to provider alias: %#v", choice)
	}
}

func TestAdmitOpenAIToolsRequiresStrictClosedSchema(t *testing.T) {
	body := map[string]any{"tools": []any{map[string]any{"type": "function", "name": "strict_tool", "parameters": map[string]any{
		"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []any{"path"}, "additionalProperties": false,
	}, "strict": true}}}
	admission, err := admitOpenAITools(body, true, nil)
	if err != nil || admission.admitted != 1 {
		t.Fatalf("valid strict schema rejected: admission=%#v err=%v", admission, err)
	}
}
