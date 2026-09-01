package forwarder

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"connectrpc.com/connect"

	"cursor/gen/aiserverv1"
	"cursor/gen/aiserverv1/aiserverv1connect"
	modeladapter "cursor/internal/backend/agent/model"
)

type nameTabProviderFunc func(context.Context, ProviderRequest, func(modeladapter.ModelEvent) error) error

func (fn nameTabProviderFunc) StartStream(ctx context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	return fn(ctx, req, sink)
}

func TestNameTabGeneratesShortSummaryThroughAIHandler(t *testing.T) {
	var captured ProviderRequest
	service := &Service{provider: nameTabProviderFunc(func(_ context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
		captured = req
		return sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "拉取代码并审查变更"})
	})}
	server := httptest.NewServer(newAIHandler(service))
	t.Cleanup(server.Close)

	client := aiserverv1connect.NewAiServiceClient(server.Client(), server.URL)
	response, err := client.NameTab(context.Background(), connect.NewRequest(&aiserverv1.NameTabRequest{
		ConversationId: "conversation-1",
		Messages: []*aiserverv1.ConversationMessage{{
			Type: aiserverv1.ConversationMessage_MESSAGE_TYPE_HUMAN,
			Text: "拉取最新的代码，然后查看审查代码有什么问题？",
		}},
	}))
	if err != nil {
		t.Fatalf("NameTab() error = %v", err)
	}
	if got, want := response.Msg.GetName(), "拉取代码并审查变更"; got != want {
		t.Fatalf("NameTab() name = %q, want %q", got, want)
	}
	if captured.ThinkingEffort != "disabled" || len(captured.Tools) != 0 {
		t.Fatalf("NameTab provider request thinking=%q tools=%d", captured.ThinkingEffort, len(captured.Tools))
	}
	if len(captured.Messages) != 2 || !strings.Contains(captured.Messages[1].Content, "拉取最新的代码") {
		t.Fatalf("NameTab provider messages = %#v", captured.Messages)
	}
}

func TestNameTabFallsBackToConciseNameWhenProviderFails(t *testing.T) {
	userMessage := "拉取最新的代码，然后查看审查代码有什么问题？"
	service := &Service{provider: nameTabProviderFunc(func(context.Context, ProviderRequest, func(modeladapter.ModelEvent) error) error {
		return errors.New("provider unavailable")
	})}
	server := httptest.NewServer(newAIHandler(service))
	t.Cleanup(server.Close)

	client := aiserverv1connect.NewAiServiceClient(server.Client(), server.URL)
	response, err := client.NameTab(context.Background(), connect.NewRequest(&aiserverv1.NameTabRequest{
		Messages: []*aiserverv1.ConversationMessage{{
			Type: aiserverv1.ConversationMessage_MESSAGE_TYPE_HUMAN,
			Text: userMessage,
		}},
	}))
	if err != nil {
		t.Fatalf("NameTab() error = %v", err)
	}
	name := response.Msg.GetName()
	if name == "" || name == userMessage {
		t.Fatalf("NameTab() fallback name = %q", name)
	}
	if utf8.RuneCountInString(name) > 24 {
		t.Fatalf("NameTab() fallback name too long: %q", name)
	}
}
