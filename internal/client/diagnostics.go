package client

import (
	serverconfig "cursor/internal/backend/server/config"
)

// DiagnoseModelAdapters scans the persisted model adapters without changing them.
func (s *ProxyService) DiagnoseModelAdapters() (serverconfig.DiagnosticResult, error) {
	if s == nil {
		return serverconfig.DiagnosticResult{}, nil
	}
	cfg, err := s.LoadUserConfig()
	if err != nil {
		return serverconfig.DiagnosticResult{}, err
	}
	return serverconfig.DiagnoseModelAdapters(cfg.ModelAdapters), nil
}

// ApplyDiagnosticFixes applies the inferred provider type to the selected channels.
func (s *ProxyService) ApplyDiagnosticFixes(channelIDs []string) (serverconfig.DiagnosticResult, error) {
	if s == nil {
		return serverconfig.DiagnosticResult{}, nil
	}
	cfg, err := s.LoadUserConfig()
	if err != nil {
		return serverconfig.DiagnosticResult{}, err
	}
	selected := make(map[string]struct{}, len(channelIDs))
	for _, id := range channelIDs {
		selected[id] = struct{}{}
	}
	for i := range cfg.ModelAdapters {
		adapter := cfg.ModelAdapters[i]
		issueResult := serverconfig.DiagnoseModelAdapters([]serverconfig.ModelAdapterConfig{adapter})
		if len(issueResult.Issues) == 0 {
			continue
		}
		issue := issueResult.Issues[0]
		if _, ok := selected[adapter.ID]; !ok {
			continue
		}
		cfg.ModelAdapters[i] = serverconfig.ApplyDiagnosticFix(adapter, issue.SuggestedValue)
	}
	if err := s.SaveUserConfig(cfg); err != nil {
		return serverconfig.DiagnosticResult{}, err
	}
	return serverconfig.DiagnoseModelAdapters(cfg.ModelAdapters), nil
}
