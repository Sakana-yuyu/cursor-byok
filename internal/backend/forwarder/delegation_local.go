package forwarder

import (
	"context"
	"cursor/internal/logger"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
)

const (
	defaultLocalDelegationMaxProviderPasses = 50
	localDelegationFirstEventTimeout        = 90 * time.Second
	delegatedOverflowRetryBudgetFactor      = 0.8
	localDelegationVisibleUpdateMinBytes    = 320
	localDelegationVisibleUpdateMaxBytes    = 1200
)

type LocalDelegatedToolExecutor func(context.Context, delegation.TaskRequest, runtimecore.ToolInvocation) (string, error)

type localDelegatedAgentAdapter struct {
	store                *ConversationFileStore
	usageStore           *UsageFileStore
	compiler             PromptCompiler
	provider             ProviderGateway
	recorder             modeladapter.LLMArtifactObserver
	debug                *debugRecorder
	broker               *StreamBroker
	resolveBudget        func(string, string, *ConversationFile, CompiledConversation) (int, map[string]any)
	toolExecutor         LocalDelegatedToolExecutor
	maxPasses            int
	sequence             atomic.Uint64
	resolveContextWindow func(string) uint32
	// learnContextWindow 在溢出重试时按失败发送量收敛渠道窗口并持久化，
	// 让后续任务的首次预算落在真实窗口附近（每任务幂等一次，nil 时跳过）。
	learnContextWindow func(ctx context.Context, modelID string, sentTokens int64) (before int, after int, ok bool)
}

func newLocalDelegatedAgentAdapter(service *Service) *localDelegatedAgentAdapter {
	if service == nil {
		return nil
	}
	return &localDelegatedAgentAdapter{
		store:                service.store,
		usageStore:           service.usageStore,
		compiler:             service.compiler,
		provider:             service.provider,
		recorder:             service.recorder,
		debug:                service.debug,
		broker:               service.broker,
		resolveBudget:        service.resolveProviderOutputBudget,
		toolExecutor:         service.executeLocalDelegatedTool,
		maxPasses:            defaultLocalDelegationMaxProviderPasses,
		resolveContextWindow: service.resolveContextWindowTokens,
		learnContextWindow:   service.learnContextWindowForDelegatedOverflow,
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
	compiled, err := adapter.compiler.Compile(conversation, mode, strings.TrimSpace(request.Prompt), strings.TrimSpace(request.ModelName), "", false)
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
	compiled.Tools, err = filterDelegatedTools(compiled.Tools, request.ToolPermission, mcpToolNames, request.ToolWhitelist)
	if err != nil {
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, 1, nil, nil, "delegated worker tool filtering failed", delegation.SanitizeSupervisorText(err.Error(), request.WorkspaceHint))
		return delegation.TaskResult{Error: err, Metadata: identity.metadata(0)}
	}
	historyMessages := cloneDelegatedMessages(compiled.Messages)
	if prompt := strings.TrimSpace(request.Prompt); prompt != "" {
		historyMessages = append(historyMessages, modeladapter.Message{Role: "user", Content: prompt})
	}

	toolCallCount := 0
	maxPasses := adapter.maxPasses
	if maxPasses <= 0 {
		maxPasses = defaultLocalDelegationMaxProviderPasses
	}
	overflowRetries := 0
	previousSentInputTokens := int64(0)
	// lastOutputText 跟踪最后一个成功 pass 的文本输出，pass 超限时作为部分结果返回，
	// 避免丢弃已完成的调查成果导致整个 Task 被标记为 ERROR（客户端显示 "Stopped with error"）。
	lastOutputText := ""
	for providerPass := 1; providerPass <= maxPasses; providerPass++ {
		if err := ctx.Err(); err != nil {
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCanceled, providerPass, nil, nil, "delegated worker canceled", "")
			return delegation.TaskResult{Error: err, ToolCallCount: toolCallCount, Metadata: identity.metadata(providerPass)}
		}
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusRunning, providerPass, nil, nil, "delegated worker is running", "")

		// 主动阈值压缩：溢出重试会降低比例，确保不会用同一窗口重复发送。
		windowRatio := delegatedOverflowWindowRatio(overflowRetries)
		passView, compactErr := compactDelegatedMessagesBeforePass(
			ctx,
			adapter,
			request,
			historyMessages,
			compiled.Tools,
			providerPass,
			windowRatio,
			previousSentInputTokens,
		)
		if compactErr != nil {
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, providerPass, nil, nil, "delegated worker context window invalid", delegation.SanitizeSupervisorText(compactErr.Error(), request.WorkspaceHint))
			return delegation.TaskResult{Error: compactErr, ToolCallCount: toolCallCount, Metadata: identity.metadata(providerPass)}
		}

		pass, err := adapter.runProviderPass(ctx, request, identity, conversation, compiled, passView, providerPass, overflowRetries, windowRatio)
		if err != nil {
			if delegatedContextOverflowError(err) && overflowRetries < delegatedCompactionRetryLimit {
				previousSentInputTokens = passView.InputTokens
				overflowRetries++
				// 本任务首次溢出：按失败发送量收敛渠道窗口并持久化（每任务幂等一次），
				// 让后续任务的首次预算就落在真实窗口附近，避免每个任务都靠溢出重试兜底。
				if overflowRetries == 1 && adapter.learnContextWindow != nil {
					adapter.learnContextWindow(ctx, strings.TrimSpace(request.ModelID), previousSentInputTokens)
				}
				logger.Infof("forwarder delegated context overflow retry task_id=%s provider_pass=%d retry=%d/%d window_ratio=%.3f", strings.TrimSpace(request.ID), providerPass, overflowRetries, delegatedCompactionRetryLimit, delegatedOverflowWindowRatio(overflowRetries))
				// providerPass-- 抵消 for post 语句的 providerPass++，使重试复用同一 provider_pass。
				// 重试轮会照常执行循环顶部的主动压缩与 checkpoint；不要在此循环内添加
				// providerPass <= 0 的防护逻辑，那会过早终止重试轮。
				providerPass--
				continue
			}
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, providerPass, nil, nil, "delegated provider failed", delegation.SanitizeSupervisorText(err.Error(), request.WorkspaceHint))
			return delegation.TaskResult{Error: err, Output: strings.TrimSpace(pass.text), ToolCallCount: toolCallCount, Metadata: identity.metadata(providerPass)}
		}
		overflowRetries = 0
		previousSentInputTokens = 0
		if len(pass.invocations) == 0 {
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCompleted, providerPass, nil, nil, "delegated worker completed", "")
			return delegation.TaskResult{Output: strings.TrimSpace(pass.text), ToolCallCount: toolCallCount, Metadata: identity.metadata(providerPass)}
		}
		lastOutputText = strings.TrimSpace(pass.text)

		normalizeDelegatedToolInvocationIDs(pass.invocations)
		historyMessages = append(historyMessages, buildDelegatedAssistantToolMessage(pass.text, pass.invocations))
		for _, invocation := range pass.invocations {
			toolCallCount++
			// SubagentProfile.MaxSteps 限步：超过时优雅停止，返回部分结果（与 pass 超限处理一致）。
			if request.MaxSteps > 0 && toolCallCount >= request.MaxSteps {
				delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCompleted, providerPass, nil, nil, "delegated worker reached step limit, returning partial results", fmt.Sprintf("step limit: %d", request.MaxSteps))
				logger.Infof("forwarder local delegated worker reached step limit task_id=%s max_steps=%d tool_calls=%d", strings.TrimSpace(request.ID), request.MaxSteps, toolCallCount)
				return delegation.TaskResult{
					Output:        lastOutputText,
					ToolCallCount: toolCallCount,
					Metadata:      identity.metadata(providerPass),
				}
			}
			toolSignature := delegation.NormalizeToolSignature(invocation.ToolName, invocation.ArgsJSON)
			changedFiles := localDelegationCheckpointChangedFiles(invocation)
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusRunning, providerPass, []string{toolSignature}, changedFiles, "delegated tool is running", "")
			resultText := adapter.executeTool(ctx, request, conversation, invocation)
			historyMessages = append(historyMessages, modeladapter.Message{
				Role:       "tool",
				Content:    resultText,
				ToolCallID: strings.TrimSpace(invocation.CallID),
				Name:       strings.TrimSpace(invocation.ToolName),
			})
		}
	}
	// pass 超限：保留部分结果优雅返回，而非直接 ERROR。
	// 上游 Cursor proto 无子代理 turn 上限也无 partial result 概念，这里作为 byok 扩展：
	// 返回不带 Error 的 TaskResult（scheduler 标记 TaskCompleted），让父代理拿到已完成的
	// 调查成果，Task 卡片显示成功而非 "Stopped with error"。detail 标注已达上限。
	delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusCompleted, maxPasses, nil, nil, "delegated worker reached pass limit, returning partial results", fmt.Sprintf("provider pass limit: %d", maxPasses))
	logger.Infof("forwarder local delegated worker reached pass limit task_id=%s max_passes=%d tool_calls=%d partial_output=%t", strings.TrimSpace(request.ID), maxPasses, toolCallCount, lastOutputText != "")
	return delegation.TaskResult{
		Output:        lastOutputText,
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
	// A local worker has an independent prompt. Parent usage may describe a full
	// canonical conversation and must not constrain this worker's final window.
	child.TokenDetailsUsedTokens = 0
	child.TokenDetailsMaxTokens = 0
	if adapter.resolveContextWindow != nil {
		child.TokenDetailsMaxTokens = adapter.resolveContextWindow(strings.TrimSpace(request.ModelID))
	}
	return child, nil
}

type localProviderPass struct {
	text        string
	invocations []runtimecore.ToolInvocation
}

type delegatedProviderPassView struct {
	HistoryMessages         []modeladapter.Message
	Messages                []modeladapter.Message
	Stats                   delegatedCompactionStats
	BudgetTokens            int64
	InputTokens             int64
	PreviousSentInputTokens int64
}

func (adapter *localDelegatedAgentAdapter) runProviderPass(ctx context.Context, request delegation.TaskRequest, identity localDelegatedIdentity, conversation *ConversationFile, compiled CompiledConversation, view delegatedProviderPassView, providerPass int, overflowRetryOrdinal int, windowRatio float64) (localProviderPass, error) {
	modelCallID := fmt.Sprintf("%s-model-%d", identity.taskID, providerPass)
	maxTokens := providerDefaultMaxOutputTokens
	requestKnobs := map[string]any{}
	finalCompiled := compiled
	finalCompiled.Messages = cloneDelegatedMessages(view.Messages)
	if adapter.resolveBudget != nil {
		maxTokens, requestKnobs = adapter.resolveBudget(request.ModelID, request.ModelName, conversation, finalCompiled)
	}
	if requestKnobs == nil {
		requestKnobs = make(map[string]any)
	}
	if err := validateProviderRequestContextBudget(conversation, finalCompiled, maxTokens); err != nil {
		return localProviderPass{}, err
	}
	requestKnobs["delegated_task_id"] = identity.taskID
	requestKnobs["delegated_provider_pass"] = providerPass
	requestKnobs["delegated_context_window_ratio"] = windowRatio
	window := int64(0)
	if conversation != nil {
		window = compactionContextWindowSize(conversation)
	}
	if window <= 0 && adapter.resolveContextWindow != nil {
		window = int64(adapter.resolveContextWindow(strings.TrimSpace(request.ModelID)))
	}
	finalCompiled.StableMessageCount = delegatedStableMessageCount(compiled.StableMessageCount, compiled.Messages, view.Messages)
	beforeCompiled := compiled
	beforeCompiled.Messages = cloneDelegatedMessages(view.HistoryMessages)
	projectionDiagnostics := contextProjectionRequestDiagnostics(
		"worker",
		conversation,
		nil,
		false,
		"",
		beforeCompiled,
		finalCompiled,
		window,
		0,
		maxTokens,
		overflowRetryOrdinal,
		windowRatio,
	)
	projectionDiagnostics["mode"] = "window"
	projectionDiagnostics["window_budget_tokens"] = view.BudgetTokens
	projectionDiagnostics["previous_sent_input_tokens"] = view.PreviousSentInputTokens
	projectionDiagnostics["snipped_tool_results"] = view.Stats.SnipCount
	projectionDiagnostics["dropped_groups"] = view.Stats.DroppedCount
	projectionDiagnostics["before_group_count"] = view.Stats.BeforeGroupCount
	projectionDiagnostics["after_group_count"] = view.Stats.AfterGroupCount
	requestKnobs["context_projection"] = projectionDiagnostics
	if adapter.debug != nil {
		adapter.debug.LogRuntime(ctx, identity.requestID, identity.conversationID, "context_projection_applied", projectionDiagnostics)
	}
	providerRequest := ProviderRequest{
		RequestID:          identity.requestID,
		ConversationID:     identity.conversationID,
		RunID:              identity.runID,
		ModelCallID:        modelCallID,
		ModelID:            strings.TrimSpace(request.ModelID),
		ModelName:          strings.TrimSpace(request.ModelName),
		Role:               "worker",
		ParentModel:        strings.TrimSpace(request.ModelName),
		ModelGroupID:       strings.TrimSpace(request.ModelGroupID),
		TaskID:             strings.TrimSpace(request.ID),
		ExecutionMode:      strings.TrimSpace(request.ExecutionMode),
		Mode:               compiled.Mode,
		ThinkingEffort:     strings.TrimSpace(request.ThinkingEffort),
		MaxMode:            request.MaxMode,
		Messages:           cloneDelegatedMessages(view.Messages),
		StableMessageCount: finalCompiled.StableMessageCount,
		Tools:              append([]json.RawMessage(nil), compiled.Tools...),
		MaxTokens:          maxTokens,
		RequestKnobs:       requestKnobs,
		CompileSummary:     compiled.CompileSummary + fmt.Sprintf(" delegated_pass=%d", providerPass),
		Observer:           adapter.recorder,
		ArtifactPaths:      &modeladapter.LLMArtifactPaths{},
	}
	var textBuilder strings.Builder
	var visibleTextBuilder strings.Builder
	flushVisibleText := func(force bool) {
		if visibleTextBuilder.Len() == 0 || (!force && visibleTextBuilder.Len() < localDelegationVisibleUpdateMinBytes) {
			return
		}
		text := localDelegationVisibleProgress(visibleTextBuilder.String(), request.WorkspaceHint)
		visibleTextBuilder.Reset()
		if text != "" {
			delegation.PublishWorkerVisibleUpdate(ctx, text)
		}
	}
	invocations := make([]runtimecore.ToolInvocation, 0, 4)
	// reasoning 累加器：与父代理 actor.go 的 ProviderAccumulatedReasoning 等字段对应。
	// 子代理没有 ActiveStream，用局部变量累积 thinking 文本与签名/载体，在
	// ToolLikeCompleted 时复制到 invocation 供多轮 replay（buildDelegatedAssistantToolMessage 消费）。
	var reasoningBuilder strings.Builder
	var reasoningSignature, reasoningSignatureSource, reasoningItemID, reasoningStatus string
	var reasoningSummary json.RawMessage
	usage := turnUsageSnapshot{
		Role:          "worker",
		ParentModel:   strings.TrimSpace(request.ModelName),
		LogicalModel:  strings.TrimSpace(request.ModelName),
		ModelGroupID:  strings.TrimSpace(request.ModelGroupID),
		TaskID:        strings.TrimSpace(request.ID),
		ExecutionMode: strings.TrimSpace(request.ExecutionMode),
	}
	firstEventCtx, cancelFirstEvent := context.WithCancel(ctx)
	firstEventTimer := time.AfterFunc(localDelegationFirstEventTimeout, cancelFirstEvent)
	defer func() {
		firstEventTimer.Stop()
		cancelFirstEvent()
	}()
	var firstEvent atomic.Bool
	logger.Infof(
		"forwarder local delegated provider starting task_id=%s model_id=%s provider_pass=%d",
		strings.TrimSpace(identity.taskID),
		strings.TrimSpace(request.ModelID),
		providerPass,
	)
	err := adapter.provider.StartStream(firstEventCtx, providerRequest, func(event modeladapter.ModelEvent) error {
		if firstEvent.CompareAndSwap(false, true) {
			firstEventTimer.Stop()
		}
		switch event.Kind {
		case modeladapter.ModelEventKindTextDelta:
			// 与父代理 actor.go:654-660 对齐：累积文本并转发到子代理 composer。
			delegation.MarkWorkerProgress(ctx)
			textBuilder.WriteString(event.Text)
			visibleTextBuilder.WriteString(event.Text)
			// 只通过 task_tool_call_delta（关联到 toolCallID）转发到子代理 composer，
			// 不发布裸 textDelta 到父级流（否则会混入父代理主对话流，且客户端可能
			// 把父级流的 request_id 显示在子代理卡片区域）。
			if adapter.broker != nil {
				requestID := strings.TrimSpace(request.ParentRequest)
				toolCallID := strings.TrimSpace(request.ParentToolCall)
				if requestID != "" && toolCallID != "" {
					_ = adapter.broker.Publish(requestID, StreamEvent{
						Message: buildTaskToolCallDeltaMessage(toolCallID, modelCallID, buildTextDeltaInteraction(event.Text)),
					})
				}
			}
			flushVisibleText(false)
		case modeladapter.ModelEventKindThinkingDelta:
			// 与父代理 actor.go:661-679 对齐：累积 reasoning 文本并转发到子代理 composer。
			delegation.MarkWorkerProgress(ctx)
			reasoningBuilder.WriteString(event.Text)
			// 只通过 task_tool_call_delta（关联到 toolCallID）转发到子代理 composer，
			// 不发布裸 thinkingDelta 到父级流（避免混入父代理主对话流）。
			if adapter.broker != nil {
				requestID := strings.TrimSpace(request.ParentRequest)
				toolCallID := strings.TrimSpace(request.ParentToolCall)
				if requestID != "" && toolCallID != "" {
					_ = adapter.broker.Publish(requestID, StreamEvent{
						Message: buildTaskToolCallDeltaMessage(toolCallID, modelCallID, buildThinkingDeltaInteraction(event.Text, event.ThinkingStyle)),
					})
				}
			}
		case modeladapter.ModelEventKindThinkingCompleted:
			// 与父代理 actor.go:680-716 对齐：捕获签名/载体供 replay。
			if strings.TrimSpace(event.ThinkingSignature) != "" {
				reasoningSignature = strings.TrimSpace(event.ThinkingSignature)
				reasoningSignatureSource = strings.TrimSpace(event.ThinkingSignatureSource)
				reasoningItemID = strings.TrimSpace(event.ProviderItemID)
				reasoningStatus = strings.TrimSpace(event.ProviderStatus)
				reasoningSummary = append(json.RawMessage(nil), event.ProviderSummary...)
			}
		case modeladapter.ModelEventKindToolLikeCompleted:
			delegation.MarkWorkerProgress(ctx)
			if event.ToolInvocation != nil {
				invocation := *event.ToolInvocation
				invocation.ArgsJSON = append([]byte(nil), event.ToolInvocation.ArgsJSON...)
				// 与父代理 actor.go:806-811 对齐：把累积的 reasoning 挂到 invocation 上，
				// 供 buildDelegatedAssistantToolMessage 构建 replay 消息时携带。
				invocation.ReasoningContent = reasoningBuilder.String()
				invocation.ReasoningSignature = reasoningSignature
				invocation.ReasoningSignatureSource = reasoningSignatureSource
				invocation.ReasoningProviderItemID = reasoningItemID
				invocation.ReasoningProviderStatus = reasoningStatus
				invocation.ReasoningProviderSummary = append(json.RawMessage(nil), reasoningSummary...)
				invocation.ModelCallID = modelCallID
				invocations = append(invocations, invocation)
				// 工具调用边界清空 reasoning 累加器（与父代理在 tool 调用时重置一致）。
				reasoningBuilder.Reset()
				reasoningSignature = ""
				reasoningSignatureSource = ""
				reasoningItemID = ""
				reasoningStatus = ""
				reasoningSummary = nil
			}
		case modeladapter.ModelEventKindProviderError:
			if event.Err != nil {
				return event.Err
			}
			return fmt.Errorf("delegated provider error")
		case modeladapter.ModelEventKindTurnFinished:
			flushVisibleText(true)
			// pass 结束时清空 reasoning 累加器（与父代理 actor.go:882 重置一致）。
			reasoningBuilder.Reset()
			reasoningSignature = ""
			reasoningSignatureSource = ""
			reasoningItemID = ""
			reasoningStatus = ""
			reasoningSummary = nil
			usage.Provider = event.Provider
			usage.Model = event.Model
			usage.ProviderModel = event.Model
			usage.BaseURL = event.BaseURL
			usage.GroupName = event.GroupName
			usage.InputTokens = event.InputTokens
			usage.OutputTokens = event.OutputTokens
			usage.CacheReadTokens = event.CacheReadTokens
			usage.CacheWriteTokens = event.CacheWriteTokens
			usage.UsagePresent = event.UsagePresent
			usage.CacheReadPresent = event.CacheReadPresent
			usage.CacheWritePresent = event.CacheWritePresent
		}
		return nil
	})
	if err != nil {
		usage.ErrorCode = "provider_error"
	}
	if usage.ProviderModel == "" {
		usage.ProviderModel = usage.Model
	}
	errorText := ""
	if err != nil {
		errorText = err.Error()
	}
	if recordErr := upsertStandaloneUsageSnapshot(adapter.usageStore, identity.requestID, modelCallID, map[bool]string{true: "completed", false: "provider_error"}[err == nil], usage, errorText); recordErr != nil {
		logger.Errorf("forwarder local delegated usage persistence failed task_id=%s err=%v", strings.TrimSpace(identity.taskID), recordErr)
	}
	pass := localProviderPass{text: textBuilder.String(), invocations: invocations}
	if err != nil {
		if !firstEvent.Load() && firstEventCtx.Err() != nil && ctx.Err() == nil {
			err = fmt.Errorf("delegated provider produced no event within %s: %w", localDelegationFirstEventTimeout, firstEventCtx.Err())
		}
		logger.Errorf(
			"forwarder local delegated provider failed task_id=%s model_id=%s provider_pass=%d first_event=%t err=%v",
			strings.TrimSpace(identity.taskID),
			strings.TrimSpace(request.ModelID),
			providerPass,
			firstEvent.Load(),
			err,
		)
		return pass, err
	}
	logger.Infof(
		"forwarder local delegated provider completed task_id=%s model_id=%s provider_pass=%d first_event=%t tool_calls=%d",
		strings.TrimSpace(identity.taskID),
		strings.TrimSpace(request.ModelID),
		providerPass,
		firstEvent.Load(),
		len(invocations),
	)
	return pass, nil
}

func localDelegationVisibleProgress(text, workspaceHint string) string {
	text = delegation.SanitizeSupervisorText(strings.TrimSpace(text), workspaceHint)
	if text == "" {
		return ""
	}
	if len(text) > localDelegationVisibleUpdateMaxBytes {
		text = strings.TrimSpace(text[:localDelegationVisibleUpdateMaxBytes]) + "..."
	}
	return text
}

func (adapter *localDelegatedAgentAdapter) executeTool(ctx context.Context, request delegation.TaskRequest, conversation *ConversationFile, invocation runtimecore.ToolInvocation) string {
	originalToolName := strings.TrimSpace(invocation.ToolName)
	// 委派 worker 不应嵌套调用 Task：该工具会经 cursor bridge 转发给 Cursor 客户端
	// 创建 subagent bubble，本地委派场景下客户端无法创建，返回 bubble 超时错误并最终
	// 触发 user_stopped_generation 取消。防御性拦截，与 filterDelegatedTools 保持一致。
	if originalToolName == "Task" {
		return "工具不可用：委派 worker 不能嵌套调用 Task 工具。请直接完成当前任务或在结果中说明无法执行子代理委派。"
	}
	// Shell 系列工具依赖 Cursor 终端交互，委派 worker 经 cursor bridge 转发后
	// 客户端只回 start 事件不回 stdout/exit，worker 会永久卡死。防御性拦截。
	if delegatedToolNeedsCursorInteraction(originalToolName) {
		return "工具不可用：委派 worker 不能执行 Shell/终端工具。请使用只读工具（Read/Grep/Glob/Ls）或 MCP 工具完成当前任务。"
	}
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

func filterDelegatedTools(tools []json.RawMessage, permissions map[string]bool, mcpToolNames map[string]struct{}, toolWhitelist []string) ([]json.RawMessage, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	filtered := make([]json.RawMessage, 0, len(tools))
	for _, raw := range tools {
		name, err := extractToolName(raw)
		if err != nil {
			return nil, err
		}
		trimmedName := strings.TrimSpace(name)
		// SubagentProfile.ToolWhitelist 白名单强制：非空时只保留白名单中的工具
		//（白名单与 Task/Shell 拦截是叠加关系，两项都不满足即过滤）。
		if len(toolWhitelist) > 0 {
			allowed := false
			for _, whitelisted := range toolWhitelist {
				if trimmedName == strings.TrimSpace(whitelisted) {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		// Task 工具是主 agent 用来创建子代理/委派的入口。委派 worker 自身已经是
		// 子代理，不能再嵌套调用 Task：若允许，worker 会把 Task 工具通过 cursor
		// bridge 转发给 Cursor 客户端创建 subagent bubble，而客户端无法为本地委派
		// worker 创建 bubble，会返回 "Timeout waiting for bubble creation"，反复失败
		// 后 Cursor 判定任务异常并触发 user_stopped_generation 取消整个委派。
		if trimmedName == "Task" {
			continue
		}
		// Shell 系列工具依赖 Cursor 客户端的终端交互（bubble/stream）。委派 worker
		// 通过 cursor bridge 转发 Shell 时，客户端只回 start 事件、不回 stdout/exit，
		// worker 会永久卡在工具执行上，主请求的 Task 在 Cursor 中显示为 stopped。
		// 委派 worker 只能使用纯查询/只读工具和 MCP 工具。
		if delegatedToolNeedsCursorInteraction(trimmedName) {
			continue
		}
		permissionToolName := trimmedName
		if _, isMCPTool := mcpToolNames[trimmedName]; isMCPTool {
			permissionToolName = "CallMcpTool"
		}
		if !delegatedToolAllowed(permissions, permissionToolName) {
			continue
		}
		filtered = append(filtered, append(json.RawMessage(nil), raw...))
	}
	return filtered, nil
}

// delegatedToolNeedsCursorInteraction 判断工具是否需要 Cursor 客户端的交互执行
// （终端、流式输出、编辑确认等）。委派 worker 在 byok 内运行，这些工具经 cursor
// bridge 转发后无法可靠闭环，会卡住 worker。
func delegatedToolNeedsCursorInteraction(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "Shell", "AwaitShell", "WriteShellStdin", "ForceBackgroundShell", "ForceBackgroundSubagent":
		return true
	default:
		return false
	}
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
	if len(invocations) > 0 {
		// The replay message owns the reasoning carrier for the following tool
		// batch. Do not discard it while windowing delegated worker history.
		message.ReasoningContent = strings.TrimSpace(invocations[0].ReasoningContent)
		message.ReasoningSignature = strings.TrimSpace(invocations[0].ReasoningSignature)
		message.ReasoningSignatureSource = strings.TrimSpace(invocations[0].ReasoningSignatureSource)
		message.OpenAIResponsesReasoningID = strings.TrimSpace(invocations[0].ReasoningProviderItemID)
		message.OpenAIResponsesReasoningStatus = strings.TrimSpace(invocations[0].ReasoningProviderStatus)
		message.OpenAIResponsesReasoningSummary = append(json.RawMessage(nil), invocations[0].ReasoningProviderSummary...)
	}
	message.ToolCalls = make([]modeladapter.ToolCallDescriptor, 0, len(invocations))
	for _, invocation := range invocations {
		callID := strings.TrimSpace(invocation.CallID)
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

func normalizeDelegatedToolInvocationIDs(invocations []runtimecore.ToolInvocation) {
	used := make(map[string]struct{}, len(invocations))
	for index := range invocations {
		callID := strings.TrimSpace(invocations[index].CallID)
		if callID == "" {
			callID = fmt.Sprintf("delegated-tool-%d", index+1)
		}
		if _, exists := used[callID]; exists {
			callID = fmt.Sprintf("%s-%d", callID, index+1)
		}
		used[callID] = struct{}{}
		invocations[index].CallID = callID
	}
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

func delegatedStableMessageCount(compiledStable int, compiledMessages []modeladapter.Message, messages []modeladapter.Message) int {
	if compiledStable <= 0 || len(compiledMessages) == 0 || len(messages) == 0 {
		return 0
	}
	// CompiledConversation.StableMessageCount is a replay count. It does not
	// include the compiler's system prompt, so compare non-system messages on
	// both sides and stop at the first window gap.
	stableReplay := make([]modeladapter.Message, 0, compiledStable)
	for _, message := range compiledMessages {
		if strings.TrimSpace(message.Role) == "system" {
			continue
		}
		if len(stableReplay) >= compiledStable {
			break
		}
		stableReplay = append(stableReplay, message)
	}
	matched := 0
	for _, message := range messages {
		if strings.TrimSpace(message.Role) == "system" {
			continue
		}
		if matched >= len(stableReplay) || !reflect.DeepEqual(stableReplay[matched], message) {
			break
		}
		matched++
	}
	return matched
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
	case "Read", "Ls", "Glob", "Grep", "ReadLints":
		// 纯查询/只读工具在 byok 内本地执行，不派发给 Cursor 客户端。
		// 委派 worker 的工具经 cursor bridge 转发依赖客户端应答；委派期间的
		// 客户端 bubble 结束（或进入等待态）后客户端不再应答这些 exec，worker
		// 会永久卡在工具执行上，委派永不收尾。本地执行可彻底消除该依赖。
		return service.executeLocalQueryTool(ctx, request, invocation)
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

const (
	localQueryReadMaxBytes     = 64 * 1024
	localQueryGrepMaxBytes     = 512 * 1024
	localQueryGrepMaxMatches   = 200
	localQueryGrepMaxOutputKiB = 32
	// localQueryGrepMaxVisited 限制单次 Grep 遍历的文件数，防止超大仓库
	// 造成持续的 CPU/IO 消耗（匹配数上限之外的第二道护栏）。
	localQueryGrepMaxVisited = 20000
)

// executeLocalQueryTool 在 byok 进程内本地执行委派 worker 的只读查询工具，
// 返回给模型的文本结果。支持 Read/Ls/Glob/Grep/ReadLints。
//
// 安全边界：所有路径都约束在 workspace 内（constrainWorkspacePath）。旧的
// cursor bridge 路径依赖 Cursor 客户端的权限层；本地执行必须自己约束，否则
// worker（外部模型）可以用 .. 或绝对路径读取 byok 进程可读的任何文件。
func (service *Service) executeLocalQueryTool(ctx context.Context, request delegation.TaskRequest, invocation runtimecore.ToolInvocation) (string, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	toolName := strings.TrimSpace(invocation.ToolName)
	args, err := runtimecore.DecodeArgsMap(invocation.ArgsJSON)
	if err != nil {
		return "", fmt.Errorf("decode %s args: %w", toolName, err)
	}
	workspace := strings.TrimSpace(request.WorkspaceHint)
	if workspace == "" {
		workspace = "."
	}
	readArg := func(names ...string) string {
		for _, name := range names {
			if value := strings.TrimSpace(runtimecore.ReadStringArg(args, name)); value != "" {
				return value
			}
		}
		return ""
	}
	switch toolName {
	case "Read":
		target, err := constrainWorkspacePath(workspace, readArg("path", "Path", "file_path", "filePath", "file"))
		if err != nil {
			return "", err
		}
		content, err := os.ReadFile(target)
		if err != nil {
			return "", err
		}
		truncated := false
		if len(content) > localQueryReadMaxBytes {
			content = content[:localQueryReadMaxBytes]
			truncated = true
		}
		result := fmt.Sprintf("文件内容（%s）：\n%s", target, string(content))
		if truncated {
			result += "\n...[truncated]"
		}
		return result, nil
	case "Ls":
		target, err := constrainWorkspacePath(workspace, readArg("path", "Path", "dir", "directory"))
		if err != nil {
			return "", err
		}
		entries, err := os.ReadDir(target)
		if err != nil {
			return "", err
		}
		lines := make([]string, 0, len(entries))
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() {
				name += "/"
			}
			lines = append(lines, name)
		}
		if len(lines) == 0 {
			return "（目录为空）", nil
		}
		return strings.Join(lines, "\n"), nil
	case "Glob":
		pattern := readArg("pattern", "glob", "Path", "path")
		if pattern == "" {
			return "", fmt.Errorf("glob pattern is required")
		}
		pattern, err = constrainGlobPattern(workspace, pattern)
		if err != nil {
			return "", err
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return "", err
		}
		matches = constrainGlobMatches(workspace, matches)
		if len(matches) == 0 {
			return "（无匹配）", nil
		}
		return strings.Join(matches, "\n"), nil
	case "Grep":
		return localQueryGrep(ctx, args, workspace, readArg)
	case "ReadLints":
		return "（本地委派 worker 不提供 lint 诊断数据）", nil
	default:
		return "", fmt.Errorf("unsupported local query tool %q", toolName)
	}
}

// constrainWorkspacePath 把目标路径解析到 workspace 内；使用绝对路径或 ..
// 逃逸 workspace 的一律拒绝，防止委派 worker 经本地执行读取工作区外文件。
// 仅做词法约束不够：workspace 内的 symlink 可以指向工作区外（monorepo、
// pnpm/current 链接等），因此对已存在的路径执行 EvalSymlinks 后再校验真实
// 目标仍在 workspace 内。
func constrainWorkspacePath(workspace string, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "."
	}
	base := resolveWorkspaceBase(workspace)
	if !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	resolved := filepath.Clean(value)
	if err := ensureResolvedInsideWorkspace(base, resolved); err != nil {
		return "", err
	}
	// 路径存在时解析符号链接后重新校验（不存在时保留原路径，后续
	// os.ReadFile/os.ReadDir 会返回 not found）。
	if evaluated, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
		if err := ensureResolvedInsideWorkspace(base, evaluated); err != nil {
			return "", err
		}
		resolved = evaluated
	}
	return resolved, nil
}

// resolveWorkspaceBase 解析 workspace 根目录的真实路径：根目录本身可能是
// symlink（macOS /tmp、symlinked home 等），先用 EvalSymlinks 解析，确保
// 后续 Rel 校验在真实路径空间内进行，避免 symlinked 根导致所有路径误报
// "path escapes workspace"。
func resolveWorkspaceBase(workspace string) string {
	base, err := filepath.Abs(workspace)
	if err != nil {
		return workspace
	}
	if evaluated, evalErr := filepath.EvalSymlinks(base); evalErr == nil {
		base = evaluated
	}
	return base
}

// ensureResolvedInsideWorkspace 校验解析后的路径仍位于 workspace（base）内。
func ensureResolvedInsideWorkspace(base string, resolved string) error {
	rel, err := filepath.Rel(base, resolved)
	if err != nil {
		return fmt.Errorf("path outside workspace: %s", resolved)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes workspace: %s", resolved)
	}
	return nil
}

// constrainGlobPattern 把 glob pattern 解析到 workspace 内。先 Clean（消除
// 通配符前后的 .. 段，避免 */../../../etc 之类逃逸），再取通配符前的静态
// 前缀做工作区约束判断，最后返回清理后的完整 pattern。
func constrainGlobPattern(workspace string, pattern string) (string, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", fmt.Errorf("glob pattern is required")
	}
	base := resolveWorkspaceBase(workspace)
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(base, pattern)
	}
	cleaned := filepath.Clean(pattern)
	static := cleaned
	if idx := strings.IndexAny(static, "*?["); idx >= 0 {
		static = static[:idx]
	}
	if static == "" {
		static = "."
	}
	if _, err := constrainWorkspacePath(base, static); err != nil {
		return "", err
	}
	return cleaned, nil
}

// constrainGlobMatches 过滤 glob 匹配结果：解析符号链接后仍在 workspace 内
// 的才保留，防止 workspace 内 symlink 指向工作区外文件被枚举/读取。
func constrainGlobMatches(workspace string, matches []string) []string {
	if len(matches) == 0 {
		return matches
	}
	base := resolveWorkspaceBase(workspace)
	filtered := matches[:0]
	for _, match := range matches {
		resolved := filepath.Clean(match)
		if evaluated, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
			resolved = evaluated
		}
		if ensureResolvedInsideWorkspace(base, resolved) == nil {
			filtered = append(filtered, match)
		}
	}
	return filtered
}

// localQueryGrep 在 workspace 内递归执行 Grep：正则匹配文件内容，
// 输出 文件:行号:行内容（默认）或仅文件列表（files_with_matches）。
func localQueryGrep(ctx context.Context, args map[string]any, workspace string, readArg func(...string) string) (string, error) {
	pattern := readArg("pattern", "regex", "regexp")
	if pattern == "" {
		return "", fmt.Errorf("grep pattern is required")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid grep pattern: %w", err)
	}
	outputMode := strings.ToLower(strings.TrimSpace(readArg("output_mode", "outputMode", "mode")))
	includeGlob := readArg("glob", "include")
	var includeRe *regexp.Regexp
	if includeGlob != "" && includeGlob != "*" {
		includeRe, err = regexp.Compile(globToRegex(includeGlob))
		if err != nil {
			return "", fmt.Errorf("invalid grep glob: %w", err)
		}
	}
	target := strings.TrimSpace(readArg("path", "Path"))
	if target == "" {
		target = workspace
	}
	target, err = constrainWorkspacePath(workspace, target)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		// 单文件 grep。
		return grepFile(target, target, re, outputMode)
	}
	var builder strings.Builder
	matchCount := 0
	visitedFiles := 0
	walkErr := filepath.WalkDir(target, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if entry.IsDir() {
			if path != target && localQueryGrepSkipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		// 不跟随符号链接：workspace 内的 symlink 可能指向工作区外文件，
		// 直接跳过（文件与目录 symlink 都不读取）。
		if entry.Type()&os.ModeSymlink != 0 {
			if path != target {
				return nil
			}
		}
		// include glob 按相对根目录的斜杠路径匹配，避免绝对路径分隔符差异。
		if includeRe != nil {
			rel, relErr := filepath.Rel(target, path)
			if relErr != nil || !includeRe.MatchString(filepath.ToSlash(rel)) {
				return nil
			}
		}
		if matchCount >= localQueryGrepMaxMatches {
			return nil
		}
		visitedFiles++
		if visitedFiles > localQueryGrepMaxVisited {
			return fmt.Errorf("grep visited file limit reached (%d)", localQueryGrepMaxVisited)
		}
		lines, err := grepFile(path, target, re, outputMode)
		if err != nil {
			return nil
		}
		if lines != "" {
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(lines)
			matchCount++
		}
		return nil
	})
	if walkErr != nil {
		if ctx != nil && ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", walkErr
	}
	if builder.Len() == 0 {
		return "（无匹配）", nil
	}
	return truncateGrepOutput(builder.String()), nil
}

func grepFile(path string, root string, re *regexp.Regexp, outputMode string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > localQueryGrepMaxBytes {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	if len(data) > localQueryGrepMaxBytes {
		data = data[:localQueryGrepMaxBytes]
	}
	rel := path
	if rootRel, relErr := filepath.Rel(root, path); relErr == nil {
		rel = rootRel
	}
	var builder strings.Builder
	for lineIndex, line := range strings.Split(string(data), "\n") {
		if re.MatchString(line) {
			if outputMode == "files_with_matches" || outputMode == "files" {
				builder.WriteString(rel)
				builder.WriteString("\n")
				break
			}
			builder.WriteString(rel)
			builder.WriteString(":")
			builder.WriteString(fmt.Sprintf("%d:", lineIndex+1))
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}
	return strings.TrimRight(builder.String(), "\n"), nil
}

func localQueryGrepSkipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "dist", "build", "logs", "runtime", ".venv", "venv", "__pycache__", ".reasonix", ".superpowers":
		return true
	default:
		return false
	}
}

func globToRegex(pattern string) string {
	var builder strings.Builder
	builder.WriteString("(?i)^")
	parts := strings.Split(pattern, "/")
	skipSep := false
	for index, part := range parts {
		if part == "**" {
			// ** 段：跨目录递归。index>0 时先保留它前面的分隔符（避免
			// a/**/b 误匹配 ab）；非末段时把 ** 及后续分隔符合并为
			// (?:.*/)?（可匹配零目录深度），末段 ** 匹配任意剩余路径。
			if index > 0 {
				builder.WriteString("/")
			}
			if index < len(parts)-1 {
				builder.WriteString("(?:.*/)?")
				skipSep = true
			} else {
				builder.WriteString(".*")
			}
			continue
		}
		if index > 0 && !skipSep {
			builder.WriteString("/")
		}
		skipSep = false
		for i := 0; i < len(part); i++ {
			switch part[i] {
			case '*':
				builder.WriteString("[^/]*")
			case '?':
				builder.WriteString("[^/]")
			default:
				builder.WriteString(regexp.QuoteMeta(string(part[i])))
			}
		}
	}
	builder.WriteString("$")
	return builder.String()
}

func truncateGrepOutput(text string) string {
	const maxKiB = localQueryGrepMaxOutputKiB * 1024
	if len(text) <= maxKiB {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) > localQueryGrepMaxMatches {
		lines = lines[:localQueryGrepMaxMatches]
	}
	joined := strings.Join(lines, "\n")
	if len(joined) > maxKiB {
		joined = joined[:maxKiB]
	}
	return joined + "\n...[truncated]"
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

// compactDelegatedMessagesBeforePass 执行主动阈值压缩并打日志，返回压缩后的 messages。
func delegatedOverflowWindowRatio(retry int) float64 {
	ratio := 1.0
	for index := 0; index < retry; index++ {
		ratio *= delegatedOverflowRetryBudgetFactor
	}
	return ratio
}

// compactDelegatedMessagesBeforePass returns a structural window for this pass.
// Callers must abort rather than send a malformed tool/reasoning chain.
func compactDelegatedMessagesBeforePass(ctx context.Context, adapter *localDelegatedAgentAdapter, request delegation.TaskRequest, historyMessages []modeladapter.Message, tools []json.RawMessage, providerPass int, windowRatio float64, previousSentInputTokens int64) (delegatedProviderPassView, error) {
	view := delegatedProviderPassView{
		HistoryMessages:         cloneDelegatedMessages(historyMessages),
		PreviousSentInputTokens: previousSentInputTokens,
	}
	if adapter == nil {
		view.Messages = cloneDelegatedMessages(historyMessages)
		view.InputTokens = estimateCompiledPromptTokens(CompiledConversation{Messages: view.Messages, Tools: tools})
		return view, nil
	}
	window := int64(0)
	if adapter.resolveContextWindow != nil {
		window = int64(adapter.resolveContextWindow(strings.TrimSpace(request.ModelID)))
	}
	budget := delegatedContextBudgetForWindow(window)
	if budget > 0 && windowRatio > 0 && windowRatio < 1 {
		budget = int64(float64(budget) * windowRatio)
	}
	if previousSentInputTokens > 0 {
		retryInputBudget := int64(float64(previousSentInputTokens) * delegatedOverflowRetryBudgetFactor)
		if retryInputBudget >= previousSentInputTokens {
			retryInputBudget = previousSentInputTokens - 1
		}
		retryMessageBudget := retryInputBudget - estimateToolDescriptorsTokens(tools)
		if retryMessageBudget < 1 {
			retryMessageBudget = 1
		}
		if budget <= 0 || retryMessageBudget < budget {
			budget = retryMessageBudget
		}
	}
	if budget <= 0 {
		view.Messages = cloneDelegatedMessages(historyMessages)
		view.InputTokens = estimateCompiledPromptTokens(CompiledConversation{Messages: view.Messages, Tools: tools})
		return view, nil
	}
	stats := &delegatedCompactionStats{}
	var changed bool
	// 非溢出重试时先尝试 LLM 摘要压缩（保留上下文不丢失）；
	// 溢出重试时（previousSentInputTokens > 0）provider 已返回 context_too_large，
	// LLM 摘要同样会超限，直接走结构化裁剪。
	if previousSentInputTokens == 0 {
		out, summaryChanged, summaryErr := compactDelegatedMessagesWithSummary(ctx, adapter, request, historyMessages, budget, 0)
		if summaryErr == nil && (summaryChanged || estimateModelMessagesTokens(out) <= budget) {
			changed = summaryChanged
			stats.BeforeTokens = estimateModelMessagesTokens(historyMessages)
			stats.AfterTokens = estimateModelMessagesTokens(out)
			view.Messages = out
			view.Stats = *stats
			view.BudgetTokens = budget
			view.InputTokens = estimateCompiledPromptTokens(CompiledConversation{Messages: out, Tools: tools})
			if previousSentInputTokens > 0 && view.InputTokens >= previousSentInputTokens {
				return delegatedProviderPassView{}, fmt.Errorf("delegated context overflow cannot shrink the remaining structural window (previous=%d next=%d)", previousSentInputTokens, view.InputTokens)
			}
			if changed {
				logger.Infof("forwarder delegated context compacted (summary) task_id=%s provider_pass=%d ratio=%.3f budget=%d msgs=%d->%d tokens=%d->%d",
					strings.TrimSpace(request.ID), providerPass, windowRatio, budget, len(historyMessages), len(out), stats.BeforeTokens, stats.AfterTokens)
			}
			return view, nil
		}
		if summaryErr != nil {
			logger.Infof("forwarder delegated compaction summary failed task_id=%s err=%v, falling back to structural window", strings.TrimSpace(request.ID), summaryErr)
		}
	}
	// LLM 摘要失败或溢出重试时，降级到结构化裁剪
	out, structChanged, structErr := buildDelegatedMessageWindow(historyMessages, budget, stats)
	if structErr != nil {
		return delegatedProviderPassView{}, fmt.Errorf("build delegated context window: %w", structErr)
	}
	changed = structChanged
	view.Messages = out
	view.Stats = *stats
	view.BudgetTokens = budget
	view.InputTokens = estimateCompiledPromptTokens(CompiledConversation{Messages: out, Tools: tools})
	if previousSentInputTokens > 0 && view.InputTokens >= previousSentInputTokens {
		return delegatedProviderPassView{}, fmt.Errorf("delegated context overflow cannot shrink the remaining structural window (previous=%d next=%d)", previousSentInputTokens, view.InputTokens)
	}
	if changed {
		logger.Infof("forwarder delegated context compacted task_id=%s provider_pass=%d ratio=%.3f budget=%d snip=%d dropped=%d msgs=%d->%d tokens=%d->%d",
			strings.TrimSpace(request.ID), providerPass, windowRatio, budget, stats.SnipCount, stats.DroppedCount, len(historyMessages), len(out), stats.BeforeTokens, stats.AfterTokens)
	}
	return view, nil
}

func sameDelegatedMessages(a, b []modeladapter.Message) bool {
	return reflect.DeepEqual(a, b)
}
