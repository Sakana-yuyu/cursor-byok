//go:build !windows

package forwarder

import "syscall"

// hiddenWindowAttr 在非 Windows 平台无控制台窗口概念，返回 nil 表示不设置。
func hiddenWindowAttr() *syscall.SysProcAttr {
	return nil
}