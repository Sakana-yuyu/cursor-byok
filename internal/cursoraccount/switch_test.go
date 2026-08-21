package cursoraccount

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

type scriptedCursorRuntime struct {
	running  bool
	stopErr  error
	startErr error
	stops    int
	starts   int
}

func (r *scriptedCursorRuntime) Running() bool { return r.running }

func (r *scriptedCursorRuntime) Stop(context.Context) error {
	r.stops++
	r.running = false
	return r.stopErr
}

func (r *scriptedCursorRuntime) Start(context.Context) error {
	r.starts++
	if r.startErr != nil {
		return r.startErr
	}
	r.running = true
	return nil
}

func newSwitcherStateDB(t *testing.T, initial map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.vscdb")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)"); err != nil {
		t.Fatal(err)
	}
	for key, value := range initial {
		if _, err := db.Exec("INSERT OR REPLACE INTO ItemTable(key, value) VALUES(?, ?)", key, value); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func readSwitcherAuthValue(t *testing.T, dbPath, key string) (string, bool) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw []byte
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), true
}

func newTestSwitcher(t *testing.T, runtime *scriptedCursorRuntime) (*Manager, string, string) {
	t.Helper()
	root := t.TempDir()
	manager := NewManager(root, filepath.Join(root, "cursor-account.json"), nil)
	summary, err := manager.AddCredentialsForTest(credentials{
		AccessToken:  "test-access-switch",
		RefreshToken: "test-refresh-switch",
		AuthID:       "auth-b",
		Email:        "b@example.test",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddCredentialsForTest(credentials{
		AccessToken:  "test-access-current",
		RefreshToken: "test-refresh-current",
		AuthID:       "auth-a",
		Email:        "a@example.test",
	}, true); err != nil {
		t.Fatal(err)
	}
	dbPath := newSwitcherStateDB(t, map[string]string{
		"cursorAuth/accessToken": "old-client",
		"cursorAuth/cachedEmail": "old@example.test",
		"workbench.colorTheme":   "dark",
	})
	manager.cursorRuntime = runtime
	manager.stateDBPath = func() (string, error) { return dbPath, nil }
	return manager, summary.ID, dbPath
}

func TestSwitchRestoresBackupWhenRestartFails(t *testing.T) {
	runtime := &scriptedCursorRuntime{running: true, startErr: errors.New("start failed")}
	manager, targetID, dbPath := newTestSwitcher(t, runtime)
	beforeAccess, _ := readSwitcherAuthValue(t, dbPath, "cursorAuth/accessToken")
	beforeCurrent := manager.CurrentAccountID()

	prepared, err := manager.PrepareCursorClientAccountSwitch(targetID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.ExecuteCursorClientAccountSwitch(prepared.ConfirmationToken)
	if err == nil {
		t.Fatal("expected restart failure")
	}
	afterAccess, _ := readSwitcherAuthValue(t, dbPath, "cursorAuth/accessToken")
	if afterAccess != beforeAccess {
		t.Fatal("state database was not restored")
	}
	if manager.CurrentAccountID() != beforeCurrent {
		t.Fatal("current pointer was not restored")
	}
	if got, _ := readSwitcherAuthValue(t, dbPath, "workbench.colorTheme"); got != "dark" {
		t.Fatal("unrelated key changed during failed switch")
	}
}

func TestClientSwitchResultOmitsTokens(t *testing.T) {
	runtime := &scriptedCursorRuntime{}
	manager, targetID, _ := newTestSwitcher(t, runtime)
	prepared, err := manager.PrepareCursorClientAccountSwitch(targetID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.ExecuteCursorClientAccountSwitch(prepared.ConfirmationToken)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("test-access-switch")) || bytes.Contains(encoded, []byte("test-refresh-switch")) {
		t.Fatal("token leaked through switch result")
	}
}

func TestPrepareSwitchDoesNotWriteState(t *testing.T) {
	runtime := &scriptedCursorRuntime{running: true}
	manager, targetID, dbPath := newTestSwitcher(t, runtime)
	before, _ := os.ReadFile(dbPath)
	if _, err := manager.PrepareCursorClientAccountSwitch(targetID); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(dbPath)
	if !bytes.Equal(before, after) {
		t.Fatal("prepare wrote the state database")
	}
	if runtime.stops != 0 || runtime.starts != 0 {
		t.Fatal("prepare stopped or started cursor")
	}
}
