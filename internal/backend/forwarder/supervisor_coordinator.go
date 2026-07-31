package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
)

type SupervisorCoordinator struct {
	service   *Service
	scheduler *delegation.Scheduler
	provider  *supervisorProviderAdapter

	mu         sync.RWMutex
	aggregates map[string]*supervisedAggregate
	closed     bool
}

type supervisedAggregate struct {
	coordinator *SupervisorCoordinator
	id          string
	stream      *ActiveStream
	pending     runtimecore.PendingExec
	base        delegation.TaskRequest
	config      delegation.RuntimeConfig
	ctx         context.Context
	cancel      context.CancelFunc
	eventCh     chan supervisorAggregateEvent

	mu               sync.Mutex
	submissionErrors []delegatedWorkerResult
	tasks            map[string]*supervisedTaskState
}

type supervisedTaskState struct {
	logicalID        string
	currentTaskID    string
	currentRequest   delegation.TaskRequest
	contract         delegation.SupervisionTaskContract
	round            int
	corrections      int
	retries          int
	reassignments    int
	escalated        bool
	escalationUsed   bool
	lastSnapshot     delegation.TaskSnapshot
	lastResult       delegation.TaskResult
	lastIssue        *delegation.SupervisionIssue
	previous         *delegation.WorkerCheckpoint
	reviewPending    bool
	completed        bool
	recentSignatures []string
}

type supervisorAggregateEvent struct {
	kind     string
	identity supervisorTaskIdentity
	snapshot delegation.TaskSnapshot
	result   delegation.TaskResult
	issue    *delegation.SupervisionIssue
	decision delegation.SupervisionDecision
	err      error
}

type supervisorTaskIdentity struct {
	AggregateID  string
	LogicalID    string
	TaskID       string
	ParentExec   string
	ProviderPass int
	Round        int
}

func newSupervisorCoordinator(service *Service, scheduler *delegation.Scheduler) *SupervisorCoordinator {
	if service == nil || scheduler == nil {
		return nil
	}
	return &SupervisorCoordinator{
		service:    service,
		scheduler:  scheduler,
		provider:   newSupervisorProviderAdapter(service),
		aggregates: make(map[string]*supervisedAggregate),
	}
}

func (coordinator *SupervisorCoordinator) Start(stream *ActiveStream, pending runtimecore.PendingExec, base delegation.TaskRequest, workers []delegation.TaskRequest, config delegation.RuntimeConfig) (string, error) {
	if coordinator == nil || stream == nil {
		return "", fmt.Errorf("supervisor coordinator is unavailable")
	}
	aggregateID := strings.TrimSpace(pending.ExecID)
	if aggregateID == "" {
		return "", fmt.Errorf("delegation aggregate exec id is required")
	}
	if !supervisedParentExecStillCurrent(stream, pending) {
		return "", fmt.Errorf("delegation aggregate startup canceled for provider pass %d: %w", pending.ProviderPass, errProviderLoopInterrupted)
	}
	ctx, cancel := context.WithCancel(context.Background())
	aggregate := &supervisedAggregate{
		coordinator: coordinator,
		id:          aggregateID,
		stream:      stream,
		pending:     pending,
		base:        base,
		config:      delegation.NormalizeRuntimeConfig(config),
		ctx:         ctx,
		cancel:      cancel,
		eventCh:     make(chan supervisorAggregateEvent, max(16, len(workers)*4)),
		tasks:       make(map[string]*supervisedTaskState, len(workers)),
	}
	for _, worker := range workers {
		state := aggregate.newTaskState(worker)
		aggregate.tasks[state.logicalID] = state
	}

	coordinator.mu.Lock()
	if coordinator.closed {
		coordinator.mu.Unlock()
		cancel()
		return "", fmt.Errorf("supervisor coordinator is closed")
	}
	if !supervisedParentExecStillCurrent(stream, pending) {
		coordinator.mu.Unlock()
		cancel()
		return "", fmt.Errorf("delegation aggregate startup canceled for provider pass %d: %w", pending.ProviderPass, errProviderLoopInterrupted)
	}
	if _, exists := coordinator.aggregates[aggregateID]; exists {
		coordinator.mu.Unlock()
		cancel()
		return "", fmt.Errorf("supervised aggregate %q already exists", aggregateID)
	}
	coordinator.aggregates[aggregateID] = aggregate
	coordinator.mu.Unlock()
	go aggregate.run()
	go aggregate.dispatchInitialWorkers()
	return aggregateID, nil
}

func (coordinator *SupervisorCoordinator) Cancel(aggregateID string) {
	if coordinator == nil {
		return
	}
	coordinator.mu.RLock()
	aggregate := coordinator.aggregates[strings.TrimSpace(aggregateID)]
	coordinator.mu.RUnlock()
	if aggregate == nil {
		return
	}
	aggregate.cancelTasks()
}

func (coordinator *SupervisorCoordinator) Close() {
	if coordinator == nil {
		return
	}
	coordinator.mu.Lock()
	if coordinator.closed {
		coordinator.mu.Unlock()
		return
	}
	coordinator.closed = true
	aggregates := make([]*supervisedAggregate, 0, len(coordinator.aggregates))
	for _, aggregate := range coordinator.aggregates {
		aggregates = append(aggregates, aggregate)
	}
	coordinator.aggregates = make(map[string]*supervisedAggregate)
	coordinator.mu.Unlock()
	for _, aggregate := range aggregates {
		aggregate.cancelTasks()
	}
}

func (coordinator *SupervisorCoordinator) ActiveCount() int {
	if coordinator == nil {
		return 0
	}
	coordinator.mu.RLock()
	defer coordinator.mu.RUnlock()
	return len(coordinator.aggregates)
}

func (coordinator *SupervisorCoordinator) removeAggregate(aggregateID string, aggregate *supervisedAggregate) {
	if coordinator == nil {
		return
	}
	coordinator.mu.Lock()
	if coordinator.aggregates[strings.TrimSpace(aggregateID)] == aggregate {
		delete(coordinator.aggregates, strings.TrimSpace(aggregateID))
	}
	coordinator.mu.Unlock()
}

func (aggregate *supervisedAggregate) newTaskState(worker delegation.TaskRequest) *supervisedTaskState {
	contract := buildWorkerSupervisionContract(aggregate.id, aggregate.base, worker, aggregate.config)
	worker.Contract = &contract
	return &supervisedTaskState{
		logicalID:      strings.TrimSpace(worker.ID),
		currentTaskID:  strings.TrimSpace(worker.ID),
		currentRequest: worker,
		contract:       contract,
		round:          contract.Round,
	}
}

func (aggregate *supervisedAggregate) dispatchInitialWorkers() {
	if aggregate == nil {
		return
	}
	aggregate.mu.Lock()
	tasks := make([]*supervisedTaskState, 0, len(aggregate.tasks))
	for _, task := range aggregate.tasks {
		tasks = append(tasks, task)
	}
	aggregate.mu.Unlock()
	for _, task := range tasks {
		if aggregate.ctx.Err() != nil || !aggregate.parentExecStillCurrent() {
			aggregate.cancelTasks()
			return
		}
		if err := aggregate.submitAttempt(task, "initial", ""); err != nil {
			if errors.Is(err, errProviderLoopInterrupted) || !aggregate.parentExecStillCurrent() {
				aggregate.cancelTasks()
				return
			}
			aggregate.markTaskFailed(task, delegation.SupervisionIssueModelFailure, err.Error(), false)
		}
	}
	if aggregate.allTasksCompleted() {
		aggregate.postEvent(supervisorAggregateEvent{kind: "startup_complete"})
	}
}

func (aggregate *supervisedAggregate) submitAttempt(task *supervisedTaskState, reason string, extraPrompt string) error {
	if aggregate == nil || task == nil || aggregate.coordinator == nil || aggregate.coordinator.scheduler == nil {
		return fmt.Errorf("supervised delegation scheduler is unavailable")
	}
	if aggregate.ctx.Err() != nil || !aggregate.parentExecStillCurrent() {
		return fmt.Errorf("delegation aggregate startup canceled for provider pass %d: %w", aggregate.pending.ProviderPass, errProviderLoopInterrupted)
	}
	request := task.currentRequest
	request.ParentRequest = strings.TrimSpace(aggregate.base.ParentRequest)
	request.ParentExecID = strings.TrimSpace(aggregate.id)
	request.Contract = cloneSupervisionContract(task.contract)
	request.ID = strings.TrimSpace(task.currentTaskID)
	if extraPrompt = strings.TrimSpace(extraPrompt); extraPrompt != "" {
		request.Prompt = strings.TrimSpace(request.Prompt + "\n\nSupervisor correction:\n" + extraPrompt)
	}
	task.currentRequest = request
	if _, err := aggregate.coordinator.scheduler.Submit(request); err != nil {
		return err
	}
	aggregate.startWait(task, supervisorTaskIdentity{
		AggregateID:  aggregate.id,
		LogicalID:    task.logicalID,
		TaskID:       task.currentTaskID,
		ParentExec:   aggregate.id,
		ProviderPass: aggregate.pending.ProviderPass,
		Round:        task.round,
	})
	log.Printf(
		"forwarder supervised task submitted request_id=%s aggregate_id=%s task_id=%s logical_id=%s round=%d reason=%s",
		strings.TrimSpace(activeStreamRequestID(aggregate.stream)),
		strings.TrimSpace(aggregate.id),
		strings.TrimSpace(task.currentTaskID),
		strings.TrimSpace(task.logicalID),
		task.round,
		strings.TrimSpace(reason),
	)
	return nil
}

func (aggregate *supervisedAggregate) startWait(task *supervisedTaskState, identity supervisorTaskIdentity) {
	if aggregate == nil || task == nil || aggregate.coordinator == nil || aggregate.coordinator.scheduler == nil {
		return
	}
	go func() {
		err := aggregate.coordinator.scheduler.WaitForTerminal(aggregate.ctx, []string{identity.TaskID})
		if err != nil {
			if errors.Is(err, context.Canceled) || aggregate.ctx.Err() != nil {
				return
			}
			aggregate.postEvent(supervisorAggregateEvent{kind: "worker_terminal", identity: identity, err: err})
			return
		}
		snapshot, ok := aggregate.coordinator.scheduler.Snapshot(identity.TaskID)
		if !ok {
			aggregate.postEvent(supervisorAggregateEvent{kind: "worker_terminal", identity: identity, err: fmt.Errorf("supervised task snapshot %q disappeared", identity.TaskID)})
			return
		}
		result, _ := aggregate.coordinator.scheduler.Result(identity.TaskID)
		aggregate.postEvent(supervisorAggregateEvent{
			kind:     "worker_terminal",
			identity: identity,
			snapshot: snapshot,
			result:   result,
		})
	}()
}

func (aggregate *supervisedAggregate) run() {
	if aggregate == nil {
		return
	}
	defer aggregate.coordinator.removeAggregate(aggregate.id, aggregate)
	for {
		if aggregate.ctx.Err() != nil {
			aggregate.cancelTasks()
			return
		}
		if aggregate.allTasksCompleted() {
			aggregate.finish()
			return
		}
		select {
		case <-aggregate.ctx.Done():
			aggregate.cancelTasks()
			return
		case event := <-aggregate.eventCh:
			aggregate.handleEvent(event)
		}
	}
}

func (aggregate *supervisedAggregate) handleEvent(event supervisorAggregateEvent) {
	switch event.kind {
	case "worker_terminal":
		aggregate.handleWorkerTerminal(event)
	case "review_abandoned":
		aggregate.handleReviewAbandoned(event)
	case "review_result":
		aggregate.handleReviewResult(event)
	case "startup_complete":
		return
	}
}

func (aggregate *supervisedAggregate) handleWorkerTerminal(event supervisorAggregateEvent) {
	task, ok := aggregate.matchIdentity(event.identity)
	if !ok {
		return
	}
	if !aggregate.parentExecStillCurrent() {
		if event.err == nil {
			task.lastSnapshot = event.snapshot
			task.lastResult = event.result
		}
		aggregate.markTaskAbandoned(task, delegation.SupervisionIssueModelFailure, staleEventReason("stale worker result ignored after provider pass changed", firstNonEmpty(supervisorErrorString(event.err), strings.TrimSpace(event.snapshot.Error))))
		return
	}
	if event.err != nil {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueModelFailure, event.err.Error(), false)
		return
	}
	if !aggregate.resultIdentityMatches(task, event.identity, event.snapshot, event.result) {
		return
	}
	previousSnapshot := task.lastSnapshot
	previousResult := task.lastResult
	previousCheckpoint := cloneCheckpointPointer(task.previous)
	currentCheckpoint := resolveReviewCheckpoint(task.contract, event.snapshot)
	issue := detectSupervisionIssue(task, currentCheckpoint, event.snapshot, event.result, previousCheckpoint, previousSnapshot, previousResult)
	task.lastSnapshot = event.snapshot
	task.lastResult = event.result
	task.lastIssue = issue
	task.previous = cloneCheckpointPointer(&currentCheckpoint)
	task.reviewPending = true
	go aggregate.reviewTask(event.identity, task.contract, event.snapshot, event.result, issue, aggregate.allowedActions(task, event.snapshot))
}

func (aggregate *supervisedAggregate) reviewTask(identity supervisorTaskIdentity, contract delegation.SupervisionTaskContract, snapshot delegation.TaskSnapshot, result delegation.TaskResult, issue *delegation.SupervisionIssue, allowed []delegation.SupervisionDecisionKind) {
	if aggregate == nil {
		return
	}
	if _, ok := aggregate.matchIdentity(identity); !ok || !aggregate.parentExecStillCurrent() {
		aggregate.postReviewAbandoned(identity)
		return
	}
	provider := aggregate.coordinator.provider
	decision := delegation.SupervisionDecision{}
	var err error
	if provider == nil {
		err = &supervisorReviewError{kind: supervisorReviewErrorUnavailable, message: boundedSupervisorError(errSupervisorUnavailable)}
	} else {
		modelID := resolveSupervisorModelID(aggregate.config, aggregate.base)
		reviewDecision, reviewErr := provider.Review(aggregate.ctx, supervisorReviewInput{
			AggregateID:    aggregate.id,
			TaskID:         identity.TaskID,
			ParentExecID:   aggregate.id,
			ProviderPass:   identity.ProviderPass,
			Contract:       contract,
			Checkpoint:     resolveReviewCheckpoint(contract, snapshot),
			Result:         result,
			Snapshot:       snapshot,
			Issue:          issue,
			AllowedActions: append([]delegation.SupervisionDecisionKind(nil), allowed...),
			ModelID:        modelID,
			ModelName:      firstNonEmpty(aggregate.config.ModelNames[modelID], modelID),
			ThinkingEffort: strings.TrimSpace(aggregate.base.ThinkingEffort),
			MaxMode:        aggregate.base.MaxMode,
		})
		if reviewErr != nil {
			err = reviewErr
		} else {
			decision = reviewDecision
		}
	}
	if aggregate.ctx.Err() != nil || !aggregate.parentExecStillCurrent() {
		aggregate.postReviewAbandoned(identity)
		return
	}
	if _, ok := aggregate.matchIdentity(identity); !ok {
		aggregate.postReviewAbandoned(identity)
		return
	}
	if err != nil {
		if unavailable, ok := err.(*supervisorReviewError); ok && unavailable.kind == supervisorReviewErrorUnavailable && !aggregate.config.StrictUnavailable {
			decision = delegation.SupervisionDecision{
				Kind:   delegation.SupervisionDecisionAccept,
				Reason: "supervisor provider unavailable; preserving legacy behavior",
				At:     time.Now().UTC(),
			}
		} else {
			decision = delegation.SupervisionDecision{
				Kind:   delegation.SupervisionDecisionCircuitOpen,
				Reason: boundedSupervisorError(err),
				At:     time.Now().UTC(),
			}
		}
	}
	aggregate.postEvent(supervisorAggregateEvent{
		kind:     "review_result",
		identity: identity,
		decision: decision,
		issue:    issue,
		err:      err,
	})
}

func (aggregate *supervisedAggregate) handleReviewAbandoned(event supervisorAggregateEvent) {
	task, ok := aggregate.matchIdentity(event.identity)
	if !ok {
		return
	}
	task.reviewPending = false
	if task.completed {
		return
	}
	aggregate.settleCurrentTask(task)
}

func (aggregate *supervisedAggregate) handleReviewResult(event supervisorAggregateEvent) {
	task, ok := aggregate.matchIdentity(event.identity)
	if !ok {
		return
	}
	if !aggregate.parentExecStillCurrent() {
		aggregate.markTaskAbandoned(task, issueCodeOrDefault(event.issue, delegation.SupervisionIssueReviewFailure), staleEventReason("stale supervisor review ignored after provider pass changed", firstNonEmpty(strings.TrimSpace(event.decision.Reason), boundedSupervisorError(event.err), issueSummary(event.issue))))
		return
	}
	task.reviewPending = false
	decision := normalizeDelegationDecision(event.decision)
	if decision.Kind == "" {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueReviewFailure, firstNonEmpty(boundedSupervisorError(event.err), "supervisor decision was empty"), true)
		return
	}
	switch decision.Kind {
	case delegation.SupervisionDecisionAccept:
		aggregate.settleCurrentTask(task)
	case delegation.SupervisionDecisionContinue:
		if delegatedStatusTerminal(task.lastSnapshot.Status) && task.lastSnapshot.Status != delegation.TaskCompleted {
			aggregate.retryTask(task, "continue_after_terminal", "")
			return
		}
		aggregate.settleCurrentTask(task)
	case delegation.SupervisionDecisionCorrect:
		aggregate.correctTask(task, decision)
	case delegation.SupervisionDecisionRetry:
		aggregate.retryTask(task, "retry", "")
	case delegation.SupervisionDecisionReassign:
		aggregate.reassignTask(task, decision)
	case delegation.SupervisionDecisionEscalate:
		aggregate.escalateTask(task, decision)
	case delegation.SupervisionDecisionCircuitOpen:
		aggregate.markTaskFailed(task, issueCodeOrDefault(event.issue, delegation.SupervisionIssueReviewFailure), decision.Reason, true)
	default:
		aggregate.markTaskFailed(task, delegation.SupervisionIssueReviewFailure, firstNonEmpty(decision.Reason, "supervisor returned an unsupported decision"), true)
	}
}

func (aggregate *supervisedAggregate) correctTask(task *supervisedTaskState, decision delegation.SupervisionDecision) {
	if task == nil {
		return
	}
	if task.corrections >= task.contract.MaxCorrections {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueCorrectionLimit, "supervisor correction limit exceeded", true)
		return
	}
	if task.round >= task.contract.MaxRounds {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueRoundLimit, "supervision round limit exceeded", true)
		return
	}
	task.corrections++
	aggregate.spawnFollowup(task, "correct", decision.Summary, "", "", "")
}

func (aggregate *supervisedAggregate) retryTask(task *supervisedTaskState, reason string, prompt string) {
	if task == nil {
		return
	}
	if task.retries >= task.contract.MaxRetries {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueRetryLimit, "supervisor retry limit exceeded", true)
		return
	}
	if task.round >= task.contract.MaxRounds {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueRoundLimit, "supervision round limit exceeded", true)
		return
	}
	task.retries++
	aggregate.spawnFollowup(task, reason, prompt, "", "", "")
}

func (aggregate *supervisedAggregate) reassignTask(task *supervisedTaskState, decision delegation.SupervisionDecision) {
	if task == nil || !aggregate.config.AllowReassign {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueReviewFailure, "supervisor reassignment is disabled", true)
		return
	}
	if task.round >= task.contract.MaxRounds {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueRoundLimit, "supervision round limit exceeded", true)
		return
	}
	modelID, groupID, executionMode := aggregate.nextReassignedModel(task)
	if modelID == "" {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueModelFailure, "no reassignment target is available", true)
		return
	}
	task.reassignments++
	aggregate.spawnFollowup(task, "reassign", decision.Summary, modelID, groupID, executionMode)
}

func (aggregate *supervisedAggregate) escalateTask(task *supervisedTaskState, decision delegation.SupervisionDecision) {
	if task == nil || !aggregate.config.AllowEscalate {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueReviewFailure, "supervisor escalation is disabled", true)
		return
	}
	if task.escalationUsed {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueReviewFailure, "supervisor escalation already used", true)
		return
	}
	if task.round >= task.contract.MaxRounds {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueRoundLimit, "supervision round limit exceeded", true)
		return
	}
	modelID := firstNonEmpty(strings.TrimSpace(aggregate.config.ReviewerModelID), strings.TrimSpace(aggregate.config.SupervisorModelID), strings.TrimSpace(aggregate.base.ModelID))
	if modelID == "" {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueModelFailure, "no escalation model is available", true)
		return
	}
	task.escalationUsed = true
	task.escalated = true
	aggregate.spawnFollowup(task, "escalate", decision.Summary, modelID, "", "")
}

func (aggregate *supervisedAggregate) spawnFollowup(task *supervisedTaskState, reason string, prompt string, modelID string, groupOverride string, executionMode string) {
	if task == nil {
		return
	}
	task.round++
	task.contract.Round = task.round
	request := task.currentRequest
	request.ID = fmt.Sprintf("%s-r%d", task.logicalID, task.round)
	if strings.TrimSpace(modelID) != "" {
		request.ModelID = strings.TrimSpace(modelID)
		request.ModelName = firstNonEmpty(aggregate.config.ModelNames[request.ModelID], request.ModelID)
		request.ArgsJSON = rewriteDelegatedWorkerModel(request.ArgsJSON, request.ModelID)
	}
	if strings.TrimSpace(groupOverride) != "" {
		request.ModelGroupID = strings.TrimSpace(groupOverride)
	}
	if strings.TrimSpace(executionMode) != "" {
		request.ExecutionMode = delegation.NormalizeExecutionMode(executionMode)
	}
	task.currentTaskID = request.ID
	task.currentRequest = request
	if err := aggregate.submitAttempt(task, reason, prompt); err != nil {
		if errors.Is(err, errProviderLoopInterrupted) || !aggregate.parentExecStillCurrent() {
			aggregate.cancelTasks()
			return
		}
		aggregate.markTaskFailed(task, delegation.SupervisionIssueModelFailure, err.Error(), false)
	}
}

func (aggregate *supervisedAggregate) nextReassignedModel(task *supervisedTaskState) (string, string, string) {
	currentModelID := strings.TrimSpace(task.currentRequest.ModelID)
	currentGroupID := strings.TrimSpace(task.currentRequest.ModelGroupID)
	preferredGroup := strings.TrimSpace(aggregate.config.WorkerGroupID)
	for _, group := range aggregate.config.Groups {
		if !group.Enabled {
			continue
		}
		if preferredGroup != "" && strings.TrimSpace(group.ID) != preferredGroup {
			continue
		}
		for _, modelID := range group.ModelIDs {
			modelID = strings.TrimSpace(modelID)
			if modelID == "" || (modelID == currentModelID && strings.TrimSpace(group.ID) == currentGroupID) {
				continue
			}
			return modelID, strings.TrimSpace(group.ID), delegation.NormalizeExecutionMode(group.ExecutionMode)
		}
	}
	for _, group := range aggregate.config.Groups {
		if !group.Enabled {
			continue
		}
		for _, modelID := range group.ModelIDs {
			modelID = strings.TrimSpace(modelID)
			if modelID == "" || modelID == currentModelID {
				continue
			}
			return modelID, strings.TrimSpace(group.ID), delegation.NormalizeExecutionMode(group.ExecutionMode)
		}
	}
	return "", "", ""
}

func (aggregate *supervisedAggregate) allowedActions(task *supervisedTaskState, snapshot delegation.TaskSnapshot) []delegation.SupervisionDecisionKind {
	if aggregate == nil || task == nil {
		return []delegation.SupervisionDecisionKind{
			delegation.SupervisionDecisionAccept,
			delegation.SupervisionDecisionCircuitOpen,
		}
	}
	actions := []delegation.SupervisionDecisionKind{
		delegation.SupervisionDecisionAccept,
		delegation.SupervisionDecisionCircuitOpen,
	}
	canContinueWithoutNewPass := !delegatedStatusTerminal(snapshot.Status) || snapshot.Status == delegation.TaskCompleted
	canSpawnFollowup := task.round < task.contract.MaxRounds
	canRetry := canSpawnFollowup && task.retries < task.contract.MaxRetries
	canCorrect := canSpawnFollowup && task.corrections < task.contract.MaxCorrections
	if canContinueWithoutNewPass || canRetry {
		actions = append(actions, delegation.SupervisionDecisionContinue)
	}
	if canCorrect {
		actions = append(actions, delegation.SupervisionDecisionCorrect)
	}
	if canRetry {
		actions = append(actions, delegation.SupervisionDecisionRetry)
	}
	if aggregate.config.AllowReassign && canSpawnFollowup {
		if modelID, _, _ := aggregate.nextReassignedModel(task); modelID != "" {
			actions = append(actions, delegation.SupervisionDecisionReassign)
		}
	}
	if aggregate.config.AllowEscalate && canSpawnFollowup && !task.escalationUsed {
		modelID := firstNonEmpty(strings.TrimSpace(aggregate.config.ReviewerModelID), strings.TrimSpace(aggregate.config.SupervisorModelID), strings.TrimSpace(aggregate.base.ModelID))
		if modelID != "" {
			actions = append(actions, delegation.SupervisionDecisionEscalate)
		}
	}
	if len(actions) == 0 {
		actions = append(actions, delegation.SupervisionDecisionCircuitOpen)
	}
	return actions
}

func (aggregate *supervisedAggregate) allTasksCompleted() bool {
	aggregate.mu.Lock()
	defer aggregate.mu.Unlock()
	if len(aggregate.tasks) == 0 {
		return true
	}
	for _, task := range aggregate.tasks {
		if !task.completed || task.reviewPending {
			return false
		}
	}
	return true
}

func (aggregate *supervisedAggregate) finish() {
	if aggregate == nil || !aggregate.parentExecStillCurrent() {
		return
	}
	result := aggregate.collectResult()
	payload, err := json.Marshal(result)
	if err != nil {
		payload = []byte(fmt.Sprintf(`{"aggregate_id":%q,"status":"failed","error":%q}`, aggregate.id, err.Error()))
	}
	postErr := aggregate.coordinator.service.postStreamCommandAsync(aggregate.stream, streamCommand{
		Kind: streamCommandDelegationResult,
		Delegation: &streamDelegationResult{
			AggregateID:  aggregate.id,
			ExecID:       aggregate.pending.ExecID,
			ProviderPass: aggregate.pending.ProviderPass,
			Payload:      string(payload),
		},
	})
	if postErr != nil {
		log.Printf(
			"forwarder supervised delegation aggregate post failed request_id=%s aggregate_id=%s exec_id=%s err=%v",
			strings.TrimSpace(activeStreamRequestID(aggregate.stream)),
			strings.TrimSpace(aggregate.id),
			strings.TrimSpace(aggregate.pending.ExecID),
			postErr,
		)
		_ = aggregate.coordinator.service.failStreamIfNonTerminal(aggregate.stream, "unknown", postErr)
	}
}

func (aggregate *supervisedAggregate) collectResult() delegatedAggregateResult {
	result := delegatedAggregateResult{
		AggregateID: aggregate.id,
		Tasks:       append([]delegatedWorkerResult(nil), aggregate.submissionErrors...),
	}
	aggregate.mu.Lock()
	tasks := make([]*supervisedTaskState, 0, len(aggregate.tasks))
	for _, task := range aggregate.tasks {
		tasks = append(tasks, task)
	}
	aggregate.mu.Unlock()
	totalWorkers := len(tasks) + len(result.Tasks)
	workerOutputLimit := 0
	if totalWorkers > 0 {
		workerOutputLimit = (48 * projectedReplayKiB) / totalWorkers
	}
	for _, task := range tasks {
		snapshot := task.lastSnapshot
		output := ""
		if workerOutputLimit >= 128 {
			output = truncateProjectedReplayText("Task", task.lastResult.Output, workerOutputLimit)
		}
		item := delegatedWorkerResult{
			TaskID:               strings.TrimSpace(task.currentTaskID),
			ModelID:              strings.TrimSpace(snapshot.ModelID),
			ModelGroupID:         strings.TrimSpace(snapshot.ModelGroupID),
			ExecutionMode:        strings.TrimSpace(snapshot.ExecutionMode),
			Status:               snapshot.Status,
			DurationMS:           delegatedDuration(snapshot),
			Output:               output,
			Error:                truncateProjectedReplayText("Task error", firstNonEmpty(strings.TrimSpace(snapshot.Error), supervisorErrorString(task.lastResult.Error)), 2048),
			ToolCallCount:        snapshot.ToolCallCount,
			SupervisionStatus:    firstNonEmpty(string(snapshot.SupervisionStatus), string(delegation.SupervisionStatusCompleted)),
			SupervisionRound:     task.round,
			CorrectionCount:      task.corrections,
			RetryCount:           task.retries,
			ReassignCount:        task.reassignments,
			Escalated:            task.escalated,
			SupervisionIssueCode: supervisionIssueCode(task.lastIssue),
			SupervisionReason:    truncateProjectedReplayText("Supervisor reason", firstNonEmpty(strings.TrimSpace(snapshot.Error), issueSummary(task.lastIssue)), 512),
		}
		result.Tasks = append(result.Tasks, item)
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
	case len(result.Tasks) == 0:
		result.Status = "failed"
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

func (aggregate *supervisedAggregate) cancelTasks() {
	if aggregate == nil {
		return
	}
	aggregate.cancel()
	if aggregate.coordinator == nil || aggregate.coordinator.scheduler == nil {
		return
	}
	aggregate.mu.Lock()
	taskIDs := make([]string, 0, len(aggregate.tasks))
	for _, task := range aggregate.tasks {
		if task != nil {
			taskIDs = append(taskIDs, task.currentTaskID)
		}
	}
	aggregate.mu.Unlock()
	for _, taskID := range taskIDs {
		_ = aggregate.coordinator.scheduler.Cancel(strings.TrimSpace(taskID))
	}
}

func (aggregate *supervisedAggregate) postEvent(event supervisorAggregateEvent) {
	if aggregate == nil {
		return
	}
	select {
	case <-aggregate.ctx.Done():
		return
	case aggregate.eventCh <- event:
	}
}

func (aggregate *supervisedAggregate) postReviewAbandoned(identity supervisorTaskIdentity) {
	if aggregate == nil || aggregate.ctx.Err() != nil {
		return
	}
	aggregate.postEvent(supervisorAggregateEvent{
		kind:     "review_abandoned",
		identity: identity,
	})
}

func (aggregate *supervisedAggregate) parentExecStillCurrent() bool {
	if aggregate == nil {
		return false
	}
	return supervisedParentExecStillCurrent(aggregate.stream, aggregate.pending)
}

func (aggregate *supervisedAggregate) matchIdentity(identity supervisorTaskIdentity) (*supervisedTaskState, bool) {
	if aggregate == nil {
		return nil, false
	}
	if strings.TrimSpace(identity.AggregateID) != strings.TrimSpace(aggregate.id) || strings.TrimSpace(identity.ParentExec) != strings.TrimSpace(aggregate.id) {
		return nil, false
	}
	if identity.ProviderPass != aggregate.pending.ProviderPass {
		return nil, false
	}
	aggregate.mu.Lock()
	defer aggregate.mu.Unlock()
	task, ok := aggregate.tasks[strings.TrimSpace(identity.LogicalID)]
	if !ok || task == nil {
		return nil, false
	}
	if strings.TrimSpace(task.currentTaskID) != strings.TrimSpace(identity.TaskID) || task.round != identity.Round {
		return nil, false
	}
	return task, true
}

func (aggregate *supervisedAggregate) resultIdentityMatches(task *supervisedTaskState, identity supervisorTaskIdentity, snapshot delegation.TaskSnapshot, result delegation.TaskResult) bool {
	if task == nil {
		return false
	}
	if strings.TrimSpace(snapshot.ID) != strings.TrimSpace(identity.TaskID) || strings.TrimSpace(snapshot.ParentExecID) != strings.TrimSpace(identity.ParentExec) {
		return false
	}
	if checkpoint := snapshot.Checkpoint; checkpoint != nil && checkpoint.Round > 0 && checkpoint.Round != identity.Round {
		return false
	}
	if metadataPass, ok := parseMetadataProviderPass(result.Metadata); ok && metadataPass != identity.ProviderPass {
		return false
	}
	return true
}

func (aggregate *supervisedAggregate) recordSubmissionError(worker delegation.TaskRequest, err error) {
	if aggregate == nil || err == nil {
		return
	}
	aggregate.mu.Lock()
	aggregate.submissionErrors = append(aggregate.submissionErrors, delegatedWorkerResult{
		TaskID:               strings.TrimSpace(worker.ID),
		ModelID:              strings.TrimSpace(worker.ModelID),
		ModelGroupID:         strings.TrimSpace(worker.ModelGroupID),
		ExecutionMode:        strings.TrimSpace(worker.ExecutionMode),
		Status:               delegation.TaskFailed,
		Error:                err.Error(),
		SupervisionStatus:    string(delegation.SupervisionStatusFailed),
		SupervisionRound:     1,
		SupervisionIssueCode: string(delegation.SupervisionIssueModelFailure),
		SupervisionReason:    truncateProjectedReplayText("Supervisor reason", err.Error(), 512),
	})
	aggregate.mu.Unlock()
}

func (aggregate *supervisedAggregate) settleCurrentTask(task *supervisedTaskState) {
	if task == nil {
		return
	}
	task.completed = true
	task.lastSnapshot.ID = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ID), strings.TrimSpace(task.currentTaskID))
	task.lastSnapshot.ModelID = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ModelID), strings.TrimSpace(task.currentRequest.ModelID))
	task.lastSnapshot.ModelName = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ModelName), strings.TrimSpace(task.currentRequest.ModelName))
	task.lastSnapshot.ModelGroupID = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ModelGroupID), strings.TrimSpace(task.currentRequest.ModelGroupID))
	task.lastSnapshot.ExecutionMode = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ExecutionMode), strings.TrimSpace(task.currentRequest.ExecutionMode))
	if task.lastSnapshot.SupervisionStatus == "" {
		task.lastSnapshot.SupervisionStatus = supervisionStatusFromTaskStatus(task.lastSnapshot.Status)
	}
}

func (aggregate *supervisedAggregate) markTaskFailed(task *supervisedTaskState, code delegation.SupervisionIssueCode, reason string, circuitOpen bool) {
	if task == nil {
		return
	}
	task.completed = true
	task.lastSnapshot.ID = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ID), strings.TrimSpace(task.currentTaskID))
	task.lastSnapshot.ModelID = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ModelID), strings.TrimSpace(task.currentRequest.ModelID))
	task.lastSnapshot.ModelName = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ModelName), strings.TrimSpace(task.currentRequest.ModelName))
	task.lastSnapshot.ModelGroupID = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ModelGroupID), strings.TrimSpace(task.currentRequest.ModelGroupID))
	task.lastSnapshot.ExecutionMode = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ExecutionMode), strings.TrimSpace(task.currentRequest.ExecutionMode))
	task.lastSnapshot.Status = delegation.TaskFailed
	if circuitOpen {
		task.lastSnapshot.SupervisionStatus = delegation.SupervisionStatusCircuitOpen
	} else {
		task.lastSnapshot.SupervisionStatus = delegation.SupervisionStatusFailed
	}
	task.lastSnapshot.Error = truncateProjectedReplayText("Supervisor reason", firstNonEmpty(strings.TrimSpace(reason), "supervision failed"), 512)
	now := time.Now().UTC()
	if task.lastSnapshot.FinishedAt.IsZero() {
		task.lastSnapshot.FinishedAt = now
	}
	if task.lastIssue == nil || code != "" || strings.TrimSpace(reason) != "" {
		task.lastIssue = &delegation.SupervisionIssue{
			Code:       code,
			Summary:    task.lastSnapshot.Error,
			Round:      task.round,
			DetectedAt: now,
		}
	}
}

func (aggregate *supervisedAggregate) markTaskAbandoned(task *supervisedTaskState, code delegation.SupervisionIssueCode, reason string) {
	if task == nil {
		return
	}
	task.completed = true
	task.reviewPending = false
	task.lastSnapshot.ID = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ID), strings.TrimSpace(task.currentTaskID))
	task.lastSnapshot.ModelID = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ModelID), strings.TrimSpace(task.currentRequest.ModelID))
	task.lastSnapshot.ModelName = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ModelName), strings.TrimSpace(task.currentRequest.ModelName))
	task.lastSnapshot.ModelGroupID = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ModelGroupID), strings.TrimSpace(task.currentRequest.ModelGroupID))
	task.lastSnapshot.ExecutionMode = firstNonEmpty(strings.TrimSpace(task.lastSnapshot.ExecutionMode), strings.TrimSpace(task.currentRequest.ExecutionMode))
	task.lastSnapshot.Status = delegation.TaskCanceled
	task.lastSnapshot.SupervisionStatus = delegation.SupervisionStatusCanceled
	task.lastSnapshot.Error = truncateProjectedReplayText("Supervisor reason", firstNonEmpty(strings.TrimSpace(reason), "stale supervised event ignored"), 512)
	now := time.Now().UTC()
	if task.lastSnapshot.FinishedAt.IsZero() {
		task.lastSnapshot.FinishedAt = now
	}
	if code == "" && task.lastIssue != nil {
		code = task.lastIssue.Code
	}
	if task.lastIssue == nil || code != "" || strings.TrimSpace(reason) != "" {
		task.lastIssue = &delegation.SupervisionIssue{
			Code:       code,
			Summary:    task.lastSnapshot.Error,
			Round:      task.round,
			DetectedAt: now,
		}
	}
}

func buildWorkerSupervisionContract(aggregateID string, base delegation.TaskRequest, worker delegation.TaskRequest, config delegation.RuntimeConfig) delegation.SupervisionTaskContract {
	allowedTools := make([]string, 0, len(worker.ToolPermission))
	for toolName, allowed := range worker.ToolPermission {
		if !allowed {
			continue
		}
		if toolName = strings.TrimSpace(toolName); toolName != "" {
			allowedTools = append(allowedTools, toolName)
		}
	}
	doneCriteria := make([]string, 0, 2)
	if value := strings.TrimSpace(worker.Description); value != "" {
		doneCriteria = append(doneCriteria, value)
	}
	if value := strings.TrimSpace(worker.Prompt); value != "" {
		doneCriteria = append(doneCriteria, truncateProjectedReplayText("Supervisor criteria", value, 1024))
	}
	contract := delegation.SupervisionTaskContract{
		AggregateID:        strings.TrimSpace(aggregateID),
		TaskID:             strings.TrimSpace(worker.ID),
		Round:              1,
		Goal:               firstNonEmpty(strings.TrimSpace(worker.Description), truncateProjectedReplayText("Supervisor goal", worker.Prompt, 2048)),
		Scope:              strings.TrimSpace(worker.WorkspaceHint),
		Role:               firstNonEmpty(strings.TrimSpace(worker.SubagentType), "generalPurpose"),
		AllowedTools:       allowedTools,
		ExpectedOutput:     firstNonEmpty(strings.TrimSpace(worker.Description), "Return a concise worker result."),
		DoneCriteria:       doneCriteria,
		MaxCorrections:     config.MaxCorrections,
		MaxRetries:         config.MaxRetries,
		MaxRounds:          config.MaxRounds,
		CheckpointInterval: delegation.DefaultSupervisionCheckpointInterval,
		Timeout:            worker.Timeout,
		FailurePolicy:      "isolate_failure",
		WorkspaceHint:      strings.TrimSpace(worker.WorkspaceHint),
	}
	return contract
}

func resolveReviewCheckpoint(contract delegation.SupervisionTaskContract, snapshot delegation.TaskSnapshot) delegation.WorkerCheckpoint {
	if snapshot.Checkpoint != nil {
		checkpoint := *snapshot.Checkpoint
		if checkpoint.Round <= 0 {
			checkpoint.Round = contract.Round
		}
		return checkpoint
	}
	return delegation.WorkerCheckpoint{
		AggregateID:          strings.TrimSpace(contract.AggregateID),
		TaskID:               strings.TrimSpace(contract.TaskID),
		Round:                contract.Round,
		Phase:                snapshot.SupervisionStatus,
		RecentToolNames:      nil,
		ChangedFileSummaries: nil,
		ProgressSummary:      truncateProjectedReplayText("Supervisor progress", snapshot.Output, 1024),
		Blocker:              strings.TrimSpace(snapshot.Error),
		EffectiveProgressAt:  snapshot.FinishedAt,
	}
}

func detectSupervisionIssue(task *supervisedTaskState, checkpoint delegation.WorkerCheckpoint, snapshot delegation.TaskSnapshot, result delegation.TaskResult, previousCheckpoint *delegation.WorkerCheckpoint, previousSnapshot delegation.TaskSnapshot, previousResult delegation.TaskResult) *delegation.SupervisionIssue {
	if task == nil {
		return nil
	}
	outputSummary := truncateProjectedReplayText("Supervisor output", result.Output, 2048)
	return delegation.DetectCheckpointIssue(delegation.DetectCheckpointIssueInput{
		Contract:               cloneSupervisionContractPointer(task.contract),
		Current:                checkpoint,
		Previous:               cloneCheckpointPointer(previousCheckpoint),
		RecentToolSignatures:   append([]string(nil), task.recentSignatures...),
		ChangedFiles:           checkpoint.ChangedFileSummaries,
		ErrorText:              firstNonEmpty(strings.TrimSpace(snapshot.Error), supervisorErrorString(result.Error)),
		PreviousErrorText:      firstNonEmpty(strings.TrimSpace(previousSnapshot.Error), supervisorErrorString(previousResult.Error)),
		OutputSummary:          outputSummary,
		PreviousOutputSummary:  truncateProjectedReplayText("Supervisor output", previousResult.Output, 2048),
		ResultMetadata:         cloneStringMap(result.Metadata),
		PreviousResultMetadata: cloneStringMap(previousResult.Metadata),
		ClaimedCompletion:      snapshot.Status == delegation.TaskCompleted,
		Now:                    time.Now().UTC(),
	})
}

func resolveSupervisorModelID(config delegation.RuntimeConfig, base delegation.TaskRequest) string {
	modelID := strings.TrimSpace(config.SupervisorModelID)
	if modelID == "" {
		modelID = strings.TrimSpace(base.ModelID)
	}
	return modelID
}

func parseMetadataProviderPass(metadata map[string]string) (int, bool) {
	if len(metadata) == 0 {
		return 0, false
	}
	raw := strings.TrimSpace(metadata["provider_pass"])
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func supervisionIssueCode(issue *delegation.SupervisionIssue) string {
	if issue == nil {
		return ""
	}
	return strings.TrimSpace(string(issue.Code))
}

func cloneSupervisionContract(contract delegation.SupervisionTaskContract) *delegation.SupervisionTaskContract {
	cloned := delegationContractClone(contract)
	return &cloned
}

func supervisorErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func cloneSupervisionContractPointer(contract delegation.SupervisionTaskContract) *delegation.SupervisionTaskContract {
	cloned := delegationContractClone(contract)
	return &cloned
}

func cloneCheckpointPointer(checkpoint *delegation.WorkerCheckpoint) *delegation.WorkerCheckpoint {
	if checkpoint == nil {
		return nil
	}
	cloned := delegationCheckpointClone(*checkpoint)
	return &cloned
}

func issueCodeOrDefault(issue *delegation.SupervisionIssue, fallback delegation.SupervisionIssueCode) delegation.SupervisionIssueCode {
	if issue == nil || strings.TrimSpace(string(issue.Code)) == "" {
		return fallback
	}
	return issue.Code
}

func issueSummary(issue *delegation.SupervisionIssue) string {
	if issue == nil {
		return ""
	}
	return strings.TrimSpace(issue.Summary)
}

func staleEventReason(prefix string, detail string) string {
	prefix = strings.TrimSpace(prefix)
	detail = strings.TrimSpace(detail)
	if prefix == "" {
		return detail
	}
	if detail == "" {
		return prefix
	}
	return prefix + ": " + detail
}

func supervisionStatusFromTaskStatus(status delegation.TaskStatus) delegation.SupervisionStatus {
	switch status {
	case delegation.TaskCompleted:
		return delegation.SupervisionStatusCompleted
	case delegation.TaskFailed, delegation.TaskTimedOut:
		return delegation.SupervisionStatusFailed
	case delegation.TaskCanceled:
		return delegation.SupervisionStatusCanceled
	case delegation.TaskRunning:
		return delegation.SupervisionStatusRunning
	default:
		return delegation.SupervisionStatusCompleted
	}
}

func supervisedParentExecStillCurrent(stream *ActiveStream, pending runtimecore.PendingExec) bool {
	if stream == nil {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if isTerminalStreamStatus(stream.Status) {
		return false
	}
	switch stream.Phase {
	case TurnPhaseCanceled, TurnPhaseCompleted, TurnPhaseFailed:
		return false
	}
	current, ok := stream.PendingExecs[strings.TrimSpace(pending.ExecID)]
	if !ok || strings.TrimSpace(current.ExecKind) != "delegation_aggregate" {
		return false
	}
	if current.ProviderPass != pending.ProviderPass {
		return false
	}
	return stream.ProviderPassCount == pending.ProviderPass
}
