package appdata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realUserHomeDir 直接询问操作系统，绕开 appdata 自身的解析逻辑，
// 用来判断某个路径是否落在开发者的真实主目录里。
func realUserHomeDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		t.Skip("无法确定真实用户主目录，跳过")
	}
	return filepath.Clean(strings.TrimSpace(home))
}

func TestRootDirHonorsHomeOverrideEnv(t *testing.T) {
	override := t.TempDir()
	t.Setenv(RootDirEnvVar, override)

	if got, want := RootDir(), filepath.Join(override, appDirName); got != want {
		t.Fatalf("RootDir() = %q, want %q", got, want)
	}
	if got, want := legacyRootDir(), filepath.Join(override, legacyAppDirName); got != want {
		t.Fatalf("legacyRootDir() = %q, want %q", got, want)
	}
}

// TestRootDirUnderTestNeverResolvesInsideRealUserHome 是本次 P0 的回归护栏：
// 测试进程一旦解析出真实用户数据目录，任何写入都会污染开发者的真实会话历史。
func TestRootDirUnderTestNeverResolvesInsideRealUserHome(t *testing.T) {
	realHome := realUserHomeDir(t)
	productionRoot := filepath.Join(realHome, appDirName)

	for name, got := range map[string]string{
		"RootDir":         RootDir(),
		"HistoryRootPath": HistoryRootPath(),
		"ConfigFilePath":  ConfigFilePath(),
		"LogsRootPath":    LogsRootPath(),
		"DataRootPath":    DataRootPath(),
		"legacyRootDir":   legacyRootDir(),
	} {
		if isInside(productionRoot, got) || isInside(filepath.Join(realHome, legacyAppDirName), got) {
			t.Fatalf("%s() = %q，落在真实用户数据目录 %q 内；测试进程绝不允许解析到真实 appdata 根目录",
				name, got, realHome)
		}
	}
}

// TestSandboxRespectsTestsThatAlreadyIsolatedHome 覆盖护栏的放行分支：
// 仓库里已有用例通过 t.Setenv("USERPROFILE"/"HOME") 自行隔离，护栏不得再次改写，
// 否则这些用例写进临时目录的配置将永远读不回来。
func TestSandboxRespectsTestsThatAlreadyIsolatedHome(t *testing.T) {
	isolated := t.TempDir()
	if got := sandboxHomeIfUnderTest(isolated); got != isolated {
		t.Fatalf("sandboxHomeIfUnderTest(%q) = %q, want 原样返回", isolated, got)
	}
}

// TestSandboxRedirectsRealHomeUnderTest 覆盖护栏的拦截分支。
func TestSandboxRedirectsRealHomeUnderTest(t *testing.T) {
	realHome := realUserHomeDir(t)
	got := sandboxHomeIfUnderTest(realHome)
	if got == realHome {
		t.Fatalf("sandboxHomeIfUnderTest(真实主目录) 未被重定向，仍为 %q", got)
	}
	if !strings.Contains(filepath.Base(got), testSandboxHomePrefix) {
		t.Fatalf("重定向目标 %q 不是测试沙箱目录", got)
	}
}

func isInside(root string, candidate string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}
