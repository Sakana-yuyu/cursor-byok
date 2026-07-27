// provider_balance_configured.go 提供「可配置查询」能力（显式配置优先）。
//
// 当用户想在无 JS 运行时的情况下支持任意 provider 时，
// 用户可在 ModelAdapterConfig 上配置 BalanceQueryURL/BalanceQueryField/BalanceQueryHeaders：
// 本文件按配置发一次 GET，并按点分路径（dot-path）从 JSON 响应取值。
//
// 「模板化字段映射」而非脚本执行：URL 与 Header 值支持 {{apiKey}}、{{baseUrl}} 占位符替换。
// dot-path 支持对象字段与数组下标（如 data.infos.0.total_balance）。
package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	serverconfig "cursor/internal/backend/server/config"
)

// queryConfiguredBalance 是策略 0.5 入口：查找匹配的 adapter 配置，若配置了 BalanceQueryURL 则执行。
// 返回 (balance, matched)；matched=false 表示未配置或未找到匹配 adapter，调用方继续后续策略。
func (s *ProxyService) queryConfiguredBalance(ctx context.Context, httpClient *http.Client, request ProviderBalanceRequest, normalizedBaseURL, apiKey string) (ProviderBalance, bool) {
	adapter, ok := s.findAdapterForBalance(request.Type, request.SupplierID, normalizedBaseURL, apiKey)
	if !ok {
		return ProviderBalance{}, false
	}
	return s.queryConfiguredBalanceWithAdapter(ctx, httpClient, adapter, normalizedBaseURL, apiKey)
}

func (s *ProxyService) queryConfiguredBalanceWithAdapter(ctx context.Context, httpClient *http.Client, adapter serverconfig.ModelAdapterConfig, normalizedBaseURL, apiKey string) (ProviderBalance, bool) {
	queryURL := strings.TrimSpace(adapter.BalanceQueryURL)
	field := strings.TrimSpace(adapter.BalanceQueryField)
	if queryURL == "" || field == "" {
		return ProviderBalance{}, false
	}

	const source = "configured"
	endpoint := applyBalanceTemplate(queryURL, apiKey, normalizedBaseURL)
	body, status, transient, err := namedBalanceGETWithHeaders(ctx, httpClient, endpoint, apiKey, adapter.BalanceQueryHeaders, normalizedBaseURL)
	if err != nil {
		return namedBalanceFail(source, "网络错误："+err.Error(), transient), true
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return namedBalanceFail(source, fmt.Sprintf("鉴权失败（HTTP %d）", status), false), true
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return namedBalanceFail(source, fmt.Sprintf("接口错误（HTTP %d）", status), false), true
	}
	var root any
	if jsonErr := json.Unmarshal(body, &root); jsonErr != nil {
		return namedBalanceFail(source, "响应解析失败："+jsonErr.Error(), false), true
	}
	value, found := lookupDotPath(root, field)
	if !found {
		return namedBalanceFail(source, "响应中未找到字段："+field, false), true
	}
	remaining, ok := jsonNumberToFloat(value)
	if !ok {
		return namedBalanceFail(source, "字段值非数值："+field, false), true
	}
	rv := remaining
	return ProviderBalance{
		Supported: true,
		Source:    source,
		Currency:  "USD",
		Remaining: &rv,
		Message:   "查询成功",
	}, true
}

// findAdapterForBalance 从持久化配置里找出 supplierID + type + baseURL + apiKey 匹配的 adapter。
// supplierID 为空或 custom 时兼容旧调用方；具名 supplier 只接受同名配置，避免同一连接参数下串用余额接口。
func (s *ProxyService) findAdapterForBalance(reqType, supplierID, normalizedBaseURL, apiKey string) (serverconfig.ModelAdapterConfig, bool) {
	cfg, err := s.LoadUserConfig()
	if err != nil {
		return serverconfig.ModelAdapterConfig{}, false
	}
	wantType := strings.TrimSpace(strings.ToLower(reqType))
	wantSupplier := strings.TrimSpace(strings.ToLower(supplierID))
	wantKey := strings.TrimSpace(apiKey)
	for _, adapter := range cfg.ModelAdapters {
		if strings.TrimSpace(adapter.BalanceQueryURL) == "" || strings.TrimSpace(adapter.BalanceQueryField) == "" {
			continue
		}
		if wantType != "" && strings.TrimSpace(strings.ToLower(adapter.Type)) != wantType {
			continue
		}
		adapterSupplier := strings.TrimSpace(strings.ToLower(adapter.SupplierID))
		if wantSupplier != "" && wantSupplier != "custom" && adapterSupplier != wantSupplier {
			continue
		}
		if wantKey != "" && strings.TrimSpace(adapter.APIKey) != wantKey {
			continue
		}
		// adapter.BaseURL 已在持久化时归一化，直接比较。
		if strings.EqualFold(strings.TrimRight(adapter.BaseURL, "/"), strings.TrimRight(normalizedBaseURL, "/")) {
			return adapter, true
		}
	}
	return serverconfig.ModelAdapterConfig{}, false
}

func configuredBalanceCacheKey(baseKey string, adapter serverconfig.ModelAdapterConfig) string {
	parts := []string{
		strings.TrimSpace(adapter.BalanceQueryURL),
		strings.TrimSpace(adapter.BalanceQueryField),
	}
	keys := make([]string, 0, len(adapter.BalanceQueryHeaders))
	for key := range adapter.BalanceQueryHeaders {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, strings.TrimSpace(key)+"="+adapter.BalanceQueryHeaders[key])
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return baseKey + "|balance_config=" + hex.EncodeToString(sum[:])
}

// applyBalanceTemplate 替换 {{apiKey}}、{{baseUrl}} 占位符。
func applyBalanceTemplate(tmpl, apiKey, baseURL string) string {
	replacer := strings.NewReplacer(
		"{{apiKey}}", apiKey,
		"{{baseUrl}}", baseURL,
		"{{baseURL}}", baseURL,
	)
	return replacer.Replace(tmpl)
}

// namedBalanceGETWithHeaders 与 namedBalanceGET 类似，但用配置的 headers（占位符替换后）。
// 若未显式提供 Authorization，则默认补 Bearer；始终补 Accept。
func namedBalanceGETWithHeaders(ctx context.Context, httpClient *http.Client, endpoint, apiKey string, headers map[string]string, baseURL string) (body []byte, status int, transient bool, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, providerBalancePerRequestTimeout)
	defer cancel()
	req, reqErr := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if reqErr != nil {
		return nil, 0, false, reqErr
	}
	hasAuth := false
	for k, v := range headers {
		if strings.EqualFold(strings.TrimSpace(k), "Authorization") {
			hasAuth = true
		}
		req.Header.Set(k, applyBalanceTemplate(v, apiKey, baseURL))
	}
	if !hasAuth {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}

	resp, doErr := httpClient.Do(req)
	if doErr != nil {
		return nil, 0, true, doErr
	}
	defer resp.Body.Close()
	buf, readErr := io.ReadAll(io.LimitReader(resp.Body, providerBalanceMaxBodyBytes+1))
	if readErr != nil {
		return nil, resp.StatusCode, true, readErr
	}
	if len(buf) > providerBalanceMaxBodyBytes {
		return nil, resp.StatusCode, false, fmt.Errorf("响应体过大")
	}
	return buf, resp.StatusCode, false, nil
}

// lookupDotPath 按点分路径从解析后的 JSON（map[string]any / []any）取值。
// 支持数组下标：如 "balance_infos.0.total_balance"。
func lookupDotPath(root any, path string) (any, bool) {
	cur := root
	for _, seg := range strings.Split(path, ".") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			idx, convErr := strconv.Atoi(seg)
			if convErr != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}
