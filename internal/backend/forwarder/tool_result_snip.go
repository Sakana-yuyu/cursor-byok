package forwarder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 本文件移植自 DeepSeek-Reasonix 的「cache-first context maintenance」思想：在昂贵的
// LLM 摘要压缩（会让 prompt cache 归零并从头重建）触发之前，先用零成本的「snip/prune」
// 持久化缩短陈旧的大工具结果，从而避免或推迟摘要压缩、延长缓存命中。
//
// 与投影层（tool_result_replay_truncation.go，append-only、零缓存风险）不同，本层会持久化
// 改写历史中的 tool_result，因此会破坏被改位置之后的 prefix cache。这是有意的两害相权取其轻：
// 仅在「本来就要做摘要重建、缓存必然归零」的兜底场景触发，省掉一次摘要 API 调用并保留更多命中。
// 详细权衡见 .agents/skills/prefix-cache-stability/SKILL.md。

const (
	// snip 触发门槛：只对明显过大的陈旧工具结果动手，避免对本来就不大的结果无谓截断。
	staleToolResultSnipMinBytes = 8 * 1024

	// snip（forcePrune=false）保留头尾的目标上限。
	staleToolResultSnipLimitBytes = 4 * 1024

	// 尾部保护轮数：与 compactionPreferredTailTurns 一致，近 N 轮（含当前轮）的工具结果不动。
	staleToolResultProtectedTailTurns = 4

	snippedStaleToolResultPrefix = "[snipped stale tool result:"
	prunedStaleToolResultPrefix  = "[stale tool result elided:"
)

// snippedToolResultArchive 记录被 snip/prune 掉的工具结果原件，写入会话目录便于审计/回溯。
type snippedToolResultArchive struct {
	ConversationID string    `json:"conversation_id"`
	ToolCallID     string    `json:"tool_call_id"`
	ToolName       string    `json:"tool_name"`
	TurnSeq        int64     `json:"turn_seq"`
	Mode           string    `json:"mode"`
	OriginalText   string    `json:"original_text"`
	HadToolCall    bool      `json:"had_tool_call"`
	SnippedAt      time.Time `json:"snipped_at"`
}

// staleToolResultMaintenanceOutcome 汇报一次 snip/prune 维护的结果。
type staleToolResultMaintenanceOutcome struct {
	snipped      int
	savedChars   int
	savedTokens  int64
	archivePaths []string
	applied      bool
}

// maintainStaleToolResults 对会话中陈旧的大工具结果做持久化 snip（forcePrune=false，保留头尾）
// 或 prune（forcePrune=true，缩成占位符）。原件先存档再改写，通过 store 持久化。
// 返回维护结果；applied=false 表示本次没有任何改动（无可维护对象或全部已维护过）。
//
// 安全性：只动 tool_result 的 ResultText/ToolCall，不改 entry 数量、顺序、Kind、ToolCallID
// 配对关系，也不碰 reasoning_* 字段（部分 provider 续写依赖）。跳过已带占位符前缀的 entry（幂等）。
func (service *Service) maintainStaleToolResults(stream *ActiveStream, conversation *ConversationFile, forcePrune bool) (*staleToolResultMaintenanceOutcome, error) {
	outcome := &staleToolResultMaintenanceOutcome{}
	if service == nil || stream == nil || conversation == nil || len(conversation.Entries) == 0 {
		return outcome, nil
	}

	protectedFloor := staleToolResultProtectedTurnFloor(conversation)
	targets := collectStaleToolResultTargets(conversation, protectedFloor, forcePrune)
	if len(targets) == 0 {
		return outcome, nil
	}

	// 先把原件存档（存档失败不阻断，降级为不存档）。
	archiveDir := ""
	if service.store != nil {
		archiveDir = filepath.Join(service.store.conversationDir(stream.ConversationID), "snips")
	}
	if archiveDir != "" {
		if err := os.MkdirAll(archiveDir, 0o755); err != nil {
			archiveDir = "" // 目录建不出来就放弃存档，但不阻断 snip
		}
	}

	next := make([]HistoryEntry, len(conversation.Entries))
	copy(next, conversation.Entries)

	for _, idx := range targets {
		original := next[idx]
		rewritten, savedChars, archived, ok := rewriteStaleToolResultEntry(original, forcePrune, archiveDir, stream)
		if !ok {
			continue
		}
		next[idx] = rewritten
		outcome.snipped++
		outcome.savedChars += savedChars
		outcome.savedTokens += estimateTextTokens(strings.Repeat("x", savedChars))
		if archived != "" {
			outcome.archivePaths = append(outcome.archivePaths, archived)
		}
	}

	if outcome.snipped == 0 {
		return outcome, nil
	}
	outcome.applied = true

	// 持久化：走 compaction 同款 ReplaceEntries 路径，并同步 checkpoint 会话。
	if service.store != nil {
		persisted, err := service.store.ReplaceEntries(stream.ConversationID, append([]HistoryEntry(nil), next...), func(item *ConversationFile) error {
			if item == nil {
				return nil
			}
			// snip 不改变 token 统计语义，但清掉自动压缩挂起态，让本轮回重新评估预算。
			clearConversationAutoCompactionState(item)
			return nil
		})
		if err != nil {
			return outcome, err
		}
		stream.mu.Lock()
		stream.CheckpointConversation = persisted
		stream.UpdatedAt = time.Now().UTC()
		stream.mu.Unlock()
		return outcome, nil
	}
	// 无 store（内存模式）时直接替换 checkpoint。
	stream.mu.Lock()
	conversation.Entries = next
	stream.CheckpointConversation = cloneConversationFile(conversation)
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	return outcome, nil
}

// staleToolResultProtectedTurnFloor 计算受保护的「近期轮次」下界：最大 TurnSeq 往前数
// staleToolResultProtectedTailTurns 轮（含当前轮）。TurnSeq 严格小于该下界的工具结果视为陈旧、可维护。
func staleToolResultProtectedTurnFloor(conversation *ConversationFile) int64 {
	if conversation == nil || len(conversation.Entries) == 0 {
		return 0
	}
	var maxTurnSeq int64
	for _, entry := range conversation.Entries {
		if entry.TurnSeq > maxTurnSeq {
			maxTurnSeq = entry.TurnSeq
		}
	}
	if maxTurnSeq <= 0 {
		return 0
	}
	floor := maxTurnSeq - int64(staleToolResultProtectedTailTurns) + 1
	if floor < 1 {
		floor = 1
	}
	return floor
}

// collectStaleToolResultTargets 返回可维护（陈旧、足够大、未被维护过）的 tool_result entry 下标。
func collectStaleToolResultTargets(conversation *ConversationFile, protectedFloor int64, forcePrune bool) []int {
	var targets []int
	for i, entry := range conversation.Entries {
		if strings.TrimSpace(entry.Kind) != "tool_result" {
			continue
		}
		if entry.TurnSeq <= 0 || entry.TurnSeq >= protectedFloor {
			continue // 近期/当前轮，保护
		}
		head := strings.TrimSpace(entryTextHead(entry))
		alreadySnipped := strings.HasPrefix(head, snippedStaleToolResultPrefix)
		alreadyPruned := strings.HasPrefix(head, prunedStaleToolResultPrefix)
		// 幂等：已 prune 的永不再动；已 snip 的仅在 prune 模式下进一步缩成占位符。
		if alreadyPruned {
			continue
		}
		if alreadySnipped && !forcePrune {
			continue
		}
		// 大小门槛：仅对「未被维护过的新鲜大结果」要求 >= staleToolResultSnipMinBytes。
		// 已 snip 的结果本身已很小，prune 模式要进一步压缩它，绕过大小门槛。
		if !alreadySnipped && toolResultEntrySize(entry) < staleToolResultSnipMinBytes {
			continue // 太小，不值得动
		}
		targets = append(targets, i)
	}
	return targets
}

// entryTextHead 取出 tool_result payload 的 ResultText 头部文本（用于占位符前缀判定）。
func entryTextHead(entry HistoryEntry) string {
	var payload toolResultEntryPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		return ""
	}
	return payload.ResultText
}

// toolResultEntrySize 估算 tool_result payload 的字节体量（ResultText + ToolCall）。
func toolResultEntrySize(entry HistoryEntry) int {
	var payload toolResultEntryPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		return len(entry.Payload)
	}
	size := len(payload.ResultText)
	if len(payload.ToolCall) > 0 {
		size += len(payload.ToolCall)
	}
	return size
}

// rewriteStaleToolResultEntry 改写单个 tool_result：snip 保留头尾（forcePrune=false），
// prune 缩成占位符并清空 ToolCall（forcePrune=true）。返回新 entry、节省字节数、存档路径、是否改动。
// 原件在改写前存档（若 archiveDir 非空且存档成功）。
func rewriteStaleToolResultEntry(entry HistoryEntry, forcePrune bool, archiveDir string, stream *ActiveStream) (HistoryEntry, int, string, bool) {
	var payload toolResultEntryPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		return entry, 0, "", false
	}
	toolName := firstNonEmpty(strings.TrimSpace(payload.ToolName), "tool")
	originalText := strings.TrimSpace(payload.ResultText)
	if originalText == "" {
		return entry, 0, "", false
	}

	// 存档原件（prune 模式下若该 entry 仅是已 snip 的占位符，则不再重复存档）。
	archivePath := ""
	if archiveDir != "" && !strings.HasPrefix(originalText, snippedStaleToolResultPrefix) {
		archivePath = archiveStaleToolResult(archiveDir, stream, entry, payload)
	}

	var replacement string
	if forcePrune {
		replacement = fmt.Sprintf("%s %s, %d bytes removed; re-run the tool if the data is needed again]",
			prunedStaleToolResultPrefix, toolName, len(originalText))
	} else {
		snipped := truncateProjectedReplayTextMiddle(toolName, originalText, staleToolResultSnipLimitBytes)
		replacement = fmt.Sprintf("%s %s, %d bytes removed; showing head and tail]\n%s",
			snippedStaleToolResultPrefix, toolName, len(originalText), snipped)
	}

	savedChars := len(payload.ResultText) - len(replacement)
	if savedChars <= 0 {
		return entry, 0, archivePath, false
	}

	payload.ResultText = replacement
	if forcePrune {
		payload.ToolCall = nil // prune 模式同时清掉嵌入的结构化大负载
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return entry, 0, archivePath, false
	}
	if bytes.Equal(bytes.TrimSpace(entry.Payload), bytes.TrimSpace(encoded)) {
		return entry, 0, archivePath, false
	}
	entry.Payload = encoded
	return entry, savedChars, archivePath, true
}

// archiveStaleToolResult 把被改写的工具结果原件写入会话目录的 snips/ 子目录，便于审计回溯。
// 文件名带 turnSeq 与 toolCallID 以保证唯一与可定位；失败返回空串（不阻断 snip）。
func archiveStaleToolResult(archiveDir string, stream *ActiveStream, entry HistoryEntry, payload toolResultEntryPayload) string {
	toolCallID := strings.TrimSpace(payload.ToolCallID)
	if toolCallID == "" {
		toolCallID = strings.TrimSpace(entry.ToolCallID)
	}
	archive := snippedToolResultArchive{
		ConversationID: stream.ConversationID,
		ToolCallID:     toolCallID,
		ToolName:       strings.TrimSpace(payload.ToolName),
		TurnSeq:        entry.TurnSeq,
		Mode:           stream.Mode.String(),
		OriginalText:   payload.ResultText,
		HadToolCall:    len(payload.ToolCall) > 0,
		SnippedAt:      time.Now().UTC(),
	}
	name := fmt.Sprintf("turn-%d-%s.json", entry.TurnSeq, sanitizeArchiveName(toolCallID))
	path := filepath.Join(archiveDir, name)
	if err := writeJSONFileAtomic(path, archive); err != nil {
		return ""
	}
	return path
}

// sanitizeArchiveName 把 toolCallID 之类的字符串清洗成安全的文件名片段。
func sanitizeArchiveName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "unknown"
	}
	return out
}

// recoverBudgetBySnippingStaleToolResults 在自动压缩判定为「需要压缩」后，尝试用持久化
// snip/prune 陈旧工具结果来把上下文压回预算线下，从而避免昂贵的 LLM 摘要压缩。
// contextTokens 为当前估算/记录的上下文 token；budgetTokens 为压缩预算上限。
// 返回 true 表示已通过 snip/prune 救回（调用方应返回 nil 计划，跳过摘要压缩）。
//
// 策略：先 snip（保留头尾，forcePrune=false），若 token 仍超预算再 prune（缩成占位符）。
// 救回时设置 stream.StaleToolResultSnipApplied，让 driveProvider 重新快照+编译，
// 保证后续 provider 请求使用 snip 后的新鲜历史。
func (service *Service) recoverBudgetBySnippingStaleToolResults(stream *ActiveStream, conversation *ConversationFile, contextTokens, budgetTokens int64) bool {
	if service == nil || stream == nil || conversation == nil || budgetTokens <= 0 {
		return false
	}
	if contextTokens <= budgetTokens {
		return false // 未超预算，无需 snip
	}

	// snip（保留头尾）。
	outcome, err := service.maintainStaleToolResults(stream, conversation, false)
	if err != nil || outcome == nil || !outcome.applied {
		// snip 无可维护对象；尝试 prune。
		return service.tryRecoverByPruningStaleToolResults(stream, conversation, contextTokens, budgetTokens)
	}
	if contextTokens-outcome.savedTokens <= budgetTokens {
		service.markStaleToolResultSnipApplied(stream)
		return true
	}
	// snip 后仍超预算，尝试进一步 prune。
	return service.tryRecoverByPruningStaleToolResults(stream, conversation, contextTokens-outcome.savedTokens, budgetTokens)
}

// tryRecoverByPruningStaleToolResults 在 snip 未能救回时，尝试 prune（缩成占位符）。
// 注意：若前一步 snip 已更新 checkpoint，需基于最新 checkpoint 再 prune。
func (service *Service) tryRecoverByPruningStaleToolResults(stream *ActiveStream, conversation *ConversationFile, contextTokens, budgetTokens int64) bool {
	latest := conversation
	if stream.CheckpointConversation != nil {
		latest = stream.CheckpointConversation
	}
	outcome, err := service.maintainStaleToolResults(stream, latest, true)
	if err != nil || outcome == nil || !outcome.applied {
		return false
	}
	if contextTokens-outcome.savedTokens <= budgetTokens {
		service.markStaleToolResultSnipApplied(stream)
		return true
	}
	return false
}

// markStaleToolResultSnipApplied 标记本 pass 已做持久化 snip/prune，供 driveProvider 据此重新编译。
func (service *Service) markStaleToolResultSnipApplied(stream *ActiveStream) {
	if stream == nil {
		return
	}
	stream.mu.Lock()
	stream.StaleToolResultSnipApplied = true
	stream.mu.Unlock()
}

// staleToolResultSnipAppliedLocked 线程安全地读取本 pass 是否已做过持久化 snip/prune。
func (stream *ActiveStream) staleToolResultSnipAppliedLocked() bool {
	if stream == nil {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.StaleToolResultSnipApplied
}
