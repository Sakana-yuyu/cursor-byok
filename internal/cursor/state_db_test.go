package cursor

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"cursor/internal/logger"

	_ "modernc.org/sqlite"
)

// newTestCursorStateDB 在临时 APPDATA 下创建 Cursor state.vscdb，并写入初始键值。
func newTestCursorStateDB(t *testing.T, initial map[string]string) string {
	t.Helper()
	// 先固定 logger 到真实日志目录：logger.Infof 会延迟初始化文件句柄，
	// 若发生在 USERPROFILE 已被改到临时目录之后，句柄将占用 TempDir 导致清理失败。
	logger.Init()
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)
	t.Setenv("USERPROFILE", t.TempDir())

	statePath := filepath.Join(appData, "Cursor", "User", "globalStorage", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for key, value := range initial {
		if _, err := db.Exec("INSERT OR REPLACE INTO ItemTable(key, value) VALUES(?, ?)", key, value); err != nil {
			t.Fatalf("insert %s: %v", key, err)
		}
	}
	return statePath
}

func readCursorAuthValue(t *testing.T, statePath, key string) (string, bool) {
	t.Helper()
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var raw []byte
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", key).Scan(&raw)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatalf("query %s: %v", key, err)
	}
	return string(raw), true
}

func TestIsInjectedCursorAuthValue(t *testing.T) {
	cases := []struct {
		key, value, token, email string
		want                     bool
	}{
		{"cursorAuth/accessToken", "fake-token", "fake-token", "x@x.com", true},
		{"cursorAuth/accessToken", "official-token", "fake-token", "x@x.com", false},
		{"cursorAuth/refreshToken", "fake-token", "fake-token", "x@x.com", true},
		{"cursorAuth/cachedEmail", "cursor@ai.com", "t", "cursor@ai.com", true},
		{"cursorAuth/cachedEmail", "user@real.com", "t", "cursor@ai.com", false},
		{"cursorAuth/stripeMembershipType", "ultra", "t", "x", false},
	}
	for _, tc := range cases {
		if got := isInjectedCursorAuthValue(tc.key, tc.value, tc.token, tc.email); got != tc.want {
			t.Fatalf("isInjectedCursorAuthValue(%q,%q): got %v, want %v", tc.key, tc.value, got, tc.want)
		}
	}
}

func TestBackupAndRestoreCursorAuthRoundTrip(t *testing.T) {
	statePath := newTestCursorStateDB(t, map[string]string{
		"cursorAuth/accessToken":              "official-access",
		"cursorAuth/refreshToken":             "official-refresh",
		"cursorAuth/cachedEmail":              "user@real.com",
		"cursorAuth/cachedSignUpType":         "Email",
		"cursorAuth/stripeMembershipType":     "pro",
		"cursorAuth/stripeSubscriptionStatus": "active",
	})

	// 注入前备份官方值。
	if err := backupCursorAuthState(statePath, "fake-token", "cursor@ai.com"); err != nil {
		t.Fatalf("backup: %v", err)
	}
	backupData, err := os.ReadFile(cursorAuthBackupPath())
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	for _, want := range []string{"official-access", "official-refresh", "user@real.com"} {
		if !contains(string(backupData), want) {
			t.Fatalf("备份缺少官方值 %q: %s", want, string(backupData))
		}
	}

	// 注入模拟账号。
	if err := syncCursorAuthStateDB(statePath, buildCursorAuthStateValues("cursor@ai.com", "fake-token")); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if got, _ := readCursorAuthValue(t, statePath, "cursorAuth/accessToken"); got != "fake-token" {
		t.Fatalf("注入后 accessToken: got %q, want fake-token", got)
	}

	// 恢复官方账号。
	if err := RestoreCursorUserInfo(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for key, want := range map[string]string{
		"cursorAuth/accessToken":  "official-access",
		"cursorAuth/refreshToken": "official-refresh",
		"cursorAuth/cachedEmail":  "user@real.com",
	} {
		got, ok := readCursorAuthValue(t, statePath, key)
		if !ok || got != want {
			t.Fatalf("恢复后 %s: got %q (ok=%v), want %q", key, got, ok, want)
		}
	}
	// 注入的展示/状态键应被删除（交由 Cursor 重新生成）。
	for _, key := range []string{"cursorAuth/cachedSignUpType", "cursorAuth/stripeMembershipType", "cursorAuth/stripeSubscriptionStatus"} {
		if _, ok := readCursorAuthValue(t, statePath, key); ok {
			t.Fatalf("恢复后 %s 应被删除", key)
		}
	}
}

func TestRestoreWithoutBackupClearsInjectedAuth(t *testing.T) {
	statePath := newTestCursorStateDB(t, map[string]string{
		"cursorAuth/accessToken":          "fake-token",
		"cursorAuth/refreshToken":         "fake-token",
		"cursorAuth/cachedEmail":          "cursor@ai.com",
		"cursorAuth/stripeMembershipType": "ultra",
	})
	// 无备份文件（模拟旧版本已污染、从未备份过）。
	if _, err := os.Stat(cursorAuthBackupPath()); !os.IsNotExist(err) {
		t.Fatalf("测试前置条件：备份文件不应存在")
	}
	if err := RestoreCursorUserInfo(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for _, key := range []string{
		"cursorAuth/accessToken", "cursorAuth/refreshToken", "cursorAuth/cachedEmail",
		"cursorAuth/cachedSignUpType", "cursorAuth/stripeMembershipType", "cursorAuth/stripeSubscriptionStatus",
	} {
		if _, ok := readCursorAuthValue(t, statePath, key); ok {
			t.Fatalf("无备份恢复后 %s 应被删除（回到未登录态）", key)
		}
	}
}

func TestBackupReplacesNullBackupWhenOfficialValuePresent(t *testing.T) {
	statePath := newTestCursorStateDB(t, map[string]string{
		"cursorAuth/accessToken": "official-access",
		"cursorAuth/refreshToken": "official-refresh",
		"cursorAuth/cachedEmail":  "user@real.com",
	})
	// 模拟旧版本留下的全 null 备份（当时 state.vscdb 还是注入态）。
	if err := os.MkdirAll(filepath.Dir(cursorAuthBackupPath()), 0o755); err != nil {
		t.Fatalf("mkdir backup dir: %v", err)
	}
	if err := os.WriteFile(cursorAuthBackupPath(), []byte(`{"cursorAuth/accessToken":null,"cursorAuth/refreshToken":null,"cursorAuth/cachedEmail":null}`), 0o600); err != nil {
		t.Fatalf("write null backup: %v", err)
	}

	// 用户重新登录官方后再次注入：全 null 备份应被官方值覆盖。
	if err := backupCursorAuthState(statePath, "fake-token", "cursor@ai.com"); err != nil {
		t.Fatalf("backup: %v", err)
	}
	data, err := os.ReadFile(cursorAuthBackupPath())
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	for _, want := range []string{"official-access", "official-refresh", "user@real.com"} {
		if !contains(string(data), want) {
			t.Fatalf("备份应包含官方值 %q（全 null 备份被覆盖）: %s", want, string(data))
		}
	}

	// 再次调用不应覆盖有效备份。
	if err := backupCursorAuthState(statePath, "fake-token", "cursor@ai.com"); err != nil {
		t.Fatalf("second backup: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestBackupRefreshesWhenOfficialAccountChanged 覆盖「有效备份 + 官方账号已变更」：
// 用户恢复账号 A 后重新登录官方账号 B，再次启动本地服务时备份应刷新为 B，
// 否则停止服务会写回旧账号 A。
func TestBackupRefreshesWhenOfficialAccountChanged(t *testing.T) {
	statePath := newTestCursorStateDB(t, map[string]string{
		"cursorAuth/accessToken":  "official-A",
		"cursorAuth/refreshToken": "official-refresh-A",
		"cursorAuth/cachedEmail":  "a@real.com",
	})
	// 首次注入前备份官方 A。
	if err := backupCursorAuthState(statePath, "fake-token", "cursor@ai.com"); err != nil {
		t.Fatalf("first backup: %v", err)
	}

	// 用户重新登录官方账号 B（state.vscdb 被 Cursor 改写）。
	if err := syncCursorAuthStateDB(statePath, map[string]string{
		"cursorAuth/accessToken":  "official-B",
		"cursorAuth/refreshToken": "official-refresh-B",
		"cursorAuth/cachedEmail":  "b@real.com",
	}); err != nil {
		t.Fatalf("relogin as B: %v", err)
	}

	// 再次启动本地服务：有效备份存在但账号已变更，应刷新为 B。
	if err := backupCursorAuthState(statePath, "fake-token", "cursor@ai.com"); err != nil {
		t.Fatalf("second backup: %v", err)
	}
	data, err := os.ReadFile(cursorAuthBackupPath())
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	for _, want := range []string{"official-B", "official-refresh-B", "b@real.com"} {
		if !contains(string(data), want) {
			t.Fatalf("刷新后备份应包含 %q: %s", want, string(data))
		}
	}
	if contains(string(data), "official-A") {
		t.Fatalf("刷新后备份不应残留旧账号 A: %s", string(data))
	}

	// 恢复应回到官方 B。
	if err := RestoreCursorUserInfo(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got, _ := readCursorAuthValue(t, statePath, "cursorAuth/accessToken"); got != "official-B" {
		t.Fatalf("恢复后 accessToken: got %q, want official-B", got)
	}
}

// TestBackupKeepsValidWhenStateStillInjected 覆盖「有效备份 + state.vscdb 仍是注入值」：
// 服务未停止/恢复过、注入值残留在库中时，重启服务不应把备份刷新成注入值/空值。
func TestBackupKeepsValidWhenStateStillInjected(t *testing.T) {
	statePath := newTestCursorStateDB(t, map[string]string{
		"cursorAuth/accessToken":  "official-A",
		"cursorAuth/refreshToken": "official-refresh-A",
		"cursorAuth/cachedEmail":  "a@real.com",
	})
	if err := backupCursorAuthState(statePath, "fake-token", "cursor@ai.com"); err != nil {
		t.Fatalf("first backup: %v", err)
	}

	// 注入模拟账号（模拟服务运行中状态库被改写）。
	if err := syncCursorAuthStateDB(statePath, buildCursorAuthStateValues("cursor@ai.com", "fake-token")); err != nil {
		t.Fatalf("inject: %v", err)
	}
	// 服务重启再次备份：当前为注入值，不算账号变更，备份应保留官方 A。
	if err := backupCursorAuthState(statePath, "fake-token", "cursor@ai.com"); err != nil {
		t.Fatalf("second backup: %v", err)
	}
	data, err := os.ReadFile(cursorAuthBackupPath())
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	for _, want := range []string{"official-A", "official-refresh-A", "a@real.com"} {
		if !contains(string(data), want) {
			t.Fatalf("注入态下备份应保留官方 A（含 %q）: %s", want, string(data))
		}
	}
	if contains(string(data), "fake-token") {
		t.Fatalf("注入态下备份不应写入模拟 token: %s", string(data))
	}
}

// TestRestoreCursorUserInfoPropagatesDBError 覆盖恢复失败必须报错而非静默成功：
// state.vscdb 不可打开（目录占位）时 RestoreCursorUserInfo 应返回错误，
// 供 ClearCursorSettings/切直连路径向上传播。
func TestRestoreCursorUserInfoPropagatesDBError(t *testing.T) {
	logger.Init()
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)
	t.Setenv("USERPROFILE", t.TempDir())
	statePath := filepath.Join(appData, "Cursor", "User", "globalStorage", "state.vscdb")
	if err := os.MkdirAll(statePath, 0o755); err != nil {
		t.Fatalf("mkdir state.vscdb dir: %v", err)
	}
	// 存在有效备份 → 恢复会尝试写回，SQLite 打开目录失败。
	if err := os.MkdirAll(filepath.Dir(cursorAuthBackupPath()), 0o755); err != nil {
		t.Fatalf("mkdir backup dir: %v", err)
	}
	if err := os.WriteFile(cursorAuthBackupPath(), []byte(`{"cursorAuth/accessToken":"official-A","cursorAuth/refreshToken":"r","cursorAuth/cachedEmail":"a@real.com"}`), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	if err := RestoreCursorUserInfo(); err == nil {
		t.Fatal("state.vscdb 不可打开时 RestoreCursorUserInfo 应返回错误")
	}
}
