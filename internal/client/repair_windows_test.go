//go:build windows

package client

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// 测试进程临时充当 tasklist，直接读取 Windows 控制台句柄，避免只检查启动参数。
func init() {
	if os.Getenv("CURSOR_TEST_TASKLIST") != "1" || len(os.Args) != 4 || os.Args[1] != "/FI" {
		return
	}
	window, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	var startup windows.StartupInfo
	if err := windows.GetStartupInfo(&startup); err != nil || window != 0 || startup.Flags&windows.STARTF_USESHOWWINDOW == 0 || startup.ShowWindow != windows.SW_HIDE {
		os.Exit(2)
	}
	if os.Args[2] != "IMAGENAME eq Cursor.exe" || os.Args[3] != "/NH" {
		os.Exit(3)
	}
	fmt.Println(os.Getenv("CURSOR_TEST_TASKLIST_OUTPUT"))
	os.Exit(0)
}

func TestCursorProcessProbeWithoutConsole(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tasklist.exe"), data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("CURSOR_TEST_TASKLIST", "1")
	for _, tc := range []struct {
		output string
		want   bool
	}{
		{"Cursor.exe 123 Console 1 10,000 K", true},
		{"INFO: No tasks are running which match the specified criteria.", false},
	} {
		t.Setenv("CURSOR_TEST_TASKLIST_OUTPUT", tc.output)
		if got := IsCursorProcessRunning(); got != tc.want {
			t.Fatalf("静默探测返回 %v，期望 %v；子进程有控制台或输出解析错误", got, tc.want)
		}
	}
}
