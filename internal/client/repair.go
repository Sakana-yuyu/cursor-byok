package client

import (
	"bytes"
	"fmt"
	"os/exec"
	goruntime "runtime"
	"strings"

	"cursor/internal/cursor"
	"cursor/internal/logger"
)

// ProxyRepairResult 描述一次「一键修复代理」的执行结果。
type ProxyRepairResult struct {
	// SettingsApplied 表示 Cursor settings.json 的代理配置是否已成功写入并校验通过。
	SettingsApplied bool `json:"settingsApplied"`
	// SettingsPath 表示 Cursor settings.json 路径。
	SettingsPath string `json:"settingsPath"`
	// ProxyURL 表示期望生效的本地代理地址。
	ProxyURL string `json:"proxyURL"`
	// CursorRunning 表示检测到 Cursor 正在运行（需要重启后才能生效）。
	CursorRunning bool `json:"cursorRunning"`
	// NeedsCursorRestart 表示是否需要重启 Cursor 才能生效。
	NeedsCursorRestart bool `json:"needsCursorRestart"`
	// Details 表示修复过程的步骤摘要。
	Details []string `json:"details"`
}

// RepairProxySettings 一键修复 Cursor 代理配置：
//  1. 若本地代理服务未运行则自动启动；
//  2. 重新注入 Cursor settings.json 代理配置与 CA 证书；
//  3. 读回 settings.json 校验注入结果；
//  4. 检测 Cursor 进程是否在运行，提示是否需要重启。
func (s *ProxyService) RepairProxySettings() (ProxyRepairResult, error) {
	details := make([]string, 0, 5)
	appendDetail := func(text string) { details = append(details, text) }

	state := s.GetState()
	if s.proxy == nil || !state.ProxyRunning {
		appendDetail("本地代理服务未运行，正在自动启动…")
		started, err := s.StartProxy()
		if err != nil {
			return ProxyRepairResult{Details: details}, fmt.Errorf("启动本地代理服务失败: %w", err)
		}
		state = started
		appendDetail("本地代理服务已启动")
	}

	proxyURL := strings.TrimSpace(cursor.ProxyURLFromListenAddr(state.ProxyListenAddr))
	if proxyURL == "" {
		proxyURL = strings.TrimSpace(cursor.ProxyURLFromListenAddr(state.ListenAddr))
	}
	if proxyURL == "" {
		return ProxyRepairResult{Details: details}, fmt.Errorf("无法获取本地代理监听地址")
	}
	appendDetail(fmt.Sprintf("代理地址：%s", proxyURL))

	if err := s.ApplyCursorSettings(); err != nil {
		return ProxyRepairResult{Details: details}, fmt.Errorf("注入 Cursor 代理配置失败: %w", err)
	}
	appendDetail("已重新写入 Cursor 代理配置")

	applied, settingsPath, actualValue, verifyErr := cursor.VerifyUserProxySettings(proxyURL)
	result := ProxyRepairResult{
		SettingsApplied: applied,
		SettingsPath:    settingsPath,
		ProxyURL:        proxyURL,
		CursorRunning:   isCursorProcessRunning(),
		Details:         details,
	}
	if verifyErr != nil {
		logger.Errorf("repairProxySettings: verify failed: %v", verifyErr)
		result.SettingsApplied = false
		result.Details = append(result.Details, fmt.Sprintf("校验代理配置失败：%v", verifyErr))
		return result, nil
	}
	if applied {
		appendDetail("代理配置校验通过")
	} else {
		appendDetail(fmt.Sprintf("代理配置校验未通过（当前 http.proxy=%q）", actualValue))
	}
	result.Details = details
	result.NeedsCursorRestart = result.CursorRunning
	if result.CursorRunning {
		appendDetail("检测到 Cursor 正在运行，需完全退出后重启才能生效")
	}
	return result, nil
}

// isCursorProcessRunning 检测 Cursor 进程是否正在运行。
func isCursorProcessRunning() bool {
	switch goruntime.GOOS {
	case "windows":
		out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq Cursor.exe", "/NH").Output()
		if err != nil {
			return false
		}
		return strings.Contains(strings.ToLower(string(out)), "cursor.exe")
	case "darwin":
		out, err := exec.Command("pgrep", "-x", "Cursor").Output()
		return err == nil && len(bytes.TrimSpace(out)) > 0
	case "linux":
		out, err := exec.Command("pgrep", "-x", "cursor").Output()
		return err == nil && len(bytes.TrimSpace(out)) > 0
	default:
		return false
	}
}
