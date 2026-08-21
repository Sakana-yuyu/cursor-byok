package bridge

import (
	"encoding/json"
	"strings"
	"testing"

	"cursor/internal/client"
)

func TestProjectProviderDiagnosticsUsesExplicitSafeContract(t *testing.T) {
	projected := projectProviderDiagnostics(client.ProviderDiagnosticsSnapshot{
		GeneratedAtUnixMS: 1234,
		State:             "ready",
		RouterAvailable:   true,
		Channels: []client.ProviderChannelDiagnostic{{
			ChannelID:               "channel-a",
			DisplayName:             "Channel A",
			Provider:                "openai",
			ModelID:                 "model-a",
			EndpointScheme:          "https",
			EndpointHost:            "provider.example",
			CredentialConfigured:    true,
			CustomHeadersConfigured: true,
			HealthState:             "cooldown",
			CooldownUntilUnixMS:     9999,
		}},
		ModelCatalogCache: client.ModelCatalogCacheDiagnostic{EntryCount: 2, TTLSeconds: 300},
	})
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	serialized := string(encoded)
	for _, required := range []string{"channel-a", "provider.example", "credentialConfigured", "modelCatalogCache"} {
		if !strings.Contains(serialized, required) {
			t.Fatalf("diagnostics JSON missing %q: %s", required, serialized)
		}
	}
	for _, forbidden := range []string{"apiKey", "baseURL", "customHeadersJSON", "openAIExtraParamsJSON", "anthropicExtraParamsJSON", "requestBody", "cacheKey"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("diagnostics JSON contains forbidden field %q: %s", forbidden, serialized)
		}
	}
}

func TestGetProviderDiagnosticsNilBridgeIsStable(t *testing.T) {
	var service *ProxyService
	snapshot := service.GetProviderDiagnostics()
	if snapshot.GeneratedAtUnixMS <= 0 || snapshot.State != "unavailable" || snapshot.RouterAvailable || len(snapshot.Channels) != 0 {
		t.Fatalf("unexpected nil bridge snapshot %#v", snapshot)
	}
}
