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

// LoadRecentRequestMetrics 返回 usage.json 中已记录的最近请求明细。
// limit <= 0 表示不限制条数。offset 表示跳过前 offset 条（按时间倒序，即跳过最新的 offset 条）。
// 请求明细只展示 provider_call 事件；turn_finalized 仅用于轮次聚合统计，不进明细列表，
// 否则同一轮会同时出现 provider_call + turn_finalized 两条记录（"两条明细" bug）。
func LoadRecentRequestMetrics(path string, limit int, offset int, includeCacheWrite bool, lookup *PriceLookup) ([]RequestMetric, error) {
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
	// 先过滤出 provider_call，再做分页，保证 count 与列表口径一致。
	providerEvents := make([]usageFileEvent, 0, len(doc.RecentEvents))
	for _, event := range doc.RecentEvents {
		if !IsProviderCall(event.Kind) {
			continue
		}
		providerEvents = append(providerEvents, event)
	}
	total := len(providerEvents)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return []RequestMetric{}, nil
	}
	available := total - offset
	if limit <= 0 || limit > available {
		limit = available
	}
	result := make([]RequestMetric, 0, limit)
	for _, event := range providerEvents[offset : offset+limit] {
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

// LoadRecentRequestCount 返回 usage.json 中 recent_events 的 provider_call 事件数，
// 与 LoadRecentRequestMetrics 口径一致（turn_finalized 不计入明细总数，避免分页错乱）。
func LoadRecentRequestCount(path string) (int, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read usage file: %w", err)
	}
	var doc struct {
		RecentEvents []usageFileEvent `json:"recent_events"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return 0, fmt.Errorf("decode usage file: %w", err)
	}
	count := 0
	for _, event := range doc.RecentEvents {
		if IsProviderCall(event.Kind) {
			count++
		}
	}
	return count, nil
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
		if events, err := LoadRecentRequestMetrics(path, 0, 0, includeCacheWrite, lookup); err == nil {
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

// ResetUsageFile 把指定路径的 usage.json 重置为空文档（Totals、Daily、RecentEvents 全部归零）。
// 通过原子写入实现，确保与 forwarder 进程内 UsageFileStore 的并发写入不冲突。
func ResetUsageFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("usage file path is empty")
	}
	if err := os.MkdirAll(filepathDir(path), 0o755); err != nil {
		return fmt.Errorf("create usage directory: %w", err)
	}
	empty := struct {
		SchemaVersion int             `json:"schema_version"`
		UpdatedAt     time.Time       `json:"updated_at"`
		Totals        json.RawMessage `json:"totals"`
		Daily         json.RawMessage `json:"daily"`
		RecentEvents  json.RawMessage `json:"recent_events"`
	}{
		SchemaVersion: 2,
		UpdatedAt:     time.Now().UTC(),
		Totals:        json.RawMessage(`{}`),
		Daily:         json.RawMessage(`[]`),
		RecentEvents:  json.RawMessage(`[]`),
	}
	body, err := json.MarshalIndent(empty, "", "  ")
	if err != nil {
		return fmt.Errorf("encode empty usage document: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("write temp usage file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename usage file: %w", err)
	}
	return nil
}

// filepathDir 返回 path 的目录部分，path 为空时返回 ".".
func filepathDir(path string) string {
	idx := strings.LastIndexByte(path, os.PathSeparator)
	if idx < 0 {
		return "."
	}
	if idx == 0 {
		return string(os.PathSeparator)
	}
	return path[:idx]
}
