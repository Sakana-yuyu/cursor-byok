package historymetrics

import "time"

type Summary struct {
	ProviderCallsTotal int      `json:"providerCallsTotal"`
	TurnsTotal         int      `json:"turnsTotal"`
	ValidTurnsTotal    int      `json:"validTurnsTotal"`
	InvalidTurnsTotal  int      `json:"invalidTurnsTotal"`
	RequestTokensTotal int64    `json:"requestTokensTotal"`
	PromptTokensTotal  int64    `json:"promptTokensTotal"`
	CacheReadTokens    int64    `json:"cacheReadTokens"`
	CacheWriteTokens   int64    `json:"cacheWriteTokens"`
	CacheHitRate       *float64 `json:"cacheHitRate"`
	EstimatedCostUSD   *float64 `json:"estimatedCostUsd"`
	Currency           string   `json:"currency"`
}

type RequestMetric struct {
	EventID          string    `json:"eventId"`
	Kind             string    `json:"kind"`
	Status           string    `json:"status"`
	At               time.Time `json:"at"`
	Model            string    `json:"model"`
	Provider         string    `json:"provider"`
	BaseURL          string    `json:"baseUrl"`
	GroupName        string    `json:"groupName"`
	ErrorCode        string    `json:"errorCode"`
	InputTokens      int64     `json:"inputTokens"`
	OutputTokens     int64     `json:"outputTokens"`
	CacheReadTokens  int64     `json:"cacheReadTokens"`
	CacheWriteTokens int64     `json:"cacheWriteTokens"`
	TotalTokens      int64     `json:"totalTokens"`
	UsagePresent     bool      `json:"usagePresent"`
	CacheRate        *float64  `json:"cacheRate"`
	CostUSD          *float64  `json:"costUsd"`
	PricingKnown     bool      `json:"pricingKnown"`
	Currency         string    `json:"currency"`
}

type Totals struct {
	InputTokens        int64
	OutputTokens       int64
	CacheReadTokens    int64
	CacheWriteTokens   int64
	PromptTokensTotal  int64
	RequestTokensTotal int64
}

func cacheHitRateFromTotals(totals Totals, includeCacheWrite bool) *float64 {
	return cacheRate(totals.InputTokens, totals.CacheReadTokens, totals.CacheWriteTokens, includeCacheWrite)
}

// cacheRate 计算缓存命中率。
// includeCacheWrite 为 false 时：read /（input + read）。
// includeCacheWrite 为 true 时：read /（input + read + write）。
func cacheRate(input, read, write int64, includeCacheWrite bool) *float64 {
	denominator := input + read
	if includeCacheWrite {
		denominator += write
	}
	if denominator <= 0 {
		return nil
	}
	value := float64(read) / float64(denominator)
	return &value
}
