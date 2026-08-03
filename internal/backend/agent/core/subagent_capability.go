package runtimecore

import (
	"fmt"
	"strings"
)

const (
	SubagentTypeLongContextRead = "longContextRead"
	TaskAccessModeInspect       = "inspect"
	TaskAccessModeAct           = "act"
)

// SubagentCapability is the normalized authorization for a Task child.
type SubagentCapability struct {
	Type     string
	Readonly bool
}

// ResolveTaskSubagentCapabilityFromArgs parses the required access_mode authorization.
func ResolveTaskSubagentCapabilityFromArgs(args map[string]any) (SubagentCapability, error) {
	var legacyReadonly *bool
	for _, key := range []string{"readonly", "readOnly"} {
		value, found := args[key]
		if !found {
			continue
		}
		parsed, ok := value.(bool)
		if !ok {
			return SubagentCapability{}, fmt.Errorf("Task readonly must be boolean")
		}
		legacyReadonly = &parsed
		break
	}
	return ResolveTaskSubagentCapability(
		ReadStringArg(args, "subagent_type", "subagentType"),
		ReadStringArg(args, "access_mode", "accessMode"),
		legacyReadonly,
	)
}

// ResolveTaskSubagentCapability applies the explicit access intent.
func ResolveTaskSubagentCapability(subagentType string, accessMode string, readonly *bool) (SubagentCapability, error) {
	typeName := strings.TrimSpace(subagentType)
	mode := strings.TrimSpace(accessMode)
	if mode == "" {
		requestedReadonly := false
		if readonly != nil {
			requestedReadonly = *readonly
		}
		if typeName == "explore" || typeName == SubagentTypeLongContextRead {
			requestedReadonly = true
		}
		return ResolveSubagentCapability(typeName, requestedReadonly)
	}

	var requestedReadonly bool
	switch mode {
	case TaskAccessModeInspect:
		requestedReadonly = true
	case TaskAccessModeAct:
		if typeName != "generalPurpose" {
			return SubagentCapability{}, fmt.Errorf("Task access_mode %q requires subagent_type %q", TaskAccessModeAct, "generalPurpose")
		}
		requestedReadonly = false
	default:
		return SubagentCapability{}, fmt.Errorf("Task access_mode must be %q or %q", TaskAccessModeInspect, TaskAccessModeAct)
	}
	if readonly != nil && *readonly != requestedReadonly {
		return SubagentCapability{}, fmt.Errorf("Task access_mode %q conflicts with legacy readonly=%t", mode, *readonly)
	}
	return ResolveSubagentCapability(typeName, requestedReadonly)
}

// ResolveSubagentCapability validates the supported Task type and access pair.
func ResolveSubagentCapability(subagentType string, readonly bool) (SubagentCapability, error) {
	capability := SubagentCapability{
		Type:     strings.TrimSpace(subagentType),
		Readonly: readonly,
	}
	switch capability.Type {
	case "explore", SubagentTypeLongContextRead:
		if !capability.Readonly {
			return SubagentCapability{}, fmt.Errorf("subagent type %q must be readonly", capability.Type)
		}
	case "generalPurpose":
	default:
		return SubagentCapability{}, fmt.Errorf("unsupported subagent type %q", capability.Type)
	}
	return capability, nil
}
