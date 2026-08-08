//go:build windows

package forwarder

import "syscall"

// createNoWindow 是 Windows CreateProcess 的 CREATE_NO_WINDOW 标志（0x08000000）。
// 仅 HideWindow 不足以阻止控制台子系统子进程弹窗：当父进程没有控制台时，
// Windows 仍会为新 console 子进程分配并短暂显示一个窗口。补 CREATE_NO_WINDOW
// 可彻底抑制（与 internal/processutil 一致）。
const createNoWindow = 0x08000000

// hiddenWindowAttr 返回隐藏控制台窗口的进程属性，避免 MCP stdio server（如 python.exe）启动时弹出黑色命令行窗口。
func hiddenWindowAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
