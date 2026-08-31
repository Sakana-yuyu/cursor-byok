// Package runtimeconfig holds shared runtime DTOs consumed by server/config
// and forwarder without creating a config->forwarder import cycle.
package runtimeconfig

// MCPTrustRecord is the non-secret persisted approval for one workspace MCP
// definition. It deliberately contains no command arguments, credentials, or
// configuration values.
type MCPTrustRecord struct {
	RuntimeScope string `json:"runtimeScope" yaml:"runtimeScope"`
	Identifier   string `json:"identifier" yaml:"identifier"`
	Fingerprint  string `json:"fingerprint" yaml:"fingerprint"`
}
