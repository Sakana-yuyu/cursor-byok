// followup_cancel.go 处理「新消息顶掉当前 turn」这一类取消：客户端提交新消息时
// 会对父 composer 发 cancelAction，但语义是「父回合被替换」，不是「停止全部工作」。
// 上游对仍在前台运行的子代理先发 backgroundSubagentAction 转后台再取消父回合；
// 本文件实现等价的后台化，并负责后台化任务迟到结果的归属。
package forwarder

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
	"cursor/internal/logger"
)

const (
	// promptContextSourceBackgroundedDelegationResult 是后台化委派结果回放到模型可见
	// 历史时使用的 prompt context 来源标识。
	promptContextSourceBackgroundedDelegationResult = "backgrounded_delegation_result"
	// metadataTypeBackgroundedDelegationResult 是后台化委派结果的持久化落点。
	// metadata 不进入 prompt replay，可安全落在任意位置（含另一回合的工具调用之间），
	// 真正的模型可见回放推迟到下一回合开头由 pendingBackgroundedDelegationEntries 生成。
	metadataTypeBackgroundedDelegationResult = "backgrounded_delegation_result"
	// execStreamStateBackgrounded 表示该执行桥已转入后台，取消不得再向客户端发 abort。
	execStreamStateBackgrounded = "backgrounded"
)

// followUpBackgroundSummary 记录一次 follow-up 取消后台化了多少委派执行，供可观测性使用。
type followUpBackgroundSummary struct {
	NativeSubagents int
	Aggregates      int
}

// Total 返回本次后台化的执行总数。
func (summary followUpBackgroundSummary) Total() int {
	return summary.NativeSubagents + summary.Aggregates
}

// backgroundedDelegationExec 记录一次被后台化的委派执行。父流随后进入终态，
// 子代理最终结果不能再写回死流，必须凭该记录按会话归属重新落地。
type backgroundedDelegationExec struct {
	ExecID          string
	ExecKind        string
	StreamState     string
	ToolCallID      string
	ModelCallID     string
	Description     string
	ConversationID  string
	ParentRequestID string
	TurnSeq         int64
	BackgroundedAt  time.Time
}

// isFollowUpCancelReason 判定取消原因是否属于「新消息顶掉当前 turn」。
// 只有这一类取消才做后台化：用户按 Stop（user_stopped_generation）等原因
// 必须保持原有语义，立刻取消所有在跑的子代理。
func isFollowUpCancelReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "new_message_submitted", "replaced_by_new_turn":
		return true
	default:
		return false
	}
}

// isBackgroundedPendingExec 判定执行桥是否已被显式转入后台。
func isBackgroundedPendingExec(pending runtimecore.PendingExec) bool {
	return strings.TrimSpace(pending.StreamState) == execStreamStateBackgrounded
}

func isDelegationExecKind(execKind string) bool {
	switch strings.TrimSpace(execKind) {
	case "subagent", "delegation_aggregate":
		return true
	default:
		return false
	}
}

// backgroundFollowUpDelegations 把父流上仍在运行的委派执行转入后台，而不是取消它们。
// 必须在 handleCancelIntent 拍下 pendingExecs 快照之前调用，否则快照里仍有这些 exec，
// 后续 abort 循环仍会打到它们。
func (service *Service) backgroundFollowUpDelegations(ctx context.Context, stream *ActiveStream) followUpBackgroundSummary {
	summary := followUpBackgroundSummary{}
	if service == nil || stream == nil {
		return summary
	}
	stream.mu.Lock()
	requestID := strings.TrimSpace(stream.RequestID)
	conversationID := strings.TrimSpace(stream.ConversationID)
	turnSeq := stream.TurnSeq
	candidates := make([]runtimecore.PendingExec, 0, len(stream.PendingExecs))
	for _, pending := range stream.PendingExecs {
		if isDelegationExecKind(pending.ExecKind) {
			candidates = append(candidates, pending)
		}
	}
	stream.mu.Unlock()
	if len(candidates) == 0 {
		return summary
	}
	sort.Slice(candidates, func(left, right int) bool {
		return strings.TrimSpace(candidates[left].ExecID) < strings.TrimSpace(candidates[right].ExecID)
	})
	now := time.Now().UTC()
	for _, pending := range candidates {
		execKind := strings.TrimSpace(pending.ExecKind)
		record := backgroundedDelegationExec{
			ExecID:          strings.TrimSpace(pending.ExecID),
			ExecKind:        execKind,
			StreamState:     execStreamStateBackgrounded,
			ToolCallID:      strings.TrimSpace(pending.ToolCallID),
			ModelCallID:     strings.TrimSpace(pending.ModelCallID),
			Description:     delegationDescriptionFromArgs(pending.ArgsJSON),
			ConversationID:  conversationID,
			ParentRequestID: requestID,
			TurnSeq:         turnSeq,
			BackgroundedAt:  now,
		}
		service.rememberBackgroundedDelegation(record)
		// 先摘除 pending exec：handleCancelIntent 的 abort 快照必须看不到它。
		markExecCompleted(stream, pending)
		service.closeBackgroundedParentToolCall(stream, pending)
		switch execKind {
		case "subagent":
			summary.NativeSubagents++
		case "delegation_aggregate":
			summary.Aggregates++
		}
		logger.Infof("forwarder follow-up cancel backgrounded delegation request_id=%s conversation_id=%s exec_id=%s exec_kind=%s tool_call_id=%s",
			requestID, conversationID, record.ExecID, record.ExecKind, record.ToolCallID)
	}
	if service.debug != nil {
		service.debug.LogRuntime(ctx, requestID, conversationID, "follow_up_cancel_backgrounded", map[string]any{
			"follow_up_cancel":              true,
			"backgrounded_native_subagents": summary.NativeSubagents,
			"backgrounded_aggregates":       summary.Aggregates,
			"backgrounded_total":            summary.Total(),
			"turn_seq":                      turnSeq,
		})
	}
	return summary
}

// closeBackgroundedParentToolCall 为后台化的委派写入 backgrounded 形态的 tool_result。
// 这是硬性前提：父历史若留下悬空的 Task tool_call，下一回合重放会直接触发 provider 400。
func (service *Service) closeBackgroundedParentToolCall(stream *ActiveStream, pending runtimecore.PendingExec) {
	if service == nil || stream == nil || strings.TrimSpace(pending.ToolCallID) == "" {
		return
	}
	if err := service.appendToolResult(
		stream,
		pending.ToolCallID,
		"Task",
		pending.ArgsJSON,
		backgroundedTaskToolResultPayload(pending),
		pending.ReasoningContent,
		nil,
	); err != nil {
		logger.Errorf("forwarder backgrounded tool result append failed request_id=%s exec_id=%s tool_call_id=%s err=%v",
			strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ExecID), strings.TrimSpace(pending.ToolCallID), err)
		return
	}
	// 让 Cursor 前台的 Task 卡片收口，不再无限旋转等待一个已经转后台的子代理。
	if err := service.publishToolCallCompleted(stream.RequestID, pending.ToolCallID, pending.ModelCallID, nil); err != nil {
		logger.Infof("forwarder backgrounded tool call completed publish failed request_id=%s tool_call_id=%s err=%v",
			strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ToolCallID), err)
	}
}

func backgroundedTaskToolResultPayload(pending runtimecore.PendingExec) string {
	payload := map[string]any{
		"status": execStreamStateBackgrounded,
		"detail": "该委派任务已转入后台继续运行，因为当前回合被新消息取代。任务完成后，结果会作为后台任务结果回到本会话，不要重新派发同一个任务。",
	}
	if description := delegationDescriptionFromArgs(pending.ArgsJSON); description != "" {
		payload["description"] = description
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return `{"status":"backgrounded"}`
	}
	return string(encoded)
}

func delegationDescriptionFromArgs(argsJSON []byte) string {
	args, err := runtimecore.DecodeArgsMap(argsJSON)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(runtimecore.ReadStringArg(args, "description", "prompt"))
}

func (service *Service) rememberBackgroundedDelegation(record backgroundedDelegationExec) {
	if service == nil || strings.TrimSpace(record.ExecID) == "" {
		return
	}
	service.backgroundedDelegationMu.Lock()
	defer service.backgroundedDelegationMu.Unlock()
	if service.backgroundedDelegations == nil {
		service.backgroundedDelegations = make(map[string]backgroundedDelegationExec)
	}
	service.backgroundedDelegations[strings.TrimSpace(record.ExecID)] = record
}

func (service *Service) backgroundedDelegationRecord(execID string) (backgroundedDelegationExec, bool) {
	if service == nil || strings.TrimSpace(execID) == "" {
		return backgroundedDelegationExec{}, false
	}
	service.backgroundedDelegationMu.Lock()
	defer service.backgroundedDelegationMu.Unlock()
	record, ok := service.backgroundedDelegations[strings.TrimSpace(execID)]
	return record, ok
}

func (service *Service) forgetBackgroundedDelegation(execID string) {
	if service == nil || strings.TrimSpace(execID) == "" {
		return
	}
	service.backgroundedDelegationMu.Lock()
	defer service.backgroundedDelegationMu.Unlock()
	delete(service.backgroundedDelegations, strings.TrimSpace(execID))
}

// deliverBackgroundedDelegationResult 把后台化任务的最终结果落到会话持久化状态里。
// 结果先写成 metadata（不进入 prompt replay，可安全落在任意物理位置），下一回合开头
// 再由 pendingBackgroundedDelegationEntries 转成模型可见的 prompt_context 回放。
// 绝不向已终态的父流写入，也绝不静默丢弃。
func (service *Service) deliverBackgroundedDelegationResult(record backgroundedDelegationExec, status string, resultText string) {
	if service == nil {
		return
	}
	conversationID := strings.TrimSpace(record.ConversationID)
	status = strings.TrimSpace(status)
	resultText = strings.TrimSpace(resultText)
	values := map[string]any{
		"exec_id":      strings.TrimSpace(record.ExecID),
		"exec_kind":    strings.TrimSpace(record.ExecKind),
		"tool_call_id": strings.TrimSpace(record.ToolCallID),
		"description":  strings.TrimSpace(record.Description),
		"status":       status,
		"result":       resultText,
		"finished_at":  time.Now().UTC().Format(time.RFC3339),
	}
	if service.store == nil || conversationID == "" {
		// 没有持久化落点时至少留下可检索的运行日志，不静默吞掉结果。
		logger.Errorf("forwarder backgrounded delegation result undeliverable conversation_id=%s exec_id=%s status=%s result_chars=%d",
			conversationID, strings.TrimSpace(record.ExecID), status, len([]rune(resultText)))
		return
	}
	entry := newMetadataEntry(record.TurnSeq, record.ParentRequestID, metadataTypeBackgroundedDelegationResult, values)
	if _, _, err := service.store.AppendEntries(conversationID, []HistoryEntry{entry}); err != nil {
		logger.Errorf("forwarder backgrounded delegation result persist failed conversation_id=%s exec_id=%s err=%v",
			conversationID, strings.TrimSpace(record.ExecID), err)
		return
	}
	logger.Infof("forwarder backgrounded delegation result persisted conversation_id=%s exec_id=%s exec_kind=%s tool_call_id=%s status=%s result_chars=%d",
		conversationID, strings.TrimSpace(record.ExecID), strings.TrimSpace(record.ExecKind), strings.TrimSpace(record.ToolCallID), status, len([]rune(resultText)))
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), record.ParentRequestID, conversationID, "backgrounded_delegation_result_persisted", values)
	}
}

// pendingBackgroundedDelegationEntries 返回下一回合开头需要补放的后台任务结果。
// 它把已持久化但尚未回放过的 backgrounded_delegation_result metadata 转成 prompt_context，
// 追加在新回合最前面：模型可见历史保持 append-only，既有前缀不移动、不改写。
func (service *Service) pendingBackgroundedDelegationEntries(conversation *ConversationFile, requestID string, turnSeq int64) []HistoryEntry {
	if service == nil || conversation == nil || turnSeq <= 0 {
		return nil
	}
	replayed := make(map[string]struct{})
	pending := make([]PromptContextMessage, 0, 2)
	for _, entry := range conversation.Entries {
		switch strings.TrimSpace(entry.Kind) {
		case "prompt_context":
			var payload promptContextEntryPayload
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				continue
			}
			if strings.TrimSpace(payload.Source) != promptContextSourceBackgroundedDelegationResult {
				continue
			}
			replayed[strings.TrimSpace(payload.ContentHash)] = struct{}{}
		case "metadata":
			var payload metadataPayload
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				continue
			}
			if strings.TrimSpace(payload.Type) != metadataTypeBackgroundedDelegationResult {
				continue
			}
			context := newPromptContextMessage(
				promptContextSourceBackgroundedDelegationResult,
				modeladapter.Message{
					Role:    "user",
					Content: backgroundedDelegationReplayText(payload.Value),
				},
				true,
			)
			pending = append(pending, context)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	entries := make([]HistoryEntry, 0, len(pending))
	for _, context := range pending {
		if _, done := replayed[strings.TrimSpace(context.ContentHash)]; done {
			continue
		}
		replayed[strings.TrimSpace(context.ContentHash)] = struct{}{}
		entries = append(entries, newPromptContextEntry(turnSeq, requestID, context))
	}
	return entries
}

func backgroundedDelegationReplayText(values map[string]any) string {
	description := strings.TrimSpace(readStringValue(values["description"]))
	status := strings.TrimSpace(readStringValue(values["status"]))
	result := strings.TrimSpace(readStringValue(values["result"]))
	if status == "" {
		status = "completed"
	}
	title := description
	if title == "" {
		title = strings.TrimSpace(readStringValue(values["tool_call_id"]))
	}
	builder := strings.Builder{}
	builder.WriteString(fmt.Sprintf("此前转入后台继续运行的委派任务已结束（状态：%s）。", status))
	if title != "" {
		builder.WriteString(fmt.Sprintf("任务：%s。", title))
	}
	if result != "" {
		builder.WriteString("\n结果：\n")
		builder.WriteString(result)
	}
	return wrapSystemReminder(builder.String())
}

// absorbBackgroundedSubagentExecResult 静默吸收后台化子代理的迟到结果：
// exec 已从父流摘除，正常查找必然落到 "pending exec not found"。这里只更新
// nativeDelegations 运行态与桌面委派卡片，并把结果归属回会话，不报协议错误。
func (service *Service) absorbBackgroundedSubagentExecResult(execID string, subagentResult *subagentTerminalOutcome) bool {
	if service == nil {
		return false
	}
	record, ok := service.backgroundedDelegationRecord(execID)
	if !ok || strings.TrimSpace(record.ExecKind) != "subagent" {
		return false
	}
	if subagentResult == nil {
		// 非终态进展事件：后台子代理仍在跑，静默吸收即可。
		return true
	}
	status := delegation.TaskCompleted
	progress := "Cursor 子代理已在后台完成"
	if subagentResult.Failed {
		status = delegation.TaskFailed
		progress = "Cursor 子代理在后台执行失败"
	}
	service.updateNativeDelegationStatus(record.ExecID, status, progress, subagentResult.ErrorText)
	service.forgetBackgroundedDelegation(record.ExecID)
	service.deliverBackgroundedDelegationResult(record, string(status), subagentResult.Text)
	return true
}

// subagentTerminalOutcome 是后台化子代理终态结果的归一化形态。
type subagentTerminalOutcome struct {
	Failed    bool
	ErrorText string
	Text      string
}

// subagentTerminalOutcomeFrom 把客户端子代理终态结果归一化；非终态事件返回 nil。
func subagentTerminalOutcomeFrom(result *agentv1.SubagentResult) *subagentTerminalOutcome {
	if result == nil {
		return nil
	}
	if subagentResultFailed(result) {
		errorText := subagentResultErrorText(result)
		return &subagentTerminalOutcome{Failed: true, ErrorText: errorText, Text: errorText}
	}
	return &subagentTerminalOutcome{Text: strings.TrimSpace(result.GetSuccess().GetFinalMessage())}
}

// absorbBackgroundedAggregateResult 处理后台化 Multitask 聚合的迟到结果。
func (service *Service) absorbBackgroundedAggregateResult(payload *streamDelegationResult) bool {
	if service == nil || payload == nil {
		return false
	}
	record, ok := service.backgroundedDelegationRecord(payload.ExecID)
	if !ok || strings.TrimSpace(record.ExecKind) != "delegation_aggregate" {
		return false
	}
	var summary delegatedAggregateResult
	_ = json.Unmarshal([]byte(payload.Payload), &summary)
	status := strings.TrimSpace(summary.Status)
	if status == "" {
		status = "unknown"
	}
	service.forgetBackgroundedDelegation(record.ExecID)
	service.deliverBackgroundedDelegationResult(record, status, aggregateResultReplayText(summary, payload.Payload))
	return true
}

func aggregateResultReplayText(summary delegatedAggregateResult, rawPayload string) string {
	if len(summary.Tasks) == 0 {
		return strings.TrimSpace(rawPayload)
	}
	parts := make([]string, 0, len(summary.Tasks))
	for _, task := range summary.Tasks {
		label := firstNonEmpty(strings.TrimSpace(task.ModelID), strings.TrimSpace(task.ModelGroupID), strings.TrimSpace(task.TaskID), "worker")
		segment := fmt.Sprintf("[%s] %s", strings.TrimSpace(string(task.Status)), label)
		if output := strings.TrimSpace(task.Output); output != "" {
			segment += "\n" + output
		}
		if errText := strings.TrimSpace(task.Error); errText != "" {
			segment += "\n错误：" + errText
		}
		parts = append(parts, segment)
	}
	return strings.Join(parts, "\n\n")
}
