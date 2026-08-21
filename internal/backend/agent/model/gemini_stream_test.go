package modeladapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGeminiContentAccumulatorPreservesNativeIncrementalChunks(t *testing.T) {
	incremental := &geminiContentAccumulator{}
	if got := incremental.Delta("a", false); got != "a" {
		t.Fatalf("first incremental delta = %q", got)
	}
	if got := incremental.Delta("a", false); got != "a" {
		t.Fatalf("identical incremental delta was dropped: %q", got)
	}
	if got := incremental.Delta("ab", false); got != "ab" {
		t.Fatalf("prefix-sharing incremental delta was shortened: %q", got)
	}
	if incremental.emitted != "aaab" {
		t.Fatalf("incremental emitted content = %q", incremental.emitted)
	}

	prefixOverlap := &geminiContentAccumulator{}
	if first, second := prefixOverlap.Delta("ha", false), prefixOverlap.Delta("haha", false); first != "ha" || second != "haha" || prefixOverlap.emitted != "hahaha" {
		t.Fatalf("prefix-overlap incremental stream = first:%q second:%q emitted:%q", first, second, prefixOverlap.emitted)
	}

	terminalIncremental := &geminiContentAccumulator{}
	_ = terminalIncremental.Delta("ha", false)
	if got := terminalIncremental.Delta("haha", true); got != "haha" || terminalIncremental.emitted != "hahaha" {
		t.Fatalf("prefix-sharing terminal incremental chunk = %q emitted=%q", got, terminalIncremental.emitted)
	}
}

func TestGeminiStreamPreservesIdenticalParallelCallsAndSuppressesRepeatedSnapshots(t *testing.T) {
	chunk := `{"candidates":[{"content":{"parts":[{"functionCall":{"id":"provider-call-1","name":"lookup","args":{"q":"same"}}},{"functionCall":{"id":"provider-call-2","name":"lookup","args":{"q":"same"}}}]}}]}`
	terminal := `{"candidates":[{"content":{"parts":[{"functionCall":{"id":"provider-call-1","name":"lookup","args":{"q":"same"}}},{"functionCall":{"id":"provider-call-2","name":"lookup","args":{"q":"same"}}}]},"finishReason":"STOP"}]}`
	events, _, _, _, err := runGeminiTestStream(t, "data: "+chunk+"\n\ndata: "+chunk+"\n\ndata: "+terminal+"\n\n")
	if err != nil {
		t.Fatalf("streamGeminiEvents() error = %v", err)
	}
	var toolIDs []string
	var providerIDs []string
	turnFinished := 0
	for _, event := range events {
		if event.Kind == ModelEventKindToolLikeCompleted {
			toolIDs = append(toolIDs, event.ToolCallID)
			providerIDs = append(providerIDs, event.ProviderCallID)
		}
		if event.Kind == ModelEventKindTurnFinished {
			turnFinished++
		}
	}
	if len(toolIDs) != 2 || toolIDs[0] == toolIDs[1] {
		t.Fatalf("parallel tool IDs = %#v, want two distinct calls", toolIDs)
	}
	if len(providerIDs) != 2 || providerIDs[0] != "provider-call-1" || providerIDs[1] != "provider-call-2" {
		t.Fatalf("provider call IDs = %#v", providerIDs)
	}
	if turnFinished != 1 {
		t.Fatalf("TurnFinished count = %d, want 1", turnFinished)
	}
}

func TestGeminiFunctionCallIDsSurviveToolRoundTrip(t *testing.T) {
	body, err := geminiRequestBody(StreamRequest{Messages: []Message{
		{Role: "assistant", ToolCalls: []ToolCallDescriptor{
			{ID: "internal-1", Type: "function", OpenAIResponsesCallID: "provider-call-1", Function: ToolCallFunctionShape{Name: "lookup", Arguments: `{"q":"one"}`}},
			{ID: "internal-2", Type: "function", OpenAIResponsesCallID: "provider-call-2", Function: ToolCallFunctionShape{Name: "lookup", Arguments: `{"q":"two"}`}},
		}},
		{Role: "tool", ToolCallID: "internal-1", Name: "lookup", Content: "one-result"},
		{Role: "tool", ToolCallID: "internal-2", Name: "lookup", Content: "two-result"},
	}}, "gemini-test")
	if err != nil {
		t.Fatalf("geminiRequestBody() error = %v", err)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	wire := string(encoded)
	if strings.Count(wire, `"id":"provider-call-1"`) != 2 || strings.Count(wire, `"id":"provider-call-2"`) != 2 {
		t.Fatalf("provider call IDs were not preserved on calls and responses: %s", wire)
	}
}

func TestGeminiStreamSeparatesCachedTokensAndPreservesZeroUsagePresence(t *testing.T) {
	payload := `{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":40,"toolUsePromptTokenCount":5,"candidatesTokenCount":10,"thoughtsTokenCount":5,"totalTokenCount":125}}`
	events, input, output, cacheRead, err := runGeminiTestStream(t, "data: "+payload+"\n\n")
	if err != nil {
		t.Fatalf("streamGeminiEvents() error = %v", err)
	}
	if input != 65 || output != 20 || cacheRead != 40 {
		t.Fatalf("usage = input:%d output:%d cache:%d", input, output, cacheRead)
	}
	final := events[len(events)-1]
	if !final.UsagePresent || !final.CacheReadPresent || final.ReasoningTokens != 5 {
		t.Fatalf("usage presence or reasoning missing: %#v", final)
	}
	if final.InputTokens+final.CacheReadTokens+final.OutputTokens != 125 {
		t.Fatalf("Gemini usage does not reconcile to reported total: %#v", final)
	}

	zeroPayload := `{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":0,"cachedContentTokenCount":0,"candidatesTokenCount":0}}`
	zeroEvents, _, _, _, err := runGeminiTestStream(t, "data: "+zeroPayload+"\n\n")
	if err != nil {
		t.Fatalf("zero usage stream error = %v", err)
	}
	zeroFinal := zeroEvents[len(zeroEvents)-1]
	if !zeroFinal.UsagePresent || !zeroFinal.CacheReadPresent {
		t.Fatalf("zero-valued usage presence missing: %#v", zeroFinal)
	}
}

func TestGeminiThinkingDisabledUsesZeroBudget(t *testing.T) {
	if got := geminiThinkingBudget("disabled"); got != 0 {
		t.Fatalf("disabled thinking budget = %d, want 0", got)
	}
}

func runGeminiTestStream(t *testing.T, body string) ([]ModelEvent, int64, int64, int64, error) {
	t.Helper()
	adapter := &GeminiAdapter{}
	response := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	watchdogCtx, watchdog := newProviderStreamIdleWatchdog(context.Background(), time.Minute)
	defer watchdog.Stop()
	_ = watchdogCtx
	var events []ModelEvent
	input, output, cacheRead, _, _, err := adapter.streamGeminiEvents(response, StreamRequest{ModelCallID: "call-1"}, "gemini-test", time.Now(), watchdog, func(event ModelEvent) error {
		events = append(events, event)
		return nil
	})
	return events, input, output, cacheRead, err
}
