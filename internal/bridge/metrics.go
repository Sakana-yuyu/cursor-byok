package bridge

import (
	"cursor/internal/appdata"
	"cursor/internal/historymetrics"
	"cursor/internal/localcache"
	"strings"
	"time"
)

// LocalCacheStats 定义本地响应缓存命中统计，供前端与 provider prompt-cache 分开展示。
type LocalCacheStats = localcache.LocalCacheStats

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
type MetricsService struct {
	includeCacheWriteInHitRate func() bool
	priceRates                 func() []historymetrics.PriceRate
	resetUsage                 func() error
}

// NewMetricsService 创建首页统计 service。
// includeCacheWriteInHitRate 返回用户配置中的缓存命中率口径开关；为 nil 时按默认口径（不计入缓存创建）。
// priceRates 返回当前配置渠道的价格条目快照，用于按读取时联结计算美元花费；为 nil 时花费恒为未知。
// resetUsage 优先调用正在运行的 forwarder writer，确保 pending 事件不会在清除后写回；为空时使用文件重置兜底。
func NewMetricsService(includeCacheWriteInHitRate func() bool, priceRates func() []historymetrics.PriceRate, resetUsage func() error) *MetricsService {
	return &MetricsService{includeCacheWriteInHitRate: includeCacheWriteInHitRate, priceRates: priceRates, resetUsage: resetUsage}
}

// includeCacheWrite 读取当前缓存命中率口径。
func (service *MetricsService) includeCacheWrite() bool {
	if service.includeCacheWriteInHitRate == nil {
		return false
	}
	return service.includeCacheWriteInHitRate()
}

// priceLookup 依据当前配置构建价格查询表；无价格来源时返回 nil（花费保持未知）。
func (service *MetricsService) priceLookup() *historymetrics.PriceLookup {
	if service.priceRates == nil {
		return nil
	}
	rates := service.priceRates()
	if len(rates) == 0 {
		return nil
	}
	return historymetrics.NewPriceLookup(rates)
}

// GetLocalCacheStats 返回本地（进程内）LLM 响应缓存的命中统计。
// 该统计与 provider 侧 prompt-cache 命中率相互独立，不做混淆。
func (service *MetricsService) GetLocalCacheStats() LocalCacheStats {
	return localcache.Snapshot()
}

// GetRecentRequestMetrics 返回 usage.json 中已记录的最近请求明细（服务端分页）。
// limit <= 0 表示不限制条数；offset 表示跳过最新的 offset 条。
func (service *MetricsService) GetRecentRequestMetrics(limit int, offset int) ([]historymetrics.RequestMetric, error) {
	if err := appdata.EnsureAssistantHome(); err != nil {
		return nil, err
	}
	return historymetrics.LoadRecentRequestMetrics(appdata.UsageFilePath(), limit, offset, service.includeCacheWrite(), service.priceLookup())
}

// GetRecentRequestMetricsCount 返回 usage.json 中 recent_events 的总数，供前端分页使用。
func (service *MetricsService) GetRecentRequestMetricsCount() (int, error) {
	if err := appdata.EnsureAssistantHome(); err != nil {
		return 0, err
	}
	return historymetrics.LoadRecentRequestCount(appdata.UsageFilePath())
}

// GetRecentRequestMetricsAbnormalCount 返回 provider_call 事件的全量异常数，
// 供「请求明细」页展示跨分页的异常总数（不再只统计当前页）。
func (service *MetricsService) GetRecentRequestMetricsAbnormalCount() (int, error) {
	if err := appdata.EnsureAssistantHome(); err != nil {
		return 0, err
	}
	return historymetrics.LoadRecentRequestAbnormalCount(appdata.UsageFilePath())
}

// GetRecentRequestMetricsDegradedCount 返回 provider_call 事件的降级数（degraded 非空），
// 供「请求明细」页展示跨分页的降级总数，与异常计数同口径。
func (service *MetricsService) GetRecentRequestMetricsDegradedCount() (int, error) {
	if err := appdata.EnsureAssistantHome(); err != nil {
		return 0, err
	}
	return historymetrics.LoadRecentRequestDegradedCount(appdata.UsageFilePath())
}

// GetMetricsRangeSummary 按时间范围与模型过滤后汇总 token（仅 provider_call）。
// startUnixMs/endUnixMs 为毫秒时间戳；<=0 表示不限制该端。model 为空表示全部模型。
// 轮次计数来自 turn_finalized 事件（与 token 分离），按相同时间范围聚合。
func (service *MetricsService) GetMetricsRangeSummary(startUnixMs, endUnixMs int64, model string) (historymetrics.RangeSummary, error) {
	events, err := service.loadProviderEvents(startUnixMs, endUnixMs, model)
	if err != nil {
		return historymetrics.RangeSummary{}, err
	}
	summary := historymetrics.SummarizeEvents(events, service.includeCacheWrite())
	// 轮次独立聚合：turn_finalized 与 provider_call 分离，不重复计入 token。
	start, end := unixMsRange(startUnixMs, endUnixMs)
	if turnEvents, terr := service.loadTurnEvents(start, end); terr == nil {
		turns, valid, invalid := historymetrics.SummarizeTurns(turnEvents)
		summary.TurnsTotal = turns
		summary.ValidTurnsTotal = valid
		summary.InvalidTurnsTotal = invalid
	}
	return summary, nil
}

// GetMetricsTokenBuckets 按时间范围、模型与小时粒度分桶统计 token（仅 provider_call）。
// bucketHours <= 0 时按 1 小时分桶。
func (service *MetricsService) GetMetricsTokenBuckets(startUnixMs, endUnixMs int64, model string, bucketHours int) ([]historymetrics.TokenBucket, error) {
	if err := appdata.EnsureAssistantHome(); err != nil {
		return nil, err
	}
	all, err := historymetrics.LoadRecentRequestMetrics(appdata.UsageFilePath(), 0, 0, service.includeCacheWrite(), service.priceLookup())
	if err != nil {
		return nil, err
	}
	start, end := unixMsRange(startUnixMs, endUnixMs)
	filtered := historymetrics.FilterProviderEvents(all, start, end, strings.TrimSpace(model))
	return historymetrics.BucketEvents(filtered, start, end, bucketHours, service.includeCacheWrite()), nil
}

// GetHomeMetricsSummary 返回首页展示的全量历史统计摘要。
func (service *MetricsService) GetHomeMetricsSummary() (HomeMetricsSummary, error) {
	if err := appdata.EnsureAssistantHome(); err != nil {
		return HomeMetricsSummary{}, err
	}

	summary, err := historymetrics.LoadUsageSummary(appdata.UsageFilePath(), service.includeCacheWrite(), service.priceLookup())
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

// ResetUsageMetrics 清空所有会话统计：Totals、Daily、RecentEvents 全部归零。
// 优先委托给正在运行的 forwarder writer，确保 pending 事件不会在清除后写回。
func (service *MetricsService) ResetUsageMetrics() error {
	if service != nil && service.resetUsage != nil {
		return service.resetUsage()
	}
	if err := appdata.EnsureAssistantHome(); err != nil {
		return err
	}
	return historymetrics.ResetUsageFile(appdata.UsageFilePath())
}

// GetProviderSpendSummary 按中转站（GroupName -> baseURL 主机名 -> provider 类型）聚合区间内的用量与美元花费。
// startUnixMs/endUnixMs 为毫秒时间戳；<=0 表示不限制该端。
func (service *MetricsService) GetProviderSpendSummary(startUnixMs, endUnixMs int64) ([]historymetrics.ProviderSpend, error) {
	events, err := service.loadProviderEvents(startUnixMs, endUnixMs, "")
	if err != nil {
		return nil, err
	}
	return historymetrics.SummarizeProviderSpend(events), nil
}

// GetProviderEvents 按时间范围返回 provider_call 事件明细（与花费汇总同口径），
// 供「会话分析」页图表按当前范围加载，避免全量拉取后前端过滤。
func (service *MetricsService) GetProviderEvents(startUnixMs, endUnixMs int64, model string) ([]historymetrics.RequestMetric, error) {
	return service.loadProviderEvents(startUnixMs, endUnixMs, strings.TrimSpace(model))
}

func (service *MetricsService) loadProviderEvents(startUnixMs, endUnixMs int64, model string) ([]historymetrics.RequestMetric, error) {
	if err := appdata.EnsureAssistantHome(); err != nil {
		return nil, err
	}
	all, err := historymetrics.LoadRecentRequestMetrics(appdata.UsageFilePath(), 0, 0, service.includeCacheWrite(), service.priceLookup())
	if err != nil {
		return nil, err
	}
	start, end := unixMsRange(startUnixMs, endUnixMs)
	return historymetrics.FilterProviderEvents(all, start, end, strings.TrimSpace(model)), nil
}

// loadTurnEvents 加载 turn_finalized 事件并按时间范围过滤，供轮次聚合使用。
func (service *MetricsService) loadTurnEvents(start, end time.Time) ([]historymetrics.RequestMetric, error) {
	if err := appdata.EnsureAssistantHome(); err != nil {
		return nil, err
	}
	all, err := historymetrics.LoadRecentTurnEvents(appdata.UsageFilePath())
	if err != nil {
		return nil, err
	}
	return historymetrics.FilterTurnEvents(all, start, end), nil
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
