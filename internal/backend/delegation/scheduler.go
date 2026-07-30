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
)

const (
	DefaultMaxConcurrency = 4
	DefaultRetentionLimit = 256
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
	ID             string
	ParentRequest  string
	Prompt         string
	ModelID        string
	ModelGroupID   string
	ExecutionMode  string
	WorkspaceHint  string
	ToolPermission map[string]bool
	Timeout        time.Duration
}

// TaskResult 是适配器返回给主代理的统一结果。
type TaskResult struct {
	Output        string
	Error         error
	ToolCallCount int
}

// TaskSnapshot 是 UI 和主代理读取的稳定状态快照。
type TaskSnapshot struct {
	ID            string
	Request       TaskRequest
	Status        TaskStatus
	Output        string
	Error         string
	ToolCallCount int
	QueuedAt      time.Time
	StartedAt     time.Time
	FinishedAt    time.Time
}

type Executor func(context.Context, TaskRequest) TaskResult

type Scheduler struct {
	maxConcurrency int
	slots          chan struct{}
	executor       Executor
	ctx            context.Context
	cancel         context.CancelFunc
	retentionLimit int

	mu               sync.RWMutex
	tasks            map[string]*taskState
	activeExecutions map[string]struct{}
	closed           bool
	events           chan TaskSnapshot
	eventsClosed     bool
	sequence         atomic.Uint64
	closeOnce        sync.Once
}

type taskState struct {
	snapshot   TaskSnapshot
	ctx        context.Context
	cancel     context.CancelFunc
	runnerDone bool
}

type Config struct {
	MaxConcurrency int
	EventBuffer    int
	RetentionLimit int
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
		tasks:            make(map[string]*taskState),
		activeExecutions: make(map[string]struct{}),
		events:           make(chan TaskSnapshot, eventBuffer),
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
		request.ID = fmt.Sprintf("delegated-%d", s.sequence.Add(1))
	}
	request = cloneTaskRequest(request)
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
	state := &taskState{
		snapshot: TaskSnapshot{ID: request.ID, Request: request, Status: TaskQueued, QueuedAt: now},
		ctx:      taskCtx,
		cancel:   taskCancel,
	}
	s.tasks[request.ID] = state
	s.activeExecutions[request.ID] = struct{}{}
	s.pruneTerminalTasksLocked()
	s.publishLocked(state.snapshot)
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
	state.snapshot.StartedAt = time.Now().UTC()
	runningSnapshot := cloneTaskSnapshot(state.snapshot)
	request := cloneTaskRequest(state.snapshot.Request)
	s.publishLocked(runningSnapshot)
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
	snapshot := cloneTaskSnapshot(state.snapshot)
	s.publishLocked(snapshot)
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
	state.snapshot.FinishedAt = time.Now().UTC()
	state.cancel()
	snapshot := cloneTaskSnapshot(state.snapshot)
	s.publishLocked(snapshot)
	s.mu.Unlock()
	return nil
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
			s.publishLocked(state.snapshot)
		}
		s.pruneTerminalTasksLocked()
		s.purgeBufferedEventsLocked()
		s.eventsClosed = true
		close(s.events)
		s.mu.Unlock()
		s.cancel()
	})
}

func (s *Scheduler) publishLocked(snapshot TaskSnapshot) {
	if s.eventsClosed {
		return
	}
	snapshot = cloneTaskSnapshot(snapshot)
	select {
	case s.events <- snapshot:
	default:
		if !isTerminalStatus(snapshot.Status) {
			return
		}
		s.purgeBufferedEventsLocked()
		if len(s.events) >= cap(s.events) {
			s.evictOldestNonTerminalEventLocked()
		}
		select {
		case s.events <- snapshot:
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
	snapshot := cloneTaskSnapshot(state.snapshot)
	s.publishLocked(snapshot)
	s.mu.Unlock()
}

func (s *Scheduler) pruneTerminalTasksLocked() {
	for len(s.tasks) > s.retentionLimit {
		var oldestID string
		var oldestTime time.Time
		for taskID, state := range s.tasks {
			if !state.runnerDone || !isTerminalStatus(state.snapshot.Status) {
				continue
			}
			if oldestID == "" || state.snapshot.FinishedAt.Before(oldestTime) {
				oldestID = taskID
				oldestTime = state.snapshot.FinishedAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(s.tasks, oldestID)
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

func cloneTaskRequest(request TaskRequest) TaskRequest {
	request.ToolPermission = cloneToolPermissions(request.ToolPermission)
	return request
}

func cloneTaskSnapshot(snapshot TaskSnapshot) TaskSnapshot {
	snapshot.Request = cloneTaskRequest(snapshot.Request)
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
