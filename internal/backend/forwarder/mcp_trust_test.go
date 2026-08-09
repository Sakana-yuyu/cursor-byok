package forwarder

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMCPTrustFingerprintTracksExecutableIdentityWithoutSecretValues(t *testing.T) {
	base := MCPServerConfig{
		Identifier:   " Workspace Server ",
		Scope:        MCPConfigScopeWorkspace,
		RuntimeScope: MCPRuntimeScope("C:\\Repo\\Example"),
		Transport:    "streamable_http",
		Command:      "C:\\Tools\\runner.exe",
		Args:         []string{"--mode", "safe"},
		Env:          map[string]string{"API_TOKEN": "secret-one", "MODE": "safe"},
		Headers:      map[string]string{"Authorization": "Bearer secret-one", "X-Mode": "safe"},
		Cwd:          "C:\\Repo\\Example",
		ConfigPath:   "C:\\Repo\\Example\\.cursor\\mcp.json",
		URL:          "https://user:password@Example.com:443/private/path?token=secret-one#fragment",
	}

	fingerprint := MCPTrustFingerprint(base)
	if !strings.HasPrefix(fingerprint, "mcp-trust-v1:sha256:") {
		t.Fatalf("fingerprint = %q, want versioned sha256 marker", fingerprint)
	}

	secretOnly := cloneMCPServerConfig(base)
	secretOnly.Env["API_TOKEN"] = "secret-two"
	secretOnly.Headers["Authorization"] = "Bearer secret-two"
	secretOnly.URL = "https://another:credential@example.com:443/another/path?token=secret-two"
	if got := MCPTrustFingerprint(secretOnly); got != fingerprint {
		t.Fatalf("secret-only changes invalidated trust: got %q want %q", got, fingerprint)
	}

	cases := map[string]func(*MCPServerConfig){
		"command":  func(config *MCPServerConfig) { config.Command = "C:\\Tools\\other.exe" },
		"argument": func(config *MCPServerConfig) { config.Args[1] = "unsafe" },
		"argument order": func(config *MCPServerConfig) {
			config.Args[0], config.Args[1] = config.Args[1], config.Args[0]
		},
		"cwd":                func(config *MCPServerConfig) { config.Cwd = "C:\\Repo\\Other" },
		"transport":          func(config *MCPServerConfig) { config.Transport = "sse" },
		"config source path": func(config *MCPServerConfig) { config.ConfigPath = "C:\\Repo\\Example\\.agents\\mcp.json" },
		"URL origin":         func(config *MCPServerConfig) { config.URL = "https://api.example.net/private?token=secret-one" },
		"header name":        func(config *MCPServerConfig) { config.Headers["X-New"] = "secret" },
		"environment name":   func(config *MCPServerConfig) { config.Env["NEW_TOKEN"] = "secret" },
		"environment case": func(config *MCPServerConfig) {
			delete(config.Env, "API_TOKEN")
			config.Env["api_token"] = "secret-one"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			changed := cloneMCPServerConfig(base)
			mutate(&changed)
			if got := MCPTrustFingerprint(changed); got == fingerprint {
				t.Fatalf("%s change did not invalidate trust", name)
			}
		})
	}
}

func TestMCPTrustFingerprintCanonicalizesEquivalentCommandPaths(t *testing.T) {
	workspace := t.TempDir()
	base := MCPServerConfig{
		Identifier:   "workspace-stdio",
		Scope:        MCPConfigScopeWorkspace,
		RuntimeScope: MCPRuntimeScope(workspace),
		Transport:    "stdio",
		Command:      filepath.Join(workspace, "bin") + string(filepath.Separator) + ".." + string(filepath.Separator) + "runner.exe",
	}

	equivalent := cloneMCPServerConfig(base)
	equivalent.Command = filepath.Join(workspace, "runner.exe")
	if got, want := MCPTrustFingerprint(equivalent), MCPTrustFingerprint(base); got != want {
		t.Fatalf("equivalent command paths differed after canonicalization: got %q want %q", got, want)
	}

	changed := cloneMCPServerConfig(base)
	changed.Command = filepath.Join(workspace, "other.exe")
	if got, want := MCPTrustFingerprint(changed), MCPTrustFingerprint(base); got == want {
		t.Fatalf("real command change retained trust: got %q want different from %q", got, want)
	}
}

func TestConnectMCPWorkspaceTrustRequiredBeforeRuntimeConnect(t *testing.T) {
	registry := NewMCPRuntimeRegistry()
	config := MCPServerConfig{
		Identifier:   "workspace-http",
		Name:         "workspace-http",
		Scope:        MCPConfigScopeWorkspace,
		RuntimeScope: MCPRuntimeScope(t.TempDir()),
		Transport:    "http",
		URL:          "http://127.0.0.1:1/mcp",
	}
	registry.ReplaceScope(config.RuntimeScope, []MCPServerConfig{config})

	err := registry.Connect(t.Context(), config.RuntimeScope, config.Identifier)
	var trustErr *MCPTrustRequiredError
	if !errors.As(err, &trustErr) {
		t.Fatalf("Connect() error = %T %v, want *MCPTrustRequiredError", err, err)
	}
	if snapshot := registry.Snapshot(config.RuntimeScope); len(snapshot) != 1 || snapshot[0].Status != MCPRuntimeDisconnected {
		t.Fatalf("runtime changed before trust grant: %+v", snapshot)
	}
}

func TestConnectMCPWorkspaceTrustRequiredBeforeStdioProcessStart(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	registry := NewMCPRuntimeRegistry()
	config := MCPServerConfig{
		Identifier:   "workspace-stdio",
		Name:         "workspace-stdio",
		Scope:        MCPConfigScopeWorkspace,
		RuntimeScope: MCPRuntimeScope(t.TempDir()),
		Transport:    "stdio",
		Command:      executable,
		Args:         []string{"-test.run=TestMCPTrustHelperProcess", "--", marker},
		Env:          map[string]string{"GO_WANT_MCP_TRUST_HELPER": "1"},
	}
	registry.ReplaceScope(config.RuntimeScope, []MCPServerConfig{config})

	err = registry.Connect(t.Context(), config.RuntimeScope, config.Identifier)
	var trustErr *MCPTrustRequiredError
	if !errors.As(err, &trustErr) {
		t.Fatalf("Connect() error = %T %v, want *MCPTrustRequiredError", err, err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("workspace stdio process started before trust: stat error = %v", statErr)
	}
}

func TestMCPTrustHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_TRUST_HELPER") != "1" {
		return
	}
	marker := os.Args[len(os.Args)-1]
	if err := os.WriteFile(marker, []byte("started"), 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestMCPTrustSnapshotUsesPersistedFingerprintAndSanitizedPreview(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create MCP config directory: %v", err)
	}
	data := []byte("{\"mcpServers\":{\"workspace-tools\":{\"command\":\"node\",\"args\":[\"--token\",\"argument-secret\"],\"env\":{\"TOKEN\":\"env-secret\"},\"cwd\":" + strconv.Quote(filepath.ToSlash(workspace)) + "}}}")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write MCP config: %v", err)
	}

	InvalidateMCPScanCache()
	t.Cleanup(InvalidateMCPScanCache)
	items := SnapshotMCPServersWithSettings(workspace, SkillMCPScanSettings{Enabled: true})
	if len(items) != 1 {
		t.Fatalf("snapshot len = %d, want 1: %+v", len(items), items)
	}
	item := items[0]
	if !item.IsWorkspace || item.Trusted || !item.TrustRequired {
		t.Fatalf("ungranted trust state = %+v", item)
	}
	if item.TrustFingerprint == "" || item.SourcePath == "" || item.Cwd == "" {
		t.Fatalf("missing trust preview metadata: %+v", item)
	}
	if item.CommandPreview != "node" || item.TrustArgumentCount != 2 {
		t.Fatalf("command preview = %q with %d arguments, want basename and redacted count", item.CommandPreview, item.TrustArgumentCount)
	}
	if strings.Contains(item.CommandPreview, "argument-secret") || strings.Contains(item.CommandPreview, "env-secret") {
		t.Fatalf("command preview leaked a secret: %q", item.CommandPreview)
	}

	granted := SnapshotMCPServersWithSettings(workspace, SkillMCPScanSettings{
		Enabled: true,
		MCPTrustRecords: []MCPTrustRecord{{
			RuntimeScope: item.RuntimeScope,
			Identifier:   item.Identifier,
			Fingerprint:  item.TrustFingerprint,
		}},
	})
	if len(granted) != 1 || !granted[0].Trusted || granted[0].TrustRequired {
		t.Fatalf("granted trust state = %+v", granted)
	}
}

func TestMCPTrustFingerprintCanonicalizesAliasesAndMapOrder(t *testing.T) {
	left := MCPServerConfig{
		Identifier: "Example",
		Scope:      MCPConfigScopeWorkspace,
		Transport:  "streamable-http",
		Command:    "runner",
		ConfigPath: "C:\\Repo\\.cursor\\..\\.cursor\\mcp.json",
		URL:        "HTTPS://EXAMPLE.COM:443/a?token=one",
		Env:        map[string]string{"B": "two", "A": "one"},
		Headers:    map[string]string{"X-B": "two", "X-A": "one"},
	}
	right := cloneMCPServerConfig(left)
	right.Transport = "streamable_http"
	right.ConfigPath = "C:\\Repo\\.cursor\\mcp.json"
	right.URL = "https://example.com:443/other?token=two"
	right.Env = map[string]string{"A": "changed", "B": "changed"}
	right.Headers = map[string]string{"X-A": "changed", "X-B": "changed"}
	if got, want := MCPTrustFingerprint(left), MCPTrustFingerprint(right); got != want {
		t.Fatalf("canonical fingerprints differ: got %q want %q", got, want)
	}
	right.URL = "https://example.com/other?token=three"
	if got, want := MCPTrustFingerprint(left), MCPTrustFingerprint(right); got != want {
		t.Fatalf("default-port origins differ: got %q want %q", got, want)
	}
}

func TestMCPTrustFingerprintCanonicalizesRuntimeWorkspaceScope(t *testing.T) {
	workspace := t.TempDir()
	left := MCPServerConfig{
		Identifier:   "Example",
		Scope:        MCPConfigScopeWorkspace,
		RuntimeScope: " workspace:" + filepath.Join(workspace, "nested", "..") + " ",
		Transport:    "stdio",
		Command:      "node",
	}
	right := cloneMCPServerConfig(left)
	right.RuntimeScope = MCPRuntimeScope(workspace)
	if got, want := MCPTrustFingerprint(left), MCPTrustFingerprint(right); got != want {
		t.Fatalf("normalized runtime scope fingerprints differ: got %q want %q", got, want)
	}
}

func TestRequireMCPTrustMatchesExactWorkspaceRecordAndSkipsUserScope(t *testing.T) {
	config := MCPServerConfig{
		Identifier:   "workspace-http",
		Name:         "workspace-http",
		Scope:        MCPConfigScopeWorkspace,
		RuntimeScope: MCPRuntimeScope(t.TempDir()),
		Transport:    "http",
		URL:          "http://127.0.0.1:1/mcp",
	}

	err := RequireMCPTrust(config, nil)
	var trustErr *MCPTrustRequiredError
	if !errors.As(err, &trustErr) {
		t.Fatalf("RequireMCPTrust() error = %T %v, want *MCPTrustRequiredError", err, err)
	}
	if trustErr.Code() != MCPTrustRequiredCode || trustErr.UserAction() != "grant_mcp_workspace_trust" {
		t.Fatalf("trust error contract = code %q action %q", trustErr.Code(), trustErr.UserAction())
	}

	record, err := NewMCPTrustRecord(config)
	if err != nil {
		t.Fatalf("NewMCPTrustRecord() error = %v", err)
	}
	if err := RequireMCPTrust(config, []MCPTrustRecord{record}); err != nil {
		t.Fatalf("RequireMCPTrust() with exact record error = %v", err)
	}

	changed := cloneMCPServerConfig(config)
	changed.URL = "http://127.0.0.1:2/mcp"
	if err := RequireMCPTrust(changed, []MCPTrustRecord{record}); !errors.As(err, &trustErr) {
		t.Fatalf("changed config error = %T %v, want trust required", err, err)
	}

	userConfig := cloneMCPServerConfig(config)
	userConfig.Scope = MCPConfigScopeUser
	userConfig.RuntimeScope = MCPRuntimeScope("")
	if err := RequireMCPTrust(userConfig, nil); err != nil {
		t.Fatalf("user-scope RequireMCPTrust() error = %v", err)
	}
	userConfig.RuntimeScope = MCPRuntimeScope(t.TempDir())
	if err := RequireMCPTrust(userConfig, nil); err != nil {
		t.Fatalf("user-scope config with workspace runtime metadata was trust-gated: %v", err)
	}
}

type typedMCPTrustSettingsProvider struct {
	records []MCPTrustRecord
}

func (provider typedMCPTrustSettingsProvider) SkillMCPScanEnabled() bool {
	return true
}

func (provider typedMCPTrustSettingsProvider) SkillMCPScanSkillSources() map[string]bool {
	return nil
}

func (provider typedMCPTrustSettingsProvider) SkillMCPScanMCPSources() map[string]bool {
	return nil
}

func (provider typedMCPTrustSettingsProvider) SkillMCPScanEnabledSkills() map[string]bool {
	return nil
}

func (provider typedMCPTrustSettingsProvider) SkillMCPScanDisabledMCPServers() map[string]bool {
	return nil
}

func (provider typedMCPTrustSettingsProvider) SkillMCPScanTrustRecords() []MCPTrustRecord {
	return provider.records
}

func TestReadSkillMCPScanSettingsUsesTypedTrustRecords(t *testing.T) {
	record := MCPTrustRecord{
		RuntimeScope: "workspace:/repo",
		Identifier:   "workspace-server",
		Fingerprint:  "mcp-trust-v1:sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	settings := readSkillMCPScanSettings(typedMCPTrustSettingsProvider{records: []MCPTrustRecord{record}})
	if len(settings.MCPTrustRecords) != 1 || settings.MCPTrustRecords[0] != record {
		t.Fatalf("typed trust records = %+v, want %+v", settings.MCPTrustRecords, record)
	}
}
