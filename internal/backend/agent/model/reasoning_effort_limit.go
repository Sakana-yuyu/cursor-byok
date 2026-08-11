package modeladapter

var thinkingEffortRank = map[string]int{
	"disabled": 0,
	"low":      1,
	"medium":   2,
	"high":     3,
	"xhigh":    4,
	"max":      5,
}

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
