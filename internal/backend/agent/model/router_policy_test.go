package modeladapter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"cursor/internal/routing"
	legacyruntime "cursor/internal/runtime"
)

type policyAwareResolver struct {
	diagnosticsResolverStub
	policy  routing.Policy
	history *routing.History
	metrics map[string]routing.CandidateInput
}

func (resolver *policyAwareResolver) RoutingPolicy() routing.Policy {
	return resolver.policy
}

func (resolver *policyAwareResolver) RecordRoutingDecision(record routing.DecisionRecord) {
	if resolver.history != nil {
		resolver.history.Append(record)
	}
}

func (resolver *policyAwareResolver) RoutingCandidateMetrics(channelID string) (routing.CandidateInput, bool) {
	item, ok := resolver.metrics[channelID]
	return item, ok
}

type channelCallAdapter struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]error
	emit  map[string]string
}

func (adapter *channelCallAdapter) Stream(_ context.Context, req StreamRequest, sink func(ModelEvent) error) error {
	adapter.mu.Lock()
	adapter.calls = append(adapter.calls, req.ResolvedChannelID)
	fail := adapter.fail[req.ResolvedChannelID]
	text := adapter.emit[req.ResolvedChannelID]
	adapter.mu.Unlock()
	if text != "" && sink != nil {
		_ = sink(ModelEvent{Kind: ModelEventKindTextDelta, Text: text})
	}
	return fail
}

func (adapter *channelCallAdapter) seen() []string {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	out := make([]string, len(adapter.calls))
	copy(out, adapter.calls)
	return out
}

func newPolicyRouter(resolver *policyAwareResolver, adapter ModelAdapter) *Router {
	return &Router{
		openai:          adapter,
		anthropic:       NewAnthropicAdapter(),
		gemini:          NewGeminiAdapter(),
		resolver:        resolver,
		healthByChannel: make(map[string]channelHealth),
	}
}

func expensiveCheapChannels() []*legacyruntime.ResolvedChannel {
	return []*legacyruntime.ResolvedChannel{
		{ID: "expensive", Name: "expensive", Provider: "openai", ProtocolGroup: "chat_completions", BaseURL: "https://expensive.example/v1", APIKey: "sk-a", Model: "model-1", OpenAIEndpoint: "/v1/chat/completions"},
		{ID: "cheap", Name: "cheap", Provider: "openai", ProtocolGroup: "chat_completions", BaseURL: "https://cheap.example/v1", APIKey: "sk-b", Model: "model-1", OpenAIEndpoint: "/v1/chat/completions"},
	}
}

func costMetrics() map[string]routing.CandidateInput {
	return map[string]routing.CandidateInput{
		"expensive": {ChannelID: "expensive", Available: true, PricingKnown: true, EstimatedCostMicrosUSD: 9_000_000},
		"cheap":     {ChannelID: "cheap", Available: true, PricingKnown: true, EstimatedCostMicrosUSD: 1_000},
	}
}

func TestRouterStreamDisabledPolicyKeepsConfigOrder(t *testing.T) {
	adapter := &channelCallAdapter{}
	history := &routing.History{}
	resolver := &policyAwareResolver{
		diagnosticsResolverStub: diagnosticsResolverStub{channels: expensiveCheapChannels()},
		policy:                  routing.DefaultPolicy(),
		history:                 history,
		metrics:                 costMetrics(),
	}
	router := newPolicyRouter(resolver, adapter)
	if err := router.Stream(context.Background(), StreamRequest{ModelID: "model-1", Messages: []Message{{Role: "user", Content: "hi"}}}, func(ModelEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := adapter.seen(); len(got) != 1 || got[0] != "expensive" {
		t.Fatalf("disabled policy selected %v, want expensive first", got)
	}
	page, err := history.List(routing.DecisionQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("disabled policy wrote history: %#v", page.Items)
	}
}

func TestRouterStreamEnabledCostPolicyPrefersCheaperChannel(t *testing.T) {
	adapter := &channelCallAdapter{}
	history := &routing.History{}
	resolver := &policyAwareResolver{
		diagnosticsResolverStub: diagnosticsResolverStub{channels: expensiveCheapChannels()},
		policy: routing.Policy{
			Enabled: true, Strategy: routing.StrategyCost, CostWeight: 100,
			LatencyWeight: 0, ReliabilityWeight: 0, BalanceWeight: 0,
			MaxFailoverAttempts: 1,
		},
		history: history,
		metrics: costMetrics(),
	}
	router := newPolicyRouter(resolver, adapter)
	if err := router.Stream(context.Background(), StreamRequest{
		RequestID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ModelID:   "model-1",
		Messages:  []Message{{Role: "user", Content: "hi"}},
	}, func(ModelEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := adapter.seen(); len(got) != 1 || got[0] != "cheap" {
		t.Fatalf("enabled cost policy selected %v, want cheap", got)
	}
	page, err := history.List(routing.DecisionQuery{Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("history = %#v err=%v", page, err)
	}
	if page.Items[0].SelectedChannelID != "cheap" || page.Items[0].Result != "succeeded" {
		t.Fatalf("record = %#v", page.Items[0])
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee") || strings.Contains(string(raw), "sk-") {
		t.Fatalf("history leaked secrets: %s", raw)
	}
}

func TestRouterStreamEnabledPolicyRespectsFailoverCap(t *testing.T) {
	adapter := &channelCallAdapter{fail: map[string]error{"cheap": errors.New("provider status=503")}}
	resolver := &policyAwareResolver{
		diagnosticsResolverStub: diagnosticsResolverStub{channels: expensiveCheapChannels()},
		policy: routing.Policy{
			Enabled: true, Strategy: routing.StrategyCost, CostWeight: 100,
			MaxFailoverAttempts: 0,
		},
		metrics: costMetrics(),
	}
	router := newPolicyRouter(resolver, adapter)
	err := router.Stream(context.Background(), StreamRequest{ModelID: "model-1", Messages: []Message{{Role: "user", Content: "hi"}}}, func(ModelEvent) error { return nil })
	if err == nil {
		t.Fatal("expected failure without failover")
	}
	if got := adapter.seen(); len(got) != 1 || got[0] != "cheap" {
		t.Fatalf("calls = %v, want only cheap", got)
	}
}

func TestRouterStreamEnabledPolicyFailoversBeforeOutput(t *testing.T) {
	adapter := &channelCallAdapter{fail: map[string]error{"cheap": errors.New("provider status=503")}}
	resolver := &policyAwareResolver{
		diagnosticsResolverStub: diagnosticsResolverStub{channels: expensiveCheapChannels()},
		policy: routing.Policy{
			Enabled: true, Strategy: routing.StrategyCost, CostWeight: 100,
			MaxFailoverAttempts: 1,
		},
		metrics: costMetrics(),
	}
	router := newPolicyRouter(resolver, adapter)
	if err := router.Stream(context.Background(), StreamRequest{ModelID: "model-1", Messages: []Message{{Role: "user", Content: "hi"}}}, func(ModelEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := adapter.seen(); len(got) != 2 || got[0] != "cheap" || got[1] != "expensive" {
		t.Fatalf("calls = %v, want cheap then expensive", got)
	}
}

func TestRouterStreamEnabledPolicyStopsAfterObservableOutput(t *testing.T) {
	adapter := &channelCallAdapter{
		fail: map[string]error{"cheap": ErrMidStreamInterrupted},
		emit: map[string]string{"cheap": "partial"},
	}
	resolver := &policyAwareResolver{
		diagnosticsResolverStub: diagnosticsResolverStub{channels: expensiveCheapChannels()},
		policy: routing.Policy{
			Enabled: true, Strategy: routing.StrategyCost, CostWeight: 100,
			MaxFailoverAttempts: 5,
		},
		metrics: costMetrics(),
	}
	router := newPolicyRouter(resolver, adapter)
	err := router.Stream(context.Background(), StreamRequest{ModelID: "model-1", Messages: []Message{{Role: "user", Content: "hi"}}}, func(ModelEvent) error { return nil })
	if !errors.Is(err, ErrMidStreamInterrupted) {
		t.Fatalf("err = %v, want mid-stream", err)
	}
	if got := adapter.seen(); len(got) != 1 || got[0] != "cheap" {
		t.Fatalf("failovers after output: %v", got)
	}
}

func TestRouterStreamEnabledBalancedPolicyPrefersHigherUsageRemaining(t *testing.T) {
	adapter := &channelCallAdapter{}
	history := &routing.History{}
	resolver := &policyAwareResolver{
		diagnosticsResolverStub: diagnosticsResolverStub{channels: expensiveCheapChannels()},
		policy: routing.Policy{
			Enabled: true, Strategy: routing.StrategyBalanced,
			LatencyWeight: 0, CostWeight: 0, ReliabilityWeight: 0, BalanceWeight: 100,
			MaxFailoverAttempts: 1,
		},
		history: history,
		metrics: map[string]routing.CandidateInput{
			"expensive": {ChannelID: "expensive", Available: true, BalanceKnown: true, UsageRemainingBasisPoints: 1000, PricingKnown: true, EstimatedCostMicrosUSD: 9_000_000},
			"cheap":     {ChannelID: "cheap", Available: true, BalanceKnown: true, UsageRemainingBasisPoints: 9000, PricingKnown: true, EstimatedCostMicrosUSD: 1_000},
		},
	}
	router := newPolicyRouter(resolver, adapter)
	if err := router.Stream(context.Background(), StreamRequest{ModelID: "model-1", Messages: []Message{{Role: "user", Content: "hi"}}}, func(ModelEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := adapter.seen(); len(got) != 1 || got[0] != "cheap" {
		t.Fatalf("usage-aware balanced policy selected %v, want cheap", got)
	}
}

func TestRouterStreamEnabledPolicySkipsExhaustedUsageWindow(t *testing.T) {
	adapter := &channelCallAdapter{}
	resolver := &policyAwareResolver{
		diagnosticsResolverStub: diagnosticsResolverStub{channels: expensiveCheapChannels()},
		policy: routing.Policy{
			Enabled: true, Strategy: routing.StrategyBalanced, BalanceWeight: 100,
		},
		metrics: map[string]routing.CandidateInput{
			"expensive": {ChannelID: "expensive", Available: true, BalanceKnown: true, UsageRemainingBasisPoints: 9000},
			"cheap":     {ChannelID: "cheap", Available: false},
		},
	}
	router := newPolicyRouter(resolver, adapter)
	if err := router.Stream(context.Background(), StreamRequest{ModelID: "model-1", Messages: []Message{{Role: "user", Content: "hi"}}}, func(ModelEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := adapter.seen(); len(got) != 1 || got[0] != "expensive" {
		t.Fatalf("calls = %v, want expensive only", got)
	}
}
func TestRouterStreamEnabledCostPolicyDoesNotTreatUnknownPriceAsFree(t *testing.T) {
	adapter := &channelCallAdapter{}
	resolver := &policyAwareResolver{
		diagnosticsResolverStub: diagnosticsResolverStub{channels: expensiveCheapChannels()},
		policy: routing.Policy{
			Enabled: true, Strategy: routing.StrategyCost, CostWeight: 100,
		},
		metrics: map[string]routing.CandidateInput{
			"expensive": {ChannelID: "expensive", Available: true, PricingKnown: false, EstimatedCostMicrosUSD: 0},
			"cheap":     {ChannelID: "cheap", Available: true, PricingKnown: true, EstimatedCostMicrosUSD: 1_000},
		},
	}
	router := newPolicyRouter(resolver, adapter)
	if err := router.Stream(context.Background(), StreamRequest{ModelID: "model-1", Messages: []Message{{Role: "user", Content: "hi"}}}, func(ModelEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := adapter.seen(); len(got) != 1 || got[0] != "cheap" {
		t.Fatalf("unknown price selected as free: %v", got)
	}
}

func TestRouterStreamEnabledPolicyIdleRetryDoesNotConsumeFailover(t *testing.T) {
	adapter := &idleStallAdapter{stallsLeft: 1}
	history := &routing.History{}
	resolver := &policyAwareResolver{
		diagnosticsResolverStub: diagnosticsResolverStub{channels: expensiveCheapChannels()},
		policy: routing.Policy{
			Enabled: true, Strategy: routing.StrategyCost, CostWeight: 100,
			MaxFailoverAttempts: 0,
		},
		history: history,
		metrics: costMetrics(),
	}
	router := newPolicyRouter(resolver, adapter)
	if err := router.Stream(context.Background(), idleRetryStreamRequest(), func(ModelEvent) error { return nil }); err != nil {
		t.Fatalf("Stream() = %v, want nil after same-channel idle retry", err)
	}
	if adapter.calls != 2 {
		t.Fatalf("adapter calls = %d, want 2", adapter.calls)
	}
	page, err := history.List(routing.DecisionQuery{Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].SelectedChannelID != "cheap" || page.Items[0].AttemptCount != 2 {
		t.Fatalf("history = %#v err=%v", page, err)
	}
}
