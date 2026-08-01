// Package delegation 提供与 Cursor 客户端无关的非阻塞子代理调度能力。
package delegation

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

const (
	DefaultMaxConcurrency = 4
	DefaultRetentionLimit = 256
	DefaultRetentionAge   = 10 * time.Minute
)

type TaskStatus string

const (
	TaskQueued    TaskStatus = "queued"
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCanceled  TaskStatus = "canceled"
	TaskTimedOut  TaskStatus = "timed_out"
)

// TaskRequest 是调度器与具体子代理适配器之间的最小输入契约。
type TaskRequest struct {
	ID                           string
	ParentRequest                string
	ParentExecID                 string
	ParentToolCall               string
	ConversationID               string
	RootConversationID           string
	ArgsJSON                     []byte
	Prompt                       string
	Description                  string
	SubagentType                 string
	Readonly                     bool
	RunInBackground              bool
	ModelID                      string
	ModelName                    string
	Mode                         agentv1.AgentMode
	ThinkingEffort               string
	MaxMode                      bool
	Contract                     *SupervisionTaskContract
	SubagentModelOverrides       map[string]runtimecore.SubagentModelOverrideSelection
	SelectedSubagentModels       []*agentv1.RequestedModel
	SelectedSubagentModelDetails []*agentv1.ModelDetails
	ModelGroupID                 string
	ExecutionMode                string
	WorkspaceHint                string
	ToolPermission               map[string]bool
	RuntimeSupervisionRound      int
	RuntimeCorrectionCount       int
	RuntimeRetryCount            int
	RuntimeReassignCount         int
	RuntimeEscalateCount         int
	RuntimeSupervisionIssue      SupervisionIssueCode
	RuntimeProgressSummary       string
	ModelParams                  []*agentv1.RequestedModel_ModelParameterValue
	// QueueTimeout bounds how long a worker can wait for a scheduler slot. It
	// protects later delegation batches from being held behind stalled workers.
	QueueTimeout time.Duration
	Timeout      time.Duration
}

// TaskResult 是适配器返回给主代理的统一结果。
type TaskResult struct {
	Output         string
	Error          error
	ToolCallCount  int
	SubagentResult *agentv1.SubagentResult
	Metadata       map[string]string
}

// TaskSnapshot 是 UI 和主代理读取的稳定状态快照，只保留安全元数据。
type TaskSnapshot struct {
	ID                string
	Description       string
	ModelID           string
	ModelName         string
	ModelGroupID      string
	WorkerRole        string
	ExecutionMode     string
	ParentRequestID   string
	ParentExecID      string
	GroupID           string
	Checkpoint        *WorkerCheckpoint
	SupervisionStatus SupervisionStatus
	Counters          SupervisionCounters
	SupervisionRound  int
	CorrectionCount   int
	RetryCount        int
	ReassignCount     int
	EscalateCount     int
	SupervisionIssue  SupervisionIssueCode
	ProgressSummary   string
	Status            TaskStatus
	Output            string
	Error             string
	ToolCallCount     int
	EventID           string
	Sequence          uint64
	EventType         string
	QueuedAt          time.Time
	StartedAt         time.Time
	FinishedAt        time.Time
	UpdatedAt         time.Time
}

type Executor func(context.Context, TaskRequest) TaskResult

type Scheduler struct {
	maxConcurrency  int
	slots           chan struct{}
	executor        Executor
	ctx             context.Context
	cancel          context.CancelFunc
	retentionBase   int
	retentionMargin int
	retentionLimit  int
	retentionAge    time.Duration

	mu               sync.RWMutex
	tasks            map[string]*taskState
	activeExecutions map[string]struct{}
	closed           bool
	events           chan TaskSnapshot
	eventsClosed     bool
	stateChanged     chan struct{}
	taskSequence     atomic.Uint64
	eventSequence    atomic.Uint64
	closeOnce        sync.Once
}

type taskState struct {
	request    TaskRequest
	snapshot   TaskSnapshot
	result     TaskResult
	contract   *SupervisionTaskContract
	checkpoint *WorkerCheckpoint
	counters   SupervisionCounters
	ctx        context.Context
	cancel     context.CancelFunc
	runnerDone bool
	lastEffectiveProgressAt time.Time
}

type Config struct {
	MaxConcurrency int
	EventBuffer    int
	RetentionLimit int
	RetentionAge   time.Duration
}

func NewScheduler(cfg Config, executor Executor) *Scheduler {
	maxConcurrency := cfg.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	eventBuffer := cfg.EventBuffer
	if eventBuffer <= 0 {
		eventBuffer = 128
	}
	retentionLimit := cfg.RetentionLimit
	if retentionLimit <= 0 {
		retentionLimit = DefaultRetentionLimit
	}
	retentionAge := cfg.RetentionAge
	if retentionAge <= 0 {
		retentionAge = DefaultRetentionAge
	}
	if eventBuffer < retentionLimit {
		eventBuffer = retentionLimit
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		maxConcurrency:   maxConcurrency,
		slots:            make(chan struct{}, maxConcurrency),
		executor:         executor,
		ctx:              ctx,
		cancel:           cancel,
		retentionBase:    retentionLimit,
		retentionLimit:   retentionLimit,
		retentionAge:     retentionAge,
		tasks:            make(map[string]*taskState),
		activeExecutions: make(map[string]struct{}),
		events:           make(chan TaskSnapshot, eventBuffer),
		stateChanged:     make(chan struct{}),
	}
}

// Submit 只负责登记和启动任务，不等待执行结果。
func (s *Scheduler) Submit(request TaskRequest) (string, error) {
	if s == nil {
		return "", fmt.Errorf("delegation scheduler is nil")
	}
	if s.executor == nil {
		return "", fmt.Errorf("delegation executor is nil")
	}
	request.ID = strings.TrimSpace(request.ID)
	if request.ID == "" {
		request.ID = fmt.Sprintf("delegated-%d", s.taskSequence.Add(1))
	}
	request = cloneTaskRequest(request)
	now := time.Now().UTC()
	contract, err := canonicalizeSupervisionTaskContract(request)
	if err != nil {
		return "", err
	}
	request.Contract = cloneSupervisionTaskContract(contract)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", fmt.Errorf("delegation scheduler is closed")
	}
	if _, exists := s.tasks[request.ID]; exists {
		s.mu.Unlock()
		return "", fmt.Errorf("delegated task %q already exists", request.ID)
	}
	if _, exists := s.activeExecutions[request.ID]; exists {
		s.mu.Unlock()
		return "", fmt.Errorf("delegated task %q is still executing", request.ID)
	}
	taskCtx, taskCancel := context.WithCancel(s.ctx)
	snapshot := buildTaskSnapshot(request, now)
	if contract != nil {
		snapshot.SupervisionStatus = SupervisionStatusPlanned
	}
	state := &taskState{
		request:  request,
		snapshot: snapshot,
		contract: contract,
		ctx:      taskCtx,
		cancel:   taskCancel,
		lastEffectiveProgressAt: now,
	}
	s.tasks[request.ID] = state
	s.activeExecutions[request.ID] = struct{}{}
	s.pruneTerminalTasksLocked()
	s.publishLocked(&state.snapshot)
	s.mu.Unlock()
	go s.run(state)
	return request.ID, nil
}

func (s *Scheduler) run(state *taskState) {
	executorStarted := false
	defer func() {
		state.cancel()
		s.mu.Lock()
		state.runnerDone = true
		if !executorStarted {
			delete(s.activeExecutions, state.snapshot.ID)
		}
		s.pruneTerminalTasksLocked()
		s.purgeBufferedEventsLocked()
		s.mu.Unlock()
	}()
	queueCtx := state.ctx
	cancelQueueTimeout := func() {}
	if state.request.QueueTimeout > 0 {
		queueCtx, cancelQueueTimeout = context.WithTimeout(state.ctx, state.request.QueueTimeout)
	}
	defer cancelQueueTimeout()
	select {
	case s.slots <- struct{}{}:
	case <-queueCtx.Done():
		s.finishFromContext(state, queueCtx.Err())
		return
	}
	defer func() { <-s.slots }()

	s.mu.Lock()
	if isTerminalStatus(state.snapshot.Status) {
		s.mu.Unlock()
		return
	}
	state.snapshot.Status = TaskRunning
	if state.contract != nil {
		state.snapshot.SupervisionStatus = SupervisionStatusRunning
	}
	state.snapshot.StartedAt = time.Now().UTC()
	state.lastEffectiveProgressAt = state.snapshot.StartedAt
	request := cloneTaskRequest(state.request)
	s.publishLocked(&state.snapshot)
	s.mu.Unlock()

	executionCtx := state.ctx
	cancelTimeout := func() {}
	if request.Timeout > 0 {
		executionCtx, cancelTimeout = context.WithTimeout(state.ctx, request.Timeout)
	}
	defer cancelTimeout()
	taskID := state.snapshot.ID
	executionCtx = withWorkerCheckpointPublisher(executionCtx, func(checkpoint WorkerCheckpoint) bool {
		return s.PublishCheckpoint(taskID, checkpoint)
	})
	executionCtx = withWorkerProgressPublisher(executionCtx, func() bool {
		return s.MarkEffectiveProgress(taskID)
	})
	resultChannel := make(chan TaskResult, 1)
	executorStarted = true
	go s.watchEffectiveProgress(state)
	go func(taskID string) {
		result := s.executor(executionCtx, request)
		s.mu.Lock()
		delete(s.activeExecutions, taskID)
		s.mu.Unlock()
		resultChannel <- result
	}(state.snapshot.ID)
	var result TaskResult
	select {
	case result = <-resultChannel:
	case <-executionCtx.Done():
		result.Error = executionCtx.Err()
	}
	finished := time.Now().UTC()
	s.mu.Lock()
	if isTerminalStatus(state.snapshot.Status) {
		s.mu.Unlock()
		return
	}
	state.snapshot.Output = result.Output
	state.snapshot.ToolCallCount = result.ToolCallCount
	state.snapshot.FinishedAt = finished
	state.result = cloneTaskResult(result)
	if executionCtx.Err() != nil {
		if executionCtx.Err() == context.DeadlineExceeded {
			state.snapshot.Status = TaskTimedOut
			state.snapshot.Error = executionCtx.Err().Error()
		} else {
			state.snapshot.Status = TaskCanceled
		}
	} else if result.Error != nil {
		state.snapshot.Status = TaskFailed
		state.snapshot.Error = SanitizeSupervisorText(result.Error.Error(), request.WorkspaceHint)
	} else {
		state.snapshot.Status = TaskCompleted
	}
	if state.contract != nil {
		state.snapshot.SupervisionStatus = supervisionStatusForTaskStatus(state.snapshot.Status)
	}
	s.publishLocked(&state.snapshot)
	s.mu.Unlock()
}

// Cancel 只取消目标任务，不影响其他委派任务。
func (s *Scheduler) Cancel(taskID string) error {
	if s == nil {
		return fmt.Errorf("delegation scheduler is nil")
	}
	s.mu.Lock()
	state, ok := s.tasks[taskID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("delegated task %q not found", taskID)
	}
	if isTerminalStatus(state.snapshot.Status) {
		s.mu.Unlock()
		return nil
	}
	logSchedulerCancellation("cancel", taskID, state.snapshot.Status)
	state.snapshot.Status = TaskCanceled
	if state.contract != nil {
		state.snapshot.SupervisionStatus = SupervisionStatusCanceled
	}
	state.snapshot.FinishedAt = time.Now().UTC()
	state.cancel()
	s.publishLocked(&state.snapshot)
	s.mu.Unlock()
	return nil
}

// CancelIfActive atomically cancels a non-terminal task and reports whether
// this call changed its state.
func (s *Scheduler) CancelIfActive(taskID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	state, ok := s.tasks[taskID]
	if !ok || isTerminalStatus(state.snapshot.Status) {
		s.mu.Unlock()
		return false
	}
	logSchedulerCancellation("cancel_if_active", taskID, state.snapshot.Status)
	state.snapshot.Status = TaskCanceled
	if state.contract != nil {
		state.snapshot.SupervisionStatus = SupervisionStatusCanceled
	}
	state.snapshot.FinishedAt = time.Now().UTC()
	state.cancel()
	s.publishLocked(&state.snapshot)
	s.mu.Unlock()
	return true
}

func logSchedulerCancellation(action string, taskID string, status TaskStatus) {
	callerFile := "unknown"
	callerLine := 0
	if _, file, line, ok := runtime.Caller(2); ok {
		callerFile = file
		callerLine = line
	}
	log.Printf(
		"delegation scheduler task cancellation action=%s task_id=%s previous_status=%s caller=%s:%d",
		strings.TrimSpace(action),
		strings.TrimSpace(taskID),
		strings.TrimSpace(string(status)),
		callerFile,
		callerLine,
	)
}

func (s *Scheduler) PublishCheckpoint(taskID string, checkpoint WorkerCheckpoint) bool {
	if s == nil {
		return false
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return false
	}
	checkpoint = normalizeWorkerCheckpoint(checkpoint)
	if checkpoint.TaskID == "" || checkpoint.TaskID != taskID {
		return false
	}
	s.mu.Lock()
	state, ok := s.tasks[taskID]
	if !ok || s.closed || isTerminalStatus(state.snapshot.Status) || state.contract == nil {
		s.mu.Unlock()
		return false
	}
	contract := state.contract
	if strings.TrimSpace(contract.TaskID) == "" || checkpoint.TaskID != contract.TaskID {
		s.mu.Unlock()
		return false
	}
	if strings.TrimSpace(contract.AggregateID) == "" || checkpoint.AggregateID == "" || checkpoint.AggregateID != contract.AggregateID {
		s.mu.Unlock()
		return false
	}
	if checkpoint.Round > 0 && checkpoint.Round != contract.Round {
		s.mu.Unlock()
		return false
	}
	if checkpoint.Round <= 0 && contract.Round > 0 {
		checkpoint.Round = contract.Round
	}
	checkpoint = normalizeSupervisedWorkerCheckpoint(checkpoint, contract.WorkspaceHint)
	now := time.Now().UTC()
	checkpoint.EffectiveProgressAt = resolveCheckpointEffectiveProgressAt(state.checkpoint, checkpoint, now)
	if state.checkpoint == nil || checkpointShowsEffectiveProgress(*state.checkpoint, checkpoint) {
		state.lastEffectiveProgressAt = now
	}
	state.checkpoint = cloneWorkerCheckpoint(&checkpoint)
	state.snapshot.Checkpoint = cloneWorkerCheckpoint(&checkpoint)
	state.counters.Checkpoints++
	if checkpoint.Round > state.counters.Rounds {
		state.counters.Rounds = checkpoint.Round
	}
	state.snapshot.Counters = cloneSupervisionCounters(state.counters)
	state.snapshot.SupervisionStatus = checkpoint.Phase
	state.snapshot.SupervisionRound = checkpoint.Round
	state.snapshot.ProgressSummary = checkpoint.ProgressSummary
	s.publishLocked(&state.snapshot)
	s.mu.Unlock()
	return true
}

// MarkEffectiveProgress records a real provider/tool/file event without
// publishing a synthetic checkpoint. Transport heartbeats and periodic
// summaries must not call this method.
func (s *Scheduler) MarkEffectiveProgress(taskID string) bool {
	if s == nil {
		return false
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.tasks[taskID]
	if !ok || isTerminalStatus(state.snapshot.Status) {
		return false
	}
	state.lastEffectiveProgressAt = time.Now().UTC()
	return true
}

func (s *Scheduler) watchEffectiveProgress(state *taskState) {
	if s == nil || state == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-state.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			if isTerminalStatus(state.snapshot.Status) {
				s.mu.Unlock()
				return
			}
			last := state.lastEffectiveProgressAt
			if last.IsZero() {
				last = state.snapshot.StartedAt
			}
			if time.Since(last) < DefaultEffectiveProgressTimeout {
				s.mu.Unlock()
				continue
			}
			state.snapshot.Status = TaskTimedOut
			state.snapshot.Error = fmt.Sprintf("无有效进展超时：连续 %s 没有新的 provider 事件、工具调用、工具结果、文件变更或有效 checkpoint", DefaultEffectiveProgressTimeout)
			state.snapshot.FinishedAt = time.Now().UTC()
			state.result.Error = errors.New(state.snapshot.Error)
			if state.contract != nil {
				state.snapshot.SupervisionStatus = supervisionStatusForTaskStatus(state.snapshot.Status)
			}
			state.cancel()
			s.publishLocked(&state.snapshot)
			s.mu.Unlock()
			return
		}
	}
}

func resolveCheckpointEffectiveProgressAt(previous *WorkerCheckpoint, checkpoint WorkerCheckpoint, now time.Time) time.Time {
	if previous != nil && !previous.EffectiveProgressAt.IsZero() {
		previousAt := previous.EffectiveProgressAt.UTC()
		if checkpointShowsEffectiveProgress(*previous, checkpoint) {
			return now.UTC()
		}
		return previousAt
	}
	return now.UTC()
}

func checkpointShowsEffectiveProgress(previous WorkerCheckpoint, current WorkerCheckpoint) bool {
	if strings.TrimSpace(current.ProgressSummary) != strings.TrimSpace(previous.ProgressSummary) {
		return true
	}
	if strings.TrimSpace(current.Blocker) != strings.TrimSpace(previous.Blocker) {
		return true
	}
	if current.Phase != previous.Phase {
		return true
	}
	if strings.Join(current.ChangedFileSummaries, "\x00") != strings.Join(previous.ChangedFileSummaries, "\x00") {
		return true
	}
	return strings.Join(current.RecentToolNames, "\x00") != strings.Join(previous.RecentToolNames, "\x00")
}

func (s *Scheduler) Snapshot(taskID string) (TaskSnapshot, bool) {
	if s == nil {
		return TaskSnapshot{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.tasks[taskID]
	if !ok {
		return TaskSnapshot{}, false
	}
	return cloneTaskSnapshot(state.snapshot), true
}

func (s *Scheduler) Result(taskID string) (TaskResult, bool) {
	if s == nil {
		return TaskResult{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.tasks[taskID]
	if !ok {
		return TaskResult{}, false
	}
	return cloneTaskResult(state.result), true
}

// WaitForTaskUpdate waits for a newer immutable snapshot for one task. It uses
// the scheduler state-change signal instead of consuming the shared Events
// channel, so multiple supervised aggregates can observe progress independently.
func (s *Scheduler) WaitForTaskUpdate(ctx context.Context, taskID string, afterSequence uint64) (TaskSnapshot, error) {
	if s == nil {
		return TaskSnapshot{}, fmt.Errorf("delegation scheduler is nil")
	}
	if ctx == nil {
		return TaskSnapshot{}, fmt.Errorf("delegation wait context is nil")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return TaskSnapshot{}, fmt.Errorf("delegation wait task id is required")
	}
	for {
		s.mu.RLock()
		state, ok := s.tasks[taskID]
		if !ok {
			s.mu.RUnlock()
			return TaskSnapshot{}, fmt.Errorf("delegated task %q not found", taskID)
		}
		snapshot := cloneTaskSnapshot(state.snapshot)
		waitCh := s.stateChanged
		s.mu.RUnlock()
		if snapshot.Sequence > afterSequence || isTerminalStatus(snapshot.Status) {
			return snapshot, nil
		}
		select {
		case <-ctx.Done():
			return TaskSnapshot{}, ctx.Err()
		case <-waitCh:
		}
	}
}

func (s *Scheduler) Snapshots() []TaskSnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]TaskSnapshot, 0, len(s.tasks))
	for _, state := range s.tasks {
		items = append(items, cloneTaskSnapshot(state.snapshot))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].QueuedAt.Equal(items[j].QueuedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].QueuedAt.Before(items[j].QueuedAt)
	})
	return items
}

func (s *Scheduler) Events() <-chan TaskSnapshot {
	if s == nil {
		return nil
	}
	return s.events
}

func (s *Scheduler) WaitForTerminal(ctx context.Context, taskIDs []string) error {
	if s == nil {
		return fmt.Errorf("delegation scheduler is nil")
	}
	if ctx == nil {
		return fmt.Errorf("delegation wait context is nil")
	}
	if len(taskIDs) == 0 {
		return fmt.Errorf("delegation wait task ids are required")
	}
	trimmedIDs := make([]string, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			return fmt.Errorf("delegation wait task ids must not be blank")
		}
		trimmedIDs = append(trimmedIDs, taskID)
	}
	for {
		s.mu.RLock()
		waitCh := s.stateChanged
		allTerminal := true
		for _, taskID := range trimmedIDs {
			state, ok := s.tasks[taskID]
			if !ok {
				s.mu.RUnlock()
				return fmt.Errorf("delegated task %q not found", taskID)
			}
			if !isTerminalStatus(state.snapshot.Status) {
				allTerminal = false
			}
		}
		s.mu.RUnlock()
		if allTerminal {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-waitCh:
		}
	}
}

// ReserveRetentionMargin keeps the largest aggregate's worker count as bounded
// headroom above the live workload. It does not accumulate historical task
// counts, and a smaller overlapping aggregate cannot shrink an active batch's
// retention safety margin.
func (s *Scheduler) ReserveRetentionMargin(margin int) {
	if s == nil {
		return
	}
	if margin < 0 {
		margin = 0
	}
	s.mu.Lock()
	if margin > s.retentionMargin {
		s.retentionMargin = margin
	}
	s.pruneTerminalTasksLocked()
	s.mu.Unlock()
}

func (s *Scheduler) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		now := time.Now().UTC()
		s.mu.Lock()
		s.closed = true
		for _, state := range s.tasks {
			if isTerminalStatus(state.snapshot.Status) {
				continue
			}
			state.snapshot.Status = TaskCanceled
			if state.contract != nil {
				state.snapshot.SupervisionStatus = SupervisionStatusCanceled
			}
			state.snapshot.FinishedAt = now
			state.cancel()
			s.publishLocked(&state.snapshot)
		}
		s.pruneTerminalTasksLocked()
		s.purgeBufferedEventsLocked()
		s.eventsClosed = true
		close(s.events)
		s.mu.Unlock()
		s.cancel()
	})
}

func (s *Scheduler) publishLocked(snapshot *TaskSnapshot) {
	if snapshot == nil {
		return
	}
	s.decorateSnapshotLocked(snapshot)
	s.notifyStateChangedLocked()
	if s.eventsClosed {
		return
	}
	event := cloneTaskSnapshot(*snapshot)
	select {
	case s.events <- event:
	default:
		if !isTerminalStatus(event.Status) {
			return
		}
		s.purgeBufferedEventsLocked()
		if len(s.events) >= cap(s.events) {
			s.evictOldestNonTerminalEventLocked()
		}
		select {
		case s.events <- event:
		default:
		}
	}
}

func (s *Scheduler) finishFromContext(state *taskState, cause error) {
	s.mu.Lock()
	if isTerminalStatus(state.snapshot.Status) {
		s.mu.Unlock()
		return
	}
	state.snapshot.FinishedAt = time.Now().UTC()
	if cause == context.DeadlineExceeded {
		state.snapshot.Status = TaskTimedOut
		state.snapshot.Error = cause.Error()
	} else {
		state.snapshot.Status = TaskCanceled
	}
	if state.contract != nil {
		state.snapshot.SupervisionStatus = supervisionStatusForTaskStatus(state.snapshot.Status)
	}
	s.publishLocked(&state.snapshot)
	s.mu.Unlock()
}

func (s *Scheduler) pruneTerminalTasksLocked() {
	s.refreshRetentionLimitLocked()
	pruned := false
	cutoff := time.Now().UTC().Add(-s.retentionAge)
	for len(s.tasks) > s.retentionLimit {
		var oldestID string
		var oldestTime time.Time
		for taskID, state := range s.tasks {
			if !state.runnerDone || !isTerminalStatus(state.snapshot.Status) || state.snapshot.FinishedAt.After(cutoff) {
				continue
			}
			if oldestID == "" || state.snapshot.FinishedAt.Before(oldestTime) {
				oldestID = taskID
				oldestTime = state.snapshot.FinishedAt
			}
		}
		if oldestID == "" {
			break
		}
		delete(s.tasks, oldestID)
		pruned = true
	}
	if pruned {
		s.notifyStateChangedLocked()
	}
}

func (s *Scheduler) refreshRetentionLimitLocked() {
	limit := s.retentionBase
	if liveLimit := s.liveTaskCountLocked() + s.retentionMargin; liveLimit > limit {
		limit = liveLimit
	}
	if limit <= 0 {
		limit = DefaultRetentionLimit
	}
	s.retentionLimit = limit
}

func (s *Scheduler) liveTaskCountLocked() int {
	live := 0
	for _, state := range s.tasks {
		if state == nil {
			continue
		}
		if !state.runnerDone || !isTerminalStatus(state.snapshot.Status) {
			live++
		}
	}
	return live
}

func (s *Scheduler) purgeBufferedEventsLocked() {
	if s.eventsClosed || len(s.events) == 0 {
		return
	}
	buffered := make([]TaskSnapshot, 0, len(s.events))
	for {
		select {
		case event := <-s.events:
			if _, retained := s.tasks[event.ID]; retained {
				buffered = append(buffered, event)
			}
		default:
			for _, event := range buffered {
				s.events <- event
			}
			return
		}
	}
}

func (s *Scheduler) evictOldestNonTerminalEventLocked() {
	if s.eventsClosed || len(s.events) == 0 {
		return
	}
	buffered := make([]TaskSnapshot, 0, len(s.events))
	dropped := false
	for {
		select {
		case event := <-s.events:
			if !dropped && !isTerminalStatus(event.Status) {
				dropped = true
				continue
			}
			buffered = append(buffered, event)
		default:
			for _, event := range buffered {
				s.events <- event
			}
			return
		}
	}
}

func isTerminalStatus(status TaskStatus) bool {
	switch status {
	case TaskCompleted, TaskFailed, TaskCanceled, TaskTimedOut:
		return true
	default:
		return false
	}
}

func (s *Scheduler) decorateSnapshotLocked(snapshot *TaskSnapshot) {
	if snapshot == nil {
		return
	}
	now := time.Now().UTC()
	snapshot.Sequence = s.eventSequence.Add(1)
	snapshot.EventID = fmt.Sprintf("delegation-event-%d", snapshot.Sequence)
	switch {
	case isTerminalStatus(snapshot.Status):
		if snapshot.SupervisionStatus != "" {
			snapshot.SupervisionStatus = supervisionStatusForTaskStatus(snapshot.Status)
		}
		snapshot.EventType = string(snapshot.Status)
	case snapshot.Checkpoint != nil && snapshot.Checkpoint.Phase != "":
		snapshot.EventType = string(snapshot.Checkpoint.Phase)
	case snapshot.SupervisionStatus != "":
		snapshot.EventType = string(snapshot.SupervisionStatus)
	default:
		snapshot.EventType = string(snapshot.Status)
	}
	if snapshot.Checkpoint != nil {
		snapshot.Checkpoint.EventSequence = snapshot.Sequence
	}
	snapshot.UpdatedAt = now
}

func (s *Scheduler) notifyStateChangedLocked() {
	if s.stateChanged == nil {
		s.stateChanged = make(chan struct{})
	}
	close(s.stateChanged)
	s.stateChanged = make(chan struct{})
}

func cloneTaskRequest(request TaskRequest) TaskRequest {
	request.ArgsJSON = append([]byte(nil), request.ArgsJSON...)
	request.Contract = cloneSupervisionTaskContract(request.Contract)
	request.ToolPermission = cloneToolPermissions(request.ToolPermission)
	request.SubagentModelOverrides = cloneSubagentModelOverrides(request.SubagentModelOverrides)
	request.SelectedSubagentModels = cloneRequestedModels(request.SelectedSubagentModels)
	request.SelectedSubagentModelDetails = cloneModelDetails(request.SelectedSubagentModelDetails)
	request.ModelParams = cloneModelParams(request.ModelParams)
	return request
}

func cloneTaskSnapshot(snapshot TaskSnapshot) TaskSnapshot {
	// Keep this external clone explicit: TaskSnapshot is a safe DTO and must
	// never grow an implicit copy of the internal TaskRequest or contract.
	return TaskSnapshot{
		ID:                snapshot.ID,
		Description:       snapshot.Description,
		ModelID:           snapshot.ModelID,
		ModelName:         snapshot.ModelName,
		ModelGroupID:      snapshot.ModelGroupID,
		WorkerRole:        snapshot.WorkerRole,
		ExecutionMode:     snapshot.ExecutionMode,
		ParentRequestID:   snapshot.ParentRequestID,
		ParentExecID:      snapshot.ParentExecID,
		GroupID:           snapshot.GroupID,
		Checkpoint:        cloneWorkerCheckpoint(snapshot.Checkpoint),
		SupervisionStatus: snapshot.SupervisionStatus,
		Counters:          cloneSupervisionCounters(snapshot.Counters),
		SupervisionRound:  snapshot.SupervisionRound,
		CorrectionCount:   snapshot.CorrectionCount,
		RetryCount:        snapshot.RetryCount,
		ReassignCount:     snapshot.ReassignCount,
		EscalateCount:     snapshot.EscalateCount,
		SupervisionIssue:  snapshot.SupervisionIssue,
		ProgressSummary:   snapshot.ProgressSummary,
		Status:            snapshot.Status,
		Output:            snapshot.Output,
		Error:             snapshot.Error,
		ToolCallCount:     snapshot.ToolCallCount,
		EventID:           snapshot.EventID,
		Sequence:          snapshot.Sequence,
		EventType:         snapshot.EventType,
		QueuedAt:          snapshot.QueuedAt,
		StartedAt:         snapshot.StartedAt,
		FinishedAt:        snapshot.FinishedAt,
		UpdatedAt:         snapshot.UpdatedAt,
	}
}

func buildTaskSnapshot(request TaskRequest, queuedAt time.Time) TaskSnapshot {
	workerRole := ""
	supervisionRound := request.RuntimeSupervisionRound
	if request.Contract != nil {
		workerRole = firstNonEmpty(strings.TrimSpace(request.SubagentType), "generalPurpose")
		if supervisionRound <= 0 {
			supervisionRound = request.Contract.Round
		}
	}
	return TaskSnapshot{
		ID:               request.ID,
		Description:      strings.TrimSpace(request.Description),
		ModelID:          strings.TrimSpace(request.ModelID),
		ModelName:        strings.TrimSpace(request.ModelName),
		ModelGroupID:     strings.TrimSpace(request.ModelGroupID),
		WorkerRole:       workerRole,
		ExecutionMode:    strings.TrimSpace(request.ExecutionMode),
		ParentRequestID:  strings.TrimSpace(request.ParentRequest),
		ParentExecID:     strings.TrimSpace(request.ParentExecID),
		GroupID:          strings.TrimSpace(firstNonEmpty(request.ParentExecID, request.ParentRequest)),
		SupervisionRound: supervisionRound,
		CorrectionCount:  request.RuntimeCorrectionCount,
		RetryCount:       request.RuntimeRetryCount,
		ReassignCount:    request.RuntimeReassignCount,
		EscalateCount:    request.RuntimeEscalateCount,
		SupervisionIssue: request.RuntimeSupervisionIssue,
		ProgressSummary:  strings.TrimSpace(request.RuntimeProgressSummary),
		Status:           TaskQueued,
		QueuedAt:         queuedAt,
	}
}

func canonicalizeSupervisionTaskContract(request TaskRequest) (*SupervisionTaskContract, error) {
	if request.Contract == nil {
		return nil, nil
	}
	contract := normalizeSupervisionTaskContract(*request.Contract)
	if contract.TaskID != "" && contract.TaskID != request.ID {
		return nil, fmt.Errorf("supervision contract task %q does not match request %q", contract.TaskID, request.ID)
	}
	contract.TaskID = request.ID
	parentID := firstNonEmpty(request.ParentExecID, request.ParentRequest)
	if parentID == "" {
		parentID = request.ID
	}
	contract.AggregateID = parentID
	return &contract, nil
}

func cloneToolPermissions(source map[string]bool) map[string]bool {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]bool, len(source))
	for name, allowed := range source {
		cloned[name] = allowed
	}
	return cloned
}

func cloneTaskResult(result TaskResult) TaskResult {
	result.Metadata = cloneStringMap(result.Metadata)
	if result.SubagentResult != nil {
		if cloned, ok := proto.Clone(result.SubagentResult).(*agentv1.SubagentResult); ok {
			result.SubagentResult = cloned
		}
	}
	return result
}

func cloneModelParams(source []*agentv1.RequestedModel_ModelParameterValue) []*agentv1.RequestedModel_ModelParameterValue {
	if len(source) == 0 {
		return nil
	}
	cloned := make([]*agentv1.RequestedModel_ModelParameterValue, 0, len(source))
	for _, item := range source {
		if item == nil {
			continue
		}
		if copy, ok := proto.Clone(item).(*agentv1.RequestedModel_ModelParameterValue); ok {
			cloned = append(cloned, copy)
		}
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func cloneSubagentModelOverrides(source map[string]runtimecore.SubagentModelOverrideSelection) map[string]runtimecore.SubagentModelOverrideSelection {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]runtimecore.SubagentModelOverrideSelection, len(source))
	for key, value := range source {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		cloned[key] = value
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func cloneRequestedModels(source []*agentv1.RequestedModel) []*agentv1.RequestedModel {
	if len(source) == 0 {
		return nil
	}
	cloned := make([]*agentv1.RequestedModel, 0, len(source))
	for _, item := range source {
		if item == nil {
			continue
		}
		if copy, ok := proto.Clone(item).(*agentv1.RequestedModel); ok {
			cloned = append(cloned, copy)
		}
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func cloneModelDetails(source []*agentv1.ModelDetails) []*agentv1.ModelDetails {
	if len(source) == 0 {
		return nil
	}
	cloned := make([]*agentv1.ModelDetails, 0, len(source))
	for _, item := range source {
		if item == nil {
			continue
		}
		if copy, ok := proto.Clone(item).(*agentv1.ModelDetails); ok {
			cloned = append(cloned, copy)
		}
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		cloned[key] = strings.TrimSpace(value)
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}
