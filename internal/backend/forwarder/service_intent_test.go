package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
)

func TestResolveRequestedModelNameUsesShortDisplayName(t *testing.T) {
	message := &agentv1.AgentClientMessage{
		Message: &agentv1.AgentClientMessage_RunRequest{RunRequest: &agentv1.AgentRunRequest{
			ModelDetails: &agentv1.ModelDetails{ModelId: "gpt-5.6", DisplayNameShort: "GPT-5.6", DisplayModelId: "gpt-5.6"},
		}},
	}
	service := &Service{}
	if got := service.resolveRequestedModelName(message, "gpt-5.6"); got != "GPT-5.6" {
		t.Fatalf("model name = %q, want GPT-5.6", got)
	}
}

func TestExtractRuntimeThinkingEffortAcceptsEffortParameter(t *testing.T) {
	if got := extractRuntimeThinkingEffort(messageWithModelParameter("effort", "high")); got != "high" {
		t.Fatalf("thinking effort: got %q, want high", got)
	}
}

func TestExtractRuntimeThinkingEffortAcceptsCustomEffortParameter(t *testing.T) {
	if got := extractRuntimeThinkingEffort(messageWithModelParameter("effort", "Adaptive")); got != "adaptive" {
		t.Fatalf("custom thinking effort: got %q, want adaptive", got)
	}
	// 无字母的自定义值（如纯数字）不可信：参数路径同样拒绝，避免误透传。
	if got := extractRuntimeThinkingEffort(messageWithModelParameter("effort", "70")); got != "" {
		t.Fatalf("numeric-only effort must be rejected: got %q", got)
	}
}

func TestSplitRuntimeThinkingEffortVariantStringAcceptsCustomEffort(t *testing.T) {
	modelID, effort := splitRuntimeThinkingEffortVariantString("channel-gpt:adaptive")
	if modelID != "channel-gpt" || effort != "adaptive" {
		t.Fatalf("variant split: got (%q, %q), want (channel-gpt, adaptive)", modelID, effort)
	}
	// 纯数字后缀保持按 modelID 处理，避免 "gpt:5" 类模型名被误拆。
	if modelID, effort := splitRuntimeThinkingEffortVariantString("gpt:5"); modelID != "" || effort != "" {
		t.Fatalf("numeric suffix must not split: got (%q, %q)", modelID, effort)
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
