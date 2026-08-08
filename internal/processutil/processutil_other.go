//go:build !windows

package processutil

import "os/exec"

// newSysProcAttrHideWindow 在非 Windows 平台返回 nil（无窗口概念，无需特殊属性）。
// HideWindow 对 nil 的 SysProcAttr 不做赋值，保持调用方原有行为。
func newSysProcAttrHideWindow() *exec.SysProcAttr {
	return nil
}
