package client

import (
	"os"
	"path/filepath"
	"testing"

	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/logger"
)

// TestSaveUserConfigPropagatesRestoreErrorWhenSwitchingToUpstream 验证切直连
// （routingMode=upstream）时 RestoreCursorUserInfo 失败会向上传播，
// 而不是被 logger 吞掉后仍返回成功（模拟 token 残留将导致官方连接 401）。
func TestSaveUserConfigPropagatesRestoreErrorWhenSwitchingToUpstream(t *testing.T) {
	// 先固定 logger 句柄到真实日志目录：若在 USERPROFILE 被改到临时目录之后再
	// 初始化，句柄会占用 TempDir 导致清理失败（与 state_db_test 相同约束）。
	logger.Init()
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)
	t.Setenv("USERPROFILE", t.TempDir())
	// state.vscdb 用目录占位，使 RestoreCursorUserInfo 的 SQLite 打开失败。
	statePath := filepath.Join(appData, "Cursor", "User", "globalStorage", "state.vscdb")
	if err := os.MkdirAll(statePath, 0o755); err != nil {
		t.Fatalf("mkdir state.vscdb dir: %v", err)
	}
	// 存在有效备份 → 恢复会尝试写回官方值，打开失败即报错。
	// 备份路径由 USERPROFILE 推算（appdata.RootDir/data/cursor-auth-backup.json）。
	backupDir := filepath.Join(os.Getenv("USERPROFILE"), ".cursor-local-assistant-v2", "data")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatalf("mkdir backup dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "cursor-auth-backup.json"), []byte(`{"cursorAuth/accessToken":"official-A","cursorAuth/refreshToken":"r","cursorAuth/cachedEmail":"a@real.com"}`), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	service := &ProxyService{
		store: serverconfig.NewStore(filepath.Join(t.TempDir(), "config.yml"), t.TempDir()),
	}
	cfg := serverconfig.DefaultConfig()
	cfg.Routing.Mode = "upstream"
	if err := service.SaveUserConfig(cfg); err == nil {
		t.Fatal("切直连且官方账号恢复失败时 SaveUserConfig 应返回错误")
	}
}