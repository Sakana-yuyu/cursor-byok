package bridge

import (
	"cursor/internal/controlcenter"
	"cursor/internal/cursoraccount"
)

type ControlCenterOverview = controlcenter.ControlCenterOverview
type ControlCenterDomainStatus = controlcenter.ControlCenterDomainStatus
type PreparedOperation = controlcenter.PreparedOperation
type OperationResult = controlcenter.OperationResult
type CursorAccountSummary = cursoraccount.CursorAccountSummary
type CursorAccountImportRequest = cursoraccount.CursorAccountImportRequest
type CursorAccountRecoveryExportRequest = cursoraccount.CursorAccountRecoveryExportRequest
type CursorAccountRecoveryExportResult = cursoraccount.CursorAccountRecoveryExportResult
type CursorAccountDeleteRequest = cursoraccount.CursorAccountDeleteRequest
type CursorAccountLoginSession = cursoraccount.CursorAccountLoginSession
type CursorAccountLoginStatus = cursoraccount.CursorAccountLoginStatus
type CursorAccountSwitchPreparation = cursoraccount.CursorAccountSwitchPreparation
type CursorAccountSwitchResult = cursoraccount.CursorAccountSwitchResult

func WireCursorRuntime(service *ProxyService, windows *WindowService) {
	if service == nil || service.core == nil {
		return
	}
	service.core.AttachCursorRuntime(newCursorProcessRuntime(windows))
}

func (s *ProxyService) GetControlCenterOverview() (ControlCenterOverview, error) {
	return s.core.GetControlCenterOverview()
}

func (s *ProxyService) ListCursorAccounts() ([]CursorAccountSummary, error) {
	return s.core.ListCursorAccounts()
}

func (s *ProxyService) ImportCursorAccount(req CursorAccountImportRequest) (CursorAccountSummary, error) {
	return s.core.ImportCursorAccount(req)
}

func (s *ProxyService) PrepareCursorAccountRecoveryExport(req CursorAccountRecoveryExportRequest) (PreparedOperation, error) {
	return s.core.PrepareCursorAccountRecoveryExport(req)
}

func (s *ProxyService) ExecuteCursorAccountRecoveryExport(confirmationToken, destinationPath string) (CursorAccountRecoveryExportResult, error) {
	return s.core.ExecuteCursorAccountRecoveryExport(confirmationToken, destinationPath)
}

func (s *ProxyService) SetCurrentCursorAccount(accountID string) (CursorAccountSummary, error) {
	return s.core.SetCurrentCursorAccount(accountID)
}

func (s *ProxyService) UpdateCursorAccountTags(accountID string, tags []string) (CursorAccountSummary, error) {
	return s.core.UpdateCursorAccountTags(accountID, tags)
}

func (s *ProxyService) DeleteCursorAccounts(req CursorAccountDeleteRequest) error {
	return s.core.DeleteCursorAccounts(req)
}

func (s *ProxyService) PrepareCursorClientAccountSwitch(accountID string) (CursorAccountSwitchPreparation, error) {
	return s.core.PrepareCursorClientAccountSwitch(accountID)
}

func (s *ProxyService) ExecuteCursorClientAccountSwitch(confirmationToken string) (CursorAccountSwitchResult, error) {
	return s.core.ExecuteCursorClientAccountSwitch(confirmationToken)
}

func (s *ProxyService) BeginCursorAccountLogin() (CursorAccountLoginSession, error) {
	return s.core.BeginCursorAccountLogin()
}

func (s *ProxyService) GetCursorAccountLoginStatus(sessionID string) (CursorAccountLoginStatus, error) {
	return s.core.GetCursorAccountLoginStatus(sessionID)
}

func (s *ProxyService) CancelCursorAccountLogin(sessionID string) (OperationResult, error) {
	return s.core.CancelCursorAccountLogin(sessionID)
}
