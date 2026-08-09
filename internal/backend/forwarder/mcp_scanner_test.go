package forwarder

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMCPWorkspaceConfigPrecedesUserAndWinsEffectiveSelection(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)
	workspace := t.TempDir()
	writeTestMCPJSON(t, filepath.Join(home, ".cursor", "mcp.json"), "node-user")
	writeTestMCPJSON(t, filepath.Join(workspace, ".cursor", "mcp.json"), "node-workspace")

	files := orderedMCPConfigFiles(workspace)
	workspaceIndex := mcpConfigFileIndex(files, filepath.Join(workspace, ".cursor", "mcp.json"))
	userIndex := mcpConfigFileIndex(files, filepath.Join(home, ".cursor", "mcp.json"))
	if workspaceIndex < 0 || userIndex < 0 || workspaceIndex >= userIndex {
		t.Fatalf("Cursor config order workspace=%d user=%d, want workspace first", workspaceIndex, userIndex)
	}

	InvalidateMCPScanCache()
	t.Cleanup(InvalidateMCPScanCache)
	configs := effectiveMCPServerConfigs(ScanMCPServerConfigs(workspace, SkillMCPScanSettings{Enabled: true}))
	if len(configs) != 1 {
		t.Fatalf("effective configs = %d, want 1: %+v", len(configs), configs)
	}
	if configs[0].Command != "node-workspace" || configs[0].Scope != MCPConfigScopeWorkspace {
		t.Fatalf("effective config = %+v, want workspace command", configs[0])
	}
}

func TestMCPWorkspaceFilesPrecedeUserFilesForEveryDualScopeSource(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)
	workspace := t.TempDir()
	files := orderedMCPConfigFiles(workspace)

	for _, source := range []MCPSource{
		MCPSourceCursor,
		MCPSourceClaude,
		MCPSourceShared,
		MCPSourceZCode,
		MCPSourceCodex,
		MCPSourceGemini,
		MCPSourceWindsurf,
		MCPSourceVSCode,
	} {
		workspaceIndex := firstMCPConfigFileIndex(files, source, MCPConfigScopeWorkspace)
		userIndex := firstMCPConfigFileIndex(files, source, MCPConfigScopeUser)
		if workspaceIndex < 0 || userIndex < 0 || workspaceIndex >= userIndex {
			t.Errorf("source %q order workspace=%d user=%d, want workspace first", source, workspaceIndex, userIndex)
		}
	}
}

func TestGeminiMCPWorkspaceConfigurationIsDiscovered(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)
	workspace := t.TempDir()
	writeTestMCPJSON(t, filepath.Join(home, ".gemini", "settings.json"), "gemini-user")
	writeTestMCPJSON(t, filepath.Join(workspace, ".gemini", "settings.json"), "gemini-workspace")

	InvalidateMCPScanCache()
	t.Cleanup(InvalidateMCPScanCache)
	configs := ScanMCPServerConfigs(workspace, SkillMCPScanSettings{Enabled: true})
	var workspaceConfig, userConfig *MCPServerConfig
	for index := range configs {
		config := &configs[index]
		if config.Source != MCPSourceGemini || config.Identifier != "same-server" {
			continue
		}
		if config.Scope == MCPConfigScopeWorkspace {
			workspaceConfig = config
		} else if config.Scope == MCPConfigScopeUser {
			userConfig = config
		}
	}
	if workspaceConfig == nil || workspaceConfig.Command != "gemini-workspace" {
		t.Fatalf("Gemini workspace config missing or wrong: %+v", workspaceConfig)
	}
	if userConfig == nil || userConfig.Command != "gemini-user" {
		t.Fatalf("Gemini user config missing or wrong: %+v", userConfig)
	}
}

func TestCopilotGitHubMCPWorkspaceConfigurationIsDiscovered(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)
	workspace := t.TempDir()
	writeTestMCPJSON(t, filepath.Join(workspace, ".github", "mcp.json"), "copilot-workspace")

	InvalidateMCPScanCache()
	t.Cleanup(InvalidateMCPScanCache)
	configs := ScanMCPServerConfigs(workspace, SkillMCPScanSettings{Enabled: true})
	for _, config := range configs {
		if config.Source != MCPSourceCopilot || config.Identifier != "same-server" {
			continue
		}
		if config.Scope != MCPConfigScopeWorkspace || config.Command != "copilot-workspace" {
			t.Fatalf("Copilot workspace config = %+v, want workspace command", config)
		}
		return
	}
	t.Fatalf("Copilot .github/mcp.json config not discovered: %+v", configs)
}

func writeTestMCPJSON(t *testing.T, path, command string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create MCP config directory: %v", err)
	}
	data := []byte(`{"mcpServers":{"same-server":{"command":"` + command + `"}}}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write MCP config: %v", err)
	}
}

func mcpConfigFileIndex(files []mcpConfigFile, path string) int {
	for index, file := range files {
		if file.Path == path {
			return index
		}
	}
	return -1
}

func firstMCPConfigFileIndex(files []mcpConfigFile, source MCPSource, scope MCPConfigScope) int {
	for index, file := range files {
		if file.Source == source && file.Scope == scope {
			return index
		}
	}
	return -1
}
