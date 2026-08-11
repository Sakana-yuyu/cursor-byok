package forwarder

import (
	"context"
	"strings"
	"testing"

	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
)

type visibleProgressProvider struct{}

func (visibleProgressProvider) StartStream(_ context.Context, _ ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	if err := sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "found the relevant code path"}); err != nil {
		return err
	}
	return sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTurnFinished})
}

func TestHasDelegationAggregateForToolCallOnlyMatchesSameCall(t *testing.T) {
	stream := &ActiveStream{
		PendingExecs: map[string]runtimecore.PendingExec{
			"automatic": {
				ExecKind:   "delegation_aggregate",
				ToolCallID: "tc_automatic",
			},
		},
	}

	if hasDelegationAggregateForToolCall(stream, "tc_model") {
		t.Fatal("an automatic aggregate must not block an independent Task call")
	}
	if !hasDelegationAggregateForToolCall(stream, "tc_automatic") {
		t.Fatal("the same Task call must not start a duplicate aggregate")
	}
}

func TestLocalDelegationTaskDoesNotAdvertiseSyntheticSubagentID(t *testing.T) {
	invocation := runtimecore.ToolInvocation{
		ToolName: "Task",
		CallID:   "task-call",
		ArgsJSON: []byte(`{"description":"inspect","prompt":"inspect"}`),
	}

	started := buildStartedToolCall(invocation)
	clearTaskToolCallIdentity(started)
	if agentID := started.GetTaskToolCall().GetArgs().GetAgentId(); agentID != "" {
		t.Fatalf("local aggregate Task agent_id = %q, want empty because no native child was opened", agentID)
	}
	completed := buildDelegationCompletedTaskToolCall(invocation.ArgsJSON, `{"status":"completed"}`, "", 1)
	if agentID := completed.GetTaskToolCall().GetResult().GetSuccess().GetAgentId(); agentID != "" {
		t.Fatalf("local aggregate Task result agent_id = %q, want empty", agentID)
	}
}

func TestLocalDelegationTextDeltaPublishesBoundedWorkerProgress(t *testing.T) {
	adapter := &localDelegatedAgentAdapter{
		provider: visibleProgressProvider{},
	}
	updates := make([]string, 0, 1)
	ctx := delegation.WithWorkerVisibleUpdatePublisher(context.Background(), func(text string) bool {
		updates = append(updates, text)
		return true
	})
	_, err := adapter.runProviderPass(ctx, delegation.TaskRequest{ID: "worker-1", Description: "inspect runtime", ModelID: "model"}, localDelegatedIdentity{taskID: "worker-1", requestID: "request", conversationID: "conversation", runID: "run"}, &ConversationFile{}, CompiledConversation{}, delegatedProviderPassView{}, 1, 0, 1)
	if err != nil {
		t.Fatalf("runProviderPass() error: %v", err)
	}
	if len(updates) != 1 || !strings.Contains(updates[0], "found the relevant code path") {
		t.Fatalf("visible updates = %#v, want worker text", updates)
	}
}
