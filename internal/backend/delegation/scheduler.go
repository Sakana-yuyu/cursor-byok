// Package delegation 提供与 Cursor 客户端无关的非阻塞子代理调度能力。
package delegation

import (
	"context"
	"fmt"
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
	ModelParams                  []*agentv1.RequestedModel_ModelParameterValue
	Timeout                      time.Duration
}

// TaskResult 是适配器返回给主代理的统一结果。
type TaskResult struct {
	Output         string
	Error          error
	ToolCallCount  int
	SubagentResult *agentv1.SubagentResult
	Metadata       map[string]string
}

// TaskSnapshot 是 UI 和主代理读取的稳定状态快照。
type TaskSnapshot struct {
	ID                string
	Request           TaskRequest
	Contract          *SupervisionTaskContract
	Checkpoint        *WorkerCheckpoint
	SupervisionStatus SupervisionStatus
	Counters          SupervisionCounters
	Status            TaskStatus
	Output            string
	Error             string
	ToolCallCount     int
	EventID           string
	Sequence          uint64
	EventType         string
	ParentRequestID   string
	ParentExecID      string
	GroupID           string
	QueuedAt          time.Time
	StartedAt         time.Time
	FinishedAt        time.Time
	UpdatedAt         time.Time
}

type Executor func(context.Context, TaskRequest) TaskResult

type Scheduler struct {
	maxConcurrency int
	slots          chan struct{}
	executor       Executor
	ctx            context.Context
	cancel         context.CancelFunc
	retentionLimit int
	retentionAge   time.Duration

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
	snapshot   TaskSnapshot
	result     TaskResult
	contract   *SupervisionTaskContract
	checkpoint *WorkerCheckpoint
	counters   SupervisionCounters
	ctx        context.Context
	cancel     context.CancelFunc
	runnerDone bool
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
	if request.Contract != nil {
		contract := normalizeSupervisionTaskContract(*request.Contract)
		request.Contract = &contract
	}
	now := time.Now().UTC()

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
	supervisionStatus := SupervisionStatus("")
	if request.Contract != nil {
		supervisionStatus = SupervisionStatusPlanned
	}
	state := &taskState{
		snapshot: TaskSnapshot{
			ID:                request.ID,
			Request:           request,
			Contract:          cloneSupervisionTaskContract(request.Contract),
			Status:            TaskQueued,
			SupervisionStatus: supervisionStatus,
			QueuedAt:          now,
		},
		contract: request.Contract,
		ctx:      taskCtx,
		cancel:   taskCancel,
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
	select {
	case s.slots <- struct{}{}:
	case <-state.ctx.Done():
		s.finishFromContext(state, state.ctx.Err())
		return
	}
	defer func() { <-s.slots }()

	s.mu.Lock()
	if isTerminalStatus(state.snapshot.Status) {
		s.mu.Unlock()
		return
	}
	state.snapshot.Status = TaskRunning
	if state.snapshot.Contract != nil {
		state.snapshot.SupervisionStatus = SupervisionStatusRunning
	}
	state.snapshot.StartedAt = time.Now().UTC()
	request := cloneTaskRequest(state.snapshot.Request)
	s.publishLocked(&state.snapshot)
	s.mu.Unlock()

	executionCtx := state.ctx
	cancelTimeout := func() {}
	if request.Timeout > 0 {
		executionCtx, cancelTimeout = context.WithTimeout(state.ctx, request.Timeout)
	}
	defer cancelTimeout()
	resultChannel := make(chan TaskResult, 1)
	executorStarted = true
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
		state.snapshot.Error = result.Error.Error()
	} else {
		state.snapshot.Status = TaskCompleted
	}
	if state.snapshot.Contract != nil {
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
	state.snapshot.Status = TaskCanceled
	if state.snapshot.Contract != nil {
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
	state.snapshot.Status = TaskCanceled
	if state.snapshot.Contract != nil {
		state.snapshot.SupervisionStatus = SupervisionStatusCanceled
	}
	state.snapshot.FinishedAt = time.Now().UTC()
	state.cancel()
	s.publishLocked(&state.snapshot)
	s.mu.Unlock()
	return true
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
	if checkpoint.TaskID == "" {
		checkpoint.TaskID = taskID
	}
	if checkpoint.TaskID != taskID {
		return false
	}
	s.mu.Lock()
	state, ok := s.tasks[taskID]
	if !ok || s.closed || isTerminalStatus(state.snapshot.Status) {
		s.mu.Unlock()
		return false
	}
	if state.snapshot.Contract != nil {
		contract := state.snapshot.Contract
		if contract.TaskID != "" && contract.TaskID != taskID {
			s.mu.Unlock()
			return false
		}
		if contract.AggregateID != "" {
			if checkpoint.AggregateID == "" {
				checkpoint.AggregateID = contract.AggregateID
			} else if checkpoint.AggregateID != contract.AggregateID {
				s.mu.Unlock()
				return false
			}
		}
		if checkpoint.Round <= 0 && contract.Round > 0 {
			checkpoint.Round = contract.Round
		}
	}
	state.checkpoint = cloneWorkerCheckpoint(&checkpoint)
	state.snapshot.Checkpoint = cloneWorkerCheckpoint(&checkpoint)
	state.counters.Checkpoints++
	if checkpoint.Round > state.counters.Rounds {
		state.counters.Rounds = checkpoint.Round
	}
	state.snapshot.Counters = cloneSupervisionCounters(state.counters)
	state.snapshot.SupervisionStatus = checkpoint.Phase
	s.publishLocked(&state.snapshot)
	s.mu.Unlock()
	return true
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

// EnsureRetentionLimit only raises the retained task ceiling. Active aggregates
// can therefore keep every worker snapshot until fan-in has consumed it.
func (s *Scheduler) EnsureRetentionLimit(limit int) {
	if s == nil || limit <= 0 {
		return
	}
	s.mu.Lock()
	if limit > s.retentionLimit {
		s.retentionLimit = limit
	}
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
	if state.snapshot.Contract != nil {
		state.snapshot.SupervisionStatus = supervisionStatusForTaskStatus(state.snapshot.Status)
	}
	s.publishLocked(&state.snapshot)
	s.mu.Unlock()
}

func (s *Scheduler) pruneTerminalTasksLocked() {
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
	snapshot.ParentRequestID = strings.TrimSpace(snapshot.Request.ParentRequest)
	snapshot.ParentExecID = strings.TrimSpace(snapshot.Request.ParentExecID)
	snapshot.GroupID = snapshot.ParentExecID
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
	snapshot.Request = cloneTaskRequest(snapshot.Request)
	snapshot.Contract = cloneSupervisionTaskContract(snapshot.Contract)
	snapshot.Checkpoint = cloneWorkerCheckpoint(snapshot.Checkpoint)
	snapshot.Counters = cloneSupervisionCounters(snapshot.Counters)
	return snapshot
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
