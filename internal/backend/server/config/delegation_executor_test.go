package config

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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

func TestNormalizeDelegationExecutorConfigRejectsDuplicateIDs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Delegation.Executors = []DelegationExecutorConfig{
		{ID: " claude-code ", Kind: DelegationExecutorKindBuiltin},
		{ID: "claude-code", Kind: DelegationExecutorKindBuiltin, Enabled: false},
	}
	_, err := NormalizeConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
}

func TestNormalizeDelegationExecutorConfigAppliesBoundsAndDeduplicatesEnvironment(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Delegation.ExecutorFailoverLimit = -1
	cfg.Delegation.Executors = []DelegationExecutorConfig{{
		ID: " claude-code ", Kind: " builtin ", Enabled: true, Priority: -5,
		ProbeTimeoutSeconds: -1, ExecutionTimeoutSeconds: -1,
		EnvironmentVariables: []string{" ANTHROPIC_API_KEY ", "ANTHROPIC_API_KEY"},
		Options:              map[string]string{" outputFormat ": " stream-json "},
	}}
	got, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig() error=%v", err)
	}
	if got.Delegation.ExecutorFailoverLimit != DefaultDelegationExecutorFailoverLimit || len(got.Delegation.Executors) != 1 {
		t.Fatalf("delegation=%#v", got.Delegation)
	}
	executor := got.Delegation.Executors[0]
	if executor.ID != "claude-code" || executor.Kind != DelegationExecutorKindBuiltin || !executor.Enabled || executor.Priority != 0 {
		t.Fatalf("executor=%#v", executor)
	}
	if executor.ProbeTimeoutSeconds != DefaultDelegationExecutorProbeTimeoutSeconds || executor.ExecutionTimeoutSeconds != DefaultDelegationExecutorExecutionTimeoutSeconds {
		t.Fatalf("timeouts=%#v", executor)
	}
	if !reflect.DeepEqual(executor.EnvironmentVariables, []string{"ANTHROPIC_API_KEY"}) || executor.Options["outputFormat"] != "stream-json" {
		t.Fatalf("policy=%#v", executor)
	}
}

func TestNormalizeCustomExecutorContract(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]string
		want    string
	}{
		{name: "arguments must be JSON tokens", options: map[string]string{"arguments": `--prompt {{prompt}} | tee out`}, want: "arguments"},
		{name: "unknown template token", options: map[string]string{"arguments": `["{{unknown}}"]`}, want: "unknown template variable"},
		{name: "secret literal", options: map[string]string{"arguments": `["--key","sk-custom-secret-12345678"]`}, want: "secret literal"},
		{name: "unbounded output", options: map[string]string{"arguments": `[]`, "outputLimitBytes": "4194305"}, want: "outputLimitBytes"},
		{name: "missing final mapping", options: map[string]string{"arguments": `[]`, "outputMode": "jsonl"}, want: "finalField"},
		{name: "version arguments cannot use task tokens", options: map[string]string{"arguments": `["{{prompt}}"]`, "versionArguments": `["--workspace","{{workspace}}"]`}, want: "versionArguments cannot contain template variables"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Delegation.Executors = []DelegationExecutorConfig{{ID: "grok-cli", Kind: DelegationExecutorKindCustom, Executable: "grok", Options: tc.options}}
			_, err := NormalizeConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NormalizeConfig() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestNormalizeCustomExecutorContractAcceptsStructuredOptions(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Delegation.Executors = []DelegationExecutorConfig{{
		ID: "grok-cli", Kind: DelegationExecutorKindCustom, Executable: "grok",
		EnvironmentVariables: []string{"XAI_API_KEY"},
		Options: map[string]string{
			"arguments":        `["--workspace","{{workspace}}","--readonly","{{readonly}}"]`,
			"versionArguments": `["--version"]`, "stdinMode": "prompt", "outputMode": "jsonl",
			"finalField": "result.text", "progressField": "delta", "errorField": "error.message", "outputLimitBytes": "1048576",
		},
	}}
	got, err := NormalizeConfig(cfg)
	if err != nil || len(got.Delegation.Executors) != 1 {
		t.Fatalf("NormalizeConfig() got=%#v error=%v", got.Delegation.Executors, err)
	}
}

func TestNormalizeCustomExecutorRejectsBuiltinIDCollision(t *testing.T) {
	for _, id := range []string{"claude-code", "codex-cli", "gemini-cli", "cursor-agent"} {
		t.Run(id, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Delegation.Executors = []DelegationExecutorConfig{{
				ID: id, Kind: DelegationExecutorKindCustom, Executable: "custom",
				Options: map[string]string{"arguments": `["{{prompt}}"]`},
			}}
			_, err := NormalizeConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Fatalf("NormalizeConfig() error=%v", err)
			}
		})
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
		ID:   "custom-cli",
		Kind: DelegationExecutorKindBuiltin,
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
