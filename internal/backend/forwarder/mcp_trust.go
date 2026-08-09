package forwarder

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	MCPTrustFingerprintVersion = "mcp-trust-v1"
	MCPTrustRequiredCode       = "mcp_workspace_trust_required"
	MCPTrustRequiredAction     = "grant_mcp_workspace_trust"
)

// MCPTrustRecord is the non-secret persisted approval for one workspace MCP
// definition. It deliberately contains no command arguments, credentials, or
// configuration values.
type MCPTrustRecord struct {
	RuntimeScope string `json:"runtimeScope" yaml:"runtimeScope"`
	Identifier   string `json:"identifier" yaml:"identifier"`
	Fingerprint  string `json:"fingerprint" yaml:"fingerprint"`
}

// MCPTrustRequiredError signals that an action can proceed only after the user
// has explicitly approved the exact workspace MCP definition.
type MCPTrustRequiredError struct {
	DiagnosticCode     string
	Action             string
	UserActionRequired bool
	RuntimeScope       string
	WorkspaceScope     string
	Identifier         string
	Fingerprint        string
	SourcePath         string
	CommandPreview     string
	TrustArgumentCount int
	URLOrigin          string
	Cwd                string
}

func (err *MCPTrustRequiredError) Error() string {
	if err == nil {
		return MCPTrustRequiredCode + ": workspace MCP trust is required"
	}
	return fmt.Sprintf("%s: workspace MCP server %q requires explicit trust before it can connect", err.Code(), err.Identifier)
}

func (err *MCPTrustRequiredError) Code() string {
	if err != nil && strings.TrimSpace(err.DiagnosticCode) != "" {
		return err.DiagnosticCode
	}
	return MCPTrustRequiredCode
}

func (err *MCPTrustRequiredError) UserAction() string {
	if err != nil && strings.TrimSpace(err.Action) != "" {
		return err.Action
	}
	return MCPTrustRequiredAction
}

// MCPTrustPreview is the sanitized review surface for one MCP definition.
type MCPTrustPreview struct {
	SourcePath         string
	CommandPreview     string
	TrustArgumentCount int
	URLOrigin          string
	Cwd                string
}

func BuildMCPTrustPreview(config MCPServerConfig) MCPTrustPreview {
	return MCPTrustPreview{
		SourcePath:         normalizeMCPTrustPath(config.ConfigPath),
		CommandPreview:     mcpCommandBasename(config.Command),
		TrustArgumentCount: len(config.Args),
		URLOrigin:          mcpURLOrigin(config.URL),
		Cwd:                normalizeMCPTrustPath(config.Cwd),
	}
}

// MCPTrustFingerprint returns a deterministic identity for the executable or
// remote endpoint shape of one MCP configuration. Credential values are never
// included: rotations of a token do not silently invalidate a user approval.
func MCPTrustFingerprint(config MCPServerConfig) string {
	payload, _ := json.Marshal(mcpTrustFingerprintPayload{
		SchemaVersion: 1,
		Identifier:    normalizeMCPTrustIdentifier(config.Identifier),
		Source:        mcpSourceKind(config.Source),
		ConfigPath:    normalizeMCPTrustPath(config.ConfigPath),
		Scope:         mcpConfigScopeKind(config.Scope),
		RuntimeScope:  normalizeMCPRuntimeScope(config.RuntimeScope),
		Transport:     mcpTransportKind(config.Transport),
		Command:       normalizeMCPTrustCommand(config.Command),
		Args:          append([]string(nil), config.Args...),
		Cwd:           normalizeMCPTrustPath(config.Cwd),
		URLOrigin:     mcpURLOrigin(config.URL),
		HeaderNames:   normalizedMCPTrustNames(config.Headers, true),
		EnvNames:      normalizedMCPTrustNames(config.Env, false),
	})
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%s:sha256:%x", MCPTrustFingerprintVersion, sum)
}

type mcpTrustFingerprintPayload struct {
	SchemaVersion int      `json:"schemaVersion"`
	Identifier    string   `json:"identifier"`
	Source        string   `json:"source"`
	ConfigPath    string   `json:"configPath"`
	Scope         string   `json:"scope"`
	RuntimeScope  string   `json:"runtimeScope"`
	Transport     string   `json:"transport"`
	Command       string   `json:"command"`
	Args          []string `json:"args"`
	Cwd           string   `json:"cwd"`
	URLOrigin     string   `json:"urlOrigin"`
	HeaderNames   []string `json:"headerNames"`
	EnvNames      []string `json:"envNames"`
}

// NewMCPTrustRecord returns the non-secret approval key for a workspace MCP
// server. User-scoped MCP servers retain their existing connection behavior and
// must not create a trust record.
func NewMCPTrustRecord(config MCPServerConfig) (MCPTrustRecord, error) {
	if !mcpWorkspaceTrustRequired(config) {
		return MCPTrustRecord{}, fmt.Errorf("MCP trust records only apply to workspace-scoped servers")
	}
	identifier := normalizeMCPTrustIdentifier(config.Identifier)
	if identifier == "" {
		return MCPTrustRecord{}, fmt.Errorf("mcp server identifier is required for a trust record")
	}
	return MCPTrustRecord{
		RuntimeScope: normalizeMCPRuntimeScope(config.RuntimeScope),
		Identifier:   identifier,
		Fingerprint:  MCPTrustFingerprint(config),
	}, nil
}

// RequireMCPTrust accepts all user-scoped configs unchanged. Workspace configs
// need an exact matching record, so an edit to the launch or endpoint identity
// forces a new explicit approval before process or network startup.
func RequireMCPTrust(config MCPServerConfig, records []MCPTrustRecord) error {
	if !mcpWorkspaceTrustRequired(config) {
		return nil
	}
	required, err := NewMCPTrustRecord(config)
	if err != nil {
		return err
	}
	for _, record := range records {
		if normalizeMCPRuntimeScope(record.RuntimeScope) == required.RuntimeScope &&
			normalizeMCPTrustIdentifier(record.Identifier) == required.Identifier &&
			strings.TrimSpace(record.Fingerprint) == required.Fingerprint {
			return nil
		}
	}
	preview := BuildMCPTrustPreview(config)
	return &MCPTrustRequiredError{
		DiagnosticCode:     MCPTrustRequiredCode,
		Action:             MCPTrustRequiredAction,
		UserActionRequired: true,
		RuntimeScope:       required.RuntimeScope,
		WorkspaceScope:     required.RuntimeScope,
		Identifier:         required.Identifier,
		Fingerprint:        required.Fingerprint,
		SourcePath:         preview.SourcePath,
		CommandPreview:     preview.CommandPreview,
		TrustArgumentCount: preview.TrustArgumentCount,
		URLOrigin:          preview.URLOrigin,
		Cwd:                preview.Cwd,
	}
}

func mcpWorkspaceTrustRequired(config MCPServerConfig) bool {
	return config.Scope == MCPConfigScopeWorkspace
}

func normalizeMCPTrustIdentifier(identifier string) string {
	return strings.ToLower(strings.TrimSpace(identifier))
}

func normalizeMCPTrustCommand(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if absolute, err := filepath.Abs(value); err == nil {
		value = absolute
	}
	value = filepath.ToSlash(filepath.Clean(value))
	if runtime.GOOS == "windows" {
		value = strings.ToLower(value)
	}
	return value
}

func normalizeMCPTrustPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if absolute, err := filepath.Abs(value); err == nil {
		value = absolute
	}
	value = filepath.ToSlash(filepath.Clean(value))
	if runtime.GOOS == "windows" {
		value = strings.ToLower(value)
	}
	return value
}

func normalizedMCPTrustNames(values map[string]string, foldCase bool) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	for name := range values {
		name = strings.TrimSpace(name)
		if foldCase {
			name = strings.ToLower(name)
		}
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
