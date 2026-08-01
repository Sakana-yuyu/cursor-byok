//go:build !windows

package bridge

import "os/exec"

// findCursorExecutableOnPath 非 Windows 平台无需解析 cmd 启动脚本。
func findCursorExecutableOnPath() string { return "" }

// launchCursorProcess 启动 Cursor 编辑器（非 Windows 平台走原生 exec.Command）。
func launchCursorProcess(path, workspaceDir string) error {
	var cmd *exec.Cmd
	if workspaceDir != "" {
		cmd = exec.Command(path, workspaceDir)
	} else {
		cmd = exec.Command(path)
	}
	return cmd.Start()
}
