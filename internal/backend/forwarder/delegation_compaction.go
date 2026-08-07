package forwarder

import (
	"strings"
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
