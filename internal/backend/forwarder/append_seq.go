package forwarder

import (
	"context"
	"strings"
	"sync"
	"time"

	"cursor/internal/logger"
)

const appendSequenceRetention = 10 * time.Minute

type appendSequenceTracker struct {
	mu     sync.Mutex
	states map[string]*appendSequenceState
}

type appendSequenceState struct {
	mu         sync.Mutex
	next       int64
	processing bool
	ready      chan struct{}
	updatedAt  time.Time
}

type appendSequenceTicket struct {
	state *appendSequenceState
	seq   int64
}

func newAppendSequenceTracker() *appendSequenceTracker {
	return &appendSequenceTracker{
		states: make(map[string]*appendSequenceState),
	}
}

func (tracker *appendSequenceTracker) Acquire(ctx context.Context, requestID string, appendSeq int64) (appendSequenceTicket, bool, error) {
	if tracker == nil || strings.TrimSpace(requestID) == "" || appendSeq <= 0 {
		return appendSequenceTicket{}, false, nil
	}
	requestID = strings.TrimSpace(requestID)
	state := tracker.state(requestID)
	stale, err := state.acquire(ctx, requestID, appendSeq)
	if err != nil || stale {
		return appendSequenceTicket{}, stale, err
	}
	return appendSequenceTicket{
		state: state,
		seq:   appendSeq,
	}, false, nil
}

func (tracker *appendSequenceTracker) state(requestID string) *appendSequenceState {
	now := time.Now().UTC()
	cutoff := now.Add(-appendSequenceRetention)

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	for key, state := range tracker.states {
		if state == nil || state.expired(cutoff) {
			delete(tracker.states, key)
		}
	}
	if state, ok := tracker.states[requestID]; ok && state != nil {
		state.touch(now)
		return state
	}
	state := &appendSequenceState{
		next:      1,
		ready:     make(chan struct{}),
		updatedAt: now,
	}
	tracker.states[requestID] = state
	return state
}

// Reset 把指定 request_id 的 append 序列状态强制重置为初始（next=1, processing=false）。
// 当 turn-staleness 看门狗检测到「Cursor 工具结果被持续误判为 stale」造成回合卡死时调用，
// 让 Cursor 后续（可能补发的）真实工具结果能重新被 Acquire 接受。
// 旧的 ready channel 会被关闭，唤醒任何在 acquire 中阻塞等待的协程，使其基于重置后的状态重试。
func (tracker *appendSequenceTracker) Reset(requestID string) {
	if tracker == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	requestID = strings.TrimSpace(requestID)
	now := time.Now().UTC()
	tracker.mu.Lock()
	old := tracker.states[requestID]
	tracker.states[requestID] = &appendSequenceState{
		next:      1,
		ready:     make(chan struct{}),
		updatedAt: now,
	}
	tracker.mu.Unlock()
	if old != nil {
		old.mu.Lock()
		// 唤醒在 acquire 中等待旧 ready 的协程，让它们基于重置后的状态重新判断。
		old.processing = false
		select {
		case <-old.ready:
			// already closed
		default:
			close(old.ready)
		}
		old.mu.Unlock()
	}
	logger.Infof("forwarder reset append sequence on demand request_id=%s", requestID)
}

func (state *appendSequenceState) acquire(ctx context.Context, requestID string, appendSeq int64) (bool, error) {
	for {
		state.mu.Lock()
		now := time.Now().UTC()
		if state.next <= 0 {
			state.next = 1
		}
		if state.ready == nil {
			state.ready = make(chan struct{})
		}
		state.updatedAt = now

		// Cursor may reuse the same request_id for a later turn and restart
		// append_seqno from 1. Accept that as a sequence restart when idle so
		// tool results are not discarded as stale forever.
		if appendSeq == 1 && state.next > 1 {
			if state.processing {
				ready := state.ready
				state.mu.Unlock()
				select {
				case <-ctx.Done():
					return false, ctx.Err()
				case <-ready:
				}
				continue
			}
			prevNext := state.next
			state.next = 1
			state.processing = true
			state.mu.Unlock()
			logger.Infof("forwarder reset append sequence request_id=%s previous_next=%d append_seqno=1", requestID, prevNext)
			return false, nil
		}

		switch {
		case appendSeq < state.next:
			state.mu.Unlock()
			return true, nil
		case appendSeq == state.next && !state.processing:
			state.processing = true
			state.mu.Unlock()
			return false, nil
		default:
			ready := state.ready
			state.mu.Unlock()
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-ready:
			}
		}
	}
}

func (state *appendSequenceState) Release(seq int64) {
	if state == nil || seq <= 0 {
		return
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	if state.processing && state.next == seq {
		state.processing = false
		state.next++
		close(state.ready)
		state.ready = make(chan struct{})
	}
	state.updatedAt = time.Now().UTC()
}

func (state *appendSequenceState) expired(cutoff time.Time) bool {
	if state == nil {
		return true
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.processing {
		return false
	}
	return !state.updatedAt.IsZero() && state.updatedAt.Before(cutoff)
}

func (state *appendSequenceState) touch(now time.Time) {
	if state == nil {
		return
	}
	state.mu.Lock()
	state.updatedAt = now
	state.mu.Unlock()
}

func (ticket appendSequenceTicket) Release() {
	if ticket.state == nil || ticket.seq <= 0 {
		return
	}
	ticket.state.Release(ticket.seq)
}
