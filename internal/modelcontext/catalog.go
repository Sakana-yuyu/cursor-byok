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
	// Pricing 是该模型官方公布的 token 价格（每百万 token，USD）。
	// 仅在 adapter 未配置手动/catalog 价格时作为兜底使用。
	Pricing *BuiltinPricing `json:"-"`
}

// BuiltinPricing 表示模型官方公布的 token 单价（每百万 token）。
type BuiltinPricing struct {
	Input      *float64
	Output     *float64
	CacheRead  *float64
	CacheWrite *float64
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
		Capability{DisplayName: "Claude Opus 4.8", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_000, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricingCW(15, 75, 1.5, 18.75)},
	},
	{
		regexp.MustCompile(`^(?:claude-)?opus-?4[.-]?7(?:-|$)|^claude-4[.-]?7-opus(?:-|$)`),
		Capability{DisplayName: "Claude Opus 4.7", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_000, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricingCW(15, 75, 1.5, 18.75)},
	},
	{
		regexp.MustCompile(`^(?:claude-)?sonnet-?4[.-]?6(?:-|$)|^claude-4[.-]?6-sonnet(?:-|$)`),
		Capability{DisplayName: "Claude Sonnet 4.6", ContextWindowTokens: 1_000_000, MaxOutputTokens: 64_000, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricingCW(3, 15, 0.3, 3.75)},
	},
	{
		regexp.MustCompile(`^(?:claude-)?sonnet-?5(?:-|$)|^claude-5-sonnet(?:-|$)`),
		Capability{DisplayName: "Claude Sonnet 5", ContextWindowTokens: 1_000_000, MaxOutputTokens: 64_000, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricingCW(3, 15, 0.3, 3.75)},
	},
	// claude-3-5 系列
	{
		regexp.MustCompile(`^claude-3-5-sonnet`),
		Capability{DisplayName: "Claude 3.5 Sonnet", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingCW(3, 15, 0.3, 3.75)},
	},
	{
		regexp.MustCompile(`^claude-3-5-haiku`),
		Capability{DisplayName: "Claude 3.5 Haiku", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingCW(0.8, 4, 0.08, 1)},
	},
	// claude-3 系列兜底
	{
		regexp.MustCompile(`^claude-3`),
		Capability{DisplayName: "Claude 3", ContextWindowTokens: 200_000, MaxOutputTokens: 4_096, SupportsVision: true, SupportsTools: true, Pricing: pricingCW(0.8, 4, 0.08, 1)},
	},
	// claude 系列通用兜底
	{
		regexp.MustCompile(`^claude`),
		Capability{DisplayName: "Claude", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingCW(3, 15, 0.3, 3.75)},
	},

	// ─── GPT ───────────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^gpt-?5[.-]?6(?:-|$)`),
		Capability{DisplayName: "GPT-5.6", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricing(1.25, 10, 0.125)},
	},
	{
		regexp.MustCompile(`^gpt-?5(?:-|$)`),
		Capability{DisplayName: "GPT-5", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricing(1.25, 10, 0.125)},
	},
	{
		regexp.MustCompile(`^gpt-?4o-mini(?:-|$)`),
		Capability{DisplayName: "GPT-4o mini", ContextWindowTokens: 128_000, MaxOutputTokens: 16_384, SupportsVision: true, SupportsTools: true, Pricing: pricing(0.15, 0.6, 0.075)},
	},
	{
		regexp.MustCompile(`^gpt-?4o(?:-|$)`),
		Capability{DisplayName: "GPT-4o", ContextWindowTokens: 128_000, MaxOutputTokens: 16_384, SupportsVision: true, SupportsTools: true, Pricing: pricing(2.5, 10, 1.25)},
	},
	{
		regexp.MustCompile(`^o[13]-mini(?:-|$)`),
		Capability{DisplayName: "OpenAI o-mini", ContextWindowTokens: 128_000, MaxOutputTokens: 65_536, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricing(1.1, 4.4, 0.55)},
	},
	{
		regexp.MustCompile(`^o[134](?:-|$)`),
		Capability{DisplayName: "OpenAI o 系列", ContextWindowTokens: 200_000, MaxOutputTokens: 100_000, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricing(15, 60, 1.875)},
	},

	// ─── Gemini ────────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^gemini-2[.-]?5-pro`),
		Capability{DisplayName: "Gemini 2.5 Pro", ContextWindowTokens: 1_000_000, MaxOutputTokens: 65_536, SupportsVision: true, SupportsAudio: true, SupportsTools: true, SupportsThinking: true, Pricing: pricing(1.25, 10, 0.3125)},
	},
	{
		regexp.MustCompile(`^gemini-2[.-]?5-flash`),
		Capability{DisplayName: "Gemini 2.5 Flash", ContextWindowTokens: 1_000_000, MaxOutputTokens: 65_536, SupportsVision: true, SupportsAudio: true, SupportsTools: true, SupportsThinking: true, Pricing: pricing(0.3, 2.5, 0.075)},
	},
	{
		regexp.MustCompile(`^gemini-2[.-]?0`),
		Capability{DisplayName: "Gemini 2.0", ContextWindowTokens: 1_000_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsAudio: true, SupportsTools: true, Pricing: pricing(0.3, 2.5, 0.075)},
	},
	{
		regexp.MustCompile(`^gemini`),
		Capability{DisplayName: "Gemini", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricing(0.3, 2.5, 0.075)},
	},

	// ─── Grok ──────────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^grok-?4[.-]?5(?:-|$)`),
		Capability{DisplayName: "Grok 4.5", ContextWindowTokens: 500_000, MaxOutputTokens: 32_768, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricing(2, 6, 0.5)},
	},
	{
		regexp.MustCompile(`^grok-?4[.-]?(?:3|20)(?:-|$)`),
		Capability{DisplayName: "Grok 4.3/4.20", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricing(3, 15, 0.75)},
	},
	{
		regexp.MustCompile(`^grok-?4(?:-|$)`),
		Capability{DisplayName: "Grok 4", ContextWindowTokens: 256_000, MaxOutputTokens: 32_768, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricing(3, 15, 0.75)},
	},
	{
		regexp.MustCompile(`^grok`),
		Capability{DisplayName: "Grok", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricing(3, 15, 0.75)},
	},

	// ─── Qwen ──────────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^qwen-?3[.-]?8-max-preview(?:-|$)`),
		Capability{DisplayName: "Qwen3-8-Max-Preview", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: false, SupportsTools: true, SupportsThinking: true, Pricing: pricing(0.84, 2.52, 0.084)},
	},
	{
		regexp.MustCompile(`^qwen-?3[.-]?7-max(?:-|$)`),
		Capability{DisplayName: "Qwen3-7-Max", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: false, SupportsTools: true, SupportsThinking: true, Pricing: pricing(0.84, 2.52, 0.084)},
	},
	{
		regexp.MustCompile(`^qwen-?(?:vl|2-vl|2\.5-vl)`),
		Capability{DisplayName: "Qwen-VL", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricing(0.28, 0.84, 0.028)},
	},
	{
		regexp.MustCompile(`^qwen-?(?:max|plus|turbo|long)(?:-|$)`),
		Capability{DisplayName: "Qwen", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true, Pricing: pricing(0.84, 2.52, 0.084)},
	},
	{
		regexp.MustCompile(`^qwen`),
		Capability{DisplayName: "Qwen", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true, Pricing: pricing(0.3, 0.6, 0.03)},
	},

	// ─── DeepSeek ──────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^deepseek-?v?4-flash(?:-|$)`),
		Capability{DisplayName: "DeepSeek V4 Flash", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: false, SupportsTools: true, SupportsThinking: false, Pricing: pricing(0.14, 0.28, 0.014)},
	},
	{
		regexp.MustCompile(`^deepseek-?v?4-pro(?:-|$)`),
		Capability{DisplayName: "DeepSeek V4 Pro", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: false, SupportsTools: true, SupportsThinking: false, Pricing: pricing(0.27, 1.1, 0.027)},
	},
	{
		regexp.MustCompile(`^deepseek-?v?4(?:-|$)`),
		Capability{DisplayName: "DeepSeek V4", ContextWindowTokens: 1_000_000, MaxOutputTokens: 32_768, SupportsVision: false, SupportsTools: true, SupportsThinking: false, Pricing: pricing(0.27, 1.1, 0.027)},
	},
	{
		regexp.MustCompile(`^deepseek-?v?3(?:-|$)`),
		Capability{DisplayName: "DeepSeek V3", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true, Pricing: pricing(0.27, 1.1, 0.014)},
	},
	{
		regexp.MustCompile(`^deepseek-?r[12](?:-|$)`),
		Capability{DisplayName: "DeepSeek R 系列", ContextWindowTokens: 128_000, MaxOutputTokens: 32_768, SupportsVision: false, SupportsTools: true, SupportsThinking: true, Pricing: pricing(0.55, 2.19, 0.028)},
	},
	{
		regexp.MustCompile(`^deepseek`),
		Capability{DisplayName: "DeepSeek", ContextWindowTokens: 64_000, MaxOutputTokens: 4_096, SupportsVision: false, SupportsTools: true, Pricing: pricing(0.27, 1.1, 0.014)},
	},

	// ─── Kimi / Moonshot ───────────────────────────────────────────────────
	{
		regexp.MustCompile(`^kimi-?k?2[.-]?6(?:-|$)`),
		Capability{DisplayName: "Kimi K2.6", ContextWindowTokens: 256_000, MaxOutputTokens: 16_384, SupportsVision: false, SupportsTools: true, Pricing: pricing(0.6, 2.5, 0.06)},
	},
	{
		regexp.MustCompile(`^kimi-?k?2[.-]?7(?:-|$)`),
		Capability{DisplayName: "Kimi K2.7", ContextWindowTokens: 256_000, MaxOutputTokens: 4_096, SupportsVision: false, SupportsTools: true, Pricing: pricing(0.6, 2.5, 0.06)},
	},
	{
		regexp.MustCompile(`^kimi-?k[12](?:-|$)`),
		Capability{DisplayName: "Kimi K 系列", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true, Pricing: pricing(0.6, 2.5, 0.06)},
	},
	{
		regexp.MustCompile(`^moonshot`),
		Capability{DisplayName: "Moonshot", ContextWindowTokens: 128_000, MaxOutputTokens: 4_096, SupportsVision: false, SupportsTools: true, Pricing: pricing(0.6, 2.5, 0.06)},
	},
	{
		regexp.MustCompile(`^kimi`),
		Capability{DisplayName: "Kimi", ContextWindowTokens: 128_000, MaxOutputTokens: 4_096, SupportsVision: false, SupportsTools: true, Pricing: pricing(0.6, 2.5, 0.06)},
	},

	// ─── GLM ───────────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^glm-?5[.-]?2(?:-|$)`),
		// Excel 中 GLM-5.2 为 200K-1M，自动配置采用保守下限。
		Capability{DisplayName: "GLM-5.2", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricing(0.5, 2, 0.05)},
	},
	{
		regexp.MustCompile(`^glm-?4v`),
		Capability{DisplayName: "GLM-4V", ContextWindowTokens: 128_000, MaxOutputTokens: 4_096, SupportsVision: true, SupportsTools: true, Pricing: pricing(0.5, 2, 0.05)},
	},
	{
		regexp.MustCompile(`^glm`),
		Capability{DisplayName: "GLM", ContextWindowTokens: 128_000, MaxOutputTokens: 4_096, SupportsVision: false, SupportsTools: true, Pricing: pricing(0.5, 2, 0.05)},
	},

	// ─── MiMo ──────────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^mimo-?vl(?:-|$)`),
		Capability{DisplayName: "MiMo-VL", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricing(0.35, 0.35, 0.035)},
	},
	{
		regexp.MustCompile(`^mimo`),
		Capability{DisplayName: "MiMo", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true, SupportsThinking: true, Pricing: pricing(0.35, 0.35, 0.035)},
	},

	// ─── MiniMax ───────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^minimax-?(?:vl|vision)`),
		Capability{DisplayName: "MiniMax-VL", ContextWindowTokens: 256_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricing(1, 3, 0.1)},
	},
	{
		regexp.MustCompile(`^minimax`),
		Capability{DisplayName: "MiniMax", ContextWindowTokens: 256_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true, Pricing: pricing(1, 3, 0.1)},
	},

	// ─── StepFun ───────────────────────────────────────────────────────────
	{
		regexp.MustCompile(`^step-2`),
		Capability{DisplayName: "Step-2", ContextWindowTokens: 512_000, MaxOutputTokens: 16_384, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricing(0.7, 2.8, 0.07)},
	},
	{
		regexp.MustCompile(`^step-1[.-]?v(?:-|$)`),
		Capability{DisplayName: "Step-1V", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricing(0.7, 2.8, 0.07)},
	},
	{
		regexp.MustCompile(`^step-1`),
		Capability{DisplayName: "Step-1", ContextWindowTokens: 256_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true, Pricing: pricing(0.7, 2.8, 0.07)},
	},
	{
		regexp.MustCompile(`^step`),
		Capability{DisplayName: "StepFun", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true, Pricing: pricing(0.7, 2.8, 0.07)},
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

// MaxOutputTokens 根据模型 ID 返回该模型允许的最大输出 token 数。
// 未知模型返回 0，调用方应保留自己的默认值。
func MaxOutputTokens(modelID string) int {
	c := Capabilities(modelID)
	if c == nil {
		return 0
	}
	return c.MaxOutputTokens
}

// BuiltinPricingFor 返回模型官方公布的 token 单价（每百万 token）。
// 未知模型返回 nil。
func BuiltinPricingFor(modelID string) *BuiltinPricing {
	c := Capabilities(modelID)
	if c == nil {
		return nil
	}
	return c.Pricing
}

// p 是构造 *float64 的简写辅助函数。
func p(v float64) *float64 { return &v }

// pricing 构造 BuiltinPricing 的简写辅助函数。
func pricing(input, output, cacheRead float64) *BuiltinPricing {
	return &BuiltinPricing{Input: p(input), Output: p(output), CacheRead: p(cacheRead)}
}

// pricingCW 同上但带 cacheWrite。
func pricingCW(input, output, cacheRead, cacheWrite float64) *BuiltinPricing {
	return &BuiltinPricing{Input: p(input), Output: p(output), CacheRead: p(cacheRead), CacheWrite: p(cacheWrite)}
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
