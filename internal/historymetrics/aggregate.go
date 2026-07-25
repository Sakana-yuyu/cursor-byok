package historymetrics

import (
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

// SummarizeEvents 汇总 provider 事件的 token。
func SummarizeEvents(events []RequestMetric) RangeSummary {
	var summary RangeSummary
	for _, event := range events {
		summary.RequestCount++
		summary.InputTokens += event.InputTokens
		summary.OutputTokens += event.OutputTokens
		summary.CacheReadTokens += event.CacheReadTokens
		summary.CacheWriteTokens += event.CacheWriteTokens
		summary.TotalTokens += event.TotalTokens
	}
	summary.CacheRate = cacheRate(summary.InputTokens, summary.CacheReadTokens, summary.CacheWriteTokens)
	return summary
}

// BucketEvents 将事件按 bucketHours 小时粒度分桶；bucketHours <= 0 时按 1 小时。
// 返回从 start 对齐到 end 的完整时间轴（空桶保留，便于图表连续展示）。
func BucketEvents(events []RequestMetric, start, end time.Time, bucketHours int) []TokenBucket {
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
			CacheRate:        cacheRate(item.input, item.cacheRead, item.cacheWrite),
		})
	}
	return result
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