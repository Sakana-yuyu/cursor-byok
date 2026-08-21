package client

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cursor/internal/controlcenter"
	"cursor/internal/cursoraccount"
)

type CursorAccountStatus = cursoraccount.Status

func (s *ProxyService) GetCursorAccountStatus() CursorAccountStatus {
	if s == nil || s.cursorAccount == nil {
		return CursorAccountStatus{State: cursoraccount.StateSignedOut}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.cursorAccount.EnsureEmail(ctx)
	return s.cursorAccount.Status()
}

func (s *ProxyService) StartCursorAccountLogin() (CursorAccountStatus, error) {
	if s == nil || s.cursorAccount == nil {
		return CursorAccountStatus{State: cursoraccount.StateError}, fmt.Errorf("Cursor 账号服务未初始化")
	}
	return s.cursorAccount.StartLogin()
}

func (s *ProxyService) DisconnectCursorAccount() (CursorAccountStatus, error) {
	if s == nil || s.cursorAccount == nil {
		return CursorAccountStatus{State: cursoraccount.StateSignedOut}, nil
	}
	return s.cursorAccount.Disconnect()
}

func (s *ProxyService) ListCursorAccounts() ([]cursoraccount.CursorAccountSummary, error) {
	if s == nil || s.cursorAccount == nil {
		return []cursoraccount.CursorAccountSummary{}, fmt.Errorf("Cursor 账号服务未初始化")
	}
	return s.cursorAccount.ListAccounts()
}

func (s *ProxyService) ImportCursorAccount(req cursoraccount.CursorAccountImportRequest) (cursoraccount.CursorAccountSummary, error) {
	if s == nil || s.cursorAccount == nil {
		return cursoraccount.CursorAccountSummary{}, fmt.Errorf("Cursor 账号服务未初始化")
	}
	switch strings.TrimSpace(req.Mode) {
	case "local_cursor":
		return s.cursorAccount.ImportFromLocal()
	case "token":
		if strings.TrimSpace(req.Token) == "" {
			return cursoraccount.CursorAccountSummary{}, fmt.Errorf("import token is empty")
		}
		if len(req.Token) > 8*1024 {
			return cursoraccount.CursorAccountSummary{}, fmt.Errorf("import token exceeds 8 kib")
		}
		return s.cursorAccount.ImportToken(context.Background(), req.Token)
	case "recovery_json":
		if strings.TrimSpace(req.JSONContent) == "" {
			return cursoraccount.CursorAccountSummary{}, fmt.Errorf("import json is empty")
		}
		if len(req.JSONContent) > 1<<20 {
			return cursoraccount.CursorAccountSummary{}, fmt.Errorf("import json exceeds 1 mib")
		}
		summaries, err := s.cursorAccount.ImportJSON(req.JSONContent)
		if err != nil {
			return cursoraccount.CursorAccountSummary{}, err
		}
		if len(summaries) == 0 {
			return cursoraccount.CursorAccountSummary{}, fmt.Errorf("import json is empty")
		}
		return summaries[0], nil
	default:
		return cursoraccount.CursorAccountSummary{}, fmt.Errorf("unsupported import mode")
	}
}

func (s *ProxyService) PrepareCursorAccountRecoveryExport(req cursoraccount.CursorAccountRecoveryExportRequest) (controlcenter.PreparedOperation, error) {
	if s == nil || s.cursorAccount == nil {
		return controlcenter.PreparedOperation{}, fmt.Errorf("Cursor 账号服务未初始化")
	}
	for _, id := range req.AccountIDs {
		if strings.TrimSpace(id) == "" {
			return controlcenter.PreparedOperation{}, fmt.Errorf("account id is empty")
		}
	}
	return s.cursorAccount.PrepareRecoveryExport(req)
}

func (s *ProxyService) ExecuteCursorAccountRecoveryExport(confirmationToken, destinationPath string) (cursoraccount.CursorAccountRecoveryExportResult, error) {
	if s == nil || s.cursorAccount == nil {
		return cursoraccount.CursorAccountRecoveryExportResult{}, fmt.Errorf("Cursor 账号服务未初始化")
	}
	return s.cursorAccount.ExecuteRecoveryExport(confirmationToken, destinationPath)
}

func (s *ProxyService) SetCurrentCursorAccount(accountID string) (cursoraccount.CursorAccountSummary, error) {
	if s == nil || s.cursorAccount == nil {
		return cursoraccount.CursorAccountSummary{}, fmt.Errorf("Cursor 账号服务未初始化")
	}
	if strings.TrimSpace(accountID) == "" {
		return cursoraccount.CursorAccountSummary{}, fmt.Errorf("account id is empty")
	}
	return s.cursorAccount.SetCurrent(accountID)
}

func (s *ProxyService) UpdateCursorAccountTags(accountID string, tags []string) (cursoraccount.CursorAccountSummary, error) {
	if s == nil || s.cursorAccount == nil {
		return cursoraccount.CursorAccountSummary{}, fmt.Errorf("Cursor 账号服务未初始化")
	}
	if strings.TrimSpace(accountID) == "" {
		return cursoraccount.CursorAccountSummary{}, fmt.Errorf("account id is empty")
	}
	return s.cursorAccount.UpdateTags(accountID, tags)
}

func (s *ProxyService) DeleteCursorAccounts(req cursoraccount.CursorAccountDeleteRequest) error {
	if s == nil || s.cursorAccount == nil {
		return fmt.Errorf("Cursor 账号服务未初始化")
	}
	for _, id := range req.AccountIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("account id is empty")
		}
	}
	return s.cursorAccount.Delete(req)
}

func (s *ProxyService) PrepareCursorClientAccountSwitch(accountID string) (cursoraccount.CursorAccountSwitchPreparation, error) {
	if s == nil || s.cursorAccount == nil {
		return cursoraccount.CursorAccountSwitchPreparation{}, fmt.Errorf("Cursor 账号服务未初始化")
	}
	if strings.TrimSpace(accountID) == "" {
		return cursoraccount.CursorAccountSwitchPreparation{}, fmt.Errorf("account id is empty")
	}
	return s.cursorAccount.PrepareCursorClientAccountSwitch(accountID)
}

func (s *ProxyService) ExecuteCursorClientAccountSwitch(confirmationToken string) (cursoraccount.CursorAccountSwitchResult, error) {
	if s == nil || s.cursorAccount == nil {
		return cursoraccount.CursorAccountSwitchResult{}, fmt.Errorf("Cursor 账号服务未初始化")
	}
	return s.cursorAccount.ExecuteCursorClientAccountSwitch(confirmationToken)
}
