package modeladapter

import (
	"encoding/json"
	"testing"
)

func TestNormalizeOpenAIChatToolsRequiredNull(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"function",
		"function":{
			"name":"Read",
			"parameters":{
				"type":"object",
				"required":null,
				"properties":{
					"path":{"type":"string"},
					"options":{"type":"object","required":null,"properties":{"limit":{"type":"number"}}}
				}
			}
		}
	}`)

	tools, err := normalizeOpenAIChatTools([]json.RawMessage{raw})
	if err != nil {
		t.Fatalf("normalizeOpenAIChatTools returned error: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(tools[0], &got); err != nil {
		t.Fatalf("decode normalized tool: %v", err)
	}
	parameters := got["function"].(map[string]any)["parameters"].(map[string]any)
	if required, ok := parameters["required"].([]any); !ok || len(required) != 0 {
		t.Fatalf("expected top-level required to be empty array, got %#v", parameters["required"])
	}
	options := parameters["properties"].(map[string]any)["options"].(map[string]any)
	if required, ok := options["required"].([]any); !ok || len(required) != 0 {
		t.Fatalf("expected nested required to be empty array, got %#v", options["required"])
	}
}

func TestNormalizeOpenAIResponsesToolsRequiredNull(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"function",
		"function":{
			"name":"Shell",
			"parameters":{
				"type":"object",
				"required":null,
				"properties":{"command":{"type":"string"}}
			}
		}
	}`)

	tools, err := normalizeOpenAIResponsesTools([]json.RawMessage{raw})
	if err != nil {
		t.Fatalf("normalizeOpenAIResponsesTools returned error: %v", err)
	}
	parameters := tools[0]["parameters"].(map[string]any)
	if required, ok := parameters["required"].([]any); !ok || len(required) != 0 {
		t.Fatalf("expected required to be empty array, got %#v", parameters["required"])
	}
}

func TestNormalizeOpenAIToolSchemaRequiredPreservesValidArray(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"command"},
	}
	normalizeOpenAIToolSchemaRequired(schema)
	if required, ok := schema["required"].([]any); !ok || len(required) != 1 || required[0] != "command" {
		t.Fatalf("expected valid required array to be preserved, got %#v", schema["required"])
	}
}
