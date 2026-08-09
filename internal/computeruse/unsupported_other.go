//go:build !windows

// 非平台文件：ComputerUse 本地执行目前仅支持 Windows。其余平台返回「不支持」，
// 保证包可编译、导入安全（forwarder 注入点会在调用前检查运行平台）。
package computeruse

import "fmt"

func ScreenSize() (int, int, error)            { return 0, 0, fmt.Errorf("ComputerUse 本地执行仅支持 Windows") }
func CaptureScreen(displayIdx int) ([]byte, error) { return nil, fmt.Errorf("ComputerUse 本地执行仅支持 Windows") }
func CaptureRect(x, y, width, height int) ([]byte, error) { return nil, fmt.Errorf("ComputerUse 本地执行仅支持 Windows") }
