package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"cursor/internal/backend/forwarder"
	"cursor/internal/backend/runtimeconfig"
)

const (
	testMCPFingerprintA = "mcp-trust-v1:sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testMCPFingerprintB = "mcp-trust-v1:sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestMCPTrustGrantsNormalizeDeduplicateSortAndPersistOnlyKeys(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MCPTrustGrants = []runtimeconfig.MCPTrustRecord{
		{RuntimeScope: " workspace:/z/repo ", Identifier: " Server-B ", Fingerprint: strings.ToUpper(testMCPFingerprintB)},
		{RuntimeScope: "workspace:/a/other/../repo", Identifier: "server-a", Fingerprint: testMCPFingerprintA},
		{RuntimeScope: "workspace:/a/repo", Identifier: " SERVER-A ", Fingerprint: testMCPFingerprintA},
		{RuntimeScope: "user", Identifier: "ignored", Fingerprint: testMCPFingerprintA},
		{RuntimeScope: "workspace:/missing", Identifier: "", Fingerprint: testMCPFingerprintA},
	}
	normalized, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	want := []runtimeconfig.MCPTrustRecord{
		{RuntimeScope: forwarder.MCPRuntimeScope("/a/repo"), Identifier: "server-a", Fingerprint: testMCPFingerprintA},
		{RuntimeScope: forwarder.MCPRuntimeScope("/z/repo"), Identifier: "server-b", Fingerprint: testMCPFingerprintB},
	}
	if !reflect.DeepEqual(normalized.MCPTrustGrants, want) {
		t.Fatalf("normalized grants = %+v, want %+v", normalized.MCPTrustGrants, want)
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	store := NewStore(path, "")
	if _, err := store.Save(context.Background(), normalized); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(persisted)
	for _, expected := range []string{forwarder.MCPRuntimeScope("/a/repo"), "server-a", testMCPFingerprintA} {
		if !strings.Contains(text, expected) {
			t.Fatalf("persisted trust metadata missing %q: %s", expected, text)
		}
	}
	for _, forbidden := range []string{"command", "args", "env-secret", "header-secret", "url-secret"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("persisted trust metadata contains forbidden value %q: %s", forbidden, text)
		}
	}
}

func TestMCPTrustManagerGrantAndRevokeAreAtomicReadModifyWrite(t *testing.T) {
	manager := newMCPTrustTestManager(t)
	ctx := context.Background()
	const grants = 16
	var wg sync.WaitGroup
	for index := 0; index < grants; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			identifier := fmt.Sprintf("server-%02d", index)
			if err := manager.GrantMCPServerTrust(ctx, "workspace:/repo", identifier, testMCPFingerprintA); err != nil {
				t.Errorf("GrantMCPServerTrust(%s): %v", identifier, err)
			}
		}()
	}
	wg.Wait()
	cfg := manager.Current()
	if len(cfg.MCPTrustGrants) != grants {
		t.Fatalf("grants after concurrent updates = %d, want %d: %+v", len(cfg.MCPTrustGrants), grants, cfg.MCPTrustGrants)
	}

	if err := manager.GrantMCPServerTrust(ctx, "workspace:/other", "server-00", testMCPFingerprintB); err != nil {
		t.Fatalf("grant other workspace: %v", err)
	}
	if err := manager.RevokeMCPServerTrust(ctx, "workspace:/repo", "SERVER-00"); err != nil {
		t.Fatalf("RevokeMCPServerTrust() error = %v", err)
	}
	if manager.HasMCPServerTrust("workspace:/repo", "server-00", testMCPFingerprintA) {
		t.Fatal("revoked workspace trust still matches")
	}
	if !manager.HasMCPServerTrust("workspace:/other", "server-00", testMCPFingerprintB) {
		t.Fatalf("revoke removed another workspace grant: %+v", manager.Current().MCPTrustGrants)
	}
}

func TestMCPTrustManagerSavePreservesConcurrentTrustMetadata(t *testing.T) {
	manager := newMCPTrustTestManager(t)
	ctx := context.Background()
	stale := manager.Current()
	if err := manager.GrantMCPServerTrust(ctx, "workspace:/repo", "server", testMCPFingerprintA); err != nil {
		t.Fatalf("GrantMCPServerTrust() error = %v", err)
	}
	stale.Log = true
	if _, err := manager.Save(ctx, stale); err != nil {
		t.Fatalf("Save(stale) error = %v", err)
	}
	if !manager.HasMCPServerTrust("workspace:/repo", "server", testMCPFingerprintA) {
		t.Fatalf("stale full-config save lost trust grant: %+v", manager.Current().MCPTrustGrants)
	}
}

func TestMCPTrustManagerCurrentClonesTrustAndScanCollections(t *testing.T) {
	manager := newMCPTrustTestManager(t)
	ctx := context.Background()
	cfg := manager.Current()
	cfg.SkillMCPScan.DisabledMCPServers = map[string]bool{"workspace-server": true}
	if _, err := manager.Save(ctx, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := manager.GrantMCPServerTrust(ctx, "workspace:/repo", "workspace-server", testMCPFingerprintA); err != nil {
		t.Fatalf("GrantMCPServerTrust() error = %v", err)
	}

	snapshot := manager.Current()
	snapshot.SkillMCPScan.DisabledMCPServers["workspace-server"] = false
	snapshot.MCPTrustGrants[0].Identifier = "mutated"

	current := manager.Current()
	if !current.SkillMCPScan.DisabledMCPServers["workspace-server"] {
		t.Fatal("Current() scan maps alias caller mutations")
	}
	if current.MCPTrustGrants[0].Identifier != "workspace-server" {
		t.Fatalf("Current() trust grants alias caller mutations: %+v", current.MCPTrustGrants)
	}
}

func newMCPTrustTestManager(t *testing.T) *Manager {
	t.Helper()
	manager, err := NewManager(context.Background(), NewStore(filepath.Join(t.TempDir(), "config.yaml"), ""))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager
}
