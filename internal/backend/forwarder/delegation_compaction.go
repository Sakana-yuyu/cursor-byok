package forwarder

import (
	"fmt"
	"strings"

	modeladapter "cursor/internal/backend/agent/model"
)

const (
	delegatedSnipThresholdBytes    = 16 * 1024
	delegatedSnipTargetBytes       = 4 * 1024
	delegatedCompactionBudgetFloor = int64(16_000)
	delegatedCompactionWindowRatio = 0.8
	delegatedCompactionRetryLimit  = 2
)

// delegatedContextBudgetForWindow 由上下文窗口推导 worker 压缩预算：
// budget = 0.8*window - compactionAutoReserveTokens；下限 protected，
// window<=0 表示无窗口信息，返回 0（关闭主动压缩，超限自救仍可用）。
func delegatedContextBudgetForWindow(window int64) int64 {
	if window <= 0 {
		return 0
	}
	budget := int64(delegatedCompactionWindowRatio*float64(window)) - compactionAutoReserveTokens
	if budget < delegatedCompactionBudgetFloor {
		// The floor is a large-window quality guard, never permission to send an
		// input that leaves no room for the provider output safety reserve.
		maximumInputBudget := window - providerOutputSafetyTokens
		if maximumInputBudget < 1 {
			return 1
		}
		if maximumInputBudget < delegatedCompactionBudgetFloor {
			return maximumInputBudget
		}
		return delegatedCompactionBudgetFloor
	}
	return budget
}

// delegatedContextOverflowError 判断错误是否属于上下文窗口超限（可触发压缩重试）。
func delegatedContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "context_too_large") ||
		strings.Contains(text, "context_length_exceeded") ||
		strings.Contains(text, "exceeds the context window")
}

type delegatedCompactionStats struct {
	SnipCount        int
	DroppedCount     int
	BeforeTokens     int64
	AfterTokens      int64
	BeforeGroupCount int
	AfterGroupCount  int
}

func delegatedToolResultOmittedText(toolName string) string {
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "tool"
	}
	return "[工具输出过长已省略（工具: " + name + "）]"
}

// snipDelegatedOversizedToolResults 把超长 tool result 截断到预算内。
// 最近一轮（最后一条 role=tool 消息）不截断，保护当前正在使用的上下文。
func snipDelegatedOversizedToolResults(messages []modeladapter.Message, budget int64, stats *delegatedCompactionStats) ([]modeladapter.Message, bool) {
	if budget <= 0 || len(messages) == 0 {
		return messages, false
	}
	changed := false
	if stats != nil {
		stats.BeforeTokens = estimateModelMessagesTokens(messages)
	}
	// 找出最近一轮 tool 消息的索引（不截断它）
	lastToolIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.TrimSpace(messages[i].Role) == "tool" {
			lastToolIdx = i
			break
		}
	}
	for estimateModelMessagesTokens(messages) > budget {
		target := -1
		for i := 0; i < len(messages); i++ {
			if i == lastToolIdx {
				continue
			}
			if strings.TrimSpace(messages[i].Role) != "tool" {
				continue
			}
			if len(messages[i].Content) > delegatedSnipThresholdBytes {
				target = i
				break
			}
		}
		if target < 0 {
			break
		}
		snipped := messages[target].Content[:delegatedSnipTargetBytes] + delegatedToolResultOmittedText(messages[target].Name)
		messages[target].Content = snipped
		changed = true
		if stats != nil {
			stats.SnipCount++
		}
	}
	if stats != nil {
		stats.AfterTokens = estimateModelMessagesTokens(messages)
	}
	return messages, changed
}

const delegatedCompactionKeepTurns = 4

type delegatedMessageGroup struct {
	start     int
	end       int
	protected bool
	hasTools  bool
}

// buildDelegatedMessageWindow clones and windows delegated-worker messages without
// applying any replay normalization. Tool batches therefore either remain complete
// or are rejected/dropped as one unit.
func buildDelegatedMessageWindow(messages []modeladapter.Message, budget int64, stats *delegatedCompactionStats) ([]modeladapter.Message, bool, error) {
	working := cloneDelegatedMessages(messages)
	if budget <= 0 || len(working) == 0 {
		return working, false, nil
	}
	groups, err := groupDelegatedMessages(working)
	if err != nil {
		return nil, false, err
	}
	if stats != nil {
		stats.BeforeTokens = estimateModelMessagesTokens(working)
		stats.BeforeGroupCount = len(groups)
	}
	changed := false

	// Snip only optional older tool batches. The newest batch is protected as a
	// complete group so a retry never damages the currently active tool chain.
	for estimateModelMessagesTokens(working) > budget {
		target := -1
		for _, group := range groups {
			if group.protected || !group.hasTools {
				continue
			}
			for index := group.start; index <= group.end; index++ {
				message := working[index]
				if strings.TrimSpace(message.Role) == "tool" && len(message.Content) > delegatedSnipThresholdBytes {
					target = index
					break
				}
			}
			if target >= 0 {
				break
			}
		}
		if target < 0 {
			break
		}
		working[target].Content = working[target].Content[:delegatedSnipTargetBytes] + delegatedToolResultOmittedText(working[target].Name)
		changed = true
		if stats != nil {
			stats.SnipCount++
		}
	}

	keep := make([]bool, len(groups))
	for index := range keep {
		keep[index] = true
	}
	for estimateKeptDelegatedGroups(working, groups, keep) > budget {
		dropped := false
		for index, group := range groups {
			if !keep[index] || group.protected {
				continue
			}
			keep[index] = false
			changed = true
			dropped = true
			if stats != nil {
				stats.DroppedCount++
			}
			break
		}
		if !dropped {
			break
		}
	}
	out := make([]modeladapter.Message, 0, len(working))
	for index, group := range groups {
		if keep[index] {
			out = append(out, working[group.start:group.end+1]...)
		}
	}
	if err := validateDelegatedMessageStructure(out); err != nil {
		return nil, false, err
	}
	if stats != nil {
		stats.AfterTokens = estimateModelMessagesTokens(out)
		stats.AfterGroupCount = len(groups) - stats.DroppedCount
	}
	return out, changed, nil
}

func groupDelegatedMessages(messages []modeladapter.Message) ([]delegatedMessageGroup, error) {
	groups := make([]delegatedMessageGroup, 0, len(messages))
	callIDs := make(map[string]struct{})
	firstUser := -1
	lastUser := -1
	for index, message := range messages {
		if strings.TrimSpace(message.Role) == "user" {
			if firstUser < 0 {
				firstUser = index
			}
			lastUser = index
		}
	}
	for index := 0; index < len(messages); {
		message := messages[index]
		role := strings.TrimSpace(message.Role)
		switch role {
		case "tool":
			return nil, fmt.Errorf("orphan delegated tool result %q", strings.TrimSpace(message.ToolCallID))
		case "user":
			end := index
			// A non-tool turn starts at a user message and includes its assistant
			// reply. Keep the initial task and latest current prompt protected,
			// but never leave historical request/reply pairs half-visible.
			if index+1 < len(messages) && strings.TrimSpace(messages[index+1].Role) == "assistant" && len(messages[index+1].ToolCalls) == 0 {
				end = index + 1
			}
			groups = append(groups, delegatedMessageGroup{start: index, end: end, protected: index == firstUser || index == lastUser})
			index = end + 1
		case "assistant":
			if len(message.ToolCalls) == 0 {
				groups = append(groups, delegatedMessageGroup{start: index, end: index})
				index++
				continue
			}
			expected := make(map[string]struct{}, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				callID := strings.TrimSpace(call.ID)
				if callID == "" {
					return nil, fmt.Errorf("delegated assistant tool call is missing an id")
				}
				if _, exists := callIDs[callID]; exists {
					return nil, fmt.Errorf("duplicate delegated tool call id %q", callID)
				}
				callIDs[callID] = struct{}{}
				expected[callID] = struct{}{}
			}
			end := index
			if index+1 < len(messages) && strings.TrimSpace(messages[index+1].Role) != "tool" {
				return nil, fmt.Errorf("incomplete delegated tool batch after assistant message %d", index)
			}
			seen := make(map[string]struct{}, len(expected))
			for end+1 < len(messages) && strings.TrimSpace(messages[end+1].Role) == "tool" {
				end++
				callID := strings.TrimSpace(messages[end].ToolCallID)
				if _, exists := expected[callID]; !exists {
					return nil, fmt.Errorf("delegated tool result %q does not match its assistant batch", callID)
				}
				if _, exists := seen[callID]; exists {
					return nil, fmt.Errorf("duplicate delegated tool result %q", callID)
				}
				seen[callID] = struct{}{}
			}
			if len(seen) != len(expected) {
				return nil, fmt.Errorf("incomplete delegated tool batch after assistant message %d", index)
			}
			groups = append(groups, delegatedMessageGroup{start: index, end: end, hasTools: true})
			index = end + 1
		default:
			groups = append(groups, delegatedMessageGroup{start: index, end: index, protected: role == "system" || index == firstUser || index == lastUser})
			index++
		}
	}
	if len(groups) > 0 {
		groups[len(groups)-1].protected = true
	}
	return groups, nil
}

func estimateKeptDelegatedGroups(messages []modeladapter.Message, groups []delegatedMessageGroup, keep []bool) int64 {
	total := int64(0)
	for index, group := range groups {
		if keep[index] {
			total += estimateModelMessagesTokens(messages[group.start : group.end+1])
		}
	}
	return total
}

func validateDelegatedMessageStructure(messages []modeladapter.Message) error {
	_, err := groupDelegatedMessages(messages)
	return err
}

// dropDelegatedEarlyMessages 从最旧开始按轮成组丢弃 assistant 及其后所有连续
// role==tool 消息，直到预算内。一条 assistant 可能带多个 ToolCalls（并行工具调用），
// 对应其后多条连续 tool 消息，必须整组删除，否则会残留孤立 tool 消息导致
// provider 报 "tool result without matching tool_use"。
// 索引 0（system）与索引 1（首条 user）永不丢弃；保留最近 delegatedCompactionKeepTurns 轮。
// 注意：调用方必须使用返回的切片；底层数组可能已被原地修改（append 覆盖）。
func dropDelegatedEarlyMessages(messages []modeladapter.Message, budget int64, stats *delegatedCompactionStats) ([]modeladapter.Message, bool) {
	if budget <= 0 || len(messages) <= 2 {
		return messages, false
	}
	changed := false
	if stats != nil && stats.BeforeTokens == 0 {
		stats.BeforeTokens = estimateModelMessagesTokens(messages)
	}
	// 计算保留起点 keepStart：从尾部数第 delegatedCompactionKeepTurns 个 assistant 的索引。
	// 不足该轮数则 keepStart 保持 0（无可丢区间）。
	keepStart := 0
	seenTurns := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.TrimSpace(messages[i].Role) == "assistant" {
			seenTurns++
			if seenTurns == delegatedCompactionKeepTurns {
				keepStart = i
				break
			}
		}
	}
	for estimateModelMessagesTokens(messages) > budget {
		dropped := false
		for i := 2; i < keepStart; i++ { // 索引 0=system、1=首条 user 永不丢
			if strings.TrimSpace(messages[i].Role) != "assistant" {
				continue
			}
			// 找到该 assistant 之后所有连续 role==tool 消息的最后一个索引 end
			// （一轮并行调用多个工具 = 1 条 assistant + N 条连续 tool）。
			end := i + 1
			for end < len(messages) && strings.TrimSpace(messages[end].Role) == "tool" {
				end++
			}
			end-- // 最后一个连续 tool 索引；若无 tool（end==i）则不构成可丢轮
			if end == i {
				continue
			}
			messages = append(messages[:i], messages[end+1:]...)
			dropped = true
			changed = true
			if stats != nil {
				stats.DroppedCount++
			}
			removed := end - i + 1
			if keepStart >= removed {
				keepStart -= removed // 删除发生在保留起点之前，起点前移 removed
			} else {
				keepStart = 0
			}
			break
		}
		if !dropped {
			break
		}
	}
	if stats != nil {
		stats.AfterTokens = estimateModelMessagesTokens(messages)
	}
	return messages, changed
}

// maybeCompactDelegatedMessages 是主动阈值压缩组合入口：snip → drop。
func maybeCompactDelegatedMessages(messages []modeladapter.Message, budget int64, stats *delegatedCompactionStats) ([]modeladapter.Message, bool) {
	if budget <= 0 || len(messages) == 0 {
		return messages, false
	}
	out, changed, err := buildDelegatedMessageWindow(messages, budget, stats)
	if err != nil {
		return messages, false
	}
	return out, changed
}
