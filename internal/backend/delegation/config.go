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
	Enabled                 bool
	MaxConcurrency          int
	Groups                  []RuntimeModelGroup
	ModelNames              map[string]string
	SupervisionEnabled      bool
	SupervisorModelID       string
	ReviewerModelID         string
	WorkerGroupID           string
	MaxCorrections          int
	MaxRetries              int
	MaxRounds               int
	AllowReassign           bool
	AllowEscalate           bool
	StrictUnavailable       bool
	VisionDelegationEnabled bool
	VisionModelID           string
	VisionMode              string
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

func NormalizeRuntimeConfig(config RuntimeConfig) RuntimeConfig {
	config.MaxConcurrency = normalizePositive(config.MaxConcurrency, DefaultMaxConcurrency)
	config.SupervisorModelID = strings.TrimSpace(config.SupervisorModelID)
	config.ReviewerModelID = strings.TrimSpace(config.ReviewerModelID)
	config.WorkerGroupID = strings.TrimSpace(config.WorkerGroupID)
	config.MaxCorrections = normalizePositive(config.MaxCorrections, DefaultSupervisionCorrections)
	config.MaxRetries = normalizePositive(config.MaxRetries, DefaultSupervisionRetries)
	config.MaxRounds = normalizePositive(config.MaxRounds, DefaultSupervisionRounds)
	config.VisionModelID = strings.TrimSpace(config.VisionModelID)
	config.VisionMode = strings.TrimSpace(config.VisionMode)
	if !config.Enabled {
		config.SupervisionEnabled = false
		// 视觉委派是独立能力：是否生效只取决于是否配置了有效的识图模型
		// （见下方 VisionModelID 判断），不应被多任务委派总开关关闭。
		// 否则用户仅开启「视觉委派」而未开启多任务委派时，纯文本主模型无法识图。
	}
	if config.VisionModelID == "" {
		config.VisionDelegationEnabled = false
	}
	if len(config.Groups) > 0 {
		cloned := make([]RuntimeModelGroup, 0, len(config.Groups))
		for _, group := range config.Groups {
			group.ID = strings.TrimSpace(group.ID)
			group.Name = strings.TrimSpace(group.Name)
			group.ModelIDs = append([]string(nil), group.ModelIDs...)
			group.DefaultModelID = strings.TrimSpace(group.DefaultModelID)
			group.ExecutionMode = NormalizeExecutionMode(group.ExecutionMode)
			group.ToolPermissions = cloneRuntimeBoolMap(group.ToolPermissions)
			cloned = append(cloned, group)
		}
		config.Groups = cloned
	}
	config.ModelNames = cloneRuntimeStringMap(config.ModelNames)
	return config
}

func normalizePositive(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func cloneRuntimeBoolMap(input map[string]bool) map[string]bool {
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

func cloneRuntimeStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			output[key] = value
		}
	}
	if len(output) == 0 {
		return nil
	}
	return output
}
