package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cursor/internal/appdata"
	serverconfig "cursor/internal/backend/server/config"
)

// TestNewHostNeverBindsRealUserHistoryRoot 复现并守护本次 P0：
// NewHost -> rebuildLocked -> forwarder.NewModuleWithExecutorRegistry(appdata.HistoryRootPath())
// -> newService -> startHistoryMaintenance()，会在后台 goroutine 里遍历 history 根目录并
// 给非终态会话追加 interrupted 记录。若该根目录是开发者的真实主目录，跑测试就会改写真实会话。
func TestNewHostNeverBindsRealUserHistoryRoot(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		t.Skip("无法确定真实用户主目录，跳过")
	}
	realAppRoot := filepath.Join(filepath.Clean(strings.TrimSpace(home)), ".cursor-local-assistant-v2")

	store := serverconfig.NewStore(filepath.Join(t.TempDir(), "config.yaml"), "")
	host, err := NewHost(store, nil)
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	t.Cleanup(func() { _ = host.Stop(context.Background()) })

	historyRoot := appdata.HistoryRootPath()
	rel, relErr := filepath.Rel(realAppRoot, filepath.Clean(historyRoot))
	if relErr == nil && (rel == "." || !strings.HasPrefix(rel, "..")) {
		t.Fatalf("history 根目录解析为 %q，落在真实用户数据目录 %q 内；"+
			"启动期 history 维护 goroutine 会改写真实会话", historyRoot, realAppRoot)
	}
}
