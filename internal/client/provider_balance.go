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
	// providerBalanceTimeout 表示整个余额查询（多端点多策略）的总超时时间。
	providerBalanceTimeout = 15 * time.Second
	// providerBalancePerRequestTimeout 表示单个 GET 的超时上限；对齐 cc-switch 具名余额查询。
	// 总查询预算同为 15s，因此慢端点不会让 UI 无限卡住。
	providerBalancePerRequestTimeout = 15 * time.Second
	// providerBalanceMaxBodyBytes 表示余额响应体最多读取的字节数。
	providerBalanceMaxBodyBytes = 1 << 20
	// newAPIQuotaPerUnit 表示 One/NewAPI 的标准换算比例：500000 quota 单位 = $1。
	newAPIQuotaPerUnit = 500000.0
	// providerBalanceUsageLookbackDays 表示 OpenAI 用量查询回溯的天数窗口。
	providerBalanceUsageLookbackDays = 100
	// providerBalanceUnlimitedThreshold 表示「无限额度」的判定阈值。
	// One/NewAPI 对无限额度令牌会把额度上限置为 1e8（100000000），换算后接近 $99999999.99；
	// 真实余额不可能达到此量级，故超过该阈值即视为不限额。
	providerBalanceUnlimitedThreshold = 99_000_000.0
)

// ProviderBalanceRequest 是查询余额所需的临时连接参数，镜像 ModelCatalogRequest。
type ProviderBalanceRequest struct {
	Type       string `json:"type"`
	SupplierID string `json:"supplierID,omitempty"`
	BaseURL    string `json:"baseURL"`
	APIKey     string `json:"apiKey"`
	// ForceRefresh 为 true 时绕过进程内 TTL 缓存，强制重新查询（供 UI 显式刷新使用）。
	ForceRefresh bool `json:"forceRefresh,omitempty"`
}

// ProviderBalance 是统一的余额/额度查询结果，带 JSON 标签供 Wails 前端使用。
type ProviderBalance struct {
	Supported bool     `json:"supported"`
	Source    string   `json:"source"`    // "openai_billing" | "newapi" | ""
	Currency  string   `json:"currency"`  // "USD"
	Unlimited bool     `json:"unlimited"` // true 表示不限额度（One/NewAPI 无限令牌哨兵值）
	Total     *float64 `json:"total"`     // 总额度，未知为 nil；不限额时为 nil
	Used      *float64 `json:"used"`      // 已用金额，未知为 nil
	Remaining *float64 `json:"remaining"` // 剩余额度，未知为 nil；不限额时为 nil
	Message   string   `json:"message"`   // 人类可读状态 / 错误信息
	// Transient 标记本次失败是否为瞬时传输失败（网络不可达/超时/读体中断）。
	// true：前端可保留并继续展示上一次成功值（keep-last-good）。
	// false：确定性失败（空 key/鉴权失败/非 2xx/非法 JSON/不支持），前端应清空为不可用。
	// 仅在 Supported=false 时有意义；Supported=true 时恒为 false。
	Transient bool `json:"transient"`
}

// transientTracker 在多策略试探链中累积「是否发生过瞬时传输失败」。
// 任一底层 GET 因 http.Client.Do 错误或读体错误失败时置位，
// 供最终 unsupported 结果判定 Transient。非并发使用，无需加锁。
type transientTracker struct{ hit bool }

// QueryProviderBalance 依次尝试各余额查询策略，返回第一个成功结果。
func (s *ProxyService) QueryProviderBalance(request ProviderBalanceRequest) ProviderBalance {
	apiKey := strings.TrimSpace(request.APIKey)
	if apiKey == "" {
		return ProviderBalance{Supported: false, Transient: false, Message: "缺少 apiKey，无法查询余额"}
	}
	normalized, err := modelchannel.NormalizeBaseURL(request.BaseURL)
	if err != nil {
		return ProviderBalance{Supported: false, Transient: false, Message: fmt.Sprintf("baseURL 无效：%v", err)}
	}

	cacheKey := metadataCacheKey(request.Type, request.BaseURL, apiKey)
	if supplierID := strings.ToLower(strings.TrimSpace(request.SupplierID)); supplierID != "" && supplierID != "custom" {
		cacheKey += "|supplier=" + supplierID
	}
	configuredAdapter, hasConfiguredAdapter := s.findAdapterForBalance(request.Type, request.SupplierID, normalized, apiKey)
	// 显式配置的余额接口优先，cache identity 必须包含配置 hash，避免和具名/通用策略串用缓存。
	if hasConfiguredAdapter {
		cacheKey = configuredBalanceCacheKey(cacheKey, configuredAdapter)
	}
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

	// 策略 0：显式配置查询。provider 若配置了 BalanceQueryURL，按模板发一次 GET 并按点分路径取值。
	if hasConfiguredAdapter {
		if configured, matched := s.queryConfiguredBalanceWithAdapter(ctx, httpClient, configuredAdapter, normalized, apiKey); matched {
			if configured.Supported {
				s.providerBalanceCache.set(cacheKey, configured)
			}
			return configured
		}
	}

	// 策略 1：具名 provider 路由（DeepSeek/OpenRouter/SiliconFlow/StepFun/Novita 等，硬编码端点/字段/单位）。
	// 命中官方域名即认为该 provider「负责本次查询」，其成功/确定性失败都直接返回，不再走后续通用试探链；
	// 仅当为瞬时失败时透传 Transient，交给前端 keep-last-good。
	if named, matched := s.queryNamedProviderBalance(ctx, httpClient, normalized, apiKey, request.SupplierID); matched {
		if named.Supported {
			s.providerBalanceCache.set(cacheKey, named)
		}
		return named
	}

	tracker := &transientTracker{}

	// 策略 2：OpenAI 计费端点（one-api/new-api 系中转站，返回 total+used）。
	if balance, ok := queryOpenAIBillingBalance(ctx, httpClient, normalized, apiKey, tracker); ok {
		// 仅缓存查询成功（Supported）的结果，不支持/失败结果不进缓存。
		s.providerBalanceCache.set(cacheKey, balance)
		return balance
	}

	// 策略 3：sub2api 风格 /v1/usage 端点（返回 remaining/unit/is_active）。
	if balance, ok := querySub2APIUsageBalance(ctx, httpClient, normalized, apiKey, tracker); ok {
		s.providerBalanceCache.set(cacheKey, balance)
		return balance
	}

	// 策略 4：NewAPI / OneAPI 风格用户信息端点。
	if balance, ok := queryNewAPIBalance(ctx, httpClient, normalized, apiKey, tracker); ok {
		s.providerBalanceCache.set(cacheKey, balance)
		return balance
	}

	// 策略 5：均不支持。不缓存，便于后续重试直接命中网络。
	// 若试探过程中发生过瞬时传输失败，标记 Transient=true，让前端保留上次成功值。
	return ProviderBalance{Supported: false, Transient: tracker.hit, Message: "该中转站不支持已知的余额查询接口"}
}

// queryOpenAIBillingBalance 查询 OpenAI 计费端点，成功时返回额度信息。
// 兼容两种常见部署：计费端点位于 API 根（含 /v1）下，或位于站点 origin 根下（不含 /v1）。
func queryOpenAIBillingBalance(ctx context.Context, httpClient *http.Client, normalizedBaseURL, apiKey string, tracker *transientTracker) (ProviderBalance, bool) {
	for _, root := range billingRootCandidates(normalizedBaseURL) {
		if balance, ok := queryOpenAIBillingBalanceAtRoot(ctx, httpClient, root, apiKey, tracker); ok {
			return balance, true
		}
	}
	return ProviderBalance{}, false
}

// querySub2APIUsageBalance 查询 sub2api 风格的 /usage 端点。
// sub2api（如 subapi.elias.ccwu.cc）在 {baseUrl}/v1/usage（GET + Bearer）返回：
//
//	{ "remaining": <数值>, "unit": "USD", "is_active": true }
//
// remaining 亦可能位于 quota.remaining 或 balance；unit 亦可能位于 quota.unit。
// 该端点直接给出剩余额度（无 total/used）。
func querySub2APIUsageBalance(ctx context.Context, httpClient *http.Client, normalizedBaseURL, apiKey string, tracker *transientTracker) (ProviderBalance, bool) {
	for _, usageURL := range sub2apiUsageCandidates(normalizedBaseURL) {
		body, ok := doProviderBalanceGET(ctx, httpClient, usageURL, apiKey, tracker)
		if !ok {
			continue
		}
		var payload struct {
			Remaining *float64 `json:"remaining"`
			Balance   *float64 `json:"balance"`
			Unit      string   `json:"unit"`
			IsActive  *bool    `json:"is_active"`
			IsValid   *bool    `json:"isValid"`
			Quota     *struct {
				Remaining *float64 `json:"remaining"`
				Unit      string   `json:"unit"`
			} `json:"quota"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			continue
		}
		remaining := payload.Remaining
		if remaining == nil && payload.Quota != nil {
			remaining = payload.Quota.Remaining
		}
		if remaining == nil {
			remaining = payload.Balance
		}
		if remaining == nil {
			// 无剩余额度字段，说明并非 sub2api usage 语义，尝试下一候选/策略。
			continue
		}
		unit := strings.TrimSpace(payload.Unit)
		if unit == "" && payload.Quota != nil {
			unit = strings.TrimSpace(payload.Quota.Unit)
		}
		if unit == "" {
			unit = "USD"
		}
		if payload.IsActive != nil && !*payload.IsActive {
			continue
		}
		if payload.IsValid != nil && !*payload.IsValid {
			continue
		}
		// 无限额度哨兵值：展示为不限额。
		if *remaining >= providerBalanceUnlimitedThreshold {
			return ProviderBalance{Supported: true, Source: "sub2api_usage", Currency: unit, Unlimited: true, Message: "额度不限"}, true
		}
		remainingValue := *remaining
		return ProviderBalance{
			Supported: true,
			Source:    "sub2api_usage",
			Currency:  unit,
			Remaining: &remainingValue,
			Message:   "查询成功",
		}, true
	}
	return ProviderBalance{}, false
}

// sub2apiUsageCandidates 返回 sub2api /usage 端点的候选 URL（去重保序）。
// 覆盖 baseURL 含或不含 /v1 的情况：优先 {apiRoot}/usage，再补 {origin}/v1/usage 与 {origin}/usage。
func sub2apiUsageCandidates(normalizedBaseURL string) []string {
	candidates := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	add := func(url string) {
		url = strings.TrimRight(strings.TrimSpace(url), "/")
		if url == "" {
			return
		}
		if _, ok := seen[url]; ok {
			return
		}
		seen[url] = struct{}{}
		candidates = append(candidates, url)
	}
	add(billingAPIRoot(normalizedBaseURL) + "/usage")
	if origin, err := billingOrigin(normalizedBaseURL); err == nil {
		add(origin + "/v1/usage")
		add(origin + "/usage")
	}
	return candidates
}

// billingRootCandidates 返回计费端点可能所在的根路径候选：
//   - API 根（保留 /v1，剥离 /models、/chat/completions 等尾段）
//   - 站点 origin 根（scheme+host，覆盖计费挂在根路径的部署）
//
// 去重并保持顺序，优先尝试 API 根。
func billingRootCandidates(normalizedBaseURL string) []string {
	roots := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	add := func(candidate string) {
		candidate = strings.TrimRight(strings.TrimSpace(candidate), "/")
		if candidate == "" {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		roots = append(roots, candidate)
	}
	add(billingAPIRoot(normalizedBaseURL))
	if origin, err := billingOrigin(normalizedBaseURL); err == nil {
		add(origin)
	}
	return roots
}

// queryOpenAIBillingBalanceAtRoot 在指定根路径下尝试 OpenAI 计费查询。
func queryOpenAIBillingBalanceAtRoot(ctx context.Context, httpClient *http.Client, root, apiKey string, tracker *transientTracker) (ProviderBalance, bool) {
	subURL := root + "/dashboard/billing/subscription"
	subBody, ok := doProviderBalanceGET(ctx, httpClient, subURL, apiKey, tracker)
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
		// 没有额度上限字段，说明该接口并非 OpenAI 计费语义，交给下一候选/策略。
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
	// 无限额度令牌：额度上限为哨兵值（≈1e8），展示为不限额而非天文数字。
	if *total >= providerBalanceUnlimitedThreshold {
		return ProviderBalance{
			Supported: true,
			Source:    "openai_billing",
			Currency:  "USD",
			Unlimited: true,
			Message:   "额度不限",
		}, true
	}
	if usageBody, usageOK := doProviderBalanceGET(ctx, httpClient, usageURL, apiKey, tracker); usageOK {
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
// 注意：多数 NewAPI 部署的 /api/user/self 需要 Web 登录态 access token，
// 用 sk 渠道密钥通常无法通过鉴权；此策略仅作为 OpenAI 计费端点之后的兜底。
func queryNewAPIBalance(ctx context.Context, httpClient *http.Client, normalizedBaseURL, apiKey string, tracker *transientTracker) (ProviderBalance, bool) {
	origin, err := billingOrigin(normalizedBaseURL)
	if err != nil {
		return ProviderBalance{}, false
	}
	body, ok := doNewAPIUserGET(ctx, httpClient, origin+"/api/user/self", apiKey, tracker)
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
	// 无限额度令牌：剩余额度为哨兵值（≈1e8），展示为不限额。
	if remaining >= providerBalanceUnlimitedThreshold {
		return ProviderBalance{
			Supported: true,
			Source:    "newapi",
			Currency:  "USD",
			Unlimited: true,
			Message:   "额度不限",
		}, true
	}
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

// doNewAPIUserGET 以 NewAPI 兼容方式发送用户信息查询：同时带 Bearer 与 New-API-User 头，
// 提升不同部署的兼容性。非 200/读取失败返回 ok=false。
func doNewAPIUserGET(ctx context.Context, httpClient *http.Client, endpoint, apiKey string, tracker *transientTracker) ([]byte, bool) {
	reqCtx, cancel := context.WithTimeout(ctx, providerBalancePerRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("New-API-User", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		// 传输层失败（连接/超时/上下文取消）为瞬时。
		markTransient(tracker)
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, providerBalanceMaxBodyBytes+1))
	if err != nil || len(body) > providerBalanceMaxBodyBytes {
		if err != nil {
			// 读体中断为瞬时；超限为确定性（不置位）。
			markTransient(tracker)
		}
		return nil, false
	}
	return body, true
}

// doProviderBalanceGET 发送带 Bearer 鉴权的 GET 请求，返回 200 响应体。
// 非 200、读取失败或超限时返回 ok=false，交由调用方回退到下一策略。
func doProviderBalanceGET(ctx context.Context, httpClient *http.Client, endpoint, apiKey string, tracker *transientTracker) ([]byte, bool) {
	reqCtx, cancel := context.WithTimeout(ctx, providerBalancePerRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		// 传输层失败（连接/超时/上下文取消）为瞬时。
		markTransient(tracker)
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, providerBalanceMaxBodyBytes+1))
	if err != nil {
		// 读体中断为瞬时。
		markTransient(tracker)
		return nil, false
	}
	if len(body) > providerBalanceMaxBodyBytes {
		return nil, false
	}
	return body, true
}

// markTransient 将瞬时失败记入 tracker；tracker 为 nil 时安全跳过（具名 provider 分支不用 tracker）。
func markTransient(tracker *transientTracker) {
	if tracker != nil {
		tracker.hit = true
	}
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
