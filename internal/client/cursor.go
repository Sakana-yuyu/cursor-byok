package client

import (
	"errors"
	"fmt"
	goruntime "runtime"

	"cursor/internal/cursor"
	"cursor/internal/logger"
	localruntime "cursor/internal/runtime"
)

// ApplyCursorSettings serializes standalone repair with start/stop and CA reload.
func (s *ProxyService) ApplyCursorSettings() error {
	if s == nil {
		return errors.New("proxy service is not initialized")
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.applyCursorSettingsLocked()
}

func (s *ProxyService) applyCursorSettingsLocked() (applyErr error) {
	if s.currentProxy() == nil {
		return fmt.Errorf("proxy is not initialized")
	}
	if err := cursor.ValidateUserSettings(); err != nil {
		return err
	}
	s.mu.RLock()
	alreadyApplied := s.cursorSettingsApplied
	s.mu.RUnlock()
	defer func() {
		if applyErr != nil && !alreadyApplied {
			if err := cursor.RestoreHostSettings(); err != nil {
				applyErr = errors.Join(applyErr, fmt.Errorf("恢复宿主设置失败: %w", err))
			}
		}
	}()
	// 证书材料读取走 s.mu 快照：CA 修复热重载（reloadCAFromDiskLocked）持 s.mu
	// 重写这些字段，无锁读会拿到新旧混合状态（len 检查通过但读到旧 PEM）。
	s.mu.RLock()
	caCertPEM := make([]byte, len(s.caCertPEM))
	copy(caCertPEM, s.caCertPEM)
	caFilePath := s.caFilePath
	s.mu.RUnlock()
	// 降级启动（CA 异常，certManager=nil）时 caCertPEM 为空，
	// 绝不能拿空 PEM 去写 CA 证书文件——否则会覆盖真实 CA 证书。
	if len(caCertPEM) == 0 {
		return errors.New("CA 证书材料缺失（应用已降级启动，本地代理不可用）")
	}
	s.caFileMu.Lock()
	caCertPath, err := cursor.EnsureCACertFile(caCertPEM, caFilePath)
	if err == nil {
		// 回写路径与 reloadCAFromDiskLocked 同走 s.mu，保证 caFilePath 的
		// 读写全部在同一把锁下（caFileMu 只负责串行化本函数自身）。
		s.mu.Lock()
		s.caFilePath = caCertPath
		s.mu.Unlock()
	}
	s.caFileMu.Unlock()
	if err != nil {
		return fmt.Errorf("ensure ca cert file: %w", err)
	}

	switch goruntime.GOOS {
	case "windows", "darwin":
		if err := cursor.EnsureCACertInstalled(caCertPEM, caCertPath); err != nil {
			return fmt.Errorf("install ca cert: %w", err)
		}
		// Node 默认不读系统证书库（含 Windows 的 LocalMachine\Root），
		// 必须显式写入 NODE_EXTRA_CA_CERTS，否则 Cursor 的云端请求会报
		// "self signed certificate in certificate chain"。
		if err := cursor.SetSystemNodeExtraCACerts(caCertPath); err != nil {
			return fmt.Errorf("set node extra ca certs: %w", err)
		}
	case "linux":
		if err := cursor.EnsureCACertInstalled(caCertPEM, caCertPath); err != nil {
			return fmt.Errorf("install ca cert: %w", err)
		}
	}

	if err := cursor.WriteUserProxySettings(cursor.ProxyURLFromListenAddr(s.currentProxy().Snapshot().ListenAddr)); err != nil {
		return err
	}
	// 终端预设独立于代理生命周期：即使用户稍后停止代理，Cursor 仍会保留
	// 已选中的现代终端和 Python 3 路径。探测失败不能阻断代理可用性。
	if _, err := cursor.EnsureTerminalEnvironmentSettings(); err != nil {
		logger.Errorf("apply cursor terminal environment failed: %v", err)
	}
	s.setCursorSettingsApplied(true)
	return nil
}

// ClearCursorSettings restores only settings still owned by this application.
func (s *ProxyService) ClearCursorSettings() error {
	if s == nil {
		return errors.New("proxy service is not initialized")
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.clearCursorSettingsLocked()
}

func (s *ProxyService) clearCursorSettingsLocked() error {
	hostErr := cursor.RestoreHostSettings()
	authErr := cursor.RestoreInjectedCursorUserInfo(localruntime.InjectAuthToken)
	if authErr != nil {
		authErr = fmt.Errorf("restore cursor user info: %w", authErr)
	}
	if hostErr == nil {
		s.setCursorSettingsApplied(false)
	}
	return errors.Join(hostErr, authErr)
}

// GetDeviceID 用于处理与 GetDeviceID 相关的逻辑。
func (s *ProxyService) GetDeviceID() (string, error) {
	return cursor.GetDeviceID()
}
