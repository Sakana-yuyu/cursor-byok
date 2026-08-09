// input_windows.go 实现 Windows 鼠标/键盘控制（Win32 SendInput），纯 Go 无 CGO。
package computeruse

import (
	"fmt"
	"unsafe"
	"time"
)

var (
	user32SendInput    = user32.NewProc("SendInput")
	user32SetCursorPos = user32.NewProc("SetCursorPos")
	user32GetCursorPos = user32.NewProc("GetCursorPos")
	user32VkKeyScan    = user32.NewProc("VkKeyScanW")
	user32MapVirtualKey = user32.NewProc("MapVirtualKeyW")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfMove       = 0x0001
	mouseeventfLeftDown   = 0x0002
	mouseeventfLeftUp     = 0x0004
	mouseeventfRightDown  = 0x0008
	mouseeventfRightUp    = 0x0010
	mouseeventfMiddleDown = 0x0020
	mouseeventfMiddleUp   = 0x0040
	mouseeventfAbsolute   = 0x8000
	mouseeventfWheel      = 0x0800

	keyeventfKeyup   = 0x0002
	keyeventfUnicode = 0x0004
	keyeventfScancode = 0x0008

	mapvkVkToScancode = 0
)

type mouseInputData struct {
	Dx        int32
	Dy        int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type keyboardInputData struct {
	W       uint16
	Scan    uint16
	Flags   uint32
	Time    uint32
	Extra   uintptr
}

// inputUnion 用 [24]byte 占位，与 Windows INPUT 结构的 union 大小一致（mouse=24, keyboard=16，取最大）。
type tagINPUT struct {
	Type uint32
	// mouseInputData 与 keyboardInputData 共享这片内存（二者在原生层是 union）。
	// 取 max(24, 16)=24 字节。mouseInputData 正好 24 字节（int32*2 + uint32*3 + uintptr）。
	Union [24]byte
}

type point struct {
	X, Y int32
}

// MouseMove 移动鼠标到绝对坐标 (x, y)。
func MouseMove(x, y int) error {
	r1, _, err := user32SetCursorPos.Call(uintptr(x), uintptr(y))
	if r1 == 0 {
		return fmt.Errorf("SetCursorPos 失败: %v", err)
	}
	return nil
}

// CursorPosition 返回当前鼠标坐标。
func CursorPosition() (int, int, error) {
	var pt point
	r1, _, err := user32GetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	if r1 == 0 {
		return 0, 0, fmt.Errorf("GetCursorPos 失败: %v", err)
	}
	return int(pt.X), int(pt.Y), nil
}

// MouseClick 在 (x,y) 执行点击：先移动，再按下+抬起指定次数。
func MouseClick(x, y int, button string, count int) error {
	if count < 1 {
		count = 1
	}
	if err := MouseMove(x, y); err != nil {
		return err
	}
	down, up := mouseButtonFlags(button)
	if down == 0 {
		return fmt.Errorf("未知鼠标按键: %q", button)
	}
	for i := 0; i < count; i++ {
		if err := sendMouse(down); err != nil {
			return err
		}
		sleepBrief()
		if err := sendMouse(up); err != nil {
			return err
		}
		sleepBrief()
	}
	return nil
}

// MouseDown / MouseUp 只按下/抬起，不移动（用于拖拽起止）。
func MouseDown(button string) error {
	down, _ := mouseButtonFlags(button)
	if down == 0 {
		return fmt.Errorf("未知鼠标按键: %q", button)
	}
	return sendMouse(down)
}

func MouseUp(button string) error {
	_, up := mouseButtonFlags(button)
	if up == 0 {
		return fmt.Errorf("未知鼠标按键: %q", button)
	}
	return sendMouse(up)
}

// Scroll 在 (x,y) 滚动，direction=up/down，amount 为刻度数。
func Scroll(x, y int, direction string, amount int) error {
	if amount < 1 {
		amount = 1
	}
	if err := MouseMove(x, y); err != nil {
		return err
	}
	delta := int32(amount * 120) // WHEEL_DELTA=120
	if direction == "up" {
		delta = -delta
	}
	return sendMouseFlags(mouseeventfWheel, uint32(delta))
}

func sendMouse(flags uint32) error {
	return sendMouseFlags(flags, 0)
}

func sendMouseFlags(flags uint32, mouseData uint32) error {
	mi := mouseInputData{
		Flags:     flags,
		MouseData: mouseData,
	}
	in := tagINPUT{Type: inputMouse}
	*(*mouseInputData)(unsafe.Pointer(&in.Union)) = mi
	return sendInputs([]tagINPUT{in})
}

func mouseButtonFlags(button string) (down, up uint32) {
	switch button {
	case "", "left":
		return mouseeventfLeftDown, mouseeventfLeftUp
	case "right":
		return mouseeventfRightDown, mouseeventfRightUp
	case "middle":
		return mouseeventfMiddleDown, mouseeventfMiddleUp
	}
	return 0, 0
}

// KeyPress 按下并释放一个按键名（如 "Enter", "a", "ctrl+shift+t"）。
// 支持组合键（+ 分隔修饰键与主键）。
func KeyPress(key string) error {
	return KeyPressHold(key, 0)
}

// KeyPressHold 按下按键，按住 durationMs 后释放（durationMs<=0 表示正常短按）。
func KeyPressHold(key string, durationMs int) error {
	keys := parseKeyCombo(key)
	if len(keys) == 0 {
		return fmt.Errorf("无法解析按键: %q", key)
	}
	// 按下所有键
	for _, vk := range keys {
		if err := sendKey(vk, false); err != nil {
			return err
		}
	}
	if durationMs > 0 {
		time.Sleep(time.Duration(durationMs) * time.Millisecond)
	} else {
		sleepBrief()
	}
	// 反序释放
	for i := len(keys) - 1; i >= 0; i-- {
		if err := sendKey(keys[i], true); err != nil {
			return err
		}
	}
	return nil
}

// KeyType 逐字符输入文本（用 KEYEVENTF_UNICODE 兼容任意字符，不依赖键盘布局）。
func KeyType(text string) error {
	runes := []rune(text)
	for _, r := range runes {
		ki := keyboardInputData{
			W:     uint16(r),
			Flags: keyeventfUnicode,
		}
		in := tagINPUT{Type: inputKeyboard}
		*(*keyboardInputData)(unsafe.Pointer(&in.Union)) = ki
		if err := sendInputs([]tagINPUT{in}); err != nil {
			return err
		}
		kiUp := keyboardInputData{
			W:     uint16(r),
			Flags: keyeventfUnicode | keyeventfKeyup,
		}
		inUp := tagINPUT{Type: inputKeyboard}
		*(*keyboardInputData)(unsafe.Pointer(&inUp.Union)) = kiUp
		if err := sendInputs([]tagINPUT{inUp}); err != nil {
			return err
		}
	}
	return nil
}

func sendKey(vk uint16, up bool) error {
	flags := uint32(0)
	if up {
		flags |= keyeventfKeyup
	}
	scan, _, _ := user32MapVirtualKey.Call(uintptr(vk), mapvkVkToScancode)
	ki := keyboardInputData{
		W:     vk,
		Scan:  uint16(scan),
		Flags: flags,
	}
	in := tagINPUT{Type: inputKeyboard}
	*(*keyboardInputData)(unsafe.Pointer(&in.Union)) = ki
	return sendInputs([]tagINPUT{in})
}

func sendInputs(inputs []tagINPUT) error {
	if len(inputs) == 0 {
		return nil
	}
	sent, _, err := user32SendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(inputs[0]),
	)
	if int(sent) != len(inputs) {
		return fmt.Errorf("SendInput 仅成功 %d/%d: %v", int(sent), len(inputs), err)
	}
	return nil
}

// sleepBrief 是点击/按键之间的极短间隔，避免事件粘连。
func sleepBrief() {
	time.Sleep(8 * time.Millisecond)
}
