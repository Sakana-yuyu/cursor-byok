package cursoraccount

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
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
	credPath := filepath.Join(dir, "cursor-account.json")

	t.Run("imports_real_values", func(t *testing.T) {
		writeBackupFile(t, backupPath, map[string]any{
			"cursorAuth/accessToken":  "access-token-123",
			"cursorAuth/refreshToken": "refresh-token-456",
			"cursorAuth/cachedEmail":  "user@example.com",
		})
		manager := NewManager(credPath, nil)
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
		reloaded := NewManager(credPath, nil)
		if reloaded.Status().State != StateSignedIn {
			t.Fatal("expected persisted credentials to reload as signed_in")
		}
	})

	t.Run("null_values_do_not_import", func(t *testing.T) {
		credPath2 := filepath.Join(dir, "cursor-account-null.json")
		writeBackupFile(t, backupPath, map[string]any{
			"cursorAuth/accessToken":  nil,
			"cursorAuth/refreshToken": nil,
			"cursorAuth/cachedEmail":  nil,
		})
		manager := NewManager(credPath2, nil)
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
		credPath3 := filepath.Join(dir, "cursor-account-missing.json")
		manager := NewManager(credPath3, nil)
		imported, err := manager.ImportFromCursorBackup(filepath.Join(dir, "does-not-exist.json"))
		if err != nil {
			t.Fatal(err)
		}
		if imported {
			t.Fatal("expected imported=false for missing backup")
		}
	})

	t.Run("already_signed_in_not_overwritten", func(t *testing.T) {
		credPath4 := filepath.Join(dir, "cursor-account-existing.json")
		manager := NewManager(credPath4, nil)
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
		credPath5 := filepath.Join(dir, "cursor-account-corrupt.json")
		if err := os.WriteFile(backupPath, []byte("{not-json"), 0o600); err != nil {
			t.Fatal(err)
		}
		manager := NewManager(credPath5, nil)
		if _, err := manager.ImportFromCursorBackup(backupPath); err == nil {
			t.Fatal("expected error for corrupt backup")
		}
	})

	t.Run("save_failure_rolls_back_state", func(t *testing.T) {
		// path 指向一个已存在的目录：temp 写入成功后 rename 覆盖目录必失败。
		dirAsPath := filepath.Join(dir, "a-directory")
		if err := os.MkdirAll(dirAsPath, 0o700); err != nil {
			t.Fatal(err)
		}
		writeBackupFile(t, backupPath, map[string]any{
			"cursorAuth/accessToken": "access-token-rollback",
			"cursorAuth/cachedEmail": "rollback@example.com",
		})
		manager := NewManager(dirAsPath, nil)
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
		credPath7 := filepath.Join(dir, "cursor-account-waiting.json")
		writeBackupFile(t, backupPath, map[string]any{
			"cursorAuth/accessToken":  "imported-token",
			"cursorAuth/refreshToken": "imported-refresh",
			"cursorAuth/cachedEmail":  "imported@example.com",
		})
		manager := NewManager(credPath7, nil)
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
		credPath8 := filepath.Join(dir, "cursor-account-marker.json")
		markerPath := filepath.Join(dir, "auto-import-off.marker")
		writeBackupFile(t, backupPath, map[string]any{
			"cursorAuth/accessToken": "access-token-marker",
			"cursorAuth/cachedEmail": "marker@example.com",
		})
		manager := NewManager(credPath8, nil)
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
		credPath9 := filepath.Join(dir, "cursor-account-clear-marker.json")
		markerPath := filepath.Join(dir, "auto-import-off-clear.marker")
		manager := NewManager(credPath9, nil)
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
