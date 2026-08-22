package client

import (
	"fmt"
	"strings"

	"cursor/internal/agentops"
	"cursor/internal/configprofile"
	"cursor/internal/controlcenter"
	"cursor/internal/cursoraccount"
	"cursor/internal/requestlab"
	"cursor/internal/routing"
	"cursor/internal/safego"
)

func (s *ProxyService) GetControlCenterOverview() (controlcenter.ControlCenterOverview, error) {
	overview := controlcenter.ControlCenterOverview{}
	overview.Accounts = s.overviewAccounts()
	overview.RequestLab = s.overviewRequestLab()
	overview.Routing = s.overviewRouting()
	overview.Agents = s.overviewAgents()
	overview.Profiles = s.overviewProfiles()
	return overview, nil
}

func (s *ProxyService) overviewAccounts() controlcenter.ControlCenterDomainStatus {
	if s == nil || s.cursorAccount == nil {
		return controlcenter.ControlCenterDomainStatus{State: "error", WarningCode: "account_store_unreadable"}
	}
	accounts, err := s.cursorAccount.ListAccounts()
	if err != nil {
		return controlcenter.ControlCenterDomainStatus{State: "error", WarningCode: "account_store_unreadable"}
	}
	state := "empty"
	if len(accounts) > 0 {
		state = "ready"
	}
	return controlcenter.ControlCenterDomainStatus{State: state, Count: len(accounts)}
}

func (s *ProxyService) overviewRequestLab() controlcenter.ControlCenterDomainStatus {
	if s == nil || s.requestLab == nil {
		return controlcenter.ControlCenterDomainStatus{State: "error", WarningCode: "request_source_unavailable"}
	}
	return controlcenter.ControlCenterDomainStatus{State: "ready", Count: s.requestLab.Count()}
}

func (s *ProxyService) overviewRouting() controlcenter.ControlCenterDomainStatus {
	if s == nil {
		return controlcenter.ControlCenterDomainStatus{State: "error", WarningCode: "routing_policy_save_failed"}
	}
	cfg, err := s.LoadUserConfig()
	if err != nil {
		return controlcenter.ControlCenterDomainStatus{State: "error", WarningCode: "routing_history_read_failed"}
	}
	state := "ready"
	if !cfg.Routing.Policy.Enabled {
		state = "empty"
	}
	return controlcenter.ControlCenterDomainStatus{State: state}
}

func (s *ProxyService) overviewAgents() controlcenter.ControlCenterDomainStatus {
	if s == nil || s.agentOps == nil {
		return controlcenter.ControlCenterDomainStatus{State: "error", WarningCode: "agent_runs_unavailable"}
	}
	count := s.agentOps.Count()
	state := "empty"
	if count > 0 {
		state = "ready"
	}
	return controlcenter.ControlCenterDomainStatus{State: state, Count: count}
}

func (s *ProxyService) overviewProfiles() controlcenter.ControlCenterDomainStatus {
	if s == nil || s.profiles == nil {
		return controlcenter.ControlCenterDomainStatus{State: "error", WarningCode: "profile_store_unreadable"}
	}
	count := s.profiles.Count()
	state := "empty"
	if count > 0 {
		state = "ready"
	}
	return controlcenter.ControlCenterDomainStatus{State: state, Count: count}
}

func (s *ProxyService) BeginCursorAccountLogin() (cursoraccount.CursorAccountLoginSession, error) {
	if s == nil || s.cursorAccount == nil {
		return cursoraccount.CursorAccountLoginSession{}, fmt.Errorf("Cursor 账号服务未初始化")
	}
	return s.cursorAccount.BeginLogin()
}

func (s *ProxyService) GetCursorAccountLoginStatus(sessionID string) (cursoraccount.CursorAccountLoginStatus, error) {
	if s == nil || s.cursorAccount == nil {
		return cursoraccount.CursorAccountLoginStatus{}, fmt.Errorf("Cursor 账号服务未初始化")
	}
	status, err := s.cursorAccount.LoginStatus(strings.TrimSpace(sessionID))
	if err == nil && status.State == cursoraccount.StateSignedIn {
		// 全量余额刷新可能持续数十秒，放后台执行，绝不阻塞登录状态轮询响应。
		safego.Go("client:登录完成后余额同步", func() {
			s.maybeSyncAfterLoginSession(strings.TrimSpace(sessionID))
		})
	}
	return status, err
}

func (s *ProxyService) CancelCursorAccountLogin(sessionID string) (controlcenter.OperationResult, error) {
	if s == nil || s.cursorAccount == nil {
		return controlcenter.OperationResult{}, fmt.Errorf("Cursor 账号服务未初始化")
	}
	return s.cursorAccount.CancelLogin(strings.TrimSpace(sessionID))
}

func (s *ProxyService) ListRequestSources(query requestlab.RequestSourceQuery) (requestlab.RequestSourcePage, error) {
	if s == nil || s.requestLab == nil {
		return requestlab.RequestSourcePage{}, controlcenter.NewError("request_source_unavailable", "request lab is unavailable")
	}
	return s.requestLab.List(query)
}

func (s *ProxyService) BuildRequestComparison(request requestlab.RequestComparisonRequest) (requestlab.RequestComparison, error) {
	if s == nil || s.requestLab == nil {
		return requestlab.RequestComparison{}, controlcenter.NewError("request_source_unavailable", "request lab is unavailable")
	}
	return s.requestLab.Compare(request)
}

func (s *ProxyService) ExportSanitizedRequestComparison(comparisonID string) (controlcenter.SanitizedExport, error) {
	if s == nil || s.requestLab == nil {
		return controlcenter.SanitizedExport{}, controlcenter.NewError("comparison_not_found", "request lab is unavailable")
	}
	return s.requestLab.Export(comparisonID)
}

func (s *ProxyService) GetRoutingPolicy() (routing.Policy, error) {
	cfg, err := s.LoadUserConfig()
	if err != nil {
		return routing.DefaultPolicy(), err
	}
	return routing.NormalizePolicy(cfg.Routing.Policy)
}

func (s *ProxyService) SaveRoutingPolicy(policy routing.Policy) (routing.Policy, error) {
	normalized, err := routing.NormalizePolicy(policy)
	if err != nil {
		return routing.Policy{}, err
	}
	cfg, err := s.LoadUserConfig()
	if err != nil {
		return routing.Policy{}, controlcenter.WrapError("routing_policy_save_failed", "load config failed", err)
	}
	cfg.Routing.Policy = normalized
	if err := s.SaveUserConfig(cfg); err != nil {
		return routing.Policy{}, controlcenter.WrapError("routing_policy_save_failed", "save routing policy failed", err)
	}
	return normalized, nil
}

func (s *ProxyService) PreviewRoutingDecision(request routing.PreviewRequest) (routing.DecisionPreview, error) {
	if strings.TrimSpace(request.ModelID) == "" {
		return routing.DecisionPreview{}, controlcenter.NewError("routing_requirement_invalid", "model id is required")
	}
	policy, err := s.GetRoutingPolicy()
	if err != nil {
		return routing.DecisionPreview{}, err
	}
	candidates := s.routingPreviewCandidates(request.ModelID)
	if len(candidates) == 0 {
		return routing.DecisionPreview{}, controlcenter.NewError("routing_no_candidate", "no routing candidate")
	}
	decision := routing.Rank(policy, routing.Request{
		ModelID:                request.ModelID,
		EstimatedContextTokens: request.EstimatedContextTokens,
		SessionHash:            request.SessionHash,
		Requirements:           request.Requirements,
		Candidates:             candidates,
	})
	return routing.DecisionPreview{DecisionID: decision.DecisionID, Strategy: policy.Strategy, Candidates: decision.Candidates}, nil
}

func (s *ProxyService) routingPreviewCandidates(modelID string) []routing.CandidateInput {
	if s != nil && s.backendHost != nil {
		if candidates := s.backendHost.BuildRoutingCandidates(modelID); len(candidates) > 0 {
			return candidates
		}
	}
	if s != nil && s.configs != nil {
		return s.configs.BuildRoutingCandidates(modelID)
	}
	return nil
}

func (s *ProxyService) GetRoutingDecisionHistory(query routing.DecisionQuery) (routing.DecisionPage, error) {
	if s != nil && s.backendHost != nil {
		return s.backendHost.RoutingDecisionHistory(query)
	}
	if s == nil || s.routingHist == nil {
		return routing.DecisionPage{Items: []routing.DecisionRecord{}}, nil
	}
	return s.routingHist.List(query)
}

func (s *ProxyService) GetAgentRuns(query agentops.RunQuery) (agentops.RunPage, error) {
	if s == nil || s.agentOps == nil {
		return agentops.RunPage{}, controlcenter.NewError("agent_runs_unavailable", "agent runs unavailable")
	}
	return s.agentOps.List(query)
}

func (s *ProxyService) GetAgentRun(runID string) (agentops.RunDetail, error) {
	if s == nil || s.agentOps == nil {
		return agentops.RunDetail{}, controlcenter.NewError("agent_runs_unavailable", "agent runs unavailable")
	}
	return s.agentOps.Get(runID)
}

func (s *ProxyService) CancelAgentRun(runID string) (controlcenter.OperationResult, error) {
	if s == nil || s.agentOps == nil {
		return controlcenter.OperationResult{}, controlcenter.NewError("agent_runs_unavailable", "agent runs unavailable")
	}
	return s.agentOps.Cancel(runID)
}

func (s *ProxyService) PrepareAgentRunRetry(runID string) (agentops.RetryPreparation, error) {
	if s == nil || s.agentOps == nil {
		return agentops.RetryPreparation{}, controlcenter.NewError("agent_runs_unavailable", "agent runs unavailable")
	}
	return s.agentOps.PrepareRetry(runID)
}

func (s *ProxyService) ExecuteAgentRunRetry(confirmationToken string) (agentops.RetryResult, error) {
	if s == nil || s.agentOps == nil {
		return agentops.RetryResult{}, controlcenter.NewError("agent_runs_unavailable", "agent runs unavailable")
	}
	return s.agentOps.ExecuteRetry(confirmationToken)
}

func (s *ProxyService) ExportSanitizedAgentRunReport(runID string) (controlcenter.SanitizedExport, error) {
	if s == nil || s.agentOps == nil {
		return controlcenter.SanitizedExport{}, controlcenter.NewError("agent_run_not_found", "agent runs unavailable")
	}
	return s.agentOps.Export(runID)
}

func (s *ProxyService) ListConfigProfiles() ([]configprofile.Summary, error) {
	if s == nil || s.profiles == nil {
		return nil, controlcenter.NewError("profile_store_unreadable", "profile store is unreadable")
	}
	return s.profiles.List()
}

func (s *ProxyService) SaveCurrentConfigProfile(request configprofile.SaveRequest) (configprofile.Summary, error) {
	if s == nil || s.profiles == nil {
		return configprofile.Summary{}, controlcenter.NewError("profile_store_unreadable", "profile store is unreadable")
	}
	cfg, err := s.LoadUserConfig()
	if err != nil {
		return configprofile.Summary{}, err
	}
	return s.profiles.SaveCurrent(request.Name, request.Description, request.Domains, cfg)
}

func (s *ProxyService) DeleteConfigProfile(profileID string) (controlcenter.OperationResult, error) {
	if s == nil || s.profiles == nil {
		return controlcenter.OperationResult{}, controlcenter.NewError("profile_store_unreadable", "profile store is unreadable")
	}
	return s.profiles.Delete(profileID)
}

func (s *ProxyService) PreviewConfigProfile(profileID string) (configprofile.Preview, error) {
	if s == nil || s.profiles == nil {
		return configprofile.Preview{}, controlcenter.NewError("profile_store_unreadable", "profile store is unreadable")
	}
	cfg, err := s.LoadUserConfig()
	if err != nil {
		return configprofile.Preview{}, err
	}
	return s.profiles.Preview(profileID, cfg)
}

func (s *ProxyService) PrepareConfigProfileApply(profileID string) (configprofile.ApplyPreparation, error) {
	if s == nil || s.profiles == nil {
		return configprofile.ApplyPreparation{}, controlcenter.NewError("profile_store_unreadable", "profile store is unreadable")
	}
	cfg, err := s.LoadUserConfig()
	if err != nil {
		return configprofile.ApplyPreparation{}, err
	}
	return s.profiles.PrepareApply(profileID, cfg)
}

func (s *ProxyService) ExecuteConfigProfileApply(confirmationToken string) (controlcenter.OperationResult, error) {
	if s == nil || s.profiles == nil {
		return controlcenter.OperationResult{}, controlcenter.NewError("profile_store_unreadable", "profile store is unreadable")
	}
	cfg, err := s.LoadUserConfig()
	if err != nil {
		return controlcenter.OperationResult{}, err
	}
	return s.profiles.ExecuteApply(confirmationToken, cfg, func(next UserConfig) (UserConfig, error) {
		if err := s.SaveUserConfig(next); err != nil {
			return UserConfig{}, err
		}
		return s.LoadUserConfig()
	})
}

func (s *ProxyService) ExportConfigProfile(profileID string) (controlcenter.SanitizedExport, error) {
	if s == nil || s.profiles == nil {
		return controlcenter.SanitizedExport{}, controlcenter.NewError("profile_not_found", "profile store is unreadable")
	}
	return s.profiles.Export(profileID)
}

func (s *ProxyService) ImportConfigProfile(content string) (configprofile.Preview, error) {
	if s == nil || s.profiles == nil {
		return configprofile.Preview{}, controlcenter.NewError("profile_store_unreadable", "profile store is unreadable")
	}
	preview, err := s.profiles.Import(content)
	if err != nil && preview.Profile.ID == "" {
		return preview, err
	}
	cfg, cfgErr := s.LoadUserConfig()
	if cfgErr != nil || strings.TrimSpace(preview.Profile.ID) == "" {
		return preview, err
	}
	full, previewErr := s.profiles.Preview(preview.Profile.ID, cfg)
	if previewErr != nil {
		if err != nil {
			return preview, err
		}
		return preview, previewErr
	}
	if err != nil {
		return full, err
	}
	return full, nil
}
