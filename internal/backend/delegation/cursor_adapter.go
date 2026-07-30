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

const defaultCursorAdapterRetention = 2 * time.Minute

type CursorPublisher func(requestID string, message *agentv1.AgentServerMessage) error

type CursorAdapter struct {
	execBridge execbridge.ExecBridge
	publish    CursorPublisher
	retention  time.Duration
	now        func() time.Time
	sequence   atomic.Uint64

	mu                  sync.Mutex
	waitersByExecID     map[string]*cursorTaskWaiter
	waitersByMessageID  map[uint32]*cursorTaskWaiter
	waitersByTaskID     map[string]*cursorTaskWaiter
	tombstoneExecIDs    map[string]time.Time
	tombstoneMessageIDs map[uint32]time.Time
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
	expectSubagent      bool
	done                bool
	resultCh            chan TaskResult
}

func NewCursorAdapter(bridge execbridge.ExecBridge, publish CursorPublisher) *CursorAdapter {
	return &CursorAdapter{
		execBridge:          bridge,
		publish:             publish,
		retention:           defaultCursorAdapterRetention,
		now:                 func() time.Time { return time.Now().UTC() },
		waitersByExecID:     make(map[string]*cursorTaskWaiter),
		waitersByMessageID:  make(map[uint32]*cursorTaskWaiter),
		waitersByTaskID:     make(map[string]*cursorTaskWaiter),
		tombstoneExecIDs:    make(map[string]time.Time),
		tombstoneMessageIDs: make(map[uint32]time.Time),
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
	return adapter.executeInvocation(ctx, request, invocation, true)
}

func (adapter *CursorAdapter) ExecuteTool(ctx context.Context, request TaskRequest, invocation runtimecore.ToolInvocation) TaskResult {
	return adapter.executeInvocation(ctx, request, invocation, strings.TrimSpace(invocation.ToolName) == "Task")
}

func (adapter *CursorAdapter) executeInvocation(ctx context.Context, request TaskRequest, invocation runtimecore.ToolInvocation, expectSubagent bool) TaskResult {
	if adapter == nil {
		return TaskResult{Error: fmt.Errorf("cursor adapter is nil")}
	}
	if adapter.execBridge == nil {
		return TaskResult{Error: fmt.Errorf("cursor exec bridge is unavailable")}
	}
	if adapter.publish == nil {
		return TaskResult{Error: fmt.Errorf("cursor publisher is unavailable")}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	parentRequestID := strings.TrimSpace(request.ParentRequest)
	if parentRequestID == "" {
		return TaskResult{Error: fmt.Errorf("delegated cursor exec requires parent request id")}
	}
	taskID := strings.TrimSpace(request.ID)
	if taskID == "" {
		return TaskResult{Error: fmt.Errorf("delegated cursor exec requires task id")}
	}
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
		return TaskResult{Error: err}
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
		expectSubagent:      expectSubagent,
		resultCh:            make(chan TaskResult, 1),
	}
	adapter.registerWaiter(waiter)
	if err := adapter.publish(parentRequestID, serverMessage); err != nil {
		adapter.unregisterWaiter(waiter, false)
		return TaskResult{Error: err}
	}

	select {
	case result := <-waiter.resultCh:
		return cloneTaskResult(result)
	case <-ctx.Done():
		return adapter.cancelTask(waiter.taskID, ctx.Err())
	}
}

func (adapter *CursorAdapter) ConsumeExecMessage(message *agentv1.ExecClientMessage) bool {
	if adapter == nil || message == nil {
		return false
	}
	waiter, tombstoned := adapter.lookupWaiter(message.GetExecId(), message.GetId())
	if tombstoned {
		return true
	}
	if waiter == nil {
		return false
	}
	if waiter.expectSubagent && message.GetSubagentResult() == nil {
		err := fmt.Errorf("delegated cursor exec returned unexpected payload")
		adapter.completeWaiter(waiter, TaskResult{Error: err, Output: err.Error(), Metadata: adapter.waiterMetadata(waiter)})
		return true
	}
	adapter.observeExecProgress(waiter, message)
	applyResult, err := adapter.execBridge.ApplyExecClientMessage(message, waiter.pending)
	result := TaskResult{
		Metadata: adapter.waiterMetadata(waiter),
	}
	if err != nil {
		result.Error = err
		result.Output = err.Error()
		adapter.completeWaiter(waiter, result)
		return true
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
	adapter.completeWaiter(waiter, result)
	return true
}

func (adapter *CursorAdapter) ConsumeExecControl(message *agentv1.ExecClientControlMessage) bool {
	if adapter == nil || message == nil {
		return false
	}
	messageID, ok := execControlMessageID(message)
	if !ok {
		return false
	}
	waiter, tombstoned := adapter.lookupWaiter("", messageID)
	if tombstoned {
		return true
	}
	if waiter == nil {
		return false
	}
	applyResult, err := adapter.execBridge.ApplyExecClientControl(message, waiter.pending)
	if err != nil {
		adapter.completeWaiter(waiter, TaskResult{
			Error:    err,
			Output:   err.Error(),
			Metadata: adapter.waiterMetadata(waiter),
		})
		return true
	}
	if !applyResult.IsTerminal {
		return true
	}
	payload := strings.TrimSpace(applyResult.ToolResultPayload)
	if payload == "" {
		payload = "delegated cursor exec terminated"
	}
	adapter.completeWaiter(waiter, TaskResult{
		Error:    errors.New(payload),
		Output:   payload,
		Metadata: adapter.waiterMetadata(waiter),
	})
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
	defer adapter.mu.Unlock()
	switch event := shellStream.GetEvent().(type) {
	case *agentv1.ShellStream_Stdout:
		waiter.pending.StdoutBuffer += execbridge.DecodeShellStdout(event.Stdout)
	case *agentv1.ShellStream_Stderr:
		waiter.pending.StderrBuffer += event.Stderr.GetData()
	}
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
	if err := adapter.publish(waiter.parentRequestID, buildCursorExecAbort(waiter.pending)); err != nil {
		result.Error = errors.Join(cause, fmt.Errorf("publish delegated cursor abort: %w", err))
	}
	adapter.completeWaiter(waiter, TaskResult{
		Error:    result.Error,
		Output:   result.Output,
		Metadata: result.Metadata,
	})
	return true
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
		if execID := strings.TrimSpace(waiter.pending.ExecID); execID != "" {
			adapter.tombstoneExecIDs[execID] = now
		}
		if waiter.pending.MessageID != 0 {
			adapter.tombstoneMessageIDs[waiter.pending.MessageID] = now
		}
	}
	adapter.pruneTombstonesLocked(now)
}

func (adapter *CursorAdapter) lookupWaiter(execID string, messageID uint32) (*cursorTaskWaiter, bool) {
	if adapter == nil {
		return nil, false
	}
	now := adapter.now()
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.pruneTombstonesLocked(now)
	execID = strings.TrimSpace(execID)
	if execID != "" {
		if waiter := adapter.waitersByExecID[execID]; waiter != nil {
			if messageID != 0 && waiter.pending.MessageID != messageID {
				return nil, false
			}
			return waiter, false
		}
		if _, ok := adapter.tombstoneExecIDs[execID]; ok {
			return nil, true
		}
		return nil, false
	}
	if messageID != 0 {
		if waiter := adapter.waitersByMessageID[messageID]; waiter != nil {
			return waiter, false
		}
		if _, ok := adapter.tombstoneMessageIDs[messageID]; ok {
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

func (adapter *CursorAdapter) completeWaiter(waiter *cursorTaskWaiter, result TaskResult) {
	if adapter == nil || waiter == nil {
		return
	}
	adapter.mu.Lock()
	if waiter.done {
		adapter.mu.Unlock()
		return
	}
	waiter.done = true
	adapter.mu.Unlock()
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
	for execID, createdAt := range adapter.tombstoneExecIDs {
		if now.Sub(createdAt) >= adapter.retention {
			delete(adapter.tombstoneExecIDs, execID)
		}
	}
	for messageID, createdAt := range adapter.tombstoneMessageIDs {
		if now.Sub(createdAt) >= adapter.retention {
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
