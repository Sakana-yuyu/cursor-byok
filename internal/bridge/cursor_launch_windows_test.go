//go:build windows

package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveCursorLauncherScript(t *testing.T) {
	root := t.TempDir()
	// 模拟 Cursor 安装结构: .../resources/app/bin/cursor.cmd 引用 ../../.. 的 Cursor.exe
	binDir := filepath.Join(root, "resources", "app", "bin")
	cursorExe := filepath.Join(root, "Cursor.exe")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cursorExe, []byte("MZ"), 0o755); err != nil {
		t.Fatalf("write exe: %v", err)
	}
	script := filepath.Join(binDir, "cursor.cmd")
	content := "@echo off\r\nsetlocal\r\nset ELECTRON_RUN_AS_NODE=1\r\n\"%~dp0..\\..\\..\\Cursor.exe\" \"%~dp0..\\out\\cli.js\" %*\r\nendlocal\r\n"
	if err := os.WriteFile(script, []byte(content), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	got := resolveCursorLauncherScript(script)
	if got == "" {
		t.Fatalf("resolveCursorLauncherScript 应解析出 Cursor.exe, got empty")
	}
	abs, _ := filepath.Abs(cursorExe)
	if filepath.Clean(got) != filepath.Clean(abs) {
		t.Errorf("resolveCursorLauncherScript = %q, want %q", got, abs)
	}

	// 指向不存在 exe 的脚本 → 空串
	broken := filepath.Join(binDir, "cursor-broken.cmd")
	brokenContent := `"%~dp0..\..\..\Missing.exe"` + "\r\n"
	if err := os.WriteFile(broken, []byte(brokenContent), 0o644); err != nil {
		t.Fatalf("write broken script: %v", err)
	}
	if got := resolveCursorLauncherScript(broken); got != "" {
		t.Errorf("指向不存在 exe 应返回空串, got %q", got)
	}
}

func TestFindCursorExecutableOnPath(t *testing.T) {
	root := t.TempDir()
	// 场景: PATH 前面是"半残"安装(bin/cursor.cmd 指向不存在 exe),
	// 后面是"完整"安装(bin/cursor.cmd 指向真实 Cursor.exe)。
	brokenBin := filepath.Join(root, "broken", "resources", "app", "bin")
	goodRoot := filepath.Join(root, "good")
	goodBin := filepath.Join(goodRoot, "resources", "app", "bin")
	goodExe := filepath.Join(goodRoot, "Cursor.exe")

	for _, dir := range []string{brokenBin, goodBin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(goodExe, []byte("MZ"), 0o755); err != nil {
		t.Fatalf("write good exe: %v", err)
	}
	brokenScript := filepath.Join(brokenBin, "cursor.cmd")
	goodScript := filepath.Join(goodBin, "cursor.cmd")
	if err := os.WriteFile(brokenScript, []byte("\"%~dp0..\\..\\..\\Missing.exe\"\r\n"), 0o644); err != nil {
		t.Fatalf("write broken script: %v", err)
	}
	if err := os.WriteFile(goodScript, []byte("\"%~dp0..\\..\\..\\Cursor.exe\"\r\n"), 0o644); err != nil {
		t.Fatalf("write good script: %v", err)
	}

	t.Setenv("PATH", brokenBin+string(os.PathListSeparator)+goodBin)
	got := findCursorExecutableOnPath()
	abs, _ := filepath.Abs(goodExe)
	if filepath.Clean(got) != filepath.Clean(abs) {
		t.Errorf("findCursorExecutableOnPath = %q, want %q", got, abs)
	}
}
