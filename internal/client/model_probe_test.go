package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	serverconfig "cursor/internal/backend/server/config"
)

func TestRunModelAdapterProbeStreamSupportsGemini(t *testing.T) {
	server := newGeminiTestServer(t, "gemini-2.5-flash", func(body map[string]any) {
		generation, _ := body["generationConfig"].(map[string]any)
		thinking, _ := generation["thinkingConfig"].(map[string]any)
		if budget, ok := thinking["thinkingBudget"].(float64); !ok || budget != 0 {
			t.Fatalf("probe thinking budget = %#v, want 0", thinking["thinkingBudget"])
		}
	})
	defer server.Close()

	sawContent, err := runModelAdapterProbeStream(context.Background(), serverconfig.ModelAdapterConfig{
		ID: "gemini-channel", Type: "gemini", BaseURL: server.URL + "/v1beta", APIKey: "gemini-key", ModelID: "gemini-2.5-flash",
	})
	if err != nil {
		t.Fatalf("runModelAdapterProbeStream() error = %v", err)
	}
	if !sawContent {
		t.Fatal("Gemini probe did not observe content")
	}
}

func TestGeminiProbeThinkingEffortUsesExplicitAllowlist(t *testing.T) {
	if got := geminiProbeThinkingEffort("gemini-2.5-flash"); got != "disabled" {
		t.Fatalf("Gemini 2.5 Flash effort = %q, want disabled", got)
	}
	for _, modelID := range []string{"gemini-2.5-pro", "gemini-2.0-flash", "gemini-3-flash", "custom-flash-alias"} {
		if got := geminiProbeThinkingEffort(modelID); got != "" {
			t.Fatalf("model %q unexpectedly received effort %q", modelID, got)
		}
	}
}

func TestRunModelAdapterProbeStreamOmitsZeroThinkingForGeminiPro(t *testing.T) {
	server := newGeminiTestServer(t, "gemini-2.5-pro", func(body map[string]any) {
		generation, _ := body["generationConfig"].(map[string]any)
		if _, exists := generation["thinkingConfig"]; exists {
			t.Fatalf("Gemini Pro probe must not force thinkingConfig: %#v", generation)
		}
	})
	defer server.Close()

	sawContent, err := runModelAdapterProbeStream(context.Background(), serverconfig.ModelAdapterConfig{
		ID: "gemini-pro-channel", Type: "gemini", BaseURL: server.URL + "/v1beta", APIKey: "gemini-key", ModelID: "gemini-2.5-pro",
	})
	if err != nil {
		t.Fatalf("runModelAdapterProbeStream() error = %v", err)
	}
	if !sawContent {
		t.Fatal("Gemini Pro probe did not observe content")
	}
}

func newGeminiTestServer(t *testing.T, modelID string, inspect func(map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		wantPath := "/v1beta/models/" + modelID + ":streamGenerateContent"
		if request.URL.Path != wantPath || request.URL.Query().Get("alt") != "sse" {
			t.Errorf("Gemini request URL = %s", request.URL.String())
		}
		if request.Header.Get("x-goog-api-key") != "gemini-key" {
			t.Errorf("Gemini API key header = %q", request.Header.Get("x-goog-api-key"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode Gemini request: %v", err)
		}
		if inspect != nil {
			inspect(body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":2}}\n\n"))
	}))
}

func geminiBenchmarkAdapter(baseURL string) serverconfig.ModelAdapterConfig {
	return serverconfig.ModelAdapterConfig{
		ID: "gemini-channel", DisplayName: "Gemini", TooltipData: "Gemini", Type: "gemini", ProtocolMode: "auto",
		BaseURL: baseURL + "/v1beta", APIKey: "gemini-key", ModelID: "gemini-test", MaxCompletionTokens: 128, ReasoningEffort: "low",
	}
}

func containsGeminiText(value string) bool {
	return strings.Contains(strings.TrimSpace(value), "ok")
}
