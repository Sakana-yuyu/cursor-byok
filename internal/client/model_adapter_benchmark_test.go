package client

import (
	"testing"

	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/modelchannel"
)

func TestBuildOpenAIProbeCandidates(t *testing.T) {
	adapter := serverconfig.ModelAdapterConfig{
		Type:               "openai",
		OpenAIEndpoint:     modelchannel.OpenAIEndpointResponses,
		OpenAIRequestGroup: modelchannel.OpenAIRequestGroupResponses,
	}

	got := buildOpenAIProbeCandidates(adapter)
	want := []openAIProbeCandidate{
		{Endpoint: modelchannel.OpenAIEndpointResponses, RequestGroup: modelchannel.OpenAIRequestGroupResponses},
		{Endpoint: modelchannel.OpenAIEndpointChatCompletions, RequestGroup: modelchannel.OpenAIRequestGroupChatCompletions},
		{Endpoint: modelchannel.OpenAIEndpointChatCompletions, RequestGroup: modelchannel.OpenAIRequestGroupChatCompletionsCompat},
	}

	if len(got) != len(want) {
		t.Fatalf("candidate count mismatch: got=%d want=%d candidates=%+v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("candidate[%d] mismatch: got=%+v want=%+v", index, got[index], want[index])
		}
	}
}

func TestBuildOpenAIProbeCandidatesPromotesCompatBeforeEndpointSwitch(t *testing.T) {
	adapter := serverconfig.ModelAdapterConfig{
		Type:               "openai",
		OpenAIEndpoint:     modelchannel.OpenAIEndpointChatCompletions,
		OpenAIRequestGroup: modelchannel.OpenAIRequestGroupChatCompletions,
	}

	got := buildOpenAIProbeCandidates(adapter)
	want := []openAIProbeCandidate{
		{Endpoint: modelchannel.OpenAIEndpointChatCompletions, RequestGroup: modelchannel.OpenAIRequestGroupChatCompletions},
		{Endpoint: modelchannel.OpenAIEndpointChatCompletions, RequestGroup: modelchannel.OpenAIRequestGroupChatCompletionsCompat},
		{Endpoint: modelchannel.OpenAIEndpointResponses, RequestGroup: modelchannel.OpenAIRequestGroupResponses},
	}

	if len(got) != len(want) {
		t.Fatalf("candidate count mismatch: got=%d want=%d candidates=%+v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("candidate[%d] mismatch: got=%+v want=%+v", index, got[index], want[index])
		}
	}
}
