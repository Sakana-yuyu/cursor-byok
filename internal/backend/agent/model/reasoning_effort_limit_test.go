package modeladapter

import "testing"

func TestResolveEffectiveThinkingEffortHonorsConfiguredMaximum(t *testing.T) {
	tests := []struct {
		name         string
		runtimeValue string
		maximum      string
		want         string
	}{
		{name: "missing runtime uses maximum", maximum: "medium", want: "medium"},
		{name: "lower runtime preserved", runtimeValue: "low", maximum: "medium", want: "low"},
		{name: "equal runtime preserved", runtimeValue: "medium", maximum: "medium", want: "medium"},
		{name: "higher runtime capped", runtimeValue: "max", maximum: "medium", want: "medium"},
		{name: "disabled always allowed", runtimeValue: "disabled", maximum: "medium", want: "disabled"},
		{name: "missing maximum preserves runtime", runtimeValue: "max", want: "max"},
		{name: "invalid runtime uses maximum", runtimeValue: "turbo", maximum: "high", want: "high"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveEffectiveThinkingEffort(test.runtimeValue, test.maximum); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}
