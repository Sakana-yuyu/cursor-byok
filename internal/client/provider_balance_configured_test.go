package client

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	serverconfig "cursor/internal/backend/server/config"
)

func TestQueryNamedProviderBalanceSupplierDoesNotOverrideCustomBaseURL(t *testing.T) {
	balance, matched := (*ProxyService)(nil).queryNamedProviderBalance(
		t.Context(),
		http.DefaultClient,
		"https://relay.example.com/v1",
		"test-key",
		"moonshot",
	)
	if matched {
		t.Fatalf("expected custom relay baseURL not to match named provider, got %#v", balance)
	}
}

func TestFindAdapterForBalanceMatchesSupplierID(t *testing.T) {
	service := newProviderBalanceTestService(t, nil)
	cfg := serverconfig.DefaultConfig()
	cfg.ModelAdapters = []serverconfig.ModelAdapterConfig{
		{
			DisplayName:        "DeepSeek",
			Type:               "openai",
			SupplierID:         "deepseek",
			ProtocolMode:       "auto",
			BaseURL:            "https://relay.example.com/v1",
			APIKey:             "same-key",
			TooltipData:        "test",
			ModelID:            "deepseek-chat",
			ReasoningEffort:    "medium",
			OpenAIEndpoint:     "/v1/chat/completions",
			OpenAIRequestGroup: "chat_completions",
			BalanceQueryURL:    "https://balance.example.com/deepseek",
			BalanceQueryField:  "data.remaining",
		},
		{
			DisplayName:        "Moonshot",
			Type:               "openai",
			SupplierID:         "moonshot",
			ProtocolMode:       "auto",
			BaseURL:            "https://relay.example.com/v1",
			APIKey:             "same-key",
			TooltipData:        "test",
			ModelID:            "kimi-k2",
			ReasoningEffort:    "medium",
			OpenAIEndpoint:     "/v1/chat/completions",
			OpenAIRequestGroup: "chat_completions",
			BalanceQueryURL:    "https://balance.example.com/moonshot",
			BalanceQueryField:  "data.remaining",
		},
	}
	if err := service.SaveUserConfig(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	adapter, ok := service.findAdapterForBalance("openai", "moonshot", "https://relay.example.com/v1", "same-key")
	if !ok {
		t.Fatal("expected moonshot adapter match")
	}
	if adapter.SupplierID != "moonshot" {
		t.Fatalf("expected moonshot adapter, got %q", adapter.SupplierID)
	}
}

func TestQueryProviderBalanceConfiguredTakesPriorityOverNamedProvider(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"remaining":12.5}}`))
	}))
	defer server.Close()

	service := newProviderBalanceTestService(t, server.Client())
	cfg := serverconfig.DefaultConfig()
	cfg.ModelAdapters = []serverconfig.ModelAdapterConfig{{
		DisplayName:        "Moonshot",
		Type:               "openai",
		SupplierID:         "moonshot",
		ProtocolMode:       "auto",
		BaseURL:            "https://api.moonshot.cn/v1",
		APIKey:             "test-key",
		TooltipData:        "test",
		ModelID:            "kimi-k2",
		ReasoningEffort:    "medium",
		OpenAIEndpoint:     "/v1/chat/completions",
		OpenAIRequestGroup: "chat_completions",
		BalanceQueryURL:    server.URL + "/balance",
		BalanceQueryField:  "data.remaining",
	}}
	if err := service.SaveUserConfig(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	balance := service.QueryProviderBalance(ProviderBalanceRequest{
		Type:       "openai",
		SupplierID: "moonshot",
		BaseURL:    "https://api.moonshot.cn/v1",
		APIKey:     "test-key",
	})
	if !called {
		t.Fatal("expected configured balance endpoint to be called")
	}
	if !balance.Supported || balance.Source != "configured" {
		t.Fatalf("expected configured balance, got %#v", balance)
	}
	if balance.Remaining == nil || *balance.Remaining != 12.5 {
		t.Fatalf("unexpected remaining balance %#v", balance.Remaining)
	}
}

func newProviderBalanceTestService(t *testing.T, client *http.Client) *ProxyService {
	t.Helper()
	if client == nil {
		client = http.DefaultClient
	}
	return &ProxyService{
		store:                        serverconfig.NewStore(filepath.Join(t.TempDir(), "config.yml"), t.TempDir()),
		publicClient:                 client,
		providerBalanceCache:         newMetadataCache[ProviderBalance](providerBalanceCacheTTL),
		providerBalanceNegativeCache: newMetadataCache[ProviderBalance](providerBalanceNegativeCacheTTL),
	}
}
