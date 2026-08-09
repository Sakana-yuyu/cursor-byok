//go:build windows

package processutil

import (
	"os/exec"
	"syscall"
)

// createNoWindow 是 Windows CreateProcess 的 CREATE_NO_WINDOW 标志。
const createNoWindow = 0x08000000

// HideWindow prevents console windows from flashing when starting commands.
func HideWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
