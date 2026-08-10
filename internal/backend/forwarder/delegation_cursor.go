package forwarder

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"cursor/gen/agentv1"
	execbridge "cursor/internal/backend/agent/bridge/exec"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
)

type cursorDelegationBridge struct {
	cursor *delegation.CursorAdapter
}

// CursorAgentExecutionAvailable 只在 Cursor 客户端仍订阅活动父请求时返回 true。
// parentRequestID 为空时检查任一活动请求，供 executor probe 使用。
func (service *Service) CursorAgentExecutionAvailable(parentRequestID string) bool {
	if service == nil || service.cursorDelegation == nil || service.cursorDelegation.cursor == nil || service.broker == nil {
		return false
	}
	parentRequestID = strings.TrimSpace(parentRequestID)
	if parentRequestID != "" {
		return service.cursorAgentStreamAvailable(parentRequestID)
	}
	for _, requestID := range service.broker.ActiveRequestIDs() {
		if service.cursorAgentStreamAvailable(requestID) {
			return true
		}
	}
	return false
}

func (service *Service) cursorAgentStreamAvailable(requestID string) bool {
	stream, ok := service.broker.Get(requestID)
	if !ok || stream == nil {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if strings.TrimSpace(stream.ConversationID) == "" || len(stream.Subscribers) == 0 || isTerminalStreamStatus(stream.Status) {
		return false
	}
	switch stream.Phase {
	case TurnPhaseCanceled, TurnPhaseCompleted, TurnPhaseFailed:
		return false
	default:
		return true
	}
}

func newCursorDelegationBridge(service *Service) *cursorDelegationBridge {
	if service == nil || service.execBridge == nil || service.broker == nil {
		return nil
	}
	cursor := delegation.NewCursorAdapterWithProgress(service.execBridge, func(requestID string, message *agentv1.AgentServerMessage) error {
		return service.broker.PublishToActiveSubscriber(strings.TrimSpace(requestID), StreamEvent{Message: message})
	}, func(parentRequestID string, pending runtimecore.PendingExec, message *agentv1.ExecClientMessage, result execbridge.ExecApplyResult) {
		if strings.TrimSpace(pending.ExecKind) != "subagent" {
			return
		}
		if service.markNativeDelegationEffectiveProgress(pending.ExecID, "Cursor 子代理收到真实执行结果") {
			service.rescheduleExecWatchdog(parentRequestID, pending)
		}
	})
	return &cursorDelegationBridge{
		cursor: cursor,
	}
}

func (bridge *cursorDelegationBridge) Close() {
}

func (service *Service) ExecuteCursorAgent(ctx context.Context, request delegation.TaskRequest) delegation.TaskResult {
	if service == nil || service.cursorDelegation == nil || service.cursorDelegation.cursor == nil {
		return delegation.TaskResult{Error: delegation.NewClassifiedExecutorError(
			delegation.ExecutorFailureSwitchable,
			true,
			"cursor_agent_unavailable",
			errors.New("Cursor agent bridge is unavailable"),
		)}
	}
	return service.cursorDelegation.cursor.Execute(ctx, request)
}

func (bridge *cursorDelegationBridge) ConsumeExecMessage(requestID string, message *agentv1.ExecClientMessage) bool {
	if bridge == nil || bridge.cursor == nil {
		return false
	}
	return bridge.cursor.ConsumeExecMessage(requestID, message)
}

func (bridge *cursorDelegationBridge) ConsumeExecControl(requestID string, message *agentv1.ExecClientControlMessage) bool {
	if bridge == nil || bridge.cursor == nil {
		return false
	}
	return bridge.cursor.ConsumeExecControl(requestID, message)
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

// buildNativeTaskOpenContextForStream selects Cursor's direct parent-child
// protocol only for a Task started from the parent conversation. Other exec
// tools must retain the generic context.
func buildNativeTaskOpenContextForStream(stream *ActiveStream, overrides map[string]runtimecore.SubagentModelOverrideSelection) execbridge.OpenExecContext {
	openContext := buildExecOpenContextForStream(stream, overrides)
	openContext.DirectMetaParentChild = true
	return openContext
}

func firstSubagentOverrides(values ...map[string]runtimecore.SubagentModelOverrideSelection) map[string]runtimecore.SubagentModelOverrideSelection {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func buildDelegatedCursorTaskRequest(stream *ActiveStream, pending runtimecore.PendingExec, invocation runtimecore.ToolInvocation, executionMode string, modelGroupID string, subagentProfiles map[string]string) delegation.TaskRequest {
	openContext := buildExecOpenContextForStream(stream, nil)
	args, _ := runtimecore.DecodeArgsMap(invocation.ArgsJSON)
	parentModel := ""
	if stream != nil {
		stream.mu.Lock()
		parentModel = firstNonEmpty(stream.ModelName, stream.ModelID)
		stream.mu.Unlock()
	}
	subagentType := delegatedTaskSubagentType(invocation.ArgsJSON)
	// C1 子代理注册表：按 subagent_type 注入角色片段（配置覆盖 > 内置；缺省类型原样透传）。
	prompt := runtimecore.ApplySubagentPromptFragment(subagentType, runtimecore.ReadStringArg(args, "prompt"), subagentProfiles)
	// SubagentProfile.ToolWhitelist 注入：由 filterDelegatedTools 在 worker 侧强制。
	var toolWhitelist []string
	// SubagentProfile.MaxSteps 注入：由 Execute 循环在 worker 侧强制。
	var maxSteps int
	if profile, ok := runtimecore.LookupSubagentProfile(subagentType); ok {
		toolWhitelist = profile.ToolWhitelist
		maxSteps = profile.MaxSteps
	}
	return delegation.TaskRequest{
		ParentRequest:                activeStreamRequestID(stream),
		ParentExecID:                 strings.TrimSpace(pending.ExecID),
		ParentToolCall:               strings.TrimSpace(invocation.CallID),
		ConversationID:               openContext.ConversationID,
		RootConversationID:           openContext.RootConversationID,
		ArgsJSON:                     append([]byte(nil), invocation.ArgsJSON...),
		SubagentType:                 subagentType,
		Prompt:                       strings.TrimSpace(prompt),
		Description:                  strings.TrimSpace(runtimecore.ReadStringArg(args, "description")),
		Readonly:                     runtimecore.ReadBoolArg(args, "readonly", "readOnly"),
		RunInBackground:              runtimecore.ReadBoolArg(args, "run_in_background", "runInBackground"),
		ModelID:                      firstNonEmpty(runtimecore.ReadStringArg(args, "model", "model_id", "modelId"), openContext.ModelID),
		ModelName:                    parentModel,
		SubagentModelOverrides:       cloneSubagentModelOverrides(openContext.SubagentModelOverrides),
		SelectedSubagentModels:       cloneSelectedSubagentModels(openContext.SelectedSubagentModels),
		SelectedSubagentModelDetails: cloneSelectedSubagentModelDetails(openContext.SelectedSubagentModelDetails),
		ModelGroupID:                 strings.TrimSpace(modelGroupID),
		ExecutionMode:                strings.TrimSpace(executionMode),
		WorkspaceHint:                openContext.WorkspaceHint,
		ToolWhitelist:                toolWhitelist,
		MaxSteps:                     maxSteps,
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
