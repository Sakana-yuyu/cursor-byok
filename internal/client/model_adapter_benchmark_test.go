package client

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/modelchannel"
)

func TestExecuteGeminiStreamingTest(t *testing.T) {
	server := newGeminiTestServer(t, "gemini-test", func(body map[string]any) {
		generation, _ := body["generationConfig"].(map[string]any)
		if maxOutput, ok := generation["maxOutputTokens"].(float64); !ok || maxOutput != 128 {
			t.Fatalf("benchmark maxOutputTokens = %#v, want 128", generation["maxOutputTokens"])
		}
	})
	defer server.Close()

	service := &ProxyService{}
	metrics, err := service.executeGeminiStreamingTest(t.Context(), geminiBenchmarkAdapter(server.URL))
	if err != nil {
		t.Fatalf("executeGeminiStreamingTest() error = %v", err)
	}
	if !containsGeminiText(metrics.text.String()) || metrics.outputTokens != 2 || !metrics.outputProvided {
		t.Fatalf("unexpected Gemini benchmark metrics: text=%q output=%d provided=%v", metrics.text.String(), metrics.outputTokens, metrics.outputProvided)
	}
}

func TestExecuteOpenAIStreamingTestUsesSafeDefaultMaxTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Errorf("OpenAI request path = %q, want %q", request.URL.Path, "/v1/chat/completions")
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode OpenAI request: %v", err)
		}
		if maxTokens, ok := body["max_tokens"].(float64); !ok || maxTokens != 4096 {
			t.Errorf("OpenAI max_tokens = %#v, want 4096", body["max_tokens"])
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	metrics, err := (&ProxyService{}).executeOpenAIStreamingTest(t.Context(), serverconfig.ModelAdapterConfig{
		ID:                 "openai-channel",
		DisplayName:        "Daoxe",
		Type:               "openai",
		ProtocolMode:       modelchannel.ProtocolModeAuto,
		ProtocolGroup:      modelchannel.ProtocolGroupChatCompletions,
		BaseURL:            server.URL + "/v1",
		APIKey:             "test-key",
		ModelID:            "test-model",
		OpenAIEndpoint:     modelchannel.OpenAIEndpointChatCompletions,
		OpenAIRequestGroup: modelchannel.OpenAIRequestGroupChatCompletions,
	})
	if err != nil {
		t.Fatalf("executeOpenAIStreamingTest() error = %v", err)
	}
	if !strings.Contains(metrics.text.String(), "ok") {
		t.Fatalf("OpenAI benchmark text = %q, want response text", metrics.text.String())
	}
}

func TestCalculateGenerationTokensPerSecondExcludesFirstResponseLatency(t *testing.T) {
	startedAt := time.Unix(0, 0)
	firstResponseAt := startedAt.Add(20 * time.Second)
	finishedAt := startedAt.Add(24 * time.Second)

	got := calculateGenerationTokensPerSecond(240, firstResponseAt, finishedAt)

	if math.Abs(got-60) > 0.0001 {
		t.Fatalf("tokens per second mismatch: got=%f want=60", got)
	}
}

func TestBuildSuccessfulModelAdapterTestResultSeparatesTotalAndVisibleThroughput(t *testing.T) {
	startedAt := time.Unix(0, 0)
	metrics := &modelAdapterTestMetrics{
		firstResponseAt:  startedAt.Add(20 * time.Second),
		firstTextTokenAt: startedAt.Add(23 * time.Second),
		finishedAt:       startedAt.Add(24 * time.Second),
		outputTokens:     240,
		outputProvided:   true,
		reasoningTokens:  180,
	}
	_, _ = metrics.text.WriteString(strings.Repeat("a", 240))

	result, ok := buildSuccessfulModelAdapterTestResult("adapter", "hash", startedAt, metrics)
	if !ok {
		t.Fatal("expected successful result")
	}
	if math.Abs(result.TokensPerSecond-60) > 0.0001 {
		t.Fatalf("total TPS = %f, want 60", result.TokensPerSecond)
	}
	if result.FirstResponseMS != 20_000 || result.FirstTextTokenMS != 23_000 {
		t.Fatalf("latencies = response:%d text:%d", result.FirstResponseMS, result.FirstTextTokenMS)
	}
	if result.ReasoningTokens != 180 || result.VisibleTokensPerSecond <= 0 {
		t.Fatalf("usage = reasoning:%d visible_tps:%f", result.ReasoningTokens, result.VisibleTokensPerSecond)
	}
	wantSummary := "总生成 60 t/s | 正文 60 t/s | 首响应 20.0 s | 首字 23.0 s"
	if result.SummaryText != wantSummary {
		t.Fatalf("summary = %q, want %q", result.SummaryText, wantSummary)
	}
}

func TestCalculateGenerationTokensPerSecondRejectsInvalidGenerationWindow(t *testing.T) {
	base := time.Unix(0, 0)
	tests := []struct {
		name             string
		outputTokens     int64
		firstTextTokenAt time.Time
		finishedAt       time.Time
	}{
		{name: "zero output tokens", outputTokens: 0, firstTextTokenAt: base, finishedAt: base.Add(time.Second)},
		{name: "zero duration", outputTokens: 10, firstTextTokenAt: base, finishedAt: base},
		{name: "negative duration", outputTokens: 10, firstTextTokenAt: base.Add(time.Second), finishedAt: base},
		{name: "missing first token time", outputTokens: 10, finishedAt: base.Add(time.Second)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := calculateGenerationTokensPerSecond(
				test.outputTokens,
				test.firstTextTokenAt,
				test.finishedAt,
			)

			if got != 0 {
				t.Fatalf("tokens per second mismatch: got=%f want=0", got)
			}
		})
	}
}

func TestCalculateVisibleTokensPerSecondRejectsInvalidVisibleWindow(t *testing.T) {
	base := time.Unix(0, 0)
	tests := []struct {
		name                string
		visibleOutputTokens int64
		firstTextTokenAt    time.Time
		finishedAt          time.Time
	}{
		{name: "zero visible tokens", visibleOutputTokens: 0, firstTextTokenAt: base, finishedAt: base.Add(time.Second)},
		{name: "zero duration", visibleOutputTokens: 10, firstTextTokenAt: base, finishedAt: base},
		{name: "negative duration", visibleOutputTokens: 10, firstTextTokenAt: base.Add(time.Second), finishedAt: base},
		{name: "missing first text time", visibleOutputTokens: 10, finishedAt: base.Add(time.Second)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := calculateVisibleTokensPerSecond(test.visibleOutputTokens, test.firstTextTokenAt, test.finishedAt)
			if got != 0 {
				t.Fatalf("visible tokens per second mismatch: got=%f want=0", got)
			}
		})
	}
}

func TestBuildSuccessfulModelAdapterTestResultUsesCandidateStartTime(t *testing.T) {
	originalStartedAt := time.Unix(0, 0)
	candidateStartedAt := originalStartedAt.Add(15 * time.Second)
	metrics := &modelAdapterTestMetrics{
		firstTextTokenAt: originalStartedAt.Add(20 * time.Second),
		finishedAt:       originalStartedAt.Add(24 * time.Second),
		outputTokens:     240,
		outputProvided:   true,
	}

	result, ok := buildSuccessfulModelAdapterTestResult(
		"adapter-id",
		"request-hash",
		candidateStartedAt,
		metrics,
	)

	if !ok {
		t.Fatal("expected successful result")
	}
	if result.FirstTextTokenMS != 5_000 {
		t.Fatalf("first token latency mismatch: got=%d want=5000", result.FirstTextTokenMS)
	}
	if result.TotalDurationMS != 9_000 {
		t.Fatalf("total duration mismatch: got=%d want=9000", result.TotalDurationMS)
	}
	if math.Abs(result.TokensPerSecond-60) > 0.0001 {
		t.Fatalf("tokens per second mismatch: got=%f want=60", result.TokensPerSecond)
	}
}

func TestBuildOpenAIProbeCandidates(t *testing.T) {
	adapter := serverconfig.ModelAdapterConfig{
		Type:               "openai",
		OpenAIEndpoint:     modelchannel.OpenAIEndpointResponses,
		OpenAIRequestGroup: modelchannel.OpenAIRequestGroupResponses,
	}

	got := buildOpenAIProbeCandidates(adapter)
	want := []openAIProbeCandidate{
		{Endpoint: modelchannel.OpenAIEndpointResponses, RequestGroup: modelchannel.OpenAIRequestGroupResponses},
		{Endpoint: modelchannel.OpenAIEndpointChatCompletions, RequestGroup: modelchannel.OpenAIRequestGroupChatCompletions},
		{Endpoint: modelchannel.OpenAIEndpointChatCompletions, RequestGroup: modelchannel.OpenAIRequestGroupChatCompletionsCompat},
	}

	if len(got) != len(want) {
		t.Fatalf("candidate count mismatch: got=%d want=%d candidates=%+v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("candidate[%d] mismatch: got=%+v want=%+v", index, got[index], want[index])
		}
	}
}

func TestBuildOpenAIProbeCandidatesPromotesCompatBeforeEndpointSwitch(t *testing.T) {
	adapter := serverconfig.ModelAdapterConfig{
		Type:               "openai",
		OpenAIEndpoint:     modelchannel.OpenAIEndpointChatCompletions,
		OpenAIRequestGroup: modelchannel.OpenAIRequestGroupChatCompletions,
	}

	got := buildOpenAIProbeCandidates(adapter)
	want := []openAIProbeCandidate{
		{Endpoint: modelchannel.OpenAIEndpointChatCompletions, RequestGroup: modelchannel.OpenAIRequestGroupChatCompletions},
		{Endpoint: modelchannel.OpenAIEndpointChatCompletions, RequestGroup: modelchannel.OpenAIRequestGroupChatCompletionsCompat},
		{Endpoint: modelchannel.OpenAIEndpointResponses, RequestGroup: modelchannel.OpenAIRequestGroupResponses},
	}

	if len(got) != len(want) {
		t.Fatalf("candidate count mismatch: got=%d want=%d candidates=%+v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("candidate[%d] mismatch: got=%+v want=%+v", index, got[index], want[index])
		}
	}
}

func TestModelAdapterTestDefaultsMatchProviderSafetyPolicy(t *testing.T) {
	tests := []struct {
		name    string
		adapter serverconfig.ModelAdapterConfig
		want    int
	}{
		{name: "openai", adapter: serverconfig.ModelAdapterConfig{Type: "openai"}, want: 4096},
		{name: "gemini", adapter: serverconfig.ModelAdapterConfig{Type: "gemini"}, want: 4096},
		{name: "anthropic", adapter: serverconfig.ModelAdapterConfig{Type: "anthropic"}, want: 65536},
		{
			name:    "explicit openai",
			adapter: serverconfig.ModelAdapterConfig{Type: "openai", MaxCompletionTokens: 8192},
			want:    8192,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got int
			switch normalizeModelAdapterTestType(tt.adapter.Type) {
			case "anthropic":
				got = modelAdapterTestConfiguredAnthropicMaxTokens(tt.adapter)
			case "gemini":
				got = modelAdapterTestConfiguredGeminiMaxTokens(tt.adapter)
			default:
				got = modelAdapterTestConfiguredOpenAIMaxTokens(tt.adapter)
			}
			if got != tt.want {
				t.Fatalf("configured benchmark max tokens = %d, want %d", got, tt.want)
			}
		})
	}
}
