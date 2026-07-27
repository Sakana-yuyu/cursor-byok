package modelcontext

import (
	"regexp"
	"strings"
)

const DataSource = "主流大模型列表.xlsx"

// Capability 表示一个已知模型的能力元数据。
type Capability struct {
	DisplayName         string
	ContextWindowTokens int
	MaxOutputTokens     int
	SupportsVision      bool
	SupportsAudio       bool
	SupportsTools       bool
	SupportsThinking    bool
}

type capabilityRule struct {
	pattern    *regexp.Regexp
	capability Capability
}

// capabilityRules 按模型 ID 前缀/正则匹配，条目越靠前优先级越高。
var capabilityRules = []capabilityRule{
	// ─── Claude ────────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^(?:claude-)?opus-?4[.-]?8(?:-|$)|^claude-4[.-]?8-opus(?:-|$)`),
		Capability{DisplayName: "Claude Opus 4.8", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_000, SupportsVision: true, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^(?:claude-)?opus-?4[.-]?7(?:-|$)|^claude-4[.-]?7-opus(?:-|$)`),
		Capability{DisplayName: "Claude Opus 4.7", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_000, SupportsVision: true, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^(?:claude-)?sonnet-?4[.-]?6(?:-|$)|^claude-4[.-]?6-sonnet(?:-|$)`),
		Capability{DisplayName: "Claude Sonnet 4.6", ContextWindowTokens: 1_000_000, MaxOutputTokens: 64_000, SupportsVision: true, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^(?:claude-)?sonnet-?5(?:-|$)|^claude-5-sonnet(?:-|$)`),
		Capability{DisplayName: "Claude Sonnet 5", ContextWindowTokens: 1_000_000, MaxOutputTokens: 64_000, SupportsVision: true, SupportsTools: true, SupportsThinking: true},
	},
	// claude-3-5 系列
	{
		regexp.MustCompile(`^claude-3-5-sonnet`),
		Capability{DisplayName: "Claude 3.5 Sonnet", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^claude-3-5-haiku`),
		Capability{DisplayName: "Claude 3.5 Haiku", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true},
	},
	// claude-3 系列兜底
	{
		regexp.MustCompile(`^claude-3`),
		Capability{DisplayName: "Claude 3", ContextWindowTokens: 200_000, MaxOutputTokens: 4_096, SupportsVision: true, SupportsTools: true},
	},
	// claude 系列通用兜底
	{
		regexp.MustCompile(`^claude`),
		Capability{DisplayName: "Claude", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true},
	},

	// ─── GPT ───────────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^gpt-?5[.-]?6(?:-|$)`),
		Capability{DisplayName: "GPT-5.6", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: true, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^gpt-?5(?:-|$)`),
		Capability{DisplayName: "GPT-5", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: true, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^gpt-?4o-mini(?:-|$)`),
		Capability{DisplayName: "GPT-4o mini", ContextWindowTokens: 128_000, MaxOutputTokens: 16_384, SupportsVision: true, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^gpt-?4o(?:-|$)`),
		Capability{DisplayName: "GPT-4o", ContextWindowTokens: 128_000, MaxOutputTokens: 16_384, SupportsVision: true, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^o[13]-mini(?:-|$)`),
		Capability{DisplayName: "OpenAI o-mini", ContextWindowTokens: 128_000, MaxOutputTokens: 65_536, SupportsVision: true, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^o[134](?:-|$)`),
		Capability{DisplayName: "OpenAI o 系列", ContextWindowTokens: 200_000, MaxOutputTokens: 100_000, SupportsVision: true, SupportsTools: true, SupportsThinking: true},
	},

	// ─── Gemini ────────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^gemini-2[.-]?5-pro`),
		Capability{DisplayName: "Gemini 2.5 Pro", ContextWindowTokens: 1_000_000, MaxOutputTokens: 65_536, SupportsVision: true, SupportsAudio: true, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^gemini-2[.-]?5-flash`),
		Capability{DisplayName: "Gemini 2.5 Flash", ContextWindowTokens: 1_000_000, MaxOutputTokens: 65_536, SupportsVision: true, SupportsAudio: true, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^gemini-2[.-]?0`),
		Capability{DisplayName: "Gemini 2.0", ContextWindowTokens: 1_000_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsAudio: true, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^gemini`),
		Capability{DisplayName: "Gemini", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true},
	},

	// ─── Grok ──────────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^grok-?4[.-]?5(?:-|$)`),
		Capability{DisplayName: "Grok 4.5", ContextWindowTokens: 500_000, MaxOutputTokens: 32_768, SupportsVision: true, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^grok-?4[.-]?(?:3|20)(?:-|$)`),
		Capability{DisplayName: "Grok 4.3/4.20", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: true, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^grok-?4(?:-|$)`),
		Capability{DisplayName: "Grok 4", ContextWindowTokens: 256_000, MaxOutputTokens: 32_768, SupportsVision: true, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^grok`),
		Capability{DisplayName: "Grok", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true},
	},

	// ─── Qwen ──────────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^qwen-?3[.-]?8-max-preview(?:-|$)`),
		Capability{DisplayName: "Qwen3-8-Max-Preview", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: false, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^qwen-?3[.-]?7-max(?:-|$)`),
		Capability{DisplayName: "Qwen3-7-Max", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: false, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^qwen-?(?:vl|2-vl|2\.5-vl)`),
		Capability{DisplayName: "Qwen-VL", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^qwen-?(?:max|plus|turbo|long)(?:-|$)`),
		Capability{DisplayName: "Qwen", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^qwen`),
		Capability{DisplayName: "Qwen", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true},
	},

	// ─── DeepSeek ──────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^deepseek-?v?4-flash(?:-|$)`),
		Capability{DisplayName: "DeepSeek V4 Flash", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: false, SupportsTools: true, SupportsThinking: false},
	},
	{
		regexp.MustCompile(`^deepseek-?v?4-pro(?:-|$)`),
		Capability{DisplayName: "DeepSeek V4 Pro", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: false, SupportsTools: true, SupportsThinking: false},
	},
	{
		regexp.MustCompile(`^deepseek-?v?4(?:-|$)`),
		Capability{DisplayName: "DeepSeek V4", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: false, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^deepseek-?v?3(?:-|$)`),
		Capability{DisplayName: "DeepSeek V3", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^deepseek-?r[12](?:-|$)`),
		Capability{DisplayName: "DeepSeek R 系列", ContextWindowTokens: 128_000, MaxOutputTokens: 32_768, SupportsVision: false, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^deepseek`),
		Capability{DisplayName: "DeepSeek", ContextWindowTokens: 64_000, MaxOutputTokens: 4_096, SupportsVision: false, SupportsTools: true},
	},

	// ─── Kimi / Moonshot ───────────────────────────────────────────────────
	{
		regexp.MustCompile(`^kimi-?k?2[.-]?6(?:-|$)`),
		Capability{DisplayName: "Kimi K2.6", ContextWindowTokens: 256_000, MaxOutputTokens: 16_384, SupportsVision: false, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^kimi-?k[12](?:-|$)`),
		Capability{DisplayName: "Kimi K 系列", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^moonshot`),
		Capability{DisplayName: "Moonshot", ContextWindowTokens: 128_000, MaxOutputTokens: 4_096, SupportsVision: false, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^kimi`),
		Capability{DisplayName: "Kimi", ContextWindowTokens: 128_000, MaxOutputTokens: 4_096, SupportsVision: false, SupportsTools: true},
	},

	// ─── GLM ───────────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^glm-?5[.-]?2(?:-|$)`),
		// Excel 中 GLM-5.2 为 200K-1M，自动配置采用保守下限。
		Capability{DisplayName: "GLM-5.2", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^glm-?4v`),
		Capability{DisplayName: "GLM-4V", ContextWindowTokens: 128_000, MaxOutputTokens: 4_096, SupportsVision: true, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^glm`),
		Capability{DisplayName: "GLM", ContextWindowTokens: 128_000, MaxOutputTokens: 4_096, SupportsVision: false, SupportsTools: true},
	},

	// ─── MiMo ──────────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^mimo-?vl(?:-|$)`),
		Capability{DisplayName: "MiMo-VL", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^mimo`),
		Capability{DisplayName: "MiMo", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true, SupportsThinking: true},
	},

	// ─── MiniMax ───────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^minimax-?(?:vl|vision)`),
		Capability{DisplayName: "MiniMax-VL", ContextWindowTokens: 256_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^minimax`),
		Capability{DisplayName: "MiniMax", ContextWindowTokens: 256_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true},
	},

	// ─── StepFun ───────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^step-2`),
		Capability{DisplayName: "Step-2", ContextWindowTokens: 512_000, MaxOutputTokens: 16_384, SupportsVision: true, SupportsTools: true, SupportsThinking: true},
	},
	{
		regexp.MustCompile(`^step-1[.-]?v(?:-|$)`),
		Capability{DisplayName: "Step-1V", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^step-1`),
		Capability{DisplayName: "Step-1", ContextWindowTokens: 256_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true},
	},
	{
		regexp.MustCompile(`^step`),
		Capability{DisplayName: "StepFun", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true},
	},
}

// Capabilities 根据模型 ID 返回能力元数据；未知模型返回 nil。
func Capabilities(modelID string) *Capability {
	normalized := normalizeModelID(modelID)
	if normalized == "" {
		return nil
	}
	for i := range capabilityRules {
		if capabilityRules[i].pattern.MatchString(normalized) {
			c := capabilityRules[i].capability
			return &c
		}
	}
	return nil
}

// SupportsVision 报告模型是否支持图片输入。
// 返回 nil 表示未知（保守策略：调用方应默认保留图片）。
func SupportsVision(modelID string) *bool {
	c := Capabilities(modelID)
	if c == nil {
		return nil
	}
	v := c.SupportsVision
	return &v
}

// WindowTokens 根据模型 ID 返回上下文窗口大小；未知模型返回 0。
// 保留向后兼容。
func WindowTokens(modelID string) int {
	c := Capabilities(normalizeModelID(modelID))
	if c == nil {
		return 0
	}
	return c.ContextWindowTokens
}

// Resolve 优先使用 explicitTokens，否则从目录推断。
// 保留向后兼容。
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
