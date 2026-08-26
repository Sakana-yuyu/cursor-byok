//go:build linux

package cursor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"cursor/internal/logger"
)

// LinuxCATrustPlan describes a distribution trust anchor directory and refresh command.
type LinuxCATrustPlan struct {
	AnchorDir      string
	RefreshCommand []string
}

// LinuxCAOptions provides testable command and terminal hooks.
type LinuxCAOptions struct {
	RunCommand        func(name string, args ...string) ([]byte, error)
	TerminalAvailable func() bool
}

func DetectLinuxCATrust(root string) (LinuxCATrustPlan, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" {
		return LinuxCATrustPlan{}, errors.New("linux trust root is empty")
	}
	candidates := []LinuxCATrustPlan{
		{AnchorDir: filepath.Join(root, "etc", "pki", "ca-trust", "source", "anchors"), RefreshCommand: []string{"update-ca-trust", "extract"}},
		{AnchorDir: filepath.Join(root, "etc", "ca-certificates", "trust-source", "anchors"), RefreshCommand: []string{"trust", "extract-compat"}},
		{AnchorDir: filepath.Join(root, "usr", "local", "share", "ca-certificates"), RefreshCommand: []string{"update-ca-certificates"}},
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate.AnchorDir); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return LinuxCATrustPlan{}, fmt.Errorf("未找到 Linux CA trust anchor 目录: %s", root)
}

func linuxCAInstalled(certPEM []byte, plan LinuxCATrustPlan) (bool, error) {
	if strings.TrimSpace(plan.AnchorDir) == "" {
		return false, errors.New("linux CA anchor directory is empty")
	}
	entries, err := os.ReadDir(plan.AnchorDir)
	if err != nil {
		return false, fmt.Errorf("读取 Linux CA anchor 目录失败: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".crt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(plan.AnchorDir, entry.Name()))
		if err != nil {
			return false, fmt.Errorf("读取 Linux CA anchor 失败: %w", err)
		}
		if string(data) == string(certPEM) {
			return true, nil
		}
	}
	return false, nil
}

func ensureLinuxCACertInstalled(certPEM []byte, certPath string, plan LinuxCATrustPlan, options LinuxCAOptions) (bool, error) {
	if len(plan.RefreshCommand) == 0 {
		return false, errors.New("Linux CA refresh command is empty")
	}
	installed, err := linuxCAInstalled(certPEM, plan)
	if err != nil {
		return false, err
	}
	if installed {
		return true, nil
	}

	terminalAvailable := options.TerminalAvailable
	if terminalAvailable == nil {
		terminalAvailable = func() bool { return os.Getenv("TERM") != "" }
	}
	refreshCommand := formatLinuxCommand(plan.RefreshCommand)
	if !terminalAvailable() {
		return false, fmt.Errorf("Linux CA trust 尚未安装；请在终端执行 sudo %s", refreshCommand)
	}

	runCommand := options.RunCommand
	if runCommand == nil {
		runCommand = func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		}
	}
	if err := os.MkdirAll(plan.AnchorDir, 0o755); err != nil {
		return false, fmt.Errorf("创建 Linux CA anchor 目录失败: %w", err)
	}
	anchorPath := filepath.Join(plan.AnchorDir, "cursor-byok.crt")
	if err := os.WriteFile(anchorPath, certPEM, 0o644); err != nil {
		if strings.TrimSpace(certPath) == "" {
			return false, fmt.Errorf("写入 Linux CA anchor 失败: %w", err)
		}
		if _, installErr := runCommand("sudo", "install", "-m", "0644", certPath, anchorPath); installErr != nil {
			return false, fmt.Errorf("Linux CA anchor 安装失败；请执行 sudo install -m 0644 %s %s: %w", certPath, anchorPath, installErr)
		}
	}
	if _, err := runCommand("sudo", plan.RefreshCommand...); err != nil {
		return false, fmt.Errorf("Linux CA trust 刷新失败；请执行 sudo %s: %w", refreshCommand, err)
	}
	installed, err = linuxCAInstalled(certPEM, plan)
	if err != nil {
		return false, err
	}
	if !installed {
		return false, fmt.Errorf("Linux CA trust 刷新完成，但未检测到 anchor；请执行 sudo %s", refreshCommand)
	}
	logger.Infof("ensureLinuxCACertInstalled: cert installed at %s", anchorPath)
	return true, nil
}

func formatLinuxCommand(command []string) string {
	parts := make([]string, 0, len(command))
	for _, part := range command {
		if strings.ContainsAny(part, " \\t\\\"'\\\\") {
			parts = append(parts, "'"+strings.ReplaceAll(part, "'", "'\\\"'\\\"'")+"'")
			continue
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

// EnsureCACertInstalled ensures the CA is trusted by the Linux distribution store.
func EnsureCACertInstalled(certPEM []byte, certPath string) error {
	plan, err := DetectLinuxCATrust("/")
	if err != nil {
		return err
	}
	_, err = ensureLinuxCACertInstalled(certPEM, certPath, plan, LinuxCAOptions{})
	return err
}
