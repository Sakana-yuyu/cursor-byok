package forwarder

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cursor/gen/agentv1"
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

// TestCollectAggregateSurfacesFailedWorkerError 锁定 Bug B 修复：collectAggregate
// 必须把首个失败 worker 的错误提到聚合顶层 error，否则 delegationResultRunDetail
// 只读顶层 error 会永远返回空串，子代理卡片显示空白错误。
func TestCollectAggregateSurfacesFailedWorkerError(t *testing.T) {
	failed := delegatedAggregateResult{
		AggregateID: "agg-1",
		Status:      "failed",
		Error:       "local delegated worker failed: boom",
		Failed:      1,
		Tasks:       []delegatedWorkerResult{{TaskID: "worker-1", Status: delegation.TaskFailed, Error: "local delegated worker failed: boom"}},
	}
	payload, err := json.Marshal(failed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if detail := delegationResultRunDetail(string(payload)); strings.TrimSpace(detail) == "" {
		t.Fatalf("delegationResultRunDetail() = empty, want the failed worker error surfaced to the aggregate top level")
	}
	if status := delegationResultRunStatus(string(payload)); status != agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_ERROR {
		t.Fatalf("status = %v, want ERROR", status)
	}

	// 成功路径仍返回空 detail。
	completed := delegatedAggregateResult{AggregateID: "agg-2", Status: "completed", Succeeded: 1}
	completedPayload, _ := json.Marshal(completed)
	if detail := delegationResultRunDetail(string(completedPayload)); detail != "" {
		t.Fatalf("completed detail = %q, want empty", detail)
	}
}
