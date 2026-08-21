package client

import (
	"fmt"
	"strings"

	"cursor/internal/controlcenter"
	"cursor/internal/cursoraccount"
)

func (s *ProxyService) GetControlCenterOverview() (controlcenter.ControlCenterOverview, error) {
	overview := controlcenter.ControlCenterOverview{
		RequestLab: controlcenter.ControlCenterDomainStatus{State: "unavailable"},
		Routing:    controlcenter.ControlCenterDomainStatus{State: "unavailable"},
		Agents:     controlcenter.ControlCenterDomainStatus{State: "unavailable"},
		Profiles:   controlcenter.ControlCenterDomainStatus{State: "unavailable"},
	}
	if s == nil || s.cursorAccount == nil {
		overview.Accounts = controlcenter.ControlCenterDomainStatus{State: "error", WarningCode: "account_store_unreadable"}
		return overview, nil
	}
	accounts, err := s.cursorAccount.ListAccounts()
	if err != nil {
		overview.Accounts = controlcenter.ControlCenterDomainStatus{State: "error", WarningCode: "account_store_unreadable"}
		return overview, nil
	}
	state := "empty"
	if len(accounts) > 0 {
		state = "ready"
	}
	overview.Accounts = controlcenter.ControlCenterDomainStatus{State: state, Count: len(accounts)}
	return overview, nil
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
	return s.cursorAccount.LoginStatus(strings.TrimSpace(sessionID))
}

func (s *ProxyService) CancelCursorAccountLogin(sessionID string) (controlcenter.OperationResult, error) {
	if s == nil || s.cursorAccount == nil {
		return controlcenter.OperationResult{}, fmt.Errorf("Cursor 账号服务未初始化")
	}
	return s.cursorAccount.CancelLogin(strings.TrimSpace(sessionID))
}
