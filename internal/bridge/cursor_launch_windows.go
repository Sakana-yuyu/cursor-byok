//go:build windows

package bridge

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unsafe"
)

var (
	shell32       = syscall.NewLazyDLL("shell32.dll")
	shellExecuteW = shell32.NewProc("ShellExecuteW")
)

// cursorLauncherExePattern 匹配 cursor.cmd/.bat 启动脚本中对真实 Cursor.exe 的引用，
// 如 "%~dp0..\..\..\Cursor.exe"。%~dp0 是批处理内置变量，指脚本所在目录（带尾斜杠）。
var cursorLauncherExePattern = regexp.MustCompile(`(?i)%~dp0([^"\r\n]*\.exe)`)

// findCursorExecutableOnPath 遍历 PATH，解析 cursor 启动脚本指向的真实 Cursor.exe。
// Cursor 通常把 ...\resources\app\bin 加入 PATH（含 cursor.cmd / cursor.exe），
// 其中 cursor.cmd 引用的 ..\..\..\Cursor.exe 才是真正的可执行文件；直接执行 .cmd
// 会弹出 cmd 窗口且可能指向不存在的安装，因此这里优先返回解析出的 .exe。
// 若某个 PATH 条目的脚本解析不到（如指向已删除的安装），继续检查后续条目。
func findCursorExecutableOnPath() string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		for _, name := range []string{"Cursor.exe", "cursor.exe"} {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
		for _, name := range []string{"cursor.cmd", "cursor.bat"} {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				if exe := resolveCursorLauncherScript(candidate); exe != "" {
					return exe
				}
			}
		}
	}
	return ""
}

// resolveCursorLauncherScript 解析 Cursor 的 cursor.cmd/.bat 启动脚本，
// 提取其中 "%~dp0<rel>.exe" 形式引用并换算为绝对路径；目标存在则返回，否则空串。
func resolveCursorLauncherScript(script string) string {
	data, err := os.ReadFile(script)
	if err != nil {
		return ""
	}
	match := cursorLauncherExePattern.FindSubmatch(data)
	if len(match) < 2 {
		return ""
	}
	abs := filepath.Join(filepath.Dir(script), filepath.Clean(string(match[1])))
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		return abs
	}
	return ""
}

// launchCursorProcess 启动 Cursor 编辑器。
// 使用 ShellExecuteW 并采用默认操作：Windows 会自动应用 exe 的兼容性标志
// （如 RUNASADMIN），需要时弹出 UAC 提升，从而让普通权限的助手也能拉起管理员 Cursor。
func launchCursorProcess(path, workspaceDir string) error {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("invalid cursor path: %w", err)
	}
	var paramsPtr *uint16
	if trimmed := strings.TrimSpace(workspaceDir); trimmed != "" {
		paramsPtr, err = syscall.UTF16PtrFromString(trimmed)
		if err != nil {
			return fmt.Errorf("invalid workspace dir: %w", err)
		}
	}
	// hwnd=0, operation=NULL(默认动作), file=path, params=workspaceDir, dir=NULL, SW_SHOWNORMAL=1。
	// 成功时返回 >32 的句柄；<=32 为 SE_ERR_* 错误码；1223 为用户取消 UAC 提升。
	r1, _, callErr := shellExecuteW.Call(
		0,
		0,
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(paramsPtr)),
		0,
		1,
	)
	if int(r1) == 1223 {
		return fmt.Errorf("启动 Cursor 需要管理员权限，但未获得授权（用户取消）")
	}
	if int(r1) <= 32 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return fmt.Errorf("ShellExecuteW failed: %w", callErr)
		}
		return fmt.Errorf("ShellExecuteW failed with error code %d", int32(r1))
	}
	return nil
}
