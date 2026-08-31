// billing_query_test.go 验证计费查询全局开关的配置默认值与价格快照兜底：
// 旧配置缺 billingQuery 键时按默认开启处理，显式关闭被保留；
// PricingRates 对未配价渠道回退内置官方价且 Known=true，供统计页费用估算使用。
package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func loadConfigFromYAML(t *testing.T, content string) Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := NewStore(path, "").Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return cfg
}

func TestStoreLoadBillingQueryDefaultsToEnabled(t *testing.T) {
	// 旧配置文件没有 billingQuery 键：按默认开启处理，不能被 Go 零值静默关闭。
	cfg := loadConfigFromYAML(t, "log: false\n")
	if !cfg.BillingQuery.Enabled {
		t.Fatal("expected billingQuery.Enabled default true for legacy config without the key")
	}

	// 键存在但未写 enabled 子键：同样按默认开启处理。
	cfg = loadConfigFromYAML(t, "billingQuery: {}\n")
	if !cfg.BillingQuery.Enabled {
		t.Fatal("expected billingQuery.Enabled default true when nested enabled key missing")
	}

	// 显式关闭必须被保留。
	cfg = loadConfigFromYAML(t, "billingQuery:\n  enabled: false\n")
	if cfg.BillingQuery.Enabled {
		t.Fatal("expected billingQuery.Enabled false when explicitly disabled")
	}
}

func TestPricingRatesIncludesBuiltinFallback(t *testing.T) {
	manager := newMCPTrustTestManager(t)
	cfg := manager.Current()
	cfg.ModelAdapters = []ModelAdapterConfig{{
		ID:          "adapter-1",
		DisplayName: "Kimi",
		Type:        "openai",
		BaseURL:     "https://api.example.com/v1",
		APIKey:      "key",
		TooltipData: "test",
		ModelID:     "kimi-k3",
	}}
	if _, err := manager.Save(context.Background(), cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	rates := manager.PricingRates()
	if len(rates) != 1 {
		t.Fatalf("expected 1 price rate, got %d", len(rates))
	}
	rate := rates[0]
	if !rate.Known {
		t.Fatal("expected builtin fallback rate to be Known=true")
	}
	if rate.Input == nil || *rate.Input != 3.0 {
		t.Fatalf("expected kimi-k3 builtin input price 3.0, got %#v", rate.Input)
	}
	if rate.Output == nil || *rate.Output != 15.0 {
		t.Fatalf("expected kimi-k3 builtin output price 15.0, got %#v", rate.Output)
	}
	if rate.Currency != "USD" {
		t.Fatalf("expected USD currency, got %q", rate.Currency)
	}
	if rate.Source != "official" {
		t.Fatalf("expected official source, got %q", rate.Source)
	}
}

func TestPricingRatesKeepsManualPricingPriority(t *testing.T) {
	manager := newMCPTrustTestManager(t)
	cfg := manager.Current()
	inputPrice := 1.25
	cfg.ModelAdapters = []ModelAdapterConfig{{
		ID:          "adapter-1",
		DisplayName: "Kimi",
		Type:        "openai",
		BaseURL:     "https://api.example.com/v1",
		APIKey:      "key",
		TooltipData: "test",
		ModelID:     "kimi-k3",
		Pricing: &ModelPricing{
			Input:    &inputPrice,
			Known:    true,
			Source:   "manual",
			Currency: "USD",
		},
	}}
	if _, err := manager.Save(context.Background(), cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	rates := manager.PricingRates()
	if len(rates) != 1 {
		t.Fatalf("expected 1 price rate, got %d", len(rates))
	}
	if rates[0].Input == nil || *rates[0].Input != 1.25 {
		t.Fatalf("expected manual input price 1.25 to win over builtin, got %#v", rates[0].Input)
	}
	if rates[0].Source != "manual" {
		t.Fatalf("expected manual source, got %q", rates[0].Source)
	}
}
