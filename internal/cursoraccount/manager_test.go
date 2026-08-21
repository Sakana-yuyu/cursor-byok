package cursoraccount

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeBackupFile(t *testing.T, path string, values map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestImportFromCursorBackup(t *testing.T) {
	dir := t.TempDir()
	backupPath := filepath.Join(dir, "cursor-auth-backup.json")

	t.Run("imports_real_values", func(t *testing.T) {
		root := t.TempDir()
		credPath := filepath.Join(root, "cursor-account.json")
		writeBackupFile(t, backupPath, map[string]any{
			"cursorAuth/accessToken":  "access-token-123",
			"cursorAuth/refreshToken": "refresh-token-456",
			"cursorAuth/cachedEmail":  "user@example.com",
		})
		manager := NewManager(root, credPath, nil)
		imported, err := manager.ImportFromCursorBackup(backupPath)
		if err != nil {
			t.Fatal(err)
		}
		if !imported {
			t.Fatal("expected imported=true")
		}
		status := manager.Status()
		if status.State != StateSignedIn {
			t.Fatalf("expected signed_in, got %q", status.State)
		}
		if status.Email != "user@example.com" {
			t.Fatalf("email mismatch: %q", status.Email)
		}
		// 已持久化，重新构造 Manager 应仍是登录态。
		reloaded := NewManager(root, credPath, nil)
		if reloaded.Status().State != StateSignedIn {
			t.Fatal("expected persisted credentials to reload as signed_in")
		}
	})

	t.Run("null_values_do_not_import", func(t *testing.T) {
		root := t.TempDir()
		credPath := filepath.Join(root, "cursor-account.json")
		writeBackupFile(t, backupPath, map[string]any{
			"cursorAuth/accessToken":  nil,
			"cursorAuth/refreshToken": nil,
			"cursorAuth/cachedEmail":  nil,
		})
		manager := NewManager(root, credPath, nil)
		imported, err := manager.ImportFromCursorBackup(backupPath)
		if err != nil {
			t.Fatal(err)
		}
		if imported {
			t.Fatal("expected imported=false for null backup")
		}
		if manager.Status().State != StateSignedOut {
			t.Fatalf("expected signed_out, got %q", manager.Status().State)
		}
	})

	t.Run("missing_file_does_not_import", func(t *testing.T) {
		root := t.TempDir()
		credPath := filepath.Join(root, "cursor-account.json")
		manager := NewManager(root, credPath, nil)
		imported, err := manager.ImportFromCursorBackup(filepath.Join(dir, "does-not-exist.json"))
		if err != nil {
			t.Fatal(err)
		}
		if imported {
			t.Fatal("expected imported=false for missing backup")
		}
	})

	t.Run("already_signed_in_not_overwritten", func(t *testing.T) {
		root := t.TempDir()
		credPath := filepath.Join(root, "cursor-account.json")
		manager := NewManager(root, credPath, nil)
		manager.mu.Lock()
		manager.credentials = credentials{AccessToken: "manual-token", Email: "manual@example.com"}
		manager.state = StateSignedIn
		manager.mu.Unlock()
		writeBackupFile(t, backupPath, map[string]any{
			"cursorAuth/accessToken": "backup-token",
			"cursorAuth/cachedEmail": "backup@example.com",
		})
		imported, err := manager.ImportFromCursorBackup(backupPath)
		if err != nil {
			t.Fatal(err)
		}
		if imported {
			t.Fatal("expected imported=false when already signed in")
		}
		status := manager.Status()
		if status.Email != "manual@example.com" {
			t.Fatalf("expected manual credentials preserved, got %q", status.Email)
		}
	})

	t.Run("corrupt_backup_returns_error", func(t *testing.T) {
		root := t.TempDir()
		credPath := filepath.Join(root, "cursor-account.json")
		if err := os.WriteFile(backupPath, []byte("{not-json"), 0o600); err != nil {
			t.Fatal(err)
		}
		manager := NewManager(root, credPath, nil)
		if _, err := manager.ImportFromCursorBackup(backupPath); err == nil {
			t.Fatal("expected error for corrupt backup")
		}
	})

	t.Run("save_failure_rolls_back_state", func(t *testing.T) {
		root := t.TempDir()
		accountsPath := filepath.Join(root, "cursor-accounts", "accounts")
		if err := os.MkdirAll(filepath.Dir(accountsPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(accountsPath, []byte("not-a-directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		writeBackupFile(t, backupPath, map[string]any{
			"cursorAuth/accessToken": "access-token-rollback",
			"cursorAuth/cachedEmail": "rollback@example.com",
		})
		manager := NewManager(root, filepath.Join(root, "cursor-account.json"), nil)
		imported, err := manager.ImportFromCursorBackup(backupPath)
		if err == nil {
			t.Fatal("expected error when save fails")
		}
		if imported {
			t.Fatal("expected imported=false when save fails")
		}
		if manager.Status().State == StateSignedIn {
			t.Fatalf("expected not signed_in after failed save, got %q", manager.Status().State)
		}
	})

	t.Run("waiting_login_not_overwritten_by_stale_finish", func(t *testing.T) {
		root := t.TempDir()
		credPath := filepath.Join(root, "cursor-account.json")
		writeBackupFile(t, backupPath, map[string]any{
			"cursorAuth/accessToken":  "imported-token",
			"cursorAuth/refreshToken": "imported-refresh",
			"cursorAuth/cachedEmail":  "imported@example.com",
		})
		manager := NewManager(root, credPath, nil)
		manager.mu.Lock()
		manager.state = StateWaiting
		manager.loginGeneration = 7
		manager.mu.Unlock()
		imported, err := manager.ImportFromCursorBackup(backupPath)
		if err != nil {
			t.Fatal(err)
		}
		if !imported {
			t.Fatal("expected imported=true over waiting state")
		}
		// 模拟旧的 pollLogin 协程 finishWithError：generation 已被导入流程递增，应 no-op。
		manager.finishWithError(7, "登录失败")
		if manager.Status().State != StateSignedIn {
			t.Fatalf("expected imported state preserved, got %q", manager.Status().State)
		}
		if manager.Status().Email != "imported@example.com" {
			t.Fatalf("email mismatch: %q", manager.Status().Email)
		}
	})

	t.Run("disconnect_marker_blocks_reimport", func(t *testing.T) {
		root := t.TempDir()
		credPath := filepath.Join(root, "cursor-account.json")
		markerPath := filepath.Join(root, "auto-import-off.marker")
		writeBackupFile(t, backupPath, map[string]any{
			"cursorAuth/accessToken": "access-token-marker",
			"cursorAuth/cachedEmail": "marker@example.com",
		})
		manager := NewManager(root, credPath, nil)
		manager.importOffMarkerPath = markerPath
		if err := os.WriteFile(markerPath, []byte("1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		imported, err := manager.ImportFromCursorBackup(backupPath)
		if err != nil {
			t.Fatal(err)
		}
		if imported {
			t.Fatal("expected imported=false when disconnect marker present")
		}
		if manager.Status().State != StateSignedOut {
			t.Fatalf("expected signed_out, got %q", manager.Status().State)
		}
	})

	t.Run("commit_credentials_clears_marker", func(t *testing.T) {
		root := t.TempDir()
		credPath := filepath.Join(root, "cursor-account.json")
		markerPath := filepath.Join(root, "auto-import-off-clear.marker")
		manager := NewManager(root, credPath, nil)
		manager.importOffMarkerPath = markerPath
		if err := writeAutoImportOffMarker(markerPath); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(markerPath); err != nil {
			t.Fatalf("marker should exist before commit: %v", err)
		}
		manager.mu.Lock()
		manager.loginGeneration = 3
		manager.mu.Unlock()
		if err := manager.commitCredentials(3, credentials{
			AccessToken:  "manual-token",
			RefreshToken: "manual-refresh",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("marker should be removed after manual login, stat err=%v", err)
		}
		if manager.Status().State != StateSignedIn {
			t.Fatalf("expected signed_in, got %q", manager.Status().State)
		}
	})
}

func TestManagerLoadsCurrentWhenLegacyMoveBlocked(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "cursor-account.json")
	writeTestJSON(t, legacy, map[string]string{
		"accessToken":  "test-access",
		"refreshToken": "test-refresh",
		"authId":       "auth-a",
		"email":        "a@example.test",
	})
	if err := os.WriteFile(filepath.Join(root, "legacy"), []byte("not-a-directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(root, legacy, nil)
	status := manager.Status()
	if status.State != StateSignedIn {
		t.Fatalf("expected signed_in after store commit, got %q err=%q", status.State, status.Error)
	}
	if status.Email != "a@example.test" {
		t.Fatalf("email mismatch: %q", status.Email)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func staticHTTPResponse(status int, contentType string, body []byte) *http.Response {
	if contentType == "" {
		contentType = "application/json"
	}
	header := make(http.Header)
	header.Set("Content-Type", contentType)
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func rejectNetwork(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.New("unexpected http request in test")
	}
	return nil, errors.New("unexpected http request in test: " + req.URL.String())
}

func pollReturns(access, refresh, authID string) http.RoundTripper {
	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req == nil || req.URL == nil {
			return nil, errors.New("unexpected http request in test")
		}
		path := req.URL.Path
		switch {
		case strings.Contains(path, "/auth/poll"):
			payload, err := json.Marshal(pollResponse{
				AccessToken:  access,
				RefreshToken: refresh,
				AuthID:       authID,
			})
			if err != nil {
				return nil, err
			}
			return staticHTTPResponse(http.StatusOK, "application/json", payload), nil
		case strings.Contains(path, "GetMe"):
			return staticHTTPResponse(http.StatusInternalServerError, "text/plain", []byte("profile unused")), nil
		default:
			return nil, errors.New("unexpected http request in test: " + req.URL.String())
		}
	})
}

func newTestManager(t *testing.T, transport http.RoundTripper) *Manager {
	t.Helper()
	root := t.TempDir()
	if transport == nil {
		transport = roundTripperFunc(rejectNetwork)
	}
	manager := NewManager(root, filepath.Join(root, "cursor-account.json"), &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
	})
	manager.openLoginURL = func(string) error { return nil }
	return manager
}

func (manager *Manager) AddCredentialsForTest(value credentials, setCurrent bool) (CursorAccountSummary, error) {
	if manager == nil || manager.store == nil {
		return CursorAccountSummary{}, errors.New("manager is nil")
	}
	summary, err := manager.store.Upsert(value)
	if err != nil {
		return CursorAccountSummary{}, err
	}
	if !setCurrent {
		return summary, nil
	}
	summary, err = manager.store.SetCurrent(summary.ID)
	if err != nil {
		return CursorAccountSummary{}, err
	}
	manager.mu.Lock()
	manager.credentials = value
	manager.state = StateSignedIn
	manager.lastError = ""
	manager.mu.Unlock()
	return summary, nil
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting")
}

func currentAuthIDHint(t *testing.T, manager *Manager) string {
	t.Helper()
	accounts, err := manager.ListAccounts()
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	currentID := manager.CurrentAccountID()
	for _, account := range accounts {
		if account.ID == currentID {
			return account.AuthIDHint
		}
	}
	return ""
}

func TestManagerOAuthCompletionAddsAccountWithoutOverwritingCurrent(t *testing.T) {
	manager := newTestManager(t, pollReturns("new-access", "new-refresh", "auth-b"))
	_, err := manager.AddCredentialsForTest(credentials{AccessToken: "old", AuthID: "auth-a", Email: "a@example.test"}, true)
	if err != nil {
		t.Fatal(err)
	}
	status, err := manager.StartLogin()
	if err != nil || status.State != StateWaiting {
		t.Fatalf("got %#v, %v", status, err)
	}
	waitFor(t, func() bool {
		accounts, listErr := manager.ListAccounts()
		return listErr == nil && len(accounts) == 2
	})
	if currentAuthIDHint(t, manager) != "auth-a" {
		t.Fatal("OAuth import changed current account")
	}
}

func TestManagerImportJSONRejectsUnknownCredentialContainers(t *testing.T) {
	manager := newTestManager(t, nil)
	_, err := manager.ImportJSON(`{"cookies":["secret"],"accessToken":"token"}`)
	if err == nil {
		t.Fatal("expected schema rejection")
	}
}

func TestManagerImportJSONAcceptsWhitelistedSingleAccount(t *testing.T) {
	manager := newTestManager(t, nil)
	got, err := manager.ImportJSON(`{"accessToken":"test-access","refreshToken":"test-refresh","authId":"auth-a","email":"a@example.test"}`)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %#v, %v", got, err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("test-access")) || bytes.Contains(encoded, []byte("test-refresh")) {
		t.Fatal("summary leaked token")
	}
	if got[0].AuthIDHint != "auth-a" || got[0].Email != "a@example.test" || !got[0].IsCurrent {
		t.Fatalf("unexpected summary: %#v", got[0])
	}
}

func TestManagerImportFromLocalUsesInjectedReader(t *testing.T) {
	manager := newTestManager(t, nil)
	manager.localAuthReader = func() (credentials, error) {
		return testCredential("auth-local", "local@example.test"), nil
	}
	got, err := manager.ImportFromLocal()
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthIDHint != "auth-local" || !got.IsCurrent {
		t.Fatalf("unexpected imported account: %#v", got)
	}
}

func expiredTestJWT() string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(-time.Hour).Unix())))
	return header + "." + payload + ".sig"
}

func TestManagerAuthorizationRefreshDoesNotOverwriteSwitchedAccount(t *testing.T) {
	expired := expiredTestJWT()
	var manager *Manager
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req == nil || req.URL == nil {
			return nil, errors.New("unexpected http request in test")
		}
		if !strings.Contains(req.URL.Path, "/oauth/token") {
			return nil, errors.New("unexpected http request in test: " + req.URL.String())
		}
		accounts, err := manager.ListAccounts()
		if err != nil {
			return nil, err
		}
		var targetID string
		for _, account := range accounts {
			if account.AuthIDHint == "auth-b" {
				targetID = account.ID
				break
			}
		}
		if targetID == "" {
			return nil, errors.New("missing switch target")
		}
		if _, err := manager.store.SetCurrent(targetID); err != nil {
			return nil, err
		}
		payload, err := json.Marshal(map[string]string{
			"access_token":  "refreshed-access",
			"refresh_token": "refreshed-refresh",
		})
		if err != nil {
			return nil, err
		}
		return staticHTTPResponse(http.StatusOK, "application/json", payload), nil
	})
	manager = newTestManager(t, transport)
	accountA, err := manager.AddCredentialsForTest(credentials{
		AccessToken:  expired,
		RefreshToken: "refresh-a",
		AuthID:       "auth-a",
		Email:        "a@example.test",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	accountB, err := manager.AddCredentialsForTest(credentials{
		AccessToken:  "test-access-b",
		RefreshToken: "test-refresh-b",
		AuthID:       "auth-b",
		Email:        "b@example.test",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.store.SetCurrent(accountA.ID); err != nil {
		t.Fatal(err)
	}

	got, err := manager.Authorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer test-access-b" {
		t.Fatalf("expected current account bearer, got %q", got)
	}
	if manager.CurrentAccountID() != accountB.ID {
		t.Fatal("current account should remain the switched account")
	}
	current, currentID, err := manager.store.LoadCurrentCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if currentID != accountB.ID || current.AccessToken != "test-access-b" {
		t.Fatal("stale refresh overwrote the newly selected account")
	}

	manager.store.mu.Lock()
	previous, readErr := manager.store.readAccountLocked(accountA.ID)
	manager.store.mu.Unlock()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if previous.AccessToken == "refreshed-access" {
		t.Fatal("discarded refresh was persisted onto the previous account")
	}
}

func TestManagerUpdateTagsNormalizesAndRejectsOverflow(t *testing.T) {
	manager := newTestManager(t, nil)
	summary, err := manager.AddCredentialsForTest(testCredential("auth-a", "a@example.test"), true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := manager.UpdateTags(summary.ID, []string{"  Alpha ", "alpha", "Beta"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "Alpha" || got.Tags[1] != "Beta" {
		t.Fatalf("unexpected tags: %#v", got.Tags)
	}
	if _, err := manager.UpdateTags(summary.ID, []string{strings.Repeat("你", 33)}); err == nil {
		t.Fatal("expected overlong tag rejection")
	}
}

func TestManagerDeleteCurrentRequiresReplacementOrClear(t *testing.T) {
	manager := newTestManager(t, nil)
	current, err := manager.AddCredentialsForTest(testCredential("auth-a", "a@example.test"), true)
	if err != nil {
		t.Fatal(err)
	}
	other, err := manager.AddCredentialsForTest(testCredential("auth-b", "b@example.test"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(CursorAccountDeleteRequest{AccountIDs: []string{current.ID}}); err == nil {
		t.Fatal("expected delete of current to require replacement or clear")
	}
	if err := manager.Delete(CursorAccountDeleteRequest{
		AccountIDs:    []string{current.ID},
		ReplacementID: other.ID,
		ClearCurrent:  true,
	}); err == nil {
		t.Fatal("expected mutually exclusive replacement and clear")
	}
	if err := manager.Delete(CursorAccountDeleteRequest{
		AccountIDs:    []string{current.ID},
		ReplacementID: other.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if manager.CurrentAccountID() != other.ID {
		t.Fatal("expected replacement to become current")
	}
}

func TestManagerRecoveryExportWritesFileWithoutReturningTokens(t *testing.T) {
	manager := newTestManager(t, nil)
	summary, err := manager.AddCredentialsForTest(testCredential("auth-a", "a@example.test"), true)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "recovery.json")
	prepared, err := manager.PrepareRecoveryExport(CursorAccountRecoveryExportRequest{
		AccountIDs: []string{summary.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ConfirmationToken == "" {
		t.Fatal("expected non-empty confirmation token")
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("prepare must not create the destination file")
	}
	encodedPrep, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encodedPrep, []byte("test-access")) || bytes.Contains(encodedPrep, []byte("test-refresh")) {
		t.Fatal("prepare DTO leaked account tokens")
	}
	hasImpact := false
	for _, code := range prepared.ImpactCodes {
		if code == "credential_file_created" {
			hasImpact = true
		}
	}
	if !hasImpact {
		t.Fatal("expected credential_file_created impact")
	}

	result, err := manager.ExecuteRecoveryExport(prepared.ConfirmationToken, dest)
	if err != nil {
		t.Fatal(err)
	}
	encodedRes, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encodedRes, []byte("test-access")) || bytes.Contains(encodedRes, []byte("test-refresh")) {
		t.Fatal("execute result leaked account tokens")
	}
	if result.ExportedCount != 1 || result.State != "succeeded" {
		t.Fatalf("unexpected result: %#v", result)
	}
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("test-access")) {
		t.Fatal("expected recovery file to contain credentials")
	}

	dest2 := filepath.Join(t.TempDir(), "recovery-again.json")
	if _, err := manager.ExecuteRecoveryExport(prepared.ConfirmationToken, dest2); err == nil {
		t.Fatal("expected one-time confirmation rejection")
	}
}
