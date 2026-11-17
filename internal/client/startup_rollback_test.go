package client

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"cursor/internal/appdata"
	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/cursor"
	"cursor/internal/logger"
)

func TestStartupRollbackRunsReverseAndKeepsAllErrors(t *testing.T) {
	first := errors.New("first cleanup")
	last := errors.New("last cleanup")
	var order []string
	var rollback startupRollback
	for _, item := range []struct {
		name string
		err  error
	}{{"backend", first}, {"auth", nil}, {"proxy", last}} {
		item := item
		rollback.add(item.name, func(ctx context.Context) error {
			if ctx.Err() != nil {
				t.Fatal("cleanup received an exhausted context")
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("cleanup lacks a timeout")
			}
			order = append(order, item.name)
			return item.err
		})
	}
	err := rollback.run()
	if !errors.Is(err, first) || !errors.Is(err, last) {
		t.Fatalf("missing recovery errors: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"proxy", "auth", "backend"}) {
		t.Fatalf("rollback order: %v", order)
	}
	if err := rollback.run(); err != nil || len(order) != 3 {
		t.Fatal("rollback was not consumed")
	}
}

func startupTestAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func startupTestService(t *testing.T, proxyAddr string) (*ProxyService, string, string) {
	t.Helper()
	logger.Init()
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv(appdata.RootDirEnvVar, t.TempDir())
	t.Setenv("APPDATA", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	userDir := filepath.Join(root, "Cursor", "User")
	if runtime.GOOS == "darwin" {
		userDir = filepath.Join(home, "Library", "Application Support", "Cursor", "User")
	}
	statePath := filepath.Join(userDir, "globalStorage", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)"); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"cursorAuth/accessToken":  "original-access",
		"cursorAuth/refreshToken": "original-refresh",
		"cursorAuth/cachedEmail":  "original@example.test",
	} {
		if _, err := db.Exec("INSERT INTO ItemTable(key, value) VALUES(?, ?)", key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store := serverconfig.NewStore(filepath.Join(t.TempDir(), "config.yml"), t.TempDir())
	cfg := serverconfig.DefaultConfig()
	cfg.BackendListenAddr = startupTestAddress(t)
	cfg.ProxyListenAddr = proxyAddr
	cfg.Delegation.Enabled = false
	if _, err := store.Save(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	s := &ProxyService{store: store}
	t.Cleanup(s.ShutdownForQuit)
	return s, filepath.Join(userDir, "settings.json"), statePath
}

func assertStartupOriginalAuth(t *testing.T, statePath string) {
	t.Helper()
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var token string
	if err := db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", "cursorAuth/accessToken").Scan(&token); err != nil {
		t.Fatal(err)
	}
	if token != "original-access" {
		t.Fatal("startup failure/cleanup changed the original account")
	}
}

func TestStartProxyInvalidHostSettingsIsReadOnly(t *testing.T) {
	s, settingsPath, statePath := startupTestService(t, startupTestAddress(t))
	original := []byte("{broken settings")
	if err := os.WriteFile(settingsPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	state, err := s.StartProxy()
	if err == nil || !strings.Contains(err.Error(), "无法解析") {
		t.Fatalf("expected syntax failure: %v", err)
	}
	if state.BackendRunning || state.ProxyRunning || s.currentBackendHost() != nil {
		t.Fatal("syntax failure started a service")
	}
	got, err := os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("syntax failure changed host settings")
	}
	assertStartupOriginalAuth(t, statePath)
}

func TestStartProxyOccupiedPortRestoresAuthAndOnlyStopsNewBackend(t *testing.T) {
	for _, wasRunning := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-backend", true: "existing-backend"}[wasRunning], func(t *testing.T) {
			occupied, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer occupied.Close()
			s, _, statePath := startupTestService(t, occupied.Addr().String())
			if wasRunning {
				if err := s.ensureBackendHost(); err != nil {
					t.Fatal(err)
				}
				if err := s.backendHost.Start(); err != nil {
					t.Fatal(err)
				}
			}
			state, err := s.StartProxy()
			if err == nil {
				t.Fatal("startup succeeded with an occupied proxy port")
			}
			if state.BackendRunning != wasRunning || state.ProxyRunning {
				t.Fatalf("wrong state after rollback: %+v", state)
			}
			assertStartupOriginalAuth(t, statePath)
		})
	}
}

func TestStartProxyBackupFailureStopsNewBackend(t *testing.T) {
	s, _, statePath := startupTestService(t, startupTestAddress(t))
	if err := os.MkdirAll(cursor.CursorAuthBackupPath(), 0700); err != nil {
		t.Fatal(err)
	}
	state, err := s.StartProxy()
	if err == nil || !strings.Contains(err.Error(), "备份 Cursor 登录态失败") {
		t.Fatalf("expected backup failure, got %v", err)
	}
	if state.BackendRunning || state.ProxyRunning {
		t.Fatal("backup failure left a newly started service running")
	}
	assertStartupOriginalAuth(t, statePath)
	// The directory was a fault injection, not a recoverable account journal.
	if err := os.Remove(cursor.CursorAuthBackupPath()); err != nil {
		t.Fatal(err)
	}
}

func TestStopWithoutTakeoverPreservesHostSettingsAndAccount(t *testing.T) {
	s, settingsPath, statePath := startupTestService(t, startupTestAddress(t))
	original := []byte(`{"http.proxy":"http://company:8080","editor.fontSize":16}`)
	if err := os.WriteFile(settingsPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StopProxy(); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearCursorSettings(); err != nil {
		t.Fatal(err)
	}
	s.ShutdownForQuit()
	got, err := os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("cleanup without takeover changed host settings")
	}
	assertStartupOriginalAuth(t, statePath)
}
