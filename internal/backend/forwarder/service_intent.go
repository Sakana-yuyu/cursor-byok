// service_intent.go 承载 Bidi 上行 intent 解码、run/cancel/metadata 分发与消息提取辅助函数。
package forwarder

import (
	"context"
	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
	"cursor/internal/logger"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
)

func (service *Service) decodeInboundIntent(requestID string, message *agentv1.AgentClientMessage, clientKind string) (InboundIntent, error) {
	intent := InboundIntent{
		RequestID:     strings.TrimSpace(requestID),
		ClientMessage: message,
	}
	var err error
	switch strings.TrimSpace(clientKind) {
	case "run_request":
		runRequest := message.GetRunRequest()
		if runRequest == nil {
			return InboundIntent{}, fmt.Errorf("run_request payload is required")
		}
		conversationID := strings.TrimSpace(runRequest.GetConversationId())
		if conversationID == "" {
			return InboundIntent{}, fmt.Errorf("conversation_id is required in run_request")
		}
		intent.ConversationID = conversationID
		intent.ConversationState = runRequest.GetConversationState()
		intent.PreFetchedBlobs = runRequest.GetPreFetchedBlobs()
		intent.UserMessage = extractUserMessage(message)
		// Cursor 粘贴图片走 blob 协议：图片数据在 pre_fetched_blobs 里，selected_images 只带 blob_id。
		// 这里把 blob 数据填充进图片，否则后续 buildSelectedImageContentParts 会静默丢弃图片，
		// 图片进不了消息 ContentPart，图片路径占位也不会触发。
		hydrateSelectedImageBlobs(intent.UserMessage, buildPrefetchedBlobMap(runRequest.GetPreFetchedBlobs()))
		actionRequestContext := extractRequestContext(message)
		intent.RequestContext = extractEffectiveRunRequestContext(message)
		intent.MCPToolsProvided = runRequest.McpTools != nil || len(actionRequestContext.GetTools()) > 0
		if service.shouldIgnoreEmptyResumeRunRequest(requestID, runRequest, intent.UserMessage, actionRequestContext) {
			intent.Kind = "metadata"
			intent.StartsRun = false
			intent.HasExplicitMode = false
			intent.ModeSource = ModeSourceUnknown
			intent.IgnoredReason = "empty_resume_without_pending_continuation"
			return intent, nil
		}
		intent.Kind = "run"
		intent.StartsRun = true
		intent.Mode, intent.ModeSource, intent.HasExplicitMode, err = extractRunMode(message)
		if err != nil {
			return InboundIntent{}, err
		}
		intent.ModelID = extractRequestedModelID(message)
		intent.ThinkingEffort = extractRuntimeThinkingEffort(message)
		intent.CustomSystemPrompt = truncatePromptGuardText("run_request.custom_system_prompt", runRequest.GetCustomSystemPrompt(), promptGuardCustomSystemPromptChars)
		intent.MaxMode = extractRequestedMaxMode(message)
		intent.SubagentTypeName = strings.TrimSpace(runRequest.GetSubagentTypeName())
		intent.SelectedSubagentModels = cloneSelectedSubagentModels(runRequest.GetSelectedSubagentModels())
		intent.SelectedSubagentModelDetails = cloneSelectedSubagentModelDetails(runRequest.GetSelectedSubagentModelDetails())
		parsedOverrides := parseSubagentModelOverrides(runRequest.GetSubagentModelOverrides())
		intent.SubagentModelOverrides = parsedOverrides.Overrides
		service.debug.LogRuntime(context.Background(), intent.RequestID, intent.ConversationID, "subagent_model_overrides_parsed", map[string]any{
			"override_count": parsedOverrides.RawCount,
			"valid_count":    len(parsedOverrides.Overrides),
			"ignored_count":  len(parsedOverrides.Ignored),
			"overrides":      subagentModelOverrideSummaries(parsedOverrides.Overrides),
			"ignored":        parsedOverrides.Ignored,
		})
		if intent.ModelID == "" {
			intent.ModelID = "default"
		}
		intent.ModelName = service.resolveRequestedModelName(message, intent.ModelID)
	case "prewarm_request":
		prewarmRequest := message.GetPrewarmRequest()
		if prewarmRequest == nil {
			return InboundIntent{}, fmt.Errorf("prewarm_request payload is required")
		}
		conversationID := strings.TrimSpace(prewarmRequest.GetConversationId())
		if conversationID == "" {
			return InboundIntent{}, fmt.Errorf("conversation_id is required in prewarm_request")
		}
		intent.Kind = "run"
		intent.Prewarm = true
		intent.StartsRun = true
		intent.ConversationID = conversationID
		intent.SubagentTypeName = strings.TrimSpace(prewarmRequest.GetSubagentTypeName())
		intent.SelectedSubagentModels = cloneSelectedSubagentModels(prewarmRequest.GetSelectedSubagentModels())
		intent.SelectedSubagentModelDetails = cloneSelectedSubagentModelDetails(prewarmRequest.GetSelectedSubagentModelDetails())
		parsedOverrides := parseSubagentModelOverrides(prewarmRequest.GetSubagentModelOverrides())
		intent.SubagentModelOverrides = parsedOverrides.Overrides
		intent.RequestContext = extractEffectivePrewarmRequestContext(prewarmRequest)
		intent.MCPToolsProvided = prewarmRequest.McpTools != nil
		intent.ConversationState = prewarmRequest.GetConversationState()
		intent.PreFetchedBlobs = prewarmRequest.GetPreFetchedBlobs()
		intent.Mode, intent.ModeSource, intent.HasExplicitMode, err = extractPrewarmMode(prewarmRequest)
		if err != nil {
			return InboundIntent{}, err
		}
		intent.ModelID = firstNonEmpty(extractRequestedModelID(message), "default")
		intent.ThinkingEffort = extractRuntimeThinkingEffort(message)
		intent.MaxMode = extractRequestedMaxMode(message)
		intent.ModelName = service.resolveRequestedModelName(message, intent.ModelID)
	case "conversation_action":
		action := message.GetConversationAction()
		if action == nil {
			return InboundIntent{}, fmt.Errorf("conversation_action payload is required")
		}
		intent.UserMessage = extractConversationActionUserMessage(action)
		intent.RequestContext = extractConversationActionRequestContext(action)
		intent.StartsRun = conversationActionStartsRun(action)
		intent.ForceNewTurn = intent.StartsRun
		intent.Mode, intent.ModeSource, intent.HasExplicitMode, err = extractConversationActionMode(action)
		if err != nil {
			return InboundIntent{}, err
		}
		switch item := action.GetAction().(type) {
		case *agentv1.ConversationAction_CancelAction:
			intent.Kind = "cancel"
			intent.CancelReason = strings.TrimSpace(item.CancelAction.GetReason())
		default:
			if intent.StartsRun || intent.HasExplicitMode {
				if stream, ok := service.broker.Get(intent.RequestID); ok && stream != nil {
					stream.mu.Lock()
					intent.ConversationID = strings.TrimSpace(stream.ConversationID)
					intent.ModelID = strings.TrimSpace(stream.ModelID)
					intent.ModelName = strings.TrimSpace(stream.ModelName)
					intent.ThinkingEffort = strings.TrimSpace(stream.ThinkingEffort)
					intent.MaxMode = stream.MaxMode
					intent.SubagentModelOverrides = cloneSubagentModelOverrides(stream.SubagentModelOverrides)
					intent.SelectedSubagentModels = cloneSelectedSubagentModels(stream.SelectedSubagentModels)
					intent.SelectedSubagentModelDetails = cloneSelectedSubagentModelDetails(stream.SelectedSubagentModelDetails)
					if !intent.HasExplicitMode && stream.Mode != agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
						intent.Mode = stream.Mode
					}
					if stream.CheckpointConversation != nil {
						intent.SubagentTypeName = strings.TrimSpace(stream.CheckpointConversation.SubagentTypeName)
					}
					stream.mu.Unlock()
				}
				if strings.TrimSpace(intent.ConversationID) == "" {
					return InboundIntent{}, fmt.Errorf("conversation_action requires active request context")
				}
			}
			if intent.StartsRun {
				intent.Kind = "run"
				intent.StartsRun = true
				if intent.ModelID == "" {
					intent.ModelID = "default"
				}
			} else {
				intent.Kind = "metadata"
			}
		}
	case "exec_client_message":
		intent.Kind = "exec_result"
		intent.ExecClientMessage = message.GetExecClientMessage()
	case "exec_client_control_message":
		intent.Kind = "exec_control"
		intent.ExecClientControlMessage = message.GetExecClientControlMessage()
	case "interaction_response":
		intent.Kind = "interaction_result"
		intent.InteractionResponse = message.GetInteractionResponse()
	case "kv_client_message":
		intent.Kind = "kv_result"
		intent.KVClientMessage = message.GetKvClientMessage()
	case "client_heartbeat":
		intent.Kind = "metadata"
	default:
		return InboundIntent{}, fmt.Errorf("unsupported client message kind: %s", clientKind)
	}
	intent.ManualCompaction = resolveInboundManualCompaction(message, intent.UserMessage)
	return intent, nil
}

func (service *Service) shouldReuseActiveRun(intent InboundIntent) bool {
	if intent.ForceNewTurn {
		return false
	}
	if service == nil || service.broker == nil || strings.TrimSpace(intent.RequestID) == "" || strings.TrimSpace(intent.ConversationID) == "" {
		return false
	}
	stream, ok := service.broker.Get(intent.RequestID)
	if !ok || stream == nil {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if strings.TrimSpace(stream.ConversationID) != strings.TrimSpace(intent.ConversationID) {
		return false
	}
	if isTerminalStreamStatus(stream.Status) {
		return false
	}
	switch stream.Phase {
	case TurnPhaseCanceled, TurnPhaseCompleted, TurnPhaseFailed:
		return false
	}
	// 用户切换模型后发送新 run_request 时，不应复用旧 stream（旧模型）。
	if requestedModel := strings.TrimSpace(intent.ModelID); requestedModel != "" {
		if currentModel := strings.TrimSpace(stream.ModelID); currentModel != "" && requestedModel != currentModel {
			return false
		}
	}
	// RunSSE 重连会重复提交同一个 run_request；只要该 request 仍处于活动回合，
	// 就不能重新初始化 checkpoint、pending exec 或 provider pass。
	return stream.TurnSeq > 0 || stream.ProviderActive || len(stream.PendingExecs) > 0 || len(stream.PendingInteractions) > 0
}

// handleRunIntent 处理 run/prewarm 类 intent，负责建会话、写 turn 和拉起 provider。
func (service *Service) handleRunIntent(intent InboundIntent) error {
	if err := service.prepareStreamForForcedTurn(intent); err != nil {
		return err
	}
	if service.shouldReuseActiveRun(intent) {
		logger.Infof("forwarder duplicate run reused request_id=%s conversation_id=%s", strings.TrimSpace(intent.RequestID), strings.TrimSpace(intent.ConversationID))
		return nil
	}
	intent.UserMessage = normalizeUserMessageForStorage(intent.UserMessage)
	// 在写历史前，把磁盘扫描到的技能/MCP server 合并进 RequestContext。
	// 仅 turn 1 需要持久化静态上下文（与 normalizeRequestContextForStorageMode 的 turnSeq==1 语义一致），
	// 复用现有 request_context → projector → engine.go 的原生 user-message 注入链路。
	service.enrichRequestContextWithScannedAssets(&intent)
	conversation, effectiveMode, turnSeq, initialEntries, err := service.bootstrapRuntimeConversation(intent)
	if err != nil {
		return err
	}
	if intent.RequestContext != nil {
		if folder := normalizeAgentTranscriptsFolder(intent.RequestContext.GetEnv().GetAgentTranscriptsFolder()); folder != "" {
			conversation.AgentTranscriptsFolder = folder
		}
	}
	rewindDecision := service.decideRunRewind(intent, conversation)
	if rewindDecision.Evaluated && !rewindDecision.Apply {
		service.logRunRewindDecision(intent.RequestID, intent.ConversationID, "rewind_skipped", rewindDecision)
	}
	if rewindDecision.Apply {
		service.logRunRewindDecision(intent.RequestID, intent.ConversationID, "rewind_detected", rewindDecision)
		turnSeq = rewindDecision.TargetTurnSeq
		initialEntries, err = buildRunEntries(intent, effectiveMode, turnSeq)
		if err != nil {
			return err
		}
	}
	// 上一回合被新消息顶掉后仍在后台跑完的委派任务，在新回合开头补一条模型可见回放。
	// 落在回合最前面而不是运行中回合的中段，避免把 user 消息插进 tool_call/tool_result 之间。
	if replay := service.pendingBackgroundedDelegationEntries(conversation, intent.RequestID, turnSeq); len(replay) > 0 {
		initialEntries = append(replay, initialEntries...)
	}
	if service.store != nil {
		if rewindDecision.Apply {
			persisted, err := service.store.ReplaceEntries(
				intent.ConversationID,
				appendReplacementRunEntries(rewindDecision.PrefixEntries, initialEntries),
				func(item *ConversationFile) error {
					applyRunRewindMetadata(item, conversation, intent, turnSeq)
					return nil
				},
			)
			if err != nil {
				return err
			}
			if persisted != nil {
				conversation = persisted
			}
			service.logRunRewindDecision(intent.RequestID, intent.ConversationID, "rewind_applied", rewindDecision)
		} else {
			persisted, err := service.store.SaveConversationWithEntries(intent.ConversationID, conversation, initialEntries)
			if err != nil {
				return err
			}
			if persisted != nil {
				conversation = persisted
			}
		}
	} else if rewindDecision.Apply {
		service.applyRunRewindToConversation(conversation, rewindDecision, initialEntries, intent, turnSeq)
		service.logRunRewindDecision(intent.RequestID, intent.ConversationID, "rewind_applied", rewindDecision)
	} else if len(initialEntries) > 0 {
		appendEntriesInPlace(conversation, initialEntries)
		deriveConversationLoopState(conversation)
	}

	stream, err := service.broker.OpenStream(intent.RequestID, intent.ConversationID, turnSeq, intent.ModelID, intent.ModelName, effectiveMode, userMessageText(intent.UserMessage))
	if err != nil {
		return err
	}
	if stream == nil {
		return fmt.Errorf("open stream failed")
	}
	if err := service.replaceCheckpointConversation(stream, conversation); err != nil {
		return err
	}
	updateStreamRequestContextData(stream, intent.RequestContext)
	service.updateStreamMCPToolServers(stream, intent.RequestContext)
	clearPendingProviderCompletion(stream)
	stream.mu.Lock()
	stream.ThinkingEffort = strings.TrimSpace(intent.ThinkingEffort)
	stream.CustomSystemPrompt = strings.TrimSpace(intent.CustomSystemPrompt)
	stream.MaxMode = intent.MaxMode
	stream.SubagentModelOverrides = cloneSubagentModelOverrides(intent.SubagentModelOverrides)
	stream.SelectedSubagentModels = cloneSelectedSubagentModels(intent.SelectedSubagentModels)
	stream.SelectedSubagentModelDetails = cloneSelectedSubagentModelDetails(intent.SelectedSubagentModelDetails)
	stream.ManualCompaction = intent.ManualCompaction
	stream.PendingProviderAction = providerActionNone
	stream.PendingCompaction = nil
	stream.PendingExecs = make(map[string]runtimecore.PendingExec)
	stream.PendingInteractions = make(map[string]runtimecore.PendingInteraction)
	// Close any leftover completion signals from previous turn, then allocate a fresh map.
	for execID, ch := range stream.ExecCompletionSignals {
		if ch != nil {
			close(ch)
		}
		delete(stream.ExecCompletionSignals, execID)
	}
	if stream.ExecCompletionSignals == nil {
		stream.ExecCompletionSignals = make(map[string]chan struct{})
	}
	stream.RecentCompletedExecs = make(map[uint32]time.Time)
	stream.RecentCompletedInteractions = make(map[string]time.Time)
	stream.BackgroundShells = make(map[string]*BackgroundShellState)
	stream.BackgroundShellsByMessageID = make(map[uint32]string)
	stream.BackgroundShellsByExecID = make(map[string]string)
	stopAllStreamTimersLocked(stream)
	if intent.ForceNewTurn {
		if stream.TimerTokens == nil {
			stream.TimerTokens = make(map[string]uint64)
		}
		if stream.StreamTimers == nil {
			stream.StreamTimers = make(map[string]*time.Timer)
		}
	} else {
		stream.TimerTokens = make(map[string]uint64)
		stream.StreamTimers = make(map[string]*time.Timer)
		stream.CurrentProviderToken = 0
		stream.CurrentCompactionToken = 0
	}
	stream.ProviderAccumulatedText = nil
	stream.ProviderTextDeltaCount = 0
	stream.ProviderAccumulatedReasoning = nil
	stream.ProviderAccumulatedReasoningSignature = ""
	stream.ProviderAccumulatedReasoningSignatureSource = ""
	stream.ProviderAccumulatedReasoningItemID = ""
	stream.ProviderAccumulatedReasoningStatus = ""
	stream.ProviderAccumulatedReasoningSummary = nil
	stream.ProviderSyntheticThinkingStartedAt = time.Time{}
	stream.ProviderSyntheticThinkingPublished = false
	stream.ProviderThinkingDeltaCount = 0
	stream.ProviderThinkingCompletedCount = 0
	stream.ProviderThinkingSuppressedCount = 0
	stream.ProviderFinishReason = ""
	stream.ProviderUsage = turnUsageSnapshot{}
	stream.ProviderToolQuarantine = nil
	stream.ProviderPassToolNames = nil
	stream.ToolInvocationCount = 0
	stream.AutoMultitaskDelegationStarted = false
	stream.ProviderTurnStartedAt = time.Now().UTC()
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	service.setTurnPhase(stream, TurnPhaseIdle)
	service.debug.LogRuntime(context.Background(), intent.RequestID, intent.ConversationID, "stream_state_updated", map[string]any{
		"turn_seq":                             turnSeq,
		"model_id":                             strings.TrimSpace(intent.ModelID),
		"model_name":                           strings.TrimSpace(intent.ModelName),
		"thinking_effort":                      strings.TrimSpace(intent.ThinkingEffort),
		"mode":                                 effectiveMode.String(),
		"prewarm":                              intent.Prewarm,
		"subagent_type":                        strings.TrimSpace(intent.SubagentTypeName),
		"subagent_model_override_count":        len(intent.SubagentModelOverrides),
		"subagent_model_overrides":             subagentModelOverrideSummaries(intent.SubagentModelOverrides),
		"selected_subagent_model_count":        len(intent.SelectedSubagentModels),
		"selected_subagent_model_detail_count": len(intent.SelectedSubagentModelDetails),
		"latest_user_text":                     userMessageText(intent.UserMessage),
		"manual_compaction_requested":          intent.ManualCompaction.Requested,
	})
	if err := service.publishCheckpointForce(intent.RequestID, intent.ConversationID); err != nil {
		return err
	}
	if intent.Prewarm {
		return nil
	}
	return service.requestProviderAction(stream, providerActionStart)
}
func (service *Service) handleCancelIntent(intent InboundIntent) error {
	stream, ok := service.broker.Get(intent.RequestID)
	if !ok || stream == nil {
		// 目标 request 没有活动流：可能是仍在会话队列中等待的 run。
		// 只删除该排队项，不取消当前 owner，不写历史，不启动 provider。
		if handled, cancelErr := service.cancelQueuedRun(intent); handled {
			return cancelErr
		}
		return fmt.Errorf("request is not active: %s", intent.RequestID)
	}
	stream.mu.Lock()
	turnSeq := stream.TurnSeq
	conversationID := strings.TrimSpace(stream.ConversationID)
	phase := stream.Phase
	status := stream.Status
	providerActive := stream.ProviderActive
	pendingExecCount := len(stream.PendingExecs)
	activeDelegationCount := 0
	for _, pending := range stream.PendingExecs {
		if strings.TrimSpace(pending.ExecKind) == "delegation_aggregate" {
			activeDelegationCount++
		}
	}
	stream.mu.Unlock()
	// 「新消息顶掉当前 turn」只替换父回合，不是让所有工作停下来。上游对仍在前台
	// 运行的子代理先发 backgroundSubagentAction 转后台，再带着 resolutions 发 cancelAction。
	followUpCancel := isFollowUpCancelReason(intent.CancelReason)
	logger.Infof("forwarder cancel intent received request_id=%s conversation_id=%s reason=%q follow_up=%t phase=%s status=%s provider_active=%t pending_execs=%d active_delegations=%d",
		strings.TrimSpace(intent.RequestID), conversationID, strings.TrimSpace(intent.CancelReason), followUpCancel, phase, status, providerActive, pendingExecCount, activeDelegationCount)
	service.debug.LogRuntime(context.Background(), intent.RequestID, conversationID, "cancel_intent_received", map[string]any{
		"reason":                     strings.TrimSpace(intent.CancelReason),
		"follow_up_cancel":           followUpCancel,
		"phase":                      string(phase),
		"status":                     string(status),
		"provider_active":            providerActive,
		"pending_exec_count":         pendingExecCount,
		"active_delegation_count":    activeDelegationCount,
		"client_requested_cancel":    true,
		"cancel_replay_policy_value": cancelReplayPolicyForReason(intent.CancelReason),
	})
	service.clearProvider400Recovery(provider400RecoveryContentExists, intent.RequestID, turnSeq)
	service.clearProvider400Recovery(provider400RecoveryToolSchema, intent.RequestID, turnSeq)
	// 先切断当前 provider 请求，再做 history、工具 abort 和委派清理。
	// 断线取消不能因为后续持久化或广播变慢而继续消耗上游额度。
	forceCancelStreamProvider(stream)
	if followUpCancel {
		// 后台化必须早于下面的 pendingExecs 快照，否则快照里仍有这些 exec，
		// abort 循环照样会把它们打掉。
		service.backgroundFollowUpDelegations(context.Background(), stream)
	} else if service.multitaskDelegation != nil {
		service.multitaskDelegation.CancelStream(stream)
	}
	hasCheckpoint := checkpointConversationInitialized(stream)
	stream.mu.Lock()
	pendingExecs := make([]runtimecore.PendingExec, 0, len(stream.PendingExecs))
	for _, pending := range stream.PendingExecs {
		pendingExecs = append(pendingExecs, pending)
	}
	if stream.ProviderCancel != nil {
		stream.ProviderCancel()
		stream.ProviderCancel = nil
	}
	stream.ProviderActive = false
	stream.CurrentProviderToken++
	stream.CurrentCompactionToken++
	stream.PendingProviderAction = providerActionNone
	stream.PendingCompaction = nil
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if hasCheckpoint {
		cancelReason := firstNonEmpty(intent.CancelReason, "user aborted")
		cancelEntry := newMetadataEntry(stream.TurnSeq, intent.RequestID, "control", map[string]any{
			"status":        "canceled",
			"reason":        cancelReason,
			"replay_policy": cancelReplayPolicyForReason(cancelReason),
		})
		if strings.TrimSpace(intent.CancelTerminalStatus) == conversationStatusInterrupted {
			cancelEntry = newInterruptedControlEntry(stream.TurnSeq, intent.RequestID, cancelReason)
		}
		if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{cancelEntry}); err != nil {
			logger.Errorf("forwarder cancellation metadata persistence failed request_id=%s conversation_id=%s err=%v", stream.RequestID, stream.ConversationID, err)
			if memoryErr := service.appendCheckpointEntries(stream, []HistoryEntry{cancelEntry}); memoryErr != nil {
				return memoryErr
			}
		}
		// 等待用户输入的 interaction 必须在这里收口：下面清空 PendingInteractions 之后，
		// 看门狗到期也查不到它，那条 tool_call 就再没有任何路径能配上 tool_result。
		service.closeCanceledPendingInteractions(stream, firstNonEmpty(intent.CancelReason, "user aborted"))
	}
	for _, pending := range pendingExecs {
		// 已显式转入后台的执行（用户转后台的长跑 shell、客户端 backgroundSubagentAction
		// 转后台的子代理）不属于本回合前台工作，任何取消都不得 abort 它们。
		if isBackgroundedPendingExec(pending) {
			logger.Infof("forwarder cancel skipped backgrounded exec request_id=%s exec_id=%s exec_kind=%s",
				strings.TrimSpace(intent.RequestID), strings.TrimSpace(pending.ExecID), strings.TrimSpace(pending.ExecKind))
			continue
		}
		if strings.TrimSpace(pending.ExecKind) == "subagent" {
			service.updateNativeDelegationStatus(pending.ExecID, delegation.TaskCanceled, "Cursor 子代理已取消", "subagent canceled")
			service.interruptNativeChildConversation(pending.ExecID, followUpCancel)
		}
		if strings.TrimSpace(pending.ExecKind) == "delegation_aggregate" {
			continue
		}
		if err := service.broker.Publish(intent.RequestID, StreamEvent{
			Message: buildExecAbortMessage(pending),
		}); err != nil {
			logger.Errorf("forwarder cancel exec-abort publish failed request_id=%s exec_id=%s err=%v", strings.TrimSpace(intent.RequestID), strings.TrimSpace(pending.ExecID), err)
		}
	}
	// 清除所有 pending exec，防止 stream 永远卡在 running
	cleanupAllPendingExecs(stream)
	clearPendingProviderCompletion(stream)
	stream.mu.Lock()
	stream.PendingExecs = make(map[string]runtimecore.PendingExec)
	stream.PendingInteractions = make(map[string]runtimecore.PendingInteraction)
	// Close any leftover completion signals from previous turn, then allocate a fresh map.
	for execID, ch := range stream.ExecCompletionSignals {
		if ch != nil {
			close(ch)
		}
		delete(stream.ExecCompletionSignals, execID)
	}
	if stream.ExecCompletionSignals == nil {
		stream.ExecCompletionSignals = make(map[string]chan struct{})
	}
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if hasCheckpoint {
		if err := service.publishCheckpointWithTerminalAction(
			stream.RequestID,
			stream.ConversationID,
			checkpointCancellationAction(firstNonEmpty(intent.CancelReason, "[canceled] User aborted request")),
		); err != nil {
			return err
		}
		if err := service.flushCheckpointPersistSync(stream, conversationID); err != nil {
			logger.Errorf("forwarder cancellation checkpoint flush failed request_id=%s conversation_id=%s err=%v", stream.RequestID, conversationID, err)
		}
		return nil
	}
	service.setTurnPhase(stream, TurnPhaseCanceled)
	// 发送 TurnEndedUpdate 让前端退出活跃状态（否则一直显示 "planning next moves"）
	if err := service.broker.Publish(intent.RequestID, StreamEvent{
		Message: buildTurnEndedMessage(0, 0, 0, 0),
	}); err != nil {
		logger.Errorf("forwarder cancel turn-ended publish failed request_id=%s err=%v", strings.TrimSpace(intent.RequestID), err)
	}
	cancelErr := service.broker.Cancel(intent.RequestID, firstNonEmpty(intent.CancelReason, "[canceled] User aborted request"))
	// 当前 turn 终态后，排空该会话因「子代理运行期间」排队的新消息。
	service.drainRunQueue(stream.ConversationID, stream.RequestID)
	return cancelErr
}

// handleExecResult 处理客户端返回的执行桥结果，并在终态时把 tool_result 写回 history。
func (service *Service) handleMetadataIntent(intent InboundIntent) error {
	stream, ok := service.broker.Get(intent.RequestID)
	if !ok || stream == nil {
		if intent.HasExplicitMode || intent.StartsRun {
			return fmt.Errorf("metadata intent requires active request context: %s", intent.RequestID)
		}
		return nil
	}
	// AsyncAskQuestionCompletionAction：客户端异步 AskQuestion UI 完成时通过
	// ConversationAction 回答案，而不是 InteractionResponse。不处理的话答案被丢弃、
	// 工具永久 pending（只靠 interaction watchdog 收口）。
	if intent.ClientMessage != nil {
		if asyncCompletion := intent.ClientMessage.GetConversationAction().GetAsyncAskQuestionCompletionAction(); asyncCompletion != nil {
			return service.handleAsyncAskQuestionCompletion(stream, intent.RequestID, asyncCompletion)
		}
	}
	backgroundShellToolCallID, backgroundShellActionWasNew := observeBackgroundShellAction(stream, intent.ClientMessage)
	observeBackgroundTaskCompletionAction(stream, intent.ClientMessage)
	backgroundSubagentToolCallID, backgroundSubagentActionWasNew := observeBackgroundSubagentAction(stream, intent.ClientMessage)
	if !checkpointConversationInitialized(stream) {
		if intent.HasExplicitMode {
			stream.mu.Lock()
			stream.Mode = intent.Mode
			stream.UpdatedAt = time.Now().UTC()
			stream.mu.Unlock()
		}
		return nil
	}
	entries := []HistoryEntry{
		newMetadataEntry(stream.TurnSeq, stream.RequestID, "metadata", map[string]any{
			"kind":       intent.Kind,
			"starts_run": intent.StartsRun,
		}),
	}
	if backgroundShellToolCallID != "" && backgroundShellActionWasNew {
		entries = append(entries, newBackgroundShellActionMetadataEntry(stream.TurnSeq, stream.RequestID, backgroundShellToolCallID, backgroundShellActionSourceClient))
	}
	if backgroundSubagentToolCallID != "" && backgroundSubagentActionWasNew {
		entries = append(entries, newBackgroundSubagentActionMetadataEntry(stream.TurnSeq, stream.RequestID, backgroundSubagentToolCallID, backgroundShellActionSourceClient))
	}
	entries = append(entries, backgroundTaskCompletionMetadataEntries(stream.TurnSeq, stream.RequestID, intent.ClientMessage)...)
	if intent.HasExplicitMode {
		modeEntry, err := newModeMetadataEntry(stream.TurnSeq, stream.RequestID, intent.Mode, true, intent.ModeSource)
		if err != nil {
			return err
		}
		modeAliasValue, err := modeAlias(intent.Mode)
		if err != nil {
			return err
		}
		entries = append(entries, modeEntry, newModeChangePromptContextEntry(stream.TurnSeq, stream.RequestID, intent.Mode))
		stream.mu.Lock()
		stream.Mode = intent.Mode
		stream.UpdatedAt = time.Now().UTC()
		stream.mu.Unlock()
		if _, err := service.updateConversationMetaAndCheckpoint(stream, stream.ConversationID, func(item *ConversationFile) error {
			if item == nil {
				return nil
			}
			item.Mode = modeAliasValue
			return nil
		}); err != nil {
			return err
		}
	}
	if _, err := service.appendConversationEntries(stream, stream.ConversationID, entries); err != nil {
		return err
	}
	if intent.HasExplicitMode {
		stream.mu.Lock()
		modelCallID := strings.TrimSpace(stream.CurrentModelCallID)
		stream.mu.Unlock()
		if modelCallID != "" {
			if err := service.syncSummaryCarryForward(stream.ConversationID, intent.RequestID, modelCallID); err != nil {
				return err
			}
		}
		if err := service.publishCheckpoint(intent.RequestID, stream.ConversationID); err != nil {
			return err
		}
	}
	return nil
}

// extractUserMessage 从 legacy run_request 中提取用户消息。
func extractUserMessage(message *agentv1.AgentClientMessage) *agentv1.UserMessage {
	if message == nil || message.GetRunRequest() == nil || message.GetRunRequest().GetAction() == nil {
		return nil
	}
	switch item := message.GetRunRequest().GetAction().GetAction().(type) {
	case *agentv1.ConversationAction_UserMessageAction:
		return item.UserMessageAction.GetUserMessage()
	case *agentv1.ConversationAction_StartPlanAction:
		return item.StartPlanAction.GetUserMessage()
	default:
		return nil
	}
}

// extractRequestContext 从 legacy 请求中提取 request_context。
func extractRequestContext(message *agentv1.AgentClientMessage) *agentv1.RequestContext {
	if message == nil || message.GetRunRequest() == nil || message.GetRunRequest().GetAction() == nil {
		return nil
	}
	switch item := message.GetRunRequest().GetAction().GetAction().(type) {
	case *agentv1.ConversationAction_UserMessageAction:
		return item.UserMessageAction.GetRequestContext()
	case *agentv1.ConversationAction_ResumeAction:
		return item.ResumeAction.GetRequestContext()
	case *agentv1.ConversationAction_StartPlanAction:
		return item.StartPlanAction.GetRequestContext()
	case *agentv1.ConversationAction_ExecutePlanAction:
		return item.ExecutePlanAction.GetRequestContext()
	default:
		return nil
	}
}

func (service *Service) shouldIgnoreEmptyResumeRunRequest(requestID string, runRequest *agentv1.AgentRunRequest, userMessage *agentv1.UserMessage, requestContext *agentv1.RequestContext) bool {
	if runRequest == nil || !conversationActionIsResume(runRequest.GetAction()) {
		return false
	}
	if userMessage != nil || requestContextHasPayload(requestContext) {
		return false
	}
	state := runRequest.GetConversationState()
	if state != nil && len(state.GetPendingToolCalls()) > 0 {
		return false
	}
	conversationID := strings.TrimSpace(runRequest.GetConversationId())
	if conversationID == "" || service.hasActiveConversationStream(conversationID, requestID) {
		return false
	}
	conversation, err := service.loadConversationForResumeGuard(conversationID)
	if err != nil || conversation == nil {
		return false
	}
	return emptyResumeCanBeIgnoredForConversation(conversation)
}

func requestContextHasPayload(requestContext *agentv1.RequestContext) bool {
	return requestContext != nil && proto.Size(requestContext) > 0
}

func (service *Service) loadConversationForResumeGuard(conversationID string) (*ConversationFile, error) {
	if service == nil || service.store == nil {
		return nil, nil
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, nil
	}
	return service.store.LoadConversation(conversationID)
}

// HasActiveConversation reports whether an in-memory stream still owns the conversation.
func (service *Service) HasActiveConversation(conversationID string, requestID string) bool {
	return service.hasActiveConversationStream(conversationID, requestID)
}

func (service *Service) hasActiveConversationStream(conversationID string, requestID string) bool {
	conversationID = strings.TrimSpace(conversationID)
	if service == nil || service.broker == nil || conversationID == "" {
		return false
	}
	if len(service.broker.OtherConversationRequestIDs(conversationID, requestID)) > 0 {
		return true
	}
	stream, ok := service.broker.Get(requestID)
	if !ok || stream == nil {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if strings.TrimSpace(stream.ConversationID) != conversationID {
		return false
	}
	if isTerminalStreamStatus(stream.Status) {
		return false
	}
	switch stream.Phase {
	case TurnPhaseCanceled, TurnPhaseCompleted, TurnPhaseFailed:
		return false
	default:
		return true
	}
}

func emptyResumeCanBeIgnoredForConversation(conversation *ConversationFile) bool {
	if conversation == nil {
		return false
	}
	status := strings.TrimSpace(conversation.CurrentLoopStatus)
	currentRequestID := strings.TrimSpace(conversation.CurrentRequestID)
	if status == "" {
		return currentRequestID == ""
	}
	switch status {
	case "completed", "idle":
		return true
	default:
		return false
	}
}

func extractConversationActionUserMessage(action *agentv1.ConversationAction) *agentv1.UserMessage {
	if action == nil {
		return nil
	}
	switch item := action.GetAction().(type) {
	case *agentv1.ConversationAction_UserMessageAction:
		return item.UserMessageAction.GetUserMessage()
	case *agentv1.ConversationAction_StartPlanAction:
		return item.StartPlanAction.GetUserMessage()
	default:
		return nil
	}
}

func extractConversationActionRequestContext(action *agentv1.ConversationAction) *agentv1.RequestContext {
	if action == nil {
		return nil
	}
	switch item := action.GetAction().(type) {
	case *agentv1.ConversationAction_UserMessageAction:
		return item.UserMessageAction.GetRequestContext()
	case *agentv1.ConversationAction_ResumeAction:
		return item.ResumeAction.GetRequestContext()
	case *agentv1.ConversationAction_StartPlanAction:
		return item.StartPlanAction.GetRequestContext()
	case *agentv1.ConversationAction_ExecutePlanAction:
		return item.ExecutePlanAction.GetRequestContext()
	default:
		return nil
	}
}

func conversationActionIsResume(action *agentv1.ConversationAction) bool {
	if action == nil {
		return false
	}
	_, ok := action.GetAction().(*agentv1.ConversationAction_ResumeAction)
	return ok
}

func inboundConversationAction(message *agentv1.AgentClientMessage) *agentv1.ConversationAction {
	if message == nil {
		return nil
	}
	if action := message.GetConversationAction(); action != nil {
		return action
	}
	if runRequest := message.GetRunRequest(); runRequest != nil {
		return runRequest.GetAction()
	}
	return nil
}

func conversationActionIsSummarize(action *agentv1.ConversationAction) bool {
	if action == nil {
		return false
	}
	_, ok := action.GetAction().(*agentv1.ConversationAction_SummarizeAction)
	return ok
}

func resolveInboundManualCompaction(message *agentv1.AgentClientMessage, userMessage *agentv1.UserMessage) manualCompactionDirective {
	instruction, requested := parseManualCompactionRequest(userMessage)
	if conversationActionIsSummarize(inboundConversationAction(message)) {
		requested = true
	}
	return manualCompactionDirective{
		Requested:   requested,
		Instruction: instruction,
	}
}

func conversationActionStartsRun(action *agentv1.ConversationAction) bool {
	if action == nil {
		return false
	}
	switch action.GetAction().(type) {
	case *agentv1.ConversationAction_UserMessageAction,
		*agentv1.ConversationAction_ResumeAction,
		*agentv1.ConversationAction_SummarizeAction,
		*agentv1.ConversationAction_StartPlanAction,
		*agentv1.ConversationAction_ExecutePlanAction:
		return true
	default:
		return false
	}
}

// extractRunMode 推导本轮应使用的 mode。
func extractRunMode(message *agentv1.AgentClientMessage) (agentv1.AgentMode, ModeSource, bool, error) {
	if message != nil && message.GetRunRequest() != nil && message.GetRunRequest().GetAction() != nil {
		switch item := message.GetRunRequest().GetAction().GetAction().(type) {
		case *agentv1.ConversationAction_StartPlanAction:
			return resolveExplicitMode(agentv1.AgentMode_AGENT_MODE_PLAN, ModeSourceStartPlanAction)
		case *agentv1.ConversationAction_ExecutePlanAction:
			mode := agentv1.AgentMode_AGENT_MODE_AGENT
			if item.ExecutePlanAction != nil && item.ExecutePlanAction.GetExecutionMode() != agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
				mode = item.ExecutePlanAction.GetExecutionMode()
			}
			return resolveExplicitMode(mode, ModeSourceExecutePlanAction)
		}
	}
	if userMessage := extractUserMessage(message); userMessage != nil && userMessage.GetMode() != agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
		return resolveExplicitMode(userMessage.GetMode(), ModeSourceUserMessage)
	}
	if message != nil && message.GetRunRequest() != nil && message.GetRunRequest().GetConversationState() != nil {
		if mode := message.GetRunRequest().GetConversationState().GetMode(); mode != agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
			return resolveExplicitMode(mode, ModeSourceConversationState)
		}
	}
	return agentv1.AgentMode_AGENT_MODE_AGENT, ModeSourceUnknown, false, nil
}

func extractPrewarmMode(request *agentv1.PrewarmRequest) (agentv1.AgentMode, ModeSource, bool, error) {
	if request == nil || request.GetConversationState() == nil {
		return agentv1.AgentMode_AGENT_MODE_AGENT, ModeSourceUnknown, false, nil
	}
	mode := request.GetConversationState().GetMode()
	if mode == agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
		return agentv1.AgentMode_AGENT_MODE_AGENT, ModeSourceUnknown, false, nil
	}
	return resolveExplicitMode(mode, ModeSourceConversationState)
}

func extractConversationActionMode(action *agentv1.ConversationAction) (agentv1.AgentMode, ModeSource, bool, error) {
	if action == nil {
		return agentv1.AgentMode_AGENT_MODE_AGENT, ModeSourceUnknown, false, nil
	}
	switch item := action.GetAction().(type) {
	case *agentv1.ConversationAction_StartPlanAction:
		return resolveExplicitMode(agentv1.AgentMode_AGENT_MODE_PLAN, ModeSourceStartPlanAction)
	case *agentv1.ConversationAction_ExecutePlanAction:
		mode := agentv1.AgentMode_AGENT_MODE_AGENT
		if item.ExecutePlanAction != nil && item.ExecutePlanAction.GetExecutionMode() != agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
			mode = item.ExecutePlanAction.GetExecutionMode()
		}
		return resolveExplicitMode(mode, ModeSourceExecutePlanAction)
	}
	if userMessage := extractConversationActionUserMessage(action); userMessage != nil && userMessage.GetMode() != agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
		return resolveExplicitMode(userMessage.GetMode(), ModeSourceUserMessage)
	}
	return agentv1.AgentMode_AGENT_MODE_AGENT, ModeSourceUnknown, false, nil
}

// extractRequestedModelID 提取本轮显式请求的模型 ID。
func extractRequestedModelID(message *agentv1.AgentClientMessage) string {
	if message == nil {
		return ""
	}
	if runRequest := message.GetRunRequest(); runRequest != nil {
		return firstNonEmpty(extractRequestedModelIDFromRequestedModel(runRequest.GetRequestedModel()), runRequest.GetModelDetails().GetModelId())
	}
	if prewarm := message.GetPrewarmRequest(); prewarm != nil {
		return firstNonEmpty(extractRequestedModelIDFromRequestedModel(prewarm.GetRequestedModel()), prewarm.GetModelDetails().GetModelId())
	}
	return ""
}

func extractRequestedModelIDFromRequestedModel(model *agentv1.RequestedModel) string {
	if model == nil {
		return ""
	}
	if model.GetIsVariantStringRepresentation() {
		modelID, _ := splitRuntimeThinkingEffortVariantString(model.GetModelId())
		if modelID != "" {
			return modelID
		}
		// variant 拆分失败（如 hash 格式的 channel ID 无冒号）时，
		// 回退到原始 model_id，避免丢失用户选择的模型
		return strings.TrimSpace(model.GetModelId())
	}
	return strings.TrimSpace(model.GetModelId())
}

func extractRuntimeThinkingEffort(message *agentv1.AgentClientMessage) string {
	if message == nil {
		return ""
	}
	if runRequest := message.GetRunRequest(); runRequest != nil {
		return extractRuntimeThinkingEffortFromRequestedModel(runRequest.GetRequestedModel())
	}
	if prewarm := message.GetPrewarmRequest(); prewarm != nil {
		return extractRuntimeThinkingEffortFromRequestedModel(prewarm.GetRequestedModel())
	}
	return ""
}

func extractRuntimeThinkingEffortFromRequestedModel(model *agentv1.RequestedModel) string {
	if model == nil {
		return ""
	}
	for _, parameter := range model.GetParameters() {
		if parameter == nil || !isRuntimeThinkingEffortParameterID(parameter.GetId()) {
			continue
		}
		if effort := normalizeRuntimeThinkingEffort(parameter.GetValue()); effort != "" {
			return effort
		}
	}
	if model.GetIsVariantStringRepresentation() {
		if _, effort := splitRuntimeThinkingEffortVariantString(model.GetModelId()); effort != "" {
			return effort
		}
		return normalizeRuntimeThinkingEffort(model.GetModelId())
	}
	return ""
}

// extractRequestedMaxMode 提取本轮请求的 max_mode 开关。
func extractRequestedMaxMode(message *agentv1.AgentClientMessage) bool {
	if message == nil {
		return false
	}
	if runRequest := message.GetRunRequest(); runRequest != nil {
		if model := runRequest.GetRequestedModel(); model != nil {
			return model.GetMaxMode()
		}
	}
	if prewarm := message.GetPrewarmRequest(); prewarm != nil {
		if model := prewarm.GetRequestedModel(); model != nil {
			return model.GetMaxMode()
		}
	}
	return false
}

func isRuntimeThinkingEffortParameterID(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case runtimeThinkingEffortParameterID,
		"effort",
		"reasoning",
		"reasoning_effort",
		"thinking_intensity",
		"anthropic_thinking_effort",
		"openai_reasoning_effort":
		return true
	default:
		return false
	}
}

func normalizeRuntimeThinkingEffort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "disabled", "low", "medium", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(raw))
	case "disable", "off", "none", "false", "no", "0":
		return "disabled"
	case "very_high", "very-high", "veryhigh", "x-high", "extra_high", "extra-high", "extrahigh":
		return "xhigh"
	case "maximum":
		return "max"
	default:
		return ""
	}
}

func splitRuntimeThinkingEffortVariantString(raw string) (string, string) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", ""
	}
	if effort := normalizeRuntimeThinkingEffort(text); effort != "" {
		return "", effort
	}
	index := strings.LastIndex(text, ":")
	if index <= 0 || index >= len(text)-1 {
		return "", ""
	}
	modelID := strings.TrimSpace(text[:index])
	effort := normalizeRuntimeThinkingEffort(text[index+1:])
	if modelID == "" || effort == "" {
		return "", ""
	}
	return modelID, effort
}

func (service *Service) resolveRequestedModelName(message *agentv1.AgentClientMessage, modelID string) string {
	if message != nil {
		if runRequest := message.GetRunRequest(); runRequest != nil {
			if name := firstNonEmpty(
				runRequest.GetModelDetails().GetDisplayName(),
				runRequest.GetModelDetails().GetDisplayNameShort(),
				runRequest.GetModelDetails().GetDisplayModelId(),
				runRequest.GetModelDetails().GetModelId(),
			); name != "" {
				return name
			}
		}
		if prewarm := message.GetPrewarmRequest(); prewarm != nil {
			if name := firstNonEmpty(
				prewarm.GetModelDetails().GetDisplayName(),
				prewarm.GetModelDetails().GetDisplayNameShort(),
				prewarm.GetModelDetails().GetDisplayModelId(),
				prewarm.GetModelDetails().GetModelId(),
			); name != "" {
				return name
			}
		}
	}
	if service != nil && service.resolver != nil {
		channel, err := service.resolver.SelectChannelForModel(context.Background(), strings.TrimSpace(modelID))
		if err == nil && channel != nil {
			if name := firstNonEmpty(channel.Name, channel.Model); name != "" {
				return name
			}
		}
	}
	return strings.TrimSpace(modelID)
}
