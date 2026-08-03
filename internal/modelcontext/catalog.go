package modelcontext

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const DataSource = "主流大模型列表.xlsx"

//go:embed models.json
var modelsCatalogJSON []byte

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

// modelsCatalogDoc 是 models.json 的反序列化结构。
type modelsCatalogDoc struct {
	Rules []modelsCatalogRule `json:"rules"`
}

// modelsCatalogRule 是 models.json 中单条规则的反序列化结构（pattern 为字符串，启动时编译为正则）。
type modelsCatalogRule struct {
	Pattern            string                  `json:"pattern"`
	DisplayName        string                  `json:"displayName"`
	ContextWindowTokens int                    `json:"contextWindowTokens"`
	MaxOutputTokens    int                     `json:"maxOutputTokens"`
	SupportsVision     bool                    `json:"supportsVision"`
	SupportsAudio      bool                    `json:"supportsAudio"`
	SupportsTools      bool                    `json:"supportsTools"`
	SupportsThinking   bool                    `json:"supportsThinking"`
	Pricing            *modelsCatalogPricing   `json:"pricing,omitempty"`
}

// modelsCatalogPricing 是 models.json 中 pricing 子结构的反序列化结构。
// 字段使用指针以便区分「未设置」与「0」。
type modelsCatalogPricing struct {
	Input      *float64 `json:"input,omitempty"`
	Output     *float64 `json:"output,omitempty"`
	CacheRead  *float64 `json:"cacheRead,omitempty"`
	CacheWrite *float64 `json:"cacheWrite,omitempty"`
	Currency   string   `json:"currency,omitempty"`
	Source     string   `json:"source,omitempty"`
}

// capabilityRules 从嵌入的 models.json 加载，按数组顺序 first-match-wins。
// 启动时一次性编译 pattern 字符串为正则并转换为 Capability（含 Pricing 指针）。
var capabilityRules = mustLoadCapabilityRules()

// mustLoadCapabilityRules 在包初始化时从 models.json 解析并编译正则规则。
// 解析失败属于构建期数据错误，直接 panic（与原手写规则同等保证）。
func mustLoadCapabilityRules() []capabilityRule {
	var doc modelsCatalogDoc
	if err := json.Unmarshal(modelsCatalogJSON, &doc); err != nil {
		panic(fmt.Sprintf("modelcontext: failed to parse embedded models.json: %v", err))
	}
	rules := make([]capabilityRule, 0, len(doc.Rules))
	for _, raw := range doc.Rules {
		pattern, err := regexp.Compile(raw.Pattern)
		if err != nil {
			panic(fmt.Sprintf("modelcontext: invalid pattern %q in models.json: %v", raw.Pattern, err))
		}
		cap := Capability{
			DisplayName:         raw.DisplayName,
			ContextWindowTokens: raw.ContextWindowTokens,
			MaxOutputTokens:     raw.MaxOutputTokens,
			SupportsVision:      raw.SupportsVision,
			SupportsAudio:       raw.SupportsAudio,
			SupportsTools:       raw.SupportsTools,
			SupportsThinking:    raw.SupportsThinking,
		}
		if raw.Pricing != nil {
			cap.Pricing = &BuiltinPricing{
				Input:      raw.Pricing.Input,
				Output:     raw.Pricing.Output,
				CacheRead:  raw.Pricing.CacheRead,
				CacheWrite: raw.Pricing.CacheWrite,
				Currency:   raw.Pricing.Currency,
				Source:     raw.Pricing.Source,
			}
		}
		rules = append(rules, capabilityRule{pattern: pattern, capability: cap})
	}
	if len(rules) == 0 {
		panic("modelcontext: embedded models.json contains no rules")
	}
	return rules
}

// 注：原手写规则数组已迁移至 models.json（go:embed 加载）；以下 lookup 函数签名不变，调用方无感知。
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
// 返回 nil 表示未知（调用方按「保守不支持视觉」处理：不把图片上传给能力未知的
// 模型，由视觉委派/路径占位兜底，避免纯文本模型收到图片返回 400）。
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
