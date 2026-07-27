package historymetrics

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type usageFileDocument struct {
	Totals struct {
		ProviderCalls     int64 `json:"provider_calls"`
		TurnsTotal        int64 `json:"turns_total"`
		ValidTurnsTotal   int64 `json:"valid_turns_total"`
		InvalidTurnsTotal int64 `json:"invalid_turns_total"`
		InputTokens       int64 `json:"input_tokens"`
		OutputTokens      int64 `json:"output_tokens"`
		CacheReadTokens   int64 `json:"cache_read_tokens"`
		CacheWriteTokens  int64 `json:"cache_write_tokens"`
		TotalTokens       int64 `json:"total_tokens"`
	} `json:"totals"`
}

type usageFileEvent struct {
	EventID          string    `json:"event_id"`
	Kind             string    `json:"kind"`
	Status           string    `json:"status"`
	At               time.Time `json:"at"`
	Model            string    `json:"model"`
	Provider         string    `json:"provider"`
	BaseURL          string    `json:"base_url"`
	GroupName        string    `json:"group_name"`
	ErrorCode        string    `json:"error_code"`
	InputTokens      int64     `json:"input_tokens"`
	OutputTokens     int64     `json:"output_tokens"`
	CacheReadTokens  int64     `json:"cache_read_tokens"`
	CacheWriteTokens int64     `json:"cache_write_tokens"`
	TotalTokens      int64     `json:"total_tokens"`
	UsagePresent     bool      `json:"usage_present"`
}

func LoadRecentRequestMetrics(path string, limit int, includeCacheWrite bool, lookup *PriceLookup) ([]RequestMetric, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []RequestMetric{}, nil
		}
		return nil, fmt.Errorf("read usage file: %w", err)
	}
	var doc struct {
		RecentEvents []usageFileEvent `json:"recent_events"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decode usage file: %w", err)
	}
	if limit <= 0 || limit > len(doc.RecentEvents) {
		limit = len(doc.RecentEvents)
	}
	result := make([]RequestMetric, 0, limit)
	for _, event := range doc.RecentEvents[:limit] {
		model := strings.TrimSpace(event.Model)
		provider := strings.TrimSpace(event.Provider)
		baseURL := strings.TrimSpace(event.BaseURL)
		cost, known, currency := lookup.Cost(model, provider, baseURL, event.InputTokens, event.OutputTokens, event.CacheReadTokens, event.CacheWriteTokens)
		result = append(result, RequestMetric{
			EventID:          strings.TrimSpace(event.EventID),
			Kind:             strings.TrimSpace(event.Kind),
			Status:           strings.TrimSpace(event.Status),
			At:               event.At,
			Model:            model,
			Provider:         provider,
			BaseURL:          baseURL,
			GroupName:        strings.TrimSpace(event.GroupName),
			ErrorCode:        strings.TrimSpace(event.ErrorCode),
			InputTokens:      event.InputTokens,
			OutputTokens:     event.OutputTokens,
			CacheReadTokens:  event.CacheReadTokens,
			CacheWriteTokens: event.CacheWriteTokens,
			TotalTokens:      event.TotalTokens,
			UsagePresent:     event.UsagePresent,
			CacheRate:        cacheRate(event.InputTokens, event.CacheReadTokens, event.CacheWriteTokens, includeCacheWrite),
			CostUSD:          cost,
			PricingKnown:     known,
			Currency:         currency,
		})
	}
	return result, nil
}
func LoadUsageSummary(path string, includeCacheWrite bool, lookup *PriceLookup) (Summary, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Summary{}, nil
		}
		return Summary{}, fmt.Errorf("read usage file: %w", err)
	}
	var doc usageFileDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return Summary{}, fmt.Errorf("decode usage file: %w", err)
	}
	totals := Totals{
		InputTokens:        doc.Totals.InputTokens,
		OutputTokens:       doc.Totals.OutputTokens,
		CacheReadTokens:    doc.Totals.CacheReadTokens,
		CacheWriteTokens:   doc.Totals.CacheWriteTokens,
		PromptTokensTotal:  doc.Totals.InputTokens + doc.Totals.CacheReadTokens + doc.Totals.CacheWriteTokens,
		RequestTokensTotal: doc.Totals.TotalTokens,
	}
	summary := Summary{
		ProviderCallsTotal: int(doc.Totals.ProviderCalls),
		TurnsTotal:         int(doc.Totals.TurnsTotal),
		ValidTurnsTotal:    int(doc.Totals.ValidTurnsTotal),
		InvalidTurnsTotal:  int(doc.Totals.InvalidTurnsTotal),
		RequestTokensTotal: totals.RequestTokensTotal,
		PromptTokensTotal:  totals.PromptTokensTotal,
		CacheReadTokens:    totals.CacheReadTokens,
		CacheWriteTokens:   totals.CacheWriteTokens,
		CacheHitRate:       cacheHitRateFromTotals(totals, includeCacheWrite),
	}
	if lookup != nil {
		if events, err := LoadRecentRequestMetrics(path, 0, includeCacheWrite, lookup); err == nil {
			cost, currency := sumEventCost(events)
			summary.EstimatedCostUSD = cost
			summary.Currency = currency
		}
	}
	return summary, nil
}

// sumEventCost 汇总 provider_call 事件的已知成本。
func sumEventCost(events []RequestMetric) (*float64, string) {
	var total float64
	found := false
	currency := ""
	for _, event := range events {
		if !IsProviderCall(event.Kind) {
			continue
		}
		if event.CostUSD == nil {
			continue
		}
		total += *event.CostUSD
		found = true
		if currency == "" {
			currency = event.Currency
		}
	}
	if !found {
		return nil, ""
	}
	return &total, currency
}
