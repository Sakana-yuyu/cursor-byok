package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
)

func TestParseManualCompactionDirectiveSupportsCursorSummarize(t *testing.T) {
	tests := []struct {
		name            string
		text            string
		wantInstruction string
		want            bool
	}{
		{name: "compact removed", text: "/compact", want: false},
		{name: "compact instruction removed", text: "/compact keep deployment details", want: false},
		{name: "summarize", text: "/summarize", want: true},
		{name: "summarize instruction", text: "/summarize keep failing tests", wantInstruction: "keep failing tests", want: true},
		{name: "surrounding whitespace", text: "  /summarize  ", want: true},
		{name: "similar command", text: "/summarized", want: false},
		{name: "ordinary text", text: "please summarize this file", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instruction, ok := parseManualCompactionDirective(test.text)
			if ok != test.want || instruction != test.wantInstruction {
				t.Fatalf("parseManualCompactionDirective(%q) = (%q, %v), want (%q, %v)", test.text, instruction, ok, test.wantInstruction, test.want)
			}
		})
	}
}

func TestParseManualCompactionRequestRecognizesCursorSummarizeCommand(t *testing.T) {
	instruction, ok := parseManualCompactionRequest(&agentv1.UserMessage{
		SelectedContext: &agentv1.SelectedContext{
			CursorCommands: []*agentv1.SelectedCursorCommand{
				{Name: "glass-action-summarize", Content: "/summarize"},
			},
		},
	})
	if !ok || instruction != "" {
		t.Fatalf("parseManualCompactionRequest() = (%q, %v), want empty instruction and true", instruction, ok)
	}
}

func TestParseManualCompactionRequestIgnoresSummarizeMetadataWhenUserTextIsPresent(t *testing.T) {
	instruction, ok := parseManualCompactionRequest(&agentv1.UserMessage{
		Text: "why does /summarize not work?",
		SelectedContext: &agentv1.SelectedContext{
			CursorCommands: []*agentv1.SelectedCursorCommand{
				{Name: "glass-action-summarize", Content: "/summarize"},
			},
		},
	})
	if ok || instruction != "" {
		t.Fatalf("parseManualCompactionRequest() = (%q, %v), want empty instruction and false", instruction, ok)
	}
}

func TestParseManualCompactionRequestIgnoresOrdinaryCursorCommands(t *testing.T) {
	instruction, ok := parseManualCompactionRequest(&agentv1.UserMessage{
		Text: "review this implementation",
		SelectedContext: &agentv1.SelectedContext{
			CursorCommands: []*agentv1.SelectedCursorCommand{
				nil,
				{Name: "review", Content: "Review the implementation."},
			},
		},
	})
	if ok || instruction != "" {
		t.Fatalf("parseManualCompactionRequest() = (%q, %v), want empty instruction and false", instruction, ok)
	}
}

func TestDecodeInboundIntentMapsRunRequestSummarizeActionToManualCompaction(t *testing.T) {
	service := &Service{debug: newDebugRecorder("", nil, nil)}
	intent, err := service.decodeInboundIntent(
		"summarize-request",
		newRunRequestMessage(newSummarizeConversationAction()),
		"run_request",
	)
	if err != nil {
		t.Fatalf("decodeInboundIntent() error = %v", err)
	}
	if intent.Kind != "run" || !intent.StartsRun {
		t.Fatalf("decodeInboundIntent() kind = %q, starts_run = %v, want run and true", intent.Kind, intent.StartsRun)
	}
	if intent.UserMessage != nil {
		t.Fatalf("decodeInboundIntent() user_message = %#v, want nil", intent.UserMessage)
	}
	if !intent.ManualCompaction.Requested || intent.ManualCompaction.Instruction != "" {
		t.Fatalf("decodeInboundIntent() manual_compaction = %#v, want requested with empty instruction", intent.ManualCompaction)
	}
}

func TestResolveInboundManualCompactionSupportsStandaloneConversationAction(t *testing.T) {
	directive := resolveInboundManualCompaction(&agentv1.AgentClientMessage{
		Message: &agentv1.AgentClientMessage_ConversationAction{
			ConversationAction: newSummarizeConversationAction(),
		},
	}, nil)
	if !directive.Requested || directive.Instruction != "" {
		t.Fatalf("resolveInboundManualCompaction() = %#v, want requested with empty instruction", directive)
	}
}

func TestResolveInboundManualCompactionIgnoresOtherStandaloneActions(t *testing.T) {
	directive := resolveInboundManualCompaction(&agentv1.AgentClientMessage{
		Message: &agentv1.AgentClientMessage_ConversationAction{
			ConversationAction: &agentv1.ConversationAction{
				Action: &agentv1.ConversationAction_CancelAction{CancelAction: &agentv1.CancelAction{}},
			},
		},
	}, nil)
	if directive.Requested {
		t.Fatalf("resolveInboundManualCompaction() = %#v, want not requested", directive)
	}
}

func TestDecodeInboundIntentDoesNotMapOrdinaryRunActionsToManualCompaction(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "ordinary message", text: "review this implementation"},
		{name: "compact command removed", text: "/compact"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &Service{debug: newDebugRecorder("", nil, nil)}
			intent, err := service.decodeInboundIntent(
				"ordinary-request",
				newRunRequestMessage(&agentv1.ConversationAction{
					Action: &agentv1.ConversationAction_UserMessageAction{
						UserMessageAction: &agentv1.UserMessageAction{
							UserMessage: &agentv1.UserMessage{Text: test.text},
						},
					},
				}),
				"run_request",
			)
			if err != nil {
				t.Fatalf("decodeInboundIntent() error = %v", err)
			}
			if intent.ManualCompaction.Requested {
				t.Fatalf("decodeInboundIntent() manual_compaction = %#v, want not requested", intent.ManualCompaction)
			}
		})
	}
}

func TestSummarizeConversationActionStartsRun(t *testing.T) {
	if !conversationActionStartsRun(newSummarizeConversationAction()) {
		t.Fatal("conversationActionStartsRun(summarize) = false, want true")
	}
	if conversationActionStartsRun(&agentv1.ConversationAction{
		Action: &agentv1.ConversationAction_CancelAction{CancelAction: &agentv1.CancelAction{}},
	}) {
		t.Fatal("conversationActionStartsRun(cancel) = true, want false")
	}
}

func TestStreamManualCompactionDirectiveUsesStructuredRequest(t *testing.T) {
	stream := &ActiveStream{
		LatestUserText: "visible user text",
		ManualCompaction: manualCompactionDirective{
			Requested:   true,
			Instruction: "keep decisions",
		},
	}
	instruction, ok := streamManualCompactionDirective(stream)
	if !ok || instruction != "keep decisions" {
		t.Fatalf("streamManualCompactionDirective() = (%q, %v), want (%q, true)", instruction, ok, "keep decisions")
	}
}

func newRunRequestMessage(action *agentv1.ConversationAction) *agentv1.AgentClientMessage {
	conversationID := "test-conversation"
	return &agentv1.AgentClientMessage{
		Message: &agentv1.AgentClientMessage_RunRequest{
			RunRequest: &agentv1.AgentRunRequest{
				ConversationId: &conversationID,
				Action:         action,
			},
		},
	}
}

func newSummarizeConversationAction() *agentv1.ConversationAction {
	return &agentv1.ConversationAction{
		Action: &agentv1.ConversationAction_SummarizeAction{
			SummarizeAction: &agentv1.SummarizeAction{},
		},
	}
}
