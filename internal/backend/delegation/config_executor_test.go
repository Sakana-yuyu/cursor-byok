package delegation

import "testing"

func TestNormalizeRuntimeConfigClonesExecutorPolicy(t *testing.T) {
	input := RuntimeConfig{
		ExecutorFailoverLimit: 2,
		Executors: []RuntimeExecutorConfig{{
			ID:                   "claude-code",
			Kind:                 "builtin",
			Enabled:              true,
			EnvironmentVariables: []string{"ANTHROPIC_API_KEY"},
			Options:              map[string]string{"outputFormat": "stream-json"},
		}},
	}

	got := NormalizeRuntimeConfig(input)
	input.Executors[0].EnvironmentVariables[0] = "MUTATED"
	input.Executors[0].Options["outputFormat"] = "mutated"
	if got.ExecutorFailoverLimit != 2 || len(got.Executors) != 1 {
		t.Fatalf("normalized runtime config = %#v", got)
	}
	if got.Executors[0].EnvironmentVariables[0] != "ANTHROPIC_API_KEY" || got.Executors[0].Options["outputFormat"] != "stream-json" {
		t.Fatalf("executor policy was not cloned: %#v", got.Executors[0])
	}
}
