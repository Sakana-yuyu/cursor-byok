// exec_shell_profile.go 承载 Shell 解释器 profile 支持：把模型请求的 profile
// （auto/powershell/pwsh/cmd/git-bash/wsl）解析为具体可执行文件，并把原始命令
// 包装成该解释器可执行的等价命令。Windows 下用 PowerShell EncodedCommand 做安全启动器。
package execbridge

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
)

// validShellProfiles 是 Shell 工具 profile 参数的合法取值集合。
var validShellProfiles = map[string]struct{}{
	"auto": {}, "powershell": {}, "pwsh": {}, "cmd": {}, "git-bash": {}, "wsl": {},
}

// normalizeShellProfile 归一化并校验 profile；空值按 auto 处理。
func normalizeShellProfile(profile string) (string, error) {
	profile = strings.ToLower(strings.TrimSpace(profile))
	if profile == "" {
		return "auto", nil
	}
	if _, ok := validShellProfiles[profile]; !ok {
		return "", fmt.Errorf("Shell profile must be one of auto, powershell, pwsh, cmd, git-bash, wsl")
	}
	return profile, nil
}

// resolveShellProfileExecutable 返回指定 profile 在当前机器上的可执行文件路径与固定参数。
// 找不到对应解释器时返回错误（openShell 会让整个调用失败，模型随即改用 auto 重试）。
func resolveShellProfileExecutable(profile string) (string, []string, error) {
	lookPath := func(names ...string) string {
		for _, name := range names {
			if path, err := osexec.LookPath(name); err == nil {
				return path
			}
		}
		return ""
	}
	switch profile {
	case "powershell":
		if path := lookPath("powershell.exe", "powershell"); path != "" {
			return path, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "-"}, nil
		}
	case "pwsh":
		if path := lookPath("pwsh.exe", "pwsh"); path != "" {
			return path, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "-"}, nil
		}
	case "cmd":
		if runtime.GOOS == "windows" {
			if path := lookPath("cmd.exe"); path != "" {
				return path, []string{"/d", "/q"}, nil
			}
		}
	case "git-bash":
		if runtime.GOOS == "windows" {
			if gitPath := lookPath("git.exe"); gitPath != "" {
				for _, candidate := range []string{
					filepath.Join(filepath.Dir(gitPath), "bash.exe"),
					filepath.Join(filepath.Dir(gitPath), "..", "bin", "bash.exe"),
				} {
					candidate = filepath.Clean(candidate)
					if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
						return candidate, []string{"--noprofile", "--norc", "-s"}, nil
					}
				}
			}
			for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
				for _, relative := range []string{filepath.Join("Git", "bin", "bash.exe"), filepath.Join("Programs", "Git", "bin", "bash.exe")} {
					candidate := filepath.Join(root, relative)
					if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
						return candidate, []string{"--noprofile", "--norc", "-s"}, nil
					}
				}
			}
		} else if path := lookPath("bash"); path != "" {
			return path, []string{"--noprofile", "--norc", "-s"}, nil
		}
	case "wsl":
		if runtime.GOOS == "windows" {
			if path := lookPath("wsl.exe"); path != "" {
				return path, []string{"sh", "-s"}, nil
			}
		}
	}
	return "", nil, fmt.Errorf("Shell profile %q is unavailable on this machine", profile)
}

// encodePowerShellCommand 把命令编码为 PowerShell -EncodedCommand 所需的
// UTF-16LE base64 字符串（PowerShell 自带该启动形态，避免命令行转义歧义）。
func encodePowerShellCommand(command string) string {
	units := utf16.Encode([]rune(command))
	data := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(data[index*2:], unit)
	}
	return base64.StdEncoding.EncodeToString(data)
}

// shellQuotePOSIX 按 POSIX 规则单引号包裹字符串（内嵌单引号用 '"'"' 转义）。
func shellQuotePOSIX(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// buildExplicitShellProfileCommand 把原始命令包装成指定 profile 可执行的等价命令。
// 非 Windows 直接用 base64 + 目标解释器；Windows 一律经 PowerShell EncodedCommand
// 安全启动目标解释器并把命令喂到其标准输入，规避 cmd/参数转义差异。
func buildExplicitShellProfileCommand(profile string, command string) (string, error) {
	target, targetArgs, err := resolveShellProfileExecutable(profile)
	if err != nil {
		return "", err
	}
	payloadCommand := command
	if runtime.GOOS == "windows" && profile == "cmd" && !strings.HasSuffix(payloadCommand, "\n") {
		payloadCommand += "\r\n"
	}
	payload := base64.StdEncoding.EncodeToString([]byte(payloadCommand))
	if runtime.GOOS != "windows" {
		return fmt.Sprintf("printf '%%s' '%s' | base64 -d | %s", payload, strings.Join(append([]string{shellQuotePOSIX(target)}, targetArgs...), " ")), nil
	}
	launcherName := "powershell.exe"
	if _, err := osexec.LookPath(launcherName); err != nil {
		launcherName = "pwsh.exe"
		if _, err := osexec.LookPath(launcherName); err != nil {
			return "", fmt.Errorf("Shell profile %q requires powershell or pwsh as the safe Windows launcher", profile)
		}
	}
	escape := func(value string) string { return strings.ReplaceAll(value, "'", "''") }
	launcherScript := fmt.Sprintf(
		"$c=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'));$i=[Diagnostics.ProcessStartInfo]::new();$i.FileName='%s';$i.Arguments='%s';$i.UseShellExecute=$false;$i.RedirectStandardInput=$true;$p=[Diagnostics.Process]::new();$p.StartInfo=$i;[void]$p.Start();$p.StandardInput.Write($c);$p.StandardInput.Close();$p.WaitForExit();exit $p.ExitCode",
		payload, escape(target), escape(strings.Join(targetArgs, " ")))
	return launcherName + " -NoLogo -NoProfile -NonInteractive -EncodedCommand " + encodePowerShellCommand(launcherScript), nil
}
