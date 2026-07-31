package forwarder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
)

const defaultLocalDelegationMaxProviderPasses = 32

type LocalDelegatedToolExecutor func(context.Context, delegation.TaskRequest, runtimecore.ToolInvocation) (string, error)

type localDelegatedAgentAdapter struct {
	store         *ConversationFileStore
	compiler      PromptCompiler
	provider      ProviderGateway
	recorder      modeladapter.LLMArtifactObserver
	resolveBudget func(string, string, *ConversationFile, CompiledConversation) (int, map[string]any)
	toolExecutor  LocalDelegatedToolExecutor
	maxPasses     int
	sequence      atomic.Uint64
}

func newLocalDelegatedAgentAdapter(service *Service) *localDelegatedAgentAdapter {
	if service == nil {
		return nil
	}
	return &localDelegatedAgentAdapter{
		store:         service.store,
		compiler:      service.compiler,
		provider:      service.provider,
		recorder:      service.recorder,
		resolveBudget: service.resolveProviderOutputBudget,
		toolExecutor:  service.executeLocalDelegatedTool,
		maxPasses:     defaultLocalDelegationMaxProviderPasses,
	}
}

func (adapter *localDelegatedAgentAdapter) Execute(ctx context.Context, request delegation.TaskRequest) delegation.TaskResult {
	if adapter == nil || adapter.compiler == nil || adapter.provider == nil {
		return delegation.TaskResult{Error: fmt.Errorf("local delegated adapter is unavailable")}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return delegation.TaskResult{Error: err}
	}

	identity := adapter.newTaskIdentity(request)
	delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusDispatched, 1, nil, nil, "delegated worker dispatched", "")
	conversation, err := adapter.buildChildConversation(request, identity)
	if err != nil {
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 1, nil, nil, "delegated worker could not initialize", delegation.SanitizeSupervisorText(err.Error(), request.WorkspaceHint))
		return delegation.TaskResult{Error: err, Metadata: identity.metadata(0)}
	}
	mode := request.Mode
	if mode == agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
		mode = agentv1.AgentMode_AGENT_MODE_AGENT
	}
	compiled, err := adapter.compiler.Compile(conversation, mode, strings.TrimSpace(request.Prompt), strings.TrimSpace(request.ModelName))
	if err != nil {
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 1, nil, nil, "delegated worker prompt compilation failed", delegation.SanitizeSupervisorText(err.Error(), request.WorkspaceHint))
		return delegation.TaskResult{Error: err, Metadata: identity.metadata(0)}
	}
	compiled = guardCompiledConversationForProvider(compiled)
	mcpToolNames, err := conversationMCPToolNameSet(conversation)
	if err != nil {
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 1, nil, nil, "delegated worker MCP discovery failed", delegation.SanitizeSupervisorText(err.Error(), request.WorkspaceHint))
		return delegation.TaskResult{Error: err, Metadata: identity.metadata(0)}
	}
	compiled.Tools, err = filterDelegatedTools(compiled.Tools, request.ToolPermission, mcpToolNames)
	if err != nil {
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 1, nil, nil, "delegated worker tool filtering failed", delegation.SanitizeSupervisorText(err.Error(), request.WorkspaceHint))
		return delegation.TaskResult{Error: err, Metadata: identity.metadata(0)}
	}
	messages := cloneDelegatedMessages(compiled.Messages)
	if prompt := strings.TrimSpace(request.Prompt); prompt != "" {
		messages = append(messages, modeladapter.Message{Role: "user", Content: prompt})
	}

	toolCallCount := 0
	maxPasses := adapter.maxPasses
	if maxPasses <= 0 {
		maxPasses = defaultLocalDelegationMaxProviderPasses
	}
	for providerPass := 1; providerPass <= maxPasses; providerPass++ {
		if err := ctx.Err(); err != nil {
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCanceled, providerPass, nil, nil, "delegated worker canceled", "")
			return delegation.TaskResult{Error: err, ToolCallCount: toolCallCount, Metadata: identity.metadata(providerPass)}
		}
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusRunning, providerPass, nil, nil, "delegated worker is running", "")
		pass, err := adapter.runProviderPass(ctx, request, identity, conversation, compiled, messages, providerPass)
		if err != nil {
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, providerPass, nil, nil, "delegated provider failed", delegation.SanitizeSupervisorText(err.Error(), request.WorkspaceHint))
			return delegation.TaskResult{Error: err, Output: strings.TrimSpace(pass.text), ToolCallCount: toolCallCount, Metadata: identity.metadata(providerPass)}
		}
		if len(pass.invocations) == 0 {
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCompleted, providerPass, nil, nil, "delegated worker completed", "")
			return delegation.TaskResult{Output: strings.TrimSpace(pass.text), ToolCallCount: toolCallCount, Metadata: identity.metadata(providerPass)}
		}

		messages = append(messages, buildDelegatedAssistantToolMessage(pass.text, pass.invocations))
		for _, invocation := range pass.invocations {
			toolCallCount++
			toolSignature := delegation.NormalizeToolSignature(invocation.ToolName, invocation.ArgsJSON)
			changedFiles := localDelegationCheckpointChangedFiles(invocation)
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusRunning, providerPass, []string{toolSignature}, changedFiles, "delegated tool is running", "")
			resultText := adapter.executeTool(ctx, request, conversation, invocation)
			messages = append(messages, modeladapter.Message{
				Role:       "tool",
				Content:    resultText,
				ToolCallID: strings.TrimSpace(invocation.CallID),
				Name:       strings.TrimSpace(invocation.ToolName),
			})
		}
	}
	delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, maxPasses, nil, nil, "delegated worker exceeded provider pass limit", fmt.Sprintf("provider pass limit: %d", maxPasses))
	return delegation.TaskResult{
		Error:         fmt.Errorf("local delegated agent exceeded %d provider passes", maxPasses),
		ToolCallCount: toolCallCount,
		Metadata:      identity.metadata(maxPasses),
	}
}

type localDelegatedIdentity struct {
	taskID         string
	requestID      string
	conversationID string
	runID          string
}

func (adapter *localDelegatedAgentAdapter) newTaskIdentity(request delegation.TaskRequest) localDelegatedIdentity {
	sequence := adapter.sequence.Add(1)
	taskID := strings.TrimSpace(request.ID)
	if taskID == "" {
		taskID = fmt.Sprintf("local-delegated-%d", sequence)
	}
	prefix := fmt.Sprintf("%s-local-%d", taskID, sequence)
	return localDelegatedIdentity{
		taskID:         taskID,
		requestID:      prefix + "-request",
		conversationID: prefix + "-conversation",
		runID:          prefix + "-run",
	}
}

func (identity localDelegatedIdentity) metadata(providerPass int) map[string]string {
	metadata := map[string]string{
		"task_id":         identity.taskID,
		"request_id":      identity.requestID,
		"conversation_id": identity.conversationID,
		"run_id":          identity.runID,
	}
	if providerPass > 0 {
		metadata["provider_pass"] = fmt.Sprintf("%d", providerPass)
	}
	return metadata
}

func (adapter *localDelegatedAgentAdapter) buildChildConversation(request delegation.TaskRequest, identity localDelegatedIdentity) (*ConversationFile, error) {
	var parent *ConversationFile
	parentConversationID := strings.TrimSpace(request.ConversationID)
	if adapter.store != nil && parentConversationID != "" {
		loaded, err := adapter.store.LoadConversation(parentConversationID)
		if err != nil {
			return nil, fmt.Errorf("load delegated parent conversation: %w", err)
		}
		parent = loaded
	}
	if parent == nil {
		parent = &ConversationFile{Entries: make([]HistoryEntry, 0), NextTurnSeq: 1, NextEntrySeq: 1}
	}
	child := cloneConversationFile(parent)
	child.ConversationID = identity.conversationID
	child.ParentConversationID = parentConversationID
	child.RootConversationID = firstNonEmpty(strings.TrimSpace(request.RootConversationID), parent.RootConversationID, parentConversationID, identity.conversationID)
	child.ParentToolCallID = strings.TrimSpace(request.ParentToolCall)
	child.SubagentTypeName = firstNonEmpty(strings.TrimSpace(request.SubagentType), "generalPurpose")
	child.CurrentRequestID = identity.requestID
	child.LatestRequestPrefix = nil
	child.LastProviderCall = nil
	return child, nil
}

type localProviderPass struct {
	text        string
	invocations []runtimecore.ToolInvocation
}

func (adapter *localDelegatedAgentAdapter) runProviderPass(ctx context.Context, request delegation.TaskRequest, identity localDelegatedIdentity, conversation *ConversationFile, compiled CompiledConversation, messages []modeladapter.Message, providerPass int) (localProviderPass, error) {
	modelCallID := fmt.Sprintf("%s-model-%d", identity.taskID, providerPass)
	maxTokens := providerDefaultMaxOutputTokens
	requestKnobs := map[string]any{}
	if adapter.resolveBudget != nil {
		maxTokens, requestKnobs = adapter.resolveBudget(request.ModelID, request.ModelName, conversation, compiled)
	}
	if requestKnobs == nil {
		requestKnobs = make(map[string]any)
	}
	requestKnobs["delegated_task_id"] = identity.taskID
	requestKnobs["delegated_provider_pass"] = providerPass
	providerRequest := ProviderRequest{
		RequestID:          identity.requestID,
		ConversationID:     identity.conversationID,
		RunID:              identity.runID,
		ModelCallID:        modelCallID,
		ModelID:            strings.TrimSpace(request.ModelID),
		Mode:               compiled.Mode,
		ThinkingEffort:     strings.TrimSpace(request.ThinkingEffort),
		MaxMode:            request.MaxMode,
		Messages:           cloneDelegatedMessages(messages),
		StableMessageCount: delegatedStableMessageCount(compiled.StableMessageCount, len(messages)),
		Tools:              append([]json.RawMessage(nil), compiled.Tools...),
		MaxTokens:          maxTokens,
		RequestKnobs:       requestKnobs,
		CompileSummary:     compiled.CompileSummary + fmt.Sprintf(" delegated_pass=%d", providerPass),
		Observer:           adapter.recorder,
		ArtifactPaths:      &modeladapter.LLMArtifactPaths{},
	}
	var textBuilder strings.Builder
	invocations := make([]runtimecore.ToolInvocation, 0, 4)
	err := adapter.provider.StartStream(ctx, providerRequest, func(event modeladapter.ModelEvent) error {
		switch event.Kind {
		case modeladapter.ModelEventKindTextDelta:
			textBuilder.WriteString(event.Text)
		case modeladapter.ModelEventKindToolLikeCompleted:
			if event.ToolInvocation != nil {
				invocation := *event.ToolInvocation
				invocation.ArgsJSON = append([]byte(nil), event.ToolInvocation.ArgsJSON...)
				invocations = append(invocations, invocation)
			}
		case modeladapter.ModelEventKindProviderError:
			if event.Err != nil {
				return event.Err
			}
			return fmt.Errorf("delegated provider error")
		}
		return nil
	})
	pass := localProviderPass{text: textBuilder.String(), invocations: invocations}
	if err != nil {
		return pass, err
	}
	return pass, nil
}

func (adapter *localDelegatedAgentAdapter) executeTool(ctx context.Context, request delegation.TaskRequest, conversation *ConversationFile, invocation runtimecore.ToolInvocation) string {
	originalToolName := strings.TrimSpace(invocation.ToolName)
	routedInvocation, routedMCP, err := rewriteConversationMCPToolInvocation(conversation, invocation)
	if err != nil {
		return fmt.Sprintf("tool routing failed: %s", err.Error())
	}
	permissionToolName := originalToolName
	if routedMCP {
		permissionToolName = "CallMcpTool"
	}
	if !delegatedToolAllowed(request.ToolPermission, permissionToolName) {
		return fmt.Sprintf("tool permission denied: %s", strings.TrimSpace(invocation.ToolName))
	}
	if adapter.toolExecutor == nil {
		return fmt.Sprintf("tool executor unavailable: %s", strings.TrimSpace(invocation.ToolName))
	}
	output, err := adapter.toolExecutor(ctx, request, routedInvocation)
	if err != nil {
		return fmt.Sprintf("tool execution failed: %s", err.Error())
	}
	return limitProjectedToolResultReplay(invocation.ToolName, strings.TrimSpace(output), strings.TrimSpace(output), false, false)
}

func filterDelegatedTools(tools []json.RawMessage, permissions map[string]bool, mcpToolNames map[string]struct{}) ([]json.RawMessage, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	filtered := make([]json.RawMessage, 0, len(tools))
	for _, raw := range tools {
		name, err := extractToolName(raw)
		if err != nil {
			return nil, err
		}
		permissionToolName := name
		if _, isMCPTool := mcpToolNames[name]; isMCPTool {
			permissionToolName = "CallMcpTool"
		}
		if !delegatedToolAllowed(permissions, permissionToolName) {
			continue
		}
		filtered = append(filtered, append(json.RawMessage(nil), raw...))
	}
	return filtered, nil
}

func delegatedToolAllowed(permissions map[string]bool, toolName string) bool {
	if len(permissions) == 0 {
		return true
	}
	allowed, configured := permissions[strings.TrimSpace(toolName)]
	return !configured || allowed
}

func buildDelegatedAssistantToolMessage(text string, invocations []runtimecore.ToolInvocation) modeladapter.Message {
	message := modeladapter.Message{Role: "assistant", Content: strings.TrimSpace(text)}
	message.ToolCalls = make([]modeladapter.ToolCallDescriptor, 0, len(invocations))
	for index, invocation := range invocations {
		callID := strings.TrimSpace(invocation.CallID)
		if callID == "" {
			callID = fmt.Sprintf("delegated-tool-%d", index+1)
		}
		message.ToolCalls = append(message.ToolCalls, modeladapter.ToolCallDescriptor{
			ID:   callID,
			Type: "function",
			Function: modeladapter.ToolCallFunctionShape{
				Name:      strings.TrimSpace(invocation.ToolName),
				Arguments: string(invocation.ArgsJSON),
			},
			OpenAIResponsesID:     strings.TrimSpace(invocation.ProviderItemID),
			OpenAIResponsesCallID: strings.TrimSpace(invocation.ProviderCallID),
			OpenAIResponsesStatus: strings.TrimSpace(invocation.ProviderStatus),
		})
	}
	return message
}

func cloneDelegatedMessages(source []modeladapter.Message) []modeladapter.Message {
	if len(source) == 0 {
		return nil
	}
	cloned := make([]modeladapter.Message, 0, len(source))
	for _, message := range source {
		cloned = append(cloned, cloneReplayModelMessage(message))
	}
	return cloned
}

func delegatedStableMessageCount(compiledStable int, messageCount int) int {
	if compiledStable < 0 {
		return 0
	}
	if compiledStable > messageCount {
		return messageCount
	}
	return compiledStable
}

func (service *Service) executeLocalDelegatedTool(ctx context.Context, request delegation.TaskRequest, invocation runtimecore.ToolInvocation) (string, error) {
	if service == nil || service.mcpRuntime == nil {
		return "", fmt.Errorf("local delegated tool runtime is unavailable")
	}
	preferredRuntimeScope := MCPRuntimeScope(request.WorkspaceHint)
	switch strings.TrimSpace(invocation.ToolName) {
	case "CallMcpTool":
		payload, err := runtimecore.DecodeMCPToolPayload(invocation.ArgsJSON)
		if err != nil {
			return "", err
		}
		serverID := firstNonEmpty(payload.Server, payload.ProviderIdentifier, runtimecore.InferMCPServerIdentifier(payload.Name))
		toolName := firstNonEmpty(payload.ToolName, runtimecore.InferMCPToolName(serverID, payload.Name))
		runtimeScope := service.mcpRuntime.ResolveScope(preferredRuntimeScope, serverID)
		result, err := service.mcpRuntime.CallTool(ctx, runtimeScope, serverID, toolName, payload.Arguments)
		return marshalDelegatedToolResult(result, err)
	case "ListMcpResources":
		var args struct {
			Server string `json:"server"`
		}
		if err := json.Unmarshal(invocation.ArgsJSON, &args); err != nil {
			return "", err
		}
		serverID := strings.TrimSpace(args.Server)
		runtimeScope := service.mcpRuntime.ResolveScope(preferredRuntimeScope, serverID)
		result, err := service.mcpRuntime.ListResources(ctx, runtimeScope, serverID)
		return marshalDelegatedToolResult(result, err)
	case "FetchMcpResource":
		var args struct {
			Server string `json:"server"`
			URI    string `json:"uri"`
		}
		if err := json.Unmarshal(invocation.ArgsJSON, &args); err != nil {
			return "", err
		}
		serverID := strings.TrimSpace(args.Server)
		runtimeScope := service.mcpRuntime.ResolveScope(preferredRuntimeScope, serverID)
		result, err := service.mcpRuntime.ReadResource(ctx, runtimeScope, serverID, strings.TrimSpace(args.URI))
		return marshalDelegatedToolResult(result, err)
	default:
		if service.cursorDelegation == nil || service.cursorDelegation.cursor == nil {
			return "", fmt.Errorf("tool %q requires the Cursor delegated exec bridge", strings.TrimSpace(invocation.ToolName))
		}
		result := service.cursorDelegation.cursor.ExecuteTool(ctx, request, invocation)
		if result.Error != nil {
			return result.Output, result.Error
		}
		return result.Output, nil
	}
}

func marshalDelegatedToolResult(value any, err error) (string, error) {
	if err != nil {
		return "", err
	}
	encoded, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return "", marshalErr
	}
	return string(encoded), nil
}

func localDelegationCheckpointChangedFiles(invocation runtimecore.ToolInvocation) []string {
	switch strings.TrimSpace(invocation.ToolName) {
	case "Write", "PatchEdit", "PatchEditLines", "PatchEditSpan", "Edit", "Delete", "GenerateImage":
	default:
		return nil
	}

	args, err := runtimecore.DecodeArgsMap(invocation.ArgsJSON)
	if err != nil {
		return nil
	}
	path := strings.TrimSpace(localDelegationCheckpointTargetPath(strings.TrimSpace(invocation.ToolName), args))
	if path == "" {
		return nil
	}
	return []string{path}
}

func localDelegationCheckpointTargetPath(toolName string, args map[string]any) string {
	switch strings.TrimSpace(toolName) {
	case "GenerateImage":
		return runtimecore.ReadStringArg(args, "file_path", "filePath", "path", "Path")
	default:
		return runtimecore.ReadStringArg(args, "path", "file_path", "filePath", "Path", "FilePath")
	}
}
