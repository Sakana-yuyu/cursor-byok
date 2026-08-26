package appdata

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DesktopSettings stores native desktop startup preferences.
type DesktopSettings struct {
	SilentStart bool `json:"silentStart"`
}

func desktopSettingsPath() string {
	return filepath.Join(RootDir(), "desktop-settings.json")
}

// LoadDesktopSettings loads native desktop preferences, returning defaults when absent.
func LoadDesktopSettings() (DesktopSettings, error) {
	data, err := os.ReadFile(desktopSettingsPath())
	if errors.Is(err, os.ErrNotExist) {
		return DesktopSettings{}, nil
	}
	if err != nil {
		return DesktopSettings{}, fmt.Errorf("read desktop settings: %w", err)
	}
	var settings DesktopSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return DesktopSettings{}, fmt.Errorf("decode desktop settings: %w", err)
	}
	return settings, nil
}

// SaveDesktopSettings persists native desktop preferences atomically.
func SaveDesktopSettings(settings DesktopSettings) error {
	if err := os.MkdirAll(RootDir(), 0o755); err != nil {
		return fmt.Errorf("create desktop settings directory: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode desktop settings: %w", err)
	}
	data = append(data, '\n')
	tempPath := desktopSettingsPath() + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o600); err != nil {
		return fmt.Errorf("write desktop settings: %w", err)
	}
	settingsPath := desktopSettingsPath()
	if err := os.Rename(tempPath, settingsPath); err != nil {
		// Windows does not replace an existing destination during Rename.
		if removeErr := os.Remove(settingsPath); removeErr != nil {
			_ = os.Remove(tempPath)
			return fmt.Errorf("save desktop settings: %w", err)
		}
		if retryErr := os.Rename(tempPath, settingsPath); retryErr != nil {
			_ = os.Remove(tempPath)
			return fmt.Errorf("save desktop settings: %w", retryErr)
		}
	}
	return nil
}
