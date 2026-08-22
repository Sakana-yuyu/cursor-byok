package cursoraccount

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cursor/internal/controlcenter"
	"cursor/internal/logger"

	"github.com/google/uuid"
)

const (
	maxImportTokenBytes         = 8 * 1024
	maxImportJSONBytes          = 1 << 20
	exportConfirmTTL            = 60 * time.Second
	recoveryPackVersion         = 1
	impactCredentialFileCreated = "credential_file_created"
)

type CursorAccountImportRequest struct {
	Mode        string `json:"mode"`
	Token       string `json:"token,omitempty"`
	JSONContent string `json:"jsonContent,omitempty"`
}

type CursorAccountRecoveryExportRequest struct {
	AccountIDs []string `json:"accountIds"`
}

type CursorAccountRecoveryExportResult struct {
	controlcenter.OperationResult
	ExportedCount int `json:"exportedCount"`
}

type CursorAccountDeleteRequest struct {
	AccountIDs    []string `json:"accountIds"`
	ReplacementID string   `json:"replacementId,omitempty"`
	ClearCurrent  bool     `json:"clearCurrent,omitempty"`
}

type importedAccount struct {
	AccessToken  string
	RefreshToken string
	AuthID       string
	Email        string
	Tags         []string
}

type recoveryPack struct {
	Version  int                   `json:"version"`
	Accounts []recoveryPackAccount `json:"accounts"`
}

type recoveryPackAccount struct {
	Email        string   `json:"email,omitempty"`
	AuthID       string   `json:"authId,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	AccessToken  string   `json:"accessToken"`
	RefreshToken string   `json:"refreshToken,omitempty"`
}

type pendingExport struct {
	operationID string
	token       string
	expiresAt   time.Time
	accountIDs  []string
	used        bool
}

var (
	singleAccountImportKeys = map[string]struct{}{
		"accessToken":  {},
		"refreshToken": {},
		"authId":       {},
		"email":        {},
		"tags":         {},
	}
	recoveryPackImportKeys = map[string]struct{}{
		"version":  {},
		"accounts": {},
	}
	recoveryAccountImportKeys = map[string]struct{}{
		"accessToken":  {},
		"refreshToken": {},
		"authId":       {},
		"email":        {},
		"tags":         {},
	}
)

func (manager *Manager) PrepareRecoveryExport(req CursorAccountRecoveryExportRequest) (controlcenter.PreparedOperation, error) {
	if manager == nil || manager.store == nil {
		return controlcenter.PreparedOperation{}, fmt.Errorf("cursor account store is not initialized")
	}
	ids, err := collectUniqueIDs(req.AccountIDs, 100)
	if err != nil {
		switch {
		case errors.Is(err, errAccountIDsEmpty), errors.Is(err, errAccountIDEmpty):
			return controlcenter.PreparedOperation{}, accountError("account_export_empty", err.Error())
		case errors.Is(err, errTooManyAccountIDs):
			return controlcenter.PreparedOperation{}, accountError("account_export_too_many", err.Error())
		default:
			return controlcenter.PreparedOperation{}, err
		}
	}
	summaries, err := manager.store.List()
	if err != nil {
		return controlcenter.PreparedOperation{}, err
	}
	known := make(map[string]struct{}, len(summaries))
	for _, summary := range summaries {
		known[summary.ID] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := known[id]; !ok {
			return controlcenter.PreparedOperation{}, accountError("account_not_found", "account not found")
		}
	}

	manager.exportMu.Lock()
	defer manager.exportMu.Unlock()
	if manager.pendingExport != nil && !manager.pendingExport.used && time.Now().Before(manager.pendingExport.expiresAt) {
		return controlcenter.PreparedOperation{}, accountError("account_export_busy", "account export is already pending")
	}
	token, err := randomConfirmationToken()
	if err != nil {
		return controlcenter.PreparedOperation{}, err
	}
	operationID := uuid.NewString()
	expiresAt := time.Now().Add(exportConfirmTTL)
	frozen := make([]string, len(ids))
	copy(frozen, ids)
	manager.pendingExport = &pendingExport{
		operationID: operationID,
		token:       token,
		expiresAt:   expiresAt,
		accountIDs:  frozen,
	}
	return controlcenter.PreparedOperation{
		OperationID:       operationID,
		ConfirmationToken: token,
		ExpiresAtUnixMS:   expiresAt.UnixMilli(),
		ImpactCodes:       []string{impactCredentialFileCreated},
		RollbackAvailable: false,
	}, nil
}

func (manager *Manager) ExecuteRecoveryExport(confirmationToken, destinationPath string) (CursorAccountRecoveryExportResult, error) {
	if manager == nil || manager.store == nil {
		return CursorAccountRecoveryExportResult{}, fmt.Errorf("cursor account store is not initialized")
	}
	dest := strings.TrimSpace(destinationPath)
	if dest == "" {
		return failedExportResult("", "account_export_destination_invalid"), accountError("account_export_destination_invalid", "export destination is invalid")
	}
	if info, err := os.Stat(dest); err == nil && info.IsDir() {
		return failedExportResult("", "account_export_destination_invalid"), accountError("account_export_destination_invalid", "export destination is invalid")
	}
	if _, err := os.Stat(filepath.Dir(dest)); err != nil {
		return failedExportResult("", "account_export_destination_invalid"), accountError("account_export_destination_invalid", "export destination is invalid")
	}

	provided := strings.TrimSpace(confirmationToken)
	manager.exportMu.Lock()
	pending := manager.pendingExport
	now := time.Now()
	switch {
	case pending == nil || now.After(pending.expiresAt):
		manager.exportMu.Unlock()
		return failedExportResult("", "confirmation_expired"), accountError("confirmation_expired", "confirmation expired")
	case pending.used:
		manager.exportMu.Unlock()
		return failedExportResult(pending.operationID, "confirmation_already_used"), accountError("confirmation_already_used", "confirmation already used")
	case !confirmationTokenMatches(pending.token, provided):
		manager.exportMu.Unlock()
		return failedExportResult(pending.operationID, "confirmation_expired"), accountError("confirmation_expired", "confirmation expired")
	}
	pending.used = true
	ids := append([]string{}, pending.accountIDs...)
	operationID := pending.operationID
	manager.exportMu.Unlock()

	records, err := manager.store.recordsByIDs(ids)
	if err != nil {
		logger.Infof("cursor account recovery export failed count=%d file=%s", len(ids), filepath.Base(dest))
		return failedExportResult(operationID, "account_export_write_failed"), err
	}
	pack := recoveryPack{
		Version:  recoveryPackVersion,
		Accounts: make([]recoveryPackAccount, 0, len(records)),
	}
	for _, record := range records {
		tags := record.Tags
		if tags == nil {
			tags = []string{}
		}
		pack.Accounts = append(pack.Accounts, recoveryPackAccount{
			Email:        record.Email,
			AuthID:       record.AuthID,
			Tags:         tags,
			AccessToken:  record.AccessToken,
			RefreshToken: record.RefreshToken,
		})
	}
	if err := writeJSONFile(dest, pack); err != nil {
		logger.Infof("cursor account recovery export failed count=%d file=%s", len(ids), filepath.Base(dest))
		return failedExportResult(operationID, "account_export_write_failed"), accountError("account_export_write_failed", "account export write failed")
	}
	logger.Infof("cursor account recovery export succeeded count=%d file=%s", len(ids), filepath.Base(dest))
	return CursorAccountRecoveryExportResult{
		OperationResult: controlcenter.OperationResult{
			OperationID:      operationID,
			State:            "succeeded",
			FinishedAtUnixMS: time.Now().UnixMilli(),
		},
		ExportedCount: len(ids),
	}, nil
}

func failedExportResult(operationID, errorCode string) CursorAccountRecoveryExportResult {
	return CursorAccountRecoveryExportResult{
		OperationResult: controlcenter.OperationResult{
			OperationID:      operationID,
			State:            "failed",
			ErrorCode:        errorCode,
			FinishedAtUnixMS: time.Now().UnixMilli(),
		},
	}
}

func randomConfirmationToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func confirmationTokenMatches(stored, provided string) bool {
	if len(stored) != len(provided) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(provided)) == 1
}

func parseImportJSON(content string) ([]importedAccount, error) {
	if strings.TrimSpace(content) == "" {
		return nil, accountError("account_import_empty", "import json is empty")
	}
	if len(content) > maxImportJSONBytes {
		return nil, accountError("account_import_too_large", "import json exceeds 1 mib")
	}
	raw, err := unmarshalObject(content)
	if err != nil {
		return nil, wrapAccountError("account_import_invalid_schema", "import json is invalid", err)
	}
	if len(raw) == 0 {
		return nil, accountError("account_import_empty", "import json is empty")
	}
	if keysAllowed(raw, singleAccountImportKeys) {
		account, err := parseImportedAccount(raw)
		if err != nil {
			return nil, err
		}
		return []importedAccount{account}, nil
	}
	if keysAllowed(raw, recoveryPackImportKeys) {
		return parseRecoveryPack(raw)
	}
	return nil, accountError("account_import_invalid_schema", "import json has unknown fields")
}

func parseRecoveryPack(raw map[string]json.RawMessage) ([]importedAccount, error) {
	versionRaw, ok := raw["version"]
	if !ok {
		return nil, accountError("account_import_invalid_schema", "import json is invalid")
	}
	var version int
	if err := json.Unmarshal(versionRaw, &version); err != nil || version != recoveryPackVersion {
		return nil, accountError("account_import_invalid_schema", "import json is invalid")
	}
	accountsRaw, ok := raw["accounts"]
	if !ok {
		return nil, accountError("account_import_invalid_schema", "import json is invalid")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(accountsRaw, &items); err != nil {
		return nil, accountError("account_import_invalid_schema", "import json is invalid")
	}
	if len(items) == 0 {
		return nil, accountError("account_import_empty", "import json is empty")
	}
	accounts := make([]importedAccount, 0, len(items))
	for _, item := range items {
		object, err := unmarshalObjectBytes(item)
		if err != nil {
			return nil, wrapAccountError("account_import_invalid_schema", "import json is invalid", err)
		}
		if !keysAllowed(object, recoveryAccountImportKeys) {
			return nil, accountError("account_import_invalid_schema", "import json has unknown fields")
		}
		account, err := parseImportedAccount(object)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

func parseImportedAccount(raw map[string]json.RawMessage) (importedAccount, error) {
	account := importedAccount{}
	if err := unmarshalOptionalString(raw, "accessToken", &account.AccessToken); err != nil {
		return importedAccount{}, err
	}
	if err := unmarshalOptionalString(raw, "refreshToken", &account.RefreshToken); err != nil {
		return importedAccount{}, err
	}
	if err := unmarshalOptionalString(raw, "authId", &account.AuthID); err != nil {
		return importedAccount{}, err
	}
	if err := unmarshalOptionalString(raw, "email", &account.Email); err != nil {
		return importedAccount{}, err
	}
	if rawTags, ok := raw["tags"]; ok {
		if err := json.Unmarshal(rawTags, &account.Tags); err != nil {
			return importedAccount{}, accountError("account_import_invalid_schema", "import json is invalid")
		}
	}
	account.AccessToken = strings.TrimSpace(account.AccessToken)
	account.RefreshToken = strings.TrimSpace(account.RefreshToken)
	account.AuthID = strings.TrimSpace(account.AuthID)
	account.Email = strings.TrimSpace(account.Email)
	if account.AccessToken == "" {
		return importedAccount{}, accountError("account_import_invalid_schema", "import json is invalid")
	}
	return account, nil
}

func unmarshalObject(content string) (map[string]json.RawMessage, error) {
	return unmarshalObjectBytes([]byte(content))
}

func unmarshalObjectBytes(data []byte) (map[string]json.RawMessage, error) {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func keysAllowed(raw map[string]json.RawMessage, allowed map[string]struct{}) bool {
	if len(raw) == 0 {
		return false
	}
	for key := range raw {
		if _, ok := allowed[key]; !ok {
			return false
		}
	}
	return true
}

func unmarshalOptionalString(raw map[string]json.RawMessage, key string, dest *string) error {
	value, ok := raw[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(value, dest); err != nil {
		return accountError("account_import_invalid_schema", "import json is invalid")
	}
	return nil
}
