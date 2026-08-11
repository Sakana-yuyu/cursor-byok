package forwarder

import (
	"context"
	"cursor/internal/logger"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
)

const (
	delegatedWorkerTimeout      = 30 * time.Minute
	delegatedWorkerQueueTimeout = 90 * time.Second
)

type streamDelegationResult struct {
	AggregateID  string
	ExecID       string
	ToolCallID   string
	ProviderPass int
	Payload      string
}

type delegatedAggregate struct {
	mu               sync.Mutex
	id               string
	workerIDs        []string
	submissionErrors []delegatedWorkerResult
	ctx              context.Context
	cancel           context.CancelFunc
	scheduler        *delegation.Scheduler
	canceled         bool
	startupDone      bool
}

type delegatedAggregateSnapshot struct {
	workerIDs        []string
	submissionErrors []delegatedWorkerResult
	canceled         bool
	startupDone      bool
}

type delegatedWorkerResult struct {
	TaskID               string                `json:"task_id"`
	ModelID              string                `json:"model_id"`
	ModelGroupID         string                `json:"model_group_id"`
	ExecutionMode        string                `json:"execution_mode"`
	Status               delegation.TaskStatus `json:"status"`
	DurationMS           int64                 `json:"duration_ms"`
	Output               string                `json:"output,omitempty"`
	Error                string                `json:"error,omitempty"`
	ToolCallCount        int                   `json:"tool_call_count"`
	SupervisionStatus    string                `json:"supervision_status,omitempty"`
	SupervisionRound     int                   `json:"supervision_round,omitempty"`
	CorrectionCount      int                   `json:"correction_count,omitempty"`
	RetryCount           int                   `json:"retry_count,omitempty"`
	ReassignCount        int                   `json:"reassign_count,omitempty"`
	Escalated            bool                  `json:"escalated,omitempty"`
	SupervisionIssueCode string                `json:"supervision_issue_code,omitempty"`
	SupervisionReason    string                `json:"supervision_reason,omitempty"`
}

type delegatedAggregateResult struct {
	AggregateID string                  `json:"aggregate_id"`
	Status      string                  `json:"status"`
	Succeeded   int                     `json:"succeeded"`
	Failed      int                     `json:"failed"`
	Canceled    int                     `json:"canceled"`
	Tasks       []delegatedWorkerResult `json:"tasks"`
}

type multitaskDelegationCoordinator struct {
	service        *Service
	configProvider delegation.RuntimeConfigProvider
	scheduler      *delegation.Scheduler
	supervisor     *SupervisorCoordinator
	maxConcurrency int

	mu         sync.RWMutex
	startMu    sync.Mutex
	aggregates map[string]*delegatedAggregate
	closed     bool
}

func newMultitaskDelegationCoordinator(service *Service, configProvider delegation.RuntimeConfigProvider) *multitaskDelegationCoordinator {
	if service == nil || service.cursorDelegation == nil || service.localDelegation == nil {
		return nil
	}
	config := delegation.NormalizeRuntimeConfig(delegation.RuntimeConfig{MaxConcurrency: delegation.DefaultMaxConcurrency})
	if configProvider != nil {
		config = delegation.NormalizeRuntimeConfig(configProvider.DelegationRuntimeConfig())
	}
	coordinator := &multitaskDelegationCoordinator{
		service:        service,
		configProvider: configProvider,
		aggregates:     make(map[string]*delegatedAggregate),
	}
	coordinator.scheduler = delegation.NewScheduler(delegation.Config{MaxConcurrency: config.MaxConcurrency}, coordinator.executeWorker)
	coordinator.maxConcurrency = normalizedDelegationConcurrency(config.MaxConcurrency)
	coordinator.supervisor = newSupervisorCoordinator(service, coordinator.scheduler)
	return coordinator
}

func (service *Service) tryStartDelegatedTask(stream *ActiveStream, invocation runtimecore.ToolInvocation) (bool, error) {
	if service == nil || service.multitaskDelegation == nil || stream == nil {
		if service != nil && service.debug != nil && stream != nil {
			service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "delegation_start_skipped", map[string]any{"reason": "service/coordinator/stream unavailable"})
		}
		return false, nil
	}
	stream.mu.Lock()
	mode := stream.Mode
	providerPass := stream.ProviderPassCount
	modelCallID := strings.TrimSpace(invocation.ModelCallID)
	reasoningContent := invocation.ReasoningContent
	reasoningSignature := invocation.ReasoningSignature
	reasoningSignatureSource := invocation.ReasoningSignatureSource
	modelName := strings.TrimSpace(stream.ModelName)
	thinkingEffort := strings.TrimSpace(stream.ThinkingEffort)
	maxMode := stream.MaxMode
	stream.mu.Unlock()
	if mode != agentv1.AgentMode_AGENT_MODE_MULTITASK {
		logger.Infof("forwarder delegation start skipped request_id=%s reason=mode_not_multitask mode=%s provider_pass=%d", strings.TrimSpace(stream.RequestID), mode.String(), providerPass)
		return false, nil
	}
	if hasDelegationAggregateForToolCall(stream, invocation.CallID) {
		logger.Errorf("forwarder delegation start rejected request_id=%s reason=duplicate_tool_call provider_pass=%d tool_call_id=%s", strings.TrimSpace(stream.RequestID), providerPass, strings.TrimSpace(invocation.CallID))
		return false, fmt.Errorf("a delegated task batch is already running for tool call %q", strings.TrimSpace(invocation.CallID))
	}
	now := time.Now().UTC()
	pending := runtimecore.PendingExec{
		ExecID:                   fmt.Sprintf("exec-delegation-%d", now.UnixNano()),
		ProviderPass:             providerPass,
		ModelCallID:              modelCallID,
		ToolCallID:               strings.TrimSpace(invocation.CallID),
		ArgsJSON:                 append([]byte(nil), invocation.ArgsJSON...),
		ReasoningContent:         reasoningContent,
		ReasoningSignature:       reasoningSignature,
		ReasoningSignatureSource: reasoningSignatureSource,
		ExecKind:                 "delegation_aggregate",
		StreamState:              "opened",
		OpenedAt:                 now,
	}
	base := buildDelegatedCursorTaskRequest(stream, pending, invocation, delegation.ExecutionModeAuto, "", service.multitaskDelegation.runtimeConfig().SubagentProfiles)
	base.ID = pending.ExecID
	base.Mode = agentv1.AgentMode_AGENT_MODE_AGENT
	base.ModelName = modelName
	base.ThinkingEffort = thinkingEffort
	base.MaxMode = maxMode

	started, err := service.multitaskDelegation.Start(stream, pending, base)
	if err != nil || !started {
		reason := "not_started"
		if err != nil {
			reason = err.Error()
		}
		logger.Infof("forwarder delegation start result request_id=%s exec_id=%s reason=%s started=%t provider_pass=%d", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), reason, started, providerPass)
		if service.debug != nil {
			service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "delegation_start_result", map[string]any{"exec_id": pending.ExecID, "started": started, "provider_pass": providerPass, "reason": reason})
		}
		return false, err
	}
	// 本地委派由 BYOK worker 执行。不要再额外派发 ExecServerMessage(subagent_args)：
	// 该消息会启动一个独立的 Cursor 原生子代理，原生子代理初始化失败时客户端会
	// 取消整个前台 turn。Task UI 状态只通过 checkpoint 中的
	// subagent_runs_by_parent_tool_call_id 和最终 tool_call_completed 同步。
	logger.Infof("forwarder delegation aggregate started request_id=%s exec_id=%s tool_call_id=%s provider_pass=%d ui_transport=checkpoint",
		strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), strings.TrimSpace(pending.ToolCallID), pending.ProviderPass)
	service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "delegation_aggregate_started", map[string]any{
		"exec_id":            strings.TrimSpace(pending.ExecID),
		"tool_call_id":       strings.TrimSpace(pending.ToolCallID),
		"provider_pass":      pending.ProviderPass,
		"ui_state_transport": "checkpoint",
		"native_exec_sent":   false,
	})
	// Keep the aggregate under the same long-running exec watchdog as a direct
	// subagent. turn-stale must not be its only recovery path.
	service.scheduleExecWatchdog(stream.RequestID, pending)
	service.recordExecDispatchMetadata(stream, pending, false, true, "delegation_scheduler")
	if err := service.publishCheckpointForce(stream.RequestID, stream.ConversationID); err != nil {
		service.multitaskDelegation.CancelAggregate(pending.ExecID)
		markExecCompleted(stream, pending)
		return false, err
	}
	logger.Infof("forwarder delegation initial checkpoint published request_id=%s exec_id=%s tool_call_id=%s",
		strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), strings.TrimSpace(pending.ToolCallID))
	service.keepDelegationTaskAlive(stream, pending)
	return true, nil
}

// maybeStartAutomaticMultitaskDelegation makes Multitask deterministic when the
// parent model starts exploring with ordinary read-only tools instead of
// emitting Task itself. The synthetic Task uses the same aggregate/checkpoint
// path as a model-authored Task, so Cursor receives a normal subagent run.
func (service *Service) maybeStartAutomaticMultitaskDelegation(stream *ActiveStream, invocation runtimecore.ToolInvocation) (bool, error) {
	if service == nil || stream == nil {
		return false, nil
	}
	toolName := strings.TrimSpace(invocation.ToolName)
	if toolName == "Task" || !isAutomaticMultitaskExplorationTool(toolName) {
		return false, nil
	}
	stream.mu.Lock()
	mode := stream.Mode
	alreadyStarted := stream.AutoMultitaskDelegationStarted
	latestUserText := strings.TrimSpace(stream.LatestUserText)
	stream.mu.Unlock()
	if mode != agentv1.AgentMode_AGENT_MODE_MULTITASK || alreadyStarted || !isAutomaticMultitaskRequest(latestUserText) {
		return false, nil
	}
	if service.multitaskDelegation == nil {
		if service.debug != nil {
			service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "automatic_delegation_skipped", map[string]any{
				"trigger_tool": toolName,
				"reason":       "multitask_coordinator_unavailable",
			})
		}
		return false, nil
	}
	config := service.multitaskDelegation.runtimeConfig()
	if !config.Enabled {
		if service.debug != nil {
			service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "automatic_delegation_skipped", map[string]any{
				"trigger_tool": toolName,
				"reason":       "delegation_disabled",
			})
		}
		return false, nil
	}
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "automatic_delegation_evaluated", map[string]any{
			"mode":         mode.String(),
			"trigger_tool": toolName,
			"request_text": latestUserText,
		})
	}
	stream.mu.Lock()
	if stream.AutoMultitaskDelegationStarted {
		stream.mu.Unlock()
		return false, nil
	}
	stream.AutoMultitaskDelegationStarted = true
	stream.mu.Unlock()

	argsJSON, err := json.Marshal(map[string]any{
		"description":   "Automatically delegated Multitask exploration",
		"prompt":        latestUserText,
		"subagent_type": "explore",
		"readonly":      true,
	})
	if err != nil {
		service.resetAutomaticMultitaskDelegation(stream)
		return false, err
	}
	autoCallID := "tc_auto_" + uuid.NewString()
	autoInvocation := runtimecore.ToolInvocation{
		ToolName:    "Task",
		CallID:      autoCallID,
		ModelCallID: strings.TrimSpace(invocation.ModelCallID),
		ArgsJSON:    argsJSON,
	}
	if err := service.handleToolInvocation(stream, autoInvocation); err != nil {
		service.resetAutomaticMultitaskDelegation(stream)
		if service.debug != nil {
			service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "automatic_delegation_skipped", map[string]any{
				"trigger_tool": toolName,
				"reason":       err.Error(),
			})
		}
		return false, err
	}
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "automatic_delegation_started", map[string]any{
			"trigger_tool":  toolName,
			"tool_call_id":  autoCallID,
			"subagent_type": "explore",
			"model_name":    stream.ModelName,
		})
	}
	return true, nil
}

func (service *Service) resetAutomaticMultitaskDelegation(stream *ActiveStream) {
	if stream == nil {
		return
	}
	stream.mu.Lock()
	stream.AutoMultitaskDelegationStarted = false
	stream.mu.Unlock()
}

func isAutomaticMultitaskExplorationTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "Ls", "Glob", "Grep", "Read", "ReadLints":
		return true
	default:
		return false
	}
}

func isAutomaticMultitaskRequest(text string) bool {
	trimmed := strings.TrimSpace(text)
	if len([]rune(trimmed)) < 12 {
		return false
	}
	lower := strings.ToLower(trimmed)
	switch lower {
	case "hi", "hello", "你好", "嗨":
		return false
	default:
		return true
	}
}

const delegationTaskProgressInterval = 15 * time.Second

// keepDelegationTaskAlive gives the Cursor foreground Task a genuine child
// progress update while its locally managed worker batch is active. A RunSSE
// heartbeat only keeps the transport open; it does not reset Cursor's
// foreground Task inactivity controller.
func (service *Service) keepDelegationTaskAlive(stream *ActiveStream, pending runtimecore.PendingExec) {
	if service == nil || stream == nil || strings.TrimSpace(pending.ExecKind) != "delegation_aggregate" {
		return
	}
	requestID := strings.TrimSpace(stream.RequestID)
	execID := strings.TrimSpace(pending.ExecID)
	toolCallID := strings.TrimSpace(pending.ToolCallID)
	if requestID == "" || execID == "" || toolCallID == "" {
		return
	}
	go func() {
		ticker := time.NewTicker(delegationTaskProgressInterval)
		defer ticker.Stop()
		consecutiveFailures := 0
		for range ticker.C {
			stream.mu.Lock()
			_, active := stream.PendingExecs[execID]
			terminal := isTerminalStreamStatus(stream.Status)
			stream.mu.Unlock()
			if !active || terminal {
				return
			}
			// 周期性同步 checkpoint：checkpoint 携带 subagent_runs_by_parent_tool_call_id
			// （RUNNING），让 Cursor 客户端 Task 卡片持续显示 running。不再向主进程
			// thinking 区域刷 "Delegated workers are still running"（避免刷屏）。
			// 瞬时失败只计数，连续多次失败才放弃，避免一次抖动导致卡片回退 stopped。
			if err := service.publishCheckpoint(requestID, stream.ConversationID); err != nil {
				consecutiveFailures++
				logger.Errorf("forwarder delegation progress checkpoint failed request_id=%s exec_id=%s failures=%d err=%v", requestID, execID, consecutiveFailures, err)
				if consecutiveFailures >= 3 {
					return
				}
				continue
			}
			consecutiveFailures = 0
		}
	}()
}

// delegationResultRunStatus 从委派结果 JSON 推导 SubagentRun 终态状态。
func delegationResultRunStatus(resultPayload string) agentv1.SubagentRunStatus {
	var summary struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal([]byte(resultPayload), &summary)
	switch strings.TrimSpace(summary.Status) {
	case "completed", "partial_success":
		return agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_SUCCESS
	case "canceled":
		return agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_ABORTED
	default:
		return agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_ERROR
	}
}

func delegationResultRunTitle(resultPayload string) string {
	_ = resultPayload
	return "委派任务"
}

// delegationResultRunDetail 返回委派失败的简要原因（成功时为空）。
func delegationResultRunDetail(resultPayload string) string {
	var summary struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	_ = json.Unmarshal([]byte(resultPayload), &summary)
	if status := strings.TrimSpace(summary.Status); status == "completed" || status == "partial_success" {
		return ""
	}
	return delegation.SanitizeSupervisorText(strings.TrimSpace(summary.Error), "")
}

func hasActiveDelegationAggregate(stream *ActiveStream) bool {
	if stream == nil {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	for _, pending := range stream.PendingExecs {
		if strings.TrimSpace(pending.ExecKind) == "delegation_aggregate" {
			return true
		}
	}
	return false
}

// hasDelegationAggregateForToolCall rejects only a duplicate dispatch of the
// same Task call. Multitask may legitimately run the synthetic exploration
// Task alongside later model-authored Task calls.
func hasDelegationAggregateForToolCall(stream *ActiveStream, toolCallID string) bool {
	if stream == nil {
		return false
	}
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	for _, pending := range stream.PendingExecs {
		if strings.TrimSpace(pending.ExecKind) == "delegation_aggregate" && strings.TrimSpace(pending.ToolCallID) == toolCallID {
			return true
		}
	}
	return false
}

func (service *Service) handleDelegationResult(stream *ActiveStream, payload *streamDelegationResult) error {
	if service == nil || stream == nil || payload == nil {
		return nil
	}
	stream.mu.Lock()
	pending, ok := stream.PendingExecs[strings.TrimSpace(payload.ExecID)]
	stream.mu.Unlock()
	if !ok || strings.TrimSpace(pending.ExecKind) != "delegation_aggregate" {
		return nil
	}
	if strings.TrimSpace(payload.AggregateID) != strings.TrimSpace(pending.ExecID) {
		return nil
	}
	if service.ignoreStaleExecProviderPass(stream, pending, "delegation_result") {
		return nil
	}
	resultPayload := strings.TrimSpace(payload.Payload)
	if resultPayload == "" {
		resultPayload = fmt.Sprintf(`{"aggregate_id":%q,"status":"failed","error":"empty delegation result"}`, payload.AggregateID)
	}
	var resultSummary delegatedAggregateResult
	_ = json.Unmarshal([]byte(resultPayload), &resultSummary)
	logger.Infof("forwarder delegation terminal handling request_id=%s exec_id=%s tool_call_id=%s payload_bytes=%d pending_found=%t", strings.TrimSpace(stream.RequestID), strings.TrimSpace(payload.ExecID), strings.TrimSpace(payload.ToolCallID), len(resultPayload), ok)
	logger.Errorf("forwarder delegation terminal received request_id=%s exec_id=%s tool_call_id=%s provider_pass=%d result_status=%s succeeded=%d failed=%d canceled=%d",
		strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), strings.TrimSpace(pending.ToolCallID), pending.ProviderPass, strings.TrimSpace(resultSummary.Status), resultSummary.Succeeded, resultSummary.Failed, resultSummary.Canceled)
	service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "delegation_terminal_received", map[string]any{
		"exec_id":       strings.TrimSpace(pending.ExecID),
		"tool_call_id":  strings.TrimSpace(pending.ToolCallID),
		"provider_pass": pending.ProviderPass,
		"result_status": strings.TrimSpace(resultSummary.Status),
		"succeeded":     resultSummary.Succeeded,
		"failed":        resultSummary.Failed,
		"canceled":      resultSummary.Canceled,
	})
	markExecCompleted(stream, pending)
	// 记录委派终态，供 publishCheckpoint 通过 subagent_runs_by_parent_tool_call_id
	// 同步给 Cursor 客户端（Task 卡片从 stopped/运行中恢复为 succeeded/error）。
	service.recordDelegationRunTerminal(stream, pending, delegationResultRunStatus(resultPayload), delegationResultRunTitle(resultPayload), delegationResultRunDetail(resultPayload))
	if err := service.appendToolResult(stream, pending.ToolCallID, "Task", pending.ArgsJSON, resultPayload, pending.ReasoningContent, nil); err != nil {
		logger.Errorf("forwarder delegation terminal append tool result failed request_id=%s exec_id=%s tool_call_id=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), strings.TrimSpace(pending.ToolCallID), err)
		return err
	}
	// 构造完整的 Task tool call（含 TaskResult）随 tool_call_completed 发送给 Cursor 客户端。
	// Cursor 客户端在 subagentTaskState 判定中依赖 tool_call_completed 的 tool_call.result：
	// 带成功/失败 Result 时 Task 显示为 succeeded/failed，缺 Result 时退化为 cancelled。
	durationMS := time.Since(firstNonZeroTime(pending.OpenedAt, time.Now().UTC())).Milliseconds()
	if durationMS < 1 {
		durationMS = 1
	}
	completedToolCall := buildDelegationCompletedTaskToolCall(pending.ArgsJSON, resultPayload, "", uint64(durationMS))
	logger.Infof("forwarder delegation terminal task result ready request_id=%s exec_id=%s tool_call_id=%s status=%s", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), strings.TrimSpace(pending.ToolCallID), strings.TrimSpace(resultSummary.Status))
	if err := service.publishToolCallCompleted(stream.RequestID, pending.ToolCallID, pending.ModelCallID, completedToolCall); err != nil {
		return err
	}
	if err := service.syncSummaryCarryForward(stream.ConversationID, stream.RequestID, pending.ModelCallID); err != nil {
		return err
	}
	if err := service.publishCheckpointForce(stream.RequestID, stream.ConversationID); err != nil {
		return err
	}
	return service.reconcileStream(stream)
}

// buildDelegationCompletedTaskToolCall 根据委派结果 JSON 构造带 TaskResult 的 Task tool call。
// resultPayload 形如 {"aggregate_id":...,"status":"completed|failed|canceled","tasks":[...]}。
// Cursor 客户端在 subagentTaskState 判定中依赖 tool_call.result：
// 带成功/失败 Result 时 Task 显示为 succeeded/failed，缺 Result 时退化为 cancelled。
func delegationSubagentID(toolCallID string) string {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return "local-delegation:unknown"
	}
	return "local-delegation:" + toolCallID
}

func setTaskToolCallIdentity(toolCall *agentv1.ToolCall, agentID string) {
	if toolCall == nil || toolCall.GetTaskToolCall() == nil {
		return
	}
	args := toolCall.GetTaskToolCall().GetArgs()
	if args == nil {
		args = &agentv1.TaskArgs{}
		toolCall.GetTaskToolCall().Args = args
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	args.AgentId = &agentID
}

// clearTaskToolCallIdentity keeps locally aggregated Task cards from claiming
// a Cursor-native child conversation that was never opened.
func clearTaskToolCallIdentity(toolCall *agentv1.ToolCall) {
	if toolCall == nil || toolCall.GetTaskToolCall() == nil || toolCall.GetTaskToolCall().GetArgs() == nil {
		return
	}
	toolCall.GetTaskToolCall().GetArgs().AgentId = nil
}

func buildDelegationCompletedTaskToolCall(argsJSON []byte, resultPayload string, agentID string, durationMS uint64) *agentv1.ToolCall {
	var summary struct {
		Status    string                  `json:"status"`
		Error     string                  `json:"error"`
		Succeeded int                     `json:"succeeded"`
		Failed    int                     `json:"failed"`
		Canceled  int                     `json:"canceled"`
		Tasks     []delegatedWorkerResult `json:"tasks"`
	}
	_ = json.Unmarshal([]byte(resultPayload), &summary)
	status := strings.TrimSpace(summary.Status)
	var taskResult *agentv1.TaskResult
	// completed / partial_success 都算委派成功：partial_success 由 collectAggregate
	// 在部分 worker 成功时产出，若映射为失败会让用户看到 worker 成功但 Task 报错。
	if status == "completed" || status == "partial_success" {
		steps := delegationResultConversationSteps(summary.Tasks)
		success := &agentv1.TaskSuccess{
			ConversationSteps: steps,
			DurationMs:        &durationMS,
		}
		if agentID = strings.TrimSpace(agentID); agentID != "" {
			success.AgentId = stringPtr(agentID)
		}
		taskResult = &agentv1.TaskResult{
			Result: &agentv1.TaskResult_Success{
				Success: success,
			},
		}
		logger.Infof("forwarder delegation completed TaskResult request_result_status=%s worker_count=%d step_count=%d output_chars=%d",
			status, len(summary.Tasks), len(steps), conversationStepsTextChars(steps))
	} else {
		errorText := strings.TrimSpace(summary.Error)
		if errorText == "" {
			errorText = "delegated task " + status
		}
		taskResult = &agentv1.TaskResult{
			Result: &agentv1.TaskResult_Error{
				Error: &agentv1.TaskError{Error: errorText},
			},
		}
	}
	toolCall := &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_TaskToolCall{
			TaskToolCall: &agentv1.TaskToolCall{
				Args:   buildTaskArgsFromJSON(argsJSON),
				Result: taskResult,
			},
		},
	}
	setTaskToolCallIdentity(toolCall, agentID)
	return toolCall
}

// delegationResultConversationSteps 将本地 worker 的安全摘要转换为 Cursor
// TaskResult.success.conversation_steps。Cursor 的 Task UI 不会从自定义的
// aggregate JSON 自动提取内容；没有 conversation_steps 时会显示成功但结果区为空。
func delegationResultConversationSteps(tasks []delegatedWorkerResult) []*agentv1.ConversationStep {
	steps := make([]*agentv1.ConversationStep, 0, len(tasks))
	for _, task := range tasks {
		status := strings.TrimSpace(string(task.Status))
		if status == "" {
			status = "unknown"
		}
		parts := []string{
			fmt.Sprintf("[%s] %s", status, firstNonEmpty(strings.TrimSpace(task.ModelID), strings.TrimSpace(task.ModelGroupID), strings.TrimSpace(task.TaskID))),
		}
		if output := strings.TrimSpace(task.Output); output != "" {
			parts = append(parts, output)
		}
		if errText := strings.TrimSpace(task.Error); errText != "" {
			parts = append(parts, "错误："+errText)
		}
		steps = append(steps, &agentv1.ConversationStep{
			Message: &agentv1.ConversationStep_AssistantMessage{
				AssistantMessage: &agentv1.AssistantMessage{Text: strings.Join(parts, "\n")},
			},
		})
	}
	if len(steps) == 0 {
		steps = append(steps, &agentv1.ConversationStep{
			Message: &agentv1.ConversationStep_AssistantMessage{
				AssistantMessage: &agentv1.AssistantMessage{Text: "委派任务已完成，但 worker 未返回可见摘要。"},
			},
		})
	}
	return steps
}

func conversationStepsTextChars(steps []*agentv1.ConversationStep) int {
	chars := 0
	for _, step := range steps {
		if step != nil && step.GetAssistantMessage() != nil {
			chars += len([]rune(step.GetAssistantMessage().GetText()))
		}
	}
	return chars
}

func (coordinator *multitaskDelegationCoordinator) Close() {
	if coordinator == nil {
		return
	}
	coordinator.startMu.Lock()
	defer coordinator.startMu.Unlock()
	coordinator.mu.Lock()
	if coordinator.closed {
		coordinator.mu.Unlock()
		return
	}
	coordinator.closed = true
	aggregates := make([]*delegatedAggregate, 0, len(coordinator.aggregates))
	for _, aggregate := range coordinator.aggregates {
		aggregates = append(aggregates, aggregate)
	}
	coordinator.aggregates = make(map[string]*delegatedAggregate)
	coordinator.mu.Unlock()
	for _, aggregate := range aggregates {
		aggregate.cancelTasks()
	}
	if coordinator.supervisor != nil {
		coordinator.supervisor.Close()
	}
	if coordinator.scheduler != nil {
		coordinator.scheduler.Close()
	}
}

func (coordinator *multitaskDelegationCoordinator) runtimeConfig() delegation.RuntimeConfig {
	if coordinator == nil || coordinator.configProvider == nil {
		return delegation.NormalizeRuntimeConfig(delegation.RuntimeConfig{})
	}
	return delegation.NormalizeRuntimeConfig(coordinator.configProvider.DelegationRuntimeConfig())
}

func (coordinator *multitaskDelegationCoordinator) enabledWorkers(base delegation.TaskRequest, config delegation.RuntimeConfig) []delegation.TaskRequest {
	if !config.Enabled {
		return nil
	}
	workers := make([]delegation.TaskRequest, 0)
	seen := make(map[string]struct{})
	filterSelectedModels := delegatedConfigHasSelectedModel(config, base)
	for _, group := range config.Groups {
		if !group.Enabled {
			continue
		}
		if config.SupervisionEnabled && config.WorkerGroupID != "" && strings.TrimSpace(group.ID) != config.WorkerGroupID {
			continue
		}
		for _, modelID := range group.ModelIDs {
			modelID = strings.TrimSpace(modelID)
			if modelID == "" {
				continue
			}
			if filterSelectedModels && !delegatedModelSelected(base, modelID) {
				continue
			}
			mode := delegation.NormalizeExecutionMode(group.ExecutionMode)
			key := strings.Join([]string{group.ID, modelID, mode}, "\x00")
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			worker := base
			worker.ID = fmt.Sprintf("%s-worker-%d", base.ID, len(workers)+1)
			worker.ModelID = modelID
			worker.ModelName = firstNonEmpty(config.ModelNames[modelID], modelID)
			worker.ModelGroupID = strings.TrimSpace(group.ID)
			worker.ExecutionMode = mode
			worker.ToolPermission = cloneDelegatedPermissions(group.ToolPermissions)
			worker.QueueTimeout = delegatedWorkerQueueTimeout
			worker.Timeout = delegatedWorkerTimeout
			worker.ArgsJSON = rewriteDelegatedWorkerModel(worker.ArgsJSON, modelID)
			workers = append(workers, worker)
		}
	}
	return workers
}

func (coordinator *multitaskDelegationCoordinator) Start(stream *ActiveStream, pending runtimecore.PendingExec, base delegation.TaskRequest) (bool, error) {
	if coordinator == nil || stream == nil {
		return false, nil
	}
	coordinator.startMu.Lock()
	coordinator.mu.RLock()
	closed := coordinator.closed
	coordinator.mu.RUnlock()
	if closed {
		coordinator.startMu.Unlock()
		return false, fmt.Errorf("multitask delegation coordinator is closed")
	}
	config := coordinator.runtimeConfig()
	logger.Infof("forwarder delegation start evaluating request_id=%s exec_id=%s enabled=%t supervision=%t strict=%t groups=%d worker_group=%s max_concurrency=%d", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), config.Enabled, config.SupervisionEnabled, config.StrictUnavailable, len(config.Groups), strings.TrimSpace(config.WorkerGroupID), config.MaxConcurrency)
	coordinator.ensureScheduler(config.MaxConcurrency)
	useSupervision, supervisionErr := coordinator.shouldUseSupervision(base, config)
	if supervisionErr != nil {
		logger.Infof("forwarder delegation start blocked request_id=%s exec_id=%s reason=supervision_unavailable err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), supervisionErr)
		coordinator.startMu.Unlock()
		return false, supervisionErr
	}
	workers := coordinator.enabledWorkers(base, config)
	if len(workers) == 0 {
		logger.Infof("forwarder delegation start skipped request_id=%s exec_id=%s reason=no_enabled_workers enabled=%t groups=%d", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), config.Enabled, len(config.Groups))
		coordinator.startMu.Unlock()
		return false, nil
	}
	coordinator.mu.RLock()
	scheduler := coordinator.scheduler
	coordinator.mu.RUnlock()
	if scheduler == nil {
		logger.Infof("forwarder delegation start blocked request_id=%s exec_id=%s reason=scheduler_nil", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID))
		coordinator.startMu.Unlock()
		return false, fmt.Errorf("delegation scheduler is nil")
	}
	scheduler.ReserveRetentionMargin(len(workers))
	if useSupervision {
		if coordinator.supervisor == nil {
			logger.Infof("forwarder delegation start blocked request_id=%s exec_id=%s reason=supervisor_nil", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID))
			coordinator.startMu.Unlock()
			return false, fmt.Errorf("supervisor coordinator is unavailable")
		}
		stream.mu.Lock()
		if delegatedStartupCanceledLocked(stream, pending.ProviderPass) {
			logger.Infof("forwarder delegation start canceled request_id=%s exec_id=%s reason=startup_canceled provider_pass=%d", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), pending.ProviderPass)
			stream.mu.Unlock()
			coordinator.startMu.Unlock()
			return false, fmt.Errorf("delegation aggregate startup canceled for provider pass %d: %w", pending.ProviderPass, errProviderLoopInterrupted)
		}
		stream.PendingExecs[pending.ExecID] = pending
		stream.UpdatedAt = time.Now().UTC()
		stream.mu.Unlock()
		_, err := coordinator.supervisor.Start(stream, pending, base, workers, config)
		coordinator.startMu.Unlock()
		if err != nil {
			logger.Errorf("forwarder delegation supervisor start failed request_id=%s exec_id=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), err)
			coordinator.CancelAggregate(pending.ExecID)
			markExecCompleted(stream, pending)
			return false, err
		}
		return true, nil
	}

	aggregateID := strings.TrimSpace(pending.ExecID)
	ctx, cancel := context.WithCancel(context.Background())
	aggregate := &delegatedAggregate{
		id:        aggregateID,
		workerIDs: make([]string, 0, len(workers)),
		ctx:       ctx,
		cancel:    cancel,
		scheduler: scheduler,
	}
	stream.mu.Lock()
	if delegatedStartupCanceledLocked(stream, pending.ProviderPass) {
		logger.Infof("forwarder delegation start canceled request_id=%s exec_id=%s reason=startup_canceled provider_pass=%d", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), pending.ProviderPass)
		stream.mu.Unlock()
		coordinator.startMu.Unlock()
		cancel()
		return false, fmt.Errorf("delegation aggregate startup canceled for provider pass %d: %w", pending.ProviderPass, errProviderLoopInterrupted)
	}
	coordinator.mu.Lock()
	if _, exists := coordinator.aggregates[aggregateID]; exists {
		logger.Errorf("forwarder delegation start rejected request_id=%s exec_id=%s reason=aggregate_already_exists", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID))
		coordinator.mu.Unlock()
		stream.mu.Unlock()
		coordinator.startMu.Unlock()
		cancel()
		return false, fmt.Errorf("delegation aggregate %q already exists", aggregateID)
	}
	coordinator.aggregates[aggregateID] = aggregate
	coordinator.mu.Unlock()
	stream.PendingExecs[pending.ExecID] = pending
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	coordinator.startMu.Unlock()
	logger.Infof("forwarder delegation aggregate registered request_id=%s exec_id=%s worker_count=%d supervision=%t provider_pass=%d", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), len(workers), useSupervision, pending.ProviderPass)

	for _, worker := range workers {
		stream.mu.Lock()
		if delegatedStartupCanceledLocked(stream, pending.ProviderPass) {
			stream.mu.Unlock()
			aggregate.cancelTasks()
			break
		}
		submitted := aggregate.submitWorker(worker)
		stream.mu.Unlock()
		if !submitted {
			break
		}
	}
	snapshot := aggregate.finishStartup()
	logger.Errorf("forwarder delegation aggregate startup finished request_id=%s exec_id=%s worker_ids=%d submission_errors=%d canceled=%t", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), len(snapshot.workerIDs), len(snapshot.submissionErrors), snapshot.canceled)
	go coordinator.awaitAggregate(stream, pending, aggregate, snapshot)
	return true, nil
}

func (coordinator *multitaskDelegationCoordinator) ensureScheduler(maxConcurrency int) {
	if coordinator == nil {
		return
	}
	normalized := normalizedDelegationConcurrency(maxConcurrency)
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.closed {
		return
	}
	activeAggregates := len(coordinator.aggregates)
	if coordinator.supervisor != nil {
		activeAggregates += coordinator.supervisor.ActiveCount()
	}
	if coordinator.scheduler != nil && (coordinator.maxConcurrency == normalized || activeAggregates > 0) {
		return
	}
	if coordinator.scheduler != nil {
		coordinator.scheduler.Close()
	}
	coordinator.scheduler = delegation.NewScheduler(delegation.Config{MaxConcurrency: normalized}, coordinator.executeWorker)
	coordinator.supervisor = newSupervisorCoordinator(coordinator.service, coordinator.scheduler)
	coordinator.maxConcurrency = normalized
}

func (coordinator *multitaskDelegationCoordinator) executeWorker(ctx context.Context, request delegation.TaskRequest) delegation.TaskResult {
	ctx = delegation.WithWorkerVisibleUpdatePublisher(ctx, func(text string) bool {
		return coordinator.publishLocalWorkerProgress(request, text)
	})
	mode := delegation.NormalizeExecutionMode(request.ExecutionMode)
	switch mode {
	case delegation.ExecutionModeCursor:
		return coordinator.service.cursorDelegation.cursor.Execute(ctx, request)
	case delegation.ExecutionModeLocal, delegation.ExecutionModeAuto:
		return coordinator.service.localDelegation.Execute(ctx, request)
	default:
		return delegation.TaskResult{Error: fmt.Errorf("unsupported delegation execution mode %q", request.ExecutionMode)}
	}
}

func (coordinator *multitaskDelegationCoordinator) publishLocalWorkerProgress(request delegation.TaskRequest, text string) bool {
	if coordinator == nil || coordinator.service == nil {
		return false
	}
	requestID := strings.TrimSpace(request.ParentRequest)
	text = localDelegationVisibleProgress(text, request.WorkspaceHint)
	if requestID == "" || text == "" {
		return false
	}
	label := firstNonEmpty(strings.TrimSpace(request.Description), strings.TrimSpace(request.ModelName), strings.TrimSpace(request.ID), "worker")
	message := fmt.Sprintf("【委派任务：%s】%s\n", label, text)
	if err := coordinator.service.broker.Publish(requestID, StreamEvent{
		Message: buildThinkingDeltaMessage(message, agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT),
	}); err != nil {
		return false
	}
	return true
}

func (coordinator *multitaskDelegationCoordinator) awaitAggregate(stream *ActiveStream, pending runtimecore.PendingExec, aggregate *delegatedAggregate, snapshot delegatedAggregateSnapshot) {
	if coordinator == nil || aggregate == nil {
		return
	}
	defer coordinator.removeAggregate(aggregate)
	if !snapshot.startupDone {
		return
	}
	if snapshot.canceled {
		aggregate.cancelTasks()
		return
	}
	if len(snapshot.workerIDs) > 0 {
		err := aggregate.scheduler.WaitForTerminal(aggregate.ctx, snapshot.workerIDs)
		if err != nil {
			if errors.Is(err, context.Canceled) && aggregate.ctx.Err() != nil {
				aggregate.cancelTasks()
				return
			}
			waitErr := fmt.Errorf("delegation aggregate %q wait failed: %w", strings.TrimSpace(aggregate.id), err)
			logger.Errorf(
				"forwarder delegation aggregate wait failed request_id=%s aggregate_id=%s exec_id=%s err=%v",
				strings.TrimSpace(activeStreamRequestID(stream)),
				strings.TrimSpace(aggregate.id),
				strings.TrimSpace(pending.ExecID),
				waitErr,
			)
			_ = coordinator.service.failStreamIfNonTerminal(stream, "unknown", waitErr)
			return
		}
	}
	if aggregate.isCanceled() {
		aggregate.cancelTasks()
		return
	}
	result := coordinator.collectAggregate(aggregate, snapshot)
	payload, err := json.Marshal(result)
	if err != nil {
		payload = []byte(fmt.Sprintf(`{"aggregate_id":%q,"status":"failed","error":%q}`, aggregate.id, err.Error()))
	}
	postErr := coordinator.service.postStreamCommandAsync(stream, streamCommand{
		Kind: streamCommandDelegationResult,
		Delegation: &streamDelegationResult{
			AggregateID:  aggregate.id,
			ExecID:       pending.ExecID,
			ToolCallID:   pending.ToolCallID,
			ProviderPass: pending.ProviderPass,
			Payload:      string(payload),
		},
	})
	if postErr != nil {
		logger.Errorf(
			"forwarder delegation aggregate post failed request_id=%s aggregate_id=%s exec_id=%s err=%v",
			strings.TrimSpace(activeStreamRequestID(stream)),
			strings.TrimSpace(aggregate.id),
			strings.TrimSpace(pending.ExecID),
			postErr,
		)
		_ = coordinator.service.failStreamIfNonTerminal(stream, "unknown", postErr)
	}
}

func (coordinator *multitaskDelegationCoordinator) collectAggregate(aggregate *delegatedAggregate, snapshot delegatedAggregateSnapshot) delegatedAggregateResult {
	result := delegatedAggregateResult{AggregateID: aggregate.id, Tasks: append([]delegatedWorkerResult(nil), snapshot.submissionErrors...)}
	totalWorkers := len(snapshot.workerIDs) + len(snapshot.submissionErrors)
	workerOutputLimit := 0
	if totalWorkers > 0 {
		workerOutputLimit = (48 * projectedReplayKiB) / totalWorkers
	}
	for _, workerID := range snapshot.workerIDs {
		taskSnapshot, ok := aggregate.scheduler.Snapshot(workerID)
		if !ok {
			continue
		}
		output := ""
		if workerOutputLimit >= 128 {
			output = truncateProjectedReplayText("Task", taskSnapshot.Output, workerOutputLimit)
		}
		worker := delegatedWorkerResult{
			TaskID: taskSnapshot.ID, ModelID: taskSnapshot.ModelID, ModelGroupID: taskSnapshot.ModelGroupID,
			ExecutionMode: taskSnapshot.ExecutionMode, Status: taskSnapshot.Status,
			DurationMS: delegatedDuration(taskSnapshot), Output: output, Error: truncateProjectedReplayText("Task error", taskSnapshot.Error, 2048),
			ToolCallCount: taskSnapshot.ToolCallCount,
		}
		result.Tasks = append(result.Tasks, worker)
	}
	for _, worker := range result.Tasks {
		switch worker.Status {
		case delegation.TaskCompleted:
			result.Succeeded++
		case delegation.TaskCanceled, delegation.TaskTimedOut:
			result.Canceled++
		default:
			result.Failed++
		}
	}
	switch {
	case result.Succeeded == len(result.Tasks):
		result.Status = "completed"
	case result.Succeeded > 0:
		result.Status = "partial_success"
	case result.Canceled == len(result.Tasks):
		result.Status = "canceled"
	default:
		result.Status = "failed"
	}
	return result
}

func (coordinator *multitaskDelegationCoordinator) CancelAggregate(aggregateID string) {
	if coordinator == nil {
		return
	}
	coordinator.mu.RLock()
	aggregate := coordinator.aggregates[strings.TrimSpace(aggregateID)]
	coordinator.mu.RUnlock()
	caller := "unknown"
	if _, file, line, ok := runtime.Caller(1); ok {
		caller = fmt.Sprintf("%s:%d", file, line)
	}
	logger.Infof("forwarder delegation cancel aggregate requested aggregate_id=%s aggregate_found=%t caller=%s", strings.TrimSpace(aggregateID), aggregate != nil, caller)
	if aggregate == nil {
		if coordinator.supervisor != nil {
			coordinator.supervisor.Cancel(aggregateID)
		}
		return
	}
	aggregate.cancelTasks()
	if coordinator.supervisor != nil {
		coordinator.supervisor.Cancel(aggregateID)
	}
}

func (coordinator *multitaskDelegationCoordinator) CancelStream(stream *ActiveStream) {
	if coordinator == nil || stream == nil {
		return
	}
	coordinator.startMu.Lock()
	defer coordinator.startMu.Unlock()
	coordinator.mu.RLock()
	closed := coordinator.closed
	coordinator.mu.RUnlock()
	if closed {
		return
	}
	stream.mu.Lock()
	execIDs := make([]string, 0)
	providerPass := stream.ProviderPassCount
	stream.MultitaskStartupCanceled = true
	if providerPass > stream.MultitaskCanceledProviderPass {
		stream.MultitaskCanceledProviderPass = providerPass
	}
	for _, pending := range stream.PendingExecs {
		if strings.TrimSpace(pending.ExecKind) == "delegation_aggregate" {
			execIDs = append(execIDs, pending.ExecID)
		}
	}
	stream.mu.Unlock()
	caller := "unknown"
	if _, file, line, ok := runtime.Caller(1); ok {
		caller = fmt.Sprintf("%s:%d", file, line)
	}
	logger.Infof("forwarder delegation cancel stream request_id=%s provider_pass=%d aggregate_count=%d reason=stream_cancel caller=%s", strings.TrimSpace(stream.RequestID), providerPass, len(execIDs), caller)
	for _, execID := range execIDs {
		coordinator.CancelAggregate(execID)
	}
}

func (coordinator *multitaskDelegationCoordinator) shouldUseSupervision(base delegation.TaskRequest, config delegation.RuntimeConfig) (bool, error) {
	if coordinator == nil {
		return false, nil
	}
	config = delegation.NormalizeRuntimeConfig(config)
	if !config.Enabled || !config.SupervisionEnabled {
		return false, nil
	}
	modelID := resolveSupervisorModelID(config, base)
	if modelID == "" {
		if config.StrictUnavailable {
			return false, fmt.Errorf("delegation supervision requires a supervisor model")
		}
		return false, nil
	}
	if coordinator.supervisor == nil || coordinator.service == nil || coordinator.service.provider == nil {
		if config.StrictUnavailable {
			return false, fmt.Errorf("delegation supervision is unavailable")
		}
		return false, nil
	}
	return true, nil
}

func delegatedStartupCanceledLocked(stream *ActiveStream, providerPass int) bool {
	if stream == nil {
		return true
	}
	if isTerminalStreamStatus(stream.Status) {
		return true
	}
	switch stream.Phase {
	case TurnPhaseCanceled, TurnPhaseCompleted, TurnPhaseFailed:
		return true
	}
	if !stream.MultitaskStartupCanceled {
		return false
	}
	if providerPass <= stream.MultitaskCanceledProviderPass {
		return true
	}
	stream.MultitaskStartupCanceled = false
	stream.MultitaskCanceledProviderPass = 0
	return false
}

func (coordinator *multitaskDelegationCoordinator) Snapshots() []delegation.TaskSnapshot {
	if coordinator == nil {
		return nil
	}
	coordinator.mu.RLock()
	scheduler := coordinator.scheduler
	coordinator.mu.RUnlock()
	if scheduler == nil {
		return nil
	}
	return scheduler.Snapshots()
}

func (coordinator *multitaskDelegationCoordinator) CancelTask(taskID string) bool {
	if coordinator == nil {
		return false
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return false
	}
	if coordinator.supervisor != nil && coordinator.supervisor.CancelTask(taskID) {
		return true
	}
	coordinator.mu.RLock()
	scheduler := coordinator.scheduler
	coordinator.mu.RUnlock()
	if scheduler == nil {
		return false
	}
	return scheduler.CancelIfActive(taskID)
}

func (coordinator *multitaskDelegationCoordinator) removeAggregate(aggregate *delegatedAggregate) {
	if coordinator == nil || aggregate == nil {
		return
	}
	coordinator.mu.Lock()
	aggregateID := strings.TrimSpace(aggregate.id)
	if coordinator.aggregates[aggregateID] == aggregate {
		delete(coordinator.aggregates, aggregateID)
	}
	coordinator.mu.Unlock()
}

func (aggregate *delegatedAggregate) submitWorker(worker delegation.TaskRequest) bool {
	if aggregate == nil || aggregate.scheduler == nil {
		return false
	}
	aggregate.mu.Lock()
	defer aggregate.mu.Unlock()
	if aggregate.canceled || aggregate.ctx.Err() != nil {
		return false
	}
	workerID, err := aggregate.scheduler.Submit(worker)
	if err != nil {
		aggregate.submissionErrors = append(aggregate.submissionErrors, delegatedWorkerResult{
			TaskID: worker.ID, ModelID: worker.ModelID, ModelGroupID: worker.ModelGroupID,
			ExecutionMode: worker.ExecutionMode, Status: delegation.TaskFailed, Error: err.Error(),
		})
		return true
	}
	aggregate.workerIDs = append(aggregate.workerIDs, workerID)
	return true
}

func (aggregate *delegatedAggregate) finishStartup() delegatedAggregateSnapshot {
	if aggregate == nil {
		return delegatedAggregateSnapshot{}
	}
	aggregate.mu.Lock()
	aggregate.startupDone = true
	snapshot := aggregate.snapshotLocked()
	aggregate.mu.Unlock()
	return snapshot
}

func (aggregate *delegatedAggregate) isCanceled() bool {
	if aggregate == nil {
		return true
	}
	aggregate.mu.Lock()
	canceled := aggregate.canceled || aggregate.ctx.Err() != nil
	aggregate.mu.Unlock()
	return canceled
}

func (aggregate *delegatedAggregate) cancelTasks() {
	if aggregate == nil {
		return
	}
	aggregate.mu.Lock()
	if !aggregate.canceled {
		aggregate.canceled = true
		aggregate.cancel()
	}
	workerIDs := append([]string(nil), aggregate.workerIDs...)
	scheduler := aggregate.scheduler
	aggregate.mu.Unlock()
	if scheduler == nil {
		return
	}
	for _, workerID := range workerIDs {
		_ = scheduler.Cancel(workerID)
	}
}

func (aggregate *delegatedAggregate) snapshotLocked() delegatedAggregateSnapshot {
	return delegatedAggregateSnapshot{
		workerIDs:        append([]string(nil), aggregate.workerIDs...),
		submissionErrors: append([]delegatedWorkerResult(nil), aggregate.submissionErrors...),
		canceled:         aggregate.canceled || aggregate.ctx.Err() != nil,
		startupDone:      aggregate.startupDone,
	}
}

func delegatedStatusTerminal(status delegation.TaskStatus) bool {
	switch status {
	case delegation.TaskCompleted, delegation.TaskFailed, delegation.TaskCanceled, delegation.TaskTimedOut:
		return true
	default:
		return false
	}
}

func delegatedDuration(snapshot delegation.TaskSnapshot) int64 {
	if snapshot.StartedAt.IsZero() || snapshot.FinishedAt.IsZero() {
		return 0
	}
	return snapshot.FinishedAt.Sub(snapshot.StartedAt).Milliseconds()
}

func rewriteDelegatedWorkerModel(argsJSON []byte, modelID string) []byte {
	args, err := runtimecore.DecodeArgsMap(argsJSON)
	if err != nil {
		return append([]byte(nil), argsJSON...)
	}
	args["model"] = strings.TrimSpace(modelID)
	encoded, err := json.Marshal(args)
	if err != nil {
		return append([]byte(nil), argsJSON...)
	}
	return encoded
}

func cloneDelegatedPermissions(source map[string]bool) map[string]bool {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]bool, len(source))
	for name, allowed := range source {
		if name = strings.TrimSpace(name); name != "" {
			result[name] = allowed
		}
	}
	return result
}

func normalizedDelegationConcurrency(value int) int {
	if value <= 0 {
		return delegation.DefaultMaxConcurrency
	}
	return value
}

func delegatedModelSelected(base delegation.TaskRequest, modelID string) bool {
	if len(base.SelectedSubagentModels) == 0 {
		return true
	}
	selected := make(map[string]struct{}, len(base.SelectedSubagentModels)*2)
	for _, model := range base.SelectedSubagentModels {
		if model == nil {
			continue
		}
		if value := strings.TrimSpace(model.GetModelId()); value != "" {
			selected[value] = struct{}{}
		}
	}
	for _, detail := range base.SelectedSubagentModelDetails {
		if detail == nil {
			continue
		}
		canonical := strings.TrimSpace(detail.GetModelId())
		display := strings.TrimSpace(detail.GetDisplayModelId())
		_, canonicalSelected := selected[canonical]
		_, displaySelected := selected[display]
		if canonicalSelected || displaySelected {
			if canonical != "" {
				selected[canonical] = struct{}{}
			}
			if display != "" {
				selected[display] = struct{}{}
			}
		}
	}
	_, ok := selected[strings.TrimSpace(modelID)]
	return ok
}

func delegatedConfigHasSelectedModel(config delegation.RuntimeConfig, base delegation.TaskRequest) bool {
	if len(base.SelectedSubagentModels) == 0 {
		return false
	}
	for _, group := range config.Groups {
		if !group.Enabled {
			continue
		}
		for _, modelID := range group.ModelIDs {
			if delegatedModelSelected(base, modelID) {
				return true
			}
		}
	}
	return false
}
