package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
)

type visibleProgressProvider struct{}

type executorFailoverConfigProvider struct {
	limit int
}

func (provider executorFailoverConfigProvider) DelegationRuntimeConfig() delegation.RuntimeConfig {
	return delegation.NormalizeRuntimeConfig(delegation.RuntimeConfig{
		Enabled:               true,
		MaxConcurrency:        1,
		ExecutorFailoverLimit: provider.limit,
	})
}

func (visibleProgressProvider) StartStream(_ context.Context, _ ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	if err := sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "found the relevant code path"}); err != nil {
		return err
	}
	return sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTurnFinished})
}

func TestDelegationAutoUsesRegistryExecutorAndSchedulerSnapshotsAttempts(t *testing.T) {
	registry := delegation.NewExecutorRegistry(delegation.ExecutorRegistryConfig{})
	var calls atomic.Int32
	registration := delegation.ExecutorRegistration{
		ID: "codex-cli", DisplayName: "Codex CLI", Enabled: true, Priority: 10,
		Capabilities: []delegation.ExecutorCapability{delegation.ExecutorCapabilityReadWorkspace},
		Probe: func(context.Context) (delegation.ExecutorProbeResult, error) {
			return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeReady}, nil
		},
		Execute: func(_ context.Context, request delegation.TaskRequest) delegation.TaskResult {
			calls.Add(1)
			return delegation.TaskResult{Output: request.ID + ":external"}
		},
	}
	if err := registry.Register(registration); err != nil {
		t.Fatalf("Register(): %v", err)
	}
	if _, err := registry.Probe(context.Background(), registration.ID, true); err != nil {
		t.Fatalf("Probe(): %v", err)
	}
	service := NewServiceWithExecutorRegistry(t.TempDir(), nilResolver{}, registry)
	defer service.multitaskDelegation.Close()
	service.multitaskDelegation.configProvider = executorFailoverConfigProvider{limit: 2}

	taskID, err := service.multitaskDelegation.scheduler.Submit(delegation.TaskRequest{
		ID: "worker-external", ExecutionMode: delegation.ExecutionModeAuto, Readonly: true,
	})
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.multitaskDelegation.scheduler.WaitForTerminal(ctx, []string{taskID}); err != nil {
		t.Fatalf("WaitForTerminal(): %v", err)
	}
	snapshot, ok := service.multitaskDelegation.scheduler.Snapshot(taskID)
	if !ok || snapshot.Status != delegation.TaskCompleted || snapshot.Output != "worker-external:external" {
		t.Fatalf("snapshot = %#v, ok=%t", snapshot, ok)
	}
	if calls.Load() != 1 || snapshot.ExecutorID != "codex-cli" || len(snapshot.Attempts) != 1 || snapshot.Attempts[0].ExecutorID != "codex-cli" {
		t.Fatalf("calls=%d snapshot=%#v", calls.Load(), snapshot)
	}
}

func TestDelegationAutoFailoverUsesConfiguredLimitAndWriteCapability(t *testing.T) {
	registry := delegation.NewExecutorRegistry(delegation.ExecutorRegistryConfig{})
	var readOnlyCalls atomic.Int32
	var firstCalls atomic.Int32
	var secondCalls atomic.Int32
	registrations := []delegation.ExecutorRegistration{
		{
			ID: "read-only", DisplayName: "Read only", Enabled: true, Priority: 1,
			Capabilities: []delegation.ExecutorCapability{delegation.ExecutorCapabilityReadWorkspace},
			Probe: func(context.Context) (delegation.ExecutorProbeResult, error) {
				return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeReady}, nil
			},
			Execute: func(context.Context, delegation.TaskRequest) delegation.TaskResult {
				readOnlyCalls.Add(1)
				return delegation.TaskResult{Output: "wrong"}
			},
		},
		{
			ID: "first-writer", DisplayName: "First writer", Enabled: true, Priority: 2,
			Capabilities: []delegation.ExecutorCapability{delegation.ExecutorCapabilityReadWorkspace, delegation.ExecutorCapabilityWriteWorkspace},
			Probe: func(context.Context) (delegation.ExecutorProbeResult, error) {
				return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeReady}, nil
			},
			Execute: func(context.Context, delegation.TaskRequest) delegation.TaskResult {
				firstCalls.Add(1)
				return delegation.TaskResult{Error: delegation.NewClassifiedExecutorError(delegation.ExecutorFailureSwitchable, true, "temporary", errors.New("temporary"))}
			},
		},
		{
			ID: "second-writer", DisplayName: "Second writer", Enabled: true, Priority: 3,
			Capabilities: []delegation.ExecutorCapability{delegation.ExecutorCapabilityReadWorkspace, delegation.ExecutorCapabilityWriteWorkspace},
			Probe: func(context.Context) (delegation.ExecutorProbeResult, error) {
				return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeReady}, nil
			},
			Execute: func(context.Context, delegation.TaskRequest) delegation.TaskResult {
				secondCalls.Add(1)
				return delegation.TaskResult{Output: "recovered"}
			},
		},
	}
	for _, registration := range registrations {
		if err := registry.Register(registration); err != nil {
			t.Fatalf("Register(%s): %v", registration.ID, err)
		}
		if _, err := registry.Probe(context.Background(), registration.ID, true); err != nil {
			t.Fatalf("Probe(%s): %v", registration.ID, err)
		}
	}
	service := NewServiceWithExecutorRegistry(t.TempDir(), nilResolver{}, registry)
	defer service.multitaskDelegation.Close()
	service.multitaskDelegation.configProvider = executorFailoverConfigProvider{limit: 2}

	result := service.multitaskDelegation.executeWorker(context.Background(), delegation.TaskRequest{
		ID: "write-worker", ExecutionMode: delegation.ExecutionModeAuto, Readonly: false,
	})
	if result.Error != nil || result.Output != "recovered" || result.ExecutorID != "second-writer" {
		t.Fatalf("result = %#v", result)
	}
	if readOnlyCalls.Load() != 0 || firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("calls read=%d first=%d second=%d", readOnlyCalls.Load(), firstCalls.Load(), secondCalls.Load())
	}
	if len(result.Attempts) != 2 || result.Attempts[0].ExecutorID != "first-writer" || result.Attempts[1].ExecutorID != "second-writer" {
		t.Fatalf("attempts = %#v", result.Attempts)
	}
}

func TestDelegationLocalModeDoesNotUseExternalExecutor(t *testing.T) {
	registry := delegation.NewExecutorRegistry(delegation.ExecutorRegistryConfig{})
	var calls atomic.Int32
	registration := delegation.ExecutorRegistration{
		ID: "external", DisplayName: "External", Enabled: true, Priority: 1,
		Capabilities: []delegation.ExecutorCapability{delegation.ExecutorCapabilityReadWorkspace},
		Probe: func(context.Context) (delegation.ExecutorProbeResult, error) {
			return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeReady}, nil
		},
		Execute: func(context.Context, delegation.TaskRequest) delegation.TaskResult {
			calls.Add(1)
			return delegation.TaskResult{Output: "external"}
		},
	}
	if err := registry.Register(registration); err != nil {
		t.Fatalf("Register(): %v", err)
	}
	if _, err := registry.Probe(context.Background(), registration.ID, true); err != nil {
		t.Fatalf("Probe(): %v", err)
	}
	service := NewServiceWithExecutorRegistry(t.TempDir(), nilResolver{}, registry)
	defer service.multitaskDelegation.Close()
	result := service.multitaskDelegation.executeWorker(context.Background(), delegation.TaskRequest{ExecutionMode: delegation.ExecutionModeLocal})
	if calls.Load() != 0 {
		t.Fatalf("local mode external calls = %d", calls.Load())
	}
	if result.Error == nil {
		t.Fatalf("local mode result = %#v", result)
	}
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

// TestDelegationBubbleCreationTimeout 锁定 cursor 执行模式兜底判定：客户端
// 返回 "Timeout waiting for bubble creation" 时视为可降级失败（发生在派发阶段，
// worker 尚未工作），其余错误不触发降级。
func TestDelegationBubbleCreationTimeout(t *testing.T) {
	if !delegationBubbleCreationTimeout(delegation.TaskResult{Error: errors.New("Timeout waiting for bubble creation: composerId=abc, toolCallId=child-tool-1")}) {
		t.Fatal("bubble creation timeout must be recognized as fallback failure")
	}
	if delegationBubbleCreationTimeout(delegation.TaskResult{Error: errors.New("delegated worker failed: boom")}) {
		t.Fatal("unrelated worker error must not trigger fallback")
	}
	if delegationBubbleCreationTimeout(delegation.TaskResult{}) {
		t.Fatal("nil error must not trigger fallback")
	}
}

// TestCursorBubbleUnavailableLatch 锁定冷却门闩行为：首次 bubble 失败后进入
// 冷却期，冷却期内不再尝试 cursor 派发，避免每个 worker 空等 5 秒后集体失败。
func TestCursorBubbleUnavailableLatch(t *testing.T) {
	coordinator := &multitaskDelegationCoordinator{}
	if coordinator.cursorBubbleUnavailable() {
		t.Fatal("fresh coordinator must not be in fallback window")
	}
	coordinator.markCursorBubbleUnavailable(delegation.TaskRequest{ParentRequest: "request-1", ID: "worker-1"}, delegation.TaskResult{Error: errors.New("Timeout waiting for bubble creation: composerId=abc, toolCallId=child-tool-1")})
	if !coordinator.cursorBubbleUnavailable() {
		t.Fatal("bubble failure must arm the fallback window")
	}
	coordinator.mu.Lock()
	coordinator.cursorBubbleUnavailableUntil = time.Now().UTC().Add(-time.Minute)
	coordinator.mu.Unlock()
	if coordinator.cursorBubbleUnavailable() {
		t.Fatal("expired fallback window must allow cursor attempts again")
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

func TestBuildTaskToolCallDeltaMessageStructure(t *testing.T) {
	msg := buildTaskToolCallDeltaMessage("tc_call_1", "model_call_1", buildThinkingDeltaInteraction("进度", agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT))
	iu := msg.GetInteractionUpdate()
	if iu == nil {
		t.Fatalf("missing interaction_update")
	}
	delta := iu.GetToolCallDelta()
	if delta == nil {
		t.Fatalf("missing tool_call_delta")
	}
	if got := delta.GetCallId(); got != "tc_call_1" {
		t.Fatalf("call_id = %q, want tc_call_1", got)
	}
	if got := delta.GetModelCallId(); got != "model_call_1" {
		t.Fatalf("model_call_id = %q, want model_call_1", got)
	}
	taskDelta := delta.GetToolCallDelta().GetTaskToolCallDelta()
	if taskDelta == nil {
		t.Fatalf("missing task_tool_call_delta")
	}
	inner := taskDelta.GetInteractionUpdate()
	if inner == nil || inner.GetThinkingDelta() == nil {
		t.Fatalf("missing nested thinking_delta interaction_update")
	}
	if got := inner.GetThinkingDelta().GetText(); got != "进度" {
		t.Fatalf("nested thinking text = %q, want 进度", got)
	}
}

func TestDelegationStartupDeltaText(t *testing.T) {
	if got := delegationStartupDeltaText([]byte(`{"description":"并行审查","prompt":"x"}`)); got != "任务已启动：并行审查" {
		t.Fatalf("startup text = %q", got)
	}
	if got := delegationStartupDeltaText([]byte(`{"prompt":"x"}`)); got != "任务已启动：委派任务" {
		t.Fatalf("fallback startup text = %q", got)
	}
}
