package forwarder

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
)

// openFollowUpParentStream 打开一个 Multitask 父流，模拟「父 turn 正在等待子代理」的现场。
func openFollowUpParentStream(t *testing.T, service *Service, requestID, conversationID string) *ActiveStream {
	t.Helper()
	stream, err := service.broker.OpenStream(requestID, conversationID, 1, "model-id", "Model Name",
		agentv1.AgentMode_AGENT_MODE_MULTITASK, "run three tasks in parallel")
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func countToolResultEntries(t *testing.T, entries []HistoryEntry, toolCallID string) int {
	t.Helper()
	count := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.Kind) != "tool_result" {
			continue
		}
		var payload toolResultEntryPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			continue
		}
		if strings.TrimSpace(payload.ToolCallID) == strings.TrimSpace(toolCallID) {
			count++
		}
	}
	return count
}

func TestFollowUpCancelKeepsNativeSubagentRunning(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	requestID := "req-followup-parent"
	conversationID := "conv-followup-parent"
	execID := "exec-followup-subagent"
	toolCallID := "call-followup-subagent"

	stream := openFollowUpParentStream(t, service, requestID, conversationID)

	pending := runtimecore.PendingExec{
		ExecID:     execID,
		ExecKind:   "subagent",
		ToolCallID: toolCallID,
		ArgsJSON:   []byte(`{"description":"inspect routes","subagent_type":"explore"}`),
	}
	if !service.registerNativeDelegation(stream, pending, nil) {
		t.Fatal("native delegation was not registered")
	}
	stream.mu.Lock()
	stream.PendingExecs[execID] = pending
	stream.Phase = TurnPhaseWaitingExternal
	stream.mu.Unlock()

	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      requestID,
		ConversationID: conversationID,
		CancelReason:   "new_message_submitted",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	task, ok := service.nativeDelegationTask(execID)
	if !ok {
		t.Fatal("native delegation runtime disappeared")
	}
	if task.Status == delegation.TaskCanceled {
		t.Fatalf("follow-up cancel killed the running Task subagent: status=%q error=%q", task.Status, task.Error)
	}
}

func TestUserStoppedGenerationCancelStillCancelsNativeSubagent(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	requestID := "req-stop-parent"
	conversationID := "conv-stop-parent"
	execID := "exec-stop-subagent"

	stream := openFollowUpParentStream(t, service, requestID, conversationID)
	pending := runtimecore.PendingExec{
		ExecID:     execID,
		ExecKind:   "subagent",
		ToolCallID: "call-stop-subagent",
		ArgsJSON:   []byte(`{"description":"inspect routes","subagent_type":"explore"}`),
	}
	if !service.registerNativeDelegation(stream, pending, nil) {
		t.Fatal("native delegation was not registered")
	}
	stream.mu.Lock()
	stream.PendingExecs[execID] = pending
	stream.Phase = TurnPhaseWaitingExternal
	stream.mu.Unlock()

	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      requestID,
		ConversationID: conversationID,
		CancelReason:   "user_stopped_generation",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	task, ok := service.nativeDelegationTask(execID)
	if !ok {
		t.Fatal("native delegation runtime disappeared")
	}
	if task.Status != delegation.TaskCanceled {
		t.Fatalf("user stop must cancel the subagent: status=%q", task.Status)
	}
}

func TestFollowUpCancelWritesBackgroundedToolResultForNativeSubagent(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	requestID := "req-followup-toolresult"
	conversationID := "conv-followup-toolresult"
	execID := "exec-followup-toolresult"
	toolCallID := "call-followup-toolresult"

	stream := openFollowUpParentStream(t, service, requestID, conversationID)
	if err := service.replaceCheckpointConversation(stream, testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, requestID, "run three tasks in parallel"),
	})); err != nil {
		t.Fatalf("replaceCheckpointConversation() error = %v", err)
	}
	pending := runtimecore.PendingExec{
		ExecID:     execID,
		ExecKind:   "subagent",
		ToolCallID: toolCallID,
		ArgsJSON:   []byte(`{"description":"inspect routes","subagent_type":"explore"}`),
	}
	if !service.registerNativeDelegation(stream, pending, nil) {
		t.Fatal("native delegation was not registered")
	}
	stream.mu.Lock()
	stream.PendingExecs[execID] = pending
	stream.Phase = TurnPhaseWaitingExternal
	stream.mu.Unlock()

	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      requestID,
		ConversationID: conversationID,
		CancelReason:   "replaced_by_new_turn",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	stream.mu.Lock()
	entries := append([]HistoryEntry(nil), stream.CheckpointConversation.Entries...)
	stream.mu.Unlock()
	if got := countToolResultEntries(t, entries, toolCallID); got != 1 {
		t.Fatalf("backgrounded Task tool_result count = %d, want 1 (a dangling tool_call breaks the next replay)", got)
	}
}

func TestFollowUpCancelKeepsMultitaskAggregateRunning(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	defer service.multitaskDelegation.Close()
	requestID := "req-followup-aggregate"
	conversationID := "conv-followup-aggregate"
	execID := "exec-followup-aggregate"

	stream := openFollowUpParentStream(t, service, requestID, conversationID)
	aggregate := registerTestAggregate(service, execID)
	pending := runtimecore.PendingExec{
		ExecID:     execID,
		ExecKind:   "delegation_aggregate",
		ToolCallID: "call-followup-aggregate",
		ArgsJSON:   []byte(`{"description":"run three tasks","subagent_type":"generalPurpose"}`),
	}
	stream.mu.Lock()
	stream.PendingExecs[execID] = pending
	stream.Phase = TurnPhaseWaitingExternal
	stream.mu.Unlock()

	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      requestID,
		ConversationID: conversationID,
		CancelReason:   "new_message_submitted",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	if aggregate.isCanceled() {
		t.Fatal("follow-up cancel killed the running Multitask worker batch")
	}
	stream.mu.Lock()
	startupCanceled := stream.MultitaskStartupCanceled
	stream.mu.Unlock()
	if startupCanceled {
		t.Fatal("follow-up cancel poisoned the multitask startup gate")
	}
}

func TestUserStoppedGenerationCancelStillCancelsMultitaskAggregate(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	defer service.multitaskDelegation.Close()
	requestID := "req-stop-aggregate"
	conversationID := "conv-stop-aggregate"
	execID := "exec-stop-aggregate"

	stream := openFollowUpParentStream(t, service, requestID, conversationID)
	aggregate := registerTestAggregate(service, execID)
	stream.mu.Lock()
	stream.PendingExecs[execID] = runtimecore.PendingExec{
		ExecID:     execID,
		ExecKind:   "delegation_aggregate",
		ToolCallID: "call-stop-aggregate",
	}
	stream.Phase = TurnPhaseWaitingExternal
	stream.mu.Unlock()

	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      requestID,
		ConversationID: conversationID,
		CancelReason:   "user_stopped_generation",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	if !aggregate.isCanceled() {
		t.Fatal("user stop must cancel the running Multitask worker batch")
	}
}

func TestBackgroundedAggregateResultPersistsInsteadOfWritingDeadStream(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	defer service.multitaskDelegation.Close()
	requestID := "req-followup-late"
	conversationID := "conv-followup-late"
	execID := "exec-followup-late"
	toolCallID := "call-followup-late"

	stream := openFollowUpParentStream(t, service, requestID, conversationID)
	if err := service.replaceCheckpointConversation(stream, testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, requestID, "run three tasks in parallel"),
	})); err != nil {
		t.Fatalf("replaceCheckpointConversation() error = %v", err)
	}
	registerTestAggregate(service, execID)
	pending := runtimecore.PendingExec{
		ExecID:     execID,
		ExecKind:   "delegation_aggregate",
		ToolCallID: toolCallID,
		ArgsJSON:   []byte(`{"description":"run three tasks","subagent_type":"generalPurpose"}`),
	}
	stream.mu.Lock()
	stream.PendingExecs[execID] = pending
	stream.Phase = TurnPhaseWaitingExternal
	stream.mu.Unlock()

	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      requestID,
		ConversationID: conversationID,
		CancelReason:   "new_message_submitted",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	// 走 awaitAggregate 的真实收口入口：父流已终态，结果不能再进 actor 邮箱。
	service.multitaskDelegation.publishAggregateTerminal(stream, pending, &streamDelegationResult{
		AggregateID: execID,
		ExecID:      execID,
		ToolCallID:  toolCallID,
		Payload:     `{"aggregate_id":"` + execID + `","status":"completed","succeeded":1,"failed":0,"canceled":0,"tasks":[{"task_id":"w1","status":"completed","output":"路由检查完成：三个入口全部有鉴权"}]}`,
	})

	conversation, err := service.store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v", err)
	}
	if conversation == nil {
		t.Fatal("conversation was not persisted")
	}
	if got := countToolResultEntries(t, conversation.Entries, toolCallID); got != 1 {
		t.Fatalf("tool_result count for backgrounded aggregate = %d, want exactly the backgrounded closure", got)
	}
	found := false
	for _, entry := range conversation.Entries {
		if strings.TrimSpace(entry.Kind) != "metadata" {
			continue
		}
		var payload metadataPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			continue
		}
		if strings.TrimSpace(payload.Type) != metadataTypeBackgroundedDelegationResult {
			continue
		}
		if !strings.Contains(readStringValue(payload.Value["result"]), "路由检查完成") {
			t.Fatalf("backgrounded delegation result = %#v", payload.Value)
		}
		found = true
	}
	if !found {
		t.Fatal("late background delegation result was silently dropped instead of persisted for the next turn")
	}

	// 下一回合开头必须把它转成模型可见的 prompt_context 回放，且只回放一次。
	replay := service.pendingBackgroundedDelegationEntries(conversation, "req-next-turn", 2)
	if len(replay) != 1 {
		t.Fatalf("next-turn replay entries = %d, want 1", len(replay))
	}
	var replayPayload promptContextEntryPayload
	if err := json.Unmarshal(replay[0].Payload, &replayPayload); err != nil {
		t.Fatalf("decode replay prompt_context: %v", err)
	}
	if strings.TrimSpace(replayPayload.Source) != promptContextSourceBackgroundedDelegationResult {
		t.Fatalf("replay source = %q", replayPayload.Source)
	}
	if !strings.Contains(replayPayload.Content, "路由检查完成") {
		t.Fatalf("replay content = %q", replayPayload.Content)
	}
	appendEntriesInPlace(conversation, replay)
	if again := service.pendingBackgroundedDelegationEntries(conversation, "req-next-turn-2", 3); len(again) != 0 {
		t.Fatalf("already replayed background result was queued again: %d entries", len(again))
	}
}

func TestCancelDoesNotAbortBackgroundedShellExec(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	requestID := "req-background-shell"
	conversationID := "conv-background-shell"
	execID := "exec-background-shell"

	stream := openFollowUpParentStream(t, service, requestID, conversationID)
	stream.mu.Lock()
	stream.PendingExecs[execID] = runtimecore.PendingExec{
		ExecID:      execID,
		ExecKind:    "shell",
		MessageID:   4242,
		ToolCallID:  "call-background-shell",
		StreamState: "backgrounded",
	}
	stream.Phase = TurnPhaseWaitingExternal
	stream.mu.Unlock()

	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      requestID,
		ConversationID: conversationID,
		CancelReason:   "user_stopped_generation",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	stream.mu.Lock()
	backlog := append([]StreamEvent(nil), stream.Backlog...)
	stream.mu.Unlock()
	for _, event := range backlog {
		abort := event.Message.GetExecServerControlMessage().GetAbort()
		if abort != nil && abort.GetId() == 4242 {
			t.Fatal("cancel aborted a shell the user explicitly moved to the background")
		}
	}
}

func TestFollowUpCancelAbsorbsLateNativeSubagentExecResult(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	requestID := "req-followup-absorb"
	conversationID := "conv-followup-absorb"
	execID := "exec-followup-absorb"
	toolCallID := "call-followup-absorb"

	stream := openFollowUpParentStream(t, service, requestID, conversationID)
	if err := service.replaceCheckpointConversation(stream, testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, requestID, "run three tasks in parallel"),
	})); err != nil {
		t.Fatalf("replaceCheckpointConversation() error = %v", err)
	}
	pending := runtimecore.PendingExec{
		ExecID:     execID,
		ExecKind:   "subagent",
		ToolCallID: toolCallID,
		ArgsJSON:   []byte(`{"description":"inspect routes","subagent_type":"explore"}`),
	}
	if !service.registerNativeDelegation(stream, pending, nil) {
		t.Fatal("native delegation was not registered")
	}
	stream.mu.Lock()
	stream.PendingExecs[execID] = pending
	stream.Phase = TurnPhaseWaitingExternal
	stream.mu.Unlock()

	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      requestID,
		ConversationID: conversationID,
		CancelReason:   "new_message_submitted",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	finalMessage := "后台子代理已确认三个入口都有鉴权"
	// 父流 actor 在取消后已经停止，迟到结果必须在入站分发层就被吸收，
	// 否则连 handleExecResult 都进不去（errProviderLoopInterrupted）。
	if err := service.dispatchInboundIntent(InboundIntent{
		Kind:           "exec_result",
		RequestID:      requestID,
		ConversationID: conversationID,
		ExecClientMessage: &agentv1.ExecClientMessage{
			ExecId: execID,
			Message: &agentv1.ExecClientMessage_SubagentResult{
				SubagentResult: &agentv1.SubagentResult{
					Result: &agentv1.SubagentResult_Success{
						Success: &agentv1.SubagentSuccess{FinalMessage: &finalMessage},
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("late backgrounded subagent result must be absorbed, got error = %v", err)
	}

	task, ok := service.nativeDelegationTask(execID)
	if !ok {
		t.Fatal("native delegation runtime disappeared")
	}
	if task.Status != delegation.TaskCompleted {
		t.Fatalf("backgrounded subagent terminal status = %q, want completed", task.Status)
	}
	conversation, err := service.store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v", err)
	}
	replay := service.pendingBackgroundedDelegationEntries(conversation, "req-next-turn", 2)
	if len(replay) != 1 {
		t.Fatalf("next-turn replay entries = %d, want 1", len(replay))
	}
	var replayPayload promptContextEntryPayload
	if err := json.Unmarshal(replay[0].Payload, &replayPayload); err != nil {
		t.Fatalf("decode replay prompt_context: %v", err)
	}
	if !strings.Contains(replayPayload.Content, finalMessage) {
		t.Fatalf("replay content = %q", replayPayload.Content)
	}
}

func TestIsFollowUpCancelReasonLocksSemanticBoundary(t *testing.T) {
	followUp := []string{"new_message_submitted", "replaced_by_new_turn", " New_Message_Submitted "}
	for _, reason := range followUp {
		if !isFollowUpCancelReason(reason) {
			t.Fatalf("isFollowUpCancelReason(%q) = false, want true", reason)
		}
	}
	stop := []string{"user_stopped_generation", "migration", "remote_control_stopped", "", "[canceled] User aborted request"}
	for _, reason := range stop {
		if isFollowUpCancelReason(reason) {
			t.Fatalf("isFollowUpCancelReason(%q) = true, want false", reason)
		}
	}
}

func TestBackgroundFollowUpDelegationsReportsCounts(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	defer service.multitaskDelegation.Close()
	requestID := "req-followup-counts"
	conversationID := "conv-followup-counts"

	stream := openFollowUpParentStream(t, service, requestID, conversationID)
	if err := service.replaceCheckpointConversation(stream, testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, requestID, "run three tasks in parallel"),
	})); err != nil {
		t.Fatalf("replaceCheckpointConversation() error = %v", err)
	}
	subagent := runtimecore.PendingExec{
		ExecID:     "exec-counts-subagent",
		ExecKind:   "subagent",
		ToolCallID: "call-counts-subagent",
		ArgsJSON:   []byte(`{"description":"inspect","subagent_type":"explore"}`),
	}
	if !service.registerNativeDelegation(stream, subagent, nil) {
		t.Fatal("native delegation was not registered")
	}
	registerTestAggregate(service, "exec-counts-aggregate")
	stream.mu.Lock()
	stream.PendingExecs[subagent.ExecID] = subagent
	stream.PendingExecs["exec-counts-aggregate"] = runtimecore.PendingExec{
		ExecID:     "exec-counts-aggregate",
		ExecKind:   "delegation_aggregate",
		ToolCallID: "call-counts-aggregate",
	}
	stream.PendingExecs["exec-counts-shell"] = runtimecore.PendingExec{
		ExecID:   "exec-counts-shell",
		ExecKind: "shell",
	}
	stream.mu.Unlock()

	summary := service.backgroundFollowUpDelegations(context.Background(), stream)
	if summary.NativeSubagents != 1 || summary.Aggregates != 1 {
		t.Fatalf("summary = %#v, want 1 native subagent and 1 aggregate", summary)
	}
	stream.mu.Lock()
	_, subagentStillPending := stream.PendingExecs[subagent.ExecID]
	_, aggregateStillPending := stream.PendingExecs["exec-counts-aggregate"]
	_, shellStillPending := stream.PendingExecs["exec-counts-shell"]
	stream.mu.Unlock()
	if subagentStillPending || aggregateStillPending {
		t.Fatal("backgrounded execs must leave PendingExecs before the abort snapshot is taken")
	}
	if !shellStillPending {
		t.Fatal("a plain foreground shell must not be backgrounded by a follow-up cancel")
	}
}

// registerTestAggregate 在 coordinator 中登记一个空的委派聚合，用于观察取消行为。
func registerTestAggregate(service *Service, aggregateID string) *delegatedAggregate {
	ctx, cancel := context.WithCancel(context.Background())
	aggregate := &delegatedAggregate{
		id:        aggregateID,
		ctx:       ctx,
		cancel:    cancel,
		scheduler: service.multitaskDelegation.scheduler,
	}
	service.multitaskDelegation.mu.Lock()
	service.multitaskDelegation.aggregates[aggregateID] = aggregate
	service.multitaskDelegation.mu.Unlock()
	return aggregate
}
