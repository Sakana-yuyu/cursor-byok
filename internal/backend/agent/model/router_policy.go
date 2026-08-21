package modeladapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"cursor/internal/routing"
	legacyruntime "cursor/internal/runtime"
)

type routingPolicyProvider interface {
	RoutingPolicy() routing.Policy
}

type routingDecisionSink interface {
	RecordRoutingDecision(routing.DecisionRecord)
}

type routingMetricsProvider interface {
	RoutingCandidateMetrics(channelID string) (routing.CandidateInput, bool)
}

func (router *Router) currentRoutingPolicy() routing.Policy {
	if router == nil {
		return routing.DefaultPolicy()
	}
	provider, ok := router.resolver.(routingPolicyProvider)
	if !ok {
		return routing.DefaultPolicy()
	}
	policy, err := routing.NormalizePolicy(provider.RoutingPolicy())
	if err != nil {
		return routing.DefaultPolicy()
	}
	return policy
}

func (router *Router) resolveChannels(ctx context.Context, modelID string) ([]*legacyruntime.ResolvedChannel, error) {
	if router == nil || router.resolver == nil {
		return nil, nil
	}
	if resolver, ok := router.resolver.(multiChannelResolver); ok {
		return resolver.SelectChannelsForModel(ctx, modelID)
	}
	channel, err := router.resolver.SelectChannelForModel(ctx, modelID)
	if err != nil || channel == nil {
		return nil, err
	}
	return []*legacyruntime.ResolvedChannel{channel}, nil
}

func (router *Router) rankLiveChannels(
	policy routing.Policy,
	req StreamRequest,
	channels []*legacyruntime.ResolvedChannel,
) (routing.Decision, []*legacyruntime.ResolvedChannel) {
	candidates := make([]routing.CandidateInput, 0, len(channels))
	byID := make(map[string]*legacyruntime.ResolvedChannel, len(channels))
	for index, channel := range channels {
		if channel == nil {
			continue
		}
		channelID := strings.TrimSpace(channel.ID)
		if channelID == "" {
			continue
		}
		byID[channelID] = channel
		candidate := routing.CandidateInput{
			ChannelID:   channelID,
			ConfigOrder: index,
			Available:   true,
			Cooldown:    router.channelOnCooldown(channel),
		}
		if provider, ok := router.resolver.(routingMetricsProvider); ok {
			if extra, found := provider.RoutingCandidateMetrics(channelID); found {
				extra.ChannelID = channelID
				extra.ConfigOrder = index
				extra.Cooldown = candidate.Cooldown
				candidate = extra
			}
		}
		candidates = append(candidates, candidate)
	}
	decision := routing.Rank(policy, routing.Request{
		RequestHash: anonymousRequestHash(req),
		ModelID:     strings.TrimSpace(req.ModelID),
		Candidates:  candidates,
	})
	ranked := make([]*legacyruntime.ResolvedChannel, 0, len(decision.Candidates))
	for _, score := range decision.Candidates {
		if channel := byID[score.ChannelID]; channel != nil {
			ranked = append(ranked, channel)
		}
	}
	return decision, ranked
}

func (router *Router) channelOnCooldown(channel *legacyruntime.ResolvedChannel) bool {
	if router == nil || channel == nil {
		return false
	}
	key := channelHealthMapKey(channel)
	now := time.Now()
	router.healthMu.Lock()
	defer router.healthMu.Unlock()
	health, ok := router.healthByChannel[key]
	if !ok {
		return false
	}
	if !health.cooldownUntil.After(now) {
		delete(router.healthByChannel, key)
		return false
	}
	return true
}

func nextRankedChannel(
	ranked []*legacyruntime.ResolvedChannel,
	scores []routing.CandidateScore,
	tried map[string]struct{},
) *legacyruntime.ResolvedChannel {
	eligible := make(map[string]bool, len(scores))
	for _, score := range scores {
		eligible[score.ChannelID] = score.Eligible
	}
	for _, channel := range ranked {
		if channel == nil {
			continue
		}
		if _, used := tried[channelHealthMapKey(channel)]; used {
			continue
		}
		if !eligible[strings.TrimSpace(channel.ID)] {
			continue
		}
		return channel
	}
	return nil
}

func anonymousRequestHash(req StreamRequest) string {
	raw := strings.TrimSpace(req.RequestID)
	if raw == "" {
		raw = strings.TrimSpace(req.ModelCallID)
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

func (router *Router) recordLiveRoutingDecision(
	policy routing.Policy,
	req StreamRequest,
	decision routing.Decision,
	selectedChannelID string,
	attemptCount int,
	startedAt time.Time,
	streamErr error,
	ctxErr error,
) {
	if router == nil || !policy.Enabled {
		return
	}
	sink, ok := router.resolver.(routingDecisionSink)
	if !ok {
		return
	}
	result := "failed"
	if streamErr == nil {
		result = "succeeded"
	} else if ctxErr != nil {
		result = "canceled"
	}
	durationMS := int64(0)
	if !startedAt.IsZero() {
		durationMS = time.Since(startedAt).Milliseconds()
	}
	candidates := decision.Candidates
	if candidates == nil {
		candidates = []routing.CandidateScore{}
	}
	sink.RecordRoutingDecision(routing.DecisionRecord{
		DecisionID:        decision.DecisionID,
		ModelID:           strings.TrimSpace(req.ModelID),
		Strategy:          policy.Strategy,
		SelectedChannelID: strings.TrimSpace(selectedChannelID),
		Candidates:        candidates,
		AttemptCount:      attemptCount,
		Result:            result,
		DurationMS:        durationMS,
	})
}
