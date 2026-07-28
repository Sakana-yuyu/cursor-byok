// turn_stale.go 实现 turn-staleness 看门狗：当一轮回合停留在「等待外部（工具/交互结果）」
// 阶段且无任何进展超过阈值时，分两阶段自救，避免 Cursor 客户端永久显示「运行中」却无返回。
//
// 阶段一（重对齐 + 宽限）：重置 appendSequenceTracker 对该 request_id 的状态，让 Cursor
//
//	可能补发的真实工具结果能重新被接受；并设置一个较短的宽限期，给真实结果一次机会。
//
// 阶段二（强制收口 + 自动继续）：宽限后仍卡住时，对所有未完成的 pending exec 注入合成
//
//	工具结果（复用 recoverExecWithoutTerminal），随后自动驱动 provider 继续——模型收到
//	tool 超时错误后会自行重试或换方案，从而让任务最终完成而非永久中断。
package forwarder

import (
	"context"
	"log"
	"strings"
	"time"

	runtimecore "cursor/internal/backend/agent/core"
)

// turnStaleMinDelay 是看门狗允许的最小触发间隔，作为防御性下限（与 config 层 clamp 互不依赖）。
const turnStaleMinDelay = 30 * time.Second

// defaultTurnStaleTimeoutSeconds 与 config.DefaultTurnStaleTimeoutSeconds 保持一致：
// 当 resolver 不可用（如测试桩）时的兜底阈值，单位秒。
const defaultTurnStaleTimeoutSeconds = 120

// turnStaleGraceSeconds 与 config.TurnStaleGraceSeconds 保持一致：
// 阶段一（重对齐 append 序列）后给真实工具结果的宽限期，单位秒。
const turnStaleGraceSeconds = 60

// resolveTurnStaleDelay 解析当前应使用的 turn-staleness 触发延迟。
// 阶段一用配置的完整阈值；阶段二（已进入宽限期）用宽限期长度。
func (service *Service) resolveTurnStaleDelay(stream *ActiveStream) time.Duration {
	if service == nil || stream == nil || service.resolver == nil {
		return defaultTurnStaleDelay()
	}
	stream.mu.Lock()
	inGrace := !stream.TurnStaleGraceStartedAt.IsZero()
	stream.mu.Unlock()
	if inGrace {
		return time.Duration(turnStaleGraceSeconds) * time.Second
	}
	delay := service.resolver.TurnStaleTimeout(context.Background())
	if delay < turnStaleMinDelay {
		delay = turnStaleMinDelay
	}
	return delay
}

// defaultTurnStaleDelay 返回阶段一的默认触发延迟（不依赖运行时 resolver，用于 resolver 缺失等兜底场景）。
func defaultTurnStaleDelay() time.Duration {
	return time.Duration(defaultTurnStaleTimeoutSeconds) * time.Second
}

// handleTurnStaleTimeout 是 turn-staleness 看门狗的到期处理入口。
// 它会二次校验真实状态，避免在已经恢复/终态/正常等待用户输入时误触发。
func (service *Service) handleTurnStaleTimeout(stream *ActiveStream, payload *streamTimerEvent) error {
	if stream == nil || payload == nil {
		return nil
	}
	stream.mu.Lock()
	requestID := stream.RequestID
	conversationID := stream.ConversationID
	phase := stream.Phase
	status := stream.Status
	providerActive := stream.ProviderActive
	inGrace := !stream.TurnStaleGraceStartedAt.IsZero()
	awaitingUser := false
	for _, pending := range stream.PendingInteractions {
		if !shouldAutoResumeAfterInteraction(pending) {
			awaitingUser = true
			break
		}
	}
	pendingExecCount := len(stream.PendingExecs)
	stream.mu.Unlock()

	// 已离开等待态 / provider 仍在跑 / 已终态 / 正在等待真实用户输入 → 不干预。
	if isTerminalStreamStatus(status) || providerActive || awaitingUser || phase != TurnPhaseWaitingExternal {
		// 重新挂一个看门狗，确保「仍处于等待态但本次条件不满足」时不会丢失兜底。
		if phase == TurnPhaseWaitingExternal && !isTerminalStreamStatus(status) {
			service.scheduleTurnStaleWatchdog(stream)
		}
		return nil
	}

	if !inGrace {
		// 阶段一：进入宽限期，给真实工具结果一次机会。
		// 注意：不再调用 appendSeq.Reset()。旧实现把 next 强制重置为 1，
		// 但 Cursor 客户端不会重置自己的 seqno，导致 seqno>1 的工具结果在
		// acquire() 里永远阻塞等待 seqno=1，反而让工具执行彻底卡死形成死循环。
		// acquire() 内置了 appendSeq==1 && state.next>1 的分支，已能处理
		// 真实的客户端 seqno 重置场景；这里不需要额外干预。
		stream.mu.Lock()
		stream.TurnStaleGraceStartedAt = time.Now().UTC()
		stream.mu.Unlock()
		service.debug.LogRuntime(context.Background(), requestID, conversationID, "turn_stale_realign", map[string]any{
			"grace_seconds": turnStaleGraceSeconds,
			"pending_execs": pendingExecCount,
		})
		log.Printf("forwarder turn stale realign request_id=%s pending_execs=%d grace_seconds=%d",
			strings.TrimSpace(requestID), pendingExecCount, turnStaleGraceSeconds)
		// 用宽限期长度重新调度看门狗。
		service.scheduleTurnStaleWatchdog(stream)
		return nil
	}

	// 阶段二：强制收口所有未完成的 pending exec，再自动继续 provider。
	return service.forceCompleteTurnStale(stream, requestID, conversationID, pendingExecCount)
}

// forceCompleteTurnStale 对所有未完成的 pending exec 注入合成工具结果，
// 然后自动驱动 provider 继续。若无任何 pending exec（纯序列失配死锁），直接强制 resume。
func (service *Service) forceCompleteTurnStale(stream *ActiveStream, requestID string, conversationID string, pendingExecCount int) error {
	if pendingExecCount > 0 {
		pendingExecs := snapshotPendingExecs(stream)
		for _, pending := range pendingExecs {
			// recoverExecWithoutTerminal 会写合成 tool_result、清理 pending，并调用 reconcileStream。
			// 在仍有其他 pending 时 reconcile 会因 pendingBridgeCount>0 提前返回；
			// 最后一个被收口后才会真正触发 provider resume。
			if err := service.recoverExecWithoutTerminal(stream, pending, "turn_stale_force_complete"); err != nil {
				return err
			}
		}
		// recoverExecWithoutTerminal 末尾的 reconcileStream 已负责推进；这里再做一次兜底 reconcile。
		if err := service.reconcileStream(stream); err != nil {
			return err
		}
	} else {
		// 没有 pending exec 却仍卡在等待态：通常是序列失配导致工具结果全被丢弃的死锁。
		// 清掉可能的 pending completion，直接强制重新驱动 provider。
		clearPendingProviderCompletion(stream)
		if err := service.requestProviderAction(stream, providerActionResume); err != nil {
			return err
		}
	}

	// 清掉宽限标记，避免后续重复进入阶段二。
	stream.mu.Lock()
	stream.TurnStaleGraceStartedAt = time.Time{}
	stream.mu.Unlock()

	service.debug.LogRuntime(context.Background(), requestID, conversationID, "turn_stale_force_complete", map[string]any{
		"recovered_execs": pendingExecCount,
	})
	log.Printf(
		"forwarder turn stale force complete request_id=%s recovered_execs=%d",
		strings.TrimSpace(requestID),
		pendingExecCount,
	)
	return nil
}

// snapshotPendingExecs 在锁内拷贝当前所有 pending exec（用于阶段二遍历收口，避免边遍历边删）。
func snapshotPendingExecs(stream *ActiveStream) []runtimecore.PendingExec {
	if stream == nil {
		return nil
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	pending := make([]runtimecore.PendingExec, 0, len(stream.PendingExecs))
	for _, item := range stream.PendingExecs {
		pending = append(pending, item)
	}
	return pending
}
