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
	// Pricing 是该模型官方公布的 token 价格（每百万 token）。
	// 仅在 adapter 未配置手动/catalog 价格时作为兜底使用。
	Pricing *BuiltinPricing `json:"-"`
}

// BuiltinPricing 表示模型官方公布的 token 单价（每百万 token）。
type BuiltinPricing struct {
	Input      *float64
	Output     *float64
	CacheRead  *float64
	CacheWrite *float64
	Currency   string
	Source     string
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
		Capability{DisplayName: "GLM-5.2", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricingUSD(1.4, 4.4, 0.26)},
	},
	{
		regexp.MustCompile(`^glm-?5[.-]?1(?:-|$)`),
		Capability{DisplayName: "GLM-5.1", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, SupportsThinking: true, Pricing: pricingUSD(1.4, 4.4, 0.26)},
	},
	{
		regexp.MustCompile(`^glm-?5-turbo(?:-|$)`),
		Capability{DisplayName: "GLM-5-Turbo", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingUSD(1.2, 4.0, 0.24)},
	},
	{
		regexp.MustCompile(`^glm-?5(?:-|$)`),
		Capability{DisplayName: "GLM-5", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingUSD(1.0, 3.2, 0.2)},
	},
	{
		regexp.MustCompile(`^glm-?4[.-]?7-flashx(?:-|$)`),
		Capability{DisplayName: "GLM-4.7-FlashX", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingUSD(0.07, 0.4, 0.01)},
	},
	{
		regexp.MustCompile(`^glm-?4[.-]?7(?:-|$)`),
		Capability{DisplayName: "GLM-4.7", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingUSD(0.6, 2.2, 0.11)},
	},
	{
		regexp.MustCompile(`^glm-?4[.-]?6(?:-|$)`),
		Capability{DisplayName: "GLM-4.6", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingUSD(0.6, 2.2, 0.11)},
	},
	{
		regexp.MustCompile(`^glm-?4[.-]?5-airx(?:-|$)`),
		Capability{DisplayName: "GLM-4.5-AirX", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingUSD(1.1, 4.5, 0.22)},
	},
	{
		regexp.MustCompile(`^glm-?4[.-]?5-air(?:-|$)`),
		Capability{DisplayName: "GLM-4.5-Air", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingUSD(0.2, 1.1, 0.03)},
	},
	{
		regexp.MustCompile(`^glm-?4[.-]?5-x(?:-|$)`),
		Capability{DisplayName: "GLM-4.5-X", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingUSD(2.2, 8.9, 0.45)},
	},
	{
		regexp.MustCompile(`^glm-?4[.-]?5(?:-|$)`),
		Capability{DisplayName: "GLM-4.5", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingUSD(0.6, 2.2, 0.11)},
	},
	{
		regexp.MustCompile(`^glm-?4-32b-0414-128k(?:-|$)`),
		Capability{DisplayName: "GLM-4-32B-0414-128K", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true, Pricing: pricingUSDNoCache(0.1, 0.1)},
	},
	{
		regexp.MustCompile(`^glm-?4[.-]?7-flash(?:-|$)|^glm-?4[.-]?5-flash(?:-|$)`),
		Capability{DisplayName: "GLM Flash", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true, Pricing: pricingUSD(0, 0, 0)},
	},
	{
		regexp.MustCompile(`^glm-?4v`),
		Capability{DisplayName: "GLM-4V", ContextWindowTokens: 128_000, MaxOutputTokens: 4_096, SupportsVision: true, SupportsTools: true, Pricing: pricingUSD(0.3, 0.9, 0.05)},
	},
	{
		regexp.MustCompile(`^glm-?5v-turbo(?:-|$)`),
		Capability{DisplayName: "GLM-5V-Turbo", ContextWindowTokens: 200_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingUSD(1.2, 4.0, 0.24)},
	},
	{
		regexp.MustCompile(`^glm-?4[.-]?6v-flashx(?:-|$)`),
		Capability{DisplayName: "GLM-4.6V-FlashX", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingUSD(0.04, 0.4, 0.004)},
	},
	{
		regexp.MustCompile(`^glm-?4[.-]?6v(?:-|$)`),
		Capability{DisplayName: "GLM-4.6V", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingUSD(0.3, 0.9, 0.05)},
	},
	{
		regexp.MustCompile(`^glm-?4[.-]?5v(?:-|$)`),
		Capability{DisplayName: "GLM-4.5V", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingUSD(0.6, 1.8, 0.11)},
	},
	{
		regexp.MustCompile(`^glm-?ocr(?:-|$)`),
		Capability{DisplayName: "GLM-OCR", ContextWindowTokens: 128_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingUSDNoCache(0.03, 0.03)},
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
		Capability{DisplayName: "MiniMax-VL", ContextWindowTokens: 256_000, MaxOutputTokens: 8_192, SupportsVision: true, SupportsTools: true, Pricing: pricingCNY(2.1, 8.4, 0.42, 2.625)},
	},
	{
		regexp.MustCompile(`^minimax-m3(?:-|$)`),
		// 默认采用官方标准档 <=512K；超 512K 请求需按官方分段价计费，当前 PriceRate 结构无法表达阈值。
		Capability{DisplayName: "MiniMax-M3", ContextWindowTokens: 1_000_000, MaxOutputTokens: 16_384, SupportsVision: false, SupportsTools: true, Pricing: pricingCNYNoWrite(2.1, 8.4, 0.42)},
	},
	{
		regexp.MustCompile(`^minimax-m2[.-]?7-highspeed(?:-|$)`),
		Capability{DisplayName: "MiniMax-M2.7-highspeed", ContextWindowTokens: 1_000_000, MaxOutputTokens: 16_384, SupportsVision: false, SupportsTools: true, Pricing: pricingCNY(4.2, 16.8, 0.42, 2.625)},
	},
	{
		regexp.MustCompile(`^minimax-m2[.-]?7(?:-|$)`),
		Capability{DisplayName: "MiniMax-M2.7", ContextWindowTokens: 1_000_000, MaxOutputTokens: 16_384, SupportsVision: false, SupportsTools: true, Pricing: pricingCNY(2.1, 8.4, 0.42, 2.625)},
	},
	{
		regexp.MustCompile(`^minimax`),
		Capability{DisplayName: "MiniMax", ContextWindowTokens: 256_000, MaxOutputTokens: 8_192, SupportsVision: false, SupportsTools: true, Pricing: pricingCNY(2.1, 8.4, 0.21, 2.625)},
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

// BuiltinPricingForAdapter 返回供应商感知的官方价格。
func BuiltinPricingForAdapter(modelID, supplierID, provider, baseURL string) *BuiltinPricing {
	supplier := strings.ToLower(strings.TrimSpace(supplierID))
	provider = strings.ToLower(strings.TrimSpace(provider))
	base := strings.ToLower(strings.TrimSpace(baseURL))
	if supplier == "opencode_go" || strings.Contains(base, "opencode.ai/zen/go") {
		return clonePricing(opencodeGoBuiltinPricing(modelID))
	}
	if strings.Contains(base, "opencode.ai/zen") {
		return clonePricing(opencodeZenBuiltinPricing(modelID))
	}
	if supplier == "zhipu_glm" || supplier == "zhipu_glm_en" || supplier == "zhipu" || supplier == "zhipu_team" || strings.Contains(base, "bigmodel.cn") || strings.Contains(base, "api.z.ai") || strings.Contains(base, "z.ai") {
		return clonePricing(BuiltinPricingFor(modelID))
	}
	if supplier == "minimax" || supplier == "minimax_en" || strings.Contains(base, "minimaxi") || strings.Contains(base, "minimax.io") || strings.Contains(base, "minimax.com") {
		return clonePricing(BuiltinPricingFor(modelID))
	}
	if supplier == "volcengine" || supplier == "volcengine_agent" || supplier == "doubaoseed" || supplier == "byteplus" || strings.Contains(base, "volces.com") || strings.Contains(base, "bytepluses.com") {
		return clonePricing(volcengineBuiltinPricing(modelID))
	}
	if provider == "" {
		return clonePricing(BuiltinPricingFor(modelID))
	}
	return clonePricing(BuiltinPricingFor(modelID))
}

// BuiltinPricingCurrencyForAdapter 返回未命中具体模型价格时应使用的估算币种。
func BuiltinPricingCurrencyForAdapter(supplierID, provider, baseURL string) string {
	supplier := strings.ToLower(strings.TrimSpace(supplierID))
	base := strings.ToLower(strings.TrimSpace(baseURL))
	if supplier == "minimax" || supplier == "minimax_en" || strings.Contains(base, "minimaxi") || strings.Contains(base, "minimax.io") || strings.Contains(base, "minimax.com") {
		return "CNY"
	}
	if supplier == "volcengine" || supplier == "volcengine_agent" || supplier == "doubaoseed" || supplier == "byteplus" || strings.Contains(base, "volces.com") || strings.Contains(base, "bytepluses.com") {
		return "CNY"
	}
	if supplier == "zhipu_glm" || supplier == "zhipu_glm_en" || supplier == "zhipu" || supplier == "zhipu_team" || strings.Contains(base, "bigmodel.cn") || strings.Contains(base, "z.ai") {
		return "USD"
	}
	_ = provider
	return "USD"
}

// volcengineBuiltinPricing 只收录本轮从火山方舟官方价格表逐项核对的固定段价格。
// 其它模型的官方价格可能随输入长度分段或套餐变化，交由调用方使用 CNY 均价估算。
func volcengineBuiltinPricing(modelID string) *BuiltinPricing {
	normalized := normalizeModelID(modelID)
	switch {
	case strings.HasPrefix(normalized, "doubao-seed-2-1-pro") || strings.HasPrefix(normalized, "doubao-seed-2.1-pro"):
		return &BuiltinPricing{Input: p(3.0), Output: p(15.0), CacheRead: p(1.2), Currency: "CNY", Source: "official"}
	default:
		return nil
	}
}

// opencodeZenBuiltinPricing mirrors the current official pricing table at
// https://opencode.ai/docs/zen. Prices are USD per 1M tokens.
func opencodeZenBuiltinPricing(modelID string) *BuiltinPricing {
	switch normalized := normalizeModelID(modelID); normalized {
	case "big-pickle", "deepseek-v4-flash-free", "mimo-v2.5-free", "laguna-s-2.1-free", "ling-3.0-flash-free", "north-mini-code-free", "nemotron-3-ultra-free":
		return pricingUSD(0, 0, 0)
	case "minimax-m3", "minimax-m2.7", "minimax-m2.5":
		return pricingUSD(0.30, 1.20, 0.06)
	case "glm-5.2", "glm-5.1":
		return pricingUSD(1.40, 4.40, 0.26)
	case "glm-5":
		return pricingUSD(1.00, 3.20, 0.20)
	case "kimi-k2.7-code":
		return pricingUSD(0.95, 4.00, 0.19)
	case "kimi-k3":
		return pricingUSD(3.00, 15.00, 0.30)
	case "kimi-k2.6":
		return pricingUSD(0.95, 4.00, 0.16)
	case "kimi-k2.5":
		return pricingUSD(0.60, 3.00, 0.10)
	case "qwen3.7-max":
		return pricingCW(2.50, 7.50, 0.50, 3.125)
	case "qwen3.7-plus":
		return pricingCW(0.40, 1.60, 0.04, 0.50)
	case "qwen3.6-plus":
		return pricingCW(0.50, 3.00, 0.05, 0.625)
	case "qwen3.5-plus":
		return pricingCW(0.20, 1.20, 0.02, 0.25)
	case "deepseek-v4-pro":
		return pricingUSD(1.74, 3.48, 0.145)
	case "deepseek-v4-flash":
		return pricingUSD(0.14, 0.28, 0.028)
	case "claude-fable-5":
		return pricingCW(10.00, 50.00, 1.00, 12.50)
	case "claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-6", "claude-opus-4-5":
		return pricingCW(5.00, 25.00, 0.50, 6.25)
	case "claude-sonnet-5":
		return pricingCW(2.00, 10.00, 0.20, 2.50)
	case "claude-sonnet-4-6", "claude-sonnet-4-5":
		return pricingCW(3.00, 15.00, 0.30, 3.75)
	case "claude-haiku-4-5":
		return pricingCW(1.00, 5.00, 0.10, 1.25)
	case "gemini-3.6-flash":
		return pricingUSD(1.50, 7.50, 0.15)
	case "gemini-3.5-flash":
		return pricingUSD(1.50, 9.00, 0.15)
	case "gemini-3.5-flash-lite":
		return pricingUSD(0.30, 2.50, 0.03)
	case "gemini-3.1-pro":
		return pricingUSD(2.00, 12.00, 0.20)
	case "gemini-3-flash":
		return pricingUSD(0.50, 3.00, 0.05)
	case "grok-4.5":
		return pricingUSD(2.00, 6.00, 0.30)
	case "grok-build-0.1":
		return pricingUSD(1.00, 2.00, 0.20)
	case "gpt-5.6-sol":
		return pricingCW(5.00, 30.00, 0.50, 6.25)
	case "gpt-5.6-terra":
		return pricingCW(2.00, 12.00, 0.20, 2.50)
	case "gpt-5.6-luna":
		return pricingCW(0.20, 1.20, 0.02, 0.25)
	case "gpt-5.5":
		return pricingUSD(5.00, 30.00, 0.50)
	case "gpt-5.5-pro":
		return pricingUSD(30.00, 180.00, 30.00)
	case "gpt-5.4":
		return pricingUSD(2.50, 15.00, 0.25)
	case "gpt-5.4-pro":
		return pricingUSD(30.00, 180.00, 30.00)
	case "gpt-5.4-mini":
		return pricingUSD(0.75, 4.50, 0.075)
	case "gpt-5.4-nano":
		return pricingUSD(0.20, 1.25, 0.02)
	case "gpt-5.3-codex-spark", "gpt-5.3-codex", "gpt-5.2", "gpt-5.2-codex":
		return pricingUSD(1.75, 14.00, 0.175)
	case "gpt-5.1", "gpt-5.1-codex":
		return pricingUSD(1.07, 8.50, 0.107)
	case "gpt-5.1-codex-max":
		return pricingUSD(1.25, 10.00, 0.125)
	case "gpt-5.1-codex-mini":
		return pricingUSD(0.25, 2.00, 0.025)
	case "gpt-5", "gpt-5-codex":
		return pricingUSD(1.07, 8.50, 0.107)
	case "gpt-5-nano":
		return pricingUSD(0.05, 0.40, 0.005)
	default:
		return nil
	}
}

// opencodeGoBuiltinPricing mirrors the current official pricing table at
// https://opencode.ai/docs/go. Prices are USD per 1M tokens.
func opencodeGoBuiltinPricing(modelID string) *BuiltinPricing {
	switch normalized := normalizeModelID(modelID); normalized {
	case "grok-4.5":
		return pricingUSD(2.00, 6.00, 0.30)
	case "gpt-5.6-luna":
		return pricingCW(0.20, 1.20, 0.02, 0.25)
	case "glm-5.2", "glm-5.1":
		return pricingUSD(1.40, 4.40, 0.26)
	case "kimi-k3":
		return pricingUSD(3.00, 15.00, 0.30)
	case "kimi-k2.7-code":
		return pricingUSD(0.95, 4.00, 0.19)
	case "kimi-k2.6":
		return pricingUSD(0.95, 4.00, 0.16)
	case "mimo-v2.5":
		return pricingUSD(0.14, 0.28, 0.0028)
	case "mimo-v2.5-pro":
		return pricingUSD(0.435, 0.87, 0.003625)
	case "minimax-m3":
		return pricingUSD(0.30, 1.20, 0.06)
	case "minimax-m2.7", "minimax-m2.5":
		return pricingCW(0.30, 1.20, 0.06, 0.375)
	case "qwen3.7-max":
		return pricingCW(2.50, 7.50, 0.50, 3.125)
	case "qwen3.7-plus":
		return pricingCW(0.40, 1.60, 0.04, 0.50)
	case "qwen3.6-plus":
		return pricingCW(0.50, 3.00, 0.05, 0.625)
	case "deepseek-v4-pro":
		return pricingUSD(0.435, 0.87, 0.003625)
	case "deepseek-v4-flash":
		return pricingUSD(0.14, 0.28, 0.0028)
	case "hy3":
		return pricingUSD(0.14, 0.58, 0.035)
	default:
		return nil
	}
}

// AverageBuiltinPricing 返回内置官方目录中各价格字段的算术平均。
// 这是估算价，不是任何供应商的官方报价；按币种分别聚合。
func AverageBuiltinPricing(currency string) *BuiltinPricing {
	wanted := strings.ToUpper(strings.TrimSpace(currency))
	if wanted == "" {
		wanted = "USD"
	}
	var input, output, cacheRead, cacheWrite float64
	var inputN, outputN, cacheReadN, cacheWriteN int
	for _, rule := range capabilityRules {
		pricing := rule.capability.Pricing
		if pricing == nil || strings.ToUpper(strings.TrimSpace(pricing.Currency)) != wanted || pricing.Source == "average" {
			continue
		}
		if pricing.Input != nil {
			input += *pricing.Input
			inputN++
		}
		if pricing.Output != nil {
			output += *pricing.Output
			outputN++
		}
		if pricing.CacheRead != nil {
			cacheRead += *pricing.CacheRead
			cacheReadN++
		}
		if pricing.CacheWrite != nil {
			cacheWrite += *pricing.CacheWrite
			cacheWriteN++
		}
	}
	result := &BuiltinPricing{Currency: wanted, Source: "average"}
	if inputN > 0 {
		result.Input = p(input / float64(inputN))
	}
	if outputN > 0 {
		result.Output = p(output / float64(outputN))
	}
	if cacheReadN > 0 {
		result.CacheRead = p(cacheRead / float64(cacheReadN))
	}
	if cacheWriteN > 0 {
		result.CacheWrite = p(cacheWrite / float64(cacheWriteN))
	}
	if result.Input == nil && result.Output == nil && result.CacheRead == nil && result.CacheWrite == nil {
		return nil
	}
	return result
}

// p 是构造 *float64 的简写辅助函数。
func p(v float64) *float64 { return &v }

// pricing 构造 BuiltinPricing 的简写辅助函数。
func pricing(input, output, cacheRead float64) *BuiltinPricing {
	return pricingUSD(input, output, cacheRead)
}

// pricingCW 同上但带 cacheWrite。
func pricingCW(input, output, cacheRead, cacheWrite float64) *BuiltinPricing {
	return &BuiltinPricing{Input: p(input), Output: p(output), CacheRead: p(cacheRead), CacheWrite: p(cacheWrite), Currency: "USD", Source: "official"}
}

func pricingUSD(input, output, cacheRead float64) *BuiltinPricing {
	return &BuiltinPricing{Input: p(input), Output: p(output), CacheRead: p(cacheRead), Currency: "USD", Source: "official"}
}

func pricingUSDNoCache(input, output float64) *BuiltinPricing {
	return &BuiltinPricing{Input: p(input), Output: p(output), Currency: "USD", Source: "official"}
}

func pricingCNY(input, output, cacheRead, cacheWrite float64) *BuiltinPricing {
	return &BuiltinPricing{Input: p(input), Output: p(output), CacheRead: p(cacheRead), CacheWrite: p(cacheWrite), Currency: "CNY", Source: "official"}
}

func pricingCNYNoWrite(input, output, cacheRead float64) *BuiltinPricing {
	return &BuiltinPricing{Input: p(input), Output: p(output), CacheRead: p(cacheRead), Currency: "CNY", Source: "official"}
}

func clonePricing(input *BuiltinPricing) *BuiltinPricing {
	if input == nil {
		return nil
	}
	copy := *input
	return &copy
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
