package routing

import "testing"

func TestMetricsFromUsagePrefersTighterWindowRemaining(t *testing.T) {
	low := 0.2
	high := 0.9
	known, micros, basis, available := MetricsFromUsage([]UsageWindowSnapshot{
		{ID: "5h", RemainingFraction: &high, Status: "ok"},
		{ID: "7d", RemainingFraction: &low, Status: "warning"},
	}, nil, false)
	if !known || micros != 0 || basis != 2000 || !available {
		t.Fatalf("metrics = known=%v micros=%d basis=%d available=%v", known, micros, basis, available)
	}
}

func TestMetricsFromUsageMarksExhaustedUnavailable(t *testing.T) {
	remaining := 0.5
	known, micros, basis, available := MetricsFromUsage([]UsageWindowSnapshot{
		{ID: "5h", RemainingFraction: &remaining, Status: "exhausted"},
	}, nil, false)
	if known || micros != 0 || basis != 0 || available {
		t.Fatalf("metrics = known=%v micros=%d basis=%d available=%v", known, micros, basis, available)
	}
}

func TestRankBalancedPrefersHigherUsageRemaining(t *testing.T) {
	policy := Policy{
		Enabled:           true,
		Strategy:          StrategyBalanced,
		LatencyWeight:     0,
		CostWeight:        0,
		ReliabilityWeight: 0,
		BalanceWeight:     100,
	}
	decision := Rank(policy, Request{
		ModelID: "m",
		Candidates: []CandidateInput{
			{ChannelID: "low", ConfigOrder: 0, Available: true, BalanceKnown: true, UsageRemainingBasisPoints: 1000},
			{ChannelID: "high", ConfigOrder: 1, Available: true, BalanceKnown: true, UsageRemainingBasisPoints: 9000},
		},
	})
	if decision.Candidates[0].ChannelID != "high" {
		t.Fatalf("candidates = %#v", decision.Candidates)
	}
}
