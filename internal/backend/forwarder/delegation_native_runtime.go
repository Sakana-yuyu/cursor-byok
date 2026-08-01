package forwarder

import (
	"strings"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
)

const nativeDelegationRetention = 10 * time.Minute
const nativeDelegationProgressInterval = 12 * time.Second
const nativeDelegationEffectiveProgressTimeout = 5 * time.Minute

// nativeDelegationRuntime tracks direct Cursor Task -> subagent executions.
// These executions do not enter the Multitask scheduler, but they still need
// the same sanitized runtime view for the desktop delegation UI.
type nativeDelegationRuntime struct {
	ID                      string
	ParentRequestID         string
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
}

func (service *Service) registerNativeDelegation(stream *ActiveStream, pending runtimecore.PendingExec, serverMessage *agentv1.AgentServerMessage) {
	if service == nil || stream == nil || strings.TrimSpace(pending.ExecKind) != "subagent" {
		return
	}
	args, _ := runtimecore.DecodeArgsMap(pending.ArgsJSON)
	now := time.Now().UTC()
	modelID := strings.TrimSpace(runtimecore.ReadStringArg(args, "model", "model_id", "modelId"))
	modelName := modelID
	workerRole := strings.TrimSpace(runtimecore.ReadStringArg(args, "subagent_type", "subagentType"))
	if serverMessage != nil {
		if execMessage := serverMessage.GetExecServerMessage(); execMessage != nil {
			if subagentArgs := execMessage.GetSubagentArgs(); subagentArgs != nil {
				modelID = firstNonEmpty(subagentArgs.GetModelId(), modelID)
				modelName = modelID
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
		modelName = firstNonEmpty(strings.TrimSpace(stream.ModelName), modelID)
	}
	stream.mu.Unlock()
	if strings.TrimSpace(pending.ExecID) == "" || parentRequestID == "" {
		return
	}
	service.delegationRuntimeMu.Lock()
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
	}
	service.delegationRuntimeMu.Unlock()
	service.publishNativeDelegationProgress(parentRequestID, pending.ExecID, nativeDelegationStartSummary(description, modelName, workerRole))
	service.keepNativeDelegationAlive(parentRequestID, pending.ExecID)
	service.watchNativeDelegationProgress(parentRequestID, pending.ExecID)
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
	if err := service.broker.Publish(requestID, StreamEvent{
		Message: buildThinkingDeltaMessage(summary, agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT),
	}); err != nil {
		return
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

func (service *Service) watchNativeDelegationProgress(requestID, execID string) {
	if service == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(execID) == "" {
		return
	}
	go func() {
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
			if time.Since(last) < nativeDelegationEffectiveProgressTimeout {
				continue
			}
			service.updateNativeDelegationStatus(execID, delegation.TaskTimedOut, "Cursor 子代理无有效进展，已停止等待", "无有效进展超时：Cursor 子代理连续 5 分钟没有新的工具调用、工具结果或有效输出")
			stream, ok := service.broker.Get(requestID)
			if ok && stream != nil {
				if pending, found := selectPendingExec(execID, 0, stream); found {
					_ = service.recoverExecWithoutTerminal(stream, pending, "无有效进展超时：Cursor 子代理连续 5 分钟没有新的工具调用、工具结果或有效输出")
				}
			}
			return
		}
	}()
}

func (service *Service) keepNativeDelegationAlive(requestID, execID string) {
	if service == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(execID) == "" {
		return
	}
	go func() {
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
	}()
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
	}
	requestID := item.ParentRequestID
	snapshot := *item
	service.delegationRuntimeMu.Unlock()
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
	copy := *item
	return &copy, true
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
	if err := service.publishCheckpoint(stream.RequestID, stream.ConversationID); err != nil {
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
