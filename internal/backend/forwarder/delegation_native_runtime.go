package forwarder

import (
	"context"
	"errors"
	"strings"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
	"cursor/internal/logger"
	"cursor/internal/safego"
)

const nativeDelegationRetention = 10 * time.Minute
const nativeDelegationProgressInterval = 12 * time.Second

// defaultNativeDelegationProgressTimeout 是 native Cursor 子代理「无有效进展」看门狗的兜底阈值，
// 运行时由 config.nativeDelegationProgressTimeout 覆盖（默认 5 分钟，最小 1 分钟）。
const defaultNativeDelegationProgressTimeout = 5 * time.Minute

// defaultNativeDelegationConcurrencyLimit 是直接 Task→Cursor 原生子代理的全局并发上限。
// 这类执行不进入 Multitask scheduler，此前完全不受限制：模型每个 provider pass 都能
// 再批量启动子代理，数百个 Cursor 子会话同时运行会拖垮客户端与整机（曾出现 300+ 并发卡死）。
// 上限优先取 delegation.maxConcurrency（与 Multitask 一致），未配置时回退本默认值。
// 该值同时作为 nativeDelegationLimiter 的兜底上限。
const defaultNativeDelegationConcurrencyLimit = 4

// errNativeDelegationConcurrencyLimit 作为工具错误返回给模型，阻止继续批量派发子代理。
var errNativeDelegationConcurrencyLimit = errors.New("子代理并发数已达上限：当前同时运行的 Cursor 子代理过多。请等待现有子代理完成后再启动新任务，或将并行任务合并为串行执行；不要再继续批量启动子代理")

// nativeDelegationRuntime tracks direct Cursor Task -> subagent executions.
// These executions do not enter the Multitask scheduler, but they still need
// the same sanitized runtime view for the desktop delegation UI.
type nativeDelegationRuntime struct {
	ID                      string
	ParentRequestID         string
	ConversationID          string
	ToolCallID              string
	Description             string
	ModelID                 string
	ModelName               string
	WorkerRole              string
	Status                  delegation.TaskStatus
	Error                   string
	ProgressSummary         string
	ToolCallCount           int
	QueuedAt                time.Time
	StartedAt               time.Time
	FinishedAt              time.Time
	UpdatedAt               time.Time
	LastEffectiveProgressAt time.Time
	// ExecWatchdogDeferrals 记录 exec 看门狗因「子代理仍在客户端运行」而延期的次数，
	// 达到 nativeSubagentExecWatchdogMaxDeferrals 后不再延期，按超时收口。
	ExecWatchdogDeferrals int
	// holdsSlot 表示该任务已占用原生子代理并发信号量；terminal 时释放，防重复释放。
	holdsSlot bool
}

// registerNativeDelegation 登记一个直接 Task→Cursor 原生子代理，并占用全局并发槽位。
// 返回 false 表示已达并发上限，调用方必须放弃本次派发并把错误交还给模型，
// 不能继续向客户端发送 ExecServerMessage(subagent_args)。
func (service *Service) registerNativeDelegation(stream *ActiveStream, pending runtimecore.PendingExec, serverMessage *agentv1.AgentServerMessage) bool {
	if service == nil || stream == nil || strings.TrimSpace(pending.ExecKind) != "subagent" {
		return false
	}
	args, _ := runtimecore.DecodeArgsMap(pending.ArgsJSON)
	now := time.Now().UTC()
	modelID := strings.TrimSpace(runtimecore.ReadStringArg(args, "model", "model_id", "modelId"))
	modelName := ""
	workerRole := strings.TrimSpace(runtimecore.ReadStringArg(args, "subagent_type", "subagentType"))
	if serverMessage != nil {
		if execMessage := serverMessage.GetExecServerMessage(); execMessage != nil {
			if subagentArgs := execMessage.GetSubagentArgs(); subagentArgs != nil {
				modelID = firstNonEmpty(subagentArgs.GetModelId(), modelID)
				workerRole = firstNonEmpty(subagentArgs.GetSubagentType(), workerRole)
			}
		}
	}
	stream.mu.Lock()
	parentRequestID := strings.TrimSpace(stream.RequestID)
	if modelID == "" {
		modelID = strings.TrimSpace(stream.ModelID)
	}
	if modelName == "" {
		modelName = service.resolveDelegationTaskModelName(modelID, stream.ModelName)
	}
	stream.mu.Unlock()
	if strings.TrimSpace(pending.ExecID) == "" || parentRequestID == "" {
		return false
	}
	logger.Infof("forwarder native delegation identity request_id=%s exec_id=%s tool_call_id=%s model_id=%s model_name=%s worker_role=%s source=config_or_resolver",
		parentRequestID, strings.TrimSpace(pending.ExecID), strings.TrimSpace(pending.ToolCallID), strings.TrimSpace(modelID), strings.TrimSpace(modelName), strings.TrimSpace(workerRole))
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), parentRequestID, stream.ConversationID, "native_delegation_identity_resolved", map[string]any{
			"exec_id":      strings.TrimSpace(pending.ExecID),
			"tool_call_id": strings.TrimSpace(pending.ToolCallID),
			"model_id":     strings.TrimSpace(modelID),
			"model_name":   strings.TrimSpace(modelName),
			"worker_role":  strings.TrimSpace(workerRole),
		})
	}
	service.delegationRuntimeMu.Lock()
	if !service.acquireNativeDelegationSlotLocked() {
		service.delegationRuntimeMu.Unlock()
		logger.Errorf("forwarder native delegation rejected request_id=%s exec_id=%s reason=concurrency_limit", parentRequestID, strings.TrimSpace(pending.ExecID))
		if service.debug != nil {
			service.debug.LogRuntime(context.Background(), parentRequestID, stream.ConversationID, "native_delegation_rejected", map[string]any{
				"exec_id": strings.TrimSpace(pending.ExecID),
				"reason":  "concurrency_limit",
			})
		}
		return false
	}
	if service.nativeDelegations == nil {
		service.nativeDelegations = make(map[string]*nativeDelegationRuntime)
	}
	service.pruneNativeDelegationsLocked(now)
	description := firstNonEmpty(
		strings.TrimSpace(runtimecore.ReadStringArg(args, "description")),
		strings.TrimSpace(runtimecore.ReadStringArg(args, "prompt")),
	)
	service.nativeDelegations[pending.ExecID] = &nativeDelegationRuntime{
		ID:                      strings.TrimSpace(pending.ExecID),
		ParentRequestID:         parentRequestID,
		ConversationID:          strings.TrimSpace(stream.ConversationID),
		ToolCallID:              strings.TrimSpace(pending.ToolCallID),
		Description:             description,
		ModelID:                 modelID,
		ModelName:               modelName,
		WorkerRole:              workerRole,
		Status:                  delegation.TaskRunning,
		ProgressSummary:         nativeDelegationStartSummary(description, modelName, workerRole),
		QueuedAt:                firstNonZeroTime(pending.OpenedAt, now),
		StartedAt:               now,
		UpdatedAt:               now,
		LastEffectiveProgressAt: now,
		holdsSlot:               true,
	}
	service.delegationRuntimeMu.Unlock()
	service.publishNativeDelegationProgress(parentRequestID, pending.ExecID, nativeDelegationStartSummary(description, modelName, workerRole))
	service.keepNativeDelegationAlive(parentRequestID, pending.ExecID)
	service.watchNativeDelegationProgress(parentRequestID, pending.ExecID)
	return true
}

// resolveDelegationTaskModelName converts Cursor's runtime model identifier
// into the configured display name used by the desktop delegation cards.
// Direct Cursor Task executions bypass the Multitask worker builder, so this
// lookup must happen here as well.
func (service *Service) resolveDelegationTaskModelName(modelID string, fallback string) string {
	modelID = strings.TrimSpace(modelID)
	fallback = strings.TrimSpace(fallback)
	lookupID := modelID
	if canonical, _ := splitRuntimeThinkingEffortVariantString(modelID); canonical != "" {
		lookupID = canonical
	}
	if service != nil && service.delegationConfig != nil {
		config := delegation.NormalizeRuntimeConfig(service.delegationConfig.DelegationRuntimeConfig())
		if name := strings.TrimSpace(config.ModelNames[lookupID]); name != "" {
			return name
		}
	}
	if service != nil && service.resolver != nil && lookupID != "" {
		if channel, err := service.resolver.SelectChannelForModel(context.Background(), lookupID); err == nil && channel != nil {
			if name := strings.TrimSpace(channel.Name); name != "" {
				return name
			}
		}
	}
	return firstNonEmpty(fallback, modelID)
}

func nativeDelegationStartSummary(description, modelName, workerRole string) string {
	parts := make([]string, 0, 3)
	if description = strings.TrimSpace(description); description != "" {
		parts = append(parts, "任务："+description)
	}
	if modelName = strings.TrimSpace(modelName); modelName != "" {
		parts = append(parts, "模型："+modelName)
	}
	if workerRole = strings.TrimSpace(workerRole); workerRole != "" {
		parts = append(parts, "角色："+workerRole)
	}
	if len(parts) == 0 {
		return "工作摘要：委派子代理已启动，正在检查任务上下文并执行必要操作。"
	}
	return "工作摘要：" + strings.Join(parts, "；") + "。正在检查任务上下文并执行必要操作。"
}

func nativeDelegationRunningSummary(item *nativeDelegationRuntime) string {
	if item == nil {
		return "工作摘要：委派子代理仍在执行，正在继续检查和整理结果。"
	}
	if description := strings.TrimSpace(item.Description); description != "" {
		return "工作摘要：正在处理“" + description + "”，子代理仍在执行工具调用并整理结果。"
	}
	return "工作摘要：委派子代理仍在执行，正在继续检查和整理结果。"
}

func nativeDelegationTerminalSummary(item *nativeDelegationRuntime) string {
	if item == nil {
		return "工作摘要：委派子代理已结束。"
	}
	switch item.Status {
	case delegation.TaskCompleted:
		return "工作摘要：委派子代理已完成检查并返回结果。"
	case delegation.TaskCanceled:
		return "工作摘要：委派子代理已取消。"
	case delegation.TaskTimedOut:
		return "工作摘要：委派子代理执行超时，已停止等待。"
	case delegation.TaskFailed:
		return "工作摘要：委派子代理执行失败，已返回失败状态。"
	default:
		return nativeDelegationRunningSummary(item)
	}
}

func (service *Service) publishNativeDelegationProgress(requestID, execID, summary string) {
	if service == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(execID) == "" || strings.TrimSpace(summary) == "" {
		return
	}
	item, ok := service.nativeDelegationTask(execID)
	if !ok || strings.TrimSpace(item.ToolCallID) == "" {
		return
	}
	if err := service.broker.Publish(requestID, StreamEvent{
		Message: buildTaskToolCallDeltaMessage(
			item.ToolCallID,
			"",
			buildThinkingDeltaInteraction(summary, agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT),
		),
	}); err != nil {
		return
	}
}

// mirrorNativeChildInteraction forwards a direct Cursor child conversation's
// visible stream event into the owning Task bubble. The child's own request
// remains the source of truth; this is only the parent-side projection Cursor
// uses while the subagent window is open.
func (service *Service) mirrorNativeChildInteraction(child *ActiveStream, modelCallID string, interaction *agentv1.InteractionUpdate) {
	if service == nil || child == nil || interaction == nil {
		return
	}
	child.mu.Lock()
	conversation := child.CheckpointConversation
	childConversationID := strings.TrimSpace(child.ConversationID)
	child.mu.Unlock()
	if conversation == nil || strings.TrimSpace(conversation.SubagentTypeName) == "" {
		return
	}
	parentConversationID := strings.TrimSpace(conversation.ParentConversationID)
	parentToolCallID := strings.TrimSpace(conversation.ParentToolCallID)
	if childConversationID == "" || parentConversationID == "" || parentToolCallID == "" {
		return
	}

	service.delegationRuntimeMu.Lock()
	var parentRequestID string
	for _, item := range service.nativeDelegations {
		if item == nil || delegatedStatusTerminal(item.Status) {
			continue
		}
		if strings.TrimSpace(item.ConversationID) == parentConversationID && strings.TrimSpace(item.ToolCallID) == parentToolCallID {
			parentRequestID = strings.TrimSpace(item.ParentRequestID)
			break
		}
	}
	service.delegationRuntimeMu.Unlock()
	if parentRequestID == "" {
		return
	}
	if err := service.broker.Publish(parentRequestID, StreamEvent{
		Message: buildTaskToolCallDeltaMessage(parentToolCallID, strings.TrimSpace(modelCallID), interaction),
	}); err != nil {
		logger.Infof("forwarder native child delta mirror failed child_conversation_id=%s parent_request_id=%s parent_tool_call_id=%s err=%v", childConversationID, parentRequestID, parentToolCallID, err)
	}
}

func (service *Service) setNativeDelegationProgress(execID, summary string) bool {
	if service == nil || strings.TrimSpace(execID) == "" {
		return false
	}
	service.delegationRuntimeMu.Lock()
	defer service.delegationRuntimeMu.Unlock()
	item := service.nativeDelegations[strings.TrimSpace(execID)]
	if item == nil || delegatedStatusTerminal(item.Status) {
		return false
	}
	item.ProgressSummary = strings.TrimSpace(summary)
	item.UpdatedAt = time.Now().UTC()
	return true
}

func (service *Service) markNativeDelegationEffectiveProgress(execID, summary string) bool {
	if service == nil || strings.TrimSpace(execID) == "" {
		return false
	}
	service.delegationRuntimeMu.Lock()
	defer service.delegationRuntimeMu.Unlock()
	item := service.nativeDelegations[strings.TrimSpace(execID)]
	if item == nil || delegatedStatusTerminal(item.Status) {
		return false
	}
	now := time.Now().UTC()
	item.LastEffectiveProgressAt = now
	item.UpdatedAt = now
	if strings.TrimSpace(summary) != "" {
		item.ProgressSummary = strings.TrimSpace(summary)
	}
	return true
}

// deferNativeSubagentExecWatchdog 在 exec 看门狗到期时尝试延期一次 native 子代理的强杀：
// 仅当子代理运行态仍在（客户端尚未回报终态，说明子代理可能仍在客户端执行）且延期次数
// 未达上限时返回 true，并原子递增计数；返回 false 表示应走正常超时收口。
func (service *Service) deferNativeSubagentExecWatchdog(execID string) bool {
	if service == nil || strings.TrimSpace(execID) == "" {
		return false
	}
	service.delegationRuntimeMu.Lock()
	defer service.delegationRuntimeMu.Unlock()
	item := service.nativeDelegations[strings.TrimSpace(execID)]
	if item == nil || delegatedStatusTerminal(item.Status) {
		return false
	}
	if item.ExecWatchdogDeferrals >= nativeSubagentExecWatchdogMaxDeferrals {
		return false
	}
	item.ExecWatchdogDeferrals++
	item.UpdatedAt = time.Now().UTC()
	return true
}

func (service *Service) watchNativeDelegationProgress(requestID, execID string) {
	if service == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(execID) == "" {
		return
	}
	safego.Go("forwarder:native-delegation-watchdog", func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			item, ok := service.nativeDelegationTask(execID)
			if !ok || delegatedStatusTerminal(item.Status) {
				return
			}
			last := item.LastEffectiveProgressAt
			if last.IsZero() {
				last = item.StartedAt
			}
			if time.Since(last) < service.nativeDelegationProgressTimeout() {
				continue
			}
			// 子代理会话仍有模型输出/思考或工具活动（conversation 级）时不算「无有效进展」：
			// 模型正在长文本生成或长思考期间没有工具结果事件，误判会砍掉正常任务。
			if service.markDelegationConversationProgress(item) {
				continue
			}
			service.updateNativeDelegationStatus(execID, delegation.TaskTimedOut, "Cursor 子代理无有效进展，已停止等待", "无有效进展超时：Cursor 子代理连续没有新的工具调用、工具结果或有效输出")
			stream, ok := service.broker.Get(requestID)
			if ok && stream != nil {
				if pending, found := selectPendingExec(execID, 0, stream); found {
					if err := service.recoverExecWithoutTerminal(stream, pending, "无有效进展超时：Cursor 子代理连续没有新的工具调用、工具结果或有效输出"); err != nil {
						logger.Errorf("forwarder native delegation watchdog recover failed exec_id=%s err=%v", strings.TrimSpace(execID), err)
					}
				}
			}
			return
		}
	})
}

// nativeDelegationProgressTimeout 解析当前 native 子代理「无有效进展」看门狗阈值，
// 读取热加载配置；resolver 缺失时回退默认 5 分钟。
func (service *Service) nativeDelegationProgressTimeout() time.Duration {
	if service != nil && service.resolver != nil {
		return service.resolver.NativeDelegationProgressTimeout(context.Background())
	}
	return defaultNativeDelegationProgressTimeout
}

// markConversationActivity 记录某 conversation 最近一次模型输出/思考/工具活动的时间。
// native 子代理与主 agent 共享 conversation_id（本地模式下同一会话的不同 request），
// 子代理模型仍在生成内容、思考或执行工具时该时间持续刷新，使无进展看门狗不会误杀正常任务。
func (service *Service) markConversationActivity(conversationID string) {
	if service == nil || strings.TrimSpace(conversationID) == "" {
		return
	}
	service.conversationActivityMu.Lock()
	if service.conversationLastActivity == nil {
		service.conversationLastActivity = make(map[string]time.Time)
	}
	service.conversationLastActivity[strings.TrimSpace(conversationID)] = time.Now().UTC()
	service.conversationActivityMu.Unlock()
}

// markDelegationConversationProgress 检查子代理所属 conversation 最近是否有模型输出/思考
// 或工具活动；有则续期该子代理的「有效进展」时间并返回 true。
// 子代理与主 agent 共享 conversation_id（本地模式下同一会话的不同 request），
// 因此 conversation 级活动可直接反映该子代理是否仍在工作。
func (service *Service) markDelegationConversationProgress(item *nativeDelegationRuntime) bool {
	if service == nil || item == nil {
		return false
	}
	conversationID := strings.TrimSpace(item.ConversationID)
	if conversationID == "" {
		return false
	}
	service.conversationActivityMu.Lock()
	last, ok := service.conversationLastActivity[conversationID]
	service.conversationActivityMu.Unlock()
	if !ok {
		return false
	}
	if time.Since(last) >= service.nativeDelegationProgressTimeout() {
		return false
	}
	return service.markNativeDelegationEffectiveProgress(item.ID, "")
}

func (service *Service) keepNativeDelegationAlive(requestID, execID string) {
	if service == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(execID) == "" {
		return
	}
	safego.Go("forwarder:native-delegation-progress", func() {
		ticker := time.NewTicker(nativeDelegationProgressInterval)
		defer ticker.Stop()
		for range ticker.C {
			item, ok := service.nativeDelegationTask(execID)
			if !ok || delegatedStatusTerminal(item.Status) {
				return
			}
			summary := nativeDelegationRunningSummary(item)
			if service.setNativeDelegationProgress(execID, summary) {
				service.publishNativeDelegationProgress(requestID, execID, summary)
			}
		}
	})
}

func (service *Service) updateNativeDelegationStatus(execID string, status delegation.TaskStatus, progress string, errorText string) {
	if service == nil || strings.TrimSpace(execID) == "" {
		return
	}
	now := time.Now().UTC()
	service.delegationRuntimeMu.Lock()
	service.pruneNativeDelegationsLocked(now)
	item := service.nativeDelegations[strings.TrimSpace(execID)]
	if item == nil {
		service.delegationRuntimeMu.Unlock()
		return
	}
	if delegatedStatusTerminal(item.Status) {
		service.delegationRuntimeMu.Unlock()
		return
	}
	item.Status = status
	item.ProgressSummary = strings.TrimSpace(progress)
	item.Error = strings.TrimSpace(errorText)
	item.UpdatedAt = now
	if delegatedStatusTerminal(status) {
		item.FinishedAt = now
		if item.holdsSlot {
			item.holdsSlot = false
			service.releaseNativeDelegationSlotLocked()
		}
	}
	requestID := item.ParentRequestID
	snapshot := *item
	service.delegationRuntimeMu.Unlock()
	// 成功/普通状态转换用 INFO 记录；只有带错误文本或失败态（failed/canceled/timed_out）
	// 才用 ERR，避免子代理正常完成被误记为错误日志（曾出现 status=completed error= 的假报警）。
	statusText := strings.TrimSpace(string(status))
	errorText = strings.TrimSpace(errorText)
	if errorText != "" || status == delegation.TaskFailed || status == delegation.TaskCanceled || status == delegation.TaskTimedOut {
		logger.Errorf("forwarder native delegation status request_id=%s conversation_id=%s exec_id=%s tool_call_id=%s model_id=%s model_name=%s status=%s error=%s",
			strings.TrimSpace(requestID), strings.TrimSpace(snapshot.ConversationID), strings.TrimSpace(execID), strings.TrimSpace(snapshot.ToolCallID), strings.TrimSpace(snapshot.ModelID), strings.TrimSpace(snapshot.ModelName), statusText, errorText)
	} else {
		logger.Infof("forwarder native delegation status request_id=%s conversation_id=%s exec_id=%s tool_call_id=%s model_id=%s model_name=%s status=%s error=%s",
			strings.TrimSpace(requestID), strings.TrimSpace(snapshot.ConversationID), strings.TrimSpace(execID), strings.TrimSpace(snapshot.ToolCallID), strings.TrimSpace(snapshot.ModelID), strings.TrimSpace(snapshot.ModelName), statusText, errorText)
	}
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), requestID, snapshot.ConversationID, "native_delegation_status", map[string]any{
			"exec_id":      strings.TrimSpace(execID),
			"tool_call_id": strings.TrimSpace(snapshot.ToolCallID),
			"model_id":     strings.TrimSpace(snapshot.ModelID),
			"model_name":   strings.TrimSpace(snapshot.ModelName),
			"status":       statusText,
			"error":        errorText,
		})
	}
	if delegatedStatusTerminal(status) {
		service.publishNativeDelegationProgress(requestID, execID, nativeDelegationTerminalSummary(&snapshot))
		_ = service.broker.Publish(requestID, StreamEvent{
			Message: buildThinkingCompletedMessage(int32(time.Since(snapshot.StartedAt).Milliseconds())),
		})
	}
}

func (service *Service) nativeDelegationSnapshots() []DelegationTaskSnapshot {
	if service == nil {
		return nil
	}
	now := time.Now().UTC()
	service.delegationRuntimeMu.Lock()
	defer service.delegationRuntimeMu.Unlock()
	service.pruneNativeDelegationsLocked(now)
	items := make([]DelegationTaskSnapshot, 0, len(service.nativeDelegations))
	for _, item := range service.nativeDelegations {
		if item == nil {
			continue
		}
		end := firstNonZeroTime(item.FinishedAt, now)
		duration := int64(0)
		if !item.StartedAt.IsZero() {
			duration = end.Sub(item.StartedAt).Milliseconds()
			if duration < 0 {
				duration = 0
			}
		}
		items = append(items, DelegationTaskSnapshot{
			ID:               item.ID,
			AggregateID:      item.ParentRequestID,
			Description:      item.Description,
			ModelID:          item.ModelID,
			ModelName:        item.ModelName,
			WorkerRole:       item.WorkerRole,
			ExecutionMode:    delegation.ExecutionModeCursor,
			Status:           item.Status,
			ProgressSummary:  item.ProgressSummary,
			Error:            item.Error,
			ParentRequestID:  item.ParentRequestID,
			ParentExecID:     item.ID,
			GroupID:          item.ParentRequestID,
			QueuedAtUnixMS:   unixMilliseconds(item.QueuedAt),
			StartedAtUnixMS:  unixMilliseconds(item.StartedAt),
			FinishedAtUnixMS: unixMilliseconds(item.FinishedAt),
			UpdatedAtUnixMS:  unixMilliseconds(item.UpdatedAt),
			DurationMS:       duration,
			Cancelable:       !delegatedStatusTerminal(item.Status),
		})
	}
	return items
}

func (service *Service) pruneNativeDelegationsLocked(now time.Time) {
	cutoff := now.Add(-nativeDelegationRetention)
	for id, item := range service.nativeDelegations {
		if item == nil || (delegatedStatusTerminal(item.Status) && item.UpdatedAt.Before(cutoff)) {
			if item != nil && item.holdsSlot {
				item.holdsSlot = false
				service.releaseNativeDelegationSlotLocked()
			}
			delete(service.nativeDelegations, id)
		}
	}
}

func (service *Service) nativeDelegationTask(execID string) (*nativeDelegationRuntime, bool) {
	if service == nil {
		return nil, false
	}
	service.delegationRuntimeMu.Lock()
	defer service.delegationRuntimeMu.Unlock()
	service.pruneNativeDelegationsLocked(time.Now().UTC())
	item, ok := service.nativeDelegations[strings.TrimSpace(execID)]
	if !ok || item == nil {
		return nil, false
	}
	// nativeDelegationRuntime 当前全部为值类型字段，浅拷贝安全。
	// 若将来新增指针/切片/map 字段，需改为深拷贝，避免与 updateNativeDelegationStatus 的写产生数据竞争。
	snapshot := *item
	return &snapshot, true
}

func (service *Service) cancelNativeDelegation(execID string) bool {
	item, ok := service.nativeDelegationTask(execID)
	if !ok || delegatedStatusTerminal(item.Status) {
		return false
	}
	stream, ok := service.broker.Get(item.ParentRequestID)
	if !ok || stream == nil {
		return false
	}
	pending, ok := selectPendingExec(execID, 0, stream)
	if !ok || strings.TrimSpace(pending.ExecKind) != "subagent" {
		return false
	}
	if err := service.broker.Publish(item.ParentRequestID, StreamEvent{Message: buildExecAbortMessage(pending)}); err != nil {
		return false
	}
	markExecCompleted(stream, pending)
	service.updateNativeDelegationStatus(execID, delegation.TaskCanceled, "Cursor 子代理已取消", "subagent canceled")
	service.recordDelegationRunTerminal(stream, pending, agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_ABORTED, "Cursor 子代理", "subagent canceled")
	payload := "{\"status\":\"canceled\",\"detail\":\"Cursor 子代理已取消\"}"
	if err := service.appendToolResult(stream, pending.ToolCallID, "Task", pending.ArgsJSON, payload, pending.ReasoningContent, nil); err != nil {
		return false
	}
	if err := service.publishToolCallCompleted(stream.RequestID, pending.ToolCallID, pending.ModelCallID, nil); err != nil {
		return false
	}
	if err := service.syncSummaryCarryForward(stream.ConversationID, stream.RequestID, pending.ModelCallID); err != nil {
		return false
	}
	if err := service.publishCheckpointForce(stream.RequestID, stream.ConversationID); err != nil {
		return false
	}
	_ = service.reconcileStream(stream)
	return true
}

func subagentResultFailed(result *agentv1.SubagentResult) bool {
	return result != nil && result.GetError() != nil
}

func subagentResultErrorText(result *agentv1.SubagentResult) string {
	if result == nil || result.GetError() == nil {
		return ""
	}
	return strings.TrimSpace(result.GetError().GetError())
}

// hasActiveDelegation reports whether the stream currently owns any live
// delegated work that must not be torn down by an orphan-cancel or a turn-stale
// watchdog. It covers both delegation shapes: a Multitask aggregate
// (delegation_aggregate pending exec) and a native Cursor subagent
// (subagent pending exec registered in service.nativeDelegations that is still
// non-terminal). Native subagents live outside stream.PendingExecs as an
// aggregate, so hasActiveDelegationAggregate alone would miss them.
func (service *Service) hasActiveDelegation(stream *ActiveStream) bool {
	if hasActiveDelegationAggregate(stream) {
		return true
	}
	if service == nil || stream == nil {
		return false
	}
	requestID := strings.TrimSpace(stream.RequestID)
	if requestID == "" {
		return false
	}
	service.delegationRuntimeMu.Lock()
	defer service.delegationRuntimeMu.Unlock()
	service.pruneNativeDelegationsLocked(time.Now().UTC())
	for _, item := range service.nativeDelegations {
		if item == nil {
			continue
		}
		if strings.TrimSpace(item.ParentRequestID) == requestID && !delegatedStatusTerminal(item.Status) {
			return true
		}
	}
	return false
}
