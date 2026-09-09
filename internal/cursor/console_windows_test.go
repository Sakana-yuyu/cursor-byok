//go:build windows

package cursor

import "testing"

func TestSystemCommandsDoNotCreateConsole(t *testing.T) {
	attr := hideWindow()
	if !attr.HideWindow || attr.CreationFlags&0x08000000 == 0 {
		t.Fatal("证书和 Defender 命令必须同时隐藏窗口并禁止创建控制台")
	}
}
