package config

import (
	"context"
	"testing"
)

func TestIsObservabilityLogEnabledDoesNotCloneConfigurationCollections(t *testing.T) {
	manager := newMCPTrustTestManager(t)
	cfg := manager.Current()
	cfg.Log = true
	cfg.SkillMCPScan = SkillMCPScanConfig{
		SkillSources:       map[string]bool{"cursor": true},
		MCPSources:         map[string]bool{"cursor": true},
		EnabledSkills:      map[string]bool{"skill": true},
		DisabledSkills:     map[string]bool{"legacy": true},
		DisabledMCPServers: map[string]bool{"server": true},
		SkillSummaries:     map[string]string{"skill": "summary"},
		MCPSummaries:       map[string]string{"server": "summary"},
	}
	if _, err := manager.Save(context.Background(), cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	allocations := testing.AllocsPerRun(100, func() {
		if !manager.IsObservabilityLogEnabled(context.Background()) {
			t.Fatal("IsObservabilityLogEnabled() = false, want true")
		}
	})
	if allocations != 0 {
		t.Fatalf("IsObservabilityLogEnabled() allocations = %v, want 0", allocations)
	}
}

func TestDebugLogMaxBytesDoesNotCloneConfigurationCollections(t *testing.T) {
	manager := newMCPTrustTestManager(t)
	cfg := manager.Current()
	cfg.DebugLogMaxBytes = 1024
	cfg.SkillMCPScan = SkillMCPScanConfig{
		SkillSources:       map[string]bool{"cursor": true},
		MCPSources:         map[string]bool{"cursor": true},
		EnabledSkills:      map[string]bool{"skill": true},
		DisabledSkills:     map[string]bool{"legacy": true},
		DisabledMCPServers: map[string]bool{"server": true},
		SkillSummaries:     map[string]string{"skill": "summary"},
		MCPSummaries:       map[string]string{"server": "summary"},
	}
	if _, err := manager.Save(context.Background(), cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	allocations := testing.AllocsPerRun(100, func() {
		if got := manager.DebugLogMaxBytes(context.Background()); got != 1024 {
			t.Fatalf("DebugLogMaxBytes() = %d, want 1024", got)
		}
	})
	if allocations != 0 {
		t.Fatalf("DebugLogMaxBytes() allocations = %v, want 0", allocations)
	}
}
