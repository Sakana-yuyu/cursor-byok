//go:build windows

package bridge

import (
	"os/exec"
	"syscall"
)

const cursorCreateNoWindow uint32 = 0x08000000

func configureCursorCommand(command *exec.Cmd) {
	if command == nil {
		return
	}
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: cursorCreateNoWindow,
	}
}
