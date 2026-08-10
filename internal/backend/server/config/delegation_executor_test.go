package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeDelegationExecutorConfigRejectsSecretValues(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Delegation.Executors = []DelegationExecutorConfig{{
		ID:                   "custom-cli",
		Kind:                 DelegationExecutorKindCustom,
		Executable:           "custom-agent",
		EnvironmentVariables: []string{"CUSTOM_API_KEY=secret-value"},
	}}

	_, err := NormalizeConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "environment variable name") {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
}

func TestNormalizeDelegationExecutorConfigRejectsInvalidID(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Delegation.Executors = []DelegationExecutorConfig{{ID: "../claude code", Kind: DelegationExecutorKindBuiltin}}

	_, err := NormalizeConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "id is invalid") {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
}

func TestNormalizeDelegationExecutorConfigDeduplicatesAndAppliesBounds(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Delegation.ExecutorFailoverLimit = -1
	cfg.Delegation.Executors = []DelegationExecutorConfig{
		{
			ID:                      " claude-code ",
			Kind:                    " builtin ",
			Enabled:                 true,
			Priority:                -5,
			ProbeTimeoutSeconds:     -1,
			ExecutionTimeoutSeconds: -1,
			EnvironmentVariables:    []string{" ANTHROPIC_API_KEY ", "ANTHROPIC_API_KEY"},
			Options:                 map[string]string{" outputFormat ": " stream-json "},
		},
		{ID: "claude-code", Kind: DelegationExecutorKindBuiltin, Enabled: false},
	}

	got, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if got.Delegation.ExecutorFailoverLimit != DefaultDelegationExecutorFailoverLimit {
		t.Fatalf("ExecutorFailoverLimit = %d", got.Delegation.ExecutorFailoverLimit)
	}
	if len(got.Delegation.Executors) != 1 {
		t.Fatalf("Executors = %#v", got.Delegation.Executors)
	}
	executor := got.Delegation.Executors[0]
	if executor.ID != "claude-code" || executor.Kind != DelegationExecutorKindBuiltin || !executor.Enabled {
		t.Fatalf("normalized executor = %#v", executor)
	}
	if executor.Priority != 0 || executor.ProbeTimeoutSeconds != DefaultDelegationExecutorProbeTimeoutSeconds || executor.ExecutionTimeoutSeconds != DefaultDelegationExecutorExecutionTimeoutSeconds {
		t.Fatalf("normalized limits = %#v", executor)
	}
	if len(executor.EnvironmentVariables) != 1 || executor.EnvironmentVariables[0] != "ANTHROPIC_API_KEY" {
		t.Fatalf("EnvironmentVariables = %v", executor.EnvironmentVariables)
	}
	if executor.Options["outputFormat"] != "stream-json" {
		t.Fatalf("Options = %v", executor.Options)
	}
}

func TestNormalizeDelegationExecutorConfigRequiresCustomExecutable(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Delegation.Executors = []DelegationExecutorConfig{{
		ID:   "custom-cli",
		Kind: DelegationExecutorKindCustom,
	}}

	_, err := NormalizeConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
}

func TestNormalizeDelegationExecutorConfigRejectsSensitiveOptions(t *testing.T) {
	for _, key := range []string{"apiKey", "apikey", "api_token"} {
		t.Run(key, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Delegation.Executors = []DelegationExecutorConfig{{
				ID:         "custom-cli",
				Kind:       DelegationExecutorKindCustom,
				Executable: "custom-agent",
				Options:    map[string]string{key: "secret-value"},
			}}

			_, err := NormalizeConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), "sensitive option") {
				t.Fatalf("NormalizeConfig() error = %v", err)
			}
		})
	}
}

func TestNormalizeDelegationExecutorConfigAllowsNonSensitiveOptionNamesContainingKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Delegation.Executors = []DelegationExecutorConfig{{
		ID:         "custom-cli",
		Kind:       DelegationExecutorKindCustom,
		Executable: "custom-agent",
		Options: map[string]string{
			"keyboardLayout": "us",
			"monkeyMode":     "enabled",
		},
	}}

	got, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	options := got.Delegation.Executors[0].Options
	if options["keyboardLayout"] != "us" || options["monkeyMode"] != "enabled" {
		t.Fatalf("Options = %v", options)
	}
}

func TestDelegationExecutorConfigRoundTripsWithoutSecretValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	store := NewStore(path, "")
	cfg := DefaultConfig()
	cfg.Delegation.Executors = []DelegationExecutorConfig{{
		ID:                   "claude-code",
		Kind:                 DelegationExecutorKindBuiltin,
		Enabled:              true,
		EnvironmentVariables: []string{"ANTHROPIC_API_KEY"},
	}}
	if _, err := store.Save(context.Background(), cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got.Delegation.Executors) != 1 || got.Delegation.Executors[0].EnvironmentVariables[0] != "ANTHROPIC_API_KEY" {
		t.Fatalf("round-trip executors = %#v", got.Delegation.Executors)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(persisted), "secret-value") {
		t.Fatalf("persisted secret value: %s", persisted)
	}
}

func TestDelegationRuntimeConfigIncludesExecutorPolicy(t *testing.T) {
	manager, err := NewManager(context.Background(), NewStore(filepath.Join(t.TempDir(), "config.yaml"), ""))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	cfg, err := manager.GetDelegationConfig(context.Background())
	if err != nil {
		t.Fatalf("GetDelegationConfig() error = %v", err)
	}
	cfg.ExecutorFailoverLimit = 2
	cfg.Executors = []DelegationExecutorConfig{{ID: "codex-cli", Kind: DelegationExecutorKindBuiltin, Enabled: true, EnvironmentVariables: []string{"OPENAI_API_KEY"}}}
	if _, err := manager.SaveDelegationConfig(context.Background(), cfg); err != nil {
		t.Fatalf("SaveDelegationConfig() error = %v", err)
	}
	runtimeConfig := manager.DelegationRuntimeConfig()
	if runtimeConfig.ExecutorFailoverLimit != 2 || len(runtimeConfig.Executors) != 1 || runtimeConfig.Executors[0].ID != "codex-cli" {
		t.Fatalf("runtime executor policy = %#v", runtimeConfig)
	}
}

func TestLegacyDelegationConfigLeavesExternalExecutorsDisabled(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.Delegation.Executors) != 0 {
		t.Fatalf("default executors = %#v", cfg.Delegation.Executors)
	}
	if !cfg.Delegation.Enabled {
		t.Fatal("existing delegation default must remain enabled")
	}
}

func TestCloneDelegationConfigClonesExecutorMapsAndSlices(t *testing.T) {
	input := DelegationConfig{Executors: []DelegationExecutorConfig{{
		ID:                   "claude-code",
		EnvironmentVariables: []string{"ANTHROPIC_API_KEY"},
		Options:              map[string]string{"outputFormat": "stream-json"},
	}}}
	cloned := cloneDelegationConfig(input)
	cloned.Executors[0].EnvironmentVariables[0] = "MUTATED"
	cloned.Executors[0].Options["outputFormat"] = "mutated"
	if input.Executors[0].EnvironmentVariables[0] != "ANTHROPIC_API_KEY" || input.Executors[0].Options["outputFormat"] != "stream-json" {
		t.Fatalf("clone mutation leaked into input: %#v", input.Executors[0])
	}
}
