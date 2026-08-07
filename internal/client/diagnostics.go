package client

import (
	"strings"

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
		// 只修正可自动修正的类别（provider_mismatch）；catalog_uncovered 无法自动修正，
		// 交给用户手动补填能力或目录补录，不能把空建议值当修正写入配置。
		issue, ok := findFixableIssue(issueResult.Issues)
		if !ok {
			continue
		}
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

// findFixableIssue 返回第一个可自动修正的 issue（provider_mismatch）。
// catalog_uncovered 等类别没有 SuggestedValue，不能自动修正。
func findFixableIssue(issues []serverconfig.DiagnosticIssue) (serverconfig.DiagnosticIssue, bool) {
	for _, issue := range issues {
		if issue.Category == serverconfig.DiagnosticCategoryProviderMismatch && strings.TrimSpace(issue.SuggestedValue) != "" {
			return issue, true
		}
	}
	return serverconfig.DiagnosticIssue{}, false
}
