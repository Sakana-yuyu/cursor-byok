package delegation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
	execbridge "cursor/internal/backend/agent/bridge/exec"
	runtimecore "cursor/internal/backend/agent/core"
)

const defaultCursorAdapterRetention = 45 * time.Minute

type CursorPublisher func(requestID string, message *agentv1.AgentServerMessage) error
type CursorProgressPublisher func(parentRequestID string, pending runtimecore.PendingExec, message *agentv1.ExecClientMessage, result execbridge.ExecApplyResult)

type checkpointPublisherContextKey struct{}
type progressPublisherContextKey struct{}
type visibleUpdatePublisherContextKey struct{}

func withWorkerCheckpointPublisher(ctx context.Context, publish func(WorkerCheckpoint) bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if publish == nil {
		return ctx
	}
	return context.WithValue(ctx, checkpointPublisherContextKey{}, publish)
}

func PublishWorkerCheckpoint(ctx context.Context, checkpoint WorkerCheckpoint) bool {
	if ctx == nil {
		return false
	}
	publish, ok := ctx.Value(checkpointPublisherContextKey{}).(func(WorkerCheckpoint) bool)
	if !ok || publish == nil {
		return false
	}
	return publish(checkpoint)
}

func withWorkerProgressPublisher(ctx context.Context, publish func() bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if publish == nil {
		return ctx
	}
	return context.WithValue(ctx, progressPublisherContextKey{}, publish)
}

// MarkWorkerProgress reports a real provider/tool event to the scheduler.
// Callers must not use it for transport heartbeats or periodic summaries.
func MarkWorkerProgress(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	publish, ok := ctx.Value(progressPublisherContextKey{}).(func() bool)
	if !ok || publish == nil {
		return false
	}
	return publish()
}

// WithWorkerVisibleUpdatePublisher attaches a bounded, user-visible worker
// update channel. It is deliberately separate from checkpoints: callers must
// pass only presentable progress, never private reasoning.
func WithWorkerVisibleUpdatePublisher(ctx context.Context, publish func(string) bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if publish == nil {
		return ctx
	}
	return context.WithValue(ctx, visibleUpdatePublisherContextKey{}, publish)
}

// PublishWorkerVisibleUpdate sends a user-visible worker status/text update to
// the parent projection transport. The adapter is responsible for coalescing.
func PublishWorkerVisibleUpdate(ctx context.Context, text string) bool {
	if ctx == nil || strings.TrimSpace(text) == "" {
		return false
	}
	publish, ok := ctx.Value(visibleUpdatePublisherContextKey{}).(func(string) bool)
	if !ok || publish == nil {
		return false
	}
	return publish(text)
}

func PublishTaskCheckpoint(ctx context.Context, request TaskRequest, phase SupervisionStatus, step int, recentTools, changedFiles []string, progress, blocker string) bool {
	contract := request.Contract
	if contract == nil {
		return false
	}
	aggregateID := strings.TrimSpace(contract.AggregateID)
	taskID := strings.TrimSpace(contract.TaskID)
	if aggregateID == "" || taskID == "" || taskID != strings.TrimSpace(request.ID) || contract.Round <= 0 {
		return false
	}
	phase = normalizeSupervisionStatus(string(phase))
	if phase == "" {
		return false
	}
	if step <= 0 {
		step = 1
	}
	workspaceHint := firstNonEmpty(strings.TrimSpace(contract.WorkspaceHint), strings.TrimSpace(request.WorkspaceHint))
	checkpoint := WorkerCheckpoint{
		AggregateID:          aggregateID,
		TaskID:               taskID,
		Round:                contract.Round,
		Phase:                phase,
		Step:                 step,
		RecentToolNames:      normalizeStringSlice(recentTools),
		ChangedFileSummaries: normalizeChangedFileSummaries(changedFiles, workspaceHint),
		ProgressSummary:      sanitizeNarrativeText(progress, workspaceHint),
		Blocker:              sanitizeNarrativeText(blocker, workspaceHint),
	}
	return PublishWorkerCheckpoint(ctx, checkpoint)
}

type CursorAdapter struct {
	execBridge execbridge.ExecBridge
	publish    CursorPublisher
	progress   CursorProgressPublisher
	retention  time.Duration
	now        func() time.Time
	sequence   atomic.Uint64

	mu                  sync.Mutex
	waitersByExecID     map[string]*cursorTaskWaiter
	waitersByMessageID  map[uint32]*cursorTaskWaiter
	waitersByTaskID     map[string]*cursorTaskWaiter
	tombstoneExecIDs    map[string]cursorExecTombstone
	tombstoneMessageIDs map[uint32]cursorExecTombstone
}

type cursorTaskWaiter struct {
	taskID              string
	parentRequestID     string
	childRequestID      string
	childConversationID string
	childToolCallID     string
	subagentType        string
	modelID             string
	pending             runtimecore.PendingExec
	checkpointContext   context.Context
	checkpointRequest   TaskRequest
	checkpointStep      int
	expectSubagent      bool
	done                bool
	resultCh            chan TaskResult
}

type cursorExecTombstone struct {
	parentRequestID string
	createdAt       time.Time
}

func NewCursorAdapterWithProgress(bridge execbridge.ExecBridge, publish CursorPublisher, progress CursorProgressPublisher) *CursorAdapter {
	return &CursorAdapter{
		execBridge:          bridge,
		publish:             publish,
		progress:            progress,
		retention:           defaultCursorAdapterRetention,
		now:                 func() time.Time { return time.Now().UTC() },
		waitersByExecID:     make(map[string]*cursorTaskWaiter),
		waitersByMessageID:  make(map[uint32]*cursorTaskWaiter),
		waitersByTaskID:     make(map[string]*cursorTaskWaiter),
		tombstoneExecIDs:    make(map[string]cursorExecTombstone),
		tombstoneMessageIDs: make(map[uint32]cursorExecTombstone),
	}
}

func (adapter *CursorAdapter) Execute(ctx context.Context, request TaskRequest) TaskResult {
	argsJSON, err := buildCursorTaskArgs(request)
	if err != nil {
		return TaskResult{Error: err}
	}
	invocation := runtimecore.ToolInvocation{
		CallID:   "",
		ToolName: "Task",
		ArgsJSON: argsJSON,
	}
	waiter, err := adapter.openInvocation(ctx, request, invocation, true)
	if err != nil {
		return TaskResult{Error: err}
	}
	return adapter.awaitInvocation(ctx, waiter)
}

func (adapter *CursorAdapter) ExecuteTool(ctx context.Context, request TaskRequest, invocation runtimecore.ToolInvocation) TaskResult {
	waiter, err := adapter.openInvocation(ctx, request, invocation, strings.TrimSpace(invocation.ToolName) == "Task")
	if err != nil {
		return TaskResult{Error: err}
	}
	return adapter.awaitInvocation(ctx, waiter)
}

func (adapter *CursorAdapter) openInvocation(ctx context.Context, request TaskRequest, invocation runtimecore.ToolInvocation, expectSubagent bool) (*cursorTaskWaiter, error) {
	if adapter == nil {
		return nil, fmt.Errorf("cursor adapter is nil")
	}
	if adapter.execBridge == nil {
		return nil, fmt.Errorf("cursor exec bridge is unavailable")
	}
	if adapter.publish == nil {
		return nil, fmt.Errorf("cursor publisher is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	parentRequestID := strings.TrimSpace(request.ParentRequest)
	if parentRequestID == "" {
		return nil, fmt.Errorf("delegated cursor exec requires parent request id")
	}
	taskID := strings.TrimSpace(request.ID)
	if taskID == "" {
		return nil, fmt.Errorf("delegated cursor exec requires task id")
	}
	PublishTaskCheckpoint(ctx, request, SupervisionStatusDispatched, 1, nil, nil, "Cursor 子代理已派发", "")
	seq := adapter.sequence.Add(1)
	childConversationID := fmt.Sprintf("%s-child-conversation-%d", taskID, seq)
	childRequestID := fmt.Sprintf("%s-child-request-%d", taskID, seq)
	childToolCallID := strings.TrimSpace(invocation.CallID)
	if childToolCallID == "" {
		childToolCallID = fmt.Sprintf("%s-child-tool-%d", taskID, seq)
		invocation.CallID = childToolCallID
	}
	serverMessage, pending, err := adapter.execBridge.OpenExec(execbridge.OpenExecContext{
		ConversationID:               strings.TrimSpace(request.ConversationID),
		RootConversationID:           firstNonEmpty(strings.TrimSpace(request.RootConversationID), strings.TrimSpace(request.ConversationID)),
		ModelID:                      strings.TrimSpace(request.ModelID),
		WorkspaceHint:                strings.TrimSpace(request.WorkspaceHint),
		DirectMetaParentChild:        expectSubagent,
		SubagentModelOverrides:       cloneSubagentModelOverrides(request.SubagentModelOverrides),
		SelectedSubagentModels:       cloneRequestedModels(request.SelectedSubagentModels),
		SelectedSubagentModelDetails: cloneModelDetails(request.SelectedSubagentModelDetails),
	}, invocation)
	if err != nil {
		PublishTaskCheckpoint(ctx, request, SupervisionStatusFailed, 2, nil, nil, "Cursor 子代理初始化失败", err.Error())
		return nil, err
	}

	waiter := &cursorTaskWaiter{
		taskID:              taskID,
		parentRequestID:     parentRequestID,
		childRequestID:      childRequestID,
		childConversationID: childConversationID,
		childToolCallID:     childToolCallID,
		subagentType:        strings.TrimSpace(request.SubagentType),
		modelID:             strings.TrimSpace(request.ModelID),
		pending:             pending,
		checkpointContext:   ctx,
		checkpointRequest:   cloneTaskRequest(request),
		checkpointStep:      1,
		expectSubagent:      expectSubagent,
		resultCh:            make(chan TaskResult, 1),
	}
	adapter.registerWaiter(waiter)
	if err := adapter.publish(parentRequestID, serverMessage); err != nil {
		adapter.completeWaiterWithCheckpoint(waiter, TaskResult{Error: err, Output: err.Error()}, SupervisionStatusFailed, "Cursor 子代理派发失败", err.Error())
		return nil, err
	}
	return waiter, nil
}

func (adapter *CursorAdapter) awaitInvocation(ctx context.Context, waiter *cursorTaskWaiter) TaskResult {
	select {
	case result := <-waiter.resultCh:
		return cloneTaskResult(result)
	case <-ctx.Done():
		return adapter.cancelTask(waiter.taskID, ctx.Err())
	}
}

func (adapter *CursorAdapter) ConsumeExecMessage(parentRequestID string, message *agentv1.ExecClientMessage) bool {
	if adapter == nil || message == nil {
		return false
	}
	waiter, tombstoned := adapter.lookupWaiter(parentRequestID, message.GetExecId(), message.GetId())
	if tombstoned {
		return true
	}
	if waiter == nil {
		return false
	}
	if waiter.expectSubagent && message.GetSubagentResult() == nil {
		if message.GetShellStream() != nil {
			adapter.observeExecProgress(waiter, message)
			if adapter.hasEffectiveProgress(message, execbridge.ExecApplyResult{}) {
				adapter.publishEffectiveProgress(waiter, message, execbridge.ExecApplyResult{})
			}
			return true
		}
		err := fmt.Errorf("delegated cursor exec returned unexpected payload")
		adapter.completeWaiterWithCheckpoint(waiter, TaskResult{Error: err, Output: err.Error(), Metadata: adapter.waiterMetadata(waiter)}, SupervisionStatusFailed, "Cursor 子代理返回了无法识别的结果", err.Error())
		return true
	}
	adapter.observeExecProgress(waiter, message)
	applyResult, err := adapter.execBridge.ApplyExecClientMessage(message, adapter.pendingSnapshot(waiter))
	result := TaskResult{
		Metadata: adapter.waiterMetadata(waiter),
	}
	if err != nil {
		result.Error = err
		result.Output = err.Error()
		adapter.completeWaiterWithCheckpoint(waiter, result, SupervisionStatusFailed, "Cursor 子代理执行失败", err.Error())
		return true
	}
	if adapter.hasEffectiveProgress(message, applyResult) {
		adapter.publishEffectiveProgress(waiter, message, applyResult)
	}
	if !applyResult.IsTerminal {
		return true
	}
	result.Output = strings.TrimSpace(applyResult.ToolResultPayload)
	if !waiter.expectSubagent {
		result.ToolCallCount = 1
	}
	if subagentResult := message.GetSubagentResult(); subagentResult != nil {
		result.SubagentResult = cloneSubagentResult(subagentResult)
		if success := subagentResult.GetSuccess(); success != nil {
			result.ToolCallCount = int(success.GetToolCallCount())
		}
		if failure := subagentResult.GetError(); failure != nil {
			result.Error = errors.New(strings.TrimSpace(failure.GetError()))
			if result.Output == "" {
				result.Output = strings.TrimSpace(failure.GetError())
			}
		}
	}
	if result.Error != nil {
		adapter.completeWaiterWithCheckpoint(waiter, result, SupervisionStatusFailed, "Cursor 子代理执行失败", result.Error.Error())
	} else {
		adapter.completeWaiterWithCheckpoint(waiter, result, SupervisionStatusCompleted, "Cursor 子代理已完成", "")
	}
	return true
}

func (adapter *CursorAdapter) publishEffectiveProgress(waiter *cursorTaskWaiter, message *agentv1.ExecClientMessage, result execbridge.ExecApplyResult) {
	if adapter == nil || waiter == nil || adapter.progress == nil || message == nil {
		return
	}
	adapter.progress(waiter.parentRequestID, adapter.pendingSnapshot(waiter), message, result)
}

func (adapter *CursorAdapter) hasEffectiveProgress(message *agentv1.ExecClientMessage, result execbridge.ExecApplyResult) bool {
	if message == nil {
		return false
	}
	if result.ShellOutputDelta != nil || message.GetSubagentResult() != nil {
		return true
	}
	if message.GetShellStream() != nil {
		switch message.GetShellStream().GetEvent().(type) {
		case *agentv1.ShellStream_Stdout, *agentv1.ShellStream_Stderr, *agentv1.ShellStream_Start, *agentv1.ShellStream_Exit, *agentv1.ShellStream_Rejected, *agentv1.ShellStream_PermissionDenied, *agentv1.ShellStream_Backgrounded:
			return true
		default:
			return false
		}
	}
	return message.GetReadResult() != nil ||
		message.GetWriteResult() != nil ||
		message.GetDeleteResult() != nil ||
		message.GetGrepResult() != nil ||
		message.GetLsResult() != nil ||
		message.GetDiagnosticsResult() != nil ||
		message.GetMcpResult() != nil ||
		message.GetFetchResult() != nil ||
		message.GetExecuteHookResult() != nil ||
		message.GetWriteShellStdinResult() != nil
}

func (adapter *CursorAdapter) ConsumeExecControl(parentRequestID string, message *agentv1.ExecClientControlMessage) bool {
	if adapter == nil || message == nil {
		return false
	}
	messageID, ok := execControlMessageID(message)
	if !ok {
		return false
	}
	waiter, tombstoned := adapter.lookupWaiter(parentRequestID, "", messageID)
	if tombstoned {
		return true
	}
	if waiter == nil {
		return false
	}
	applyResult, err := adapter.execBridge.ApplyExecClientControl(message, adapter.pendingSnapshot(waiter))
	if err != nil {
		adapter.completeWaiterWithCheckpoint(waiter, TaskResult{
			Error:    err,
			Output:   err.Error(),
			Metadata: adapter.waiterMetadata(waiter),
		}, SupervisionStatusFailed, "Cursor 子代理控制通道失败", err.Error())
		return true
	}
	if !applyResult.IsTerminal {
		return true
	}
	payload := strings.TrimSpace(applyResult.ToolResultPayload)
	if payload == "" {
		payload = "delegated cursor exec terminated"
	}
	adapter.completeWaiterWithCheckpoint(waiter, TaskResult{
		Error:    errors.New(payload),
		Output:   payload,
		Metadata: adapter.waiterMetadata(waiter),
	}, SupervisionStatusFailed, "Cursor 子代理被控制通道终止", payload)
	return true
}

func (adapter *CursorAdapter) observeExecProgress(waiter *cursorTaskWaiter, message *agentv1.ExecClientMessage) {
	if adapter == nil || waiter == nil || message == nil {
		return
	}
	shellStream := message.GetShellStream()
	if shellStream == nil {
		return
	}
	adapter.mu.Lock()
	if waiter.done {
		adapter.mu.Unlock()
		return
	}
	switch event := shellStream.GetEvent().(type) {
	case *agentv1.ShellStream_Stdout:
		waiter.pending.StdoutBuffer += execbridge.DecodeShellStdout(event.Stdout)
		MarkWorkerProgress(waiter.checkpointContext)
	case *agentv1.ShellStream_Stderr:
		waiter.pending.StderrBuffer += event.Stderr.GetData()
		MarkWorkerProgress(waiter.checkpointContext)
	case *agentv1.ShellStream_Start:
		MarkWorkerProgress(waiter.checkpointContext)
	}
	waiter.checkpointStep++
	step := waiter.checkpointStep
	checkpointContext := waiter.checkpointContext
	checkpointRequest := cloneTaskRequest(waiter.checkpointRequest)
	adapter.mu.Unlock()
	PublishTaskCheckpoint(checkpointContext, checkpointRequest, SupervisionStatusRunning, step, nil, nil, "Cursor 子代理仍在执行", "")
}

func (adapter *CursorAdapter) CancelTask(taskID string, cause error) bool {
	if adapter == nil {
		return false
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return false
	}
	adapter.mu.Lock()
	waiter := adapter.waitersByTaskID[taskID]
	adapter.mu.Unlock()
	if waiter == nil {
		return false
	}
	if !adapter.claimWaiter(waiter) {
		return false
	}
	if cause == nil {
		cause = context.Canceled
	}
	result := TaskResult{
		Error:    cause,
		Output:   strings.TrimSpace(errorString(cause)),
		Metadata: adapter.waiterMetadata(waiter),
	}
	if result.Output == "" {
		result.Output = "subagent canceled"
	}
	adapter.publishWaiterCheckpoint(waiter, SupervisionStatusCanceled, "Cursor 子代理已取消", result.Output)
	if err := adapter.publish(waiter.parentRequestID, buildCursorExecAbort(adapter.pendingSnapshot(waiter))); err != nil {
		result.Error = errors.Join(cause, fmt.Errorf("publish delegated cursor abort: %w", err))
	}
	adapter.finishClaimedWaiter(waiter, TaskResult{
		Error:    result.Error,
		Output:   result.Output,
		Metadata: result.Metadata,
	})
	return true
}

func (adapter *CursorAdapter) publishWaiterCheckpoint(waiter *cursorTaskWaiter, phase SupervisionStatus, progress, blocker string) {
	if adapter == nil || waiter == nil {
		return
	}
	adapter.mu.Lock()
	checkpointContext := waiter.checkpointContext
	checkpointRequest := cloneTaskRequest(waiter.checkpointRequest)
	waiter.checkpointStep++
	step := waiter.checkpointStep
	adapter.mu.Unlock()
	PublishTaskCheckpoint(checkpointContext, checkpointRequest, phase, step, nil, nil, progress, blocker)
}

func (adapter *CursorAdapter) pendingSnapshot(waiter *cursorTaskWaiter) runtimecore.PendingExec {
	if adapter == nil || waiter == nil {
		return runtimecore.PendingExec{}
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return waiter.pending
}

func (adapter *CursorAdapter) cancelTask(taskID string, cause error) TaskResult {
	result := TaskResult{
		Error:    cause,
		Output:   strings.TrimSpace(errorString(cause)),
		Metadata: map[string]string{"task_id": strings.TrimSpace(taskID)},
	}
	if result.Output == "" {
		result.Output = "subagent canceled"
	}
	adapter.mu.Lock()
	waiter := adapter.waitersByTaskID[strings.TrimSpace(taskID)]
	adapter.mu.Unlock()
	if waiter == nil {
		return result
	}
	adapter.CancelTask(taskID, cause)
	select {
	case waiterResult := <-waiter.resultCh:
		return cloneTaskResult(waiterResult)
	default:
		return result
	}
}

func (adapter *CursorAdapter) waiterMetadata(waiter *cursorTaskWaiter) map[string]string {
	if waiter == nil {
		return nil
	}
	return map[string]string{
		"task_id":         waiter.taskID,
		"request_id":      waiter.childRequestID,
		"conversation_id": waiter.childConversationID,
		"exec_id":         strings.TrimSpace(waiter.pending.ExecID),
		"tool_call_id":    waiter.childToolCallID,
		"subagent_type":   waiter.subagentType,
		"model_id":        waiter.modelID,
	}
}

func (adapter *CursorAdapter) registerWaiter(waiter *cursorTaskWaiter) {
	if adapter == nil || waiter == nil {
		return
	}
	now := adapter.now()
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.pruneTombstonesLocked(now)
	adapter.waitersByExecID[strings.TrimSpace(waiter.pending.ExecID)] = waiter
	if waiter.pending.MessageID != 0 {
		adapter.waitersByMessageID[waiter.pending.MessageID] = waiter
	}
	adapter.waitersByTaskID[strings.TrimSpace(waiter.taskID)] = waiter
}

func (adapter *CursorAdapter) unregisterWaiter(waiter *cursorTaskWaiter, tombstone bool) {
	if adapter == nil || waiter == nil {
		return
	}
	now := adapter.now()
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	delete(adapter.waitersByExecID, strings.TrimSpace(waiter.pending.ExecID))
	if waiter.pending.MessageID != 0 {
		delete(adapter.waitersByMessageID, waiter.pending.MessageID)
	}
	delete(adapter.waitersByTaskID, strings.TrimSpace(waiter.taskID))
	if tombstone {
		entry := cursorExecTombstone{
			parentRequestID: strings.TrimSpace(waiter.parentRequestID),
			createdAt:       now,
		}
		if execID := strings.TrimSpace(waiter.pending.ExecID); execID != "" {
			adapter.tombstoneExecIDs[execID] = entry
		}
		if waiter.pending.MessageID != 0 {
			adapter.tombstoneMessageIDs[waiter.pending.MessageID] = entry
		}
	}
	adapter.pruneTombstonesLocked(now)
}

func (adapter *CursorAdapter) lookupWaiter(parentRequestID, execID string, messageID uint32) (*cursorTaskWaiter, bool) {
	if adapter == nil {
		return nil, false
	}
	now := adapter.now()
	parentRequestID = strings.TrimSpace(parentRequestID)
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.pruneTombstonesLocked(now)
	execID = strings.TrimSpace(execID)
	if execID != "" {
		if waiter := adapter.waitersByExecID[execID]; waiter != nil {
			if strings.TrimSpace(waiter.parentRequestID) != parentRequestID {
				return nil, false
			}
			// exec_id is the stable child execution identity. The bridge message
			// id is transport metadata and may be zero or reassigned by Cursor.
			return waiter, false
		}
		if entry, ok := adapter.tombstoneExecIDs[execID]; ok {
			if entry.parentRequestID != parentRequestID {
				return nil, false
			}
			// A matching exec tombstone is sufficient to absorb a late result;
			// do not require the stale transport message id to match as well.
			return nil, true
		}
		return nil, false
	}
	if messageID != 0 {
		if waiter := adapter.waitersByMessageID[messageID]; waiter != nil {
			if strings.TrimSpace(waiter.parentRequestID) != parentRequestID {
				return nil, false
			}
			return waiter, false
		}
		if entry, ok := adapter.tombstoneMessageIDs[messageID]; ok && entry.parentRequestID == parentRequestID {
			return nil, true
		}
	}
	return nil, false
}

func buildCursorTaskArgs(request TaskRequest) ([]byte, error) {
	args := map[string]any{}
	if len(request.ArgsJSON) > 0 {
		decoded, err := runtimecore.DecodeArgsMap(request.ArgsJSON)
		if err != nil {
			return nil, fmt.Errorf("decode delegated cursor task args: %w", err)
		}
		args = decoded
	}
	if strings.TrimSpace(runtimecore.ReadStringArg(args, "subagent_type", "subagentType")) == "" {
		args["subagent_type"] = firstNonEmpty(request.SubagentType, "generalPurpose")
	}
	if strings.TrimSpace(runtimecore.ReadStringArg(args, "prompt")) == "" && strings.TrimSpace(request.Prompt) != "" {
		args["prompt"] = strings.TrimSpace(request.Prompt)
	}
	if strings.TrimSpace(runtimecore.ReadStringArg(args, "model", "model_id", "modelId")) == "" && strings.TrimSpace(request.ModelID) != "" {
		args["model"] = strings.TrimSpace(request.ModelID)
	}
	if request.Readonly {
		args["readonly"] = true
	}
	if request.RunInBackground {
		args["run_in_background"] = true
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("encode delegated cursor task args: %w", err)
	}
	return encoded, nil
}

func buildCursorExecAbort(pending runtimecore.PendingExec) *agentv1.AgentServerMessage {
	return &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerControlMessage{
			ExecServerControlMessage: &agentv1.ExecServerControlMessage{
				Message: &agentv1.ExecServerControlMessage_Abort{
					Abort: &agentv1.ExecServerAbort{Id: pending.MessageID},
				},
			},
		},
	}
}

func (adapter *CursorAdapter) completeWaiterWithCheckpoint(waiter *cursorTaskWaiter, result TaskResult, phase SupervisionStatus, progress, blocker string) bool {
	if !adapter.claimWaiter(waiter) {
		return false
	}
	adapter.publishWaiterCheckpoint(waiter, phase, progress, blocker)
	adapter.finishClaimedWaiter(waiter, result)
	return true
}

func (adapter *CursorAdapter) claimWaiter(waiter *cursorTaskWaiter) bool {
	if adapter == nil || waiter == nil {
		return false
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if waiter.done {
		return false
	}
	waiter.done = true
	return true
}

func (adapter *CursorAdapter) finishClaimedWaiter(waiter *cursorTaskWaiter, result TaskResult) {
	if adapter == nil || waiter == nil {
		return
	}
	adapter.unregisterWaiter(waiter, true)
	select {
	case waiter.resultCh <- cloneTaskResult(result):
	default:
	}
}

func (adapter *CursorAdapter) pruneTombstonesLocked(now time.Time) {
	if adapter == nil {
		return
	}
	for execID, tombstone := range adapter.tombstoneExecIDs {
		if now.Sub(tombstone.createdAt) >= adapter.retention {
			delete(adapter.tombstoneExecIDs, execID)
		}
	}
	for messageID, tombstone := range adapter.tombstoneMessageIDs {
		if now.Sub(tombstone.createdAt) >= adapter.retention {
			delete(adapter.tombstoneMessageIDs, messageID)
		}
	}
}

func cloneSubagentResult(source *agentv1.SubagentResult) *agentv1.SubagentResult {
	if source == nil {
		return nil
	}
	cloned, _ := proto.Clone(source).(*agentv1.SubagentResult)
	return cloned
}

func execControlMessageID(message *agentv1.ExecClientControlMessage) (uint32, bool) {
	if message == nil {
		return 0, false
	}
	switch item := message.GetMessage().(type) {
	case *agentv1.ExecClientControlMessage_StreamClose:
		return item.StreamClose.GetId(), true
	case *agentv1.ExecClientControlMessage_Throw:
		return item.Throw.GetId(), true
	case *agentv1.ExecClientControlMessage_Heartbeat:
		return item.Heartbeat.GetId(), true
	default:
		return 0, false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
