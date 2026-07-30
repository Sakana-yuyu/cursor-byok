package forwarder

import (
	"context"
	"path/filepath"
	"strings"

	"cursor/gen/agentv1"
	execbridge "cursor/internal/backend/agent/bridge/exec"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
)

type cursorDelegationBridge struct {
	scheduler *delegation.Scheduler
	cursor    *delegation.CursorAdapter
}

func newCursorDelegationBridge(service *Service) *cursorDelegationBridge {
	if service == nil || service.execBridge == nil || service.broker == nil {
		return nil
	}
	cursor := delegation.NewCursorAdapter(service.execBridge, func(requestID string, message *agentv1.AgentServerMessage) error {
		return service.broker.Publish(strings.TrimSpace(requestID), StreamEvent{Message: message})
	})
	return &cursorDelegationBridge{
		scheduler: delegation.NewScheduler(delegation.Config{}, cursor.Execute),
		cursor:    cursor,
	}
}

func (bridge *cursorDelegationBridge) Close() {
	if bridge == nil || bridge.scheduler == nil {
		return
	}
	bridge.scheduler.Close()
}

func (bridge *cursorDelegationBridge) Submit(ctx context.Context, request delegation.TaskRequest) (string, error) {
	if bridge == nil || bridge.scheduler == nil {
		return "", context.Canceled
	}
	taskID, err := bridge.scheduler.Submit(request)
	if err != nil {
		return "", err
	}
	if ctx == nil {
		return taskID, nil
	}
	go func(taskID string) {
		<-ctx.Done()
		_ = bridge.scheduler.Cancel(taskID)
	}(taskID)
	return taskID, nil
}

func (bridge *cursorDelegationBridge) ConsumeExecMessage(message *agentv1.ExecClientMessage) bool {
	if bridge == nil || bridge.cursor == nil {
		return false
	}
	return bridge.cursor.ConsumeExecMessage(message)
}

func (bridge *cursorDelegationBridge) ConsumeExecControl(message *agentv1.ExecClientControlMessage) bool {
	if bridge == nil || bridge.cursor == nil {
		return false
	}
	return bridge.cursor.ConsumeExecControl(message)
}

func (bridge *cursorDelegationBridge) ConsumeConversationAction(message *agentv1.AgentClientMessage) bool {
	if bridge == nil || bridge.cursor == nil || message == nil {
		return false
	}
	action := message.GetConversationAction()
	if action == nil {
		return false
	}
	switch item := action.GetAction().(type) {
	case *agentv1.ConversationAction_CancelSubagentAction:
		if item.CancelSubagentAction == nil {
			return false
		}
		return bridge.cursor.CancelTask(item.CancelSubagentAction.GetSubagentId(), context.Canceled)
	default:
		return false
	}
}

func buildExecOpenContextForStream(stream *ActiveStream, overrides map[string]runtimecore.SubagentModelOverrideSelection) execbridge.OpenExecContext {
	if stream == nil {
		return execbridge.OpenExecContext{
			SubagentModelOverrides: cloneSubagentModelOverrides(overrides),
		}
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	conversationID := strings.TrimSpace(stream.ConversationID)
	rootConversationID := conversationID
	if stream.CheckpointConversation != nil {
		rootConversationID = firstNonEmpty(
			strings.TrimSpace(stream.CheckpointConversation.RootConversationID),
			strings.TrimSpace(stream.CheckpointConversation.ConversationID),
			conversationID,
		)
	}
	return execbridge.OpenExecContext{
		ConversationID:               conversationID,
		RootConversationID:           rootConversationID,
		ModelID:                      strings.TrimSpace(stream.ModelID),
		WorkspaceHint:                workspaceHintFromStreamLocked(stream),
		SubagentModelOverrides:       cloneSubagentModelOverrides(firstSubagentOverrides(overrides, stream.SubagentModelOverrides)),
		SelectedSubagentModels:       cloneSelectedSubagentModels(stream.SelectedSubagentModels),
		SelectedSubagentModelDetails: cloneSelectedSubagentModelDetails(stream.SelectedSubagentModelDetails),
	}
}

func firstSubagentOverrides(values ...map[string]runtimecore.SubagentModelOverrideSelection) map[string]runtimecore.SubagentModelOverrideSelection {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func buildDelegatedCursorTaskRequest(stream *ActiveStream, pending runtimecore.PendingExec, invocation runtimecore.ToolInvocation, executionMode string, modelGroupID string) delegation.TaskRequest {
	openContext := buildExecOpenContextForStream(stream, nil)
	return delegation.TaskRequest{
		ParentRequest:                activeStreamRequestID(stream),
		ParentExecID:                 strings.TrimSpace(pending.ExecID),
		ParentToolCall:               strings.TrimSpace(invocation.CallID),
		ConversationID:               openContext.ConversationID,
		RootConversationID:           openContext.RootConversationID,
		ArgsJSON:                     append([]byte(nil), invocation.ArgsJSON...),
		SubagentType:                 delegatedTaskSubagentType(invocation.ArgsJSON),
		ModelID:                      openContext.ModelID,
		SubagentModelOverrides:       cloneSubagentModelOverrides(openContext.SubagentModelOverrides),
		SelectedSubagentModels:       cloneSelectedSubagentModels(openContext.SelectedSubagentModels),
		SelectedSubagentModelDetails: cloneSelectedSubagentModelDetails(openContext.SelectedSubagentModelDetails),
		ModelGroupID:                 strings.TrimSpace(modelGroupID),
		ExecutionMode:                strings.TrimSpace(executionMode),
		WorkspaceHint:                openContext.WorkspaceHint,
	}
}

func workspaceHintFromStreamLocked(stream *ActiveStream) string {
	if stream == nil {
		return ""
	}
	for _, path := range stream.WorkspacePaths {
		if trimmed := strings.TrimSpace(path); trimmed != "" {
			return trimmed
		}
	}
	if folder := strings.TrimSpace(stream.TerminalsFolder); folder != "" {
		return filepath.Clean(folder)
	}
	return ""
}

func delegatedTaskSubagentType(argsJSON []byte) string {
	args, err := runtimecore.DecodeArgsMap(argsJSON)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(runtimecore.ReadStringArg(args, "subagent_type", "subagentType"))
}
