// Package runtimeconfig holds shared runtime DTOs consumed by server/config
// and forwarder without creating a config→forwarder import cycle.
package runtimeconfig

import "time"

// MCPTrustRecord is the non-secret persisted approval for one workspace MCP
// definition. It deliberately contains no command arguments, credentials, or
// configuration values.
type MCPTrustRecord struct {
	RuntimeScope string `json:"runtimeScope" yaml:"runtimeScope"`
	Identifier   string `json:"identifier" yaml:"identifier"`
	Fingerprint  string `json:"fingerprint" yaml:"fingerprint"`
}

// GoalRuntimeConfig is the goal-loop runtime budget consumed by forwarder;
// populated from server/config persistence (see delegation.RuntimeConfig).
type GoalRuntimeConfig struct {
	Enabled           bool
	MaxProviderPasses int
	MaxDuration       time.Duration
	MaxCostUSD        float64
	SelfCheckPasses   int
	VerifyMaxRetries  int
	ErrorMaxRetries   int
	ProgressInterval  int
}
