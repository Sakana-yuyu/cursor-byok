// service_tool.go 承载模型工具调用下发、exec 进度合并与 tool result 落盘。
package forwarder

import (
	"context"
	"cursor/gen/agentv1"
	execbridge "cursor/internal/backend/agent/bridge/exec"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
	"cursor/internal/logger"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
)

// handleToolInvocation 把模型产生的工具意图转成 exec/interaction 请求并下发给客户端。
func (service *Service) handleToolInvocation(stream *ActiveStream, invocation runtimecore.ToolInvocation) error {
	if err := providerLoopInterruptErr(nil, stream, invocation.ModelCallID); err != nil {
		return err
	}
	// Shell 指纹熔断：本轮内同一确定性拒绝达到阈值后，Shell 对剩余轮次不可用。
	if strings.TrimSpace(invocation.ToolName) == "Shell" {
		circuit := currentTurnShellCircuit(stream)
		if circuit.Open {
			stream.mu.Lock()
			stream.ToolInvocationCount++
			stream.UpdatedAt = time.Now().UTC()
			stream.mu.Unlock()
			if err := service.recordShellCircuitLocalBlock(stream, invocation, circuit); err != nil {
				return err
			}
			cause := fmt.Errorf("Shell is unavailable for the rest of this turn after a %s rejection; use a non-Shell tool or finish with the blocker", circuit.RejectionClass)
			if err := service.completePreDispatchToolError(stream, invocation, nil, false, false, cause); err != nil {
				return err
			}
			if circuit.LocalBlocks+1 >= shellCircuitLocalBlockLimit {
				return providerTerminalError{cause: fmt.Errorf("Shell circuit stopped the provider loop after %d local blocks", circuit.LocalBlocks+1)}
			}
			return nil
		}
	}
	invocation = service.rewriteDirectMCPToolInvocation(stream, invocation)
	invocation = service.normalizeCallMCPToolInvocation(stream, invocation)
	trimmedToolName := strings.TrimSpace(invocation.ToolName)
	signature := delegation.NormalizeToolSignature(trimmedToolName, invocation.ArgsJSON)
	stream.mu.Lock()
	mode := stream.Mode
	providerPass := stream.ProviderPassCount
	subagentTypeName := ""
	if stream.CheckpointConversation != nil {
		subagentTypeName = strings.TrimSpace(stream.CheckpointConversation.SubagentTypeName)
	}
	stream.ToolInvocationCount++
	stream.UpdatedAt = time.Now().UTC()
	// B1 doom loop 检测：以（工具名+规范化参数）签名对连续相同调用计数。
	// 签名变化即重置；达到硬阈值时中断本轮，达到警告阈值时注入提示。
	// 轮询型工具（SubagentAwait/AwaitShell）按设计就会以相同参数反复调用，
	// 不参与计数，否则会误杀正在等待长任务子代理的正常轮询。
	doomLoopCount := 0
	countsDoomLoop := !isPollingAwaitTool(trimmedToolName)
	if countsDoomLoop {
		if stream.lastDoomLoopSignature != signature {
			stream.doomLoopCounts = map[string]int{}
			stream.lastDoomLoopSignature = signature
		}
		if stream.doomLoopCounts == nil {
			stream.doomLoopCounts = map[string]int{}
		}
		stream.doomLoopCounts[signature]++
		doomLoopCount = stream.doomLoopCounts[signature]
	}
	stream.mu.Unlock()
	if doomLoopCount >= doomLoopHardLimit {
		err := service.completePreDispatchToolError(stream, invocation, nil, false, false,
			fmt.Errorf("检测到 %s 以相同参数连续调用 %d 次，已中断本轮：请先阅读之前的工具结果并改变策略", trimmedToolName, doomLoopCount))
		if err != nil {
			return err
		}
		// 相同调用达到硬上限说明模型/缓存已进入确定性死循环，继续 resume 只会
		// 反复命中同一响应（本地响应缓存回放时尤其如此）。标记终结工具调用，
		// 让本轮在 provider pass 收口时直接结束而不是再次 resume。
		markProviderTerminalToolInvocation(stream)
		return nil
	}
	if doomLoopCount == doomLoopThreshold {
		stream.mu.Lock()
		stream.pendingDoomLoopNotice = fmt.Sprintf("[检测到 %s 以相同参数连续调用 %d 次，请先阅读上次工具结果并改变策略]", trimmedToolName, doomLoopCount)
		stream.mu.Unlock()
	}
	delegationEnabled := false
	delegationSupervision := false
	delegationGroups := 0
	if service != nil && service.multitaskDelegation != nil {
		config := service.multitaskDelegation.runtimeConfig()
		delegationEnabled = config.Enabled
		delegationSupervision = config.SupervisionEnabled
		delegationGroups = len(config.Groups)
	}
	logger.Infof("forwarder tool invocation request_id=%s conversation_id=%s mode=%s tool=%s call_id=%s model_call_id=%s provider_pass=%d multitask_coordinator=%t delegation_enabled=%t supervision_enabled=%t delegation_groups=%d",
		strings.TrimSpace(stream.RequestID), strings.TrimSpace(stream.ConversationID), mode.String(), trimmedToolName, strings.TrimSpace(invocation.CallID), strings.TrimSpace(invocation.ModelCallID), providerPass, service != nil && service.multitaskDelegation != nil, delegationEnabled, delegationSupervision, delegationGroups)
	if service != nil && service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "tool_invocation_routed", map[string]any{
			"mode":                   mode.String(),
			"tool_name":              trimmedToolName,
			"call_id":                strings.TrimSpace(invocation.CallID),
			"model_call_id":          strings.TrimSpace(invocation.ModelCallID),
			"provider_pass":          providerPass,
			"multitask_coordinator":  service != nil && service.multitaskDelegation != nil,
			"delegation_enabled":     delegationEnabled,
			"supervision_enabled":    delegationSupervision,
			"delegation_group_count": delegationGroups,
		})
	}
	if !isToolAllowedInMode(mode, subagentTypeName, trimmedToolName) {
		return service.completePreDispatchToolError(stream, invocation, nil, false, false, fmt.Errorf("tool invocation is not enabled in mode %s: %s", mode.String(), invocation.ToolName))
	}
	// inspect 子代理（Task 子会话 + PLAN 模式）的 Shell 调用在服务端强制只读白名单，
	// 校验失败直接拒绝，不依赖提示词描述；校验通过时注入 --no-pager 等保护参数。
	if trimmedToolName == "Shell" && isChildConversationSubagentTypeName(subagentTypeName) && normalizeMode(mode) == agentv1.AgentMode_AGENT_MODE_PLAN {
		rewritten, policyErr := service.enforceReadonlyShellPolicy(stream, invocation)
		if policyErr != nil {
			opened, recordErr := service.recordPreDispatchShellRejection(stream, invocation, policyErr)
			if recordErr != nil {
				return recordErr
			}
			if opened {
				// 第二次同指纹失败：附上明确纠正指引并开路，后续同类调用被 circuit.Open 分支拦截。
				policyErr = fmt.Errorf("%s. Do not retry this Shell command this turn — the same deterministic validation error will repeat and Shell is now blocked; use Read/Grep/Glob instead or report the blocker", policyErr.Error())
			}
			return service.completePreDispatchToolError(stream, invocation, nil, false, false, policyErr)
		}
		invocation = rewritten
	}
	var err error
	invocation, err = service.sanitizeCreatePlanInvocationForCurrentPlan(stream, invocation)
	if err != nil {
		if cause, ok := recoverableToolInvocationCause(err); ok {
			return service.completePreDispatchToolError(stream, invocation, nil, false, false, cause)
		}
		return err
	}
	if isPatchEditToolName(trimmedToolName) {
		if err := service.handlePatchEditToolInvocation(stream, invocation); err != nil {
			if cause, ok := recoverableToolInvocationCause(err); ok {
				return service.completePreDispatchToolError(stream, invocation, nil, false, false, cause)
			}
			return err
		}
		return nil
	}
	if trimmedToolName == "Write" {
		if err := service.handleWriteToolInvocation(stream, invocation); err != nil {
			if cause, ok := recoverableToolInvocationCause(err); ok {
				return service.completePreDispatchToolError(stream, invocation, nil, false, false, cause)
			}
			return err
		}
		return nil
	}
	isExecInvocation := isExecTool(trimmedToolName)
	isInteractionInvocation := isInteractionTool(trimmedToolName)
	isLocalStateInvocation := isLocalStateTool(trimmedToolName)
	isImmediateNativeInvocation := isImmediateNativeTool(trimmedToolName)
	if !isExecInvocation && !isInteractionInvocation && !isLocalStateInvocation && !isImmediateNativeInvocation {
		available := ""
		if service.toolCatalog != nil {
			if _, names, loadErr := service.toolCatalog.Load(mode, subagentTypeName); loadErr == nil && len(names) > 0 {
				available = fmt.Sprintf("（可用工具：%s）", strings.Join(names, ", "))
			}
		}
		err := service.completePreDispatchToolError(stream, invocation, nil, false, false, fmt.Errorf("unsupported tool invocation: %s%s", invocation.ToolName, available))
		if err != nil {
			return err
		}
		// unsupported tool 是确定性不可恢复错误：模型若再次尝试同一工具（本地缓存
		// 回放时必然如此）会形成无限 resume 死循环。标记终结工具调用，让本轮在
		// provider pass 收口时直接结束，把错误结果留给用户，而不是空转数百轮。
		markProviderTerminalToolInvocation(stream)
		return nil
	}
	var subagentOverrides map[string]runtimecore.SubagentModelOverrideSelection
	if isExecInvocation {
		subagentOverrides = cloneSubagentModelOverrides(stream.SubagentModelOverrides)
		if resolutionPayload := taskSubagentModelResolutionPayload(invocation, stream.ModelID, subagentOverrides); resolutionPayload != nil {
			service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "subagent_model_override_resolved", resolutionPayload)
		}
		invocation = rewriteTaskInvocationModelForDisplay(invocation, stream.ModelID, subagentOverrides)
	}
	bufferExecDispatch := isExecInvocation && shouldBufferExecDispatch(invocation.ToolName)
	suppressStartedToolCall := shouldSuppressStartedToolCallAfterPartial(stream, trimmedToolName, invocation.CallID)
	startedToolCall := buildStartedToolCall(invocation)
	// Task 工具调用用父代理实际模型名覆盖 args 中的 model 别名（如 "fast"），
	// 让 Cursor 客户端 Task 卡片显示真实模型名（如 gpt-5.3-codex-spark）而非别名。
	if trimmedToolName == "Task" && startedToolCall != nil {
		ensureTaskToolCallModel(startedToolCall, stream.ModelName)
	}
	startedEmitted := suppressStartedToolCall
	delegatedTaskStarted := false
	nativeTaskOpened := false
	var nativeTaskServerMessage *agentv1.AgentServerMessage
	var nativeTaskPending runtimecore.PendingExec
	ensureLoopActive := func() error {
		return providerLoopInterruptErr(nil, stream, invocation.ModelCallID)
	}
	if autoStarted, autoErr := service.maybeStartAutomaticMultitaskDelegation(stream, invocation); autoErr != nil {
		logger.Infof("forwarder automatic multitask delegation ignored request_id=%s tool=%s reason=%v", strings.TrimSpace(stream.RequestID), trimmedToolName, autoErr)
	} else if autoStarted {
		logger.Infof("forwarder automatic multitask delegation started request_id=%s trigger_tool=%s", strings.TrimSpace(stream.RequestID), trimmedToolName)
	}
	if startedToolCall != nil {
		if err := ensureLoopActive(); err != nil {
			return err
		}
		toolCallPayload, err := protojson.Marshal(startedToolCall)
		if err != nil {
			return err
		}
		_, err = service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
			newToolCallEntryWithProviderMetadata(stream.TurnSeq, stream.RequestID, invocation.CallID, invocation.ToolName, invocation.ReasoningContent, invocation.ReasoningSignature, invocation.ReasoningSignatureSource, invocation.ReasoningProviderItemID, invocation.ReasoningProviderStatus, invocation.ReasoningProviderSummary, invocation.ProviderItemID, invocation.ProviderCallID, invocation.ProviderStatus, toolCallPayload),
		})
		if err != nil {
			return err
		}
	}
	// Cursor creates the Task bubble as soon as tool_call_started arrives. For a
	// locally executed Task, register the aggregate and publish its RUNNING
	// checkpoint first so that the bubble can be associated with a live
	// subagent run instead of briefly falling back to the client's "Stopped"
	// label. A second checkpoint immediately after the bubble is required
	// because the first one predates the client-side tool bubble.
	if trimmedToolName == "Task" && !bufferExecDispatch && !suppressStartedToolCall {
		if err := ensureLoopActive(); err != nil {
			return err
		}
		delegatedTaskStarted, err = service.tryStartDelegatedTask(stream, invocation)
		if err != nil {
			if errors.Is(err, errProviderLoopInterrupted) {
				return err
			}
			return service.completePreDispatchToolError(stream, invocation, startedToolCall, startedToolCall != nil, startedEmitted, err)
		}
		if delegatedTaskStarted {
			logger.Infof("forwarder task dispatch order request_id=%s tool_call_id=%s order=aggregate_started_then_checkpoint_then_started", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID))
			if service.debug != nil {
				service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "task_dispatch_order", map[string]any{
					"tool_call_id": strings.TrimSpace(invocation.CallID),
					"order":        "aggregate_started_then_checkpoint_then_started",
				})
			}
		} else {
			// Direct Cursor Task executions must be registered before the client
			// sees tool_call_started. The installed Cursor client creates the Task
			// bubble immediately and labels it Stopped when no RUNNING subagent
			// checkpoint exists at that moment.
			nativeTaskServerMessage, nativeTaskPending, err = service.openNativeTaskExec(stream, invocation, subagentOverrides)
			if err != nil {
				if errors.Is(err, errProviderLoopInterrupted) {
					return err
				}
				return service.completePreDispatchToolError(stream, invocation, startedToolCall, startedToolCall != nil, startedEmitted, err)
			}
			nativeTaskOpened = true
			logger.Infof("forwarder task dispatch order request_id=%s tool_call_id=%s order=native_exec_registered_then_checkpoint_then_started exec_id=%s", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID), strings.TrimSpace(nativeTaskPending.ExecID))
			if service.debug != nil {
				service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "task_dispatch_order", map[string]any{
					"tool_call_id": strings.TrimSpace(invocation.CallID),
					"exec_id":      strings.TrimSpace(nativeTaskPending.ExecID),
					"order":        "native_exec_registered_then_checkpoint_then_started",
				})
			}
		}
	}
	if !bufferExecDispatch && !suppressStartedToolCall {
		if err := ensureLoopActive(); err != nil {
			return err
		}
		if trimmedToolName == "Task" {
			logger.Infof("forwarder task tool_call_started publishing request_id=%s tool_call_id=%s model_call_id=%s agent_id=%s args_bytes=%d", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID), strings.TrimSpace(invocation.ModelCallID), delegationSubagentID(invocation.CallID), len(invocation.ArgsJSON))
		}
		if err := service.broker.Publish(stream.RequestID, StreamEvent{
			Message: buildToolCallStartedMessage(invocation.CallID, invocation.ModelCallID, startedToolCall),
		}); err != nil {
			if trimmedToolName == "Task" {
				logger.Errorf("forwarder task tool_call_started publish failed request_id=%s tool_call_id=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID), err)
			}
			return err
		}
		startedEmitted = true
		if trimmedToolName == "Task" && (delegatedTaskStarted || nativeTaskOpened) {
			if err := service.publishCheckpointForce(stream.RequestID, stream.ConversationID); err != nil {
				logger.Errorf("forwarder task post-start checkpoint failed request_id=%s tool_call_id=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID), err)
				return err
			}
			logger.Infof("forwarder task post-start checkpoint published request_id=%s tool_call_id=%s", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID))
		}
	}
	if isImmediateNativeInvocation {
		return service.handleImmediateNativeToolInvocation(stream, invocation)
	}
	if isLocalStateInvocation {
		return service.handleLocalStateToolInvocation(stream, invocation)
	}
	if isInteractionInvocation {
		if err := service.handleInteractionToolInvocation(stream, invocation); err != nil {
			if cause, ok := recoverableToolInvocationCause(err); ok {
				return service.completePreDispatchToolError(stream, invocation, startedToolCall, startedToolCall != nil, startedEmitted, cause)
			}
			return err
		}
		return nil
	}
	if isExecInvocation {
		// A Multitask Task handled by the local delegation aggregate is already
		// registered, checkpointed, and represented in Cursor's Task bubble above.
		// Do not fall through to OpenExec: doing so registers a second native
		// subagent for the same tool call and can leave the foreground turn stuck
		// in Stopped after the native watchdog fires.
		if trimmedToolName == "Task" && delegatedTaskStarted {
			logger.Infof("forwarder task local aggregate dispatch complete request_id=%s tool_call_id=%s", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID))
			// 发送首个 task tool_call_delta：Cursor 客户端在 delta 到达时创建
			// 子代理 composer（subagentHandle），Task 卡片才从 loading/Stopped
			// 变为运行中，并开始流式展示内嵌的 thinkingDelta 进度。
			if err := service.broker.Publish(stream.RequestID, StreamEvent{
				Message: buildTaskToolCallDeltaMessage(invocation.CallID, invocation.ModelCallID, buildThinkingDeltaInteraction(delegationStartupDeltaText(invocation.ArgsJSON), agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT)),
			}); err != nil {
				logger.Errorf("forwarder task tool_call_delta initial publish failed request_id=%s tool_call_id=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID), err)
			}
			return nil
		}
		if trimmedToolName == "Task" && !delegatedTaskStarted && !nativeTaskOpened {
			started, err := service.tryStartDelegatedTask(stream, invocation)
			if err != nil {
				if errors.Is(err, errProviderLoopInterrupted) {
					return err
				}
				return service.completePreDispatchToolError(stream, invocation, startedToolCall, startedToolCall != nil, startedEmitted, err)
			}
			if started {
				return nil
			}
		}
		serverMessage := nativeTaskServerMessage
		pendingExec := nativeTaskPending
		if !nativeTaskOpened {
			serverMessage, pendingExec, err = service.execBridge.OpenExec(buildExecOpenContextForStream(stream, subagentOverrides), invocation)
			if err != nil {
				return service.completePreDispatchToolError(stream, invocation, startedToolCall, startedToolCall != nil, startedEmitted, err)
			}
			pendingExec.ModelCallID = invocation.ModelCallID
			pendingExec.ReasoningContent = invocation.ReasoningContent
			pendingExec.ReasoningSignature = invocation.ReasoningSignature
			pendingExec.ReasoningSignatureSource = invocation.ReasoningSignatureSource
			pendingExec = initializePendingExecForTracking(pendingExec)
			stream.mu.Lock()
			pendingExec.ProviderPass = stream.ProviderPassCount
			stream.PendingExecs[pendingExec.ExecID] = pendingExec
			stream.mu.Unlock()
			if strings.TrimSpace(pendingExec.ExecKind) == "subagent" {
				if !service.registerNativeDelegation(stream, pendingExec, serverMessage) {
					// 并发槽位已满：放弃派发，清理 pendingExec 并把并发上限错误
					// 交还给模型，阻止继续批量启动子代理。
					stream.mu.Lock()
					delete(stream.PendingExecs, pendingExec.ExecID)
					stream.mu.Unlock()
					return service.completePreDispatchToolError(stream, invocation, startedToolCall, startedToolCall != nil, startedEmitted, errNativeDelegationConcurrencyLimit)
				}
			}
			service.scheduleShellForegroundRecovery(stream.RequestID, pendingExec)
			service.scheduleExecWatchdog(stream.RequestID, pendingExec)
		}
		removePendingExec := func() {
			stream.mu.Lock()
			delete(stream.PendingExecs, pendingExec.ExecID)
			stream.mu.Unlock()
			if strings.TrimSpace(pendingExec.ExecKind) == "subagent" {
				status := delegation.TaskFailed
				progress := "Cursor 子代理派发失败"
				if !streamStillActive(stream) {
					status = delegation.TaskCanceled
					progress = "Cursor 子代理已取消"
				}
				service.updateNativeDelegationStatus(pendingExec.ExecID, status, progress, progress)
			}
		}
		if err := ensureLoopActive(); err != nil {
			removePendingExec()
			return err
		}
		if bufferExecDispatch {
			if err := ensureLoopActive(); err != nil {
				removePendingExec()
				return err
			}
			if err := service.broker.Publish(stream.RequestID, StreamEvent{Message: serverMessage}); err != nil {
				removePendingExec()
				return err
			}
			if err := ensureLoopActive(); err != nil {
				removePendingExec()
				return err
			}
			if err := service.broker.Publish(stream.RequestID, StreamEvent{
				Message: buildToolCallStartedMessage(invocation.CallID, invocation.ModelCallID, startedToolCall),
			}); err != nil {
				removePendingExec()
				return err
			}
			startedEmitted = true
			service.recordExecDispatchMetadata(stream, pendingExec, true, startedEmitted, "exec_then_started_then_checkpoint")
			if err := ensureLoopActive(); err != nil {
				removePendingExec()
				return err
			}
			if err := service.publishCheckpoint(stream.RequestID, stream.ConversationID); err != nil {
				removePendingExec()
				return err
			}
			return nil
		}
		if err := ensureLoopActive(); err != nil {
			removePendingExec()
			return err
		}
		if err := service.publishCheckpoint(stream.RequestID, stream.ConversationID); err != nil {
			removePendingExec()
			return err
		}
		if err := ensureLoopActive(); err != nil {
			removePendingExec()
			return err
		}
		if err := service.broker.Publish(stream.RequestID, StreamEvent{Message: serverMessage}); err != nil {
			removePendingExec()
			return err
		}
		service.recordExecDispatchMetadata(stream, pendingExec, false, startedEmitted, "started_then_checkpoint_then_exec")
		return nil
	}
	return nil
}

// openNativeTaskExec registers a direct Cursor Task before tool_call_started is
// published. Cursor creates the Task bubble on that event and immediately
// falls back to Stopped when its checkpoint has no RUNNING subagent entry.
func (service *Service) openNativeTaskExec(stream *ActiveStream, invocation runtimecore.ToolInvocation, subagentOverrides map[string]runtimecore.SubagentModelOverrideSelection) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	if service == nil || stream == nil || service.execBridge == nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("cursor exec bridge is unavailable")
	}
	serverMessage, pendingExec, err := service.execBridge.OpenExec(buildExecOpenContextForStream(stream, subagentOverrides), invocation)
	if err != nil {
		return nil, runtimecore.PendingExec{}, err
	}
	pendingExec.ModelCallID = invocation.ModelCallID
	pendingExec.ReasoningContent = invocation.ReasoningContent
	pendingExec.ReasoningSignature = invocation.ReasoningSignature
	pendingExec.ReasoningSignatureSource = invocation.ReasoningSignatureSource
	pendingExec = initializePendingExecForTracking(pendingExec)
	stream.mu.Lock()
	pendingExec.ProviderPass = stream.ProviderPassCount
	stream.PendingExecs[pendingExec.ExecID] = pendingExec
	stream.mu.Unlock()
	if strings.TrimSpace(pendingExec.ExecKind) == "subagent" {
		if !service.registerNativeDelegation(stream, pendingExec, serverMessage) {
			// 并发槽位已满：清理已登记的 pendingExec 并返回错误，由调用方经
			// completePreDispatchToolError 把并发上限错误交还给模型。
			stream.mu.Lock()
			delete(stream.PendingExecs, pendingExec.ExecID)
			stream.mu.Unlock()
			return nil, runtimecore.PendingExec{}, errNativeDelegationConcurrencyLimit
		}
	}
	if strings.TrimSpace(pendingExec.ExecKind) == "subagent" {
		service.scheduleShellForegroundRecovery(stream.RequestID, pendingExec)
		service.scheduleExecWatchdog(stream.RequestID, pendingExec)
		// ComputerUse 在 BYOK 下依赖客户端回传，本地执行器接管（Windows），
		// 避免调用挂起。仍发送 ExecServerMessage 兼容官方客户端，本地结果通过
		// dispatchInboundIntent 注入（与客户端回传语义等价、由 stream actor 串行消费）。
		service.maybeDispatchLocalComputerUse(stream.RequestID, pendingExec, pendingExec.ArgsJSON)
	}
	logger.Infof("forwarder native task pre-start registered request_id=%s conversation_id=%s tool_call_id=%s exec_id=%s exec_kind=%s message_id=%d", strings.TrimSpace(stream.RequestID), strings.TrimSpace(stream.ConversationID), strings.TrimSpace(pendingExec.ToolCallID), strings.TrimSpace(pendingExec.ExecID), strings.TrimSpace(pendingExec.ExecKind), pendingExec.MessageID)
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "native_task_prestart_registered", map[string]any{
			"tool_call_id": strings.TrimSpace(pendingExec.ToolCallID),
			"exec_id":      strings.TrimSpace(pendingExec.ExecID),
			"exec_kind":    strings.TrimSpace(pendingExec.ExecKind),
			"message_id":   pendingExec.MessageID,
		})
	}
	if err := service.publishCheckpointForce(stream.RequestID, stream.ConversationID); err != nil {
		stream.mu.Lock()
		delete(stream.PendingExecs, pendingExec.ExecID)
		stream.mu.Unlock()
		service.updateNativeDelegationStatus(pendingExec.ExecID, delegation.TaskFailed, "Cursor 子代理启动状态同步失败", err.Error())
		return nil, runtimecore.PendingExec{}, err
	}
	logger.Infof("forwarder native task pre-start checkpoint published request_id=%s tool_call_id=%s exec_id=%s", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pendingExec.ToolCallID), strings.TrimSpace(pendingExec.ExecID))
	return serverMessage, pendingExec, nil
}

func shouldSuppressStartedToolCallAfterPartial(stream *ActiveStream, toolName string, callID string) bool {
	if stream == nil {
		return false
	}
	switch strings.TrimSpace(toolName) {
	case "CreatePlan", "GenerateImage":
	default:
		return false
	}
	trimmedCallID := strings.TrimSpace(callID)
	if trimmedCallID == "" {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.PartialToolCallIDs == nil {
		return false
	}
	_, ok := stream.PartialToolCallIDs[trimmedCallID]
	return ok
}

func (service *Service) recordExecDispatchMetadata(stream *ActiveStream, pending runtimecore.PendingExec, buffered bool, startedEmitted bool, dispatchOrder string) {
	if service == nil || stream == nil {
		return
	}
	toolName := strings.TrimSpace(deriveToolNameFromPendingExec(pending))
	if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
		newMetadataEntry(stream.TurnSeq, stream.RequestID, "exec_dispatch", map[string]any{
			"tool_call_id":    pending.ToolCallID,
			"message_id":      pending.MessageID,
			"exec_id":         pending.ExecID,
			"exec_kind":       pending.ExecKind,
			"provider_pass":   pending.ProviderPass,
			"tool_name":       toolName,
			"model_call_id":   pending.ModelCallID,
			"buffered":        buffered,
			"started_emitted": startedEmitted,
			"dispatch_order":  strings.TrimSpace(dispatchOrder),
			"opened_at":       pending.OpenedAt,
		}),
	}); err != nil {
		logger.Errorf("forwarder exec dispatch metadata failed request_id=%s tool_call_id=%s message_id=%d err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ToolCallID), pending.MessageID, err)
	}
}

// shouldBufferExecDispatch 把只需要完整参数的快工具改成“先发 exec 请求，再发 started，再发 checkpoint”，
// 避免客户端在参数仍未稳定前过早起计时，同时保留显式的工具开始信号。
func shouldBufferExecDispatch(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "Read", "Grep", "Glob":
		return true
	default:
		return false
	}
}

// appendToolResult 把已完成的工具结果追加到 history，供后续 prompt replay 使用。
//
// reasoning 在已提交 history 中应挂在 assistant_text / tool_call 上。
// tool_result 保存一份 reasoning_content 兜底，replay 只会在缺失 tool_call entry
// 且 reasoning 可回放时用它重建 assistant tool_use，不会把 thinking 复制到工具消息上。
func (service *Service) appendToolResult(stream *ActiveStream, toolCallID string, toolName string, argsJSON []byte, resultText string, reasoningContent string, toolCall *agentv1.ToolCall) error {
	if stream == nil {
		return nil
	}
	// B1 doom loop 提示注入：取走并清空待注入提示，非空时追加到工具结果末尾。
	stream.mu.Lock()
	notice := stream.pendingDoomLoopNotice
	stream.pendingDoomLoopNotice = ""
	stream.mu.Unlock()
	if notice != "" && strings.TrimSpace(resultText) != "" {
		resultText = strings.TrimSpace(resultText) + "\n" + notice
	} else if notice != "" {
		resultText = notice
	}
	var payload json.RawMessage
	if toolCall != nil {
		encoded, err := protojson.Marshal(toolCall)
		if err != nil {
			return err
		}
		payload = encoded
	}
	_, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
		newToolResultEntry(stream.TurnSeq, stream.RequestID, toolCallID, toolName, string(argsJSON), resultText, reasoningContent, payload),
	})
	return err
}

func (service *Service) publishToolCallCompleted(requestID string, toolCallID string, modelCallID string, toolCall *agentv1.ToolCall) error {
	if strings.TrimSpace(requestID) == "" || strings.TrimSpace(toolCallID) == "" {
		return nil
	}
	task := toolCall.GetTaskToolCall()
	resultKind := "none"
	agentID := ""
	stepCount := 0
	if task != nil && task.GetResult() != nil {
		switch task.GetResult().GetResult().(type) {
		case *agentv1.TaskResult_Success:
			resultKind = "success"
			if success := task.GetResult().GetSuccess(); success != nil {
				agentID = success.GetAgentId()
				stepCount = len(success.GetConversationSteps())
			}
		case *agentv1.TaskResult_Error:
			resultKind = "error"
		}
	}
	logger.Infof("forwarder tool_call_completed publishing request_id=%s tool_call_id=%s model_call_id=%s task_result=%s agent_id=%s conversation_steps=%d", strings.TrimSpace(requestID), strings.TrimSpace(toolCallID), strings.TrimSpace(modelCallID), resultKind, strings.TrimSpace(agentID), stepCount)
	err := service.broker.Publish(requestID, StreamEvent{
		Message: buildToolCallCompletedMessage(toolCallID, modelCallID, toolCall),
	})
	if err != nil {
		logger.Errorf("forwarder tool_call_completed publish failed request_id=%s tool_call_id=%s model_call_id=%s err=%v", strings.TrimSpace(requestID), strings.TrimSpace(toolCallID), strings.TrimSpace(modelCallID), err)
	}
	return err
}

func (service *Service) applyExecProgress(stream *ActiveStream, pending runtimecore.PendingExec, message *agentv1.ExecClientMessage) runtimecore.PendingExec {
	if stream == nil || message == nil || strings.TrimSpace(pending.ExecKind) != "shell" {
		return pending
	}
	shellStream := message.GetShellStream()
	if shellStream == nil {
		return pending
	}

	stream.mu.Lock()
	defer stream.mu.Unlock()
	current, ok := stream.PendingExecs[pending.ExecID]
	if !ok {
		return pending
	}
	now := time.Now().UTC()
	switch event := shellStream.GetEvent().(type) {
	case *agentv1.ShellStream_Stdout:
		if current.FirstChunkAt.IsZero() {
			current.FirstChunkAt = now
		}
		current.ChunkCount++
		current.StreamState = "streaming"
		current.LastShellActivityAt = now
		current.StdoutBuffer += execbridge.DecodeShellStdout(event.Stdout)
	case *agentv1.ShellStream_Stderr:
		if current.FirstChunkAt.IsZero() {
			current.FirstChunkAt = now
		}
		current.ChunkCount++
		current.StreamState = "streaming"
		current.LastShellActivityAt = now
		current.StderrBuffer += event.Stderr.GetData()
	case *agentv1.ShellStream_Start:
		if current.FirstChunkAt.IsZero() {
			current.FirstChunkAt = now
		}
		current.StreamState = "started"
		current.LastShellActivityAt = now
	case *agentv1.ShellStream_Backgrounded:
		current.StreamState = "backgrounded"
		current.LastShellActivityAt = now
	case *agentv1.ShellStream_Exit:
		current.StreamState = "exited"
		current.LastShellActivityAt = now
	case *agentv1.ShellStream_Rejected:
		current.StreamState = "rejected"
		current.LastShellActivityAt = now
	case *agentv1.ShellStream_PermissionDenied:
		current.StreamState = "permission_denied"
		current.LastShellActivityAt = now
	case *agentv1.ShellStream_HookContext:
		// hook 附加上下文出现在 shell 开始阶段，不改 StreamState（保留 opened/started 原值），
		// 仅续期 LastShellActivityAt，避免污染 observeShellStreamClose 的状态判断。
		current.LastShellActivityAt = now
	case *agentv1.ShellStream_SandboxUnsupported:
		current.StreamState = "sandbox_unsupported"
		current.LastShellActivityAt = now
	}
	stream.PendingExecs[pending.ExecID] = current
	return current
}

func (service *Service) applyExecControlProgress(stream *ActiveStream, pending runtimecore.PendingExec, message *agentv1.ExecClientControlMessage) runtimecore.PendingExec {
	if stream == nil || message == nil || strings.TrimSpace(pending.ExecKind) != "shell" {
		return pending
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	current, ok := stream.PendingExecs[pending.ExecID]
	if !ok {
		return pending
	}
	now := time.Now().UTC()
	switch message.GetMessage().(type) {
	case *agentv1.ExecClientControlMessage_Heartbeat:
		current.LastShellActivityAt = now
		current.LastShellHeartbeatAt = now
	case *agentv1.ExecClientControlMessage_StreamClose:
		current.LastShellActivityAt = now
	case *agentv1.ExecClientControlMessage_Throw:
		current.LastShellActivityAt = now
		current.StreamState = "throw"
	}
	stream.PendingExecs[pending.ExecID] = current
	return current
}
