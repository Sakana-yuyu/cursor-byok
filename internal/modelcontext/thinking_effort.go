package modelcontext

import (
	"regexp"
	"strings"
)

// MaxCustomThinkingEfforts 限制单个模型适配器可配置的自定义思考档位数量。
const MaxCustomThinkingEfforts = 8

// customThinkingEffortPattern 约束自定义档位 token 形态：小写字母数字开头，
// 仅含字母/数字/-/_，长度 1~32。限制字符集是为了让 channelID:effort 变体串
// 拆分与 Cursor 参数透传都无歧义（不含冒号、空格等分隔符）。
var customThinkingEffortPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// IsStandardThinkingEffort 报告 value 是否为标准思考档位（含 disabled）。
func IsStandardThinkingEffort(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "disabled", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

// NormalizeCustomThinkingEffort 校验单个自定义思考档位：须为安全 token 且不得
// 与标准档位同名。非法（含空串）返回空串。
func NormalizeCustomThinkingEffort(value string) string {
	text := strings.ToLower(strings.TrimSpace(value))
	if text == "" || IsStandardThinkingEffort(text) {
		return ""
	}
	if !customThinkingEffortPattern.MatchString(text) {
		return ""
	}
	return text
}

// NormalizeCustomThinkingEfforts 归一化自定义档位列表：逐项校验、去重、限量。
// 非法项静默丢弃，全部非法时返回 nil。列表为 nil 时原样返回 nil。
func NormalizeCustomThinkingEfforts(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	output := make([]string, 0, len(values))
	for _, value := range values {
		normalized := NormalizeCustomThinkingEffort(value)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		output = append(output, normalized)
		if len(output) >= MaxCustomThinkingEfforts {
			break
		}
	}
	if len(output) == 0 {
		return nil
	}
	return output
}

// ContainsCustomThinkingEffort 报告 value（忽略大小写与首尾空白）是否出现在
// 自定义档位列表中。
func ContainsCustomThinkingEffort(values []string, value string) bool {
	text := strings.ToLower(strings.TrimSpace(value))
	if text == "" {
		return false
	}
	for _, item := range values {
		if strings.EqualFold(strings.TrimSpace(item), text) {
			return true
		}
	}
	return false
}

// FirstInvalidCustomThinkingEffort 返回列表中第一个非法的自定义档位原始值；
// 全部合法返回空串。空串与标准档位同名项按静默丢弃处理、不算非法（保存路径的
// 错误只留给真正写错的 token）。用于保存路径给出可定位的错误信息。
func FirstInvalidCustomThinkingEffort(values []string) string {
	for _, value := range values {
		text := strings.TrimSpace(value)
		if text == "" || IsStandardThinkingEffort(text) {
			continue
		}
		if NormalizeCustomThinkingEffort(text) == "" {
			return value
		}
	}
	return ""
}
