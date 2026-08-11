package client

import (
	"context"
	"time"

	"cursor/internal/backend/delegation"
	"cursor/internal/backend/forwarder"
	serverconfig "cursor/internal/backend/server/config"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type DelegationExecutorSnapshot struct {
	ID                      string     `json:"id"`
	DisplayName             string     `json:"displayName"`
	InstallURL              string     `json:"installURL,omitempty"`
	Enabled                 bool       `json:"enabled"`
	Priority                int        `json:"priority"`
	Capabilities            []string   `json:"capabilities,omitempty"`
	State                   string     `json:"state"`
	ExecutablePath          string     `json:"executablePath,omitempty"`
	Version                 string     `json:"version,omitempty"`
	Installed               bool       `json:"installed"`
	EditorAvailable         bool       `json:"editorAvailable"`
	AgentExecutionAvailable bool       `json:"agentExecutionAvailable"`
	AuthState               string     `json:"authState"`
	DiagnosticCode          string     `json:"diagnosticCode,omitempty"`
	DiagnosticText          string     `json:"diagnosticText,omitempty"`
	ProbedAt                *time.Time `json:"probedAt,omitempty"`
	CooldownUntil           *time.Time `json:"cooldownUntil,omitempty"`
	FailureClass            string     `json:"failureClass,omitempty"`
	FailureCode             string     `json:"failureCode,omitempty"`
}

func publicDelegationExecutorSnapshots(source []delegation.ExecutorSnapshot) []DelegationExecutorSnapshot {
	if len(source) == 0 {
		return nil
	}
	result := make([]DelegationExecutorSnapshot, 0, len(source))
	for _, item := range source {
		capabilities := make([]string, 0, len(item.Capabilities))
		for _, capability := range item.Capabilities {
			capabilities = append(capabilities, string(capability))
		}
		result = append(result, DelegationExecutorSnapshot{
			ID:                      string(item.ID),
			DisplayName:             item.DisplayName,
			InstallURL:              item.InstallURL,
			Enabled:                 item.Enabled,
			Priority:                item.Priority,
			Capabilities:            capabilities,
			State:                   string(item.Probe.State),
			ExecutablePath:          item.Probe.ExecutablePath,
			Version:                 item.Probe.Version,
			Installed:               item.Probe.Installed,
			EditorAvailable:         item.Probe.EditorAvailable,
			AgentExecutionAvailable: item.Probe.AgentExecutionAvailable,
			AuthState:               string(item.Probe.AuthState),
			DiagnosticCode:          item.Probe.DiagnosticCode,
			DiagnosticText:          delegation.SanitizeSupervisorText(item.Probe.DiagnosticText, ""),
			ProbedAt:                optionalDelegationExecutorTime(item.Probe.ProbedAt),
			CooldownUntil:           optionalDelegationExecutorTime(item.CooldownUntil),
			FailureClass:            string(item.FailureClass),
			FailureCode:             item.FailureCode,
		})
	}
	return result
}

func optionalDelegationExecutorTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

// GetDelegationTaskSnapshots returns retained Multitask worker state.
func (s *ProxyService) GetDelegationTaskSnapshots() []forwarder.DelegationTaskSnapshot {
	if s == nil || s.backendHost == nil {
		return nil
	}
	return s.backendHost.DelegationTaskSnapshots()
}

func (s *ProxyService) GetDelegationExecutorSnapshots() []DelegationExecutorSnapshot {
	if s == nil || s.backendHost == nil {
		return nil
	}
	return publicDelegationExecutorSnapshots(s.backendHost.DelegationExecutorSnapshots())
}

func (s *ProxyService) RefreshDelegationExecutorProbes() ([]DelegationExecutorSnapshot, error) {
	if s == nil || s.backendHost == nil {
		return nil, nil
	}
	ctx := context.Background()
	if app := application.Get(); app != nil {
		ctx = app.Context()
	}
	items, err := s.backendHost.RefreshDelegationExecutorProbes(ctx)
	return publicDelegationExecutorSnapshots(items), err
}

// InstallDelegationExecutor 仅安装后端白名单中的 CLI，并在安装完成后返回强制复检结果。
func (s *ProxyService) InstallDelegationExecutor(id string) (DelegationExecutorSnapshot, error) {
	if s == nil || s.backendHost == nil {
		return DelegationExecutorSnapshot{}, nil
	}
	ctx := context.Background()
	if app := application.Get(); app != nil {
		ctx = app.Context()
	}
	snapshot, err := s.backendHost.InstallDelegationExecutor(ctx, id)
	if err != nil {
		return DelegationExecutorSnapshot{}, err
	}
	items := publicDelegationExecutorSnapshots([]delegation.ExecutorSnapshot{snapshot})
	if len(items) == 0 {
		return DelegationExecutorSnapshot{}, nil
	}
	return items[0], nil
}

// GetDelegationConfig returns the normalized delegation and supervision config.
func (s *ProxyService) GetDelegationConfig() (serverconfig.DelegationConfig, error) {
	if s == nil {
		return serverconfig.DefaultConfig().Delegation, nil
	}
	app := application.Get()
	ctx := context.Background()
	if app != nil {
		ctx = app.Context()
	}
	if s.backendHost != nil {
		return s.backendHost.GetDelegationConfig(ctx)
	}
	cfg, err := s.LoadUserConfig()
	if err != nil {
		return serverconfig.DefaultConfig().Delegation, err
	}
	return cfg.Delegation, nil
}

// SaveDelegationConfig persists only the delegation subtree and returns the normalized result.
func (s *ProxyService) SaveDelegationConfig(cfg serverconfig.DelegationConfig) (serverconfig.DelegationConfig, error) {
	if s == nil {
		return serverconfig.DefaultConfig().Delegation, nil
	}
	app := application.Get()
	ctx := context.Background()
	if app != nil {
		ctx = app.Context()
	}
	var (
		normalized serverconfig.DelegationConfig
		fullConfig UserConfig
		err        error
	)
	if s.backendHost != nil {
		normalized, err = s.backendHost.SaveDelegationConfig(ctx, cfg)
		if err != nil {
			return serverconfig.DefaultConfig().Delegation, err
		}
		fullConfig, err = s.backendHost.LoadConfig(ctx)
		if err != nil {
			return serverconfig.DefaultConfig().Delegation, err
		}
	} else {
		fullConfig, err = s.LoadUserConfig()
		if err != nil {
			return serverconfig.DefaultConfig().Delegation, err
		}
		fullConfig.Delegation = cfg
		if s.store == nil {
			s.emitUserConfigChanged(fullConfig)
			return fullConfig.Delegation, nil
		}
		fullConfig, err = s.store.Save(ctx, fullConfig)
		if err != nil {
			return serverconfig.DefaultConfig().Delegation, err
		}
		normalized = fullConfig.Delegation
	}
	s.emitUserConfigChanged(fullConfig)
	return normalized, nil
}

// CancelDelegationTask cancels one Multitask worker without stopping siblings.
func (s *ProxyService) CancelDelegationTask(taskID string) bool {
	if s == nil || s.backendHost == nil {
		return false
	}
	return s.backendHost.CancelDelegationTask(taskID)
}
