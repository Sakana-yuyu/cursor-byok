// Package terminalenv 检测当前机器上可供 Cursor 使用的终端与 Python 环境。
// 供 bridge 层展示状态、cursor 层写入 Cursor settings.json，以及 Shell profile
// 解析（pwsh 启动器）共用，避免各模块各自探测造成口径不一致。
package terminalenv

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"cursor/internal/processutil"
)

// Status 描述一次终端环境探测的结果。
// 前端按这些字段展示「终端 / Python」状态并决定「应用到 Cursor」按钮可用性。
type Status struct {
	// Platform 取值：windows / darwin / linux（runtime.GOOS）。
	Platform string
	// ShellName 是探测到的首选 shell 显示名（如 "PowerShell 7"、"zsh"）。
	ShellName string
	// ShellVersion 是 shell 版本号字符串，可能为空。
	ShellVersion string
	// ShellPath 是可执行文件的绝对路径；空表示未检测到可用 shell。
	ShellPath string
	// PythonPath 是探测到的 Python 3 解释器路径；空表示未检测到。
	PythonPath string
	// PythonVersion 是 python 的版本行（如 "Python 3.12.4"），可能为空。
	PythonVersion string
	// UpgradeRecommended 为 true 时前端提示用户升级到更现代的 shell。
	UpgradeRecommended bool
	// UpgradeMessage 是升级提示文案（仅在 UpgradeRecommended 时展示）。
	UpgradeMessage string
	// ConfigurationNotice 是展示在状态区的说明性文案（如 PowerShell 7 未安装提示）。
	ConfigurationNotice string
}

// Validate 校验探测结果是否可用：Shell 路径缺失时返回错误。
// 供 EnsureTerminalEnvironmentSettings 在写入 Cursor 配置前把关。
func (s Status) Validate() error {
	if strings.TrimSpace(s.ShellPath) == "" {
		return errNoShell
	}
	return nil
}

var errNoShell = &shellUnavailableError{}

type shellUnavailableError struct{}

func (e *shellUnavailableError) Error() string {
	return "未检测到可用的终端 shell"
}

// Detect 探测当前机器的终端与 Python 3 环境。
// Windows 优先选择 PowerShell 7（pwsh），找不到时回退 Windows PowerShell 5.1；
// 其他平台取 $SHELL 或常见默认 shell。
func Detect() Status {
	platform := runtime.GOOS
	status := Status{Platform: platform}
	switch platform {
	case "windows":
		detectWindows(&status)
	default:
		detectUnix(&status)
	}
	detectPython(&status)
	return status
}

// detectWindows 探测 Windows 下的 PowerShell。
// PowerShell 7 通过 PATH 中的 pwsh 定位；Windows PowerShell 5.1 走系统目录回退。
func detectWindows(status *Status) {
	if path := lookupExecutable("pwsh.exe", "pwsh"); path != "" {
		status.ShellName = "PowerShell 7"
		status.ShellPath = path
		status.ShellVersion = pwshVersion(path)
		return
	}
	systemRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	candidate := systemRoot + `\System32\WindowsPowerShell\v1.0\powershell.exe`
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		status.ShellName = "Windows PowerShell 5.1"
		status.ShellPath = candidate
		status.ConfigurationNotice = "未检测到 PowerShell 7；已回退到 Windows PowerShell 5.1。建议安装 PowerShell 7 以获得更完整的现代终端支持。"
		return
	}
	status.ConfigurationNotice = "未检测到 PowerShell；请安装 PowerShell 7 或启用 Windows PowerShell。"
}

// detectUnix 探测非 Windows 平台的 shell：优先 $SHELL，其次常见默认。
func detectUnix(status *Status) {
	candidates := []string{
		strings.TrimSpace(os.Getenv("SHELL")),
		"zsh", "bash", "sh",
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if path := lookupExecutable(candidate); path != "" {
			status.ShellName = shellDisplayName(candidate)
			status.ShellPath = path
			return
		}
	}
	status.ConfigurationNotice = "未检测到可用的终端 shell。"
}

// shellDisplayName 把可执行名转换为显示名（如 /bin/zsh -> zsh）。
func shellDisplayName(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	base := trimmed
	if idx := strings.LastIndexAny(trimmed, `/\`); idx >= 0 {
		base = trimmed[idx+1:]
	}
	return strings.TrimSuffix(strings.ToLower(base), ".exe")
}

// detectPython 探测 Python 3 解释器：优先 python3，其次 python，Windows 下再尝试 py -3。
func detectPython(status *Status) {
	for _, candidate := range []string{"python3", "python"} {
		if path := lookupExecutable(candidate); path != "" {
			if version := pythonVersion(path); version != "" {
				status.PythonPath = path
				status.PythonVersion = version
				return
			}
		}
	}
	if status.Platform == "windows" {
		path, version := pythonLauncher()
		if path != "" {
			status.PythonPath = path
			status.PythonVersion = version
		}
	}
}

// pythonLauncher 通过 py launcher 探测 Python 3（Windows）。
func pythonLauncher() (string, string) {
	pyPath := lookupExecutable("py.exe", "py")
	if pyPath == "" {
		return "", ""
	}
	cmd := exec.Command(pyPath, "-3", "-c", "import sys;print(sys.executable)")
	processutil.HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", ""
	}
	exe := strings.TrimSpace(string(out))
	if exe == "" {
		return "", ""
	}
	return exe, pythonVersion(exe)
}

// lookupExecutable 依次尝试 names，返回第一个可执行文件路径；全部失败返回空串。
func lookupExecutable(names ...string) string {
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

// pwshVersion 读取 pwsh 的版本号（第一行输出，如 "7.4.5"）。
func pwshVersion(path string) string {
	cmd := exec.Command(path, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "$PSVersionTable.PSVersion.ToString()")
	processutil.HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// pythonVersion 读取 python 的版本行（如 "Python 3.12.4"）。
func pythonVersion(path string) string {
	cmd := exec.Command(path, "--version")
	processutil.HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
