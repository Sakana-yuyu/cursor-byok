//go:build windows

package forwarder

import "syscall"

// hiddenWindowAttr 返回隐藏控制台窗口的进程属性，避免 MCP stdio server（如 python.exe）启动时弹出黑色命令行窗口。
func hiddenWindowAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}