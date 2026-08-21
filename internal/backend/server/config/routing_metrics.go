package config

import (
	"strings"

	"cursor/internal/routing"
)

func (manager *Manager) SetRoutingMetricsSnapshot(snapshot *routing.MetricsSnapshot) {
	if manager == nil {
		return
	}
	manager.routingMetrics = snapshot
}

func (manager *Manager) RoutingCandidateMetrics(channelID string) (routing.CandidateInput, bool) {
	if manager == nil {
		return routing.CandidateInput{}, false
	}
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return routing.CandidateInput{}, false
	}
	adapter, ok := manager.adapterByID(channelID)
	if !ok {
		return routing.CandidateInput{}, false
	}
	candidate := routingCandidateFromAdapter(adapter)
	if manager.routingMetrics != nil {
		if overlay, found := manager.routingMetrics.Get(channelID); found {
			candidate = routing.MergeCandidateMetrics(candidate, overlay)
		}
	}
	return candidate, true
}

func (manager *Manager) BuildRoutingCandidates(modelID string) []routing.CandidateInput {
	if manager == nil {
		return nil
	}
	modelID = strings.TrimSpace(modelID)
	candidates := make([]routing.CandidateInput, 0)
	for index, adapter := range manager.Current().ModelAdapters {
		if modelID != "" &&
			adapter.ModelID != modelID &&
			adapter.ID != modelID &&
			adapter.DisplayName != modelID {
			continue
		}
		candidate := routingCandidateFromAdapter(adapter)
		candidate.ConfigOrder = index
		if metrics, ok := manager.RoutingCandidateMetrics(strings.TrimSpace(adapter.ID)); ok {
			candidate = routing.MergeCandidateMetrics(candidate, metrics)
			candidate.ConfigOrder = index
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func (manager *Manager) adapterByID(channelID string) (ModelAdapterConfig, bool) {
	for _, adapter := range manager.Current().ModelAdapters {
		if strings.TrimSpace(adapter.ID) == channelID {
			return adapter, true
		}
	}
	return ModelAdapterConfig{}, false
}

func routingCandidateFromAdapter(adapter ModelAdapterConfig) routing.CandidateInput {
	channelID := strings.TrimSpace(adapter.ID)
	candidate := routing.CandidateInput{
		ChannelID:    channelID,
		Available:    true,
		Capabilities: []string{adapter.Type},
	}
	if adapter.Pricing != nil {
		candidate.PricingKnown = adapter.Pricing.Known
		if adapter.Pricing.Input != nil {
			candidate.EstimatedCostMicrosUSD = int64(*adapter.Pricing.Input * 1_000_000)
		}
	}
	return candidate
}
