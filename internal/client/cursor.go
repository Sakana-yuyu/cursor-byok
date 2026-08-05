package client

import (
	"fmt"
	goruntime "runtime"

	"cursor/internal/cursor"
)

// ApplyCursorSettings 用于处理与 ApplyCursorSettings 相关的逻辑。
func (s *ProxyService) ApplyCursorSettings() error {
	if s == nil || s.proxy == nil {
		return fmt.Errorf("proxy is not initialized")
	}
	s.caFileMu.Lock()
	caCertPath, err := cursor.EnsureCACertFile(s.caCertPEM, s.caFilePath)
	if err == nil {
		s.caFilePath = caCertPath
	}
	s.caFileMu.Unlock()
	if err != nil {
		return fmt.Errorf("ensure ca cert file: %w", err)
	}

	switch goruntime.GOOS {
	case "windows", "darwin":
		if err := cursor.EnsureCACertInstalled(s.caCertPEM, caCertPath); err != nil {
			return fmt.Errorf("install ca cert: %w", err)
		}
		// Node 默认不读系统证书库（含 Windows 的 LocalMachine\Root），
		// 必须显式写入 NODE_EXTRA_CA_CERTS，否则 Cursor 的云端请求会报
		// "self signed certificate in certificate chain"。
		if err := cursor.SetSystemNodeExtraCACerts(caCertPath); err != nil {
			return fmt.Errorf("set node extra ca certs: %w", err)
		}
	}

	if err := cursor.WriteUserProxySettings(cursor.ProxyURLFromListenAddr(s.proxy.Snapshot().ListenAddr)); err != nil {
		return err
	}
	s.setCursorSettingsApplied(true)
	return nil
}

// ClearCursorSettings 用于处理与 ClearCursorSettings 相关的逻辑。
func (s *ProxyService) ClearCursorSettings() error {
	if goruntime.GOOS == "darwin" || goruntime.GOOS == "windows" {
		if err := cursor.ClearSystemNodeExtraCACerts(); err != nil {
			return err
		}
	}
	if err := cursor.ClearUserProxySettings(); err != nil {
		return err
	}
	// 恢复 Cursor 官方登录态：清除注入的模拟账号（state.vscdb cursorAuth/*）。
	// 停止服务/退出/直连官方时必须执行，否则模拟 token 残留导致官方连接 401。
	// 恢复失败不可静默成功——上层需据此提示用户手动处理，否则直连官方会 401。
	if err := cursor.RestoreCursorUserInfo(); err != nil {
		s.setCursorSettingsApplied(false)
		return fmt.Errorf("restore cursor user info: %w", err)
	}
	s.setCursorSettingsApplied(false)
	return nil
}

// GetDeviceID 用于处理与 GetDeviceID 相关的逻辑。
func (s *ProxyService) GetDeviceID() (string, error) {
	return cursor.GetDeviceID()
}
