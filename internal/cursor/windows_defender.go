//go:build windows

package cursor

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf16"

	"cursor/internal/logger"
)

// WindowsDefenderExclusionState 描述本地 Windows Defender 排除项引导状态（供前端/bridge 读取）。
type WindowsDefenderExclusionState struct {
	// Supported 表示当前平台是否支持 Defender 排除项（仅 windows=true）。
	Supported bool `json:"supported"`
	// DefenderActive 表示检测到 Windows Defender 为活动杀软。
	DefenderActive bool `json:"defenderActive"`
	// AlreadyExcluded 表示目标路径已在 Defender 排除列表中。
	AlreadyExcluded bool `json:"alreadyExcluded"`
	// Offered 表示是否已向用户提示过（持久化标志，用于「仅一次」）。
	Offered bool `json:"offered"`
	// Path 是建议排除的路径（应用 home 目录）。
	Path string `json:"path"`
}

// IsWindowsDefenderActive 检测 Windows Defender 是否为活动杀软。
// 通过非提权的 Get-MpComputerStatus 查询 AMRunningMode（非 Defender 环境下该命令会失败/报错）。
// 检测失败时保守返回 true（让前端走一键添加路径，失败再降级为引导）。
func IsWindowsDefenderActive() bool {
	powerShellPath, err := windowsSystemExecutable("System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if err != nil {
		logger.Infof("defender: locate powershell failed, assume active: %v", err)
		return true
	}
	// Get-MpComputerStatus 仅在 Defender 可用时返回对象；第三方杀软接管时该命令会报错。
	cmd := exec.Command(powerShellPath,
		"-NoProfile", "-NonInteractive", "-Command",
		"$s = Get-MpComputerStatus -ErrorAction SilentlyContinue; if ($s -and $s.AMRunningMode) { 'active' } else { 'inactive' }",
	)
	cmd.SysProcAttr = hideWindow()
	output, err := cmd.CombinedOutput()
	if err != nil {
		// 命令失败通常意味着 Defender 未启用或被第三方接管；保守视为非活动，走引导。
		logger.Infof("defender: Get-MpComputerStatus failed, treat as inactive: %v output=%s", err, strings.TrimSpace(string(output)))
		return false
	}
	active := strings.TrimSpace(string(output)) == "active"
	logger.Infof("defender: active=%v", active)
	return active
}

// IsPathExcludedByDefender 检测目标路径是否已在 Defender 排除列表中（非提权）。
func IsPathExcludedByDefender(path string) bool {
	powerShellPath, err := windowsSystemExecutable("System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if err != nil {
		return false
	}
	cmd := exec.Command(powerShellPath,
		"-NoProfile", "-NonInteractive", "-Command",
		"@(Get-MpPreference).ExclusionPath -contains "+quotePowerShellLiteral(path),
	)
	cmd.SysProcAttr = hideWindow()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == "True"
}

// AddDefenderExclusion 通过提权 PowerShell 把目标路径加入 Defender 排除列表。
// 触发 UAC：用户点「是」即授权；点「否」返回「用户取消」错误。
func AddDefenderExclusion(path string) error {
	powerShellPath, err := windowsSystemExecutable("System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if err != nil {
		return fmt.Errorf("定位 Windows PowerShell 失败: %w", err)
	}
	// 内层命令使用 EncodedCommand，避免路径中的引号、空格或中文经过两层
	// PowerShell 字符串解析后损坏。
	script := buildElevatedDefenderExclusionScript(powerShellPath, path)
	// 外层用一个新的 powershell 进程发起 Start-Process -Verb RunAs。
	outer := exec.Command(powerShellPath,
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script,
	)
	outer.SysProcAttr = hideWindow()
	output, err := outer.CombinedOutput()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == windowsUserCancelCode {
		return fmt.Errorf("用户取消了管理员权限授予")
	}
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return fmt.Errorf("添加 Defender 排除项失败: %w", err)
	}
	return fmt.Errorf("添加 Defender 排除项失败: %w, output: %s", err, trimmed)
}

func buildElevatedDefenderExclusionScript(powerShellPath string, path string) string {
	innerCommand := "$ErrorActionPreference = 'Stop'; try { Add-MpPreference -ExclusionPath " + quotePowerShellLiteral(path) + " -ErrorAction Stop; exit 0 } catch { exit 1 }"
	return fmt.Sprintf(
		"$process = Start-Process -FilePath %s -ArgumentList @('-NoProfile','-NonInteractive','-EncodedCommand','%s') -Verb RunAs -WindowStyle Hidden -Wait -PassThru; exit $process.ExitCode",
		quotePowerShellLiteral(powerShellPath),
		encodePowerShellCommand(innerCommand),
	)
}

// encodePowerShellCommand 生成 Windows PowerShell -EncodedCommand 所需的 UTF-16LE Base64。
func encodePowerShellCommand(command string) string {
	units := utf16.Encode([]rune(command))
	data := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(data[index*2:], unit)
	}
	return base64.StdEncoding.EncodeToString(data)
}

// QueryDefenderExclusionState 汇总当前 Defender 排除项状态（非提权，供前端展示）。
func QueryDefenderExclusionState(path string, offered bool) WindowsDefenderExclusionState {
	return WindowsDefenderExclusionState{
		Supported:       true,
		DefenderActive:  IsWindowsDefenderActive(),
		AlreadyExcluded: IsPathExcludedByDefender(path),
		Offered:         offered,
		Path:            path,
	}
}
