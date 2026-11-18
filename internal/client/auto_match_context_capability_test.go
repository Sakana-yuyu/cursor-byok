package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	serverconfig "cursor/internal/backend/server/config"
)

func TestProviderCatalogSeparatesContextInputOutput(t *testing.T) {
	tests := []struct {
		name, payload          string
		context, input, output int
	}{
		{"top level", `{"id":"custom","context":120000,"max_input":100000,"max_output":8000}`, 100000, 100000, 8000},
		{"nested limits", `{"id":"custom","limits":{"context":120000,"max_input":90000,"max_output":7000}}`, 90000, 90000, 7000},
		{"models dev", `{"id":"custom","limit":{"context":120000,"input":110000,"output":16000}}`, 110000, 110000, 16000},
		{"nested capabilities", `{"id":"custom","capabilities":{"context":{"max_input_tokens":64000,"max_output_tokens":4096}}}`, 64000, 64000, 4096},
		{"gemini", `{"name":"models/custom","inputTokenLimit":32000,"outputTokenLimit":2048}`, 32000, 32000, 2048},
		{"top provider", `{"id":"custom","context_length":200000,"top_provider":{"context_length":128000,"max_output_tokens":32768}}`, 128000, 0, 32768},
		{"output only", `{"id":"custom","max_output_tokens":8192}`, 0, 0, 8192},
		{"not pricing", `{"id":"custom","pricing":{"context":100000,"input":3,"output":15}}`, 0, 0, 0},
		{"integer strings", `{"id":"custom","model_info":{"max_input_tokens":"64000","max_output_tokens":"4000"}}`, 64000, 64000, 4000},
		{"invalid values", `{"id":"custom","context":-1,"max_input":1.5,"max_output":"bad"}`, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := decodeModelCatalog([]byte(`{"data":[` + tt.payload + `]}`))
			if err != nil {
				t.Fatal(err)
			}
			got := items[0]
			if got.ContextWindowTokens != tt.context || got.MaxInputTokens != tt.input || got.MaxOutputTokens != tt.output || got.CapabilitySource != "provider" {
				t.Fatalf("got %+v, want context/input/output %d/%d/%d", got, tt.context, tt.input, tt.output)
			}
		})
	}
}

func TestProviderInputLimitDoesNotClaimTotalContext(t *testing.T) {
	for _, tt := range []struct {
		name, metadata, source          string
		window, reported, input, output int
	}{
		{"input only", `"max_input_tokens":64000`, "provider_input_limit", 64000, 0, 64000, 0},
		{"independent output", `"max_input_tokens":64000,"max_output_tokens":8000`, "provider_input_limit", 64000, 0, 64000, 8000},
		{"explicit total and tighter input", `"context_length":128000,"max_input_tokens":64000,"max_output_tokens":8000`, "provider_input_limit", 64000, 128000, 64000, 8000},
		{"explicit total only", `"context_length":128000,"max_output_tokens":8000`, "provider_context", 128000, 128000, 0, 8000},
		{"total tighter than input", `"context_length":32000,"max_input_tokens":64000,"max_output_tokens":8000`, "provider_context", 32000, 32000, 64000, 8000},
		{"output alone", `"max_output_tokens":8000`, "", 0, 0, 0, 8000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			items, err := decodeModelCatalog([]byte(`{"data":[{"id":"custom",` + tt.metadata + `}]}`))
			if err != nil {
				t.Fatal(err)
			}
			got := items[0]
			if got.ContextWindowTokens != tt.window || got.ReportedContextWindowTokens != tt.reported || got.MaxInputTokens != tt.input || got.MaxOutputTokens != tt.output || got.ContextWindowSource != tt.source {
				t.Fatalf("incorrect limit provenance: %+v, want %+v", got, tt)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			if tt.reported == 0 {
				if _, exists := payload["reportedContextWindowTokens"]; exists {
					t.Fatalf("input/output-only response must not claim a reported total: %s", encoded)
				}
			}
		})
	}
}

func TestProviderCatalogMatchNamespacePriority(t *testing.T) {
	items := []ModelCatalogItem{
		{ID: "owner-a/custom", ContextWindowTokens: 64000},
		{ID: "owner-b/custom", ContextWindowTokens: 128000},
		{ID: "owner-a/unique", ContextWindowTokens: 32000},
	}
	for _, tt := range []struct {
		id   string
		want int
	}{
		{"models/OWNER-A/custom", 64000}, {"owner-b/custom", 128000},
		{"custom", 0}, {"owner-c/custom", 0}, {"owner-b/unique", 0},
		{"unique", 32000}, {"", 0},
	} {
		got, ok := matchModelCatalogItem(items, tt.id)
		if ok != (tt.want > 0) || got.ContextWindowTokens != tt.want {
			t.Fatalf("match(%q) = %+v,%v; want %d", tt.id, got, ok, tt.want)
		}
	}
	items = append(items, ModelCatalogItem{ID: "custom", ContextWindowTokens: 16000})
	if got, ok := matchModelCatalogItem(items, "custom"); !ok || got.ContextWindowTokens != 16000 {
		t.Fatalf("full unqualified ID must win over aliases: %+v, %v", got, ok)
	}
}

func TestAutoMatchProviderRoundTripKeepsSmallWindowsAndAmbiguousAliases(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"owner-a/custom","limits":{"context":128000,"max_input":96000,"max_output":4096}},{"id":"owner-b/custom","context":64000},{"id":"owner-a/output-only","max_output":8192},{"id":"owner-a/input-only","max_input_tokens":64000,"max_output_tokens":8000}]}`)
	}))
	defer server.Close()
	service := newAutoMatchTestService(t)
	service.publicClient = server.Client()
	cfg := serverconfig.DefaultConfig()
	cfg.AutoMatchContextWindow = true
	for _, tt := range []struct {
		id     string
		before int
	}{
		{"owner-a/custom", 32000}, {"owner-b/custom", 200000},
		{"custom", 24000}, {"owner-c/custom", 16000}, {"owner-a/output-only", 12000}, {"owner-a/input-only", 0},
	} {
		adapter := autoMatchTestAdapter(tt.id, tt.before)
		adapter.BaseURL = server.URL + "/v1"
		adapter.SupplierID = "custom"
		cfg.ModelAdapters = append(cfg.ModelAdapters, adapter)
	}
	if err := service.SaveUserConfig(cfg); err != nil {
		t.Fatal(err)
	}
	result, err := service.AutoMatchContextWindows(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.FromProbe != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	loaded, err := service.LoadUserConfig()
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{32000, 64000, 24000, 16000, 12000, 64000} {
		if got := loaded.ModelAdapters[i].ContextWindowTokens; got != want {
			t.Fatalf("adapter %d = %d, want %d", i, got, want)
		}
	}
	if detail := result.Details[0]; detail.CapabilityTokens != 96000 || detail.MaxOutputTokens != 4096 || detail.After != 32000 || detail.CapabilitySource != "provider" {
		t.Fatalf("recognized capability and channel setting must be separate: %+v", detail)
	}
	if detail := result.Details[0]; detail.ReportedContextWindowTokens != 128000 || detail.MaxInputTokens != 96000 || detail.ContextWindowSource != "provider_input_limit" {
		t.Fatalf("reported total and conservative input limit must remain distinct: %+v", detail)
	}
	if detail := result.Details[5]; detail.ReportedContextWindowTokens != 0 || detail.CapabilityTokens != 64000 || detail.MaxInputTokens != 64000 || detail.MaxOutputTokens != 8000 || detail.ContextWindowSource != "provider_input_limit" {
		t.Fatalf("input and output must not become a claimed total: %+v", detail)
	}
	second, err := service.AutoMatchContextWindows(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed {
		t.Fatalf("second round must be stable: %+v", second)
	}
	if requests > 2 {
		t.Fatalf("expected one provider request per group/round, got %d", requests)
	}
}

func TestAutoMatchCatalogSmallWindowRoundTripReportsCapability(t *testing.T) {
	service := newAutoMatchTestService(t)
	cfg := serverconfig.DefaultConfig()
	cfg.AutoMatchContextWindow = true
	cfg.ModelAdapters = []serverconfig.ModelAdapterConfig{autoMatchTestAdapter("gpt-6.1-sol", 32000)}
	if err := service.SaveUserConfig(cfg); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		result, err := service.AutoMatchContextWindows(context.Background(), true)
		if err != nil {
			t.Fatal(err)
		}
		if result.Changed || result.Details[0].After != 32000 || result.Details[0].CapabilityTokens != 272000 || result.Details[0].MaxOutputTokens != 128000 {
			t.Fatalf("round %d: %+v", round, result)
		}
	}
}

func TestAutoMatchBucketSeparatesMetadataCredentials(t *testing.T) {
	base := autoMatchTestAdapter("custom", 16000)
	for _, change := range []func(*serverconfig.ModelAdapterConfig){
		func(a *serverconfig.ModelAdapterConfig) { a.AnthropicAuthMode = "bearer" },
		func(a *serverconfig.ModelAdapterConfig) { a.ModelCatalogURL = "https://example.com/catalog" },
		func(a *serverconfig.ModelAdapterConfig) {
			a.CustomHeadersEnabled = true
			a.CustomHeadersJSON = `{"Authorization":"other"}`
		},
		func(a *serverconfig.ModelAdapterConfig) { a.SupplierID = "other" },
	} {
		other := base
		change(&other)
		if probeBucketKey(base) == probeBucketKey(other) {
			t.Fatal("different catalog identities must not share a bucket")
		}
	}
}
