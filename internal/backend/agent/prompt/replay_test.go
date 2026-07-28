package promptengine

import (
	"reflect"
	"strings"
	"testing"

	"cursor/gen/agentv1"
)

func TestBuildUserMessageReplayMessageIncludesSelectedCursorCommands(t *testing.T) {
	message, ok := BuildUserMessageReplayMessage(&agentv1.UserMessage{
		Text: "/init",
		SelectedContext: &agentv1.SelectedContext{
			CursorCommands: []*agentv1.SelectedCursorCommand{
				{Name: "init", Content: "Analyze the repository and create AGENTS.md."},
				{Name: `review"<&`, Content: "Review the implementation."},
			},
		},
	})
	if !ok {
		t.Fatal("BuildUserMessageReplayMessage() returned ok=false")
	}

	want := strings.Join([]string{
		"<user_query>\n/init\n</user_query>",
		"<cursor_commands>\n" +
			"<cursor_command name=\"init\">\nAnalyze the repository and create AGENTS.md.\n</cursor_command>\n\n" +
			"<cursor_command name=\"review&quot;&lt;&amp;\">\nReview the implementation.\n</cursor_command>\n" +
			"</cursor_commands>",
	}, "\n\n")
	if message.Role != "user" || message.Content != want {
		t.Fatalf("message = %#v, want content %q", message, want)
	}
}

func TestBuildUserMessageReplayMessageSkipsEmptyCursorCommandsAndKeepsOrder(t *testing.T) {
	message, ok := BuildUserMessageReplayMessage(&agentv1.UserMessage{
		Text: "run commands",
		SelectedContext: &agentv1.SelectedContext{
			CursorCommands: []*agentv1.SelectedCursorCommand{
				nil,
				{Name: "empty", Content: "  "},
				{Content: "First command."},
				{Name: "second", Content: "Second command."},
			},
		},
	})
	if !ok {
		t.Fatal("BuildUserMessageReplayMessage() returned ok=false")
	}

	first := strings.Index(message.Content, "First command.")
	second := strings.Index(message.Content, "Second command.")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("cursor command order was not preserved: %q", message.Content)
	}
	if strings.Contains(message.Content, "empty") {
		t.Fatalf("empty cursor command was not skipped: %q", message.Content)
	}
	if !strings.Contains(message.Content, "<cursor_command>\nFirst command.\n</cursor_command>") {
		t.Fatalf("unnamed cursor command was not rendered safely: %q", message.Content)
	}
}

func TestBuildReplayMessagesFromPendingAssistantOutputsKeepsTextAndToolCallInOneAssistantTurn(t *testing.T) {
	raw := `{
		"id":"1",
		"role":"assistant",
		"content":[
			{"type":"reasoning","text":"I need to update service.go.","signature":"reasoning-signature"},
			{"type":"text","text":"Now let me pass stream.Mode in service.go."},
			{"type":"tool-call","toolCallId":"call_1","toolName":"PatchEdit","args":{"path":"/workspace/service.go","old_string":"old","new_string":"new"},"result":{"success":{"path":"/workspace/service.go"}}}
		]
	}`

	first := BuildReplayMessagesFromPendingAssistantOutputs([]string{raw})
	if len(first) != 2 {
		t.Fatalf("message count = %d, want 2 (assistant tool-call turn plus tool result): %#v", len(first), first)
	}

	assistant := first[0]
	if assistant.Role != "assistant" {
		t.Fatalf("assistant role = %q, want assistant", assistant.Role)
	}
	if assistant.Content != "Now let me pass stream.Mode in service.go." {
		t.Fatalf("assistant content = %q", assistant.Content)
	}
	if assistant.ReasoningContent != "I need to update service.go." {
		t.Fatalf("assistant reasoning = %q", assistant.ReasoningContent)
	}
	if assistant.ReasoningSignature != "reasoning-signature" {
		t.Fatalf("assistant reasoning signature = %q", assistant.ReasoningSignature)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant tool calls = %d, want 1", len(assistant.ToolCalls))
	}
	if assistant.ToolCalls[0].ID != "call_1" || assistant.ToolCalls[0].Function.Name != "PatchEdit" {
		t.Fatalf("assistant tool call = %#v", assistant.ToolCalls[0])
	}

	toolResult := first[1]
	if toolResult.Role != "tool" || toolResult.ToolCallID != "call_1" || toolResult.Name != "PatchEdit" {
		t.Fatalf("tool result = %#v", toolResult)
	}

	second := BuildReplayMessagesFromPendingAssistantOutputs([]string{raw})
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("rebuilding the same pending output changed messages:\nfirst: %#v\nsecond: %#v", first, second)
	}
}
