// pricing.go 提供从模型渠道配置构建 historymetrics 价格条目快照的共享逻辑，
// 供统计页（bridge MetricsService）等费用估算统一口径：
// 手动配价 / catalog 探测价（adapter.Pricing）> 内置官方价 > 按币种均价估算。
// 内置价格仅运行时注入，不写入 config.yaml。
package config

import (
	"strings"

	"cursor/internal/historymetrics"
	"cursor/internal/modelcontext"
)

// PriceRatesFromAdapters 将当前配置的模型渠道价格映射为 historymetrics 价格条目快照。
// 手动配价 / catalog 探测价（adapter.Pricing）优先；未配置价格的渠道回退内置官方价，
// 再不行按币种取内置表均价估算。没有任何可用价格时跳过该渠道。
func PriceRatesFromAdapters(adapters []ModelAdapterConfig) []historymetrics.PriceRate {
	rates := make([]historymetrics.PriceRate, 0, len(adapters))
	for _, adapter := range adapters {
		pricing := adapter.Pricing
		if pricing != nil {
			rates = append(rates, historymetrics.PriceRate{
				Model:      adapter.ModelID,
				Provider:   adapter.Type,
				BaseURL:    adapter.BaseURL,
				Input:      pricing.Input,
				Output:     pricing.Output,
				CacheRead:  pricing.CacheRead,
				CacheWrite: pricing.CacheWrite,
				Currency:   pricing.Currency,
				Known:      pricing.Known,
				Source:     PricingSourceLabel(pricing.Source, pricing.Known),
			})
			continue
		}
		// adapter 未配置价格 -> 尝试内置官方价兜底。
		builtin := modelcontext.BuiltinPricingForAdapter(adapter.ModelID, adapter.SupplierID, adapter.Type, adapter.BaseURL)
		if builtin == nil {
			builtin = modelcontext.AverageBuiltinPricing(modelcontext.BuiltinPricingCurrencyForAdapter(adapter.SupplierID, adapter.Type, adapter.BaseURL))
			if builtin == nil {
				continue
			}
		}
		rates = append(rates, historymetrics.PriceRate{
			Model:      adapter.ModelID,
			Provider:   adapter.Type,
			BaseURL:    adapter.BaseURL,
			Input:      builtin.Input,
			Output:     builtin.Output,
			CacheRead:  builtin.CacheRead,
			CacheWrite: builtin.CacheWrite,
			Currency:   builtin.Currency,
			Known:      true,
			Source:     PricingSourceLabel(builtin.Source, true),
		})
	}
	return rates
}

// PricingSourceLabel 归一化价格来源标签：显式来源（official/catalog/manual）优先，
// 未标注来源时按是否已知标注 configured；未知且未标注返回空串。
func PricingSourceLabel(source string, known bool) string {
	if strings.TrimSpace(source) != "" {
		return strings.TrimSpace(source)
	}
	if known {
		return "configured"
	}
	return ""
}
