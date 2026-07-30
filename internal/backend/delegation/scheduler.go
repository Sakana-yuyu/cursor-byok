// Package delegation 提供与 Cursor 客户端无关的非阻塞子代理调度能力。
package delegation

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const DefaultMaxConcurrency = 4

type TaskStatus string

const (
	TaskQueued    TaskStatus = "queued"
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCanceled  TaskStatus = "canceled"
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

	mu       sync.RWMutex
	tasks    map[string]*taskState
	closed   bool
	events   chan TaskSnapshot
	sequence atomic.Uint64
}

type taskState struct {
	snapshot TaskSnapshot
	cancel   context.CancelFunc
}

type Config struct {
	MaxConcurrency int
	EventBuffer    int
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
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		maxConcurrency: maxConcurrency,
		slots:          make(chan struct{}, maxConcurrency),
		executor:       executor,
		ctx:            ctx,
		cancel:         cancel,
		tasks:          make(map[string]*taskState),
		events:         make(chan TaskSnapshot, eventBuffer),
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
	request.ID = request.ID
	if request.ID == "" {
		request.ID = fmt.Sprintf("delegated-%d", s.sequence.Add(1))
	}
	now := time.Now().UTC()
	state := &taskState{snapshot: TaskSnapshot{ID: request.ID, Request: request, Status: TaskQueued, QueuedAt: now}}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", fmt.Errorf("delegation scheduler is closed")
	}
	if _, exists := s.tasks[request.ID]; exists {
		s.mu.Unlock()
		return "", fmt.Errorf("delegated task %q already exists", request.ID)
	}
	s.tasks[request.ID] = state
	s.mu.Unlock()
	s.publish(state.snapshot)
	go s.run(state)
	return request.ID, nil
}

func (s *Scheduler) run(state *taskState) {
	ctx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	state.cancel = cancel
	queuedSnapshot := state.snapshot
	s.mu.Unlock()
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		cancel()
		return
	}
	defer func() { <-s.slots }()

	s.mu.Lock()
	if state.snapshot.Status == TaskCanceled {
		s.mu.Unlock()
		cancel()
		return
	}
	state.snapshot.Status = TaskRunning
	state.snapshot.StartedAt = time.Now().UTC()
	runningSnapshot := state.snapshot
	s.mu.Unlock()
	s.publish(queuedSnapshot)
	s.publish(runningSnapshot)

	result := s.executor(ctx, state.snapshot.Request)
	finished := time.Now().UTC()
	s.mu.Lock()
	if state.snapshot.Status == TaskCanceled {
		state.snapshot.FinishedAt = finished
		canceledSnapshot := state.snapshot
		s.mu.Unlock()
		cancel()
		s.publish(canceledSnapshot)
		return
	}
	state.snapshot.Output = result.Output
	state.snapshot.ToolCallCount = result.ToolCallCount
	state.snapshot.FinishedAt = finished
	if result.Error != nil {
		state.snapshot.Status = TaskFailed
		state.snapshot.Error = result.Error.Error()
	} else {
		state.snapshot.Status = TaskCompleted
	}
	snapshot := state.snapshot
	s.mu.Unlock()
	cancel()
	s.publish(snapshot)
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
	if state.snapshot.Status == TaskCompleted || state.snapshot.Status == TaskFailed || state.snapshot.Status == TaskCanceled {
		s.mu.Unlock()
		return nil
	}
	state.snapshot.Status = TaskCanceled
	state.snapshot.FinishedAt = time.Now().UTC()
	if state.cancel != nil {
		state.cancel()
	}
	snapshot := state.snapshot
	s.mu.Unlock()
	s.publish(snapshot)
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
	return state.snapshot, true
}

func (s *Scheduler) Snapshots() []TaskSnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]TaskSnapshot, 0, len(s.tasks))
	for _, state := range s.tasks {
		items = append(items, state.snapshot)
	}
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
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
}

func (s *Scheduler) publish(snapshot TaskSnapshot) {
	select {
	case s.events <- snapshot:
	default:
		// 状态快照仍可通过 Snapshots 获取，事件通道不能阻塞执行器。
	}
}
