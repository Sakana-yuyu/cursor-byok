package client

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestFetchModelCatalogCacheKeyHidesCredentialBearingInputs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer server.Close()

	service := &ProxyService{
		publicClient:      server.Client(),
		modelCatalogCache: newMetadataCache[ModelCatalogResult](modelCatalogCacheTTL),
	}
	secretURL := server.URL + "/models?access_token=query-secret"
	_, err := service.FetchModelCatalog(ModelCatalogRequest{
		Type:                 "openai",
		SupplierID:           "secret-supplier",
		ModelCatalogStatus:   "manual_only",
		BaseURL:              server.URL,
		APIKey:               "sk-catalog-secret",
		CustomHeadersEnabled: true,
		CustomHeadersJSON:    `{"Authorization":"Bearer header-secret"}`,
		ModelCatalogURL:      secretURL,
	})
	if err != nil {
		t.Fatalf("FetchModelCatalog() error = %v", err)
	}
	if len(service.modelCatalogCache.entries) != 1 {
		t.Fatalf("cache entries = %d, want 1", len(service.modelCatalogCache.entries))
	}
	for key := range service.modelCatalogCache.entries {
		if len(key) != 64 {
			t.Fatalf("catalog cache key length = %d, want sha256 hex", len(key))
		}
		for _, forbidden := range []string{"secret-supplier", "access_token", "query-secret", "sk-catalog-secret", "header-secret", server.URL} {
			if strings.Contains(key, forbidden) {
				t.Fatalf("catalog cache key leaked %q: %s", forbidden, key)
			}
		}
	}
}

func TestFetchModelCatalogAnthropicAuthModes(t *testing.T) {
	tests := []struct {
		name              string
		baseURL           string
		mode              string
		customHeadersJSON string
		wantAPIKey        string
		wantAuthorization string
	}{
		{name: "legacy dual", mode: "legacy_dual", wantAPIKey: "token", wantAuthorization: "Bearer token"},
		{name: "auto nonofficial is dual", mode: "auto", wantAPIKey: "token", wantAuthorization: "Bearer token"},
		{name: "x api key", mode: "x_api_key", wantAPIKey: "token"},
		{name: "bearer", mode: "bearer", wantAuthorization: "Bearer token"},
		{name: "custom authorization", mode: "x_api_key", customHeadersJSON: `{"Authorization":"Bearer custom"}`, wantAuthorization: "Bearer custom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var received http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				received = request.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[{"id":"claude-test"}]}`))
			}))
			defer server.Close()
			service := &ProxyService{publicClient: server.Client(), modelCatalogCache: newMetadataCache[ModelCatalogResult](modelCatalogCacheTTL)}
			_, err := service.FetchModelCatalog(ModelCatalogRequest{
				Type: "anthropic", ModelCatalogStatus: "manual_only", BaseURL: server.URL + "/v1", APIKey: "token", AnthropicAuthMode: tt.mode,
				CustomHeadersEnabled: tt.customHeadersJSON != "", CustomHeadersJSON: tt.customHeadersJSON, ModelCatalogURL: server.URL + "/models",
			})
			if err != nil {
				t.Fatalf("FetchModelCatalog() error = %v", err)
			}
			if got := received.Get("x-api-key"); got != tt.wantAPIKey {
				t.Fatalf("x-api-key = %q, want %q", got, tt.wantAPIKey)
			}
			if got := received.Get("Authorization"); got != tt.wantAuthorization {
				t.Fatalf("Authorization = %q, want %q", got, tt.wantAuthorization)
			}
		})
	}
}

func TestFetchModelCatalogSameOriginReceivesProviderCredentials(t *testing.T) {
	tests := []struct {
		provider    string
		headerName  string
		headerValue string
	}{
		{provider: "openai", headerName: "Authorization", headerValue: "Bearer sk-secret"},
		{provider: "anthropic", headerName: "x-api-key", headerValue: "sk-secret"},
		{provider: "gemini", headerName: "x-goog-api-key", headerValue: "sk-secret"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			var received http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				received = request.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
			}))
			defer server.Close()
			service := &ProxyService{publicClient: server.Client(), modelCatalogCache: newMetadataCache[ModelCatalogResult](modelCatalogCacheTTL)}
			_, err := service.FetchModelCatalog(ModelCatalogRequest{
				Type: tt.provider, ModelCatalogStatus: "manual_only", BaseURL: server.URL + "/v1", APIKey: "sk-secret",
				CustomHeadersEnabled: true, CustomHeadersJSON: `{"X-Custom-Secret":"custom-secret"}`, ModelCatalogURL: server.URL + "/models",
			})
			if err != nil {
				t.Fatalf("FetchModelCatalog() error = %v", err)
			}
			if received.Get(tt.headerName) != tt.headerValue || received.Get("X-Custom-Secret") != "custom-secret" {
				t.Fatalf("same-origin credentials missing: %#v", received)
			}
		})
	}
}

func TestFetchModelCatalogCrossOriginOmitsProviderAndCustomCredentials(t *testing.T) {
	trusted := httptest.NewServer(http.NotFoundHandler())
	defer trusted.Close()
	var received http.Header
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		received = request.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer catalog.Close()
	service := &ProxyService{publicClient: catalog.Client(), modelCatalogCache: newMetadataCache[ModelCatalogResult](modelCatalogCacheTTL)}
	_, err := service.FetchModelCatalog(ModelCatalogRequest{
		Type: "openai", ModelCatalogStatus: "manual_only", BaseURL: trusted.URL + "/v1", APIKey: "sk-secret",
		CustomHeadersEnabled: true, CustomHeadersJSON: `{"Authorization":"Bearer custom-secret","X-Custom-Secret":"header-secret"}`, ModelCatalogURL: catalog.URL + "/models",
	})
	if err != nil {
		t.Fatalf("FetchModelCatalog() error = %v", err)
	}
	for _, name := range []string{"Authorization", "x-api-key", "x-goog-api-key", "X-Custom-Secret"} {
		if value := received.Get(name); value != "" {
			t.Fatalf("cross-origin catalog received %s=%q", name, value)
		}
	}
}

func TestFetchModelCatalogCrossOriginRedirectStripsCredentials(t *testing.T) {
	var redirectedHeaders http.Header
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		redirectedHeaders = request.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer redirectTarget.Close()
	trusted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, redirectTarget.URL+"/models", http.StatusTemporaryRedirect)
	}))
	defer trusted.Close()
	baseClient := trusted.Client()
	service := &ProxyService{publicClient: baseClient, modelCatalogCache: newMetadataCache[ModelCatalogResult](modelCatalogCacheTTL)}
	_, err := service.FetchModelCatalog(ModelCatalogRequest{
		Type: "openai", ModelCatalogStatus: "manual_only", BaseURL: trusted.URL + "/v1", APIKey: "sk-secret",
		CustomHeadersEnabled: true, CustomHeadersJSON: `{"X-Custom-Secret":"header-secret"}`, ModelCatalogURL: trusted.URL + "/models?access_token=referer-secret",
	})
	if err != nil {
		t.Fatalf("FetchModelCatalog() error = %v", err)
	}
	for _, name := range []string{"Authorization", "Cookie", "Referer", "X-Custom-Secret"} {
		if value := redirectedHeaders.Get(name); value != "" {
			t.Fatalf("redirect target received %s=%q", name, value)
		}
	}
	if strings.Contains(fmt.Sprint(redirectedHeaders), "referer-secret") {
		t.Fatalf("redirect target received query credential through headers: %#v", redirectedHeaders)
	}
	if baseClient.CheckRedirect != nil {
		t.Fatal("FetchModelCatalog mutated shared HTTP client redirect policy")
	}
}

func TestFetchModelCatalogCrossOriginFailureFallsBackToAuthenticatedSameOrigin(t *testing.T) {
	untrusted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer untrusted.Close()
	var trustedAuthorization string
	trusted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		trustedAuthorization = request.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer trusted.Close()
	appendGenerated := true
	service := &ProxyService{publicClient: trusted.Client(), modelCatalogCache: newMetadataCache[ModelCatalogResult](modelCatalogCacheTTL)}
	_, err := service.FetchModelCatalog(ModelCatalogRequest{
		Type: "openai", ModelCatalogStatus: "auto", AppendModelCatalogCandidates: &appendGenerated,
		BaseURL: trusted.URL + "/v1", APIKey: "sk-secret", ModelCatalogURL: untrusted.URL + "/models",
	})
	if err != nil {
		t.Fatalf("FetchModelCatalog() error = %v", err)
	}
	if trustedAuthorization != "Bearer sk-secret" {
		t.Fatalf("same-origin fallback authorization = %q", trustedAuthorization)
	}
}

func TestFetchModelCatalogForceRefreshBypassesCache(t *testing.T) {
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		modelID := "model-a"
		if requestCount > 1 {
			modelID = "model-b"
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"` + modelID + `"}]}`))
	}))
	defer server.Close()

	service := &ProxyService{
		publicClient:      server.Client(),
		modelCatalogCache: newMetadataCache[ModelCatalogResult](modelCatalogCacheTTL),
	}
	appendGenerated := true
	request := ModelCatalogRequest{
		Type:                         "openai",
		SupplierID:                   "custom",
		BaseURL:                      server.URL + "/v1",
		APIKey:                       "sk-catalog-secret",
		AppendModelCatalogCandidates: &appendGenerated,
	}

	first, err := service.FetchModelCatalog(request)
	if err != nil {
		t.Fatalf("first FetchModelCatalog() error = %v", err)
	}
	if len(first.Models) != 1 || first.Models[0].ID != "model-a" {
		t.Fatalf("first models = %#v, want model-a", first.Models)
	}
	if requestCount != 1 {
		t.Fatalf("requestCount after first fetch = %d, want 1", requestCount)
	}

	second, err := service.FetchModelCatalog(request)
	if err != nil {
		t.Fatalf("second FetchModelCatalog() error = %v", err)
	}
	if second.Models[0].ID != "model-a" {
		t.Fatalf("cached models = %#v, want model-a", second.Models)
	}
	if requestCount != 1 {
		t.Fatalf("requestCount after cached fetch = %d, want 1", requestCount)
	}

	request.ForceRefresh = true
	third, err := service.FetchModelCatalog(request)
	if err != nil {
		t.Fatalf("third FetchModelCatalog() error = %v", err)
	}
	if third.Models[0].ID != "model-b" {
		t.Fatalf("refreshed models = %#v, want model-b", third.Models)
	}
	if requestCount != 2 {
		t.Fatalf("requestCount after force refresh = %d, want 2", requestCount)
	}
}

func TestInvalidateModelCatalogCaches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer server.Close()

	service := &ProxyService{
		publicClient:      server.Client(),
		modelCatalogCache: newMetadataCache[ModelCatalogResult](modelCatalogCacheTTL),
	}
	appendGenerated := true
	request := ModelCatalogRequest{
		Type:                         "openai",
		BaseURL:                      server.URL + "/v1",
		APIKey:                       "sk-catalog-secret",
		AppendModelCatalogCandidates: &appendGenerated,
	}
	if _, err := service.FetchModelCatalog(request); err != nil {
		t.Fatalf("FetchModelCatalog() error = %v", err)
	}
	service.invalidateModelCatalogCaches()
	if len(service.modelCatalogCache.entries) != 0 {
		t.Fatalf("cache entries = %d, want 0 after invalidate", len(service.modelCatalogCache.entries))
	}
}

func TestFetchModelCatalogTransportErrorRedactsCandidateURL(t *testing.T) {
	service := &ProxyService{
		publicClient: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("dial failed for %s", request.URL.String())
		})},
		modelCatalogCache: newMetadataCache[ModelCatalogResult](modelCatalogCacheTTL),
	}
	_, err := service.FetchModelCatalog(ModelCatalogRequest{
		Type:               "openai",
		ModelCatalogStatus: "manual_only",
		BaseURL:            "https://user:password@example.invalid/v1?api_key=base-secret",
		APIKey:             "sk-secret",
		ModelCatalogURL:    "https://user:password@example.invalid/models?access_token=query-secret",
	})
	if err == nil {
		t.Fatal("expected model catalog request to fail")
	}
	message := err.Error()
	for _, forbidden := range []string{"user:password", "access_token", "query-secret", "api_key", "base-secret", "sk-secret", "example.invalid/models"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("transport error leaked %q: %s", forbidden, message)
		}
	}
}
