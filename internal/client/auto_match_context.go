// auto_match_context.go 实现自动为所有已存储模型适配器配对正确上下文窗口（contextWindowTokens）的能力。
//
// 配对策略（与用户确认一致）：
//   - 目录命中（modelcontext.Capabilities 知道该模型）：仅下调不覆盖——用户主动设置更小值
//     （如把 1M 限制为 200K 控制上下文/费用）时保留用户设置，避免自动配对吞掉手动配置；
//     仅在用户值缺失（<=0）或大于目录真实窗口时收敛到目录值（修正误填过大值，根治 context_length_exceeded）。
//   - 目录未命中（中转自定义模型，如 gpt-5.6-luna）：保留原值，并探测 provider 的 /models
//     接口，若能拿到该模型的真实窗口则回填；拿不到则保持不变。
//
// 受 config.yaml 的 autoMatchContextWindow 开关控制；启动时自动执行（开关开启时），
// 也可由前端「一键自动配对」按钮手动触发。MaxMode（请求级开关）继续走目录最大值，
// 本功能保证目录值准确，因此 MaxMode 自然取到正确最大值。
package client

import (
	"context"
	"strings"

	"cursor/internal/backend/server/config"
	"cursor/internal/logger"
	"cursor/internal/modelcontext"
)

// AutoMatchResult 是一次自动配对上下文窗口的汇总结果，供前端展示与日志记录。
type AutoMatchResult struct {
	// Enabled 表示是否实际执行了配对（开关关闭且未强制时为 false）。
	Enabled bool `json:"enabled"`
	// SwitchEnabled 表示 config 的 autoMatchContextWindow 开关当前是否为开启状态。
	// force 触发时即使开关关闭也会执行对齐，此时 Enabled=true 而 SwitchEnabled=false，
	// 前端据此提示「开关关闭但本次为手动强制对齐」。
	SwitchEnabled bool `json:"switchEnabled"`
	// Changed 表示是否有任何适配器被改动（决定是否落盘）。
	Changed bool `json:"changed"`
	// Total 表示参与判定的适配器总数。
	Total int `json:"total"`
	// FromCatalog 表示由内置目录命中并覆盖的适配器数量。
	FromCatalog int `json:"fromCatalog"`
	// FromProbe 表示目录未命中、但通过探测 provider /models 拿到窗口并回填的适配器数量。
	FromProbe int `json:"fromProbe"`
	// Unchanged 表示最终未改动（目录未命中且探测也未拿到窗口）的适配器数量。
	Unchanged int `json:"unchanged"`
	// Details 逐条记录每个适配器的配对结果，便于前端展示与排查。
	Details []AutoMatchDetail `json:"details,omitempty"`
}

// AutoMatchDetail 描述单个适配器的配对结果。
type AutoMatchDetail struct {
	// DisplayName 是适配器显示名，便于人工识别。
	DisplayName string `json:"displayName"`
	// ModelID 是模型标识。
	ModelID string `json:"modelID"`
	// Source 是配对来源：catalog | probe | unchanged。
	Source string `json:"source"`
	// Before 是配对前的 contextWindowTokens。
	Before int `json:"before"`
	// After 是配对后的 contextWindowTokens（未改动时与 Before 相同）。
	After int `json:"after"`
}

// AutoMatchContextWindows 执行一次自动配对：目录命中仅下调（用户手动设置的更小窗口保留），
// 目录未命中则探测 provider /models 回填。
// force=true 时无视 config.AutoMatchContextWindow 开关强制执行（供「一键诊断优化」手动触发）；
// force=false 时受开关控制，开关关闭则返回 Enabled=false。仅当有改动时才会落盘（SaveUserConfig），避免无谓写盘。
func (s *ProxyService) AutoMatchContextWindows(ctx context.Context, force bool) (AutoMatchResult, error) {
	if s == nil {
		return AutoMatchResult{}, nil
	}
	result := AutoMatchResult{Enabled: true}
	cfg, err := s.LoadUserConfig()
	if err != nil {
		return result, err
	}
	result.SwitchEnabled = cfg.AutoMatchContextWindow
	if !cfg.AutoMatchContextWindow && !force {
		// 开关关闭且未强制：不执行，但返回明确的「未启用」结果，便于前端提示。
		return AutoMatchResult{Enabled: false, SwitchEnabled: false, Total: len(cfg.ModelAdapters)}, nil
	}

	adapters := cfg.ModelAdapters
	result.Total = len(adapters)
	if result.Total == 0 {
		return result, nil
	}

	// 第一遍：目录命中直接覆盖；目录未命中的收集起来，按 (type, baseURL, apiKey) 分组待探测。
	type probeBucket struct {
		indices []int // 在 adapters 中的下标
	}
	buckets := make(map[string]*probeBucket)
	bucketOrder := make([]string, 0) // 保持稳定的探测顺序
	for i := range adapters {
		modelID := strings.TrimSpace(adapters[i].ModelID)
		before := adapters[i].ContextWindowTokens
		detail := AutoMatchDetail{
			DisplayName: strings.TrimSpace(adapters[i].DisplayName),
			ModelID:     modelID,
			Source:      "unchanged",
			Before:      before,
			After:       before,
		}
		if cap := modelcontext.Capabilities(modelID); cap != nil && cap.ContextWindowTokens > 0 {
			// 目录命中：仅下调不覆盖。用户主动设置更小值（如限制上下文/控制费用）时
			// 保留用户设置；仅在用户值缺失（<=0）或大于目录真实窗口时收敛到目录值，
			// 防止误填过大值触发 context_length_exceeded。
			if adapters[i].ContextWindowTokens <= 0 || cap.ContextWindowTokens < adapters[i].ContextWindowTokens {
				adapters[i].ContextWindowTokens = cap.ContextWindowTokens
				detail.After = cap.ContextWindowTokens
				detail.Source = "catalog"
				result.FromCatalog++
			}
		} else {
			// 目录未命中：放入待探测桶，稍后由 provider /models 兜底。
			key := probeBucketKey(adapters[i])
			if _, ok := buckets[key]; !ok {
				buckets[key] = &probeBucket{}
				bucketOrder = append(bucketOrder, key)
			}
			buckets[key].indices = append(buckets[key].indices, i)
		}
		result.Details = append(result.Details, detail)
	}

	// 第二遍：对每个分组探测一次 provider /models，按模型 ID 精确匹配回填。
	for _, key := range bucketOrder {
		bucket := buckets[key]
		if len(bucket.indices) == 0 {
			continue
		}
		representative := adapters[bucket.indices[0]]
		catalog, err := s.fetchCatalogForAutoMatch(ctx, representative)
		if err != nil {
			// 探测失败不阻断整体流程：这些适配器保持原值（unchanged）。
			logger.Errorf("auto match context window: probe failed group_key=%s error=%v", key, err)
			continue
		}
		// 建立模型 ID → contextWindowTokens / pricing 的快速查找（大小写不敏感、去 models/ 前缀）。
		windowByID := make(map[string]int, len(catalog.Models))
		pricingByID := make(map[string]*ModelPricing, len(catalog.Models))
		for _, item := range catalog.Models {
			id := normalizeModelIDForMatch(item.ID)
			if id == "" {
				continue
			}
			if item.ContextWindowTokens > 0 {
				if _, exists := windowByID[id]; !exists {
					windowByID[id] = item.ContextWindowTokens
				}
			}
			if item.Pricing != nil {
				if _, exists := pricingByID[id]; !exists {
					pricingByID[id] = item.Pricing
				}
			}
		}
		for _, idx := range bucket.indices {
			normalizedID := normalizeModelIDForMatch(adapters[idx].ModelID)
			window, ok := windowByID[normalizedID]
			if ok && window > 0 {
				adapters[idx].ContextWindowTokens = window
				result.Details[idx].After = window
				result.Details[idx].Source = "probe"
				result.FromProbe++
			}
			// 中转站探测到价格且 adapter 当前无手动价格 → 回填。
			if adapters[idx].Pricing == nil {
				if probed, found := pricingByID[normalizedID]; found && probed != nil {
					adapters[idx].Pricing = convertClientPricingToConfig(probed)
				}
			}
		}
	}

	// 汇总：统计未改动数量、是否有变化（以值是否变化为准）。
	// FromCatalog/FromProbe 计的是「来源」，与「值是否变化」可能不完全一致
	// （例如目录命中但原值恰好等于目录值），因此 Changed/Unchanged 以 Before==After 为准。
	for i := range result.Details {
		if result.Details[i].Before == result.Details[i].After {
			result.Unchanged++
		} else {
			result.Changed = true
		}
	}

	// 仅当有变化时落盘，避免无谓写盘与热加载抖动。
	if !result.Changed {
		return result, nil
	}
	cfg.ModelAdapters = adapters
	if err := s.SaveUserConfig(cfg); err != nil {
		return result, err
	}
	logger.Infof("auto match context window: matched total=%d from_catalog=%d from_probe=%d unchanged=%d",
		result.Total, result.FromCatalog, result.FromProbe, result.Unchanged)
	return result, nil
}

// probeBucketKey 生成分组键：(type, baseURL, apiKey)。
// 同一渠道下的多个模型只需探测一次 /models，复用 FetchModelCatalog 的 TTL 缓存。
func probeBucketKey(adapter config.ModelAdapterConfig) string {
	typeName := strings.ToLower(strings.TrimSpace(adapter.Type))
	baseURL := strings.TrimSpace(adapter.BaseURL)
	apiKey := strings.TrimSpace(adapter.APIKey)
	return typeName + "|" + baseURL + "|" + apiKey
}

// fetchCatalogForAutoMatch 用适配器的连接参数构造一次 /models 拉取请求。
// 复用 ProxyService.FetchModelCatalog，自动命中进程内 TTL 缓存。
func (s *ProxyService) fetchCatalogForAutoMatch(ctx context.Context, adapter config.ModelAdapterConfig) (ModelCatalogResult, error) {
	_ = ctx
	return s.FetchModelCatalog(ModelCatalogRequest{
		Type:                 adapter.Type,
		BaseURL:              adapter.BaseURL,
		APIKey:               adapter.APIKey,
		CustomHeadersEnabled: adapter.CustomHeadersEnabled,
		CustomHeadersJSON:    adapter.CustomHeadersJSON,
	})
}

// convertClientPricingToConfig 把 client.ModelPricing（探测结果）转为 config.ModelPricing（持久化结构）。
// 标记 Source="catalog"、Known=true，让 priceRatesFromAdapters 能识别为探测价格。
func convertClientPricingToConfig(src *ModelPricing) *config.ModelPricing {
	if src == nil {
		return nil
	}
	dst := &config.ModelPricing{
		Input:      src.Input,
		Output:     src.Output,
		CacheRead:  src.CacheRead,
		CacheWrite: src.CacheWrite,
		Currency:   src.Currency,
		Known:      true,
		Source:     "catalog",
	}
	if strings.TrimSpace(dst.Currency) == "" {
		dst.Currency = "USD"
	}
	return dst
}

// normalizeModelIDForMatch 把模型 ID 归一为用于精确匹配的形式：
// 去前后空白、转小写、去 models/ 前缀、取末尾段（兼容 owner/model 写法）。
func normalizeModelIDForMatch(modelID string) string {
	normalized := strings.ToLower(strings.TrimSpace(modelID))
	normalized = strings.TrimPrefix(normalized, "models/")
	if index := strings.LastIndex(normalized, "/"); index >= 0 {
		normalized = normalized[index+1:]
	}
	return normalized
}
