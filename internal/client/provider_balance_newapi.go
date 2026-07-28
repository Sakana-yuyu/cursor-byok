// provider_balance_newapi.go：New API 专用余额查询（对齐 cc-switch New API 模板）。
//
// 与渠道 sk 兜底的 queryNewAPIBalance 不同：正式 New API 需要
//   Authorization: Bearer {{accessToken}}
//   New-Api-User: {{userId}}
// 访问 /api/user/self，并将 quota / used_quota 按 500000 换算为 USD。
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// queryNewAPICredentialBalance 使用访问令牌 + 用户 ID 查询 New API 余额。
// matched=true 表示已按 New API 协议处理（成功或确定性/瞬时失败都终结策略链）。
func queryNewAPICredentialBalance(ctx context.Context, httpClient *http.Client, normalizedBaseURL string, creds balanceCredentials) (ProviderBalance, bool) {
	accessToken := strings.TrimSpace(creds.AccessToken)
	userID := strings.TrimSpace(creds.UserID)
	if accessToken == "" || userID == "" {
		return ProviderBalance{}, false
	}
	origin, err := billingOrigin(normalizedBaseURL)
	if err != nil {
		return namedBalanceFail("newapi", "baseURL 无效："+err.Error(), false), true
	}
	endpoint := strings.TrimRight(origin, "/") + "/api/user/self"
	if override := strings.TrimSpace(creds.QueryURL); override != "" {
		endpoint = applyBalanceTemplate(override, "", origin, accessToken, userID)
	}

	body, status, transient, err := doNewAPICredentialGET(ctx, httpClient, endpoint, accessToken, userID)
	if err != nil {
		return namedBalanceFail("newapi", "网络错误："+err.Error(), transient), true
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return namedBalanceFail("newapi", fmt.Sprintf("鉴权失败（HTTP %d）：请检查访问令牌与用户 ID", status), false), true
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return namedBalanceFail("newapi", fmt.Sprintf("接口错误（HTTP %d）", status), false), true
	}

	var payload struct {
		Success *bool   `json:"success"`
		Message string  `json:"message"`
		Data    *struct {
			Group     string   `json:"group"`
			Quota     *float64 `json:"quota"`
			UsedQuota *float64 `json:"used_quota"`
		} `json:"data"`
	}
	if jsonErr := json.Unmarshal(body, &payload); jsonErr != nil {
		return namedBalanceFail("newapi", "响应解析失败："+jsonErr.Error(), false), true
	}
	if payload.Success != nil && !*payload.Success {
		msg := strings.TrimSpace(payload.Message)
		if msg == "" {
			msg = "查询失败"
		}
		return namedBalanceFail("newapi", msg, false), true
	}
	if payload.Data == nil || payload.Data.Quota == nil {
		return namedBalanceFail("newapi", "响应缺少 data.quota", false), true
	}

	remaining := *payload.Data.Quota / newAPIQuotaPerUnit
	planName := strings.TrimSpace(payload.Data.Group)
	if planName == "" {
		planName = "默认套餐"
	}
	if remaining >= providerBalanceUnlimitedThreshold {
		return ProviderBalance{
			Supported: true,
			Source:    "newapi",
			Currency:  "USD",
			Unlimited: true,
			PlanName:  planName,
			Message:   "额度不限",
		}, true
	}
	balance := ProviderBalance{
		Supported: true,
		Source:    "newapi",
		Currency:  "USD",
		Remaining: &remaining,
		PlanName:  planName,
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

func doNewAPICredentialGET(ctx context.Context, httpClient *http.Client, endpoint, accessToken, userID string) (body []byte, status int, transient bool, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, providerBalancePerRequestTimeout)
	defer cancel()
	req, reqErr := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if reqErr != nil {
		return nil, 0, false, reqErr
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("New-Api-User", userID)
	req.Header.Set("User-Agent", "cursor-byok/1.0")
	req.Header.Set("Accept", "application/json")

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