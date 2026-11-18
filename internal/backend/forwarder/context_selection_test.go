package forwarder

import (
	"fmt"
	"testing"

	"cursor/gen/agentv1"
	"google.golang.org/protobuf/proto"
)

func contextSelectionMessage(t *testing.T, prewarm bool, conversationID, messageID string, model *agentv1.RequestedModel) (*agentv1.AgentClientMessage, string) {
	t.Helper()
	kind := "run_request"
	message := &agentv1.AgentClientMessage{Message: &agentv1.AgentClientMessage_RunRequest{RunRequest: &agentv1.AgentRunRequest{
		ConversationId: proto.String(conversationID), RequestedModel: model,
		Action: &agentv1.ConversationAction{Action: &agentv1.ConversationAction_UserMessageAction{UserMessageAction: &agentv1.UserMessageAction{UserMessage: &agentv1.UserMessage{MessageId: messageID, Text: "question", Mode: agentv1.AgentMode_AGENT_MODE_AGENT}}}},
	}}}
	if prewarm {
		kind = "prewarm_request"
		message = &agentv1.AgentClientMessage{Message: &agentv1.AgentClientMessage_PrewarmRequest{PrewarmRequest: &agentv1.PrewarmRequest{ConversationId: proto.String(conversationID), RequestedModel: model}}}
	}
	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	decoded := &agentv1.AgentClientMessage{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatal(err)
	}
	return decoded, kind
}

func contextSelectionModel(values ...string) *agentv1.RequestedModel {
	model := &agentv1.RequestedModel{ModelId: "channel"}
	for _, value := range values {
		model.Parameters = append(model.Parameters, &agentv1.RequestedModel_ModelParameterValue{Id: "context", Value: value})
	}
	return model
}

func TestContextSelectionDecodeRunAndPrewarm(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	for _, prewarm := range []bool{false, true} {
		for _, tc := range []struct {
			name  string
			model *agentv1.RequestedModel
			want  int
			bad   bool
		}{
			{"absent", contextSelectionModel(), 0, false},
			{"small", contextSelectionModel("32000"), 32000, false},
			{"same_duplicate", contextSelectionModel("32000", "032000"), 32000, false},
			{"max_int32", contextSelectionModel("2147483647"), 2147483647, false},
			{"conflict", contextSelectionModel("32000", "64000"), 0, true},
			{"empty", contextSelectionModel(""), 0, true},
			{"zero", contextSelectionModel("0"), 0, true},
			{"negative", contextSelectionModel("-1"), 0, true},
			{"plus", contextSelectionModel("+32000"), 0, true},
			{"spaces", contextSelectionModel(" 32000"), 0, true},
			{"fraction", contextSelectionModel("32.0"), 0, true},
			{"exponent", contextSelectionModel("32e3"), 0, true},
			{"overflow", contextSelectionModel("2147483648"), 0, true},
			{"huge", contextSelectionModel("99999999999999999999999999999"), 0, true},
		} {
			t.Run(fmt.Sprintf("prewarm_%t/%s", prewarm, tc.name), func(t *testing.T) {
				message, kind := contextSelectionMessage(t, prewarm, "conversation", "message", tc.model)
				intent, err := service.decodeInboundIntent("request", message, kind)
				if (err != nil) != tc.bad {
					t.Fatalf("error = %v, want bad %t", err, tc.bad)
				}
				if err == nil && intent.RequestedContextWindowTokens != tc.want {
					t.Fatalf("context = %d, want %d", intent.RequestedContextWindowTokens, tc.want)
				}
			})
		}
	}
}

func TestContextSelectionVariantDecode(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	for _, tc := range []struct {
		raw, model, effort string
		context            int
		parameter          string
		bad, notVariant    bool
	}{
		{raw: "channel:high:context=32000:fast=true", model: "channel", effort: "high", context: 32000},
		{raw: "channel:adaptive:context=32000:fast=false", model: "channel", effort: "adaptive", context: 32000},
		{raw: "channel:high", model: "channel", effort: "high"},
		{raw: "gpt:5:high:context=32000:fast=true", model: "gpt:5", effort: "high", context: 32000},
		{raw: "gpt:5:context=32000:fast=true", model: "gpt:5", context: 32000},
		{raw: "gpt:5", model: "gpt:5"},
		{raw: "gpt:5:high", model: "gpt:5:high", notVariant: true},
		{raw: "channel:high:context=32000:fast=true", model: "channel", effort: "high", context: 32000, parameter: "32000"},
		{raw: "channel:high:context=32000:fast=true", parameter: "64000", bad: true},
		{raw: "channel:high:context=0:fast=true", bad: true},
		{raw: "channel:high:context=2147483648:fast=true", bad: true},
		{raw: "channel:high:context=32000:context=64000:fast=true", bad: true},
		{raw: "channel:high:context=32000:context=32000:fast=true", model: "channel", effort: "high", context: 32000},
	} {
		for _, prewarm := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t/%s", tc.raw, prewarm, tc.parameter), func(t *testing.T) {
				model := contextSelectionModel()
				model.ModelId, model.IsVariantStringRepresentation = tc.raw, !tc.notVariant
				if tc.parameter != "" {
					model.Parameters = contextSelectionModel(tc.parameter).Parameters
				}
				message, kind := contextSelectionMessage(t, prewarm, "conversation", "message", model)
				intent, err := service.decodeInboundIntent("request", message, kind)
				if (err != nil) != tc.bad {
					t.Fatalf("error = %v, want bad %t", err, tc.bad)
				}
				if err == nil && (intent.ModelID != tc.model || intent.ThinkingEffort != tc.effort || intent.RequestedContextWindowTokens != tc.context) {
					t.Fatalf("selection = (%q,%q,%d), want (%q,%q,%d)", intent.ModelID, intent.ThinkingEffort, intent.RequestedContextWindowTokens, tc.model, tc.effort, tc.context)
				}
			})
		}
	}
}

func TestContextSelectionHandleStateIsolation(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	decode := func(request, conversation, messageID string, model *agentv1.RequestedModel) InboundIntent {
		t.Helper()
		message, kind := contextSelectionMessage(t, false, conversation, messageID, model)
		intent, err := service.decodeInboundIntent(request, message, kind)
		if err != nil {
			t.Fatal(err)
		}
		return intent
	}
	handle := func(intent InboundIntent) *ActiveStream {
		t.Helper()
		// Exercise the shared run/prewarm state initialization without starting an asynchronous provider.
		intent.Prewarm = true
		if err := service.handleRunIntent(intent); err != nil {
			t.Fatal(err)
		}
		stream, ok := service.broker.Get(intent.RequestID)
		if !ok {
			t.Fatal("missing stream")
		}
		return stream
	}
	first := decode("request-a", "conversation-a", "message-a", contextSelectionModel("32000"))
	stream := handle(first)
	if stream.RequestedContextWindowTokens != 32000 {
		t.Fatalf("context = %d", stream.RequestedContextWindowTokens)
	}
	stream.mu.Lock()
	stream.ContextBudgetCeilingTokens, stream.ContextBudgetRetryCount, stream.ContextBudgetRetryReserveTokens = 16000, 2, 512
	stream.mu.Unlock()
	reconnect := decode("request-a", "conversation-a", "message-a", contextSelectionModel())
	if !service.shouldReuseActiveRun(reconnect) {
		t.Fatal("same message without parameters must resume")
	}
	handle(reconnect)
	omittedModel := decode("request-a", "conversation-a", "message-a", nil)
	if !service.shouldReuseActiveRun(omittedModel) {
		t.Fatal("same message without requested model must inherit active selection")
	}
	handle(omittedModel)
	sameExplicit := decode("request-a", "conversation-a", "message-a", contextSelectionModel("32000"))
	if !service.shouldReuseActiveRun(sameExplicit) {
		t.Fatal("same explicit context must resume")
	}
	stream.mu.Lock()
	if stream.RequestedContextWindowTokens != 32000 || stream.ContextBudgetCeilingTokens != 16000 || stream.ContextBudgetRetryCount != 2 || stream.ContextBudgetRetryReserveTokens != 512 {
		t.Fatal("resume reset current selection or retry state")
	}
	stream.mu.Unlock()
	changed := decode("request-a", "conversation-a", "message-a", contextSelectionModel("64000"))
	if service.shouldReuseActiveRun(changed) {
		t.Fatal("explicit context change was swallowed as duplicate")
	}
	handle(changed)
	if stream.RequestedContextWindowTokens != 64000 || stream.ContextBudgetCeilingTokens != 0 || stream.ContextBudgetRetryCount != 0 || stream.ContextBudgetRetryReserveTokens != 0 {
		t.Fatal("new selection did not reset budget recovery state")
	}
	other := handle(decode("request-b", "conversation-b", "message-b", contextSelectionModel()))
	if other.RequestedContextWindowTokens != 0 || stream.RequestedContextWindowTokens != 64000 {
		t.Fatal("context leaked between conversations")
	}
	action := &agentv1.AgentClientMessage{Message: &agentv1.AgentClientMessage_ConversationAction{ConversationAction: &agentv1.ConversationAction{Action: &agentv1.ConversationAction_UserMessageAction{UserMessageAction: &agentv1.UserMessageAction{UserMessage: &agentv1.UserMessage{MessageId: "action-message", Text: "follow up"}}}}}}
	inherited, err := service.decodeInboundIntent("request-a", action, "conversation_action")
	if err != nil {
		t.Fatal(err)
	}
	if inherited.RequestedContextWindowTokens != 64000 {
		t.Fatalf("action context = %d", inherited.RequestedContextWindowTokens)
	}
	next := decode("request-a", "conversation-a", "new-message", contextSelectionModel())
	if service.shouldReuseActiveRun(next) {
		t.Fatal("new user message must not resume")
	}
	handle(next)
	if stream.RequestedContextWindowTokens != 0 {
		t.Fatalf("new turn inherited %d", stream.RequestedContextWindowTokens)
	}
}

func TestContextSelectionPrewarmHandleState(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	message, kind := contextSelectionMessage(t, true, "prewarm-conversation", "", contextSelectionModel("128000"))
	intent, err := service.decodeInboundIntent("prewarm-request", message, kind)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.handleRunIntent(intent); err != nil {
		t.Fatal(err)
	}
	stream, ok := service.broker.Get(intent.RequestID)
	if !ok || stream.RequestedContextWindowTokens != 128000 {
		t.Fatal("prewarm context was not applied")
	}
}
