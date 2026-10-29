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

func TestResolveEffectiveThinkingEffortWithCustom(t *testing.T) {
	custom := []string{"adaptive", "deep-think"}
	tests := []struct {
		name              string
		runtimeValue      string
		maximum           string
		configuredDefault string
		want              string
	}{
		{
			name:         "custom runtime passes through over standard maximum",
			runtimeValue: "adaptive", maximum: "low", want: "adaptive",
		},
		{
			name:         "custom runtime not in channel list falls back to maximum",
			runtimeValue: "turbo", maximum: "high", want: "high",
		},
		{
			name:              "missing runtime uses custom channel default",
			maximum:           "",
			configuredDefault: "deep-think",
			want:              "deep-think",
		},
		{
			name:         "custom channel default never caps standard runtime",
			runtimeValue: "max", maximum: "", configuredDefault: "deep-think", want: "max",
		},
		{
			name:         "disabled maximum still wins over custom runtime",
			runtimeValue: "adaptive", maximum: "disabled", want: "disabled",
		},
		{
			name:         "standard runtime capped by standard maximum as before",
			runtimeValue: "max", maximum: "xhigh", want: "xhigh",
		},
		{
			name:         "empty everything yields empty",
			runtimeValue: "", maximum: "", want: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := resolveEffectiveThinkingEffortWithCustom(test.runtimeValue, test.maximum, test.configuredDefault, custom)
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}
