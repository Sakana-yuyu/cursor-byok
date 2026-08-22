package client

import (
	"testing"

	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/routing"
)

func TestSyncProviderBalancesAfterAccountChangeClearsRoutingMetrics(t *testing.T) {
	service := &ProxyService{
		routingMetrics:               routing.NewMetricsSnapshot(),
		providerBalanceCache:         newMetadataCache[ProviderBalance](providerBalanceCacheTTL),
		providerBalanceNegativeCache: newMetadataCache[ProviderBalance](providerBalanceNegativeCacheTTL),
	}
	service.routingMetrics.Set("adapter-1", routing.CandidateInput{BalanceKnown: true, UsageRemainingBasisPoints: 5000})
	service.providerBalanceCache.set("cached", ProviderBalance{Supported: true})

	count := service.SyncProviderBalancesAfterAccountChange()
	if count != 0 {
		t.Fatalf("count = %d, want 0 without adapters", count)
	}
	if _, ok := service.routingMetrics.Get("adapter-1"); ok {
		t.Fatal("routing metrics were not cleared")
	}
	if _, ok := service.providerBalanceCache.get("cached"); ok {
		t.Fatal("balance cache was not cleared")
	}
}

func TestHasBalanceQueryCapabilityRequiresExplicitCredentialsForAutoProfile(t *testing.T) {
	if HasBalanceQueryCapability(serverconfig.ModelAdapterConfig{BalanceProfile: "auto"}) {
		t.Fatal("auto profile without credentials should not query balance")
	}
	if !HasBalanceQueryCapability(serverconfig.ModelAdapterConfig{
		BalanceProfile:     "auto",
		BalanceAccessToken: "token",
	}) {
		t.Fatal("auto profile with access token should query balance")
	}
	if !HasBalanceQueryCapability(serverconfig.ModelAdapterConfig{BalanceProfile: "newapi"}) {
		t.Fatal("explicit profile should query balance")
	}
}
