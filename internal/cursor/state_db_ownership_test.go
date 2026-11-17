package cursor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInjectCursorUserInfoRequiresRecoverableBackup(t *testing.T) {
	statePath := newTestCursorStateDB(t, map[string]string{
		"cursorAuth/accessToken":  "official-access",
		"cursorAuth/refreshToken": "official-refresh",
		"cursorAuth/cachedEmail":  "user@example.test",
	})
	if err := os.MkdirAll(cursorAuthBackupPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := InjectCursorUserInfo("local@example.test", "local-token"); err == nil {
		t.Fatal("injection succeeded without a writable backup")
	}
	if got, _ := readCursorAuthValue(t, statePath, "cursorAuth/accessToken"); got != "official-access" {
		t.Fatal("backup failure changed the original account")
	}
}

func TestRestoreInjectedCursorUserInfoPreservesUnownedAccount(t *testing.T) {
	statePath := newTestCursorStateDB(t, map[string]string{
		"cursorAuth/accessToken":  "official-access",
		"cursorAuth/refreshToken": "official-refresh",
		"cursorAuth/cachedEmail":  "user@example.test",
	})
	if err := RestoreInjectedCursorUserInfo("local-token"); err != nil {
		t.Fatal(err)
	}
	if got, _ := readCursorAuthValue(t, statePath, "cursorAuth/accessToken"); got != "official-access" {
		t.Fatal("cleanup without injection changed the original account")
	}
	if err := InjectCursorUserInfo("local@example.test", "local-token"); err != nil {
		t.Fatal(err)
	}
	if err := RestoreInjectedCursorUserInfo("local-token"); err != nil {
		t.Fatal(err)
	}
	if got, _ := readCursorAuthValue(t, statePath, "cursorAuth/accessToken"); got != "official-access" {
		t.Fatal("cleanup did not restore the original account")
	}
	if err := syncCursorAuthStateDB(statePath, buildCursorAuthStateValues("new@example.test", "new-official")); err != nil {
		t.Fatal(err)
	}
	if err := RestoreInjectedCursorUserInfo("local-token"); err != nil {
		t.Fatal(err)
	}
	if got, _ := readCursorAuthValue(t, statePath, "cursorAuth/accessToken"); got != "new-official" {
		t.Fatal("cleanup overwrote a later sign-in")
	}
}

func TestInjectCursorUserInfoBootstrapsEmptyState(t *testing.T) {
	statePath := testCursorStateDBPath(t)
	if err := InjectCursorUserInfo("local@example.test", "local-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cursorAuthBackupPath()); err != nil {
		t.Fatal(err)
	}
	if err := RestoreInjectedCursorUserInfo("local-token"); err != nil {
		t.Fatal(err)
	}
	if _, present := readCursorAuthValue(t, statePath, "cursorAuth/accessToken"); present {
		t.Fatal("restoration retained the injected token")
	}
}

func TestRestoreInjectedCursorUserInfoAllowsEmptyHostState(t *testing.T) {
	statePath := testCursorStateDBPath(t)
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreInjectedCursorUserInfo("local-token"); err != nil {
		t.Fatal(err)
	}
}
