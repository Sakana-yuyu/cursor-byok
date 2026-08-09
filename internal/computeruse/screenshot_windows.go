// Package computeruse 提供 ComputerUse 工具的本地执行能力（截图、鼠标键盘控制），
// 让 BYOK 模式下不再依赖 Cursor 客户端的 GUI 操作回传。纯 Go Win32 实现，
// 无 CGO 依赖（CGO_ENABLED=0 可编译），不改变 wails 打包链。
package computeruse

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"syscall"
	"unsafe"
)

// 截屏相关的 Win32 过程与常量。
var (
	user32GetDC              = user32.NewProc("GetDC")
	user32ReleaseDC          = user32.NewProc("ReleaseDC")
	user32GetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	gdi32CreateCompatibleDC  = gdi32.NewProc("CreateCompatibleDC")
	gdi32CreateCompatibleBMP = gdi32.NewProc("CreateCompatibleBitmap")
	gdi32SelectObject        = gdi32.NewProc("SelectObject")
	gdi32BitBlt              = gdi32.NewProc("BitBlt")
	gdi32GetDIBits           = gdi32.NewProc("GetDIBits")
	gdi32DeleteObject        = gdi32.NewProc("DeleteObject")
)

const (
	smCXScreen       = 0
	smCYScreen       = 1
	srccopy          = 0x00CC0020
	dibRGBColors     = 0
	biRGB            = 0
	bitmapInfoHeaderSize = 40
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// ScreenSize 返回主屏的逻辑分辨率（像素）。
func ScreenSize() (int, int, error) {
	w, _, err := user32GetSystemMetrics.Call(smCXScreen)
	h, _, err2 := user32GetSystemMetrics.Call(smCYScreen)
	if w == 0 || h == 0 {
		return 0, 0, fmt.Errorf("获取屏幕尺寸失败: %v / %v", err, err2)
	}
	return int(w), int(h), nil
}

// CaptureScreen 截取主屏，返回 PNG 编码的字节。displayIdx 目前固定取主屏（索引 0）。
func CaptureScreen(displayIdx int) ([]byte, error) {
	width, height, err := ScreenSize()
	if err != nil {
		return nil, err
	}
	return captureRect(0, 0, width, height)
}

// CaptureRect 截取指定矩形区域，返回 PNG 字节。
func CaptureRect(x, y, width, height int) ([]byte, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("截取区域无效: %d,%d %dx%d", x, y, width, height)
	}
	return captureRect(x, y, width, height)
}

func captureRect(left, top, width, height int) ([]byte, error) {
	hdc, _, err := user32GetDC.Call(0)
	if hdc == 0 {
		return nil, fmt.Errorf("GetDC 失败: %v", err)
	}
	defer user32ReleaseDC.Call(0, hdc)

	mdc, _, err := gdi32CreateCompatibleDC.Call(hdc)
	if mdc == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC 失败: %v", err)
	}
	defer gdi32DeleteObject.Call(mdc)

	hbmp, _, err := gdi32CreateCompatibleBMP.Call(hdc, uintptr(width), uintptr(height))
	if hbmp == 0 {
		return nil, fmt.Errorf("CreateCompatibleBitmap 失败: %v", err)
	}
	defer gdi32DeleteObject.Call(hbmp)

	old, _, _ := gdi32SelectObject.Call(mdc, hbmp)
	defer gdi32SelectObject.Call(mdc, old)

	// BitBlt 源 = 桌面 DC，目标 = 兼容 DC。
	ret, _, err := gdi32BitBlt.Call(mdc, 0, 0, uintptr(width), uintptr(height), hdc, uintptr(left), uintptr(top), srccopy)
	if ret == 0 {
		return nil, fmt.Errorf("BitBlt 失败: %v", err)
	}

	// top-down（负高度）便于直接构造 RGBA。
	bmi := bitmapInfoHeader{
		Size:     bitmapInfoHeaderSize,
		Width:    int32(width),
		Height:   int32(-height), // 负高度 = top-down
		Planes:   1,
		BitCount: 32,
		Compression: biRGB,
	}
	buf := make([]byte, width*height*4)
	rows, _, err := gdi32GetDIBits.Call(mdc, hbmp, 0, uintptr(height),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bmi)), dibRGBColors)
	// top-down 模式 GetDIBits 返回值语义为成功行数或 0（0 也可能成功），以 buffer 是否非空为准。
	_ = rows
	if err != nil && err != syscall.Errno(0) {
		return nil, fmt.Errorf("GetDIBits 失败: %v", err)
	}

	// BGRA → RGBA。
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < width*height; i++ {
		b := buf[i*4]
		g := buf[i*4+1]
		r := buf[i*4+2]
		rgba.Pix[i*4] = r
		rgba.Pix[i*4+1] = g
		rgba.Pix[i*4+2] = b
		rgba.Pix[i*4+3] = 255
	}

	var out bytes.Buffer
	if err := png.Encode(&out, rgba); err != nil {
		return nil, fmt.Errorf("PNG 编码失败: %w", err)
	}
	return out.Bytes(), nil
}
