package client

import (
	"math"
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
