package modeladapter

import (
	"cursor/internal/modelcontext"
	"strings"
)

var thinkingEffortRank = map[string]int{
	"disabled": 0,
	"low":      1,
	"medium":   2,
	"high":     3,
	"xhigh":    4,
	"max":      5,
}

// resolveEffectiveThinkingEffort 计算标准档位下本轮生效的思考强度：
// 渠道配置值作为上限，运行时值超限被压回上限，运行时缺省时回落渠道值。
func resolveEffectiveThinkingEffort(runtimeValue string, configuredMaximum string) string {
	runtimeEffort := normalizeRuntimeThinkingEffort(runtimeValue)
	maximum := normalizeRuntimeThinkingEffort(configuredMaximum)
	if maximum == "disabled" {
		return "disabled"
	}
	if runtimeEffort == "" {
		return maximum
	}
	if maximum == "" {
		return runtimeEffort
	}
	if thinkingEffortRank[runtimeEffort] <= thinkingEffortRank[maximum] {
		return runtimeEffort
	}
	return maximum
}

// resolveEffectiveThinkingEffortWithCustom 在标准逻辑上扩展渠道自定义思考档位：
//   - 运行时值命中渠道 customThinkingEfforts 时原样采用（显式选择优先于标准上限，
//     自定义档位不参与标准序比较）；
//   - 渠道 reasoningEffort 配置为自定义档位时不构成标准上限，仅在运行时缺省时
//     作为默认档位透传。
//
// 其余语义与 resolveEffectiveThinkingEffort 一致；customValues 为空时两者等价。
func resolveEffectiveThinkingEffortWithCustom(runtimeValue string, configuredMaximum string, configuredDefault string, customValues []string) string {
	runtimeEffort := normalizeRuntimeThinkingEffort(runtimeValue)
	runtimeIsCustom := false
	if runtimeEffort == "" && modelcontext.ContainsCustomThinkingEffort(customValues, runtimeValue) {
		runtimeEffort = strings.ToLower(strings.TrimSpace(runtimeValue))
		runtimeIsCustom = true
	}
	maximum := normalizeRuntimeThinkingEffort(configuredMaximum)
	if maximum == "disabled" {
		return "disabled"
	}
	if runtimeEffort == "" {
		if customDefault := strings.ToLower(strings.TrimSpace(configuredDefault)); customDefault != "" && modelcontext.NormalizeCustomThinkingEffort(customDefault) != "" {
			return customDefault
		}
		return maximum
	}
	if maximum == "" || runtimeIsCustom {
		return runtimeEffort
	}
	if thinkingEffortRank[runtimeEffort] <= thinkingEffortRank[maximum] {
		return runtimeEffort
	}
	return maximum
}
