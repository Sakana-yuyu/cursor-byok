// broker.go 负责 request 维度活动流的订阅、广播、取消和终态收口。
package forwarder

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

const subscriberSignalBufferSize = 1

// orphanSubscriberGracePeriod 是 RunSSE 订阅归零后、判定请求为孤儿前的宽限期。
// 网络波动（本地代理/线路抖动）时 Cursor 客户端会短暂断开并自动重连，期间
// subscriber 短暂归零；宽限期给客户端充足的重连窗口，避免波动几秒就误杀长任务。
const orphanSubscriberGracePeriod = 3 * time.Minute
const terminalStreamRetentionPeriod = 30 * time.Second

// errStreamNotActive 表示 Cancel/Get 等操作目标 request 已不在 broker 中
// （已被 actor 正常收口移除，或从未存在）。调用方可用 errors.Is 判定后静默忽略。
var errStreamNotActive = errors.New("stream is not active")

type StreamBroker struct {
	mu      sync.RWMutex
	streams map[string]*ActiveStream
	nextID  atomic.Uint64
	// failOverride 仅测试使用：非 nil 时 Fail 直接返回它，用于模拟
	// 「Get 之后、Fail 之前流被清理」导致 Fail 报错的场景。
	failOverride func(requestID string, terminalCode string, terminalMessage string) error
	// terminalRetention 覆盖默认 terminalStreamRetentionPeriod；零值 = 使用默认值。
	// 仅测试使用，用于加速终态流保留/清理定时器。
	terminalRetention time.Duration
	// cleanupIntercept 仅测试使用：非 nil 时 runScheduledTerminalCleanup 在校验完成后、
	// broker.mu 删除前调用它，用于确定性竞态回归测试。测试可通过 channel 阻断清理，
	// 注入并发的 Subscribe。
	cleanupIntercept func(requestID string)
}

// NewStreamBroker 创建活动流注册表。
func NewStreamBroker() *StreamBroker {
	return &StreamBroker{
		streams: make(map[string]*ActiveStream),
	}
}

// OpenStream 打开或复用指定 request 的活动流，并刷新其最新上下文。
func (broker *StreamBroker) OpenStream(requestID string, conversationID string, turnSeq int64, modelID string, modelName string, mode agentv1.AgentMode, latestUserText string) (*ActiveStream, error) {
	normalizedRequestID := strings.TrimSpace(requestID)
	if normalizedRequestID == "" {
		return nil, nil
	}
	normalizedMode, err := validateSupportedActiveMode(mode)
	if err != nil {
		return nil, err
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if existing, ok := broker.streams[normalizedRequestID]; ok {
		existing.mu.Lock()
		existing.ConversationID = strings.TrimSpace(conversationID)
		existing.TurnSeq = turnSeq
		existing.ModelID = strings.TrimSpace(modelID)
		existing.ModelName = strings.TrimSpace(modelName)
		existing.Mode = normalizedMode
		existing.LatestUserText = strings.TrimSpace(latestUserText)
		if existing.Status == "" {
			existing.Status = StreamStatusCreated
		}
		if existing.PendingExecs == nil {
			existing.PendingExecs = make(map[string]runtimecore.PendingExec)
		}
		if existing.PendingInteractions == nil {
			existing.PendingInteractions = make(map[string]runtimecore.PendingInteraction)
		}
		if existing.PartialToolCallIDs == nil {
			existing.PartialToolCallIDs = make(map[string]struct{})
		}
		if existing.PatchEditQueues == nil {
			existing.PatchEditQueues = make(map[string][]queuedPatchEditOperation)
		}
		if existing.BackgroundShells == nil {
			existing.BackgroundShells = make(map[string]*BackgroundShellState)
		}
		if existing.BackgroundShellsByMessageID == nil {
			existing.BackgroundShellsByMessageID = make(map[uint32]string)
		}
		if existing.BackgroundShellsByExecID == nil {
			existing.BackgroundShellsByExecID = make(map[string]string)
		}
		if existing.BackgroundShellActions == nil {
			existing.BackgroundShellActions = make(map[string]time.Time)
		}
		if existing.RecentCompletedInteractions == nil {
			existing.RecentCompletedInteractions = make(map[string]time.Time)
		}
		if existing.StreamTimers == nil {
			existing.StreamTimers = make(map[string]*time.Timer)
		}
		existing.UpdatedAt = time.Now().UTC()
		existing.mu.Unlock()
		return existing, nil
	}
	now := time.Now().UTC()
	stream := &ActiveStream{
		RequestID:                   normalizedRequestID,
		ConversationID:              strings.TrimSpace(conversationID),
		TurnSeq:                     turnSeq,
		ModelID:                     strings.TrimSpace(modelID),
		ModelName:                   strings.TrimSpace(modelName),
		Mode:                        normalizedMode,
		LatestUserText:              strings.TrimSpace(latestUserText),
		Status:                      StreamStatusCreated,
		Backlog:                     make([]StreamEvent, 0, 64),
		Subscribers:                 make(map[string]*StreamSubscriber),
		PendingExecs:                make(map[string]runtimecore.PendingExec),
		PendingInteractions:         make(map[string]runtimecore.PendingInteraction),
		PartialToolCallIDs:          make(map[string]struct{}),
		PatchEditQueues:             make(map[string][]queuedPatchEditOperation),
		MCPToolServers:              make(map[string]string),
		RecentCompletedExecs:        make(map[uint32]time.Time),
		RecentCompletedInteractions: make(map[string]time.Time),
		BackgroundShells:            make(map[string]*BackgroundShellState),
		BackgroundShellsByMessageID: make(map[uint32]string),
		BackgroundShellsByExecID:    make(map[string]string),
		BackgroundShellActions:      make(map[string]time.Time),
		TimerTokens:                 make(map[string]uint64),
		StreamTimers:                make(map[string]*time.Timer),
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}
	broker.streams[normalizedRequestID] = stream
	return stream, nil
}

// Get 返回指定 request 对应的活动流句柄。
func (broker *StreamBroker) Get(requestID string) (*ActiveStream, bool) {
	if broker == nil {
		return nil, false
	}
	broker.mu.RLock()
	defer broker.mu.RUnlock()
	stream, ok := broker.streams[strings.TrimSpace(requestID)]
	return stream, ok
}

// Subscribe 为指定 request 注册一个新订阅者，并返回用于唤醒 backlog 消费的信号通道。
//
// 订阅者注册过程中持有 broker.mu.RLock，保证终态清理（runScheduledTerminalCleanup）
// 无法在我们拿到流指针之后、注册订阅者之前删除该流，消除 TOCTOU 竞态。
func (broker *StreamBroker) Subscribe(requestID string) (string, <-chan struct{}, int, error) {
	normalizedRequestID := strings.TrimSpace(requestID)
	if normalizedRequestID == "" {
		return "", nil, 0, fmt.Errorf("request_id is required")
	}

	// Fast path: look up existing stream under broker.mu.RLock.
	// If the stream doesn't exist yet, we'll create a placeholder via OpenStream
	// (which takes broker.mu.Lock internally), then re-acquire RLock.
	broker.mu.RLock()
	stream, ok := broker.streams[normalizedRequestID]
	broker.mu.RUnlock()

	if !ok || stream == nil {
		// RunSSE 可能先于 BidiAppend 到达。此时先创建一个占位活动流，
		// 等待后续上行把真实 conversation/model/mode 信息补齐。
		var err error
		stream, err = broker.OpenStream(normalizedRequestID, "", 0, "", "", agentv1.AgentMode_AGENT_MODE_AGENT, "")
		if err != nil {
			return "", nil, 0, err
		}
	}

	subscriberID := fmt.Sprintf("sub-%d", broker.nextID.Add(1))
	subscriber := &StreamSubscriber{Signal: make(chan struct{}, subscriberSignalBufferSize)}

	// Hold broker.mu.RLock across subscriber registration.  This prevents
	// runScheduledTerminalCleanup from acquiring broker.mu.Lock (write) and
	// deleting the stream between our pointer lookup and registration.
	broker.mu.RLock()
	defer broker.mu.RUnlock()

	// Re-verify the stream pointer: between OpenStream returning and our RLock,
	// the stream could have been completed and cleaned up.  Fall back to the
	// current map entry if our pointer is stale.
	if current, ok := broker.streams[normalizedRequestID]; ok && current != nil {
		stream = current
	}

	stream.mu.Lock()
	broker.stopTerminalCleanupTimerLocked(stream)
	stream.Subscribers[subscriberID] = subscriber
	startCursor := stream.BacklogStartCursor
	if startCursor < 0 || startCursor > len(stream.Backlog) {
		startCursor = 0
	}
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()

	return subscriberID, subscriber.Signal, startCursor, nil
}

func (broker *StreamBroker) stopTerminalCleanupTimerLocked(stream *ActiveStream) {
	if stream == nil {
		return
	}
	stream.TerminalCleanupSeq.Add(1)
	if stream.TerminalCleanupTimer != nil {
		stream.TerminalCleanupTimer.Stop()
		stream.TerminalCleanupTimer = nil
	}
}

// Unsubscribe 移除并关闭指定订阅者，并返回移除后的剩余订阅者数量。
func (broker *StreamBroker) Unsubscribe(requestID string, subscriberID string) int {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return 0
	}
	remaining := 0
	stream.mu.Lock()
	if _, ok := stream.Subscribers[strings.TrimSpace(subscriberID)]; ok {
		delete(stream.Subscribers, strings.TrimSpace(subscriberID))
	}
	remaining = len(stream.Subscribers)
	status := stream.Status
	stream.mu.Unlock()
	// 终态流的最后一个订阅者离开时，启动保留定时器而非立即删除。
	// 定时器是终态流的唯一清理者——RemoveIfIdle 不会删除终态流。
	if remaining == 0 && (status == StreamStatusCompleted || status == StreamStatusCanceled || status == StreamStatusFailed) {
		broker.scheduleTerminalCleanup(requestID)
	}
	return remaining
}

func (broker *StreamBroker) OtherConversationRequestIDs(conversationID string, keepRequestID string) []string {
	normalizedConversationID := strings.TrimSpace(conversationID)
	normalizedKeepRequestID := strings.TrimSpace(keepRequestID)
	if normalizedConversationID == "" {
		return nil
	}
	type requestStream struct {
		requestID string
		stream    *ActiveStream
	}
	candidates := make([]requestStream, 0, 2)
	broker.mu.RLock()
	for requestID, stream := range broker.streams {
		if stream == nil || strings.TrimSpace(requestID) == normalizedKeepRequestID {
			continue
		}
		candidates = append(candidates, requestStream{
			requestID: requestID,
			stream:    stream,
		})
	}
	broker.mu.RUnlock()
	requestIDs := make([]string, 0, 2)
	for _, candidate := range candidates {
		stream := candidate.stream
		stream.mu.Lock()
		sameConversation := strings.TrimSpace(stream.ConversationID) == normalizedConversationID
		status := stream.Status
		phase := stream.Phase
		stream.mu.Unlock()
		terminalPhase := phase == TurnPhaseCanceled || phase == TurnPhaseCompleted || phase == TurnPhaseFailed
		if !sameConversation || isTerminalStreamStatus(status) || terminalPhase {
			continue
		}
		requestIDs = append(requestIDs, candidate.requestID)
	}
	return requestIDs
}

// ActiveRequestIDs 返回当前仍未进入终态的 request_id 列表，用于服务关闭前的主动收口。
func (broker *StreamBroker) ActiveRequestIDs() []string {
	if broker == nil {
		return nil
	}
	type requestStream struct {
		requestID string
		stream    *ActiveStream
	}
	candidates := make([]requestStream, 0)
	broker.mu.RLock()
	for requestID, stream := range broker.streams {
		if stream == nil || strings.TrimSpace(requestID) == "" {
			continue
		}
		candidates = append(candidates, requestStream{
			requestID: requestID,
			stream:    stream,
		})
	}
	broker.mu.RUnlock()
	requestIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		stream := candidate.stream
		stream.mu.Lock()
		status := stream.Status
		phase := stream.Phase
		stream.mu.Unlock()
		terminalPhase := phase == TurnPhaseCanceled || phase == TurnPhaseCompleted || phase == TurnPhaseFailed
		if isTerminalStreamStatus(status) || terminalPhase {
			continue
		}
		requestIDs = append(requestIDs, candidate.requestID)
	}
	return requestIDs
}

func (broker *StreamBroker) scheduleTerminalCleanup(requestID string) bool {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if len(stream.Subscribers) > 0 {
		broker.stopTerminalCleanupTimerLocked(stream)
		return false
	}
	if stream.Status != StreamStatusCompleted && stream.Status != StreamStatusCanceled && stream.Status != StreamStatusFailed {
		broker.stopTerminalCleanupTimerLocked(stream)
		return false
	}
	sequence := stream.TerminalCleanupSeq.Add(1)
	if stream.TerminalCleanupTimer != nil {
		stream.TerminalCleanupTimer.Stop()
	}
	retention := terminalStreamRetentionPeriod
	if broker.terminalRetention > 0 {
		retention = broker.terminalRetention
	}
	stream.TerminalCleanupTimer = time.AfterFunc(retention, func() {
		broker.runScheduledTerminalCleanup(requestID, sequence)
	})
	stream.UpdatedAt = time.Now().UTC()
	return true
}

func (broker *StreamBroker) runScheduledTerminalCleanup(requestID string, sequence uint64) {
	normalizedRequestID := strings.TrimSpace(requestID)
	// Hold broker.mu (write) across validation + deletion so a concurrent
	// Subscribe cannot obtain the stream pointer between our checks and the
	// map removal.
	broker.mu.Lock()
	stream, ok := broker.streams[normalizedRequestID]
	if !ok || stream == nil {
		broker.mu.Unlock()
		return
	}
	stream.mu.Lock()
	if stream.TerminalCleanupSeq.Load() != sequence {
		stream.mu.Unlock()
		broker.mu.Unlock()
		return
	}
	stream.TerminalCleanupTimer = nil
	if len(stream.Subscribers) > 0 {
		stream.mu.Unlock()
		broker.mu.Unlock()
		return
	}
	if stream.Status != StreamStatusCompleted && stream.Status != StreamStatusCanceled && stream.Status != StreamStatusFailed {
		stream.mu.Unlock()
		broker.mu.Unlock()
		return
	}
	stream.mu.Unlock()
	// 测试拦截点：在校验完成后、broker.mu 删除前阻断，允许测试注入并发的 Subscribe。
	if broker.cleanupIntercept != nil {
		broker.cleanupIntercept(requestID)
	}
	// 直接删除终态流（不再通过 RemoveIfIdle，后者已不再管理终态流）。
	delete(broker.streams, normalizedRequestID)
	broker.mu.Unlock()
}

// RemoveIfIdle 移除无订阅者的空壳占位流。
// 终态流由 terminalStreamRetentionPeriod 定时器专属管理，RemoveIfIdle 不会删除它们。
func (broker *StreamBroker) RemoveIfIdle(requestID string) bool {
	normalizedRequestID := strings.TrimSpace(requestID)
	if normalizedRequestID == "" {
		return false
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	stream, ok := broker.streams[normalizedRequestID]
	if !ok || stream == nil {
		return false
	}
	stream.mu.Lock()
	subscriberCount := len(stream.Subscribers)
	isActive := stream.ProviderActive
	hasBacklog := len(stream.Backlog) > 0
	hasConversation := strings.TrimSpace(stream.ConversationID) != ""
	status := stream.Status
	stream.mu.Unlock()
	if subscriberCount > 0 {
		return false
	}
	// 终态流由定时器独占管理；RemoveIfIdle 不删除它。
	if status == StreamStatusCompleted || status == StreamStatusCanceled || status == StreamStatusFailed {
		return false
	}
	// 非终态占位流：无 provider、无 backlog、无会话信息 → 可安全移除。
	if isActive || hasBacklog || hasConversation {
		return false
	}
	delete(broker.streams, normalizedRequestID)
	return true
}

// Publish 把一个事件写入 backlog，并唤醒当前所有订阅者读取 backlog。
func (broker *StreamBroker) Publish(requestID string, event StreamEvent) error {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return fmt.Errorf("request is not active: %s", strings.TrimSpace(requestID))
	}
	stream.mu.Lock()
	if !event.End && isTerminalStreamStatus(stream.Status) {
		stream.mu.Unlock()
		return nil
	}
	stream.Backlog = append(stream.Backlog, event)
	stream.UpdatedAt = time.Now().UTC()
	subscribers := make([]*StreamSubscriber, 0, len(stream.Subscribers))
	for _, subscriber := range stream.Subscribers {
		subscribers = append(subscribers, subscriber)
	}
	stream.mu.Unlock()

	for _, subscriber := range subscribers {
		if subscriber == nil {
			continue
		}
		select {
		case subscriber.Signal <- struct{}{}:
		default:
		}
	}
	return nil
}

// ReadFromCursor 返回从 cursor 开始尚未消费的 backlog 事件副本。
func (broker *StreamBroker) ReadFromCursor(requestID string, cursor int) ([]StreamEvent, error) {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return nil, fmt.Errorf("request is not active: %s", strings.TrimSpace(requestID))
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(stream.Backlog) {
		return nil, nil
	}
	return append([]StreamEvent(nil), stream.Backlog[cursor:]...), nil
}

// Complete 把活动流标记为成功完成，并发布一个成功 endstream 事件。
func (broker *StreamBroker) Complete(requestID string, terminalCode string, terminalMessage string) error {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return fmt.Errorf("request is not active: %s", strings.TrimSpace(requestID))
	}
	stream.mu.Lock()
	if stream.Status == StreamStatusCanceled || stream.Status == StreamStatusFailed || stream.Status == StreamStatusCompleted {
		stream.mu.Unlock()
		return nil
	}
	broker.stopTerminalCleanupTimerLocked(stream)
	stopAllStreamTimersLocked(stream)
	stream.Status = StreamStatusCompleted
	subscriberCount := len(stream.Subscribers)
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if err := broker.Publish(requestID, StreamEvent{
		End:                  true,
		TerminalErrorCode:    strings.TrimSpace(terminalCode),
		TerminalErrorMessage: strings.TrimSpace(terminalMessage),
	}); err != nil {
		return err
	}
	if subscriberCount == 0 {
		broker.scheduleTerminalCleanup(requestID)
	}
	return nil
}

// Fail 把活动流标记为失败，并发布一个失败 endstream 事件。
func (broker *StreamBroker) Fail(requestID string, terminalCode string, terminalMessage string) error {
	if broker.failOverride != nil {
		return broker.failOverride(requestID, terminalCode, terminalMessage)
	}
	return broker.FailWithDetails(requestID, TerminalFailure{
		Code:    terminalCode,
		Message: terminalMessage,
	})
}

// FailWithDetails 在可靠终态中附带稳定错误分类和恢复尝试摘要。
func (broker *StreamBroker) FailWithDetails(requestID string, failure TerminalFailure) error {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return fmt.Errorf("request is not active: %s", strings.TrimSpace(requestID))
	}
	stream.mu.Lock()
	if isTerminalStreamStatus(stream.Status) {
		stream.mu.Unlock()
		return nil
	}
	broker.stopTerminalCleanupTimerLocked(stream)
	stopAllStreamTimersLocked(stream)
	stream.Status = StreamStatusFailed
	subscriberCount := len(stream.Subscribers)
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if err := broker.Publish(requestID, StreamEvent{
		End:                       true,
		TerminalErrorCode:         strings.TrimSpace(failure.Code),
		TerminalErrorMessage:      strings.TrimSpace(failure.Message),
		TerminalTraceID:           strings.TrimSpace(failure.TraceID),
		TerminalAppErrorCode:      strings.TrimSpace(failure.AppErrorCode),
		TerminalDisposition:       strings.TrimSpace(failure.Disposition),
		TerminalRetryAttemptCount: max(0, failure.RetryAttemptCount),
	}); err != nil {
		return err
	}
	if subscriberCount == 0 {
		broker.scheduleTerminalCleanup(requestID)
	}
	return nil
}

// Cancel 主动取消活动流，并发布 canceled endstream。
func (broker *StreamBroker) Cancel(requestID string, terminalMessage string) error {
	stream, ok := broker.Get(requestID)
	if !ok || stream == nil {
		return fmt.Errorf("%w: %s", errStreamNotActive, strings.TrimSpace(requestID))
	}
	stream.mu.Lock()
	if isTerminalStreamStatus(stream.Status) {
		stream.mu.Unlock()
		return nil
	}
	broker.stopTerminalCleanupTimerLocked(stream)
	stopAllStreamTimersLocked(stream)
	if stream.ProviderCancel != nil {
		stream.ProviderCancel()
		stream.ProviderCancel = nil
	}
	stream.ProviderActive = false
	stream.Status = StreamStatusCanceled
	subscriberCount := len(stream.Subscribers)
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if err := broker.Publish(requestID, StreamEvent{
		End:                  true,
		TerminalErrorCode:    "canceled",
		TerminalErrorMessage: firstNonEmpty(strings.TrimSpace(terminalMessage), "[canceled] User aborted request"),
	}); err != nil {
		return err
	}
	if subscriberCount == 0 {
		broker.scheduleTerminalCleanup(requestID)
	}
	return nil
}

// firstNonEmpty 返回第一个非空白字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
