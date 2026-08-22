package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"cursor/internal/controlcenter"
)

const (
	StrategyManual    = "manual"
	StrategyBalanced  = "balanced"
	StrategyLatency   = "latency"
	StrategyCost      = "cost"
	StrategyStability = "stability"
)

type Policy struct {
	Enabled                 bool             `json:"enabled"`
	Strategy                string           `json:"strategy"`
	SessionAffinity         bool             `json:"sessionAffinity"`
	MaxFailoverAttempts     int              `json:"maxFailoverAttempts"`
	DailyBudgetMicrosUSD    int64            `json:"dailyBudgetMicrosUsd,omitempty"`
	SessionBudgetMicrosUSD  int64            `json:"sessionBudgetMicrosUsd,omitempty"`
	MinimumBalanceMicrosUSD int64            `json:"minimumBalanceMicrosUsd,omitempty"`
	MaximumThinkingEffort   string           `json:"maximumThinkingEffort,omitempty"`
	LatencyWeight           int              `json:"latencyWeight"`
	CostWeight              int              `json:"costWeight"`
	ReliabilityWeight       int              `json:"reliabilityWeight"`
	BalanceWeight           int              `json:"balanceWeight"`
	CapabilityRules         []CapabilityRule `json:"capabilityRules,omitempty"`
}

type CapabilityRule struct {
	Capability          string   `json:"capability"`
	Required            bool     `json:"required"`
	PreferredChannelIDs []string `json:"preferredChannelIds,omitempty"`
}

type Requirement struct {
	Capability string `json:"capability"`
	Required   bool   `json:"required"`
}

type PreviewRequest struct {
	ModelID                string        `json:"modelId"`
	EstimatedContextTokens int64         `json:"estimatedContextTokens,omitempty"`
	SessionHash            string        `json:"sessionHash,omitempty"`
	Requirements           []Requirement `json:"requirements,omitempty"`
}

type Request struct {
	RequestHash            string           `json:"requestHash"`
	ModelID                string           `json:"modelId"`
	EstimatedContextTokens int64            `json:"estimatedContextTokens,omitempty"`
	SessionHash            string           `json:"sessionHash,omitempty"`
	Requirements           []Requirement    `json:"requirements,omitempty"`
	Candidates             []CandidateInput `json:"candidates"`
}

type CandidateInput struct {
	ChannelID                 string   `json:"channelId"`
	ConfigOrder               int      `json:"configOrder"`
	Available                 bool     `json:"available"`
	Cooldown                  bool     `json:"cooldown"`
	RecentTTFTMS              int64    `json:"recentTtftMs,omitempty"`
	RecentSuccessBasisPoints  int      `json:"recentSuccessBasisPoints,omitempty"`
	EstimatedCostMicrosUSD    int64    `json:"estimatedCostMicrosUsd,omitempty"`
	PricingKnown              bool     `json:"pricingKnown"`
	BalanceMicrosUSD          int64    `json:"balanceMicrosUsd,omitempty"`
	BalanceKnown              bool     `json:"balanceKnown"`
	UsageRemainingBasisPoints int      `json:"usageRemainingBasisPoints,omitempty"`
	Capabilities              []string `json:"capabilities,omitempty"`
}

type CandidateScore struct {
	ChannelID                string   `json:"channelId"`
	Eligible                 bool     `json:"eligible"`
	Score                    int      `json:"score"`
	ReasonCodes              []string `json:"reasonCodes"`
	RecentTTFTMS             int64    `json:"recentTtftMs,omitempty"`
	RecentSuccessBasisPoints int      `json:"recentSuccessBasisPoints,omitempty"`
	EstimatedCostMicrosUSD   int64    `json:"estimatedCostMicrosUsd,omitempty"`
	PricingKnown             bool     `json:"pricingKnown"`
}

type DecisionPreview struct {
	DecisionID string           `json:"decisionId"`
	Strategy   string           `json:"strategy"`
	Candidates []CandidateScore `json:"candidates"`
}

type Decision struct {
	DecisionID              string           `json:"decisionId"`
	Candidates              []CandidateScore `json:"candidates"`
	EffectiveThinkingEffort string           `json:"effectiveThinkingEffort,omitempty"`
}

type DecisionQuery struct {
	ModelID    string `json:"modelId,omitempty"`
	ChannelID  string `json:"channelId,omitempty"`
	Result     string `json:"result,omitempty"`
	FromUnixMS int64  `json:"fromUnixMs,omitempty"`
	ToUnixMS   int64  `json:"toUnixMs,omitempty"`
	Limit      int    `json:"limit"`
	Cursor     string `json:"cursor,omitempty"`
}

type DecisionRecord struct {
	DecisionID             string           `json:"decisionId"`
	TimestampUnixMS        int64            `json:"timestampUnixMs"`
	ModelID                string           `json:"modelId"`
	Strategy               string           `json:"strategy"`
	SelectedChannelID      string           `json:"selectedChannelId,omitempty"`
	Candidates             []CandidateScore `json:"candidates"`
	AttemptCount           int              `json:"attemptCount"`
	Result                 string           `json:"result"`
	DurationMS             int64            `json:"durationMs,omitempty"`
	InputTokens            int64            `json:"inputTokens,omitempty"`
	OutputTokens           int64            `json:"outputTokens,omitempty"`
	EstimatedCostMicrosUSD int64            `json:"estimatedCostMicrosUsd,omitempty"`
}

type DecisionPage struct {
	Items      []DecisionRecord `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

func DefaultPolicy() Policy {
	return Policy{
		Enabled:             false,
		Strategy:            StrategyManual,
		MaxFailoverAttempts: 0,
		LatencyWeight:       25,
		CostWeight:          25,
		ReliabilityWeight:   25,
		BalanceWeight:       25,
	}
}

func NormalizePolicy(input Policy) (Policy, error) {
	output := DefaultPolicy()
	output.Enabled = input.Enabled
	output.SessionAffinity = input.SessionAffinity
	output.MaximumThinkingEffort = strings.TrimSpace(input.MaximumThinkingEffort)
	output.CapabilityRules = append([]CapabilityRule{}, input.CapabilityRules...)
	strategy := strings.TrimSpace(input.Strategy)
	if strategy == "" {
		strategy = StrategyManual
	}
	switch strategy {
	case StrategyManual, StrategyBalanced, StrategyLatency, StrategyCost, StrategyStability:
		output.Strategy = strategy
	default:
		return Policy{}, controlcenter.NewError("routing_policy_invalid", "strategy is invalid")
	}
	if input.MaxFailoverAttempts < 0 || input.MaxFailoverAttempts > 5 {
		return Policy{}, controlcenter.NewError("routing_policy_invalid", "max failover attempts is invalid")
	}
	output.MaxFailoverAttempts = input.MaxFailoverAttempts
	for _, amount := range []int64{input.DailyBudgetMicrosUSD, input.SessionBudgetMicrosUSD, input.MinimumBalanceMicrosUSD} {
		if amount < 0 {
			return Policy{}, controlcenter.NewError("routing_policy_invalid", "budget must be >= 0")
		}
	}
	output.DailyBudgetMicrosUSD = input.DailyBudgetMicrosUSD
	output.SessionBudgetMicrosUSD = input.SessionBudgetMicrosUSD
	output.MinimumBalanceMicrosUSD = input.MinimumBalanceMicrosUSD
	weights := []int{input.LatencyWeight, input.CostWeight, input.ReliabilityWeight, input.BalanceWeight}
	sum := 0
	for _, weight := range weights {
		if weight < 0 || weight > 100 {
			return Policy{}, controlcenter.NewError("routing_policy_invalid", "weight is invalid")
		}
		sum += weight
	}
	if sum == 0 {
		output.LatencyWeight = 25
		output.CostWeight = 25
		output.ReliabilityWeight = 25
		output.BalanceWeight = 25
	} else {
		output.LatencyWeight = input.LatencyWeight
		output.CostWeight = input.CostWeight
		output.ReliabilityWeight = input.ReliabilityWeight
		output.BalanceWeight = input.BalanceWeight
	}
	return output, nil
}

func Rank(policy Policy, request Request) Decision {
	normalized, err := NormalizePolicy(policy)
	if err != nil {
		normalized = DefaultPolicy()
	}
	scores := make([]CandidateScore, 0, len(request.Candidates))
	for _, candidate := range request.Candidates {
		score := scoreCandidate(normalized, request, candidate)
		scores = append(scores, score)
	}
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].Eligible != scores[j].Eligible {
			return scores[i].Eligible
		}
		if scores[i].Score != scores[j].Score {
			return scores[i].Score > scores[j].Score
		}
		leftOrder, rightOrder := orderOf(request.Candidates, scores[i].ChannelID), orderOf(request.Candidates, scores[j].ChannelID)
		if leftOrder != rightOrder {
			return leftOrder < rightOrder
		}
		return scores[i].ChannelID < scores[j].ChannelID
	})
	sum := sha256.Sum256([]byte(request.RequestHash + "|" + request.ModelID + "|" + normalized.Strategy))
	return Decision{
		DecisionID:              "dec-" + hex.EncodeToString(sum[:8]),
		Candidates:              scores,
		EffectiveThinkingEffort: normalized.MaximumThinkingEffort,
	}
}

func scoreCandidate(policy Policy, request Request, candidate CandidateInput) CandidateScore {
	result := CandidateScore{
		ChannelID:                candidate.ChannelID,
		Eligible:                 true,
		RecentTTFTMS:             candidate.RecentTTFTMS,
		RecentSuccessBasisPoints: candidate.RecentSuccessBasisPoints,
		EstimatedCostMicrosUSD:   candidate.EstimatedCostMicrosUSD,
		PricingKnown:             candidate.PricingKnown,
	}
	if !candidate.Available {
		result.Eligible = false
		result.ReasonCodes = append(result.ReasonCodes, "unavailable")
	}
	if candidate.Cooldown {
		result.Eligible = false
		result.ReasonCodes = append(result.ReasonCodes, "cooldown")
	}
	for _, requirement := range request.Requirements {
		if requirement.Required && !hasCapability(candidate.Capabilities, requirement.Capability) {
			result.Eligible = false
			result.ReasonCodes = append(result.ReasonCodes, "missing_capability")
		}
	}
	if policy.MinimumBalanceMicrosUSD > 0 && candidate.BalanceKnown && candidate.BalanceMicrosUSD < policy.MinimumBalanceMicrosUSD {
		result.Eligible = false
		result.ReasonCodes = append(result.ReasonCodes, "insufficient_balance")
	}
	if !result.Eligible {
		return result
	}
	switch policy.Strategy {
	case StrategyManual:
		result.Score = 1000 - candidate.ConfigOrder
		result.ReasonCodes = append(result.ReasonCodes, "manual_order")
	case StrategyLatency:
		result.Score = latencyScore(candidate.RecentTTFTMS)
		result.ReasonCodes = append(result.ReasonCodes, "latency")
	case StrategyCost:
		result.Score = costScore(candidate)
		result.ReasonCodes = append(result.ReasonCodes, "cost")
	case StrategyStability:
		result.Score = candidate.RecentSuccessBasisPoints
		result.ReasonCodes = append(result.ReasonCodes, "stability")
	default:
		result.Score = weightedScore(policy, candidate)
		result.ReasonCodes = append(result.ReasonCodes, "balanced")
	}
	return result
}

func weightedScore(policy Policy, candidate CandidateInput) int {
	latency := latencyScore(candidate.RecentTTFTMS) * policy.LatencyWeight
	cost := costScore(candidate) * policy.CostWeight
	reliability := candidate.RecentSuccessBasisPoints * policy.ReliabilityWeight / 100
	balance := balanceScore(candidate) * policy.BalanceWeight
	return latency + cost + reliability + balance
}

func balanceScore(candidate CandidateInput) int {
	if candidate.UsageRemainingBasisPoints > 0 {
		score := candidate.UsageRemainingBasisPoints / 100
		if score > 100 {
			return 100
		}
		return score
	}
	if candidate.BalanceKnown {
		return int(min64(candidate.BalanceMicrosUSD/1_000_000, 100))
	}
	return 0
}

func latencyScore(ttft int64) int {
	if ttft <= 0 {
		return 0
	}
	score := 1000 - int(ttft)
	if score < 0 {
		return 0
	}
	return score
}

func costScore(candidate CandidateInput) int {
	if !candidate.PricingKnown {
		return 0
	}
	score := 1000 - int(candidate.EstimatedCostMicrosUSD/1000)
	if score < 0 {
		return 0
	}
	return score
}

func hasCapability(values []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}

func orderOf(candidates []CandidateInput, channelID string) int {
	for _, candidate := range candidates {
		if candidate.ChannelID == channelID {
			return candidate.ConfigOrder
		}
	}
	return 1 << 20
}

func min64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

type History struct {
	mu      sync.Mutex
	records []DecisionRecord
}

func (history *History) Append(record DecisionRecord) {
	if history == nil {
		return
	}
	history.mu.Lock()
	defer history.mu.Unlock()
	if record.TimestampUnixMS == 0 {
		record.TimestampUnixMS = time.Now().UnixMilli()
	}
	history.records = append([]DecisionRecord{record}, history.records...)
	if len(history.records) > 500 {
		history.records = history.records[:500]
	}
}

func (history *History) List(query DecisionQuery) (DecisionPage, error) {
	limit := controlcenter.ClampLimit(query.Limit, 50, 1, 200)
	offset, err := controlcenter.DecodeOffsetCursor(query.Cursor)
	if err != nil {
		return DecisionPage{}, controlcenter.NewError("routing_history_read_failed", "cursor is invalid")
	}
	history.mu.Lock()
	defer history.mu.Unlock()
	filtered := make([]DecisionRecord, 0, len(history.records))
	for _, record := range history.records {
		if query.ModelID != "" && record.ModelID != query.ModelID {
			continue
		}
		if query.ChannelID != "" && record.SelectedChannelID != query.ChannelID {
			continue
		}
		if query.Result != "" && record.Result != query.Result {
			continue
		}
		if query.FromUnixMS > 0 && record.TimestampUnixMS < query.FromUnixMS {
			continue
		}
		if query.ToUnixMS > 0 && record.TimestampUnixMS > query.ToUnixMS {
			continue
		}
		filtered = append(filtered, record)
	}
	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := DecisionPage{Items: filtered[offset:end]}
	if page.Items == nil {
		page.Items = []DecisionRecord{}
	}
	if end < len(filtered) {
		page.NextCursor = controlcenter.EncodeOffsetCursor(end)
	}
	return page, nil
}
