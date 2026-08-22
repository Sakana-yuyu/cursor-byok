package cursoraccount

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cursor/internal/controlcenter"
	"cursor/internal/cursor"
	"cursor/internal/logger"

	"github.com/google/uuid"
)

const (
	maxSuccessfulSwitchBackups = 10
	impactCursorRestart        = "cursor_restart_required"
	impactCursorStateOverwrite = "cursor_state_auth_overwrite"
)

// CursorRuntime is the injectable Cursor process adapter used by account switch.
type CursorRuntime interface {
	Running() bool
	Stop(ctx context.Context) error
	Start(ctx context.Context) error
}

// CursorAccountSwitchPreparation is the prepare DTO for switching the local Cursor client.
type CursorAccountSwitchPreparation struct {
	controlcenter.PreparedOperation
	Account         CursorAccountSummary `json:"account"`
	CursorRunning   bool                 `json:"cursorRunning"`
	RequiresRestart bool                 `json:"requiresRestart"`
	BackupFileCount int                  `json:"backupFileCount"`
}

// CursorAccountSwitchResult is the execute DTO for switching the local Cursor client.
type CursorAccountSwitchResult struct {
	controlcenter.OperationResult
	Account         CursorAccountSummary `json:"account"`
	CursorRestarted bool                 `json:"cursorRestarted"`
}

type pendingSwitch struct {
	operationID string
	token       string
	expiresAt   time.Time
	accountID   string
	previousID  string
	used        bool
}

func (manager *Manager) PrepareCursorClientAccountSwitch(accountID string) (CursorAccountSwitchPreparation, error) {
	if manager == nil || manager.store == nil {
		return CursorAccountSwitchPreparation{}, fmt.Errorf("cursor account store is not initialized")
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return CursorAccountSwitchPreparation{}, accountError("account_not_found", "account id is empty")
	}
	record, err := manager.store.loadRecord(accountID)
	if err != nil {
		return CursorAccountSwitchPreparation{}, err
	}
	if strings.TrimSpace(record.AccessToken) == "" || strings.TrimSpace(record.Email) == "" {
		return CursorAccountSwitchPreparation{}, accountError("account_unusable", "account is unusable")
	}
	runtime := manager.runtime()
	if runtime == nil {
		return CursorAccountSwitchPreparation{}, accountError("cursor_process_probe_failed", "cursor process probe failed")
	}
	dbPath, err := manager.resolveStateDBPath()
	if err != nil {
		return CursorAccountSwitchPreparation{}, accountError("cursor_state_db_missing", "cursor state db is missing")
	}
	if _, err := os.Stat(dbPath); err != nil {
		return CursorAccountSwitchPreparation{}, accountError("cursor_state_db_missing", "cursor state db is missing")
	}

	manager.switchMu.Lock()
	defer manager.switchMu.Unlock()
	if manager.pendingSwitch != nil && !manager.pendingSwitch.used && time.Now().Before(manager.pendingSwitch.expiresAt) {
		return CursorAccountSwitchPreparation{}, accountError("account_switch_busy", "account switch is already pending")
	}
	token, err := randomConfirmationToken()
	if err != nil {
		return CursorAccountSwitchPreparation{}, err
	}
	operationID := uuid.NewString()
	expiresAt := time.Now().Add(exportConfirmTTL)
	running := runtime.Running()
	manager.pendingSwitch = &pendingSwitch{
		operationID: operationID,
		token:       token,
		expiresAt:   expiresAt,
		accountID:   record.ID,
		previousID:  manager.store.CurrentAccountID(),
	}
	return CursorAccountSwitchPreparation{
		PreparedOperation: controlcenter.PreparedOperation{
			OperationID:       operationID,
			ConfirmationToken: token,
			ExpiresAtUnixMS:   expiresAt.UnixMilli(),
			ImpactCodes:       []string{impactCursorRestart, impactCursorStateOverwrite},
			RollbackAvailable: true,
		},
		Account:         recordToSummary(record, record.ID == manager.store.CurrentAccountID()),
		CursorRunning:   running,
		RequiresRestart: true,
		BackupFileCount: countStateSidecars(dbPath),
	}, nil
}

func (manager *Manager) ExecuteCursorClientAccountSwitch(confirmationToken string) (CursorAccountSwitchResult, error) {
	if manager == nil || manager.store == nil {
		return CursorAccountSwitchResult{}, fmt.Errorf("cursor account store is not initialized")
	}
	manager.switchMu.Lock()
	pending := manager.pendingSwitch
	switch {
	case pending == nil:
		manager.switchMu.Unlock()
		return CursorAccountSwitchResult{}, accountError("confirmation_expired", "confirmation expired")
	case pending.used:
		manager.switchMu.Unlock()
		return CursorAccountSwitchResult{}, accountError("confirmation_already_used", "confirmation already used")
	case time.Now().After(pending.expiresAt):
		manager.switchMu.Unlock()
		return CursorAccountSwitchResult{}, accountError("confirmation_expired", "confirmation expired")
	case !confirmationTokenMatches(pending.token, strings.TrimSpace(confirmationToken)):
		manager.switchMu.Unlock()
		return CursorAccountSwitchResult{}, accountError("confirmation_expired", "confirmation expired")
	default:
		pending.used = true
		manager.switchMu.Unlock()
	}

	ctx := context.Background()
	result, err := manager.executeSwitch(ctx, pending)
	if err != nil {
		logger.Infof("cursor client account switch failed operation=%s", pending.operationID)
	}
	return result, err
}

func (manager *Manager) executeSwitch(ctx context.Context, pending *pendingSwitch) (result CursorAccountSwitchResult, err error) {
	runtime := manager.runtime()
	if runtime == nil {
		return failedSwitchResult(pending.operationID, "cursor_process_probe_failed"), accountError("cursor_process_probe_failed", "cursor process probe failed")
	}
	record, err := manager.store.loadRecord(pending.accountID)
	if err != nil {
		return failedSwitchResult(pending.operationID, "account_not_found"), err
	}
	dbPath, err := manager.resolveStateDBPath()
	if err != nil {
		return failedSwitchResult(pending.operationID, "cursor_state_db_missing"), accountError("cursor_state_db_missing", "cursor state db is missing")
	}

	var (
		stopped    bool
		started    bool
		backedUp   bool
		backup     cursor.CursorStateBackup
		currentSet bool
		previousID = pending.previousID
	)
	defer func() {
		if err == nil {
			return
		}
		result = manager.rollbackSwitch(ctx, runtime, backup, backedUp, started, currentSet, previousID, pending.operationID, result.ErrorCode, err)
	}()

	if err = runtime.Stop(ctx); err != nil {
		return failedSwitchResult(pending.operationID, "cursor_process_close_failed"), wrapAccountError("cursor_process_close_failed", "close cursor failed", err)
	}
	stopped = true
	_ = stopped

	dest := filepath.Join(manager.store.root, storeDirName, "backups", pending.operationID)
	backup, err = cursor.BackupCursorStateFiles(dbPath, dest)
	if err != nil {
		return failedSwitchResult(pending.operationID, "account_switch_backup_failed"), wrapAccountError("account_switch_backup_failed", "backup cursor state failed", err)
	}
	backedUp = true

	replaceValues := cursor.CursorAuthValues{
		AccessToken:  record.AccessToken,
		RefreshToken: record.RefreshToken,
		Email:        record.Email,
	}
	if err = cursor.ReplaceCursorAuth(dbPath, replaceValues); err != nil {
		return failedSwitchResult(pending.operationID, "account_switch_write_failed"), wrapAccountError("account_switch_write_failed", "write cursor auth failed", err)
	}
	if err = verifyReplacedCursorAuth(dbPath, replaceValues); err != nil {
		return failedSwitchResult(pending.operationID, "account_switch_verify_failed"), wrapAccountError("account_switch_verify_failed", "verify cursor auth failed", err)
	}

	if _, err = manager.store.SetCurrent(record.ID); err != nil {
		return failedSwitchResult(pending.operationID, "account_switch_write_failed"), err
	}
	currentSet = true
	manager.adoptCurrentFromStore()

	if err = runtime.Start(ctx); err != nil {
		return failedSwitchResult(pending.operationID, "cursor_restart_failed"), wrapAccountError("cursor_restart_failed", "restart cursor failed", err)
	}
	started = true

	cleanupOldSwitchBackups(filepath.Join(manager.store.root, storeDirName, "backups"))
	summary := recordToSummary(record, true)
	return CursorAccountSwitchResult{
		OperationResult: controlcenter.OperationResult{
			OperationID:      pending.operationID,
			State:            "succeeded",
			FinishedAtUnixMS: time.Now().UnixMilli(),
		},
		Account:         summary,
		CursorRestarted: true,
	}, nil
}

func (manager *Manager) rollbackSwitch(
	ctx context.Context,
	runtime CursorRuntime,
	backup cursor.CursorStateBackup,
	backedUp bool,
	started bool,
	currentSet bool,
	previousID string,
	operationID string,
	errorCode string,
	cause error,
) CursorAccountSwitchResult {
	var restoreErr error
	if started && runtime != nil {
		restoreErr = errors.Join(restoreErr, runtime.Stop(ctx))
	}
	if backedUp {
		restoreErr = errors.Join(restoreErr, cursor.RestoreCursorStateFiles(backup))
	}
	if currentSet {
		if strings.TrimSpace(previousID) == "" {
			restoreErr = errors.Join(restoreErr, manager.store.clearCurrent())
		} else if _, err := manager.store.SetCurrent(previousID); err != nil {
			restoreErr = errors.Join(restoreErr, err)
		}
		manager.adoptCurrentFromStore()
	}
	if restoreErr != nil {
		logger.Infof("cursor client account switch rollback failed operation=%s", operationID)
		return CursorAccountSwitchResult{
			OperationResult: controlcenter.OperationResult{
				OperationID:      operationID,
				State:            "rollback_failed",
				ErrorCode:        "account_switch_rollback_failed",
				RollbackState:    "rollback_failed",
				FinishedAtUnixMS: time.Now().UnixMilli(),
			},
		}
	}
	state := "rolled_back"
	code := errorCode
	if code == "" {
		code = "cursor_restart_failed"
	}
	return CursorAccountSwitchResult{
		OperationResult: controlcenter.OperationResult{
			OperationID:      operationID,
			State:            state,
			ErrorCode:        code,
			RollbackState:    "rolled_back",
			FinishedAtUnixMS: time.Now().UnixMilli(),
		},
	}
}

func (manager *Manager) runtime() CursorRuntime {
	if manager == nil {
		return nil
	}
	if manager.cursorRuntime != nil {
		return manager.cursorRuntime
	}
	return defaultCursorRuntime{}
}

func (manager *Manager) resolveStateDBPath() (string, error) {
	if manager != nil && manager.stateDBPath != nil {
		return manager.stateDBPath()
	}
	return cursor.CursorStateDBPath()
}

func failedSwitchResult(operationID, errorCode string) CursorAccountSwitchResult {
	return CursorAccountSwitchResult{
		OperationResult: controlcenter.OperationResult{
			OperationID:      operationID,
			State:            "failed",
			ErrorCode:        errorCode,
			FinishedAtUnixMS: time.Now().UnixMilli(),
		},
	}
}

func verifyReplacedCursorAuth(path string, want cursor.CursorAuthValues) error {
	got, err := cursor.ReadCursorAuth(path)
	if err != nil {
		return err
	}
	if got.AccessToken != strings.TrimSpace(want.AccessToken) || got.Email != strings.TrimSpace(want.Email) {
		return fmt.Errorf("cursor auth readback mismatch")
	}
	if strings.TrimSpace(want.RefreshToken) != "" && got.RefreshToken != strings.TrimSpace(want.RefreshToken) {
		return fmt.Errorf("cursor auth readback mismatch")
	}
	return nil
}

func countStateSidecars(dbPath string) int {
	count := 0
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			count++
		}
	}
	return count
}

func cleanupOldSwitchBackups(root string) {
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) <= maxSuccessfulSwitchBackups {
		return
	}
	type backupDir struct {
		name string
		mod  time.Time
	}
	dirs := make([]backupDir, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, backupDir{name: entry.Name(), mod: info.ModTime()})
	}
	if len(dirs) <= maxSuccessfulSwitchBackups {
		return
	}
	for i := 0; i < len(dirs); i++ {
		for j := i + 1; j < len(dirs); j++ {
			if dirs[j].mod.Before(dirs[i].mod) {
				dirs[i], dirs[j] = dirs[j], dirs[i]
			}
		}
	}
	extra := len(dirs) - maxSuccessfulSwitchBackups
	for _, dir := range dirs[:extra] {
		_ = os.RemoveAll(filepath.Join(root, dir.name))
	}
}

type defaultCursorRuntime struct{}

func (defaultCursorRuntime) Running() bool { return false }

func (defaultCursorRuntime) Stop(context.Context) error {
	return fmt.Errorf("cursor runtime is not configured")
}

func (defaultCursorRuntime) Start(context.Context) error {
	return fmt.Errorf("cursor runtime is not configured")
}

func (manager *Manager) SetCursorRuntime(runtime CursorRuntime) {
	if manager == nil {
		return
	}
	manager.cursorRuntime = runtime
}

func (manager *Manager) SetStateDBPath(resolve func() (string, error)) {
	if manager == nil {
		return
	}
	manager.stateDBPath = resolve
}

func (s *AccountStore) loadRecord(id string) (accountRecord, error) {
	records, err := s.recordsByIDs([]string{id})
	if err != nil {
		return accountRecord{}, err
	}
	if len(records) == 0 {
		return accountRecord{}, accountError("account_not_found", "account not found")
	}
	return records[0], nil
}
