//go:build windows

package bridge

import (
	"fmt"
	"os/exec"
	"strings"
)

// killCursorProcesses 结束所有 Cursor 编辑器进程（含其子进程）。
// Windows 用 taskkill /F /IM Cursor.exe /T：/F 强制结束、/T 连带子进程树。
// 即使部分进程不在（无匹配），taskkill 仍可能返回非零退出码，这里只关心
// 是否真的「无法结束」，因此按输出内容判断而非仅凭退出码。
func killCursorProcesses() error {
	cmd := exec.Command("taskkill", "/F", "/IM", "Cursor.exe", "/T")
	out, err := cmd.CombinedOutput()
	// taskkill 在「没有匹配进程」时也会返回错误码，这不算失败。
	if err != nil {
		lower := strings.ToLower(string(out))
		// 这些文案表示「没有找到对应进程」，视为已无可结束的进程。
		if strings.Contains(lower, "not found") ||
			strings.Contains(lower, "no tasks") ||
			strings.Contains(lower, "找不到") ||
			strings.Contains(lower, "没有") {
			return nil
		}
		return fmt.Errorf("taskkill 失败: %w, output: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
