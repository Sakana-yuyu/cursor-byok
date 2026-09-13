package client

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestCodingPlanBalanceFromTiersBuildsStructuredWindows(t *testing.T) {
	balance := codingPlanBalanceFromTiers("Example Plan", []codingPlanTier{
		{Known: true, ID: "5h", Name: "5小时", Utilization: 25, ResetsAt: "2026-08-20T12:00:00Z"},
		{Known: true, ID: "7d", Name: "周限额", Utilization: 87.5, ResetsAt: "2026-08-24T00:00:00Z"},
	})

	if !balance.Supported || balance.Source != "token_plan" {
		t.Fatalf("expected supported token plan, got %#v", balance)
	}
	if balance.Used == nil || *balance.Used != 25 {
		t.Fatalf("expected primary used percentage 25, got %#v", balance.Used)
	}
	if balance.Remaining == nil || *balance.Remaining != 75 {
		t.Fatalf("expected primary remaining percentage 75, got %#v", balance.Remaining)
	}
	if balance.Total == nil || *balance.Total != 100 {
		t.Fatalf("expected percentage limit 100, got %#v", balance.Total)
	}
	if len(balance.Windows) != 2 {
		t.Fatalf("expected two usage windows, got %#v", balance.Windows)
	}

	fiveHour := balance.Windows[0]
	if fiveHour.ID != "5h" || fiveHour.Label != "5小时" || fiveHour.Status != "ok" {
		t.Fatalf("unexpected five-hour window %#v", fiveHour)
	}
	if fiveHour.UsedFraction == nil || *fiveHour.UsedFraction != 0.25 {
		t.Fatalf("expected used fraction 0.25, got %#v", fiveHour.UsedFraction)
	}
	if fiveHour.RemainingFraction == nil || *fiveHour.RemainingFraction != 0.75 {
		t.Fatalf("expected remaining fraction 0.75, got %#v", fiveHour.RemainingFraction)
	}

	weekly := balance.Windows[1]
	if weekly.ID != "7d" || weekly.Status != "warning" {
		t.Fatalf("unexpected weekly window %#v", weekly)
	}
	if weekly.Remaining == nil || *weekly.Remaining != 12.5 {
		t.Fatalf("expected weekly remaining percentage 12.5, got %#v", weekly.Remaining)
	}
	if _, err := time.Parse(time.RFC3339, balance.FetchedAt); err != nil {
		t.Fatalf("expected RFC3339 fetchedAt, got %q: %v", balance.FetchedAt, err)
	}
}

func TestCodingPlanUsageWindowClampsAndClassifiesPercentages(t *testing.T) {
	tests := []struct {
		name        string
		utilization float64
		status      string
		used        *float64
		remaining   *float64
	}{
		{name: "negative", utilization: -12, status: "ok", used: floatPointer(0), remaining: floatPointer(100)},
		{name: "warning boundary", utilization: 80, status: "warning", used: floatPointer(80), remaining: floatPointer(20)},
		{name: "exhausted and clamped", utilization: 125, status: "exhausted", used: floatPointer(100), remaining: floatPointer(0)},
		{name: "unknown nan", utilization: math.NaN(), status: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			window := codingPlanUsageWindow(codingPlanTier{Known: true, ID: "test", Name: "测试", Utilization: tt.utilization}, 0)
			if window.Status != tt.status {
				t.Fatalf("expected status %q, got %#v", tt.status, window)
			}
			assertOptionalFloat(t, "used", window.Used, tt.used)
			assertOptionalFloat(t, "remaining", window.Remaining, tt.remaining)
		})
	}
}

func TestCodingPlanProviderParsersPreserveQuotaWindows(t *testing.T) {
	tests := []struct {
		name  string
		parse func(map[string]any) []codingPlanTier
		json  string
		want  []codingPlanTier
	}{
		{
			name:  "kimi",
			parse: parseKimiTiers,
			json: `{
				"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"detail":{"limit":1000,"remaining":600,"resetTime":"2026-08-20T12:00:00Z"}}],
				"usage":{"limit":100,"remaining":15,"resetTime":"2026-08-24T00:00:00Z"}
			}`,
			want: []codingPlanTier{
				{Known: true, ID: "5h", Name: "5小时", Utilization: 40, ResetsAt: "2026-08-20T12:00:00Z"},
				{Known: true, ID: "7d", Name: "周限额", Utilization: 85, ResetsAt: "2026-08-24T00:00:00Z"},
			},
		},
		{
			name:  "zhipu",
			parse: parseZhipuTiers,
			json: `{"limits":[
				{"type":"TOKENS_LIMIT","unit":3,"percentage":42,"nextResetTime":1787227200000},
				{"type":"TOKENS_LIMIT","unit":6,"percentage":91,"nextResetTime":1787529600000}
			]}`,
			want: []codingPlanTier{
				{Known: true, ID: "5h", Name: "5小时", Utilization: 42, ResetsAt: "2026-08-20T12:00:00Z"},
				{Known: true, ID: "7d", Name: "周限额", Utilization: 91, ResetsAt: "2026-08-24T00:00:00Z"},
			},
		},
		{
			name:  "minimax",
			parse: parseMiniMaxTiers,
			json: `{"model_remains":[{
				"model_name":"general",
				"current_interval_remaining_percent":65,
				"end_time":1787227200000,
				"current_weekly_status":1,
				"current_weekly_remaining_percent":20,
				"weekly_end_time":1787529600000
			}]}`,
			want: []codingPlanTier{
				{Known: true, ID: "5h", Name: "5小时", Utilization: 35, ResetsAt: "2026-08-20T12:00:00Z"},
				{Known: true, ID: "7d", Name: "周限额", Utilization: 80, ResetsAt: "2026-08-24T00:00:00Z"},
			},
		},
		{
			name:  "zenmux",
			parse: parseZenMuxTiers,
			json: `{
				"quota_5_hour":{"usage_percentage":0.2,"resets_at":"2026-08-20T12:00:00Z"},
				"quota_7_day":{"usage_percentage":0.95,"resets_at":"2026-08-24T00:00:00Z"}
			}`,
			want: []codingPlanTier{
				{Known: true, ID: "5h", Name: "5小时", Utilization: 20, ResetsAt: "2026-08-20T12:00:00Z"},
				{Known: true, ID: "7d", Name: "周限额", Utilization: 95, ResetsAt: "2026-08-24T00:00:00Z"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(tt.json), &payload); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			got := tt.parse(payload)
			if len(got) != len(tt.want) {
				t.Fatalf("expected %d tiers, got %#v", len(tt.want), got)
			}
			for index := range tt.want {
				if got[index] != tt.want[index] {
					t.Fatalf("tier %d: expected %#v, got %#v", index, tt.want[index], got[index])
				}
			}
		})
	}
}

func TestKimiParserUsesWindowIdentityAndMostRestrictiveDuplicate(t *testing.T) {
	fiveHour := map[string]any{"duration": 300.0, "timeUnit": "TIME_UNIT_MINUTE"}
	oneHour := map[string]any{"duration": 1.0, "timeUnit": "TIME_UNIT_HOUR"}
	payload := map[string]any{
		"limits": []any{
			map[string]any{"window": fiveHour, "detail": map[string]any{"limit": 100.0, "remaining": "NaN"}},
			map[string]any{"window": fiveHour, "detail": map[string]any{"limit": 100.0, "remaining": 60.0}},
			map[string]any{"window": fiveHour, "detail": map[string]any{"limit": 100.0, "remaining": 10.0}},
			map[string]any{"window": fiveHour, "detail": map[string]any{"limit": 100.0, "remaining": "Inf"}},
			map[string]any{"window": oneHour, "detail": map[string]any{"limit": 100.0, "remaining": 70.0}},
		},
	}

	tiers := parseKimiTiers(payload)
	if len(tiers) != 2 {
		t.Fatalf("expected distinct five-hour and one-hour tiers, got %#v", tiers)
	}
	if tiers[0].ID != "5h" || !tiers[0].Known || tiers[0].Utilization != 90 {
		t.Fatalf("expected most restrictive valid duplicate, got %#v", tiers[0])
	}
	if tiers[1].ID != "1h" || tiers[1].Name != "1小时" || !tiers[1].Known || tiers[1].Utilization != 30 {
		t.Fatalf("unexpected distinct one-hour tier %#v", tiers[1])
	}
}

func TestCodingPlanParsersPreserveMalformedUsageAsUnknown(t *testing.T) {
	tests := []struct {
		name  string
		parse func(map[string]any) []codingPlanTier
		data  map[string]any
	}{
		{
			name:  "kimi missing remaining",
			parse: parseKimiTiers,
			data: map[string]any{
				"limits": []any{map[string]any{"detail": map[string]any{"limit": 100.0}}},
			},
		},
		{
			name:  "zhipu malformed percentage",
			parse: parseZhipuTiers,
			data: map[string]any{
				"limits": []any{map[string]any{"type": "TOKENS_LIMIT", "unit": 3.0, "percentage": "invalid"}},
			},
		},
		{
			name:  "zenmux missing percentage",
			parse: parseZenMuxTiers,
			data: map[string]any{
				"quota_5_hour": map[string]any{"resets_at": "2026-08-20T12:00:00Z"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tiers := tt.parse(tt.data)
			if len(tiers) != 1 {
				t.Fatalf("expected one unknown tier, got %#v", tiers)
			}
			if tiers[0].Known {
				t.Fatalf("malformed usage was treated as known: %#v", tiers[0])
			}
			window := codingPlanUsageWindow(tiers[0], 0)
			if window.Status != "unknown" || window.Used != nil || window.Remaining != nil {
				t.Fatalf("expected unknown window without fabricated amounts, got %#v", window)
			}
		})
	}
}

func TestProviderBalanceLegacyJSONOmitsUsageWindows(t *testing.T) {
	remaining := 12.5
	encoded, err := json.Marshal(ProviderBalance{
		Supported: true,
		Source:    "general",
		Currency:  "USD",
		Remaining: &remaining,
		Message:   "查询成功",
	})
	if err != nil {
		t.Fatalf("marshal balance: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode balance: %v", err)
	}
	if _, ok := payload["windows"]; ok {
		t.Fatalf("legacy balance unexpectedly contains windows: %s", encoded)
	}
	if _, ok := payload["fetchedAt"]; ok {
		t.Fatalf("legacy balance unexpectedly contains fetchedAt: %s", encoded)
	}
}

func floatPointer(value float64) *float64 {
	return &value
}

func assertOptionalFloat(t *testing.T, name string, got, want *float64) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("expected %s to be nil, got %v", name, *got)
		}
		return
	}
	if got == nil || *got != *want {
		t.Fatalf("expected %s %v, got %#v", name, *want, got)
	}
}

func TestParseZhipuAccountReport(t *testing.T) {
	tests := []struct {
		name          string
		root          map[string]any
		host          string
		wantOK        bool
		wantAvailable float64
		wantSpent     *float64
		wantCurrency  string
	}{
		{
			name: "标准响应",
			root: map[string]any{
				"success": true,
				"data": map[string]any{
					"availableBalance": 70.5,
					"totalSpendAmount": 29.5,
					"currency":         "cny",
				},
			},
			host:          "https://open.bigmodel.cn",
			wantOK:        true,
			wantAvailable: 70.5,
			wantSpent:     ptrFloat(29.5),
			wantCurrency:  "CNY",
		},
		{
			name: "availableBalance 缺失回退 balance",
			root: map[string]any{
				"data": map[string]any{
					"balance":          12.34,
					"totalSpendAmount": 5,
				},
			},
			host:          "https://open.bigmodel.cn",
			wantOK:        true,
			wantAvailable: 12.34,
			wantSpent:     ptrFloat(5),
			wantCurrency:  "CNY",
		},
		{
			name: "z.ai 无币种默认 USD",
			root: map[string]any{
				"data": map[string]any{"availableBalance": 3.2},
			},
			host:          "https://api.z.ai",
			wantOK:        true,
			wantAvailable: 3.2,
			wantCurrency:  "USD",
		},
		{
			name:   "success=false",
			root:   map[string]any{"success": false, "msg": "denied"},
			host:   "https://open.bigmodel.cn",
			wantOK: false,
		},
		{
			name:   "缺少 data",
			root:   map[string]any{"success": true},
			host:   "https://open.bigmodel.cn",
			wantOK: false,
		},
		{
			name:   "无可用余额数值",
			root:   map[string]any{"data": map[string]any{"totalSpendAmount": 5}},
			host:   "https://open.bigmodel.cn",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, ok := parseZhipuAccountReport(tt.root, tt.host)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if money.available != tt.wantAvailable {
				t.Fatalf("available = %v, want %v", money.available, tt.wantAvailable)
			}
			if tt.wantSpent == nil {
				if money.spent != nil {
					t.Fatalf("spent = %v, want nil", *money.spent)
				}
			} else if money.spent == nil || *money.spent != *tt.wantSpent {
				t.Fatalf("spent = %v, want %v", money.spent, *tt.wantSpent)
			}
			if money.currency != tt.wantCurrency {
				t.Fatalf("currency = %q, want %q", money.currency, tt.wantCurrency)
			}
		})
	}
}

func ptrFloat(v float64) *float64 { return &v }

func TestApplyZhipuAccountMoney(t *testing.T) {
	quota := codingPlanBalanceFromTiers("Zhipu GLM · Pro", []codingPlanTier{
		{Known: true, ID: "5h", Name: "5小时", Utilization: 30},
	})
	if !quota.Supported {
		t.Fatalf("expected supported quota balance")
	}

	spent := 29.5
	merged := applyZhipuAccountMoney(quota, zhipuAccountMoney{currency: "CNY", available: 70.5, spent: &spent})
	if merged.Currency != "CNY" {
		t.Fatalf("currency = %q, want CNY", merged.Currency)
	}
	if merged.Remaining == nil || *merged.Remaining != 70.5 {
		t.Fatalf("remaining = %v, want 70.5", merged.Remaining)
	}
	if merged.Used == nil || *merged.Used != 29.5 {
		t.Fatalf("used = %v, want 29.5", merged.Used)
	}
	if merged.Total == nil || *merged.Total != 100 {
		t.Fatalf("total = %v, want 100 (70.5+29.5)", merged.Total)
	}
	if len(merged.Windows) != 1 || merged.Windows[0].ID != "5h" {
		t.Fatalf("expected quota windows preserved, got %#v", merged.Windows)
	}
	if merged.PlanName != "Zhipu GLM · Pro" || merged.Source != "token_plan" {
		t.Fatalf("plan/source changed: %q / %q", merged.PlanName, merged.Source)
	}
	if !strings.Contains(merged.Message, "账户余额") {
		t.Fatalf("expected money summary in message, got %q", merged.Message)
	}
}

func TestZhipuAccountOnlyBalance(t *testing.T) {
	spent := 29.5
	balance := zhipuAccountOnlyBalance(zhipuAccountMoney{currency: "CNY", available: 70.5, spent: &spent}, "Zhipu GLM")
	if !balance.Supported || balance.Source != "zhipu_account" {
		t.Fatalf("expected supported zhipu_account, got %#v", balance)
	}
	if balance.Currency != "CNY" || balance.Remaining == nil || *balance.Remaining != 70.5 {
		t.Fatalf("unexpected money fields: %#v", balance)
	}
	if balance.Total == nil || *balance.Total != 100 {
		t.Fatalf("total = %v, want 100", balance.Total)
	}
	if balance.Used == nil || *balance.Used != 29.5 {
		t.Fatalf("used = %v, want 29.5", balance.Used)
	}
	if balance.PlanName != "Zhipu GLM" || len(balance.Windows) != 0 {
		t.Fatalf("unexpected plan/windows: %q %#v", balance.PlanName, balance.Windows)
	}
}
