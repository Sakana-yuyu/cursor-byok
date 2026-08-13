// interrupted_status.go 定义可持久化的 interrupted 终态。
//
// 背景：current_loop_status 不是被直接写入的字段，而是 deriveRequestLoopStatus 从
// context.json 的条目推导出来的（每次读盘与每次写盘前都会重算）。因此「进程被杀、
// 会话永远停在 running/waiting_tool」的真正缺口不是状态字段没写，而是 context.json
// 里缺少一条终态 metadata 条目——直接改 state.json 会被下一次任何写入推导回去。
//
// 为什么不复用 control{status:"canceled"}：canceled 条目会被
// canceledReplayPolicyForEntry 识别，进而由 sanitizeCanceledReplayEntries 按
// keep_stable_input 策略把该回合的 assistant_text/tool_call/tool_result 从后续
// replay 里剔除。那是对「已经发给模型的历史前缀」的回溯性改写，违反 append-only
// 硬约束并直接打爆 prefix cache 命中率。interrupted 刻意不参与该清洗：悬空的
// tool_call 已由现成的 trimReplayDanglingAssistantToolCalls 处理。
package forwarder

import (
	"encoding/json"
	"strings"
	"time"

	"cursor/internal/logger"
)

// historyReconcileProcessStartedAt 标记本进程的启动时刻。启动期对账只收口「上一个进程
// 遗留」的会话：updated_at 不早于本进程启动时刻的会话，要么是本进程刚写的，要么属于另一
// 个仍在运行的实例，两种情况都不能碰。hasActiveConversationStream 只看得见本进程内存里的
// 流，挡不住第二个实例。
var historyReconcileProcessStartedAt = time.Now().UTC()

const conversationStatusInterrupted = "interrupted"

// historyRestartInterruptReason 是启动期对账写入的原因文案。
const historyRestartInterruptReason = "local assistant restarted while this turn was still running"

// newInterruptedControlEntry 构造一条 interrupted 终态条目。
// 不写 replay_policy：该字段只对 canceled 有意义。
func newInterruptedControlEntry(turnSeq int64, requestID string, reason string) HistoryEntry {
	return newMetadataEntry(turnSeq, requestID, "control", map[string]any{
		"status": conversationStatusInterrupted,
		"reason": reason,
	})
}

// interruptedControlEntryReason 判定一条 entry 是否为 interrupted 终态标记，并取出原因。
func interruptedControlEntryReason(entry HistoryEntry) (string, bool) {
	if strings.TrimSpace(entry.Kind) != "metadata" {
		return "", false
	}
	var payload metadataPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		return "", false
	}
	if strings.TrimSpace(payload.Type) != "control" {
		return "", false
	}
	if strings.TrimSpace(readStringValue(payload.Value["status"])) != conversationStatusInterrupted {
		return "", false
	}
	return strings.TrimSpace(readStringValue(payload.Value["reason"])), true
}

// conversationLoopStatusIsStale 判定一个持久化状态是否属于「进程被打断后遗留的非终态」。
// running 与 waiting_tool 的分野只是「最后有没有悬空的 tool_call」，是同一个问题的两种表现。
func conversationLoopStatusIsStale(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "running", "waiting_tool":
		return true
	default:
		return false
	}
}

// reconcileStaleConversationLoop 是启动期对账的单会话动作：给上次进程遗留、仍停在
// running/waiting_tool 的会话补写终态条目。
//
// 安全阀：显式挡掉仍有活动流的会话，否则可能把正在跑的会话打成 interrupted。
// 时间戳保持原样：启动期对账处理的是历史遗留数据，历史列表按 updated_at 做日内排序，
// 不能把成百上千条老会话的修改时间集体推到迁移时刻。
func (service *Service) reconcileStaleConversationLoop(conversationID string) bool {
	return service.appendInterruptedTerminal(conversationID, historyRestartInterruptReason, true, true)
}

// forceMarkConversationInterrupted 是运行期收口：关闭、放弃 orphan cancel、父回合硬
// 取消子会话时使用。这些场景下流还在 broker 里活着，正是要给它写终态，所以不能再用
// 「无活动流」做前置条件；时间戳按「此刻发生」正常前进。
func (service *Service) forceMarkConversationInterrupted(conversationID string, reason string) bool {
	return service.appendInterruptedTerminal(conversationID, reason, false, false)
}

// staleConversationNeedsInterrupt 在**不持有会话文件锁**的前提下做启动期对账的前置判断。
//
// 活动流检查必须留在锁外：既有的 appendConversationEntries 是「先拿 stream.mu、再拿会话
// 锁」，若在会话锁内回头去取 stream.mu 就构成 ABBA 死锁（history 维护与同会话的一次取消
// 撞上即互相卡死）。锁外判断带来的 TOCTOU 窗口无害：真有新回合插进来，它会带着新的
// request_id 追加条目，deriveRequestLoopStatus 取最后一个有 request_id 的回合，结果仍是
// 新回合的状态。
func (service *Service) staleConversationNeedsInterrupt(conversationID string) bool {
	conversation, err := service.store.LoadConversation(conversationID)
	if err != nil || conversation == nil {
		return false
	}
	if !conversationLoopStatusIsStale(conversation.CurrentLoopStatus) {
		return false
	}
	if !conversation.UpdatedAt.Before(historyReconcileProcessStartedAt) {
		return false
	}
	requestID := strings.TrimSpace(conversation.CurrentRequestID)
	if requestID == "" {
		return false
	}
	return !service.hasActiveConversationStream(conversationID, requestID)
}

func (service *Service) appendInterruptedTerminal(conversationID string, reason string, requireIdleStream bool, preserveTimestamps bool) bool {
	if service == nil || service.store == nil {
		return false
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return false
	}
	if requireIdleStream && !service.staleConversationNeedsInterrupt(conversationID) {
		return false
	}
	changed, err := service.store.AppendEntriesConditionally(conversationID, preserveTimestamps, func(conversation *ConversationFile) []HistoryEntry {
		if conversation == nil || !conversationLoopStatusIsStale(conversation.CurrentLoopStatus) {
			return nil
		}
		requestID := strings.TrimSpace(conversation.CurrentRequestID)
		if requestID == "" {
			return nil
		}
		if requireIdleStream && !conversation.UpdatedAt.Before(historyReconcileProcessStartedAt) {
			return nil
		}
		return []HistoryEntry{newInterruptedControlEntry(conversation.CurrentTurnSeq, requestID, reason)}
	})
	if err != nil {
		logger.Errorf("forwarder interrupted terminal append failed conversation_id=%s err=%v", conversationID, err)
		return false
	}
	if changed {
		logger.Infof("forwarder conversation marked interrupted conversation_id=%s reason=%q", conversationID, reason)
	}
	return changed
}
