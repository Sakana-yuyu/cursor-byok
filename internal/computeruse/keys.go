package computeruse

import (
	"fmt"
	"strings"
)

// 按键名 → Windows 虚拟键码。覆盖 tools.json key 字段常见的命名。
var keyNameToVK = map[string]uint16{
	// 修饰键
	"ctrl": 0x11, "control": 0x11, "alt": 0x12, "menu": 0x12, "shift": 0x10,
	"cmd": 0x5B, "win": 0x5B, "meta": 0x5B, "super": 0x5B,
	// 特殊键
	"enter": 0x0D, "return": 0x0D, "tab": 0x09, "esc": 0x1B, "escape": 0x1B,
	"backspace": 0x08, "delete": 0x2E, "del": 0x2E, "insert": 0x2D, "ins": 0x2D,
	"home": 0x24, "end": 0x23, "pageup": 0x21, "pgup": 0x21, "pagedown": 0x22, "pgdn": 0x22,
	"up": 0x26, "down": 0x28, "left": 0x25, "right": 0x27,
	"space": 0x20, "capslock": 0x14, "numlock": 0x90, "scrolllock": 0x91,
	"printscreen": 0x2C, "prtsc": 0x2C,
	// F 键
	"f1": 0x70, "f2": 0x71, "f3": 0x72, "f4": 0x73, "f5": 0x74, "f6": 0x75,
	"f7": 0x76, "f8": 0x77, "f9": 0x78, "f10": 0x79, "f11": 0x7A, "f12": 0x7B,
}

// parseKeyCombo 把按键描述（如 "Enter"、"ctrl+shift+t"、"a"）解析为虚拟键码序列。
// 单字符（如 "a"、"1"）转为其大写 ASCII 码作为虚拟键码（VK_A=0x41 等）。
func parseKeyCombo(s string) []uint16 {
	parts := strings.Split(strings.TrimSpace(strings.ToLower(s)), "+")
	keys := make([]uint16, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if vk, ok := keyNameToVK[name]; ok {
			keys = append(keys, vk)
			continue
		}
		// 单字符：转大写 ASCII 码（a-z, 0-9, 符号）
		if len(name) == 1 {
			c := name[0]
			if c >= 'a' && c <= 'z' {
				keys = append(keys, uint16(c-'a'+'A'))
				continue
			}
			keys = append(keys, uint16(toupperASCII(c)))
			continue
		}
		// 未知键名跳过（避免整个组合失败）
	}
	return keys
}

func toupperASCII(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 'a' + 'A'
	}
	return c
}

// vkToCDPKeyName 把 Windows 虚拟键码转为 Playwright Keyboard.press 的键名
// （如 "Enter"、"Control"、"ArrowUp"、"F1"）。字母/数字 VK 直接转字符。
// 无法映射时返回空字符串。
func vkToCDPKeyName(vk uint16) string {
	switch vk {
	case 0x11:
		return "Control"
	case 0x12:
		return "Alt"
	case 0x10:
		return "Shift"
	case 0x5B:
		return "Meta"
	case 0x0D:
		return "Enter"
	case 0x09:
		return "Tab"
	case 0x1B:
		return "Escape"
	case 0x08:
		return "Backspace"
	case 0x2E:
		return "Delete"
	case 0x2D:
		return "Insert"
	case 0x24:
		return "Home"
	case 0x23:
		return "End"
	case 0x21:
		return "PageUp"
	case 0x22:
		return "PageDown"
	case 0x26:
		return "ArrowUp"
	case 0x28:
		return "ArrowDown"
	case 0x25:
		return "ArrowLeft"
	case 0x27:
		return "ArrowRight"
	case 0x20:
		return "Space"
	}
	if vk >= 0x70 && vk <= 0x7B {
		return fmt.Sprintf("F%d", vk-0x70+1)
	}
	if vk >= 'A' && vk <= 'Z' {
		return string(rune(vk))
	}
	if vk >= '0' && vk <= '9' {
		return string(rune(vk))
	}
	return ""
}
