//go:build linux

package autostart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const desktopFileName = "cursor-byok.desktop"

// AutostartPath returns the per-user desktop entry path under home.
func AutostartPath(home string) string {
	return filepath.Join(home, ".config", "autostart", desktopFileName)
}

// BuildDesktopEntry builds a safe user-session autostart entry.
func BuildDesktopEntry(executable string) (string, error) {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return "", errors.New("executable path is empty")
	}
	if !filepath.IsAbs(executable) {
		return "", errors.New("executable path must be absolute")
	}
	if strings.ContainsAny(executable, "\r\n") {
		return "", errors.New("executable path contains a newline")
	}
	escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "$", "\\$").Replace(executable)
	return fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Cursor BYOK\nExec=\"%s\"\nTerminal=false\nX-GNOME-Autostart-enabled=true\n", escaped), nil
}

// WriteAutostart atomically creates or updates the per-user autostart entry.
func WriteAutostart(home, executable string) error {
	entry, err := BuildDesktopEntry(executable)
	if err != nil {
		return err
	}
	path := AutostartPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create autostart directory: %w", err)
	}
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, []byte(entry), 0o600); err != nil {
		return fmt.Errorf("write autostart entry: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("save autostart entry: %w", err)
	}
	return nil
}

func RemoveAutostart(home string) error {
	err := os.Remove(AutostartPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove autostart entry: %w", err)
	}
	return nil
}

func IsAutostartEnabled(home string) (bool, error) {
	info, err := os.Stat(AutostartPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat autostart entry: %w", err)
	}
	return !info.IsDir(), nil
}
