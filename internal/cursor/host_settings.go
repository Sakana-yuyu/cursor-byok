package cursor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
)

// These journals are intentionally beside the host settings, not the application's
// configurable data root: recovery must survive changes to CURSOR_BYOK_HOME.
const hostSettingsOwner = "cursor-byok/host-settings/v1"

var hostSettingsMu sync.Mutex
var hostAtomicWrite = atomicHostWrite

type ownedSetting struct {
	Present  bool  `json:"present"`
	Original any   `json:"original"`
	Applied  []any `json:"applied"`
	Released bool  `json:"released,omitempty"`
}
type proxySettingsSnapshot struct {
	Owner  string                   `json:"owner"`
	Target string                   `json:"target"`
	Keys   map[string]*ownedSetting `json:"keys"`
}

func atomicHostWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".byok-host-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}
func writeHostJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return hostAtomicWrite(path, append(data, '\n'), 0600)
}
func readHostJSON(path string, value any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return false, fmt.Errorf("读取宿主接管快照失败（已保留）: %w", err)
	}
	return true, nil
}
func readHostSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]any), nil
	}
	if err != nil {
		return nil, err
	}
	settings, err := decodeCursorSettingsJSONC(data)
	if err != nil {
		return nil, fmt.Errorf("Cursor 配置无法解析，未修改原文件: %w", err)
	}
	return settings, nil
}
func proxySnapshotPath(path string) string { return path + ".byok-proxy-snapshot.json" }
func loadProxySnapshot(path string) (*proxySettingsSnapshot, error) {
	var s proxySettingsSnapshot
	exists, err := readHostJSON(proxySnapshotPath(path), &s)
	if err != nil || !exists {
		return nil, err
	}
	if s.Owner != hostSettingsOwner || s.Target != path || len(s.Keys) != len(injectedCursorSettingsKeys) {
		return nil, errors.New("宿主代理快照无效，保留快照且不修改配置")
	}
	for _, key := range injectedCursorSettingsKeys {
		v := s.Keys[key]
		if v == nil || len(v.Applied) == 0 {
			return nil, errors.New("宿主代理快照缺少归属信息")
		}
	}
	return &s, nil
}
func matchesApplied(value any, present bool, s *ownedSetting) bool {
	if !present || s.Released {
		return false
	}
	for _, applied := range s.Applied {
		if reflect.DeepEqual(value, applied) {
			return true
		}
	}
	return false
}
func originalSetting(value any, present bool, s *ownedSetting) bool {
	return present == s.Present && (!present || reflect.DeepEqual(value, s.Original))
}

// ValidateUserSettings is a read-only syntax preflight; missing/empty settings are valid.
func ValidateUserSettings() error {
	hostSettingsMu.Lock()
	defer hostSettingsMu.Unlock()
	path, err := resolveCursorSettingsPath()
	if err != nil {
		return err
	}
	_, err = readHostSettings(path)
	return err
}

// ValidateUserProxySettingsInput validates input and host settings without writing.
func ValidateUserProxySettingsInput(proxyURL string) error {
	if strings.TrimSpace(proxyURL) == "" {
		return errors.New("代理地址为空")
	}
	return ValidateUserSettings()
}
func writeOwnedProxySettings(proxyURL string) error {
	hostSettingsMu.Lock()
	defer hostSettingsMu.Unlock()
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return errors.New("代理地址为空")
	}
	path, err := resolveCursorSettingsPath()
	if err != nil {
		return err
	}
	settings, err := readHostSettings(path)
	if err != nil {
		return err
	}
	s, err := loadProxySnapshot(path)
	if err != nil {
		return err
	}
	fresh := s == nil
	if fresh {
		s = &proxySettingsSnapshot{Owner: hostSettingsOwner, Target: path, Keys: make(map[string]*ownedSetting)}
	}
	applied := map[string]any{"http.proxy": proxyURL, "http.proxyKerberosServicePrincipal": proxyURL, "http.proxySupport": "on", "cursor.general.disableHttp2": true, "http.experimental.systemCertificatesV2": true}
	for _, key := range injectedCursorSettingsKeys {
		current, present := settings[key]
		entry := s.Keys[key]
		if fresh {
			entry = &ownedSetting{Present: present, Original: current}
			s.Keys[key] = entry
		} else if !entry.Released && !matchesApplied(current, present, entry) && !originalSetting(current, present, entry) {
			entry.Released = true
		}
		if entry.Released {
			continue
		}
		if !matchesApplied(applied[key], true, entry) {
			entry.Applied = append(entry.Applied, applied[key])
		}
		settings[key] = applied[key]
	}
	// Keep both previous and pending applied values: either can exist if interrupted.
	if err := writeHostJSON(proxySnapshotPath(path), s); err != nil {
		return fmt.Errorf("保存代理接管快照失败: %w", err)
	}
	if err := writeHostJSON(path, settings); err != nil {
		return err
	}
	// Once committed, old applied values are no longer owned. Retain both only
	// during the crash window between intent and commit.
	for _, key := range injectedCursorSettingsKeys {
		if !s.Keys[key].Released {
			s.Keys[key].Applied = []any{applied[key]}
		}
	}
	return writeHostJSON(proxySnapshotPath(path), s)
}
func restoreOwnedProxySettings() error {
	hostSettingsMu.Lock()
	defer hostSettingsMu.Unlock()
	path, err := resolveCursorSettingsPath()
	if err != nil {
		return err
	}
	s, err := loadProxySnapshot(path)
	if err != nil || s == nil {
		return err
	}
	settings, err := readHostSettings(path)
	if err != nil {
		return err
	}
	changed := false
	for _, key := range injectedCursorSettingsKeys {
		entry := s.Keys[key]
		current, present := settings[key]
		if !matchesApplied(current, present, entry) {
			continue
		}
		if entry.Present {
			settings[key] = entry.Original
		} else {
			delete(settings, key)
		}
		changed = true
	}
	if changed {
		if err := writeHostJSON(path, settings); err != nil {
			return err
		}
	}
	return os.Remove(proxySnapshotPath(path))
}

// RestoreHostSettings attempts both independent restorations even if one fails.
func RestoreHostSettings() error {
	return errors.Join(ClearUserProxySettings(), ClearSystemNodeExtraCACerts())
}
