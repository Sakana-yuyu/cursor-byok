//go:build windows

package processutil

import "syscall"

// createNoWindow 是 Windows CreateProcess 的 CREATE_NO_WINDOW 标志。
const createNoWindow = 0x08000000

// newSysProcAttrHideWindow 构造带 CREATE_NO_WINDOW 标志的进程属性。
func newSysProcAttrHideWindow() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
