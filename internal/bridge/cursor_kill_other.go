//go:build !windows

package bridge

import (
	"fmt"
	"os/exec"
	"runtime"
)

// killCursorProcesses 结束所有 Cursor 编辑器进程。
// darwin 进程名为 Cursor，linux 为 cursor；pkill -x 精确匹配进程名。
// 「没有匹配进程」时 pkill 返回 1，视为正常（无可结束的进程）。
func killCursorProcesses() error {
	name := "cursor"
	if runtime.GOOS == "darwin" {
		name = "Cursor"
	}
	// pkill 退出码：0=有进程被结束，1=无匹配，2=语法错误，3=致命错误。
	out, err := exec.Command("pkill", "-x", name).CombinedOutput()
	if err != nil && len(out) > 0 {
		return fmt.Errorf("pkill 失败: %w, output: %s", err, string(out))
	}
	// 退出码 1（无匹配进程）不当作错误。
	return nil
}
