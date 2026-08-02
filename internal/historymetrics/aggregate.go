package historymetrics

import (
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	// KindProviderCall 表示一次上游模型调用的 usage 事件。
	KindProviderCall = "provider_call"
	// KindTurnFinalized 表示会话回合结束；token 可能与 provider_call 重复，不能用于 token 汇总。
	KindTurnFinalized = "turn_finalized"
)

// RangeSummary 表示某个时间范围内的 token 汇总。
type RangeSummary struct {
	RequestCount     int      `json:"requestCount"`
	InputTokens      int64    `json:"inputTokens"`
	OutputTokens     int64    `json:"outputTokens"`
	CacheReadTokens  int64    `json:"cacheReadTokens"`
	CacheWriteTokens int64    `json:"cacheWriteTokens"`
	TotalTokens      int64    `json:"totalTokens"`
	CacheRate        *float64 `json:"cacheRate"`
	// TurnsTotal/ValidTurnsTotal/InvalidTurnsTotal 来自 turn_finalized 事件聚合，
	// 与 provider_call 的 token 统计分离，避免重复计入 token。
	TurnsTotal        int `json:"turnsTotal"`
	ValidTurnsTotal   int `json:"validTurnsTotal"`
	InvalidTurnsTotal int `json:"invalidTurnsTotal"`
}

// TokenBucket 表示按固定时间粒度分桶后的 token 统计。
type TokenBucket struct {
	At               time.Time `json:"at"`
	InputTokens      int64     `json:"inputTokens"`
	OutputTokens     int64     `json:"outputTokens"`
	CacheReadTokens  int64     `json:"cacheReadTokens"`
	CacheWriteTokens int64     `json:"cacheWriteTokens"`
	TotalTokens      int64     `json:"totalTokens"`
	RequestCount     int       `json:"requestCount"`
	CacheRate        *float64  `json:"cacheRate"`
}

// IsProviderCall 判断事件是否可用于 token 统计。
func IsProviderCall(kind string) bool {
	kind = strings.TrimSpace(kind)
	return kind == "" || kind == KindProviderCall
}

// FilterProviderEvents 过滤出指定时间范围、模型的 provider_call 事件。
// start/end 为零值时表示不限制该端；model 为空表示全部模型。
func FilterProviderEvents(events []RequestMetric, start, end time.Time, model string) []RequestMetric {
	model = strings.TrimSpace(model)
	result := make([]RequestMetric, 0, len(events))
	for _, event := range events {
		if !IsProviderCall(event.Kind) {
			continue
		}
		if !start.IsZero() && event.At.Before(start) {
			continue
		}
		if !end.IsZero() && !event.At.Before(end) {
			continue
		}
		if model != "" && strings.TrimSpace(event.Model) != model {
			continue
		}
		result = append(result, event)
	}
	return result
}

// FilterTurnEvents 过滤出指定时间范围内的 turn_finalized 事件，供轮次聚合使用。
// turn_finalized 不带 model/provider，故不按 model 过滤；start/end 为零值表示不限制。
func FilterTurnEvents(events []RequestMetric, start, end time.Time) []RequestMetric {
	result := make([]RequestMetric, 0, len(events))
	for _, event := range events {
		if !IsTurnFinalized(event.Kind) {
			continue
		}
		if !start.IsZero() && event.At.Before(start) {
			continue
		}
		if !end.IsZero() && !event.At.Before(end) {
			continue
		}
		result = append(result, event)
	}
	return result
}

// SummarizeEvents 汇总 provider 事件的 token。
func SummarizeEvents(events []RequestMetric, includeCacheWrite bool) RangeSummary {
	var summary RangeSummary
	for _, event := range events {
		summary.RequestCount++
		summary.InputTokens += event.InputTokens
		summary.OutputTokens += event.OutputTokens
		summary.CacheReadTokens += event.CacheReadTokens
		summary.CacheWriteTokens += event.CacheWriteTokens
		summary.TotalTokens += event.TotalTokens
	}
	summary.CacheRate = cacheRate(summary.InputTokens, summary.CacheReadTokens, summary.CacheWriteTokens, includeCacheWrite)
	return summary
}

// SummarizeTurns 聚合 turn_finalized 事件的轮次计数，与 token 统计分离。
// status=="completed" 计入有效轮次，其余计入异常轮次。
// 仅统计 kind=="turn_finalized" 事件；provider_call 不参与轮次计数。
func SummarizeTurns(events []RequestMetric) (turnsTotal, validTurnsTotal, invalidTurnsTotal int) {
	for _, event := range events {
		if !IsTurnFinalized(event.Kind) {
			continue
		}
		turnsTotal++
		if strings.TrimSpace(event.Status) == "completed" {
			validTurnsTotal++
		} else {
			invalidTurnsTotal++
		}
	}
	return turnsTotal, validTurnsTotal, invalidTurnsTotal
}

// IsTurnFinalized 判断事件是否为会话回合结束事件（用于轮次聚合，不参与 token 汇总）。
func IsTurnFinalized(kind string) bool {
	return strings.TrimSpace(kind) == KindTurnFinalized
}

// BucketEvents 将事件按 bucketHours 小时粒度分桶；bucketHours <= 0 时按 1 小时。
// 返回从 start 对齐到 end 的完整时间轴（空桶保留，便于图表连续展示）。
func BucketEvents(events []RequestMetric, start, end time.Time, bucketHours int, includeCacheWrite bool) []TokenBucket {
	if bucketHours <= 0 {
		bucketHours = 1
	}
	if end.IsZero() {
		end = time.Now().UTC()
	}
	if start.IsZero() || !start.Before(end) {
		return []TokenBucket{}
	}

	bucket := time.Duration(bucketHours) * time.Hour
	// 对齐到 bucket 边界（UTC）
	startAligned := start.UTC().Truncate(bucket)
	if startAligned.After(start.UTC()) {
		startAligned = startAligned.Add(-bucket)
	}

	type mutableBucket struct {
		input, output, cacheRead, cacheWrite, total int64
		count                                       int
	}
	buckets := make(map[int64]*mutableBucket)
	for ts := startAligned; ts.Before(end); ts = ts.Add(bucket) {
		buckets[ts.UnixNano()] = &mutableBucket{}
	}

	for _, event := range events {
		at := event.At.UTC()
		if at.Before(startAligned) || !at.Before(end) {
			continue
		}
		key := at.Truncate(bucket).UnixNano()
		item, ok := buckets[key]
		if !ok {
			continue
		}
		item.input += event.InputTokens
		item.output += event.OutputTokens
		item.cacheRead += event.CacheReadTokens
		item.cacheWrite += event.CacheWriteTokens
		item.total += event.TotalTokens
		item.count++
	}

	result := make([]TokenBucket, 0, len(buckets))
	for ts := startAligned; ts.Before(end); ts = ts.Add(bucket) {
		item := buckets[ts.UnixNano()]
		if item == nil {
			item = &mutableBucket{}
		}
		result = append(result, TokenBucket{
			At:               ts,
			InputTokens:      item.input,
			OutputTokens:     item.output,
			CacheReadTokens:  item.cacheRead,
			CacheWriteTokens: item.cacheWrite,
			TotalTokens:      item.total,
			RequestCount:     item.count,
			CacheRate:        cacheRate(item.input, item.cacheRead, item.cacheWrite, includeCacheWrite),
		})
	}
	return result
}

// ProviderSpend 表示按中转站（relay station）聚合后的用量与花费。
// 分组标签优先取 GroupName，其次 baseURL 主机名，最后 provider 类型。
type ProviderSpend struct {
	Station          string   `json:"station"`
	Provider         string   `json:"provider"`
	ProviderCalls    int      `json:"providerCalls"`
	InputTokens      int64    `json:"inputTokens"`
	OutputTokens     int64    `json:"outputTokens"`
	CacheReadTokens  int64    `json:"cacheReadTokens"`
	CacheWriteTokens int64    `json:"cacheWriteTokens"`
	TotalTokens      int64    `json:"totalTokens"`
	EstimatedCostUSD *float64 `json:"estimatedCostUsd"`
	Currency         string   `json:"currency,omitempty"`
	PricingSource    string   `json:"pricingSource,omitempty"`
}

// SummarizeProviderSpend 按中转站聚合 provider_call 事件的 token 与花费。
// 花费仅累加价格已知（CostUSD 非空）的事件；某分组内全部未知则花费为 nil。
func SummarizeProviderSpend(events []RequestMetric) []ProviderSpend {
	type acc struct {
		spend    ProviderSpend
		cost     float64
		costKnwn bool
		currency string
		source   string
		mixed    bool
	}
	order := make([]string, 0)
	byKey := make(map[string]*acc)
	for _, event := range events {
		if !IsProviderCall(event.Kind) {
			continue
		}
		station := stationLabel(event.GroupName, event.BaseURL, event.Provider)
		provider := strings.TrimSpace(event.Provider)
		currency := strings.TrimSpace(event.Currency)
		// 不同币种不能进入同一个累计桶；未知价格仍使用空币种桶。
		key := station + "\x00" + provider + "\x00" + currency
		item, ok := byKey[key]
		if !ok {
			item = &acc{spend: ProviderSpend{Station: station, Provider: provider}}
			byKey[key] = item
			order = append(order, key)
		}
		item.spend.ProviderCalls++
		item.spend.InputTokens += event.InputTokens
		item.spend.OutputTokens += event.OutputTokens
		item.spend.CacheReadTokens += event.CacheReadTokens
		item.spend.CacheWriteTokens += event.CacheWriteTokens
		item.spend.TotalTokens += event.TotalTokens
		if event.CostUSD != nil {
			if item.currency == "" {
				item.currency = currency
			}
			if item.source == "" {
				item.source = strings.TrimSpace(event.PricingSource)
			}
			item.cost += *event.CostUSD
			item.costKnwn = true
		}
	}
	result := make([]ProviderSpend, 0, len(order))
	for _, key := range order {
		item := byKey[key]
		if item.costKnwn {
			cost := item.cost
			item.spend.EstimatedCostUSD = &cost
			item.spend.Currency = item.currency
			item.spend.PricingSource = item.source
		}
		result = append(result, item.spend)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].TotalTokens > result[j].TotalTokens
	})
	return result
}

// stationLabel 解析中转站标签：GroupName -> baseURL 主机名 -> provider 类型。
func stationLabel(groupName, baseURL, provider string) string {
	if name := strings.TrimSpace(groupName); name != "" {
		return name
	}
	if host := hostFromBaseURL(baseURL); host != "" {
		return host
	}
	if p := strings.TrimSpace(provider); p != "" {
		return p
	}
	return "unknown"
}

// hostFromBaseURL 从 baseURL 中提取主机名（含端口），无法解析时返回空串。
func hostFromBaseURL(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return ""
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return trimmed
}

// ListModels 返回事件中出现过的模型名（去重排序前由调用方处理排序）。
func ListModels(events []RequestMetric) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, event := range events {
		if !IsProviderCall(event.Kind) {
			continue
		}
		model := strings.TrimSpace(event.Model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}
	return result
}
