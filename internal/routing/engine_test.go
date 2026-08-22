package routing

import "testing"

func TestRankDoesNotTreatUnknownPriceAsFree(t *testing.T) {
	policy := Policy{Enabled: true, Strategy: StrategyCost, CostWeight: 100, LatencyWeight: 0, ReliabilityWeight: 0, BalanceWeight: 0}
	decision := Rank(policy, Request{
		ModelID: "m",
		Candidates: []CandidateInput{
			{ChannelID: "known-expensive", ConfigOrder: 0, Available: true, PricingKnown: true, EstimatedCostMicrosUSD: 9_000_000},
			{ChannelID: "unknown", ConfigOrder: 1, Available: true, PricingKnown: false, EstimatedCostMicrosUSD: 0},
			{ChannelID: "known-cheap", ConfigOrder: 2, Available: true, PricingKnown: true, EstimatedCostMicrosUSD: 1_000},
		},
	})
	if len(decision.Candidates) != 3 || decision.Candidates[0].ChannelID != "known-cheap" {
		t.Fatalf("candidates = %#v", decision.Candidates)
	}
	if decision.Candidates[1].ChannelID != "known-expensive" {
		t.Fatalf("unknown price was ranked as free: %#v", decision.Candidates)
	}
}

func TestRankManualUsesConfigOrder(t *testing.T) {
	policy := DefaultPolicy()
	decision := Rank(policy, Request{
		Candidates: []CandidateInput{
			{ChannelID: "b", ConfigOrder: 1, Available: true},
			{ChannelID: "a", ConfigOrder: 0, Available: true},
		},
	})
	if decision.Candidates[0].ChannelID != "a" {
		t.Fatalf("got %#v", decision.Candidates)
	}
}

func TestNormalizePolicyRejectsBadWeights(t *testing.T) {
	_, err := NormalizePolicy(Policy{Strategy: "manual", LatencyWeight: 101})
	if err == nil {
		t.Fatal("expected invalid weights")
	}
}
