// provider_balance_coding_plan.go：Token Plan / Coding Plan 额度查询。
// 对照 cc-switch src-tauri/src/services/coding_plan.rs + codingPlanProviders.ts。
//
// 支持：
//   - Kimi For Coding
//   - Zhipu GLM / Zhipu GLM Team（团队版须显式 BalanceCodingPlanProvider=zhipu_team）
//   - MiniMax
//   - ZenMux
//
// 火山方舟 Coding Plan 依赖 AK/SK 签名，本仓库暂提示配置，不在此自动查询。
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type codingPlanProvider int

const (
	codingPlanNone codingPlanProvider = iota
	codingPlanKimi
	codingPlanZhipu
	codingPlanZhipuTeam
	codingPlanMiniMaxCN
	codingPlanMiniMaxEN
	codingPlanZenMux
	codingPlanVolcengine
)

func detectCodingPlanProvider(baseURL, explicit string) codingPlanProvider {
	switch strings.ToLower(strings.TrimSpace(explicit)) {
	case "kimi":
		return codingPlanKimi
	case "zhipu":
		return codingPlanZhipu
	case "zhipu_team":
		return codingPlanZhipuTeam
	case "minimax":
		if strings.Contains(strings.ToLower(baseURL), "minimax.io") {
			return codingPlanMiniMaxEN
		}
		return codingPlanMiniMaxCN
	case "zenmux":
		return codingPlanZenMux
	case "volcengine":
		return codingPlanVolcengine
	}

	url := strings.ToLower(baseURL)
	switch {
	case strings.Contains(url, "api.kimi.com/coding"):
		return codingPlanKimi
	case strings.Contains(url, "open.bigmodel.cn") || strings.Contains(url, "bigmodel.cn"):
		// 个人版优先；团队版 base_url 相同，须显式 codingPlanProvider。
		return codingPlanZhipu
	case strings.Contains(url, "api.z.ai"):
		return codingPlanZhipu
	case strings.Contains(url, "api.minimaxi.com"):
		return codingPlanMiniMaxCN
	case strings.Contains(url, "api.minimax.io") || strings.Contains(url, "api.minimax.com"):
		return codingPlanMiniMaxEN
	case strings.Contains(url, "zenmux"):
		return codingPlanZenMux
	case strings.Contains(url, "volces.com/api/coding"):
		return codingPlanVolcengine
	default:
		return codingPlanNone
	}
}

// queryCodingPlanBalance 查询 Token Plan 套餐进度。
// matched=true 表示命中 Coding Plan 供应商；结果用 Remaining=剩余百分比、Used=已用百分比、Currency="%"。
func queryCodingPlanBalance(ctx context.Context, httpClient *http.Client, baseURL, apiKey, explicitProvider string) (ProviderBalance, bool) {
	provider := detectCodingPlanProvider(baseURL, explicitProvider)
	if provider == codingPlanNone {
		return ProviderBalance{}, false
	}
	if strings.TrimSpace(apiKey) == "" {
		return namedBalanceFail("token_plan", "缺少 apiKey，无法查询 Token Plan", false), true
	}
	if provider == codingPlanVolcengine {
		return namedBalanceFail("token_plan", "火山方舟 Coding Plan 需要控制面 AK/SK，暂请使用自定义余额查询或官方控制台", false), true
	}

	switch provider {
	case codingPlanKimi:
		return queryKimiCodingPlan(ctx, httpClient, apiKey), true
	case codingPlanZhipu, codingPlanZhipuTeam:
		return queryZhipuCodingPlan(ctx, httpClient, baseURL, apiKey, provider == codingPlanZhipuTeam), true
	case codingPlanMiniMaxCN:
		return queryMiniMaxCodingPlan(ctx, httpClient, apiKey, true), true
	case codingPlanMiniMaxEN:
		return queryMiniMaxCodingPlan(ctx, httpClient, apiKey, false), true
	case codingPlanZenMux:
		return queryZenMuxCodingPlan(ctx, httpClient, baseURL, apiKey), true
	default:
		return ProviderBalance{}, false
	}
}

func queryKimiCodingPlan(ctx context.Context, httpClient *http.Client, apiKey string) ProviderBalance {
	body, status, transient, err := codingPlanGET(ctx, httpClient, "https://api.kimi.com/coding/v1/usages", map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Accept":        "application/json",
	})
	if err != nil {
		return namedBalanceFail("token_plan", "网络错误："+err.Error(), transient)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return namedBalanceFail("token_plan", fmt.Sprintf("鉴权失败（HTTP %d）", status), false)
	}
	if status < 200 || status >= 300 {
		return namedBalanceFail("token_plan", fmt.Sprintf("接口错误（HTTP %d）", status), false)
	}
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return namedBalanceFail("token_plan", "响应解析失败", false)
	}

	return codingPlanBalanceFromTiers("Kimi For Coding", parseKimiTiers(root))
}

func parseKimiTiers(root map[string]any) []codingPlanTier {
	var tiers []codingPlanTier
	positions := make(map[string]int)
	appendTier := func(tier codingPlanTier) {
		if position, exists := positions[tier.ID]; exists {
			current := tiers[position]
			if (!current.Known && tier.Known) || (current.Known && tier.Known && tier.Utilization > current.Utilization) {
				tiers[position] = tier
			}
			return
		}
		positions[tier.ID] = len(tiers)
		tiers = append(tiers, tier)
	}

	if limits, ok := root["limits"].([]any); ok {
		for index, item := range limits {
			obj, _ := item.(map[string]any)
			detail, _ := obj["detail"].(map[string]any)
			if detail == nil {
				continue
			}
			id, label := kimiWindowIdentity(obj["window"], index)
			utilization, known := quotaUtilizationPercent(detail["limit"], detail["remaining"])
			appendTier(codingPlanTier{
				Known:       known,
				ID:          id,
				Name:        label,
				Utilization: utilization,
				ResetsAt:    anyToReset(detail["resetTime"]),
			})
		}
	}
	if usage, ok := root["usage"].(map[string]any); ok {
		utilization, known := quotaUtilizationPercent(usage["limit"], usage["remaining"])
		appendTier(codingPlanTier{
			Known:       known,
			ID:          "7d",
			Name:        "周限额",
			Utilization: utilization,
			ResetsAt:    anyToReset(usage["resetTime"]),
		})
	}
	return tiers
}

func kimiWindowIdentity(value any, index int) (string, string) {
	window, _ := value.(map[string]any)
	duration, durationOK := asInt64(window["duration"])
	unit, _ := window["timeUnit"].(string)
	if durationOK && duration > 0 {
		switch strings.ToUpper(strings.TrimSpace(unit)) {
		case "TIME_UNIT_MINUTE", "MINUTE", "MINUTES":
			if duration%60 == 0 {
				hours := duration / 60
				return fmt.Sprintf("%dh", hours), fmt.Sprintf("%d小时", hours)
			}
			return fmt.Sprintf("%dm", duration), fmt.Sprintf("%d分钟", duration)
		case "TIME_UNIT_HOUR", "HOUR", "HOURS":
			return fmt.Sprintf("%dh", duration), fmt.Sprintf("%d小时", duration)
		case "TIME_UNIT_DAY", "DAY", "DAYS":
			return fmt.Sprintf("%dd", duration), fmt.Sprintf("%d天", duration)
		case "TIME_UNIT_SECOND", "SECOND", "SECONDS":
			return fmt.Sprintf("%ds", duration), fmt.Sprintf("%d秒", duration)
		}
	}
	return fmt.Sprintf("limit-%d", index+1), fmt.Sprintf("额度窗口 %d", index+1)
}

func queryZhipuCodingPlan(ctx context.Context, httpClient *http.Client, baseURL, apiKey string, team bool) ProviderBalance {
	host := "https://api.z.ai"
	if strings.Contains(strings.ToLower(baseURL), "bigmodel.cn") {
		host = "https://open.bigmodel.cn"
	}
	endpoint := host + "/api/monitor/usage/quota/limit"
	// 智谱不加 Bearer 前缀（对齐 cc-switch）。
	body, status, transient, err := codingPlanGET(ctx, httpClient, endpoint, map[string]string{
		"Authorization":   apiKey,
		"Content-Type":    "application/json",
		"Accept-Language": "en-US,en",
	})
	if err != nil {
		return namedBalanceFail("token_plan", "网络错误："+err.Error(), transient)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return namedBalanceFail("token_plan", fmt.Sprintf("鉴权失败（HTTP %d）", status), false)
	}
	if status < 200 || status >= 300 {
		return namedBalanceFail("token_plan", fmt.Sprintf("接口错误（HTTP %d）", status), false)
	}
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return namedBalanceFail("token_plan", "响应解析失败", false)
	}
	if success, ok := root["success"].(bool); ok && !success {
		msg, _ := root["msg"].(string)
		if strings.TrimSpace(msg) == "" {
			msg = "查询失败"
		}
		return namedBalanceFail("token_plan", msg, false)
	}
	data, _ := root["data"].(map[string]any)
	if data == nil {
		return namedBalanceFail("token_plan", "响应缺少 data", false)
	}
	tiers := parseZhipuTiers(data)
	label := "Zhipu GLM"
	if team {
		label = "Zhipu GLM Team"
	}
	if level, _ := data["level"].(string); strings.TrimSpace(level) != "" {
		label = label + " · " + strings.TrimSpace(level)
	}
	return codingPlanBalanceFromTiers(label, tiers)
}

func parseZhipuTiers(data map[string]any) []codingPlanTier {
	type entry struct {
		resetMS *int64
		pct     float64
		known   bool
		reset   string
		kind    int // 1=5h 2=week 0=unknown
	}
	var five, weekly *entry
	var unknown []entry
	limits, _ := data["limits"].([]any)
	for _, raw := range limits {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		typ, _ := item["type"].(string)
		if !strings.EqualFold(typ, "TOKENS_LIMIT") {
			continue
		}
		pct, known := asFloat(item["percentage"])
		var resetMS *int64
		if v, ok := asInt64(item["nextResetTime"]); ok {
			resetMS = &v
		}
		e := entry{resetMS: resetMS, pct: pct, known: known, reset: millisToISO(resetMS), kind: 0}
		switch unit, _ := asInt64(item["unit"]); unit {
		case 3:
			e.kind = 1
		case 6:
			e.kind = 2
		}
		switch e.kind {
		case 1:
			if five == nil {
				cp := e
				five = &cp
			}
		case 2:
			if weekly == nil {
				cp := e
				weekly = &cp
			}
		default:
			unknown = append(unknown, e)
		}
	}
	sort.SliceStable(unknown, func(i, j int) bool {
		ai, bi := unknown[i].resetMS != nil, unknown[j].resetMS != nil
		if ai != bi {
			return !ai && bi // 无 reset 优先当 5h
		}
		if unknown[i].resetMS == nil || unknown[j].resetMS == nil {
			return false
		}
		return *unknown[i].resetMS < *unknown[j].resetMS
	})
	for _, e := range unknown {
		if five == nil {
			cp := e
			five = &cp
			continue
		}
		if weekly == nil {
			cp := e
			weekly = &cp
		}
	}
	var tiers []codingPlanTier
	if five != nil {
		tiers = append(tiers, codingPlanTier{Known: five.known, ID: "5h", Name: "5小时", Utilization: five.pct, ResetsAt: five.reset})
	}
	if weekly != nil {
		tiers = append(tiers, codingPlanTier{Known: weekly.known, ID: "7d", Name: "周限额", Utilization: weekly.pct, ResetsAt: weekly.reset})
	}
	return tiers
}

func queryMiniMaxCodingPlan(ctx context.Context, httpClient *http.Client, apiKey string, isCN bool) ProviderBalance {
	domain := "api.minimax.io"
	if isCN {
		domain = "api.minimaxi.com"
	}
	endpoint := fmt.Sprintf("https://%s/v1/api/openplatform/coding_plan/remains", domain)
	body, status, transient, err := codingPlanGET(ctx, httpClient, endpoint, map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Content-Type":  "application/json",
	})
	if err != nil {
		return namedBalanceFail("token_plan", "网络错误："+err.Error(), transient)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return namedBalanceFail("token_plan", fmt.Sprintf("鉴权失败（HTTP %d）", status), false)
	}
	if status < 200 || status >= 300 {
		return namedBalanceFail("token_plan", fmt.Sprintf("接口错误（HTTP %d）", status), false)
	}
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return namedBalanceFail("token_plan", "响应解析失败", false)
	}
	if base, ok := root["base_resp"].(map[string]any); ok {
		code := anyToFloat(base["status_code"], -1)
		if code != 0 {
			msg, _ := base["status_msg"].(string)
			if strings.TrimSpace(msg) == "" {
				msg = "Unknown error"
			}
			return namedBalanceFail("token_plan", fmt.Sprintf("API error (code %v): %s", code, msg), false)
		}
	}
	return codingPlanBalanceFromTiers("MiniMax", parseMiniMaxTiers(root))
}

func parseMiniMaxTiers(root map[string]any) []codingPlanTier {
	var tiers []codingPlanTier
	remains, _ := root["model_remains"].([]any)
	var item map[string]any
	for _, raw := range remains {
		obj, _ := raw.(map[string]any)
		if obj == nil {
			continue
		}
		if name, _ := obj["model_name"].(string); name == "general" {
			item = obj
			break
		}
	}
	if item == nil {
		return tiers
	}
	if remainPct, ok := asFloat(item["current_interval_remaining_percent"]); ok {
		var reset string
		if ms, ok := asInt64(item["end_time"]); ok {
			v := ms
			reset = millisToISO(&v)
		}
		tiers = append(tiers, codingPlanTier{Known: true, ID: "5h", Name: "5小时", Utilization: 100 - remainPct, ResetsAt: reset})
	}
	if status, _ := asInt64(item["current_weekly_status"]); status == 1 {
		if remainPct, ok := asFloat(item["current_weekly_remaining_percent"]); ok {
			var reset string
			if ms, ok := asInt64(item["weekly_end_time"]); ok {
				v := ms
				reset = millisToISO(&v)
			}
			tiers = append(tiers, codingPlanTier{Known: true, ID: "7d", Name: "周限额", Utilization: 100 - remainPct, ResetsAt: reset})
		}
	}
	return tiers
}

func queryZenMuxCodingPlan(ctx context.Context, httpClient *http.Client, baseURL, apiKey string) ProviderBalance {
	// ZenMux：用量 URL 常由用户配置；此处尝试常见路径，否则对 baseURL 本身 GET（与 cc-switch 行为接近）。
	endpoint := strings.TrimRight(baseURL, "/")
	if !strings.Contains(strings.ToLower(endpoint), "quota") && !strings.Contains(strings.ToLower(endpoint), "usage") {
		// 常见：https://xxx.zenmux.ai/api/v1/quota
		if u, err := http.NewRequest(http.MethodGet, endpoint, nil); err == nil && u.URL != nil {
			// keep endpoint as-is; many ZenMux deployments put quota path in baseURL already
		}
	}
	body, status, transient, err := codingPlanGET(ctx, httpClient, endpoint, map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Accept":        "application/json",
	})
	if err != nil {
		return namedBalanceFail("token_plan", "网络错误："+err.Error(), transient)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return namedBalanceFail("token_plan", fmt.Sprintf("鉴权失败（HTTP %d）", status), false)
	}
	if status < 200 || status >= 300 {
		return namedBalanceFail("token_plan", fmt.Sprintf("接口错误（HTTP %d）", status), false)
	}
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return namedBalanceFail("token_plan", "响应解析失败", false)
	}
	if success, ok := root["success"].(bool); ok && !success {
		msg, _ := root["message"].(string)
		if strings.TrimSpace(msg) == "" {
			msg = "查询失败"
		}
		return namedBalanceFail("token_plan", msg, false)
	}
	data, _ := root["data"].(map[string]any)
	if data == nil {
		return namedBalanceFail("token_plan", "响应缺少 data", false)
	}
	tiers := parseZenMuxTiers(data)
	label := "ZenMux"
	if plan, ok := data["plan"].(map[string]any); ok {
		if tier, _ := plan["tier"].(string); strings.TrimSpace(tier) != "" {
			status, _ := data["account_status"].(string)
			label = strings.TrimSpace(tier)
			if strings.TrimSpace(status) != "" {
				label = label + " (" + status + ")"
			}
		}
	}
	return codingPlanBalanceFromTiers(label, tiers)
}

func parseZenMuxTiers(data map[string]any) []codingPlanTier {
	var tiers []codingPlanTier
	if q5, ok := data["quota_5_hour"].(map[string]any); ok {
		value, known := asFloat(q5["usage_percentage"])
		reset, _ := q5["resets_at"].(string)
		tiers = append(tiers, codingPlanTier{Known: known, ID: "5h", Name: "5小时", Utilization: value * 100, ResetsAt: reset})
	}
	if q7, ok := data["quota_7_day"].(map[string]any); ok {
		value, known := asFloat(q7["usage_percentage"])
		reset, _ := q7["resets_at"].(string)
		tiers = append(tiers, codingPlanTier{Known: known, ID: "7d", Name: "周限额", Utilization: value * 100, ResetsAt: reset})
	}
	return tiers
}

type codingPlanTier struct {
	Known       bool
	ID          string
	Name        string
	Utilization float64 // 已用百分比 0-100
	ResetsAt    string
}

func codingPlanBalanceFromTiers(planName string, tiers []codingPlanTier) ProviderBalance {
	if len(tiers) == 0 {
		return namedBalanceFail("token_plan", "未返回可用额度窗口", false)
	}

	windows := make([]ProviderUsageWindow, 0, len(tiers))
	parts := make([]string, 0, len(tiers))
	primaryIndex := -1
	for index, tier := range tiers {
		window := codingPlanUsageWindow(tier, index)
		windows = append(windows, window)
		line := window.Label + " 用量未知"
		if window.Used != nil {
			line = fmt.Sprintf("%s 已用 %.0f%%", window.Label, *window.Used)
			if primaryIndex < 0 {
				primaryIndex = index
			}
		}
		if strings.TrimSpace(window.ResetsAt) != "" {
			line += " · 重置 " + window.ResetsAt
		}
		parts = append(parts, line)
	}
	if primaryIndex < 0 {
		return namedBalanceFail("token_plan", "额度窗口缺少可识别的用量", false)
	}

	primary := windows[primaryIndex]
	return ProviderBalance{
		Supported: true,
		Source:    "token_plan",
		Currency:  "%",
		Remaining: primary.Remaining,
		Used:      primary.Used,
		Total:     primary.Limit,
		PlanName:  planName,
		Windows:   windows,
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		Message:   strings.Join(parts, "；"),
	}
}

func codingPlanUsageWindow(tier codingPlanTier, index int) ProviderUsageWindow {
	id := strings.TrimSpace(tier.ID)
	if id == "" {
		id = fmt.Sprintf("window-%d", index+1)
	}
	label := strings.TrimSpace(tier.Name)
	if label == "" {
		label = fmt.Sprintf("额度窗口 %d", index+1)
	}
	window := ProviderUsageWindow{
		ID:       id,
		Label:    label,
		Unit:     "%",
		ResetsAt: strings.TrimSpace(tier.ResetsAt),
		Status:   "unknown",
	}
	if !tier.Known || math.IsNaN(tier.Utilization) || math.IsInf(tier.Utilization, 0) {
		return window
	}

	used := minMaxFloat(tier.Utilization, 0, 100)
	limit := 100.0
	remaining := limit - used
	usedFraction := used / limit
	remainingFraction := remaining / limit
	window.Used = &used
	window.Limit = &limit
	window.Remaining = &remaining
	window.UsedFraction = &usedFraction
	window.RemainingFraction = &remainingFraction
	switch {
	case usedFraction >= 1:
		window.Status = "exhausted"
	case usedFraction >= 0.8:
		window.Status = "warning"
	default:
		window.Status = "ok"
	}
	return window
}

func minMaxFloat(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func codingPlanGET(ctx context.Context, httpClient *http.Client, endpoint string, headers map[string]string) (body []byte, status int, transient bool, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, providerBalancePerRequestTimeout)
	defer cancel()
	req, reqErr := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if reqErr != nil {
		return nil, 0, false, reqErr
	}
	for k, v := range headers {
		req.Header.Set(k, v)
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

func quotaUtilizationPercent(limitValue, remainingValue any) (float64, bool) {
	limit, limitOK := asFloat(limitValue)
	remaining, remainingOK := asFloat(remainingValue)
	if !limitOK || !remainingOK || limit <= 0 {
		return 0, false
	}
	return (limit - remaining) / limit * 100, true
}

func anyToFloat(v any, fallback float64) float64 {
	if f, ok := asFloat(v); ok {
		return f
	}
	return fallback
}

func asFloat(v any) (float64, bool) {
	var value float64
	var ok bool
	switch n := v.(type) {
	case float64:
		value, ok = n, true
	case float32:
		value, ok = float64(n), true
	case int:
		value, ok = float64(n), true
	case int64:
		value, ok = float64(n), true
	case json.Number:
		var err error
		value, err = n.Float64()
		ok = err == nil
	case string:
		var err error
		value, err = strconv.ParseFloat(strings.TrimSpace(n), 64)
		ok = err == nil
	default:
		return 0, false
	}
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}

func anyToReset(v any) string {
	if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	if ms, ok := asInt64(v); ok && ms > 0 {
		val := ms
		return millisToISO(&val)
	}
	return ""
}

func millisToISO(ms *int64) string {
	if ms == nil || *ms <= 0 {
		return ""
	}
	v := *ms
	if v < 1_000_000_000_000 {
		v *= 1000
	}
	return time.UnixMilli(v).UTC().Format(time.RFC3339)
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
