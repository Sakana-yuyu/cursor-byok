package modelcontext

import (
	"math"
	"testing"
)

func TestResolveContextBudget(t *testing.T) {
	capability := WindowTokens("gpt-4o")
	if capability <= 32000 {
		t.Fatalf("expected known capability > 32k, got %d", capability)
	}
	cases := []struct {
		name   string
		input  ContextBudgetInput
		want   int
		source string
	}{
		{"max int32 request", ContextBudgetInput{ModelID: "unlisted-model", RequestedTokens: math.MaxInt32}, math.MaxInt32, "requested"},
		{"max int32 bounded by ceiling", ContextBudgetInput{ModelID: "unlisted-model", RequestedTokens: math.MaxInt32, CeilingTokens: 32000}, 32000, "ceiling"},
		{"unknown fallback", ContextBudgetInput{ModelID: "unlisted-model"}, 200000, "default"},
		{"known capability", ContextBudgetInput{ModelID: "gpt-4o"}, capability, "capability"},
		{"configured small", ContextBudgetInput{ModelID: "gpt-4o", ConfiguredTokens: 16000}, 16000, "configured"},
		{"requested small", ContextBudgetInput{ModelID: "gpt-4o", ConfiguredTokens: 100000, RequestedTokens: 32000}, 32000, "requested"},
		{"ceiling wins", ContextBudgetInput{ModelID: "gpt-4o", ConfiguredTokens: 100000, RequestedTokens: 32000, CeilingTokens: 8000}, 8000, "ceiling"},
		{"capability wins", ContextBudgetInput{ModelID: "gpt-4o", ConfiguredTokens: 2000000, RequestedTokens: 2000000, CeilingTokens: 2000000}, capability, "capability"},
		{"unknown explicit tiny", ContextBudgetInput{ModelID: "unlisted-model", ConfiguredTokens: 7}, 7, "configured"},
		{"unknown large not default capped", ContextBudgetInput{ModelID: "unlisted-model", RequestedTokens: 1000000}, 1000000, "requested"},
		{"nonpositive ignored", ContextBudgetInput{ModelID: "unlisted-model", ConfiguredTokens: -1, RequestedTokens: -2, CeilingTokens: 12}, 12, "ceiling"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveContextBudget(tc.input)
			if got.EffectiveTokens != tc.want || got.Source != tc.source {
				t.Fatalf("budget=%+v, want %d/%s", got, tc.want, tc.source)
			}
			if got.ConfiguredTokens != tc.input.ConfiguredTokens || got.RequestedTokens != tc.input.RequestedTokens || got.CeilingTokens != tc.input.CeilingTokens || got.ModelID != tc.input.ModelID {
				t.Fatalf("input provenance lost: %+v", got)
			}
			if tc.input.ModelID == "unlisted-model" && got.CapabilityTokens != 0 {
				t.Fatalf("unknown falsely recognized: %+v", got)
			}
		})
	}
}
