// Package processutil 提供跨平台的外部进程辅助能力。
// 目前仅提供 HideWindow：确保通过 os/exec 启动的控制台命令不会弹出可见窗口
// （Windows 上任务栏/桌面闪窗），非 Windows 平台为空操作。
package processutil

import (
	"os/exec"
	"runtime"
)

// HideWindow 让 cmd 启动的进程不显示控制台窗口。
// Windows 通过 CREATE_NO_WINDOW（0x08000000）实现，并隐藏新进程组；
// 其他平台无窗口概念，为空操作。
func HideWindow(cmd *exec.Cmd) {
	if cmd == nil || runtime.GOOS != "windows" {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = newSysProcAttrHideWindow()
	} else if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		cmd.SysProcAttr.CreationFlags |= createNoWindow
	}
}
