package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreLoadBackfillsGoalDefaultsForLegacyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	legacy := []byte("backendListenAddr: 127.0.0.1:18090\nproxyListenAddr: 127.0.0.1:18080\n")
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	got, err := NewStore(path, "").Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := DefaultGoalConfig()
	if got.Goal != want {
		t.Fatalf("Goal = %+v, want legacy config defaults %+v", got.Goal, want)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	if !yamlHasKey(persisted, "goal") {
		t.Fatal("migrated config does not persist goal defaults")
	}
}

func TestSkillScanDefaultsToEmptyEnabledSkills(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.SkillMCPScan.EnabledSkills) != 0 {
		t.Fatalf("EnabledSkills = %v, want empty opt-in whitelist", cfg.SkillMCPScan.EnabledSkills)
	}
}

func TestLegacyDisabledSkillsDoNotBecomeEnabledSkills(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	legacy := []byte("backendListenAddr: 127.0.0.1:18090\nproxyListenAddr: 127.0.0.1:18080\nskillMcpScan:\n  enabled: true\n  disabledSkills:\n    old-skill: true\n")
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	got, err := NewStore(path, "").Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got.SkillMCPScan.EnabledSkills) != 0 {
		t.Fatalf("legacy disabledSkills must not migrate into enabledSkills: %v", got.SkillMCPScan.EnabledSkills)
	}
}

func TestNormalizeModelAdapterConfigsDeduplicatesChannelsAndSetsContext(t *testing.T) {
	first := ModelAdapterConfig{
		DisplayName:     "GPT-5.6 Luna",
		Type:            "openai",
		BaseURL:         "https://api.example.com/v1",
		APIKey:          "test-key",
		TooltipData:     "primary",
		ModelID:         "gpt-5.6-luna",
		ReasoningEffort: "medium",
		OpenAIEndpoint:  "/v1/responses",
	}
	duplicate := first
	duplicate.DisplayName = "GPT-5.6 Luna duplicate"
	first.GroupName = "OAI 供应商"
	duplicate.GroupName = first.GroupName

	got, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{first, duplicate})
	if err != nil {
		t.Fatalf("NormalizeModelAdapterConfigs() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("NormalizeModelAdapterConfigs() len = %d, want 1", len(got))
	}
	if got[0].ContextWindowTokens != 272_000 {
		t.Errorf("ContextWindowTokens = %d, want 272000", got[0].ContextWindowTokens)
	}
	if got[0].GroupName != "OAI 供应商" {
		t.Errorf("GroupName = %q, want %q", got[0].GroupName, "OAI 供应商")
	}
}

func TestNormalizeModelAdapterConfigsKeepsSameChannelInDifferentGroups(t *testing.T) {
	base := ModelAdapterConfig{
		DisplayName:     "GPT-5.6 Luna",
		Type:            "openai",
		BaseURL:         "https://api.example.com/v1",
		APIKey:          "test-key",
		TooltipData:     "primary",
		ModelID:         "gpt-5.6-luna",
		ReasoningEffort: "medium",
		OpenAIEndpoint:  "/v1/responses",
	}
	first := base
	first.GroupName = "供应商 A"
	second := base
	second.GroupName = "供应商 B"

	got, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{first, second})
	if err != nil {
		t.Fatalf("NormalizeModelAdapterConfigs() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("NormalizeModelAdapterConfigs() len = %d, want 2", len(got))
	}
	if got[0].GroupName != "供应商 A" || got[1].GroupName != "供应商 B" {
		t.Errorf("group names = [%q, %q], want [供应商 A, 供应商 B]", got[0].GroupName, got[1].GroupName)
	}
}
