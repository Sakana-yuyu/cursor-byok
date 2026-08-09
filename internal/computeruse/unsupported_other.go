//go:build !windows

// 非平台文件：ComputerUse 本地执行目前仅支持 Windows。其余平台返回「不支持」，
// 保证包可编译、导入安全（forwarder 注入点会在调用前检查运行平台）。
package computeruse

import "fmt"

var errDesktopUnsupported = fmt.Errorf("ComputerUse 本地执行仅支持 Windows")

func ScreenSize() (int, int, error)                       { return 0, 0, errDesktopUnsupported }
func CaptureScreen(displayIdx int) ([]byte, error)        { return nil, errDesktopUnsupported }
func CaptureRect(x, y, width, height int) ([]byte, error) { return nil, errDesktopUnsupported }
func MouseMove(x, y int) error                            { return errDesktopUnsupported }
func CursorPosition() (int, int, error)                   { return 0, 0, errDesktopUnsupported }
func MouseClick(x, y int, button string, count int) error { return errDesktopUnsupported }
func MouseDown(button string) error                       { return errDesktopUnsupported }
func MouseUp(button string) error                         { return errDesktopUnsupported }
func Scroll(x, y int, direction string, amount int) error { return errDesktopUnsupported }
func KeyPress(key string) error                           { return errDesktopUnsupported }
func KeyPressHold(key string, durationMs int) error       { return errDesktopUnsupported }
func KeyType(text string) error                           { return errDesktopUnsupported }
func sleepBrief()                                         {}
