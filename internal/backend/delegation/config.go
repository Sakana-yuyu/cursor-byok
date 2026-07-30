package delegation

import "strings"

const (
	ExecutionModeCursor = "cursor"
	ExecutionModeLocal  = "local"
	ExecutionModeAuto   = "auto"
)

type RuntimeModelGroup struct {
	ID              string
	Name            string
	Enabled         bool
	ModelIDs        []string
	DefaultModelID  string
	ExecutionMode   string
	ToolPermissions map[string]bool
}

type RuntimeConfig struct {
	Enabled        bool
	MaxConcurrency int
	Groups         []RuntimeModelGroup
	ModelNames     map[string]string
}

type RuntimeConfigProvider interface {
	DelegationRuntimeConfig() RuntimeConfig
}

func NormalizeExecutionMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ExecutionModeCursor:
		return ExecutionModeCursor
	case ExecutionModeLocal:
		return ExecutionModeLocal
	default:
		return ExecutionModeAuto
	}
}
