package modelcontext

// ContextBudgetInput separates model capability from caller-imposed upper bounds.
// Non-positive values mean unknown or unspecified, never an instruction to expand.
type ContextBudgetInput struct {
	ModelID          string
	ConfiguredTokens int
	RequestedTokens  int
	CeilingTokens    int
}

// ContextBudget records both the individual limits and the effective hard ceiling.
// Source identifies the limiting input, not a provider-confirmed tokenizer result.
type ContextBudget struct {
	ModelID          string
	CapabilityTokens int
	ConfiguredTokens int
	RequestedTokens  int
	CeilingTokens    int
	EffectiveTokens  int
	Source           string
}

// ResolveContextBudget takes the minimum of all known positive limits. The legacy
// fallback applies only when every limit is unknown, so explicit small limits survive.
func ResolveContextBudget(input ContextBudgetInput) ContextBudget {
	budget := ContextBudget{
		ModelID:          input.ModelID,
		CapabilityTokens: WindowTokens(input.ModelID),
		ConfiguredTokens: input.ConfiguredTokens,
		RequestedTokens:  input.RequestedTokens,
		CeilingTokens:    input.CeilingTokens,
	}
	for _, limit := range []struct {
		value  int
		source string
	}{
		{budget.CapabilityTokens, "capability"},
		{input.ConfiguredTokens, "configured"},
		{input.RequestedTokens, "requested"},
		{input.CeilingTokens, "ceiling"},
	} {
		if limit.value > 0 && (budget.EffectiveTokens == 0 || limit.value < budget.EffectiveTokens) {
			budget.EffectiveTokens, budget.Source = limit.value, limit.source
		}
	}
	if budget.EffectiveTokens == 0 {
		budget.EffectiveTokens, budget.Source = 200000, "default"
	}
	return budget
}
