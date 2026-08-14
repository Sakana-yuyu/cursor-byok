package modeladapter

import (
	"strings"
	"testing"
)

func TestGeminiMessagePartsRejectsMalformedToolArguments(t *testing.T) {
	_, err := geminiMessageParts(Message{
		Role: "assistant",
		ToolCalls: []ToolCallDescriptor{{
			Function: ToolCallFunctionShape{
				Name:      "Read",
				Arguments: `{"path": "e:\broken`,
			},
		}},
	})
	if err == nil {
		t.Fatal("expected malformed tool arguments to fail")
	}
	if !strings.Contains(err.Error(), "Read") {
		t.Fatalf("expected tool name in error, got: %v", err)
	}
}

func TestGeminiMessagePartsAcceptsValidToolArguments(t *testing.T) {
	parts, err := geminiMessageParts(Message{
		Role: "assistant",
		ToolCalls: []ToolCallDescriptor{{
			Function: ToolCallFunctionShape{
				Name:      "Read",
				Arguments: `{"path":"README.md"}`,
			},
		}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	call, ok := parts[0]["functionCall"].(map[string]any)
	if !ok {
		t.Fatalf("expected functionCall part, got %#v", parts[0])
	}
	args, ok := call["args"].(map[string]any)
	if !ok {
		t.Fatalf("expected args map, got %#v", call["args"])
	}
	if args["path"] != "README.md" {
		t.Fatalf("unexpected args: %#v", args)
	}
}
