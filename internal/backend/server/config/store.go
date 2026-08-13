package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"cursor/internal/logger"
)

const (
	// configFilePerm / configDirPerm 只允许当前用户读写：用户配置里保存了
	// 模型供应商 apiKey 与余额查询凭据，不能对同机其他用户可读。
	configFilePerm os.FileMode = 0o600
	configDirPerm  os.FileMode = 0o700
)

type Store struct {
	path     string
	logsRoot string
	mu       sync.Mutex
}

type fileSnapshot struct {
	exists  bool
	modTime int64
	size    int64
}

func NewStore(path string, logsRoot string) *Store {
	return &Store{
		path:     strings.TrimSpace(path),
		logsRoot: strings.TrimSpace(logsRoot),
	}
}

func (store *Store) Path() string {
	if store == nil {
		return ""
	}
	return store.path
}

func (store *Store) snapshot() fileSnapshot {
	if store == nil || strings.TrimSpace(store.path) == "" {
		return fileSnapshot{}
	}
	info, err := os.Stat(store.path)
	if err != nil {
		return fileSnapshot{}
	}
	return fileSnapshot{
		exists:  true,
		modTime: info.ModTime().UnixNano(),
		size:    info.Size(),
	}
}

func (store *Store) Load(_ context.Context) (Config, error) {
	if store == nil || strings.TrimSpace(store.path) == "" {
		return DefaultConfig(), nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	restrictExistingPermissions(store.path)

	data, err := os.ReadFile(store.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			defaultConfig := DefaultConfig()
			if err := store.saveLocked(defaultConfig); err != nil {
				return DefaultConfig(), err
			}
			return defaultConfig, nil
		}
		return DefaultConfig(), fmt.Errorf("读取用户配置失败: %w", err)
	}

	var current Config
	if err := yaml.Unmarshal(data, &current); err != nil {
		return DefaultConfig(), fmt.Errorf("解析用户配置失败: %w", err)
	}
	hasDelegation := yamlHasKey(data, "delegation")
	hasDelegationEnabled := yamlHasNestedKey(data, "delegation", "enabled")
	if !hasDelegation || !hasDelegationEnabled {
		current.Delegation.Enabled = DefaultConfig().Delegation.Enabled
	}
	if hasDelegationEnabled && !yamlHasNestedKey(data, "delegation", "maxConcurrency") {
		current.Delegation.MaxConcurrency = DefaultDelegationMaxConcurrency
	}
	if !yamlHasKey(data, "goal") {
		current.Goal = DefaultGoalConfig()
	}
	normalized, err := NormalizeConfig(current)
	if err != nil {
		return DefaultConfig(), err
	}
	if shouldPersistNormalizedConfig(data, current, normalized) {
		if err := store.saveLocked(normalized); err != nil {
			return DefaultConfig(), err
		}
	}
	return normalized, nil
}

func (store *Store) Save(_ context.Context, cfg Config) (Config, error) {
	if store == nil || strings.TrimSpace(store.path) == "" {
		return Config{}, errors.New("配置存储未初始化")
	}

	normalized, err := NormalizeConfig(cfg)
	if err != nil {
		return Config{}, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if err := store.saveLocked(normalized); err != nil {
		return Config{}, err
	}
	return normalized, nil
}

func (store *Store) saveLocked(normalized Config) error {
	if err := os.MkdirAll(filepath.Dir(store.path), configDirPerm); err != nil {
		return fmt.Errorf("创建用户配置目录失败: %w", err)
	}

	data, err := yaml.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("序列化用户配置失败: %w", err)
	}

	tempPath := store.path + ".tmp"
	if err := os.WriteFile(tempPath, data, configFilePerm); err != nil {
		return fmt.Errorf("写入临时配置失败: %w", err)
	}
	// WriteFile 只在创建时应用 perm，残留的临时文件可能带着更宽的权限，
	// 因此重命名前显式收紧一次。
	if err := os.Chmod(tempPath, configFilePerm); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("设置临时配置权限失败: %w", err)
	}
	if err := os.Rename(tempPath, store.path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("保存用户配置失败: %w", err)
	}
	return nil
}

// restrictExistingPermissions 收紧已存在配置文件与所在目录的权限。
// 配置里含 apiKey / balanceAccessToken 等凭据，早期版本以 0644 落盘，
// 升级后首次读取时就地迁移，避免同机其他用户可读。
func restrictExistingPermissions(path string) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return
	}
	if dir := filepath.Dir(trimmed); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() && info.Mode().Perm() != configDirPerm {
			if err := os.Chmod(dir, configDirPerm); err != nil {
				logger.Warn("收紧用户配置目录权限失败", "error", err)
			}
		}
	}
	info, err := os.Stat(trimmed)
	if err != nil || info.IsDir() || info.Mode().Perm() == configFilePerm {
		return
	}
	if err := os.Chmod(trimmed, configFilePerm); err != nil {
		logger.Warn("收紧用户配置文件权限失败", "error", err)
	}
}

func shouldPersistNormalizedConfig(raw []byte, current Config, normalized Config) bool {
	if yamlHasKey(raw, "routing") {
		return true
	}
	if !yamlHasKey(raw, "backendListenAddr") || !yamlHasKey(raw, "proxyListenAddr") {
		return true
	}
	if current.BackendListenAddr != normalized.BackendListenAddr || current.ProxyListenAddr != normalized.ProxyListenAddr {
		return true
	}
	if current.ProviderStreamIdleTimeout != normalized.ProviderStreamIdleTimeout && yamlHasKey(raw, "providerStreamIdleTimeout") {
		return true
	}
	if current.TurnStaleTimeout != normalized.TurnStaleTimeout && yamlHasKey(raw, "turnStaleTimeout") {
		return true
	}
	if current.NativeDelegationProgressTimeout != normalized.NativeDelegationProgressTimeout && yamlHasKey(raw, "nativeDelegationProgressTimeout") {
		return true
	}
	if modelAdapterIDsChanged(current.ModelAdapters, normalized.ModelAdapters) {
		return true
	}
	if !reflect.DeepEqual(current.Delegation, normalized.Delegation) {
		return true
	}
	if !reflect.DeepEqual(current.MCPTrustGrants, normalized.MCPTrustGrants) {
		return true
	}
	if !yamlHasKey(raw, "goal") {
		return true
	}
	return false
}

func modelAdapterIDsChanged(current []ModelAdapterConfig, normalized []ModelAdapterConfig) bool {
	if len(current) != len(normalized) {
		return true
	}
	for index := range current {
		if strings.TrimSpace(current[index].ID) != strings.TrimSpace(normalized[index].ID) {
			return true
		}
	}
	return false
}

func yamlHasKey(raw []byte, key string) bool {
	return yamlMappingHasKey(yamlRootMapping(raw), key)
}

func yamlHasNestedKey(raw []byte, parent string, key string) bool {
	mapping := yamlRootMapping(raw)
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == parent {
			return yamlMappingHasKey(mapping.Content[index+1], key)
		}
	}
	return false
}

func yamlRootMapping(raw []byte) *yaml.Node {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return &yaml.Node{}
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return &yaml.Node{}
	}
	return root.Content[0]
}

func yamlMappingHasKey(mapping *yaml.Node, key string) bool {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return false
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return true
		}
	}
	return false
}
