package config

import (
	"fmt"
	"strings"
)

const (
	DefaultDelegationMaxConcurrency = 4
	DelegationModeCursor            = "cursor"
	DelegationModeLocal             = "local"
	DelegationModeAuto              = "auto"
)

// DelegationModelGroup 描述一组可被 Multitask 委派的已配置模型。
// ModelIDs 引用 Config.ModelAdapters 中的 ID，不重复保存连接信息或密钥。
type DelegationModelGroup struct {
	ID              string          `json:"id" yaml:"id"`
	Name            string          `json:"name" yaml:"name"`
	Enabled         bool            `json:"enabled" yaml:"enabled"`
	ModelIDs        []string        `json:"modelIDs" yaml:"modelIDs"`
	DefaultModelID  string          `json:"defaultModelID,omitempty" yaml:"defaultModelID,omitempty"`
	ExecutionMode   string          `json:"executionMode,omitempty" yaml:"executionMode,omitempty"`
	ToolPermissions map[string]bool `json:"toolPermissions,omitempty" yaml:"toolPermissions,omitempty"`
}

// DelegationConfig 控制 Multitask 委派总开关、并发度和模型组。
type DelegationConfig struct {
	Enabled        bool                   `json:"enabled" yaml:"enabled"`
	MaxConcurrency int                    `json:"maxConcurrency" yaml:"maxConcurrency"`
	Groups         []DelegationModelGroup `json:"groups,omitempty" yaml:"groups,omitempty"`
}

func normalizeDelegationConfig(input DelegationConfig, adapters []ModelAdapterConfig) DelegationConfig {
	availableModels := make(map[string]struct{}, len(adapters))
	for _, adapter := range adapters {
		if adapterID := strings.TrimSpace(adapter.ID); adapterID != "" {
			availableModels[adapterID] = struct{}{}
		}
	}
	output := DelegationConfig{
		Enabled:        input.Enabled,
		MaxConcurrency: input.MaxConcurrency,
		Groups:         make([]DelegationModelGroup, 0, len(input.Groups)),
	}
	if output.MaxConcurrency <= 0 {
		output.MaxConcurrency = DefaultDelegationMaxConcurrency
	}
	seenGroups := make(map[string]struct{}, len(input.Groups))
	for index, group := range input.Groups {
		group.ID = strings.TrimSpace(group.ID)
		if group.ID == "" {
			group.ID = fmt.Sprintf("delegation-group-%d", index+1)
		}
		if _, exists := seenGroups[group.ID]; exists {
			continue
		}
		seenGroups[group.ID] = struct{}{}
		group.Name = strings.TrimSpace(group.Name)
		if group.Name == "" {
			group.Name = group.ID
		}
		group.ExecutionMode = normalizeDelegationMode(group.ExecutionMode)
		group.ModelIDs = filterAvailableModelIDs(group.ModelIDs, availableModels)
		group.DefaultModelID = strings.TrimSpace(group.DefaultModelID)
		if group.DefaultModelID != "" && !containsString(group.ModelIDs, group.DefaultModelID) {
			group.DefaultModelID = ""
		}
		if group.DefaultModelID == "" && len(group.ModelIDs) > 0 {
			group.DefaultModelID = group.ModelIDs[0]
		}
		if len(group.ModelIDs) == 0 {
			group.Enabled = false
		}
		group.ToolPermissions = cloneBoolMap(group.ToolPermissions)
		output.Groups = append(output.Groups, group)
	}
	return output
}

func filterAvailableModelIDs(values []string, available map[string]struct{}) []string {
	normalized := normalizeStringList(values)
	result := make([]string, 0, len(normalized))
	for _, value := range normalized {
		if _, exists := available[value]; exists {
			result = append(result, value)
		}
	}
	return result
}

func normalizeDelegationMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case DelegationModeCursor, DelegationModeLocal:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return DelegationModeAuto
	}
}

func normalizeStringList(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cloneBoolMap(input map[string]bool) map[string]bool {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]bool, len(input))
	for key, value := range input {
		key = strings.TrimSpace(key)
		if key != "" {
			output[key] = value
		}
	}
	if len(output) == 0 {
		return nil
	}
	return output
}
