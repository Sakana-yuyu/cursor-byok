package client

import (
	"strings"

	"cursor/internal/routing"
)

func providerUsageWindowSnapshots(balance ProviderBalance) []routing.UsageWindowSnapshot {
	windows := make([]routing.UsageWindowSnapshot, 0, len(balance.Windows))
	for _, window := range balance.Windows {
		windows = append(windows, routing.UsageWindowSnapshot{
			ID:                strings.TrimSpace(window.ID),
			RemainingFraction: window.RemainingFraction,
			Status:            strings.TrimSpace(window.Status),
		})
	}
	return windows
}

func routingMetricsFromBalance(balance ProviderBalance) routing.CandidateInput {
	known, micros, basis, available := routing.MetricsFromUsage(
		providerUsageWindowSnapshots(balance),
		balance.Remaining,
		balance.Unlimited,
	)
	return routing.CandidateInput{
		Available:                 available,
		BalanceKnown:              known,
		BalanceMicrosUSD:          micros,
		UsageRemainingBasisPoints: basis,
	}
}

func (s *ProxyService) RecordRoutingMetrics(adapterID string, balance ProviderBalance) {
	if s == nil || s.routingMetrics == nil || !balance.Supported {
		return
	}
	adapterID = strings.TrimSpace(adapterID)
	if adapterID == "" {
		return
	}
	s.routingMetrics.Set(adapterID, routingMetricsFromBalance(balance))
}
