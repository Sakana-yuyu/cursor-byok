package client

import (
	"context"

	"cursor/internal/backend/forwarder"
	serverconfig "cursor/internal/backend/server/config"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// GetDelegationTaskSnapshots returns retained Multitask worker state.
func (s *ProxyService) GetDelegationTaskSnapshots() []forwarder.DelegationTaskSnapshot {
	if s == nil || s.backendHost == nil {
		return nil
	}
	return s.backendHost.DelegationTaskSnapshots()
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
