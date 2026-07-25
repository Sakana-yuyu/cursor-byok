package bridge

import (
	"cursor/internal/appdata"
	"cursor/internal/historymetrics"
	"strings"
	"time"
)

// HomeMetricsSummary 定义首页展示的历史统计摘要。
type HomeMetricsSummary struct {
	ProviderCallsTotal int      `json:"providerCallsTotal"`
	TurnsTotal         int      `json:"turnsTotal"`
	ValidTurnsTotal    int      `json:"validTurnsTotal"`
	InvalidTurnsTotal  int      `json:"invalidTurnsTotal"`
	RequestTokensTotal int64    `json:"requestTokensTotal"`
	PromptTokensTotal  int64    `json:"promptTokensTotal"`
	CacheReadTokens    int64    `json:"cacheReadTokens"`
	CacheWriteTokens   int64    `json:"cacheWriteTokens"`
	CacheHitRate       *float64 `json:"cacheHitRate"`
}

// MetricsService 定义首页统计相关的 Wails service。
type MetricsService struct{}

// NewMetricsService 创建首页统计 service。
func NewMetricsService() *MetricsService {
	return &MetricsService{}
}

// GetRecentRequestMetrics 返回 usage.json 中已记录的最近请求明细。
func (service *MetricsService) GetRecentRequestMetrics(limit int) ([]historymetrics.RequestMetric, error) {
	if err := appdata.EnsureAssistantHome(); err != nil {
		return nil, err
	}
	return historymetrics.LoadRecentRequestMetrics(appdata.UsageFilePath(), limit)
}

// GetMetricsRangeSummary 按时间范围与模型过滤后汇总 token（仅 provider_call）。
// startUnixMs/endUnixMs 为毫秒时间戳；<=0 表示不限制该端。model 为空表示全部模型。
func (service *MetricsService) GetMetricsRangeSummary(startUnixMs, endUnixMs int64, model string) (historymetrics.RangeSummary, error) {
	events, err := service.loadProviderEvents(startUnixMs, endUnixMs, model)
	if err != nil {
		return historymetrics.RangeSummary{}, err
	}
	return historymetrics.SummarizeEvents(events), nil
}

// GetMetricsTokenBuckets 按时间范围、模型与小时粒度分桶统计 token（仅 provider_call）。
// bucketHours <= 0 时按 1 小时分桶。
func (service *MetricsService) GetMetricsTokenBuckets(startUnixMs, endUnixMs int64, model string, bucketHours int) ([]historymetrics.TokenBucket, error) {
	if err := appdata.EnsureAssistantHome(); err != nil {
		return nil, err
	}
	all, err := historymetrics.LoadRecentRequestMetrics(appdata.UsageFilePath(), 0)
	if err != nil {
		return nil, err
	}
	start, end := unixMsRange(startUnixMs, endUnixMs)
	filtered := historymetrics.FilterProviderEvents(all, start, end, strings.TrimSpace(model))
	return historymetrics.BucketEvents(filtered, start, end, bucketHours), nil
}

// GetHomeMetricsSummary 返回首页展示的全量历史统计摘要。
func (service *MetricsService) GetHomeMetricsSummary() (HomeMetricsSummary, error) {
	if err := appdata.EnsureAssistantHome(); err != nil {
		return HomeMetricsSummary{}, err
	}

	summary, err := historymetrics.LoadUsageSummary(appdata.UsageFilePath())
	if err != nil {
		return HomeMetricsSummary{}, err
	}
	return HomeMetricsSummary{
		ProviderCallsTotal: summary.ProviderCallsTotal,
		TurnsTotal:         summary.TurnsTotal,
		ValidTurnsTotal:    summary.ValidTurnsTotal,
		InvalidTurnsTotal:  summary.InvalidTurnsTotal,
		RequestTokensTotal: summary.RequestTokensTotal,
		PromptTokensTotal:  summary.PromptTokensTotal,
		CacheReadTokens:    summary.CacheReadTokens,
		CacheWriteTokens:   summary.CacheWriteTokens,
		CacheHitRate:       summary.CacheHitRate,
	}, nil
}

func (service *MetricsService) loadProviderEvents(startUnixMs, endUnixMs int64, model string) ([]historymetrics.RequestMetric, error) {
	if err := appdata.EnsureAssistantHome(); err != nil {
		return nil, err
	}
	all, err := historymetrics.LoadRecentRequestMetrics(appdata.UsageFilePath(), 0)
	if err != nil {
		return nil, err
	}
	start, end := unixMsRange(startUnixMs, endUnixMs)
	return historymetrics.FilterProviderEvents(all, start, end, strings.TrimSpace(model)), nil
}

func unixMsRange(startUnixMs, endUnixMs int64) (time.Time, time.Time) {
	var start, end time.Time
	if startUnixMs > 0 {
		start = time.UnixMilli(startUnixMs).UTC()
	}
	if endUnixMs > 0 {
		end = time.UnixMilli(endUnixMs).UTC()
	}
	if end.IsZero() {
		end = time.Now().UTC()
	}
	return start, end
}

