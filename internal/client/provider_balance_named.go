// provider_balance_named.go 提供「具名 provider 路由」余额查询（策略 0）。
//
// 与 provider_balance.go 的「中转站通用试探链」互补：本文件按 baseURL 域名硬编码分发到
// 各家官方余额端点（DeepSeek/StepFun/SiliconFlow/OpenRouter/Novita），端点/字段/单位换算
// 均对照参考实现 cc-switch 的 src-tauri/src/services/balance.rs 核对。
//
// 错误通道语义（对齐 ProviderBalance.Transient）：
//   - 传输层失败（连接/超时/读体中断）→ Transient:true，交前端 keep-last-good。
//   - 空 key / 鉴权失败(401/403) / 非 2xx / 非法 JSON → Transient:false，确定性失败。
//
// detectBalanceProvider 命中即代表「本 provider 负责本次查询」，其成功或确定性失败都终结
// 策略链，不再回落到通用试探；仅瞬时失败透传 Transient。
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// namedBalanceProvider 是具名 provider 的枚举标识。
type namedBalanceProvider int

const (
	balanceProviderNone namedBalanceProvider = iota
	balanceProviderDeepSeek
	balanceProviderStepFun
	balanceProviderSiliconFlowCN
	balanceProviderSiliconFlowEN
	balanceProviderOpenRouter
	balanceProviderNovita
	balanceProviderMoonshot
)

// detectBalanceProvider 按 baseURL 域名匹配具名 provider（对照 cc-switch detect_provider）。
// 返回 balanceProviderNone 表示未命中，交由后续策略处理。
func namedBalanceProviderForSupplier(supplierID string) namedBalanceProvider {
	switch strings.ToLower(strings.TrimSpace(supplierID)) {
	case "deepseek":
		return balanceProviderDeepSeek
	case "moonshot", "kimi":
		return balanceProviderMoonshot
	case "stepfun":
		return balanceProviderStepFun
	case "siliconflow":
		return balanceProviderSiliconFlowCN
	case "openrouter":
		return balanceProviderOpenRouter
	case "novita":
		return balanceProviderNovita
	default:
		return balanceProviderNone
	}
}

func detectBalanceProvider(baseURL string) namedBalanceProvider {
	url := strings.ToLower(baseURL)
	switch {
	case strings.Contains(url, "api.deepseek.com"):
		return balanceProviderDeepSeek
	case strings.Contains(url, "api.stepfun.ai") || strings.Contains(url, "api.stepfun.com"):
		return balanceProviderStepFun
	case strings.Contains(url, "api.siliconflow.cn"):
		return balanceProviderSiliconFlowCN
	case strings.Contains(url, "api.siliconflow.com"):
		return balanceProviderSiliconFlowEN
	case strings.Contains(url, "openrouter.ai"):
		return balanceProviderOpenRouter
	case strings.Contains(url, "api.novita.ai"):
		return balanceProviderNovita
	case strings.Contains(url, "api.moonshot.cn") || strings.Contains(url, "platform.moonshot.cn"):
		return balanceProviderMoonshot
	default:
		return balanceProviderNone
	}
}

// queryNamedProviderBalance 是具名 provider 入口：先按域名检测，命中则调对应查询函数。
// 返回 (balance, matched)；matched=false 表示未命中具名 provider，调用方继续后续策略。
func (s *ProxyService) queryNamedProviderBalance(ctx context.Context, httpClient *http.Client, normalizedBaseURL, apiKey, supplierID string) (ProviderBalance, bool) {
	provider := detectBalanceProvider(normalizedBaseURL)
	if provider == balanceProviderNone {
		return ProviderBalance{}, false
	}
	if configured := namedBalanceProviderForSupplier(supplierID); configured != balanceProviderNone && configured != provider {
		return ProviderBalance{}, false
	}
	switch provider {
	case balanceProviderDeepSeek:
		return queryDeepSeekBalance(ctx, httpClient, apiKey), true
	case balanceProviderStepFun:
		return queryStepFunBalance(ctx, httpClient, apiKey), true
	case balanceProviderSiliconFlowCN:
		return querySiliconFlowBalance(ctx, httpClient, apiKey, true), true
	case balanceProviderSiliconFlowEN:
		return querySiliconFlowBalance(ctx, httpClient, apiKey, false), true
	case balanceProviderOpenRouter:
		return queryOpenRouterBalance(ctx, httpClient, apiKey), true
	case balanceProviderNovita:
		return queryNovitaBalance(ctx, httpClient, apiKey), true
	case balanceProviderMoonshot:
		return queryMoonshotBalance(ctx, httpClient, apiKey), true
	default:
		return ProviderBalance{}, false
	}
}

// namedBalanceGET 发送带 Bearer 鉴权的 GET，区分瞬时/确定性失败：
//   - transport 错误 / 读体错误 → transient=true。
//   - 401/403 / 其他非 2xx → transient=false（body 供错误文案）。
//
// 成功返回 (body, status=200, transient=false, err=nil)。
func namedBalanceGET(ctx context.Context, httpClient *http.Client, endpoint, apiKey string) (body []byte, status int, transient bool, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, providerBalancePerRequestTimeout)
	defer cancel()
	req, reqErr := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if reqErr != nil {
		return nil, 0, false, reqErr
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, doErr := httpClient.Do(req)
	if doErr != nil {
		return nil, 0, true, doErr // 传输层失败：瞬时
	}
	defer resp.Body.Close()

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, providerBalanceMaxBodyBytes+1))
	if readErr != nil {
		return nil, resp.StatusCode, true, readErr // 读体中断：瞬时
	}
	if len(raw) > providerBalanceMaxBodyBytes {
		return nil, resp.StatusCode, false, fmt.Errorf("响应体过大")
	}
	return raw, resp.StatusCode, false, nil
}

// namedBalanceFail 构造具名 provider 的失败结果，统一填充 Source 与 Transient。
func namedBalanceFail(source, message string, transient bool) ProviderBalance {
	return ProviderBalance{Supported: false, Source: source, Transient: transient, Message: message}
}

// jsonNumber 从 json.Number 或字符串安全解析 f64（对照 cc-switch parse_f64_field，兼容字符串数值）。
func jsonNumberToFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// ── DeepSeek ────────────────────────────────────────────────
// GET https://api.deepseek.com/user/balance
// Response: { is_available, balance_infos: [{ currency, total_balance, ... }] }
func queryDeepSeekBalance(ctx context.Context, httpClient *http.Client, apiKey string) ProviderBalance {
	const source = "deepseek"
	body, status, transient, err := namedBalanceGET(ctx, httpClient, "https://api.deepseek.com/user/balance", apiKey)
	if err != nil {
		return namedBalanceFail(source, "网络错误："+err.Error(), transient)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return namedBalanceFail(source, fmt.Sprintf("鉴权失败（HTTP %d）", status), false)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return namedBalanceFail(source, fmt.Sprintf("接口错误（HTTP %d）", status), false)
	}
	var payload struct {
		IsAvailable  *bool `json:"is_available"`
		BalanceInfos []struct {
			Currency     string `json:"currency"`
			TotalBalance any    `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if jsonErr := json.Unmarshal(body, &payload); jsonErr != nil {
		return namedBalanceFail(source, "响应解析失败："+jsonErr.Error(), false)
	}
	if len(payload.BalanceInfos) == 0 {
		return namedBalanceFail(source, "响应缺少 balance_infos", false)
	}
	balances := make(map[string]float64, len(payload.BalanceInfos))
	order := make([]string, 0, len(payload.BalanceInfos))
	for _, info := range payload.BalanceInfos {
		value, ok := jsonNumberToFloat(info.TotalBalance)
		if !ok {
			continue
		}
		currency := strings.TrimSpace(info.Currency)
		if currency == "" {
			currency = "CNY"
		}
		if _, exists := balances[currency]; !exists {
			order = append(order, currency)
		}
		balances[currency] += value
	}
	if len(balances) == 0 {
		return namedBalanceFail(source, "响应缺少可解析的 total_balance", false)
	}
	selectedCurrency := order[0]
	for _, currency := range order {
		if strings.EqualFold(currency, "CNY") {
			selectedCurrency = currency
			break
		}
	}
	message := "查询成功"
	if len(balances) > 1 {
		message = "查询成功（多币种，仅显示 " + selectedCurrency + "）"
	}
	if payload.IsAvailable != nil && !*payload.IsAvailable {
		message = "Insufficient balance"
	}
	rv := balances[selectedCurrency]
	return ProviderBalance{
		Supported: true,
		Source:    source,
		Currency:  selectedCurrency,
		Remaining: &rv,
		Message:   message,
	}
}

// ── Moonshot / Kimi ─────────────────────────────────────────
// Moonshot exposes the account quota at /v1/users/me/balance. The response
// uses a USD amount in the current API; retain the generic parser fallback for
// deployments that return balance or available_balance instead.
func queryMoonshotBalance(ctx context.Context, httpClient *http.Client, apiKey string) ProviderBalance {
	const source = "moonshot"
	body, status, transient, err := namedBalanceGET(ctx, httpClient, "https://api.moonshot.cn/v1/users/me/balance", apiKey)
	if err != nil {
		return namedBalanceFail(source, "网络错误："+err.Error(), transient)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return namedBalanceFail(source, fmt.Sprintf("鉴权失败（HTTP %d）", status), false)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return namedBalanceFail(source, fmt.Sprintf("接口错误（HTTP %d）", status), false)
	}
	var payload map[string]any
	if jsonErr := json.Unmarshal(body, &payload); jsonErr != nil {
		return namedBalanceFail(source, "响应解析失败："+jsonErr.Error(), false)
	}
	remaining, ok := firstJSONNumber(payload, "balance", "available_balance", "availableBalance", "remaining")
	if !ok {
		if data, nested := payload["data"].(map[string]any); nested {
			remaining, ok = firstJSONNumber(data, "balance", "available_balance", "availableBalance", "remaining")
		}
	}
	if !ok {
		return namedBalanceFail(source, "响应缺少可解析的余额字段", false)
	}
	message := "查询成功"
	if remaining <= 0 {
		message = "No balance remaining"
	}
	return ProviderBalance{
		Supported: true,
		Source:    source,
		Currency:  "USD",
		Remaining: &remaining,
		Message:   message,
	}
}

func firstJSONNumber(payload map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		if value, ok := jsonNumberToFloat(payload[key]); ok {
			return value, true
		}
	}
	return 0, false
}

// ── StepFun ─────────────────────────────────────────────────
// GET https://api.stepfun.com/v1/accounts → { balance, ... }（单位 CNY）
func queryStepFunBalance(ctx context.Context, httpClient *http.Client, apiKey string) ProviderBalance {
	const source = "stepfun"
	body, status, transient, err := namedBalanceGET(ctx, httpClient, "https://api.stepfun.com/v1/accounts", apiKey)
	if err != nil {
		return namedBalanceFail(source, "网络错误："+err.Error(), transient)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return namedBalanceFail(source, fmt.Sprintf("鉴权失败（HTTP %d）", status), false)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return namedBalanceFail(source, fmt.Sprintf("接口错误（HTTP %d）", status), false)
	}
	var payload struct {
		Balance any `json:"balance"`
	}
	if jsonErr := json.Unmarshal(body, &payload); jsonErr != nil {
		return namedBalanceFail(source, "响应解析失败："+jsonErr.Error(), false)
	}
	remaining, _ := jsonNumberToFloat(payload.Balance)
	rv := remaining
	return ProviderBalance{
		Supported: true,
		Source:    source,
		Currency:  "CNY",
		Remaining: &rv,
		Message:   "查询成功",
	}
}

// ── SiliconFlow ─────────────────────────────────────────────
// GET https://api.siliconflow.cn/v1/user/info (or .com) → { data: { totalBalance } }
func querySiliconFlowBalance(ctx context.Context, httpClient *http.Client, apiKey string, isCN bool) ProviderBalance {
	const source = "siliconflow"
	domain := "api.siliconflow.com"
	currency := "USD"
	if isCN {
		domain = "api.siliconflow.cn"
		currency = "CNY"
	}
	body, status, transient, err := namedBalanceGET(ctx, httpClient, "https://"+domain+"/v1/user/info", apiKey)
	if err != nil {
		return namedBalanceFail(source, "网络错误："+err.Error(), transient)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return namedBalanceFail(source, fmt.Sprintf("鉴权失败（HTTP %d）", status), false)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return namedBalanceFail(source, fmt.Sprintf("接口错误（HTTP %d）", status), false)
	}
	var payload struct {
		Data *struct {
			TotalBalance any `json:"totalBalance"`
		} `json:"data"`
	}
	if jsonErr := json.Unmarshal(body, &payload); jsonErr != nil {
		return namedBalanceFail(source, "响应解析失败："+jsonErr.Error(), false)
	}
	if payload.Data == nil {
		return namedBalanceFail(source, "响应缺少 data 字段", false)
	}
	remaining, _ := jsonNumberToFloat(payload.Data.TotalBalance)
	rv := remaining
	return ProviderBalance{
		Supported: true,
		Source:    source,
		Currency:  currency,
		Remaining: &rv,
		Message:   "查询成功",
	}
}

// ── OpenRouter ──────────────────────────────────────────────
// GET https://openrouter.ai/api/v1/credits → { data: { total_credits, total_usage } }
// remaining = total_credits - total_usage（USD）
func queryOpenRouterBalance(ctx context.Context, httpClient *http.Client, apiKey string) ProviderBalance {
	const source = "openrouter"
	body, status, transient, err := namedBalanceGET(ctx, httpClient, "https://openrouter.ai/api/v1/credits", apiKey)
	if err != nil {
		return namedBalanceFail(source, "网络错误："+err.Error(), transient)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return namedBalanceFail(source, fmt.Sprintf("鉴权失败（HTTP %d）", status), false)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return namedBalanceFail(source, fmt.Sprintf("接口错误（HTTP %d）", status), false)
	}
	var root map[string]any
	if jsonErr := json.Unmarshal(body, &root); jsonErr != nil {
		return namedBalanceFail(source, "响应解析失败："+jsonErr.Error(), false)
	}
	data := root
	if wrapped, ok := root["data"].(map[string]any); ok {
		data = wrapped
	}
	totalCredits, _ := jsonNumberToFloat(data["total_credits"])
	totalUsage, _ := jsonNumberToFloat(data["total_usage"])
	remaining := totalCredits - totalUsage
	total := totalCredits
	used := totalUsage
	message := "查询成功"
	if remaining <= 0 {
		message = "No credits remaining"
	}
	return ProviderBalance{
		Supported: true,
		Source:    source,
		Currency:  "USD",
		Total:     &total,
		Used:      &used,
		Remaining: &remaining,
		Message:   message,
	}
}

// ── Novita AI ───────────────────────────────────────────────
// GET https://api.novita.ai/v3/user/balance → { availableBalance, ... }
// 金额单位 0.0001 USD，需 /10000 转 USD。
func queryNovitaBalance(ctx context.Context, httpClient *http.Client, apiKey string) ProviderBalance {
	const source = "novita"
	body, status, transient, err := namedBalanceGET(ctx, httpClient, "https://api.novita.ai/v3/user/balance", apiKey)
	if err != nil {
		return namedBalanceFail(source, "网络错误："+err.Error(), transient)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return namedBalanceFail(source, fmt.Sprintf("鉴权失败（HTTP %d）", status), false)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return namedBalanceFail(source, fmt.Sprintf("接口错误（HTTP %d）", status), false)
	}
	var payload struct {
		AvailableBalance any `json:"availableBalance"`
	}
	if jsonErr := json.Unmarshal(body, &payload); jsonErr != nil {
		return namedBalanceFail(source, "响应解析失败："+jsonErr.Error(), false)
	}
	raw, _ := jsonNumberToFloat(payload.AvailableBalance)
	remaining := raw / 10000.0
	message := "查询成功"
	if remaining <= 0 {
		message = "No balance remaining"
	}
	return ProviderBalance{
		Supported: true,
		Source:    source,
		Currency:  "USD",
		Remaining: &remaining,
		Message:   message,
	}
}
