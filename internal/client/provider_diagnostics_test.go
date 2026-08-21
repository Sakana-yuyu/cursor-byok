package client

import (
	"testing"
	"time"
)

func TestGetProviderDiagnosticsIncludesCatalogCacheMetadataWithoutRouter(t *testing.T) {
	cache := newMetadataCache[ModelCatalogResult](modelCatalogCacheTTL)
	cache.set("opaque-key", ModelCatalogResult{Models: []ModelCatalogItem{{ID: "model-a"}}})
	service := &ProxyService{modelCatalogCache: cache}

	snapshot := service.GetProviderDiagnostics()
	if snapshot.RouterAvailable || snapshot.State != "unavailable" || snapshot.ErrorCode != "backend_host_unavailable" {
		t.Fatalf("router should be unavailable without a backend host: %#v", snapshot)
	}
	if snapshot.GeneratedAtUnixMS <= 0 {
		t.Fatalf("invalid generated timestamp %#v", snapshot)
	}
	if snapshot.ModelCatalogCache.EntryCount != 1 || snapshot.ModelCatalogCache.TTLSeconds != int64(modelCatalogCacheTTL/time.Second) {
		t.Fatalf("unexpected cache metadata %#v", snapshot.ModelCatalogCache)
	}
}

func TestGetProviderDiagnosticsNilServiceIsStable(t *testing.T) {
	var service *ProxyService
	snapshot := service.GetProviderDiagnostics()
	if snapshot.GeneratedAtUnixMS <= 0 || snapshot.State != "unavailable" || snapshot.RouterAvailable || len(snapshot.Channels) != 0 {
		t.Fatalf("unexpected nil-service snapshot %#v", snapshot)
	}
}
