package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime"
	"sort"
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

	mu            sync.RWMutex
	aggregates    map[string]*supervisedAggregate
	runtimeStates map[string]delegationTaskRuntimeState
	closed        bool
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
	logicalID              string
	currentTaskID          string
	currentRequest         delegation.TaskRequest
	contract               delegation.SupervisionTaskContract
	round                  int
	corrections            int
	retries                int
	reassignments          int
	escalated              bool
	escalationUsed         bool
	lastSnapshot           delegation.TaskSnapshot
	lastResult             delegation.TaskResult
	lastIssue              *delegation.SupervisionIssue
	previous               *delegation.WorkerCheckpoint
	reviewPending          bool
	reviewCancel           context.CancelFunc
	reviewSequence         uint64
	lastReviewedSequence   uint64
	deferredWorkerEvent    *supervisorAggregateEvent
	canceled               bool
	completed              bool
	recentSignatures       []string
	lastCheckpointSequence uint64
	attemptTaskIDs         map[string]struct{}
}

type supervisorAggregateEvent struct {
	kind      string
	identity  supervisorTaskIdentity
	snapshot  delegation.TaskSnapshot
	result    delegation.TaskResult
	issue     *delegation.SupervisionIssue
	decision  delegation.SupervisionDecision
	err       error
	heartbeat bool
}

type supervisorTaskIdentity struct {
	AggregateID  string
	LogicalID    string
	TaskID       string
	ParentExec   string
	ProviderPass int
	Round        int
}

var errSupervisedTaskCanceled = errors.New("supervised task canceled")

func newSupervisorCoordinator(service *Service, scheduler *delegation.Scheduler) *SupervisorCoordinator {
	if service == nil || scheduler == nil {
		return nil
	}
	return &SupervisorCoordinator{
		service:       service,
		scheduler:     scheduler,
		provider:      newSupervisorProviderAdapter(service),
		aggregates:    make(map[string]*supervisedAggregate),
		runtimeStates: make(map[string]delegationTaskRuntimeState),
	}
}

func (coordinator *SupervisorCoordinator) rememberRuntimeState(taskID string, state delegationTaskRuntimeState) {
	if coordinator == nil {
		return
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || !state.hasSnapshotOverride {
		return
	}
	coordinator.mu.Lock()
	if coordinator.runtimeStates == nil {
		coordinator.runtimeStates = make(map[string]delegationTaskRuntimeState)
	}
	coordinator.runtimeStates[taskID] = state
	coordinator.mu.Unlock()
}

func (coordinator *SupervisorCoordinator) forgetRuntimeState(taskID string) {
	if coordinator == nil {
		return
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return
	}
	coordinator.mu.Lock()
	delete(coordinator.runtimeStates, taskID)
	coordinator.mu.Unlock()
}

func (coordinator *SupervisorCoordinator) runtimeTaskStates(taskIDs map[string]struct{}) map[string]delegationTaskRuntimeState {
	if coordinator == nil {
		return make(map[string]delegationTaskRuntimeState)
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if len(coordinator.runtimeStates) == 0 {
		return make(map[string]delegationTaskRuntimeState, len(taskIDs))
	}
	result := make(map[string]delegationTaskRuntimeState, len(coordinator.runtimeStates))
	for taskID, state := range coordinator.runtimeStates {
		if _, ok := taskIDs[taskID]; !ok {
			delete(coordinator.runtimeStates, taskID)
			continue
		}
		result[taskID] = state
	}
	return result
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

func (coordinator *SupervisorCoordinator) CancelTask(taskID string) bool {
	if coordinator == nil {
		return false
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return false
	}
	coordinator.mu.RLock()
	aggregates := make([]*supervisedAggregate, 0, len(coordinator.aggregates))
	for _, aggregate := range coordinator.aggregates {
		if aggregate != nil {
			aggregates = append(aggregates, aggregate)
		}
	}
	coordinator.mu.RUnlock()
	for _, aggregate := range aggregates {
		if aggregate.cancelTask(taskID) {
			return true
		}
	}
	return false
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
		attemptTaskIDs: map[string]struct{}{strings.TrimSpace(worker.ID): {}},
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
	aggregate.mu.Lock()
	if task.canceled || task.completed {
		aggregate.mu.Unlock()
		return errSupervisedTaskCanceled
	}
	request := task.currentRequest
	request.ParentRequest = strings.TrimSpace(aggregate.base.ParentRequest)
	request.ParentExecID = strings.TrimSpace(aggregate.id)
	request.ID = strings.TrimSpace(task.currentTaskID)
	task.contract.AggregateID = strings.TrimSpace(aggregate.id)
	task.contract.TaskID = request.ID
	task.contract.Round = task.round
	request.Contract = cloneSupervisionContract(task.contract)
	request.RuntimeSupervisionRound = task.round
	request.RuntimeCorrectionCount = task.corrections
	request.RuntimeRetryCount = task.retries
	request.RuntimeReassignCount = task.reassignments
	if task.escalated {
		request.RuntimeEscalateCount = 1
	} else {
		request.RuntimeEscalateCount = 0
	}
	request.RuntimeSupervisionIssue = issueCodeOrDefault(task.lastIssue, "")
	request.RuntimeProgressSummary = currentProgressSummary(task.lastSnapshot)
	if extraPrompt = strings.TrimSpace(extraPrompt); extraPrompt != "" {
		request.Prompt = strings.TrimSpace(request.Prompt + "\n\nSupervisor correction:\n" + extraPrompt)
	}
	task.currentRequest = request
	_, err := aggregate.coordinator.scheduler.Submit(request)
	taskID := strings.TrimSpace(task.currentTaskID)
	round := task.round
	aggregate.mu.Unlock()
	if err != nil {
		return err
	}
	aggregate.startWait(task, supervisorTaskIdentity{
		AggregateID:  aggregate.id,
		LogicalID:    task.logicalID,
		TaskID:       taskID,
		ParentExec:   aggregate.id,
		ProviderPass: aggregate.pending.ProviderPass,
		Round:        round,
	})
	log.Printf(
		"forwarder supervised task submitted request_id=%s aggregate_id=%s task_id=%s logical_id=%s round=%d reason=%s",
		strings.TrimSpace(activeStreamRequestID(aggregate.stream)),
		strings.TrimSpace(aggregate.id),
		taskID,
		strings.TrimSpace(task.logicalID),
		round,
		strings.TrimSpace(reason),
	)
	return nil
}

func (aggregate *supervisedAggregate) startWait(task *supervisedTaskState, identity supervisorTaskIdentity) {
	if aggregate == nil || task == nil || aggregate.coordinator == nil || aggregate.coordinator.scheduler == nil {
		return
	}
	checkpointInterval := delegation.DefaultSupervisionCheckpointInterval
	aggregate.mu.Lock()
	if task.contract.CheckpointInterval > 0 {
		checkpointInterval = task.contract.CheckpointInterval
	}
	aggregate.mu.Unlock()
	go func() {
		lastSequence := uint64(0)
		for {
			afterSequence := lastSequence
			heartbeat := false
			waitCtx, cancelWait := context.WithTimeout(aggregate.ctx, checkpointInterval)
			snapshot, err := aggregate.coordinator.scheduler.WaitForTaskUpdate(waitCtx, identity.TaskID, afterSequence)
			cancelWait()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					var ok bool
					snapshot, ok = aggregate.coordinator.scheduler.Snapshot(identity.TaskID)
					if !ok {
						aggregate.postEvent(supervisorAggregateEvent{kind: "worker_terminal", identity: identity, err: fmt.Errorf("supervised task snapshot %q disappeared", identity.TaskID)})
						return
					}
					heartbeat = snapshot.Sequence <= afterSequence
				} else {
					if errors.Is(err, context.Canceled) || aggregate.ctx.Err() != nil {
						return
					}
					aggregate.postEvent(supervisorAggregateEvent{kind: "worker_terminal", identity: identity, err: err})
					return
				}
			}
			if snapshot.Sequence > lastSequence {
				lastSequence = snapshot.Sequence
			}
			if delegatedStatusTerminal(snapshot.Status) {
				result, _ := aggregate.coordinator.scheduler.Result(identity.TaskID)
				aggregate.postEvent(supervisorAggregateEvent{kind: "worker_terminal", identity: identity, snapshot: snapshot, result: result})
				return
			}
			if snapshot.Checkpoint != nil {
				aggregate.postEvent(supervisorAggregateEvent{kind: "worker_checkpoint", identity: identity, snapshot: snapshot, heartbeat: heartbeat})
			}
		}
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
	case "worker_checkpoint":
		aggregate.handleWorkerCheckpoint(event)
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

func (aggregate *supervisedAggregate) handleWorkerCheckpoint(event supervisorAggregateEvent) {
	if aggregate == nil || !aggregate.parentExecStillCurrent() || event.snapshot.Checkpoint == nil {
		return
	}
	// Dispatch and startup checkpoints only report liveness. They carry no
	// worker action or result, so reviewing them can replace a live worker
	// before its first provider event arrives.
	if !supervisionCheckpointReadyForReview(*event.snapshot.Checkpoint) {
		return
	}
	reviewContext, reviewCancel := context.WithCancel(aggregate.ctx)
	aggregate.mu.Lock()
	task, ok := aggregate.matchIdentityLocked(event.identity)
	if !ok || !aggregate.snapshotIdentityMatches(event.identity, event.snapshot) {
		aggregate.mu.Unlock()
		reviewCancel()
		return
	}
	if !event.heartbeat && workerSnapshotAlreadyHandled(task, event.snapshot) {
		aggregate.mu.Unlock()
		reviewCancel()
		return
	}
	if task.reviewPending {
		deferWorkerEventLocked(task, event)
		aggregate.mu.Unlock()
		reviewCancel()
		return
	}
	previousSnapshot := task.lastSnapshot
	previousResult := task.lastResult
	previousCheckpoint := cloneCheckpointPointer(task.previous)
	currentCheckpoint := resolveReviewCheckpoint(task.contract, event.snapshot)
	if currentCheckpoint.EventSequence > task.lastCheckpointSequence {
		recordRecentToolSignatures(task, currentCheckpoint)
		task.lastCheckpointSequence = currentCheckpoint.EventSequence
	}
	issue := detectSupervisionIssue(task, currentCheckpoint, event.snapshot, delegation.TaskResult{}, previousCheckpoint, previousSnapshot, previousResult)
	task.lastSnapshot = event.snapshot
	task.previous = cloneCheckpointPointer(&currentCheckpoint)
	applyTaskRuntimeMetadata(task)
	if issue == nil {
		task.lastIssue = nil
		aggregate.mu.Unlock()
		reviewCancel()
		aggregate.rememberTaskRuntimeState(task)
		return
	}
	if currentCheckpoint.EventSequence > 0 && currentCheckpoint.EventSequence <= task.lastReviewedSequence {
		task.lastIssue = nil
		aggregate.mu.Unlock()
		reviewCancel()
		aggregate.rememberTaskRuntimeState(task)
		return
	}
	task.lastIssue = issue
	contract := *cloneSupervisionContract(task.contract)
	task.reviewCancel = reviewCancel
	task.reviewPending = true
	task.reviewSequence = event.snapshot.Sequence
	if currentCheckpoint.EventSequence > task.lastReviewedSequence {
		task.lastReviewedSequence = currentCheckpoint.EventSequence
	}
	aggregate.mu.Unlock()
	aggregate.rememberTaskRuntimeState(task)
	allowed := aggregate.allowedActions(task, event.snapshot)
	go aggregate.reviewTask(reviewContext, event.identity, contract, event.snapshot, delegation.TaskResult{}, issue, allowed)
}

func supervisionCheckpointReadyForReview(checkpoint delegation.WorkerCheckpoint) bool {
	if len(checkpoint.RecentToolNames) > 0 || len(checkpoint.ChangedFileSummaries) > 0 {
		return true
	}
	if strings.TrimSpace(checkpoint.Blocker) != "" {
		return true
	}
	switch checkpoint.Phase {
	case delegation.SupervisionStatusCompleted, delegation.SupervisionStatusFailed, delegation.SupervisionStatusCanceled:
		return true
	default:
		return false
	}
}

func (aggregate *supervisedAggregate) handleWorkerTerminal(event supervisorAggregateEvent) {
	if !aggregate.parentExecStillCurrent() {
		aggregate.mu.Lock()
		task, ok := aggregate.matchIdentityLocked(event.identity)
		if ok && event.err == nil {
			task.lastSnapshot = event.snapshot
			task.lastResult = event.result
		}
		aggregate.mu.Unlock()
		if !ok {
			return
		}
		aggregate.markTaskAbandoned(task, delegation.SupervisionIssueModelFailure, staleEventReason("stale worker result ignored after provider pass changed", firstNonEmpty(supervisorErrorString(event.err), strings.TrimSpace(event.snapshot.Error))))
		return
	}
	aggregate.mu.Lock()
	task, ok := aggregate.matchIdentityLocked(event.identity)
	if !ok {
		aggregate.mu.Unlock()
		return
	}
	if event.err == nil && workerSnapshotAlreadyHandled(task, event.snapshot) {
		aggregate.mu.Unlock()
		return
	}
	if task.reviewPending {
		deferWorkerEventLocked(task, event)
		aggregate.mu.Unlock()
		return
	}
	aggregate.mu.Unlock()
	if event.err != nil {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueModelFailure, event.err.Error(), false)
		return
	}
	reviewContext, reviewCancel := context.WithCancel(aggregate.ctx)
	aggregate.mu.Lock()
	task, ok = aggregate.matchIdentityLocked(event.identity)
	if !ok || !aggregate.resultIdentityMatches(task, event.identity, event.snapshot) {
		aggregate.mu.Unlock()
		reviewCancel()
		return
	}
	previousSnapshot := task.lastSnapshot
	previousResult := task.lastResult
	previousCheckpoint := cloneCheckpointPointer(task.previous)
	currentCheckpoint := resolveReviewCheckpoint(task.contract, event.snapshot)
	recordRecentToolSignatures(task, currentCheckpoint)
	issue := detectSupervisionIssue(task, currentCheckpoint, event.snapshot, event.result, previousCheckpoint, previousSnapshot, previousResult)
	task.lastSnapshot = event.snapshot
	task.lastResult = event.result
	task.lastIssue = issue
	task.previous = cloneCheckpointPointer(&currentCheckpoint)
	contract := *cloneSupervisionContract(task.contract)
	task.reviewCancel = reviewCancel
	task.reviewPending = true
	task.reviewSequence = event.snapshot.Sequence
	if currentCheckpoint.EventSequence > task.lastReviewedSequence {
		task.lastReviewedSequence = currentCheckpoint.EventSequence
	}
	aggregate.mu.Unlock()
	allowed := aggregate.allowedActions(task, event.snapshot)
	go aggregate.reviewTask(reviewContext, event.identity, contract, event.snapshot, event.result, issue, allowed)
}

func (aggregate *supervisedAggregate) reviewTask(reviewContext context.Context, identity supervisorTaskIdentity, contract delegation.SupervisionTaskContract, snapshot delegation.TaskSnapshot, result delegation.TaskResult, issue *delegation.SupervisionIssue, allowed []delegation.SupervisionDecisionKind) {
	if aggregate == nil {
		return
	}
	if reviewContext == nil {
		reviewContext = aggregate.ctx
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
		if delegatedStatusTerminal(snapshot.Status) {
			modelID = firstNonEmpty(strings.TrimSpace(aggregate.config.ReviewerModelID), modelID)
		}
		reviewDecision, reviewErr := provider.Review(reviewContext, supervisorReviewInput{
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
	task, deferred, reviewSequence, ok := aggregate.takeReview(event.identity)
	if !ok {
		return
	}
	if !aggregate.parentExecStillCurrent() {
		aggregate.markTaskAbandoned(task, delegation.SupervisionIssueReviewFailure, "stale supervisor review ignored after provider pass changed")
		return
	}
	if deferred == nil {
		deferred = aggregate.latestWorkerEventAfterReview(event.identity, reviewSequence)
	}
	if deferred != nil {
		aggregate.handleEvent(*deferred)
		return
	}
	aggregate.mu.Lock()
	terminal := delegatedStatusTerminal(task.lastSnapshot.Status)
	aggregate.mu.Unlock()
	if terminal {
		aggregate.settleCurrentTask(task)
		return
	}
	aggregate.resumeTaskAfterReview(task)
}

func (aggregate *supervisedAggregate) handleReviewResult(event supervisorAggregateEvent) {
	task, deferred, reviewSequence, ok := aggregate.takeReview(event.identity)
	if !ok {
		return
	}
	if !aggregate.parentExecStillCurrent() {
		aggregate.markTaskAbandoned(task, issueCodeOrDefault(event.issue, delegation.SupervisionIssueReviewFailure), staleEventReason("stale supervisor review ignored after provider pass changed", firstNonEmpty(strings.TrimSpace(event.decision.Reason), boundedSupervisorError(event.err), issueSummary(event.issue))))
		return
	}
	if deferred == nil {
		deferred = aggregate.latestWorkerEventAfterReview(event.identity, reviewSequence)
	}
	if deferred != nil {
		aggregate.handleEvent(*deferred)
		return
	}
	decision := normalizeDelegationDecision(event.decision)
	if decision.Kind == "" {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueReviewFailure, firstNonEmpty(boundedSupervisorError(event.err), "supervisor decision was empty"), true)
		return
	}
	aggregate.mu.Lock()
	currentStatus := task.lastSnapshot.Status
	aggregate.mu.Unlock()
	switch decision.Kind {
	case delegation.SupervisionDecisionAccept:
		if delegatedStatusTerminal(currentStatus) {
			aggregate.settleCurrentTask(task)
		} else {
			aggregate.resumeTaskAfterReview(task)
		}
	case delegation.SupervisionDecisionContinue:
		if delegatedStatusTerminal(currentStatus) && currentStatus != delegation.TaskCompleted {
			aggregate.retryTask(task, "continue_after_terminal", supervisorDecisionGuidance(decision))
			return
		}
		if delegatedStatusTerminal(currentStatus) {
			aggregate.settleCurrentTask(task)
		} else {
			aggregate.resumeTaskAfterReview(task)
		}
	case delegation.SupervisionDecisionCorrect:
		aggregate.correctTask(task, decision)
	case delegation.SupervisionDecisionRetry:
		aggregate.retryTask(task, "retry", supervisorDecisionGuidance(decision))
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

func supervisorDecisionGuidance(decision delegation.SupervisionDecision) string {
	return firstNonEmpty(strings.TrimSpace(decision.Summary), strings.TrimSpace(decision.Reason))
}

func (aggregate *supervisedAggregate) resumeTaskAfterReview(task *supervisedTaskState) {
	if aggregate == nil || task == nil {
		return
	}
	aggregate.mu.Lock()
	if task.canceled || task.completed {
		aggregate.mu.Unlock()
		return
	}
	task.lastIssue = nil
	applyTaskRuntimeMetadata(task)
	aggregate.mu.Unlock()
	aggregate.rememberTaskRuntimeState(task)
}

func (aggregate *supervisedAggregate) takeReview(identity supervisorTaskIdentity) (*supervisedTaskState, *supervisorAggregateEvent, uint64, bool) {
	if aggregate == nil {
		return nil, nil, 0, false
	}
	aggregate.mu.Lock()
	task, ok := aggregate.matchIdentityLocked(identity)
	if !ok || !task.reviewPending {
		aggregate.mu.Unlock()
		return nil, nil, 0, false
	}
	reviewCancel := task.reviewCancel
	deferred := task.deferredWorkerEvent
	reviewSequence := task.reviewSequence
	task.reviewCancel = nil
	task.reviewPending = false
	task.reviewSequence = 0
	task.deferredWorkerEvent = nil
	aggregate.mu.Unlock()
	if reviewCancel != nil {
		reviewCancel()
	}
	return task, deferred, reviewSequence, true
}

func (aggregate *supervisedAggregate) latestWorkerEventAfterReview(identity supervisorTaskIdentity, afterSequence uint64) *supervisorAggregateEvent {
	if aggregate == nil || aggregate.coordinator == nil || aggregate.coordinator.scheduler == nil {
		return nil
	}
	snapshot, ok := aggregate.coordinator.scheduler.Snapshot(identity.TaskID)
	if !ok || snapshot.Sequence <= afterSequence || !aggregate.snapshotIdentityMatches(identity, snapshot) {
		return nil
	}
	if delegatedStatusTerminal(snapshot.Status) {
		result, _ := aggregate.coordinator.scheduler.Result(identity.TaskID)
		return &supervisorAggregateEvent{
			kind:     "worker_terminal",
			identity: identity,
			snapshot: snapshot,
			result:   result,
		}
	}
	if snapshot.Checkpoint == nil {
		return nil
	}
	aggregate.mu.Lock()
	task, matches := aggregate.matchIdentityLocked(identity)
	supersedesReview := matches && checkpointSupersedesPendingReview(task, snapshot.Checkpoint)
	aggregate.mu.Unlock()
	if !supersedesReview {
		return nil
	}
	return &supervisorAggregateEvent{
		kind:     "worker_checkpoint",
		identity: identity,
		snapshot: snapshot,
	}
}

func (aggregate *supervisedAggregate) correctTask(task *supervisedTaskState, decision delegation.SupervisionDecision) {
	if task == nil {
		return
	}
	aggregate.mu.Lock()
	if task.canceled || task.completed {
		aggregate.mu.Unlock()
		return
	}
	if task.corrections >= task.contract.MaxCorrections {
		aggregate.mu.Unlock()
		aggregate.markTaskFailed(task, delegation.SupervisionIssueCorrectionLimit, "supervisor correction limit exceeded", true)
		return
	}
	if task.round >= task.contract.MaxRounds {
		aggregate.mu.Unlock()
		aggregate.markTaskFailed(task, delegation.SupervisionIssueRoundLimit, "supervision round limit exceeded", true)
		return
	}
	aggregate.mu.Unlock()
	aggregate.spawnFollowup(task, "correct", supervisorDecisionGuidance(decision), "", "", "")
}

func (aggregate *supervisedAggregate) retryTask(task *supervisedTaskState, reason string, prompt string) {
	if task == nil {
		return
	}
	aggregate.mu.Lock()
	if task.canceled || task.completed {
		aggregate.mu.Unlock()
		return
	}
	if task.retries >= task.contract.MaxRetries {
		aggregate.mu.Unlock()
		aggregate.markTaskFailed(task, delegation.SupervisionIssueRetryLimit, "supervisor retry limit exceeded", true)
		return
	}
	if task.round >= task.contract.MaxRounds {
		aggregate.mu.Unlock()
		aggregate.markTaskFailed(task, delegation.SupervisionIssueRoundLimit, "supervision round limit exceeded", true)
		return
	}
	aggregate.mu.Unlock()
	aggregate.spawnFollowup(task, reason, prompt, "", "", "")
}

func (aggregate *supervisedAggregate) reassignTask(task *supervisedTaskState, decision delegation.SupervisionDecision) {
	if task == nil || !aggregate.config.AllowReassign {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueReviewFailure, "supervisor reassignment is disabled", true)
		return
	}
	aggregate.mu.Lock()
	if task.canceled || task.completed {
		aggregate.mu.Unlock()
		return
	}
	if task.round >= task.contract.MaxRounds {
		aggregate.mu.Unlock()
		aggregate.markTaskFailed(task, delegation.SupervisionIssueRoundLimit, "supervision round limit exceeded", true)
		return
	}
	aggregate.mu.Unlock()
	modelID, groupID, executionMode, reason := aggregate.nextReassignedModel(task)
	if modelID == "" {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueModelFailure, firstNonEmpty(strings.TrimSpace(reason), "no reassignment target is available"), true)
		return
	}
	aggregate.mu.Lock()
	if task.canceled || task.completed {
		aggregate.mu.Unlock()
		return
	}
	aggregate.mu.Unlock()
	aggregate.spawnFollowup(task, "reassign", supervisorDecisionGuidance(decision), modelID, groupID, executionMode)
}

func (aggregate *supervisedAggregate) escalateTask(task *supervisedTaskState, decision delegation.SupervisionDecision) {
	if task == nil || !aggregate.config.AllowEscalate {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueReviewFailure, "supervisor escalation is disabled", true)
		return
	}
	aggregate.mu.Lock()
	if task.canceled || task.completed {
		aggregate.mu.Unlock()
		return
	}
	if task.escalationUsed {
		aggregate.mu.Unlock()
		aggregate.markTaskFailed(task, delegation.SupervisionIssueReviewFailure, "supervisor escalation already used", true)
		return
	}
	if task.round >= task.contract.MaxRounds {
		aggregate.mu.Unlock()
		aggregate.markTaskFailed(task, delegation.SupervisionIssueRoundLimit, "supervision round limit exceeded", true)
		return
	}
	aggregate.mu.Unlock()
	modelID := firstNonEmpty(strings.TrimSpace(aggregate.config.ReviewerModelID), strings.TrimSpace(aggregate.config.SupervisorModelID), strings.TrimSpace(aggregate.base.ModelID))
	if modelID == "" {
		aggregate.markTaskFailed(task, delegation.SupervisionIssueModelFailure, "no escalation model is available", true)
		return
	}
	aggregate.mu.Lock()
	if task.canceled || task.completed {
		aggregate.mu.Unlock()
		return
	}
	executionMode := firstNonEmpty(strings.TrimSpace(task.currentRequest.ExecutionMode), delegation.ExecutionModeAuto)
	groupID, executionMode := aggregate.resolveConfiguredModelRoute(modelID, executionMode)
	aggregate.mu.Unlock()
	aggregate.spawnFollowup(task, "escalate", supervisorDecisionGuidance(decision), modelID, groupID, executionMode)
}

func (aggregate *supervisedAggregate) spawnFollowup(task *supervisedTaskState, reason string, prompt string, modelID string, groupOverride string, executionMode string) {
	if task == nil {
		return
	}
	aggregate.mu.Lock()
	if task.canceled || task.completed {
		aggregate.mu.Unlock()
		return
	}
	previousTaskID := task.currentTaskID
	previousRequest := task.currentRequest
	previousRound := task.round
	previousContract := delegationContractClone(task.contract)
	previousCorrections := task.corrections
	previousRetries := task.retries
	previousReassignments := task.reassignments
	previousEscalated := task.escalated
	previousEscalationUsed := task.escalationUsed
	switch strings.TrimSpace(reason) {
	case "correct":
		task.corrections++
	case "retry", "continue_after_terminal":
		task.retries++
	case "reassign":
		task.reassignments++
	case "escalate":
		task.escalationUsed = true
		task.escalated = true
	}
	task.round++
	task.contract.Round = task.round
	request := task.currentRequest
	request.ID = fmt.Sprintf("%s-r%d", task.logicalID, task.round)
	if strings.TrimSpace(modelID) != "" {
		request.ModelID = strings.TrimSpace(modelID)
		request.ModelName = firstNonEmpty(aggregate.config.ModelNames[request.ModelID], request.ModelID)
		request.ArgsJSON = rewriteDelegatedWorkerModel(request.ArgsJSON, request.ModelID)
		request.ModelGroupID = strings.TrimSpace(groupOverride)
		request.ExecutionMode = delegation.NormalizeExecutionMode(firstNonEmpty(strings.TrimSpace(executionMode), strings.TrimSpace(request.ExecutionMode), delegation.ExecutionModeAuto))
		if request.ModelGroupID != "" {
			request.ToolPermission = cloneDelegatedPermissions(aggregate.toolPermissionsForGroup(request.ModelGroupID))
		}
	} else {
		if strings.TrimSpace(groupOverride) != "" {
			request.ModelGroupID = strings.TrimSpace(groupOverride)
		}
		if strings.TrimSpace(executionMode) != "" {
			request.ExecutionMode = delegation.NormalizeExecutionMode(executionMode)
		}
	}
	task.contract.AllowedTools = allowedToolNames(request.ToolPermission)
	task.currentTaskID = request.ID
	task.currentRequest = request
	if task.attemptTaskIDs == nil {
		task.attemptTaskIDs = make(map[string]struct{})
	}
	task.attemptTaskIDs[request.ID] = struct{}{}
	task.reviewSequence = 0
	task.deferredWorkerEvent = nil
	aggregate.mu.Unlock()
	aggregate.coordinator.scheduler.CancelIfActive(previousTaskID)
	aggregate.coordinator.forgetRuntimeState(previousTaskID)
	if err := aggregate.submitAttempt(task, reason, prompt); err != nil {
		aggregate.mu.Lock()
		task.currentTaskID = previousTaskID
		task.currentRequest = previousRequest
		task.round = previousRound
		task.contract = previousContract
		task.corrections = previousCorrections
		task.retries = previousRetries
		task.reassignments = previousReassignments
		task.escalated = previousEscalated
		task.escalationUsed = previousEscalationUsed
		aggregate.mu.Unlock()
		if errors.Is(err, errSupervisedTaskCanceled) {
			return
		}
		if errors.Is(err, errProviderLoopInterrupted) || !aggregate.parentExecStillCurrent() {
			aggregate.cancelTasks()
			return
		}
		aggregate.markTaskFailed(task, delegation.SupervisionIssueModelFailure, err.Error(), false)
	}
}

func (aggregate *supervisedAggregate) toolPermissionsForGroup(groupID string) map[string]bool {
	if aggregate == nil {
		return nil
	}
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return nil
	}
	for _, group := range aggregate.config.Groups {
		if strings.TrimSpace(group.ID) == groupID {
			return cloneDelegatedPermissions(group.ToolPermissions)
		}
	}
	return nil
}

func allowedToolNames(permissions map[string]bool) []string {
	if len(permissions) == 0 {
		return nil
	}
	allowed := make([]string, 0, len(permissions))
	for name, enabled := range permissions {
		if enabled {
			if name = strings.TrimSpace(name); name != "" {
				allowed = append(allowed, name)
			}
		}
	}
	sort.Strings(allowed)
	return allowed
}

func (aggregate *supervisedAggregate) nextReassignedModel(task *supervisedTaskState) (string, string, string, string) {
	aggregate.mu.Lock()
	defer aggregate.mu.Unlock()
	return aggregate.nextReassignedModelLocked(task)
}

func (aggregate *supervisedAggregate) nextReassignedModelLocked(task *supervisedTaskState) (string, string, string, string) {
	if task == nil {
		return "", "", "", "no task is available"
	}
	currentModelID := strings.TrimSpace(task.currentRequest.ModelID)
	currentGroupID := strings.TrimSpace(task.currentRequest.ModelGroupID)
	preferredGroup := strings.TrimSpace(aggregate.config.WorkerGroupID)
	if preferredGroup != "" {
		for _, group := range aggregate.config.Groups {
			if !group.Enabled || strings.TrimSpace(group.ID) != preferredGroup {
				continue
			}
			for _, modelID := range group.ModelIDs {
				modelID = strings.TrimSpace(modelID)
				if modelID == "" || (modelID == currentModelID && strings.TrimSpace(group.ID) == currentGroupID) {
					continue
				}
				return modelID, strings.TrimSpace(group.ID), delegation.NormalizeExecutionMode(group.ExecutionMode), ""
			}
			return "", "", "", fmt.Sprintf("no eligible reassignment target is configured in worker group %q", preferredGroup)
		}
		return "", "", "", fmt.Sprintf("worker group %q has no enabled reassignment candidates", preferredGroup)
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
			return modelID, strings.TrimSpace(group.ID), delegation.NormalizeExecutionMode(group.ExecutionMode), ""
		}
	}
	return "", "", "", "no reassignment target is available"
}

func (aggregate *supervisedAggregate) resolveConfiguredModelRoute(modelID string, fallbackExecutionMode string) (string, string) {
	modelID = strings.TrimSpace(modelID)
	fallbackExecutionMode = delegation.NormalizeExecutionMode(fallbackExecutionMode)
	if aggregate == nil || modelID == "" {
		return "", fallbackExecutionMode
	}
	for _, group := range aggregate.config.Groups {
		if !group.Enabled {
			continue
		}
		for _, candidate := range group.ModelIDs {
			if strings.TrimSpace(candidate) != modelID {
				continue
			}
			return strings.TrimSpace(group.ID), delegation.NormalizeExecutionMode(group.ExecutionMode)
		}
	}
	return "", fallbackExecutionMode
}

func (aggregate *supervisedAggregate) allowedActions(task *supervisedTaskState, snapshot delegation.TaskSnapshot) []delegation.SupervisionDecisionKind {
	if aggregate == nil {
		return []delegation.SupervisionDecisionKind{
			delegation.SupervisionDecisionAccept,
			delegation.SupervisionDecisionCircuitOpen,
		}
	}
	aggregate.mu.Lock()
	defer aggregate.mu.Unlock()
	if task == nil {
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
		if modelID, _, _, _ := aggregate.nextReassignedModelLocked(task); modelID != "" {
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
	if aggregate == nil {
		return delegatedAggregateResult{Status: "failed"}
	}
	aggregate.mu.Lock()
	defer aggregate.mu.Unlock()
	result := delegatedAggregateResult{
		AggregateID: aggregate.id,
		Tasks:       append([]delegatedWorkerResult(nil), aggregate.submissionErrors...),
	}
	totalWorkers := len(aggregate.tasks) + len(result.Tasks)
	workerOutputLimit := 0
	if totalWorkers > 0 {
		workerOutputLimit = (48 * projectedReplayKiB) / totalWorkers
	}
	for _, task := range aggregate.tasks {
		if task == nil {
			continue
		}
		snapshot := task.lastSnapshot
		output := ""
		if workerOutputLimit >= 128 {
			output = truncateProjectedReplayText("Task", task.lastResult.Output, workerOutputLimit)
		}
		workerError := delegation.SanitizeSupervisorText(
			firstNonEmpty(strings.TrimSpace(snapshot.Error), supervisorErrorString(task.lastResult.Error)),
			task.contract.WorkspaceHint,
		)
		supervisionReason := delegation.SanitizeSupervisorText(
			firstNonEmpty(strings.TrimSpace(snapshot.Error), issueSummary(task.lastIssue)),
			task.contract.WorkspaceHint,
		)
		item := delegatedWorkerResult{
			TaskID:               strings.TrimSpace(task.currentTaskID),
			ModelID:              strings.TrimSpace(snapshot.ModelID),
			ModelGroupID:         strings.TrimSpace(snapshot.ModelGroupID),
			ExecutionMode:        strings.TrimSpace(snapshot.ExecutionMode),
			Status:               snapshot.Status,
			DurationMS:           delegatedDuration(snapshot),
			Output:               output,
			Error:                truncateProjectedReplayText("Task error", workerError, 2048),
			ToolCallCount:        snapshot.ToolCallCount,
			SupervisionStatus:    firstNonEmpty(string(snapshot.SupervisionStatus), string(delegation.SupervisionStatusCompleted)),
			SupervisionRound:     task.round,
			CorrectionCount:      task.corrections,
			RetryCount:           task.retries,
			ReassignCount:        task.reassignments,
			Escalated:            task.escalated,
			SupervisionIssueCode: supervisionIssueCode(task.lastIssue),
			SupervisionReason:    truncateProjectedReplayText("Supervisor reason", supervisionReason, 512),
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
	callerFile := "unknown"
	callerLine := 0
	if _, file, line, ok := runtime.Caller(1); ok {
		callerFile = file
		callerLine = line
	}
	log.Printf(
		"forwarder supervised aggregate canceling aggregate_id=%s request_id=%s parent_exec_current=%t caller=%s:%d",
		strings.TrimSpace(aggregate.id),
		strings.TrimSpace(activeStreamRequestID(aggregate.stream)),
		aggregate.parentExecStillCurrent(),
		callerFile,
		callerLine,
	)
	aggregate.cancel()
	aggregate.mu.Lock()
	taskIDs := make([]string, 0, len(aggregate.tasks))
	reviewCancels := make([]context.CancelFunc, 0, len(aggregate.tasks))
	runtimeStates := make([]struct {
		taskID string
		state  delegationTaskRuntimeState
	}, 0, len(aggregate.tasks))
	now := time.Now().UTC()
	for _, task := range aggregate.tasks {
		if task == nil || task.canceled || (task.completed && !task.reviewPending) {
			continue
		}
		task.canceled = true
		task.completed = true
		task.reviewPending = false
		task.reviewSequence = 0
		task.deferredWorkerEvent = nil
		if task.reviewCancel != nil {
			reviewCancels = append(reviewCancels, task.reviewCancel)
			task.reviewCancel = nil
		}
		task.lastSnapshot.Status = delegation.TaskCanceled
		task.lastSnapshot.SupervisionStatus = delegation.SupervisionStatusCanceled
		task.lastSnapshot.Error = "supervised task canceled"
		task.lastSnapshot.UpdatedAt = now
		task.lastSnapshot.FinishedAt = now
		applyTaskRuntimeMetadata(task)
		currentTaskID := strings.TrimSpace(task.currentTaskID)
		taskIDs = append(taskIDs, taskAttemptIDsLocked(task)...)
		if currentTaskID != "" {
			runtimeStates = append(runtimeStates, struct {
				taskID string
				state  delegationTaskRuntimeState
			}{taskID: currentTaskID, state: delegationTaskRuntimeStateForTask(aggregate, task, true)})
		}
	}
	aggregate.mu.Unlock()
	for _, cancelReview := range reviewCancels {
		cancelReview()
	}
	if aggregate.coordinator == nil {
		return
	}
	for _, item := range runtimeStates {
		aggregate.coordinator.rememberRuntimeState(item.taskID, item.state)
	}
	if aggregate.coordinator.scheduler == nil {
		return
	}
	for _, taskID := range taskIDs {
		aggregate.coordinator.scheduler.CancelIfActive(strings.TrimSpace(taskID))
	}
}

func (aggregate *supervisedAggregate) cancelTask(taskID string) bool {
	if aggregate == nil {
		return false
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return false
	}
	aggregate.mu.Lock()
	var target *supervisedTaskState
	for _, task := range aggregate.tasks {
		if taskOwnsAttemptLocked(task, taskID) {
			target = task
			break
		}
	}
	if target == nil || target.canceled || (target.completed && !target.reviewPending) {
		aggregate.mu.Unlock()
		return false
	}
	target.canceled = true
	target.completed = true
	target.reviewPending = false
	target.reviewSequence = 0
	target.deferredWorkerEvent = nil
	reviewCancel := target.reviewCancel
	target.reviewCancel = nil
	target.lastSnapshot.Status = delegation.TaskCanceled
	target.lastSnapshot.SupervisionStatus = delegation.SupervisionStatusCanceled
	target.lastSnapshot.Error = "supervised task canceled"
	target.lastSnapshot.UpdatedAt = time.Now().UTC()
	target.lastSnapshot.FinishedAt = target.lastSnapshot.UpdatedAt
	applyTaskRuntimeMetadata(target)
	currentTaskID := strings.TrimSpace(target.currentTaskID)
	attemptTaskIDs := taskAttemptIDsLocked(target)
	runtimeState := delegationTaskRuntimeStateForTask(aggregate, target, true)
	scheduler := aggregate.coordinator.scheduler
	aggregate.mu.Unlock()
	for _, attemptTaskID := range attemptTaskIDs {
		if attemptTaskID != currentTaskID {
			aggregate.coordinator.forgetRuntimeState(attemptTaskID)
		}
	}
	aggregate.coordinator.rememberRuntimeState(currentTaskID, runtimeState)
	if reviewCancel != nil {
		reviewCancel()
	}
	if scheduler != nil {
		for _, attemptTaskID := range attemptTaskIDs {
			scheduler.CancelIfActive(attemptTaskID)
		}
	}
	aggregate.postEvent(supervisorAggregateEvent{kind: "task_canceled"})
	return true
}

func (aggregate *supervisedAggregate) rememberTaskRuntimeState(task *supervisedTaskState) {
	if aggregate == nil || aggregate.coordinator == nil || task == nil {
		return
	}
	aggregate.mu.Lock()
	taskID := strings.TrimSpace(task.currentTaskID)
	state := delegationTaskRuntimeStateForTask(aggregate, task, true)
	aggregate.mu.Unlock()
	aggregate.coordinator.rememberRuntimeState(taskID, state)
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
	aggregate.mu.Lock()
	defer aggregate.mu.Unlock()
	return aggregate.matchIdentityLocked(identity)
}

func (aggregate *supervisedAggregate) matchIdentityLocked(identity supervisorTaskIdentity) (*supervisedTaskState, bool) {
	if aggregate == nil {
		return nil, false
	}
	task, ok := aggregate.tasks[strings.TrimSpace(identity.LogicalID)]
	if !ok || task == nil || task.canceled || task.completed {
		return nil, false
	}
	if strings.TrimSpace(task.currentTaskID) != strings.TrimSpace(identity.TaskID) || task.round != identity.Round {
		return nil, false
	}
	return task, true
}

func deferWorkerEventLocked(task *supervisedTaskState, event supervisorAggregateEvent) {
	if task == nil {
		return
	}
	sequence := event.snapshot.Sequence
	if event.snapshot.Checkpoint != nil && event.snapshot.Checkpoint.EventSequence > sequence {
		sequence = event.snapshot.Checkpoint.EventSequence
	}
	if sequence > 0 && task.reviewSequence > 0 && sequence <= task.reviewSequence && event.kind != "worker_terminal" {
		return
	}
	if event.kind != "worker_terminal" && !checkpointSupersedesPendingReview(task, event.snapshot.Checkpoint) {
		return
	}
	if current := task.deferredWorkerEvent; current != nil {
		currentSequence := current.snapshot.Sequence
		if current.snapshot.Checkpoint != nil && current.snapshot.Checkpoint.EventSequence > currentSequence {
			currentSequence = current.snapshot.Checkpoint.EventSequence
		}
		if current.kind == "worker_terminal" && event.kind != "worker_terminal" {
			return
		}
		if event.kind != "worker_terminal" && sequence <= currentSequence {
			return
		}
		if event.kind == "worker_terminal" && current.kind == "worker_terminal" && sequence < currentSequence {
			return
		}
	}
	deferred := event
	task.deferredWorkerEvent = &deferred
}

func checkpointSupersedesPendingReview(task *supervisedTaskState, checkpoint *delegation.WorkerCheckpoint) bool {
	if task == nil || checkpoint == nil {
		return false
	}
	if task.previous == nil {
		return true
	}
	if checkpoint.EventSequence <= task.previous.EventSequence {
		return false
	}
	if checkpoint.EffectiveProgressAt.IsZero() {
		return false
	}
	return task.previous.EffectiveProgressAt.IsZero() || checkpoint.EffectiveProgressAt.After(task.previous.EffectiveProgressAt)
}

func workerSnapshotAlreadyHandled(task *supervisedTaskState, snapshot delegation.TaskSnapshot) bool {
	return task != nil && snapshot.Sequence > 0 && snapshot.Sequence <= task.lastSnapshot.Sequence
}

func taskOwnsAttemptLocked(task *supervisedTaskState, taskID string) bool {
	if task == nil {
		return false
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return false
	}
	if strings.TrimSpace(task.currentTaskID) == taskID || strings.TrimSpace(task.logicalID) == taskID {
		return true
	}
	_, ok := task.attemptTaskIDs[taskID]
	return ok
}

func taskAttemptIDsLocked(task *supervisedTaskState) []string {
	if task == nil {
		return nil
	}
	ids := make([]string, 0, len(task.attemptTaskIDs)+1)
	seen := make(map[string]struct{}, len(task.attemptTaskIDs)+1)
	appendID := func(taskID string) {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			return
		}
		if _, ok := seen[taskID]; ok {
			return
		}
		seen[taskID] = struct{}{}
		ids = append(ids, taskID)
	}
	appendID(task.currentTaskID)
	for taskID := range task.attemptTaskIDs {
		appendID(taskID)
	}
	return ids
}

func recordRecentToolSignatures(task *supervisedTaskState, checkpoint delegation.WorkerCheckpoint) {
	if task == nil || len(checkpoint.RecentToolNames) == 0 {
		return
	}
	current := make([]string, 0, len(checkpoint.RecentToolNames))
	for _, toolName := range checkpoint.RecentToolNames {
		signature := delegation.NormalizeToolSignature(toolName, nil)
		if strings.Contains(toolName, "#") {
			signature = delegation.NormalizeToolSignatureValue(toolName)
		}
		if signature != "" {
			current = append(current, signature)
		}
	}
	if len(current) == 0 {
		return
	}
	const maxRecentToolSignatures = 12
	task.recentSignatures = append(task.recentSignatures, current...)
	if len(task.recentSignatures) > maxRecentToolSignatures {
		task.recentSignatures = append([]string(nil), task.recentSignatures[len(task.recentSignatures)-maxRecentToolSignatures:]...)
	}
}

func (aggregate *supervisedAggregate) snapshotIdentityMatches(identity supervisorTaskIdentity, snapshot delegation.TaskSnapshot) bool {
	if aggregate == nil {
		return false
	}
	if strings.TrimSpace(snapshot.ID) != strings.TrimSpace(identity.TaskID) || strings.TrimSpace(snapshot.ParentExecID) != strings.TrimSpace(identity.ParentExec) {
		return false
	}
	if snapshot.SupervisionRound != identity.Round {
		return false
	}
	if checkpoint := snapshot.Checkpoint; checkpoint != nil && checkpoint.Round > 0 && checkpoint.Round != identity.Round {
		return false
	}
	return true
}

func (aggregate *supervisedAggregate) resultIdentityMatches(task *supervisedTaskState, identity supervisorTaskIdentity, snapshot delegation.TaskSnapshot) bool {
	return task != nil && aggregate.snapshotIdentityMatches(identity, snapshot)
}

func (aggregate *supervisedAggregate) settleCurrentTask(task *supervisedTaskState) {
	if task == nil {
		return
	}
	aggregate.mu.Lock()
	if task.canceled || task.completed {
		aggregate.mu.Unlock()
		return
	}
	reviewCancel := task.reviewCancel
	task.reviewCancel = nil
	task.reviewPending = false
	task.reviewSequence = 0
	task.deferredWorkerEvent = nil
	task.completed = true
	applyTaskRuntimeMetadata(task)
	if task.lastSnapshot.SupervisionStatus == "" {
		task.lastSnapshot.SupervisionStatus = supervisionStatusFromTaskStatus(task.lastSnapshot.Status)
	}
	aggregate.mu.Unlock()
	if reviewCancel != nil {
		reviewCancel()
	}
	aggregate.rememberTaskRuntimeState(task)
}

func (aggregate *supervisedAggregate) markTaskFailed(task *supervisedTaskState, code delegation.SupervisionIssueCode, reason string, circuitOpen bool) {
	if task == nil {
		return
	}
	aggregate.mu.Lock()
	if task.canceled || task.completed {
		aggregate.mu.Unlock()
		return
	}
	reviewCancel := task.reviewCancel
	task.reviewCancel = nil
	task.reviewPending = false
	task.reviewSequence = 0
	task.deferredWorkerEvent = nil
	task.completed = true
	applyTaskRuntimeMetadata(task)
	task.lastSnapshot.Status = delegation.TaskFailed
	if circuitOpen {
		task.lastSnapshot.SupervisionStatus = delegation.SupervisionStatusCircuitOpen
	} else {
		task.lastSnapshot.SupervisionStatus = delegation.SupervisionStatusFailed
	}
	safeReason := delegation.SanitizeSupervisorText(firstNonEmpty(strings.TrimSpace(reason), "supervision failed"), task.contract.WorkspaceHint)
	task.lastSnapshot.Error = truncateProjectedReplayText("Supervisor reason", safeReason, 512)
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
	applyTaskRuntimeMetadata(task)
	currentTaskID := strings.TrimSpace(task.currentTaskID)
	attemptTaskIDs := taskAttemptIDsLocked(task)
	aggregate.mu.Unlock()
	if reviewCancel != nil {
		reviewCancel()
	}
	if aggregate.coordinator != nil && aggregate.coordinator.scheduler != nil {
		for _, attemptTaskID := range attemptTaskIDs {
			aggregate.coordinator.scheduler.CancelIfActive(attemptTaskID)
			if attemptTaskID != currentTaskID {
				aggregate.coordinator.forgetRuntimeState(attemptTaskID)
			}
		}
	}
	aggregate.rememberTaskRuntimeState(task)
}

func (aggregate *supervisedAggregate) markTaskAbandoned(task *supervisedTaskState, code delegation.SupervisionIssueCode, reason string) {
	if task == nil {
		return
	}
	aggregate.mu.Lock()
	if task.canceled || task.completed {
		aggregate.mu.Unlock()
		return
	}
	reviewCancel := task.reviewCancel
	task.reviewCancel = nil
	task.completed = true
	task.reviewPending = false
	task.reviewSequence = 0
	task.deferredWorkerEvent = nil
	applyTaskRuntimeMetadata(task)
	task.lastSnapshot.Status = delegation.TaskCanceled
	task.lastSnapshot.SupervisionStatus = delegation.SupervisionStatusCanceled
	safeReason := delegation.SanitizeSupervisorText(firstNonEmpty(strings.TrimSpace(reason), "stale supervised event ignored"), task.contract.WorkspaceHint)
	task.lastSnapshot.Error = truncateProjectedReplayText("Supervisor reason", safeReason, 512)
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
	applyTaskRuntimeMetadata(task)
	currentTaskID := strings.TrimSpace(task.currentTaskID)
	attemptTaskIDs := taskAttemptIDsLocked(task)
	aggregate.mu.Unlock()
	if reviewCancel != nil {
		reviewCancel()
	}
	if aggregate.coordinator != nil && aggregate.coordinator.scheduler != nil {
		for _, attemptTaskID := range attemptTaskIDs {
			aggregate.coordinator.scheduler.CancelIfActive(attemptTaskID)
			if attemptTaskID != currentTaskID {
				aggregate.coordinator.forgetRuntimeState(attemptTaskID)
			}
		}
	}
	aggregate.rememberTaskRuntimeState(task)
}

func applyTaskRuntimeMetadata(task *supervisedTaskState) {
	if task == nil {
		return
	}
	task.lastSnapshot.ID = firstNonEmpty(strings.TrimSpace(task.currentTaskID), strings.TrimSpace(task.lastSnapshot.ID))
	task.lastSnapshot.ModelID = firstNonEmpty(strings.TrimSpace(task.currentRequest.ModelID), strings.TrimSpace(task.lastSnapshot.ModelID))
	task.lastSnapshot.ModelName = firstNonEmpty(strings.TrimSpace(task.currentRequest.ModelName), strings.TrimSpace(task.lastSnapshot.ModelName))
	task.lastSnapshot.ModelGroupID = firstNonEmpty(strings.TrimSpace(task.currentRequest.ModelGroupID), strings.TrimSpace(task.lastSnapshot.ModelGroupID))
	task.lastSnapshot.WorkerRole = firstNonEmpty(strings.TrimSpace(task.currentRequest.SubagentType), strings.TrimSpace(task.contract.Role), "generalPurpose")
	task.lastSnapshot.ExecutionMode = firstNonEmpty(strings.TrimSpace(task.currentRequest.ExecutionMode), strings.TrimSpace(task.lastSnapshot.ExecutionMode))
	task.lastSnapshot.SupervisionRound = task.round
	task.lastSnapshot.CorrectionCount = task.corrections
	task.lastSnapshot.RetryCount = task.retries
	task.lastSnapshot.ReassignCount = task.reassignments
	if task.escalated {
		task.lastSnapshot.EscalateCount = 1
	} else {
		task.lastSnapshot.EscalateCount = 0
	}
	task.lastSnapshot.SupervisionIssue = issueCodeOrDefault(task.lastIssue, "")
	task.lastSnapshot.ProgressSummary = currentProgressSummary(task.lastSnapshot)
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
		ProgressSummary:      "",
		Blocker:              strings.TrimSpace(snapshot.Error),
		EffectiveProgressAt:  snapshot.FinishedAt,
	}
}

func currentProgressSummary(snapshot delegation.TaskSnapshot) string {
	if value := strings.TrimSpace(snapshot.ProgressSummary); value != "" {
		return value
	}
	if snapshot.Checkpoint != nil {
		return strings.TrimSpace(snapshot.Checkpoint.ProgressSummary)
	}
	return ""
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
	// ProviderPass identifies the provider attempt that opened the exec, but it
	// is not the identity of the aggregate. A still-pending aggregate remains
	// valid across provider resumes; the exact exec/task identity checks above
	// provide the stale-event protection.
	return true
}
