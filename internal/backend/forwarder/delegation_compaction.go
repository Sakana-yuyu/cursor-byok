package forwarder

import (
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
	SnipCount    int
	DroppedCount int
	BeforeTokens int64
	AfterTokens  int64
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
