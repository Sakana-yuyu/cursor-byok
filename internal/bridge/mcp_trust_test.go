package bridge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cursor/internal/backend/forwarder"
	"cursor/internal/client"
	"cursor/internal/logger"
)

func TestConnectMCPWorkspaceTrustLifecycle(t *testing.T) {
	home := t.TempDir()
	setReaderMCPHomeForTest(t, home)
	workspace := t.TempDir()
	configPath := filepath.Join(workspace, ".cursor", "mcp.json")
	writeBridgeMCPConfig(t, configPath, "{\"mcpServers\":{\"workspace-server\":{\"url\":\"http://127.0.0.1:1/mcp\",\"type\":\"http\"}}}")
	forwarder.InvalidateMCPScanCache()
	t.Cleanup(forwarder.InvalidateMCPScanCache)

	service := NewProxyService(nil, nil, nil)
	initial := bridgeMCPServerByID(t, mustSkillsMCPScanSnapshot(t, service, workspace).MCPServers, "workspace-server")
	if !initial.TrustRequired || initial.Trusted || !initial.IsWorkspace {
		t.Fatalf("initial trust state = %+v", initial)
	}
	if initial.TrustFingerprint == "" || initial.TrustURLOrigin != "http://127.0.0.1:1" {
		t.Fatalf("initial trust preview = %+v", initial)
	}

	_, err := service.ConnectMCPServer(workspace, "workspace-server", "attempt-untrusted")
	var trustErr *forwarder.MCPTrustRequiredError
	if !errors.As(err, &trustErr) {
		t.Fatalf("ConnectMCPServer() error = %T %v, want *MCPTrustRequiredError", err, err)
	}
	if trustErr.Code() != forwarder.MCPTrustRequiredCode {
		t.Fatalf("trust error code = %q", trustErr.Code())
	}

	granted, err := service.GrantMCPServerTrust(workspace, "workspace-server")
	if err != nil {
		t.Fatalf("GrantMCPServerTrust() error = %v", err)
	}
	if !granted.Trusted || granted.TrustRequired {
		t.Fatalf("grant result = %+v", granted)
	}

	writeBridgeMCPConfig(t, configPath, "{\"mcpServers\":{\"workspace-server\":{\"url\":\"http://127.0.0.1:2/mcp\",\"type\":\"http\"}}}")
	forwarder.InvalidateMCPScanCache()
	changed := bridgeMCPServerByID(t, mustSkillsMCPScanSnapshot(t, service, workspace).MCPServers, "workspace-server")
	if changed.Trusted || !changed.TrustRequired || changed.TrustFingerprint == granted.TrustFingerprint {
		t.Fatalf("changed config trust state = %+v; prior = %+v", changed, granted)
	}

	revoked, err := service.RevokeMCPServerTrust(workspace, "workspace-server")
	if err != nil {
		t.Fatalf("RevokeMCPServerTrust() error = %v", err)
	}
	if revoked.Trusted || !revoked.TrustRequired {
		t.Fatalf("revoke result = %+v", revoked)
	}
}

func TestConnectMCPUserScopeTrustBehaviorUnchanged(t *testing.T) {
	home := t.TempDir()
	setReaderMCPHomeForTest(t, home)
	writeBridgeMCPConfig(t, filepath.Join(home, ".cursor", "mcp.json"), "{\"mcpServers\":{\"user-server\":{\"url\":\"http://127.0.0.1:1/mcp\",\"type\":\"http\"}}}")
	forwarder.InvalidateMCPScanCache()
	t.Cleanup(forwarder.InvalidateMCPScanCache)

	service := NewProxyService(nil, nil, nil)
	server := bridgeMCPServerByID(t, mustSkillsMCPScanSnapshot(t, service, "").MCPServers, "user-server")
	if server.TrustRequired || !server.Trusted || server.IsWorkspace {
		t.Fatalf("user trust state = %+v", server)
	}

	_, err := service.ConnectMCPServer("", "user-server", "attempt-user")
	var trustErr *forwarder.MCPTrustRequiredError
	if errors.As(err, &trustErr) {
		t.Fatalf("user-scope connect was trust-gated: %v", err)
	}
}

func TestSnapshotMCPServersPropagatesConfigLoadError(t *testing.T) {
	logger.Init()
	home := t.TempDir()
	setReaderMCPHomeForTest(t, home)
	configPath := filepath.Join(home, ".cursor-local-assistant-v2", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("mcpTrustGrants: ["), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}

	service := &ProxyService{core: client.NewProxyService(nil, nil, nil)}
	_, err := snapshotMCPServers(service, "")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "yaml") {
		t.Fatalf("snapshotMCPServers() error = %v, want YAML load error", err)
	}
}

func mustSkillsMCPScanSnapshot(t *testing.T, service *ProxyService, workspace string) SkillsMCPScanSnapshot {
	t.Helper()
	snapshot, err := service.GetSkillsMCPScanSnapshot(workspace)
	if err != nil {
		t.Fatalf("GetSkillsMCPScanSnapshot() error = %v", err)
	}
	return snapshot
}

func writeBridgeMCPConfig(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create MCP config directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write MCP config: %v", err)
	}
}

func bridgeMCPServerByID(t *testing.T, items []forwarder.MCPServerSnapshotItem, identifier string) forwarder.MCPServerSnapshotItem {
	t.Helper()
	for _, item := range items {
		if item.Identifier == identifier {
			return item
		}
	}
	t.Fatalf("MCP server %q not found: %+v", identifier, items)
	return forwarder.MCPServerSnapshotItem{}
}
