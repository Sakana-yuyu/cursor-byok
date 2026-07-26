// provider_balance.go 提供中转站余额/额度查询能力。
//
// 中转站（relay station）通常兼容 OpenAI/Anthropic API，同时暴露计费查询接口。
// 本文件按优先级尝试多种主流策略，返回第一个成功结果；全部失败时返回结构化的
// "unsupported/failed" 结果，绝不 panic。复用 model_catalog.go 的 HTTP 客户端、
// URL 归一化与鉴权头约定。
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cursor/internal/modelchannel"
)

const (
	// providerBalanceTimeout 表示单个余额查询请求的超时时间。
	providerBalanceTimeout = 15 * time.Second
	// providerBalanceMaxBodyBytes 表示余额响应体最多读取的字节数。
	providerBalanceMaxBodyBytes = 1 << 20
	// newAPIQuotaPerUnit 表示 One/NewAPI 的标准换算比例：500000 quota 单位 = $1。
	newAPIQuotaPerUnit = 500000.0
	// providerBalanceUsageLookbackDays 表示 OpenAI 用量查询回溯的天数窗口。
	providerBalanceUsageLookbackDays = 100
)

// ProviderBalanceRequest 是查询余额所需的临时连接参数，镜像 ModelCatalogRequest。
type ProviderBalanceRequest struct {
	Type    string `json:"type"`
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
	// ForceRefresh 为 true 时绕过进程内 TTL 缓存，强制重新查询（供 UI 显式刷新使用）。
	ForceRefresh bool `json:"forceRefresh,omitempty"`
}

// ProviderBalance 是统一的余额/额度查询结果，带 JSON 标签供 Wails 前端使用。
type ProviderBalance struct {
	Supported bool     `json:"supported"`
	Source    string   `json:"source"`    // "openai_billing" | "newapi" | ""
	Currency  string   `json:"currency"`  // "USD"
	Total     *float64 `json:"total"`     // 总额度，未知为 nil
	Used      *float64 `json:"used"`      // 已用金额，未知为 nil
	Remaining *float64 `json:"remaining"` // 剩余额度，未知为 nil
	Message   string   `json:"message"`   // 人类可读状态 / 错误信息
}

// QueryProviderBalance 依次尝试各余额查询策略，返回第一个成功结果。
func (s *ProxyService) QueryProviderBalance(request ProviderBalanceRequest) ProviderBalance {
	apiKey := strings.TrimSpace(request.APIKey)
	if apiKey == "" {
		return ProviderBalance{Supported: false, Message: "缺少 apiKey，无法查询余额"}
	}
	normalized, err := modelchannel.NormalizeBaseURL(request.BaseURL)
	if err != nil {
		return ProviderBalance{Supported: false, Message: fmt.Sprintf("baseURL 无效：%v", err)}
	}

	cacheKey := metadataCacheKey(request.Type, request.BaseURL, apiKey)
	if request.ForceRefresh {
		s.providerBalanceCache.invalidate(cacheKey)
	} else if cached, ok := s.providerBalanceCache.get(cacheKey); ok {
		return cached
	}

	ctx, cancel := context.WithTimeout(context.Background(), providerBalanceTimeout)
	defer cancel()

	httpClient := s.publicClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	// 策略 1：OpenAI 计费端点（多数中转站如 sub2api/cpa/sakanapi 支持）。
	if balance, ok := queryOpenAIBillingBalance(ctx, httpClient, normalized, apiKey); ok {
		// 仅缓存查询成功（Supported）的结果，不支持/失败结果不进缓存。
		s.providerBalanceCache.set(cacheKey, balance)
		return balance
	}

	// 策略 2：NewAPI / OneAPI 风格用户信息端点。
	if balance, ok := queryNewAPIBalance(ctx, httpClient, normalized, apiKey); ok {
		s.providerBalanceCache.set(cacheKey, balance)
		return balance
	}

	// 策略 3：均不支持。不缓存，便于后续重试直接命中网络。
	return ProviderBalance{Supported: false, Message: "该中转站不支持已知的余额查询接口"}
}

// queryOpenAIBillingBalance 查询 OpenAI 计费端点，成功时返回额度信息。
func queryOpenAIBillingBalance(ctx context.Context, httpClient *http.Client, normalizedBaseURL, apiKey string) (ProviderBalance, bool) {
	root := billingAPIRoot(normalizedBaseURL)

	subURL := root + "/dashboard/billing/subscription"
	subBody, ok := doProviderBalanceGET(ctx, httpClient, subURL, apiKey)
	if !ok {
		return ProviderBalance{}, false
	}
	var subscription struct {
		HardLimitUSD       *float64 `json:"hard_limit_usd"`
		SystemHardLimitUSD *float64 `json:"system_hard_limit_usd"`
	}
	if err := json.Unmarshal(subBody, &subscription); err != nil {
		return ProviderBalance{}, false
	}
	total := subscription.HardLimitUSD
	if total == nil {
		total = subscription.SystemHardLimitUSD
	}
	if total == nil {
		// 没有额度上限字段，说明该接口并非 OpenAI 计费语义，交给下一策略。
		return ProviderBalance{}, false
	}

	// 用量查询：total_usage 单位为美分，需除以 100。使用较宽的日期窗口。
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -providerBalanceUsageLookbackDays).Format("2006-01-02")
	end := now.AddDate(0, 0, 1).Format("2006-01-02")
	usageURL := fmt.Sprintf("%s/dashboard/billing/usage?start_date=%s&end_date=%s", root, start, end)

	balance := ProviderBalance{
		Supported: true,
		Source:    "openai_billing",
		Currency:  "USD",
		Total:     total,
		Message:   "查询成功",
	}
	if usageBody, usageOK := doProviderBalanceGET(ctx, httpClient, usageURL, apiKey); usageOK {
		var usage struct {
			TotalUsage *float64 `json:"total_usage"`
		}
		if err := json.Unmarshal(usageBody, &usage); err == nil && usage.TotalUsage != nil {
			usedUSD := *usage.TotalUsage / 100.0
			remaining := *total - usedUSD
			balance.Used = &usedUSD
			balance.Remaining = &remaining
		}
	}
	return balance, true
}

// queryNewAPIBalance 查询 NewAPI / OneAPI 风格的用户信息端点。
func queryNewAPIBalance(ctx context.Context, httpClient *http.Client, normalizedBaseURL, apiKey string) (ProviderBalance, bool) {
	origin, err := billingOrigin(normalizedBaseURL)
	if err != nil {
		return ProviderBalance{}, false
	}
	selfURL := origin + "/api/user/self"
	body, ok := doProviderBalanceGET(ctx, httpClient, selfURL, apiKey)
	if !ok {
		return ProviderBalance{}, false
	}
	var payload struct {
		Success *bool `json:"success"`
		Data    *struct {
			Quota     *float64 `json:"quota"`
			UsedQuota *float64 `json:"used_quota"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ProviderBalance{}, false
	}
	if payload.Success != nil && !*payload.Success {
		return ProviderBalance{}, false
	}
	if payload.Data == nil || payload.Data.Quota == nil {
		return ProviderBalance{}, false
	}

	remaining := *payload.Data.Quota / newAPIQuotaPerUnit
	balance := ProviderBalance{
		Supported: true,
		Source:    "newapi",
		Currency:  "USD",
		Remaining: &remaining,
		Message:   "查询成功",
	}
	if payload.Data.UsedQuota != nil {
		usedUSD := *payload.Data.UsedQuota / newAPIQuotaPerUnit
		total := remaining + usedUSD
		balance.Used = &usedUSD
		balance.Total = &total
	}
	return balance, true
}

// doProviderBalanceGET 发送带 Bearer 鉴权的 GET 请求，返回 200 响应体。
// 非 200、读取失败或超限时返回 ok=false，交由调用方回退到下一策略。
func doProviderBalanceGET(ctx context.Context, httpClient *http.Client, endpoint, apiKey string) ([]byte, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, providerBalanceMaxBodyBytes+1))
	if err != nil {
		return nil, false
	}
	if len(body) > providerBalanceMaxBodyBytes {
		return nil, false
	}
	return body, true
}

// billingAPIRoot 从归一化 baseURL 推导计费接口所在的 API 根路径。
// 复用 buildModelCatalogURL 的思路：剥离末尾的 /models、/chat/completions、
// /responses、/messages、/completions，保留（可能包含 /v1 的）API 根。
func billingAPIRoot(normalizedBaseURL string) string {
	parsed, err := url.Parse(normalizedBaseURL)
	if err != nil {
		return strings.TrimRight(normalizedBaseURL, "/")
	}
	path := strings.TrimRight(parsed.Path, "/")
	lowerPath := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lowerPath, "/chat/completions"):
		path = path[:len(path)-len("/chat/completions")]
	case strings.HasSuffix(lowerPath, "/models"):
		path = path[:len(path)-len("/models")]
	case strings.HasSuffix(lowerPath, "/responses"):
		path = path[:len(path)-len("/responses")]
	case strings.HasSuffix(lowerPath, "/messages"):
		path = path[:len(path)-len("/messages")]
	case strings.HasSuffix(lowerPath, "/completions"):
		path = path[:len(path)-len("/completions")]
	}
	parsed.Path = strings.TrimRight(path, "/")
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

// billingOrigin 返回 baseURL 的 scheme+host（去除路径），供 NewAPI 风格接口使用。
func billingOrigin(normalizedBaseURL string) (string, error) {
	parsed, err := url.Parse(normalizedBaseURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("baseURL 缺少 scheme 或 host")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}
