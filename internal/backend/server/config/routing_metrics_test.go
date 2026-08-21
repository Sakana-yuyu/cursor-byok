package config

import (
	"testing"

	"cursor/internal/routing"
)

func TestBuildRoutingCandidatesUsesUsageWindowSnapshot(t *testing.T) {
	manager := &Manager{}
	manager.setCurrent(Config{
		ModelAdapters: []ModelAdapterConfig{
			{ID: "low", DisplayName: "Low", Type: "openai", ModelID: "gpt-test"},
			{ID: "high", DisplayName: "High", Type: "openai", ModelID: "gpt-test"},
		},
	})
	snapshot := routing.NewMetricsSnapshot()
	snapshot.Set("low", routing.CandidateInput{BalanceKnown: true, UsageRemainingBasisPoints: 1000, Available: true})
	snapshot.Set("high", routing.CandidateInput{BalanceKnown: true, UsageRemainingBasisPoints: 9000, Available: true})
	manager.SetRoutingMetricsSnapshot(snapshot)

	candidates := manager.BuildRoutingCandidates("gpt-test")
	if len(candidates) != 2 {
		t.Fatalf("candidates = %#v", candidates)
	}
	policy := routing.Policy{
		Enabled: true, Strategy: routing.StrategyBalanced,
		LatencyWeight: 0, CostWeight: 0, ReliabilityWeight: 0, BalanceWeight: 100,
	}
	decision := routing.Rank(policy, routing.Request{ModelID: "gpt-test", Candidates: candidates})
	if decision.Candidates[0].ChannelID != "high" {
		t.Fatalf("ranked = %#v", decision.Candidates)
	}
}
