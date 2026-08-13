package appdata

// 测试隔离护栏。
//
// 这段逻辑必须放在普通源码而非 _test.go 里：_test.go 只对 appdata 包自己的测试
// 生效，而真正污染用户数据的是别的包的测试二进制（例如 internal/backend 的
// NewHost 会把 appdata.HistoryRootPath() 交给启动期 history 维护 goroutine）。
// 放在普通源码里，模块内任何测试二进制都自动获得保护，且不需要每个包各写一遍
// TestMain，也就不存在「新包忘了加」的复发口子。
//
// 这里替换的只是根目录字符串，不改变任何下游分支：mkdir、迁移、加锁、读写全部
// 按生产路径执行，生产覆盖率不受影响。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testSandboxHomePrefix = "cursor-byok-test-home-"
	// 沙箱目录无法在进程退出时自动清理，改为创建时顺手回收过期的历史沙箱。
	testSandboxRetention = 24 * time.Hour
)

// processStartHomeDir 在包初始化时抓取真实用户主目录。必须在这个时机取值：
// 测试可能随后用 t.Setenv 改写 HOME/USERPROFILE，届时再取就分不清
// 「测试已自行隔离」和「测试指向真实目录」这两种情况了。
var processStartHomeDir = detectProcessStartHomeDir()

func detectProcessStartHomeDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	trimmed := strings.TrimSpace(homeDir)
	if trimmed == "" {
		return ""
	}
	return filepath.Clean(trimmed)
}

var (
	testSandboxOnce sync.Once
	testSandboxHome string
)

// sandboxHomeIfUnderTest 在测试二进制中把真实用户主目录换成进程私有的临时目录。
// 若测试已经自己把 HOME/USERPROFILE 指向别处（仓库里已有若干这样的用例），
// 说明它清楚自己在做什么，此处原样放行。
func sandboxHomeIfUnderTest(homeDir string) string {
	if !testing.Testing() {
		return homeDir
	}
	if processStartHomeDir == "" || filepath.Clean(homeDir) != processStartHomeDir {
		return homeDir
	}
	return ensureTestSandboxHome()
}

func ensureTestSandboxHome() string {
	testSandboxOnce.Do(func() {
		sweepStaleTestSandboxHomes()
		dir, err := os.MkdirTemp("", testSandboxHomePrefix)
		if err != nil {
			// MkdirTemp 失败时退化为按 pid 命名，仍然保证进程之间互不干扰。
			dir = filepath.Join(os.TempDir(), fmt.Sprintf("%s%d", testSandboxHomePrefix, os.Getpid()))
			_ = os.MkdirAll(dir, 0o755)
		}
		testSandboxHome = dir
	})
	return testSandboxHome
}

func sweepStaleTestSandboxHomes() {
	tempRoot := os.TempDir()
	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), testSandboxHomePrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < testSandboxRetention {
			continue
		}
		_ = os.RemoveAll(filepath.Join(tempRoot, entry.Name()))
	}
}
