package client

import (
	"fmt"
	"os"
	"runtime"

	"cursor/internal/appdata"
	"cursor/internal/autostart"
)

// LoadDesktopSettings returns native desktop preferences.
func (s *ProxyService) LoadDesktopSettings() (appdata.DesktopSettings, error) {
	return appdata.LoadDesktopSettings()
}

// SaveDesktopSettings persists native desktop preferences.
func (s *ProxyService) SaveDesktopSettings(settings appdata.DesktopSettings) error {
	return appdata.SaveDesktopSettings(settings)
}

// GetAutostartEnabled reports whether the Linux user-session entry exists.
func (s *ProxyService) GetAutostartEnabled() (bool, error) {
	if runtime.GOOS != "linux" {
		return false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, fmt.Errorf("get user home: %w", err)
	}
	return autostart.IsAutostartEnabled(home)
}

// SetAutostartEnabled updates the Linux user-session entry for this executable.
func (s *ProxyService) SetAutostartEnabled(enabled bool) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get user home: %w", err)
	}
	if !enabled {
		return autostart.RemoveAutostart(home)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get application executable: %w", err)
	}
	return autostart.WriteAutostart(home, executable)
}
