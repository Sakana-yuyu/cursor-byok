package routing

import "math"

// UsageWindowSnapshot is a credential-free usage window summary for routing.
type UsageWindowSnapshot struct {
	ID                string
	RemainingFraction *float64
	Status            string
}

// MetricsFromUsage derives routing balance fields from provider usage windows.
// Exhausted windows mark the candidate unavailable; remaining fractions use the
// tightest known window so the scarcest quota drives ranking.
func MetricsFromUsage(windows []UsageWindowSnapshot, remainingUSD *float64, unlimited bool) (balanceKnown bool, balanceMicrosUSD int64, usageRemainingBasisPoints int, available bool) {
	available = true
	if unlimited {
		return true, 0, 10_000, true
	}
	minRemaining := -1.0
	for _, window := range windows {
		switch window.Status {
		case "exhausted":
			return false, 0, 0, false
		}
		if window.RemainingFraction == nil {
			continue
		}
		frac := clampFraction(*window.RemainingFraction)
		if minRemaining < 0 || frac < minRemaining {
			minRemaining = frac
		}
	}
	if remainingUSD != nil {
		remaining := math.Max(0, *remainingUSD)
		return true, int64(remaining * 1_000_000), 0, available
	}
	if minRemaining >= 0 {
		return true, 0, int(minRemaining * 10_000), available
	}
	return false, 0, 0, available
}

func clampFraction(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// MergeCandidateMetrics overlays snapshot metrics onto a base candidate without
// clearing pricing or capability fields from configuration.
func MergeCandidateMetrics(base, overlay CandidateInput) CandidateInput {
	merged := base
	if overlay.BalanceKnown {
		merged.BalanceKnown = true
		merged.BalanceMicrosUSD = overlay.BalanceMicrosUSD
	}
	if overlay.UsageRemainingBasisPoints > 0 {
		merged.UsageRemainingBasisPoints = overlay.UsageRemainingBasisPoints
	}
	if !overlay.Available {
		merged.Available = false
	}
	if overlay.RecentTTFTMS > 0 {
		merged.RecentTTFTMS = overlay.RecentTTFTMS
	}
	if overlay.RecentSuccessBasisPoints > 0 {
		merged.RecentSuccessBasisPoints = overlay.RecentSuccessBasisPoints
	}
	if overlay.EstimatedCostMicrosUSD > 0 && overlay.PricingKnown {
		merged.EstimatedCostMicrosUSD = overlay.EstimatedCostMicrosUSD
		merged.PricingKnown = true
	}
	return merged
}
