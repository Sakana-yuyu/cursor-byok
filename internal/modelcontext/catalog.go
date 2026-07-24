package modelcontext

import (
	"regexp"
	"strings"
)

const DataSource = "主流大模型列表.xlsx"

type rule struct {
	pattern *regexp.Regexp
	tokens  int
}

var rules = []rule{
	{regexp.MustCompile(`^(?:claude-)?opus-?4[.-]?8(?:-|$)|^claude-4[.-]?8-opus(?:-|$)`), 1_000_000},
	{regexp.MustCompile(`^(?:claude-)?opus-?4[.-]?7(?:-|$)|^claude-4[.-]?7-opus(?:-|$)`), 1_000_000},
	{regexp.MustCompile(`^(?:claude-)?sonnet-?4[.-]?6(?:-|$)|^claude-4[.-]?6-sonnet(?:-|$)`), 1_000_000},
	{regexp.MustCompile(`^(?:claude-)?sonnet-?5(?:-|$)|^claude-5-sonnet(?:-|$)`), 1_000_000},
	{regexp.MustCompile(`^gpt-?5[.-]?6(?:-|$)`), 1_000_000},
	{regexp.MustCompile(`^gpt-?4o(?:-|$)`), 128_000},
	{regexp.MustCompile(`^grok-?4[.-]?5(?:-|$)`), 500_000},
	{regexp.MustCompile(`^grok-?4[.-]?(?:3|20)(?:-|$)`), 1_000_000},
	{regexp.MustCompile(`^qwen-?3[.-]?8-max-preview(?:-|$)`), 1_000_000},
	{regexp.MustCompile(`^qwen-?3[.-]?7-max(?:-|$)`), 1_000_000},
	{regexp.MustCompile(`^deepseek-?v?4-flash(?:-|$)`), 1_000_000},
	{regexp.MustCompile(`^deepseek-?v?4-pro(?:-|$)`), 1_000_000},
	{regexp.MustCompile(`^kimi-?k?2[.-]?6(?:-|$)`), 256_000},
	// Excel 中 GLM-5.2 为 200K-1M，自动配置采用保守下限。
	{regexp.MustCompile(`^glm-?5[.-]?2(?:-|$)`), 200_000},
}

func WindowTokens(modelID string) int {
	normalized := normalizeModelID(modelID)
	for _, item := range rules {
		if item.pattern.MatchString(normalized) {
			return item.tokens
		}
	}
	return 0
}

func Resolve(modelID string, explicitTokens int) int {
	if explicitTokens > 0 {
		return explicitTokens
	}
	return WindowTokens(modelID)
}

func normalizeModelID(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.TrimPrefix(normalized, "models/")
	if index := strings.LastIndex(normalized, "/"); index >= 0 {
		normalized = normalized[index+1:]
	}
	normalized = strings.NewReplacer(" ", "-", "_", "-").Replace(normalized)
	return normalized
}
