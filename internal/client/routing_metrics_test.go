package client

import (
	"strings"
	"testing"

	"cursor/internal/routing"
)

func TestRoutingMetricsFromBalanceUsesTightestUsageWindow(t *testing.T) {
	low := 0.15
	high := 0.95
	metrics := routingMetricsFromBalance(ProviderBalance{
		Supported: true,
		Windows: []ProviderUsageWindow{
			{ID: "5h", RemainingFraction: &high, Status: "ok"},
			{ID: "7d", RemainingFraction: &low, Status: "warning"},
		},
	})
	if !metrics.BalanceKnown || metrics.UsageRemainingBasisPoints != 1500 || !metrics.Available {
		t.Fatalf("metrics = %#v", metrics)
	}
}

func TestRoutingMetricsFromBalanceMarksExhaustedUnavailable(t *testing.T) {
	remaining := 0.4
	metrics := routingMetricsFromBalance(ProviderBalance{
		Supported: true,
		Windows: []ProviderUsageWindow{
			{ID: "5h", RemainingFraction: &remaining, Status: "exhausted"},
		},
	})
	if metrics.Available || metrics.BalanceKnown {
		t.Fatalf("metrics = %#v", metrics)
	}
}

func TestRecordRoutingMetricsIgnoresUnsupportedBalance(t *testing.T) {
	service := &ProxyService{routingMetrics: routing.NewMetricsSnapshot()}
	service.RecordRoutingMetrics("adapter-1", ProviderBalance{Supported: false})
	if _, ok := service.routingMetrics.Get("adapter-1"); ok {
		t.Fatal("unsupported balance was recorded")
	}
}

func TestRecordRoutingMetricsStoresByAdapterID(t *testing.T) {
	service := &ProxyService{routingMetrics: routing.NewMetricsSnapshot()}
	remaining := 12.5
	service.RecordRoutingMetrics("adapter-1", ProviderBalance{
		Supported: true,
		Remaining: &remaining,
	})
	metrics, ok := service.routingMetrics.Get("adapter-1")
	if !ok || !metrics.BalanceKnown || metrics.BalanceMicrosUSD != 12_500_000 {
		t.Fatalf("metrics = %#v ok=%v", metrics, ok)
	}
}

func TestProviderUsageWindowSnapshotsOmitsCredentials(t *testing.T) {
	frac := 0.5
	snapshots := providerUsageWindowSnapshots(ProviderBalance{
		Windows: []ProviderUsageWindow{{ID: "5h", RemainingFraction: &frac, Status: "ok"}},
	})
	if len(snapshots) != 1 || strings.TrimSpace(snapshots[0].ID) != "5h" {
		t.Fatalf("snapshots = %#v", snapshots)
	}
}
