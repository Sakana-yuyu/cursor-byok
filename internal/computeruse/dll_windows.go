//go:build windows

package computeruse

import "syscall"

// user32 / gdi32 是 Windows GUI 子系统的核心 DLL，通过 syscall.LazyDLL 延迟加载。
// 集中定义，供 screenshot_windows.go / input_windows.go 共用。
var (
	user32 = syscall.NewLazyDLL("user32.dll")
	gdi32  = syscall.NewLazyDLL("gdi32.dll")
)
