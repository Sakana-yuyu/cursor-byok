package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
)

const delegatedWorkerTimeout = 30 * time.Minute

type streamDelegationResult struct {
	AggregateID  string
	ExecID       string
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
		return false, nil
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
	base := buildDelegatedCursorTaskRequest(stream, pending, invocation, delegation.ExecutionModeAuto, "")
	base.ID = pending.ExecID
	base.Mode = agentv1.AgentMode_AGENT_MODE_AGENT
	base.ModelName = modelName
	base.ThinkingEffort = thinkingEffort
	base.MaxMode = maxMode

	started, err := service.multitaskDelegation.Start(stream, pending, base)
	if err != nil || !started {
		return false, err
	}
	service.recordExecDispatchMetadata(stream, pending, false, true, "delegation_scheduler")
	if err := service.publishCheckpoint(stream.RequestID, stream.ConversationID); err != nil {
		service.multitaskDelegation.CancelAggregate(pending.ExecID)
		markExecCompleted(stream, pending)
		return false, err
	}
	return true, nil
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
	if pending.ProviderPass != payload.ProviderPass {
		return nil
	}
	if service.ignoreStaleExecProviderPass(stream, pending, "delegation_result") {
		return nil
	}
	markExecCompleted(stream, pending)
	resultPayload := strings.TrimSpace(payload.Payload)
	if resultPayload == "" {
		resultPayload = fmt.Sprintf(`{"aggregate_id":%q,"status":"failed","error":"empty delegation result"}`, payload.AggregateID)
	}
	if err := service.appendToolResult(stream, pending.ToolCallID, "Task", pending.ArgsJSON, resultPayload, pending.ReasoningContent, nil); err != nil {
		return err
	}
	if err := service.publishToolCallCompleted(stream.RequestID, pending.ToolCallID, pending.ModelCallID, nil); err != nil {
		return err
	}
	if err := service.syncSummaryCarryForward(stream.ConversationID, stream.RequestID, pending.ModelCallID); err != nil {
		return err
	}
	if err := service.publishCheckpoint(stream.RequestID, stream.ConversationID); err != nil {
		return err
	}
	return service.reconcileStream(stream)
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
	coordinator.ensureScheduler(config.MaxConcurrency)
	workers := coordinator.enabledWorkers(base, config)
	if len(workers) == 0 {
		coordinator.startMu.Unlock()
		return false, nil
	}
	coordinator.mu.RLock()
	scheduler := coordinator.scheduler
	coordinator.mu.RUnlock()
	if scheduler == nil {
		coordinator.startMu.Unlock()
		return false, fmt.Errorf("delegation scheduler is nil")
	}
	scheduler.EnsureRetentionLimit(len(scheduler.Snapshots()) + len(workers) + delegation.DefaultRetentionLimit)
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
		stream.mu.Unlock()
		coordinator.startMu.Unlock()
		cancel()
		return false, fmt.Errorf("delegation aggregate startup canceled for provider pass %d: %w", pending.ProviderPass, errProviderLoopInterrupted)
	}
	coordinator.mu.Lock()
	if _, exists := coordinator.aggregates[aggregateID]; exists {
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

	if useSupervision, supervisionErr := coordinator.shouldUseSupervision(base, config); supervisionErr != nil {
		coordinator.CancelAggregate(pending.ExecID)
		markExecCompleted(stream, pending)
		return false, supervisionErr
	} else if useSupervision {
		if coordinator.supervisor == nil {
			coordinator.CancelAggregate(pending.ExecID)
			markExecCompleted(stream, pending)
			return false, fmt.Errorf("supervisor coordinator is unavailable")
		}
		if _, err := coordinator.supervisor.Start(stream, pending, base, workers, config); err != nil {
			coordinator.CancelAggregate(pending.ExecID)
			markExecCompleted(stream, pending)
			return false, err
		}
		return true, nil
	}

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
			log.Printf(
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
			ProviderPass: pending.ProviderPass,
			Payload:      string(payload),
		},
	})
	if postErr != nil {
		log.Printf(
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
