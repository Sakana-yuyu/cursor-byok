package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
)

func TestExtractRuntimeThinkingEffortAcceptsEffortParameter(t *testing.T) {
	if got := extractRuntimeThinkingEffort(messageWithModelParameter("effort", "high")); got != "high" {
		t.Fatalf("thinking effort: got %q, want high", got)
	}
}

func TestExtractRuntimeThinkingEffortIgnoresFastParameter(t *testing.T) {
	if got := extractRuntimeThinkingEffort(messageWithModelParameter("fast", "true")); got != "" {
		t.Fatalf("fast parameter must not become thinking effort: got %q", got)
	}
}

func messageWithModelParameter(id string, value string) *agentv1.AgentClientMessage {
	return &agentv1.AgentClientMessage{
		Message: &agentv1.AgentClientMessage_RunRequest{
			RunRequest: &agentv1.AgentRunRequest{
				RequestedModel: &agentv1.RequestedModel{
					ModelId: "channel-gpt",
					Parameters: []*agentv1.RequestedModel_ModelParameterValue{{
						Id:    id,
						Value: value,
					}},
				},
			},
		},
	}
}
