// cancel_interaction.go 处理「取消时仍在等待用户输入的 interaction」。
//
// 背景：一个 pending interaction（AskQuestion / CreatePlan / SwitchMode 等）此前只有
// 一条收口路径——15 分钟看门狗 recoverStaleInteractionWithoutResponse。用户主动取消走
// handleCancelIntent，那里对 pending exec 逐个 abort 并清理，却只是把 PendingInteractions
// 整个 map 清空：看门狗到期后查不到该 interaction 会直接返回，于是没有任何机制会为这条
// tool_call 追加 tool_result，Cursor 前台的问答卡片也永远停在等待中。
//
// 这里补上与 exec abort 循环对称的 interaction 收口，形状参照 followup_cancel.go 的
// closeBackgroundedParentToolCall。
package forwarder

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/logger"
)

// closeCanceledPendingInteractions 给取消时仍在等待用户输入的 interaction 写终态：
// 追加 tool_result、停掉看门狗、并让客户端的交互卡片收口。
//
// 必须在 control{status:...} 条目之后、清空 PendingInteractions 之前调用，
// 这样 context.json 里「工具结果 → 回合终态」的时序才自洽。
//
// 锁序：全程只按「先取 stream.mu、再取会话文件锁」的既有方向，快照取完即释放
// stream.mu，绝不在持有会话锁时回头取 stream.mu。
func (service *Service) closeCanceledPendingInteractions(stream *ActiveStream, reason string) {
	if service == nil || stream == nil {
		return
	}
	stream.mu.Lock()
	pendingItems := make([]runtimecore.PendingInteraction, 0, len(stream.PendingInteractions))
	for _, pending := range stream.PendingInteractions {
		pendingItems = append(pendingItems, pending)
	}
	requestID := strings.TrimSpace(stream.RequestID)
	conversationID := strings.TrimSpace(stream.ConversationID)
	stream.mu.Unlock()
	if len(pendingItems) == 0 {
		return
	}
	// 多个 pending interaction 时保持稳定的写入顺序，避免 map 遍历顺序让
	// context.json 的条目次序在同一场景下抖动。
	sort.Slice(pendingItems, func(left, right int) bool {
		return strings.TrimSpace(pendingItems[left].InteractionID) < strings.TrimSpace(pendingItems[right].InteractionID)
	})
	for _, pending := range pendingItems {
		// 先摘除并停表：即使随后的持久化失败，也不能留下会二次收口的看门狗。
		markInteractionCompleted(stream, pending)
		clearStreamTimer(stream, providerTimerKey(streamTimerInteractionTimeout, pending.InteractionID))
		toolCallID := strings.TrimSpace(pending.ToolCallID)
		if toolCallID == "" {
			continue
		}
		toolName := strings.TrimSpace(deriveToolNameFromPendingInteraction(pending))
		if toolName == "" {
			logger.Infof("forwarder cancel interaction closure skipped unknown kind request_id=%s interaction_id=%s kind=%q",
				requestID, strings.TrimSpace(pending.InteractionID), strings.TrimSpace(pending.InteractionKind))
			continue
		}
		// 这条 tool_result 刻意不带 replay_policy：那是 control{status:"canceled"}
		// 专用的清洗语义，挂在工具结果上会把整个回合从 replay 里剔除。
		if err := service.appendToolResult(
			stream,
			toolCallID,
			toolName,
			pending.ArgsJSON,
			buildCanceledInteractionToolResultPayload(pending, reason),
			pending.ReasoningContent,
			nil,
		); err != nil {
			logger.Errorf("forwarder canceled interaction tool result append failed request_id=%s conversation_id=%s interaction_id=%s tool_call_id=%s err=%v",
				requestID, conversationID, strings.TrimSpace(pending.InteractionID), toolCallID, err)
		}
		// 让 Cursor 前台的交互卡片收口，不再等待一个永远不会到来的回答。
		if err := service.publishToolCallCompleted(requestID, toolCallID, pending.ModelCallID, nil); err != nil {
			logger.Infof("forwarder canceled interaction tool call completed publish failed request_id=%s tool_call_id=%s err=%v",
				requestID, toolCallID, err)
		}
		logger.Infof("forwarder cancel closed pending interaction request_id=%s conversation_id=%s interaction_id=%s kind=%s tool_call_id=%s reason=%q",
			requestID, conversationID, strings.TrimSpace(pending.InteractionID), strings.TrimSpace(pending.InteractionKind), toolCallID, strings.TrimSpace(reason))
	}
}

// buildCanceledInteractionToolResultPayload 构造取消收口的 tool_result 文本。
// 形状与 buildSyntheticInteractionTimeoutPayload 保持一致，只是 status 与文案不同。
func buildCanceledInteractionToolResultPayload(pending runtimecore.PendingInteraction, reason string) string {
	toolName := deriveToolNameFromPendingInteraction(pending)
	detail := fmt.Sprintf("[interaction canceled] %s 未收到用户回答：%s（interaction_id=%s）。",
		toolName, canceledInteractionDetail(reason), strings.TrimSpace(pending.InteractionID))
	summary := map[string]any{
		"status": "canceled",
		"detail": detail,
		"reason": strings.TrimSpace(reason),
		"kind":   strings.TrimSpace(pending.InteractionKind),
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return detail
	}
	return string(encoded)
}

func canceledInteractionDetail(reason string) string {
	if isFollowUpCancelReason(reason) {
		return "用户没有回答，而是直接发送了新消息取代当前回合，请以新消息为准，不要原样重问"
	}
	return "用户在回答之前停止了本回合，不要在没有新指示的情况下原样重问"
}
