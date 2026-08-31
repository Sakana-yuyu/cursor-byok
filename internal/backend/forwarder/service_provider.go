// service_provider.go 承载 provider 驱动、输出预算与模型上下文窗口同步。
package forwarder

import (
	"context"
	"cursor/gen/agentv1"
	"cursor/internal/apperror"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/logger"
	"cursor/internal/modelcontext"
	"cursor/internal/safego"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

func (service *Service) scheduleProviderResume(stream *ActiveStream, _ int) error {
	return service.requestProviderAction(stream, providerActionResume)
}

func shouldResumeAfterToolResults(finishReason string) bool {
	switch strings.TrimSpace(finishReason) {
	case "tool_use", "tool_calls", "function_call":
		return true
	default:
		return false
	}
}

// parentAgentProviderPassSafetyLimit 是父 agent 单次回合 provider pass 的硬上限。
// 正常 agent 回合的工具调用循环远低于该值；达到上限说明模型陷入死循环，兜底收口防止无限空转。
const parentAgentProviderPassSafetyLimit = 200

// parentAgentProviderTurnDurationLimit 是父 agent 单次回合的墙钟时长兜底（自 handleRunIntent 起算）。
const parentAgentProviderTurnDurationLimit = 3 * time.Hour

// delegatedProviderStreamIdleTimeout 是委派/子代理流的 provider 流空闲看门狗时长。
// 交互式父回合保持全局默认（90s）；委派 worker 与 native 子代理可能合法长静默
// （长思考、慢工具），由委派层各自的「无有效进展」看门狗（native 5min / worker 30min）
// 兜底，流级看门狗只负责识别真正断死的连接，取 10 分钟既覆盖长静默又不晚于 worker 兜底。
const delegatedProviderStreamIdleTimeout = 10 * time.Minute

// driveProvider 由 actor 触发一次 provider pass，并把真实流包装成 provider_event 回投 mailbox。
func (service *Service) driveProvider(stream *ActiveStream) error {
	if stream == nil {
		return nil
	}
	stream.mu.Lock()
	if stream.ProviderActive || stream.Status == StreamStatusCanceled || stream.Status == StreamStatusCompleted || stream.Status == StreamStatusFailed {
		stream.mu.Unlock()
		return nil
	}
	// 单回合安全预算兜底：防止模型陷入工具调用死循环无限空转。
	if stream.ProviderPassCount >= parentAgentProviderPassSafetyLimit {
		stream.mu.Unlock()
		return service.closeStreamWithTurnBudgetExceeded(stream, fmt.Sprintf("达到单回合 provider 调用上限 %d，疑似死循环，已安全停止", parentAgentProviderPassSafetyLimit))
	}
	if !stream.ProviderTurnStartedAt.IsZero() && time.Since(stream.ProviderTurnStartedAt) >= parentAgentProviderTurnDurationLimit {
		stream.mu.Unlock()
		return service.closeStreamWithTurnBudgetExceeded(stream, fmt.Sprintf("达到单回合时长上限 %s，已安全停止", parentAgentProviderTurnDurationLimit))
	}
	stream.ProviderPassCount++
	currentPass := stream.ProviderPassCount
	stream.Status = StreamStatusStreaming
	stream.PendingProviderAction = providerActionNone
	stream.CurrentModelCallID = uuid.NewString()
	stream.CurrentProviderToken++
	currentToken := stream.CurrentProviderToken
	stream.ProviderAccumulatedText = nil
	stream.ProviderAccumulatedReasoning = nil
	stream.ProviderAccumulatedReasoningSignature = ""
	stream.ProviderAccumulatedReasoningSignatureSource = ""
	stream.ProviderAccumulatedReasoningItemID = ""
	stream.ProviderAccumulatedReasoningStatus = ""
	stream.ProviderAccumulatedReasoningSummary = nil
	if stream.ProviderSyntheticThinkingStartedAt.IsZero() {
		stream.ProviderSyntheticThinkingStartedAt = time.Now().UTC()
	}
	// Synthetic encrypted-thinking placeholder 属于整个 Cursor turn，而不是
	// 单个 provider pass。保留 Published 标记，避免工具调用后的重试 pass
	// 再次创建 Cursor 思考块；新 turn 在 handleRunIntent 中统一清零。
	stream.ProviderFinishReason = ""
	stream.ProviderUsage = turnUsageSnapshot{}
	stream.ToolInvocationCount = 0
	modelCallID := stream.CurrentModelCallID
	conversationID := stream.ConversationID
	requestID := stream.RequestID
	modelID := stream.ModelID
	modelName := stream.ModelName
	thinkingEffort := stream.ThinkingEffort
	maxMode := stream.MaxMode
	mode := stream.Mode
	latestUserText := stream.LatestUserText
	customSystemPrompt := stream.CustomSystemPrompt
	thinkingCompletedPublished := stream.ProviderSyntheticThinkingPublished
	thinkingDeltaCount := stream.ProviderThinkingDeltaCount
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	logger.Infof("forwarder provider pass started request_id=%s model_call_id=%s provider_pass=%d thinking_completed=%t thinking_delta_count=%d", strings.TrimSpace(requestID), strings.TrimSpace(modelCallID), currentPass, thinkingCompletedPublished, thinkingDeltaCount)
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), requestID, conversationID, "provider_pass_started", map[string]any{
			"model_call_id":                strings.TrimSpace(modelCallID),
			"provider_pass":                currentPass,
			"thinking_completed_published": thinkingCompletedPublished,
			"thinking_delta_count":         thinkingDeltaCount,
		})
	}

	conversation, _, _, err := service.snapshotCheckpointConversation(stream)
	if err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	conversation, err = service.syncConversationContextWindowTokens(stream, conversationID, conversation)
	if err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	conversation, err = service.persistDerivedPromptContexts(stream, conversationID, requestID, conversation, mode, latestUserText)
	if err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	compiled, err := service.compiler.Compile(conversation, mode, latestUserText, modelName, customSystemPrompt)
	if err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	compiled = guardCompiledConversationForProvider(compiled)
	canonicalConversation := conversation
	canonicalCompiled := compiled
	_, manualCompactionRequested := streamManualCompactionDirective(stream)
	var activeContextProjection *contextProjectionState
	var activeTurnProjectionStats *activeTurnToolResultCompactionStats
	projectionSidecarHit := false
	projectionInvalidationReason := ""
	if !manualCompactionRequested && service.contextProjectionPressureExceeded(stream, canonicalConversation, canonicalCompiled) {
		projectedConversation, projectionState, active, invalidationReason := service.prepareConversationContextProjectionState(canonicalConversation, contextProjectionModelKey(stream))
		if active {
			projectedCompiled, compileErr := service.compiler.Compile(projectedConversation, mode, latestUserText, modelName, customSystemPrompt)
			if compileErr != nil {
				service.setTurnPhase(stream, TurnPhaseFailed)
				return service.failStream(stream, "unknown", compileErr)
			}
			conversation = projectedConversation
			compiled = guardCompiledConversationForProvider(projectedCompiled)
			compiled.StableMessageCount = contextProjectionStableMessageCount(projectionState, compiled.StableMessageCount)
			activeContextProjection = projectionState
			projectionSidecarHit = true
		} else {
			projectionInvalidationReason = invalidationReason
		}
	}
	if activeContextProjection != nil && service.contextProjectionPressureExceeded(stream, conversation, compiled) {
		contextWindowTokens := compactionContextWindowSize(conversation)
		reserveTokens := service.resolveCompactionReserveTokens(modelID)
		if reserveTokens <= 0 {
			reserveTokens = compactionAutoReserveTokens
		}
		budgetTokens := int64(float64(contextWindowTokens)*contextProjectionHardRatio) - reserveTokens
		projectedCompiled, stats, changedEnough := compactActiveTurnToolResultsForBudget(conversation, compiled, budgetTokens)
		if stats.ShortenedResults > 0 || stats.OmittedResults > 0 {
			compiled = projectedCompiled
			activeTurnProjectionStats = &stats
			if !changedEnough {
				projectionInvalidationReason = "active_turn_projection_insufficient"
			}
		}
	}
	// 大窗口下 provider usage 反映 projected 发送体积，UI/ canonical 体量更高时需同步压力标记，
	// 否则 sidecar 有效时 80% 自动压缩不触发。64K 等小窗口测试场景不受影响。
	service.syncCanonicalPressureForLargeWindows(canonicalConversation, canonicalCompiled)
	if compacted, compactErr := service.maybeCompactBeforeProvider(stream, canonicalConversation, compiled); compactErr != nil {
		// 自动/投影压缩链穷尽（或 preflight 前估算超限）时，先尝试一次强制 legacy 兜底压缩
		//（自动 /summarize），而不是直接以 context_overflow_after_compaction 终态失败。
		if escalated, escErr := service.escalateForcedPreflightCompaction(stream, canonicalConversation, canonicalCompiled, compactErr); escErr != nil {
			service.setTurnPhase(stream, TurnPhaseFailed)
			return service.failStream(stream, "unknown", escErr)
		} else if escalated {
			service.finalizeCompactionAdmission(stream)
			return nil
		}
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", compactErr)
	} else if compacted {
		service.finalizeCompactionAdmission(stream)
		return nil
	}
	if err := service.syncSummarySnapshot(stream, canonicalConversation, requestID, modelCallID); err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	service.maybeSaveLastAgentModelHash(canonicalConversation, modelID, mode, currentPass)
	// 视觉代理：主模型不支持图片输入时，自动把消息中的图片委派给识图模型，
	// 用返回的画面描述 / OCR 文本替换图片块，使纯文本模型也能“看图”。
	// 此处持有 service.provider，可发起同步子调用；替换后不再含图片 ContentPart，
	// 下游 router.stripImagesFromMessages 会原样放行，不会重复处理。
	// 未启用视觉委派时，从工具清单剔除 see_image，避免模型调用一个不可用的工具。
	if service.needsVisionProxy(modelID, modelName, compiled.Messages) {
		vdbg("[service] needsVisionProxy true -> run vision pass msgs=%d model=%s model_id=%s", len(compiled.Messages), modelName, modelID)
		// 视觉委派是同步 LLM 调用（synthesizeImageDescriptions 内部还有独立超时），
		// 期间把取消句柄挂到 stream.ProviderCancel：shutdown / 并发新回合可直接中断识图，
		// 避免 browser 多截图场景把对话拖到客户端超时判定掉线。
		visionCtx, visionCancel := context.WithCancel(context.Background())
		stream.mu.Lock()
		stream.ProviderCancel = visionCancel
		stream.mu.Unlock()
		compiled.Messages = service.synthesizeImageDescriptions(visionCtx, requestID, conversationID, compiled.Messages, modelName)
		visionCancel()
		// 视觉 pass 已结束，且主模型请求尚未开始：直接清空取消句柄。
		// （若 pass 期间被 forceCancelStreamProvider 并发清空过，这里同样置 nil，语义一致。）
		stream.mu.Lock()
		stream.ProviderCancel = nil
		stream.mu.Unlock()
	} else {
		vdbg("[service] needsVisionProxy false enabled=%v", service.visionProxyEnabled())
	}
	if !service.visionProxyEnabled() {
		compiled.Tools = filterToolDescriptorByName(compiled.Tools, seeImageToolName)
	}
	// descriptor-400 恢复：provider 明确拒绝某工具 schema 时，本回合把它从后续
	// provider 请求中剔除（见 handleProviderDoneEvent 的 tool_schema recovery）。
	if quarantined := snapshotProviderToolQuarantine(stream); len(quarantined) > 0 {
		compiled.Tools = filterToolDescriptorsByNameSet(compiled.Tools, quarantined)
	}
	maxTokens, requestKnobs := service.resolveProviderOutputBudget(modelID, modelName, conversation, compiled)
	// max_tokens 超限恢复：若本回合因中转站 400 触发过降级重试，用恢复上限覆盖预算，
	// 确保重试请求的 max_tokens 不超过中转站真实限制。
	stream.mu.Lock()
	recoveryCap := stream.MaxTokensRecoveryCap
	stream.mu.Unlock()
	if recoveryCap > 0 && recoveryCap < maxTokens {
		maxTokens = recoveryCap
		if requestKnobs == nil {
			requestKnobs = map[string]any{}
		}
		requestKnobs["max_tokens_recovery_cap"] = recoveryCap
	}
	if err := validateProviderRequestContextBudget(conversation, compiled, maxTokens); err != nil {
		// preflight 最终校验超限：尝试一次强制 legacy 兜底压缩（自动 /summarize），
		// 压缩完成后 resume 重算预算；attempts 耗尽或无可压缩内容才按原样终态失败。
		if escalated, escErr := service.escalateForcedPreflightCompaction(stream, canonicalConversation, canonicalCompiled, err); escErr != nil {
			service.setTurnPhase(stream, TurnPhaseFailed)
			return service.failStream(stream, "unknown", escErr)
		} else if escalated {
			service.finalizeCompactionAdmission(stream)
			return nil
		}
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	projectionDiagnostics := contextProjectionRequestDiagnostics(
		"parent",
		canonicalConversation,
		activeContextProjection,
		projectionSidecarHit,
		projectionInvalidationReason,
		canonicalCompiled,
		compiled,
		compactionContextWindowSize(conversation),
		service.resolveCompactionReserveTokens(modelID),
		maxTokens,
		0,
		1,
	)
	if activeTurnProjectionStats != nil {
		projectionDiagnostics["active_turn_projection"] = true
		projectionDiagnostics["active_turn_input_tokens_before"] = activeTurnProjectionStats.BeforeTokens
		projectionDiagnostics["active_turn_input_tokens_after"] = activeTurnProjectionStats.AfterTokens
		projectionDiagnostics["active_turn_shortened_results"] = activeTurnProjectionStats.ShortenedResults
		projectionDiagnostics["active_turn_omitted_results"] = activeTurnProjectionStats.OmittedResults
		projectionDiagnostics["active_turn_latest_tool_call_id"] = activeTurnProjectionStats.LatestToolCallID
	}
	if requestKnobs == nil {
		requestKnobs = map[string]any{}
	}
	requestKnobs["context_projection"] = projectionDiagnostics
	service.debug.LogRuntime(context.Background(), requestID, conversationID, "context_projection_applied", projectionDiagnostics)
	ctx, cancel := context.WithCancel(context.Background())
	stream.mu.Lock()
	stream.ProviderActive = true
	stream.ProviderCancel = cancel
	stream.ProviderPassToolNames = toolDescriptorNames(compiled.Tools)
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	service.setTurnPhase(stream, TurnPhaseProviderRunning)

	providerRequest := ProviderRequest{
		RequestID:          requestID,
		ConversationID:     conversationID,
		RunID:              requestID,
		ModelCallID:        modelCallID,
		ModelID:            modelID,
		ModelName:          modelName,
		Role:               "parent",
		ExecutionMode:      "parent",
		Mode:               compiled.Mode,
		ThinkingEffort:     compiled.Mode.String(),
		MaxMode:            maxMode,
		Messages:           compiled.Messages,
		StableMessageCount: compiled.StableMessageCount,
		Tools:              compiled.Tools,
		MaxTokens:          maxTokens,
		RequestKnobs:       requestKnobs,
		CompileSummary:     compiled.CompileSummary,
		Observer:           service.recorder,
		ArtifactPaths:      &modeladapter.LLMArtifactPaths{},
	}
	// native 子代理流放宽上游看门狗：子代理会话带 SubagentTypeName，可能合法长静默
	// （长思考/慢工具），由 native 无进展看门狗（5min）兜底；父代理流保持全局默认。
	if conversation != nil && strings.TrimSpace(conversation.SubagentTypeName) != "" {
		providerRequest.ProviderStreamIdleTimeout = delegatedProviderStreamIdleTimeout
	}
	providerRequest.ThinkingEffort = thinkingEffort
	service.debug.LogProvider(context.Background(), requestID, conversationID, "provider_request_prepared", map[string]any{
		"model_call_id":          strings.TrimSpace(modelCallID),
		"provider_pass":          currentPass,
		"model_id":               strings.TrimSpace(modelID),
		"model_name":             strings.TrimSpace(modelName),
		"mode":                   compiled.Mode.String(),
		"thinking_effort":        strings.TrimSpace(thinkingEffort),
		"max_tokens":             maxTokens,
		"request_knobs":          requestKnobs,
		"message_count":          len(compiled.Messages),
		"tool_count":             len(compiled.Tools),
		"compile_summary_length": len(compiled.CompileSummary),
	})
	// 目录未覆盖审计：模型名不在内置能力目录时记录一次（进程内按模型名去重）。
	// 只做观测，不改变请求内容、渠道选择或预算；供用户排查「能力未知的模型」入口。
	service.auditCatalogUncovered(context.Background(), requestID, conversationID, modelName)
	var markProjectionApplied func()
	if activeContextProjection != nil {
		markProjectionApplied = func() {
			if err := service.markContextProjectionApplied(canonicalConversation, activeContextProjection); err != nil {
				logger.Errorf("forwarder context projection applied marker failed request_id=%s conversation_id=%s: %v", strings.TrimSpace(requestID), strings.TrimSpace(conversationID), err)
			}
		}
	}
	safego.GoWithPanicHandler("forwarder:provider-stream", func() {
		service.runProviderStream(stream, currentToken, ctx, providerRequest, markProjectionApplied)
	}, func(panicErr error) {
		postErr := service.postStreamCommandWait(stream, streamCommand{
			Kind: streamCommandProviderEvent,
			Provider: &streamProviderEvent{
				Token: currentToken,
				Done:  true,
				Err:   panicErr,
			},
		})
		if postErr != nil && !errors.Is(postErr, errProviderLoopInterrupted) {
			logger.Errorf("forwarder provider panic completion post failed request_id=%s err=%v", strings.TrimSpace(stream.RequestID), postErr)
			if terminalErr := service.failStreamIfNonTerminal(stream, "panic", apperror.Join(panicErr, postErr)); terminalErr != nil {
				logger.Errorf("forwarder provider panic terminalization failed request_id=%s err=%v", strings.TrimSpace(stream.RequestID), terminalErr)
			}
		}
	})
	return nil
}

// auditCatalogUncovered 在 provider 请求准备完成后，检查真实模型名是否命中内置能力目录。
// 未命中时写一条去重的 runtime 审计事件，提示「目录未覆盖，能力未知」。
func (service *Service) auditCatalogUncovered(ctx context.Context, requestID string, conversationID string, modelName string) {
	modelKey := strings.TrimSpace(modelName)
	if modelKey == "" {
		return
	}
	if lookup := modelcontext.Lookup(modelKey); lookup.Covered {
		return
	}
	normalizedID := modelcontext.NormalizeModelID(modelKey)
	if _, already := service.catalogUncoveredReported.LoadOrStore(modelKey, struct{}{}); already {
		return
	}
	service.debug.LogRuntime(ctx, requestID, conversationID, "catalog_uncovered", map[string]any{
		"model_name":       modelKey,
		"normalized_model": normalizedID,
		"hint":             "模型不在内置能力目录中，能力未知；保守运行（图片不直传），可在模型编辑页补填能力",
	})
	logger.Infof("forwarder catalog_uncovered model_name=%s request_id=%s", modelKey, strings.TrimSpace(requestID))
}

func (service *Service) resolveProviderOutputBudget(modelID string, modelName string, conversation *ConversationFile, compiled CompiledConversation) (int, map[string]any) {
	configuredMaxTokens := service.resolveConfiguredProviderMaxOutputTokens(modelID)
	contextWindowTokens := compactionContextWindowSize(conversation)
	estimatedPromptTokens := estimateCompiledPromptTokens(compiled)
	remainingTokens := int64(0)
	requestMaxTokens := int64(configuredMaxTokens)
	if requestMaxTokens <= 0 {
		requestMaxTokens = providerDefaultMaxOutputTokens
	}
	// catalog 记录了每个模型 provider 侧允许的最大输出 token 数。
	// 某些 provider（如 Neurons 代理的 k2.7）会对超出的 max_tokens 直接返回 400，
	// 因此这里必须把它当作硬上限：无论 channel 配了多大的值，都不能超过模型上限。
	//
	// 注意：modelID 可能是客户端内部哈希 ID（如 "4fd90578ea9510b1"），catalog 无法匹配；
	// 必须优先用显示名 modelName（如 "kimi-k2.7-code"）查 catalog，否则会返回 0 导致 cap 失效、
	// 发出默认 65536 触发中转站 400。modelID 仅作兜底。
	catalogModelKey := strings.TrimSpace(modelName)
	if catalogModelKey == "" {
		catalogModelKey = strings.TrimSpace(modelID)
	}
	catalogMax := int64(modelcontext.MaxOutputTokens(catalogModelKey))
	if catalogMax <= 0 {
		// 显示名未命中时再用 modelID 兜底（少数场景 modelID 即真实模型名）。
		catalogMax = int64(modelcontext.MaxOutputTokens(modelID))
	}
	if catalogMax > 0 && catalogMax < requestMaxTokens {
		requestMaxTokens = catalogMax
	}
	if contextWindowTokens > 0 && estimatedPromptTokens > 0 {
		remainingTokens = contextWindowTokens - estimatedPromptTokens
		allowedTokens := remainingTokens - providerOutputSafetyTokens
		if allowedTokens < 1 {
			allowedTokens = 1
		}
		if allowedTokens < requestMaxTokens {
			requestMaxTokens = allowedTokens
		}
	}
	maxTokens := int(requestMaxTokens)
	if maxTokens <= 0 {
		maxTokens = 1
	}
	requestKnobs := map[string]any{
		"configured_max_tokens":             configuredMaxTokens,
		"dynamic_max_tokens":                maxTokens,
		"catalog_model_key":                 catalogModelKey,
		"catalog_max_output_tokens":         modelcontext.MaxOutputTokens(catalogModelKey),
		"compiled_prompt_tokens_estimate":   estimatedPromptTokens,
		"context_window_tokens":             contextWindowTokens,
		"remaining_context_tokens_estimate": remainingTokens,
		"provider_output_safety_tokens":     providerOutputSafetyTokens,
	}
	return maxTokens, withPreviousCacheFrontierHint(requestKnobs, conversation)
}

// validateProviderRequestContextBudget is the final preflight after every
// request rewrite and output cap. The budget resolver intentionally retains a
// one-token fallback for compatible providers; this guard prevents that
// fallback from sending a request whose input plus required safety space is
// already larger than the configured model window.
func validateProviderRequestContextBudget(conversation *ConversationFile, compiled CompiledConversation, maxTokens int) error {
	contextWindowTokens := compactionContextWindowSize(conversation)
	if contextWindowTokens <= 0 {
		return nil
	}
	estimatedPromptTokens := estimateCompiledPromptTokens(compiled)
	if estimatedPromptTokens <= 0 {
		return nil
	}
	if maxTokens < 1 {
		maxTokens = 1
	}
	totalRequiredTokens := estimatedPromptTokens + int64(maxTokens) + providerOutputSafetyTokens
	if totalRequiredTokens <= contextWindowTokens {
		return nil
	}
	return compactionTerminalError{
		code: compactionOverflowTerminalCode,
		message: fmt.Sprintf(
			"provider request exceeds context window after compaction (input=%d output=%d safety=%d window=%d)",
			estimatedPromptTokens,
			maxTokens,
			providerOutputSafetyTokens,
			contextWindowTokens,
		),
	}
}

func withPreviousCacheFrontierHint(requestKnobs map[string]any, conversation *ConversationFile) map[string]any {
	if len(requestKnobs) == 0 {
		requestKnobs = map[string]any{}
	}
	if conversation == nil || conversation.LatestRequestPrefix == nil {
		return requestKnobs
	}
	prefix := conversation.LatestRequestPrefix
	frontierHash := strings.TrimSpace(prefix.FrontierHash)
	if frontierHash == "" {
		return requestKnobs
	}
	requestKnobs["previous_cache_frontier_hash"] = frontierHash
	requestKnobs["previous_cache_frontier"] = map[string]any{
		"canonical_body_hash": prefix.CanonicalBodyHash,
		"frontier_hash":       frontierHash,
		"frontier_path":       prefix.FrontierPath,
		"breakpoint_count":    prefix.BreakpointCount,
		"request_id":          strings.TrimSpace(prefix.RequestID),
		"model_call_id":       strings.TrimSpace(prefix.ModelCallID),
	}
	return requestKnobs
}

func (service *Service) resolveConfiguredProviderMaxOutputTokens(modelID string) int {
	if service == nil || service.resolver == nil {
		return providerDefaultMaxOutputTokens
	}
	channel, err := service.resolver.SelectChannelForModel(context.Background(), strings.TrimSpace(modelID))
	if err != nil || channel == nil {
		return providerDefaultMaxOutputTokens
	}
	maxTokens := configuredProviderMaxOutputTokens(channel.Provider, channel.MaxTokens, channel.AnthropicMaxTokens)
	if maxTokens <= 0 {
		return providerDefaultMaxOutputTokens
	}
	return maxTokens
}

func configuredProviderMaxOutputTokens(provider string, maxTokens int, anthropicMaxTokens int) int {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic":
		if anthropicMaxTokens > 0 {
			return anthropicMaxTokens
		}
		if maxTokens > 0 {
			return maxTokens
		}
	case "openai":
		if maxTokens > 0 {
			return maxTokens
		}
		if anthropicMaxTokens > 0 {
			return anthropicMaxTokens
		}
	default:
		if maxTokens > 0 && anthropicMaxTokens > 0 {
			if anthropicMaxTokens > maxTokens {
				return anthropicMaxTokens
			}
			return maxTokens
		}
		if maxTokens > 0 {
			return maxTokens
		}
		if anthropicMaxTokens > 0 {
			return anthropicMaxTokens
		}
	}
	return providerDefaultMaxOutputTokens
}

func (service *Service) maybeSaveLastAgentModelHash(conversation *ConversationFile, modelID string, mode agentv1.AgentMode, providerPass int) {
	if service == nil || service.modelMemory == nil || service.resolver == nil {
		return
	}
	if providerPass != 1 || !isSupportedActiveMode(mode) {
		return
	}
	if conversation != nil && strings.TrimSpace(conversation.SubagentTypeName) != "" {
		return
	}
	channel, err := service.resolver.SelectChannelForModel(context.Background(), strings.TrimSpace(modelID))
	if err != nil || channel == nil || strings.TrimSpace(channel.ID) == "" {
		if err != nil {
			logger.Errorf("forwarder skipped last agent model hash update model_id=%s error=%v", strings.TrimSpace(modelID), err)
		}
		return
	}
	if err := service.modelMemory.SaveLastAgentModelHash(context.Background(), strings.TrimSpace(channel.ID)); err != nil {
		logger.Errorf("forwarder failed to save last agent model hash channel_id=%s error=%v", strings.TrimSpace(channel.ID), err)
	}
}

func (service *Service) persistDerivedPromptContexts(stream *ActiveStream, conversationID string, requestID string, conversation *ConversationFile, mode agentv1.AgentMode, latestUserText string) (*ConversationFile, error) {
	if stream == nil {
		return nil, fmt.Errorf("active stream is required")
	}
	if service == nil || service.compiler == nil {
		return conversation, nil
	}
	contexts, err := service.compiler.DerivePromptContexts(conversation, mode, latestUserText)
	if err != nil {
		return nil, err
	}
	if len(contexts) == 0 {
		return conversation, nil
	}
	stream.mu.Lock()
	turnSeq := stream.TurnSeq
	stream.mu.Unlock()
	if turnSeq <= 0 {
		return conversation, nil
	}
	entries := make([]HistoryEntry, 0, len(contexts))
	for _, context := range contexts {
		context = normalizePromptContextMessage(context)
		if !isReplayablePromptContext(context) {
			continue
		}
		entries = append(entries, newPromptContextEntry(turnSeq, requestID, context))
	}
	if len(entries) == 0 {
		return conversation, nil
	}
	if _, err := service.appendConversationEntries(stream, conversationID, entries); err != nil {
		return nil, err
	}
	conversation, _, _, err = service.snapshotCheckpointConversation(stream)
	return conversation, err
}

func (service *Service) runProviderStream(stream *ActiveStream, token uint64, ctx context.Context, request ProviderRequest, onProviderAccepted func()) {
	var accepted sync.Once
	markProviderAccepted := func() {
		if onProviderAccepted != nil {
			accepted.Do(onProviderAccepted)
		}
	}
	err := service.provider.StartStream(ctx, request, func(event modeladapter.ModelEvent) error {
		markProviderAccepted()
		command := streamCommand{
			Kind: streamCommandProviderEvent,
			Provider: &streamProviderEvent{
				Token: token,
				Event: event,
			},
		}
		if providerEventCanQueueAsync(event) {
			return service.postStreamCommandAsync(stream, command)
		}
		return service.postStreamCommandWait(stream, command)
	})
	if err == nil {
		markProviderAccepted()
	}
	if postErr := service.postStreamCommandWait(stream, streamCommand{
		Kind: streamCommandProviderEvent,
		Provider: &streamProviderEvent{
			Token: token,
			Done:  true,
			Err:   err,
		},
	}); postErr != nil && !errors.Is(postErr, errProviderLoopInterrupted) {
		service.debug.LogProvider(context.Background(), request.RequestID, request.ConversationID, "provider_completion_post_error", map[string]any{
			"model_call_id":  strings.TrimSpace(request.ModelCallID),
			"provider_token": token,
			"error":          postErr.Error(),
		})
		logger.Errorf(
			"forwarder provider completion post failed request_id=%s model_call_id=%s provider_token=%d err=%v",
			strings.TrimSpace(request.RequestID),
			strings.TrimSpace(request.ModelCallID),
			token,
			postErr,
		)
		if terminalErr := service.failStreamIfNonTerminal(stream, "unknown", postErr); terminalErr != nil {
			logger.Errorf("forwarder provider event post failure terminalization failed request_id=%s err=%v", strings.TrimSpace(stream.RequestID), terminalErr)
		}
	}
	if err != nil {
		service.debug.LogProvider(context.Background(), request.RequestID, request.ConversationID, "provider_stream_finished", map[string]any{
			"model_call_id":  strings.TrimSpace(request.ModelCallID),
			"provider_token": token,
			"error":          err.Error(),
		})
		return
	}
	service.debug.LogProvider(context.Background(), request.RequestID, request.ConversationID, "provider_stream_finished", map[string]any{
		"model_call_id":  strings.TrimSpace(request.ModelCallID),
		"provider_token": token,
	})
}

// providerEventCanQueueAsync 仅放宽高频且无状态收口职责的可见增量。邮箱仍为
// 固定容量，满时 postStreamCommandAsync 会阻塞上游读取以形成背压；回合完成、
// 工具调用和错误则继续同步等待，作为前序增量已被 actor 消费的顺序栅栏。
func providerEventCanQueueAsync(event modeladapter.ModelEvent) bool {
	switch event.Kind {
	case modeladapter.ModelEventKindTextDelta, modeladapter.ModelEventKindThinkingDelta:
		return true
	default:
		return false
	}
}

const defaultResolvedContextWindowTokens = 200_000 // align with config.defaultChannelContextWindowTokens

// largeWindowCompactionCanonicalSyncThreshold：仅在大上下文窗口下，将 canonical 编译估算
// 同步进 TokenDetailsUsedTokens 供自动压缩决策。小窗口 E2E/单测仍走 projected 压力路径。
const largeWindowCompactionCanonicalSyncThreshold = uint32(500_000)

func (service *Service) syncCanonicalPressureForLargeWindows(conversation *ConversationFile, canonicalCompiled CompiledConversation) {
	if conversation == nil || conversation.TokenDetailsMaxTokens < largeWindowCompactionCanonicalSyncThreshold {
		return
	}
	canonicalEstimate := estimateCompiledPromptTokens(canonicalCompiled)
	if canonicalEstimate > int64(conversation.TokenDetailsUsedTokens) {
		conversation.TokenDetailsUsedTokens = clampInt64ToUint32(canonicalEstimate)
	}
}

func (service *Service) resolveContextWindowTokens(modelID string) uint32 {
	if service == nil || service.resolver == nil {
		return defaultResolvedContextWindowTokens
	}
	channel, err := service.resolver.SelectChannelForModel(context.Background(), strings.TrimSpace(modelID))
	if err != nil || channel == nil || channel.ContextWindowTokens <= 0 {
		return defaultResolvedContextWindowTokens
	}
	return clampInt64ToUint32(int64(channel.ContextWindowTokens))
}

func (service *Service) syncConversationContextWindowTokens(stream *ActiveStream, conversationID string, conversation *ConversationFile) (*ConversationFile, error) {
	if stream == nil || conversation == nil {
		return conversation, nil
	}
	stream.mu.Lock()
	modelID := stream.ModelID
	stream.mu.Unlock()
	target := service.resolveContextWindowTokens(modelID)
	if target == 0 || conversation.TokenDetailsMaxTokens == target {
		return conversation, nil
	}
	return service.updateConversationMetaAndCheckpoint(stream, conversationID, func(item *ConversationFile) error {
		if item == nil {
			return nil
		}
		item.TokenDetailsMaxTokens = target
		return nil
	})
}

// userMessageText 返回用户消息中的纯文本。
func userMessageText(message *agentv1.UserMessage) string {
	if message == nil {
		return ""
	}
	return strings.TrimSpace(message.GetText())
}

func currentProviderPass(stream *ActiveStream) int {
	if stream == nil {
		return 0
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.ProviderPassCount
}

func currentStreamMode(stream *ActiveStream) agentv1.AgentMode {
	if stream == nil {
		return agentv1.AgentMode_AGENT_MODE_AGENT
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if normalized, err := validateSupportedActiveMode(stream.Mode); err == nil {
		return normalized
	}
	return stream.Mode
}
