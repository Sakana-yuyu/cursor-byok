// Package promptinject owns the optional Codex-X-compatible prompt injection state.
//
// The implementation intentionally does not execute or vendor Codex-X. It only
// consumes Markdown files from the public examples directory. The upstream
// project is MIT licensed (copyright 2026 yynxxxxx); this package keeps the
// source attribution in its persisted metadata.
package promptinject

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cursor/internal/appdata"
	"cursor/internal/logger"
)

const (
	defaultRepo = "yynxxxxx/Codex-X"
	defaultRef  = "main"
	defaultFile = "gpt5.5-unrestricted.md"
	configName  = "prompt-injection.json"
	cacheName   = "prompt-injection.cache.md"

	ModeReplace = "replace"
	ModeAppend  = "append"

	managedBegin = "<!-- CODEX-X:INSTRUCTIONS:BEGIN -->"
	managedEnd   = "<!-- CODEX-X:INSTRUCTIONS:END -->"

	softwareChinesePrompt = `语言策略（软件中文化）：
- 默认使用简体中文回答用户，并使用中文解释思路、状态、计划和结果。
- 如果用户明确要求其他语言，遵循用户的语言要求；不要擅自改写或翻译用户原文。
- 代码、代码标识符、函数名、变量名、文件名、路径、命令、API 名称、JSON/schema、协议字段和错误原文必须保持准确；除非用户明确要求，不要翻译或改动这些技术内容。
- 工具调用的名称、参数、JSON 结构和 schema 必须保持不变；不要在工具调用中加入解释性文本。
- 面向用户的说明可以中文化，但引用的日志、错误、命令输出和代码片段应保留原文；不要改变响应解析所需的格式。`
)

// Config is deliberately additive: omitted fields in older JSON decode to the
// safe default (disabled, replace, and no local prompt).
type Config struct {
	Enabled                bool             `json:"enabled,omitempty"`
	SoftwareChineseEnabled bool             `json:"softwareChineseEnabled,omitempty"`
	Mode                   string           `json:"mode,omitempty"`
	Repo                   string           `json:"repo,omitempty"`
	Ref                    string           `json:"ref,omitempty"`
	SourceURL              string           `json:"sourceUrl,omitempty"`
	SelectedTemplate       string           `json:"selectedTemplate,omitempty"`
	LocalContent           string           `json:"localContent,omitempty"`
	CacheContent           string           `json:"cacheContent,omitempty"`
	Templates              []PromptTemplate `json:"templates,omitempty"`
	LastUpdated            string           `json:"lastUpdated,omitempty"`
	LastError              string           `json:"lastError,omitempty"`
	SourceLicense          string           `json:"sourceLicense,omitempty"`
	CustomEnabled          bool             `json:"customEnabled,omitempty"`
	CustomContent          string           `json:"customContent,omitempty"`
	// Git 提交文本本地化：CommitMessageEnabled 开启后，
	// CommitMessageLanguage 为界面所选模式（auto=跟随界面语言，或具体语言代码）；
	// CommitMessageLanguageResolved 是 auto 模式下解析出的具体语言（由前端同步）。
	CommitMessageEnabled          bool   `json:"commitMessageEnabled,omitempty"`
	CommitMessageLanguage         string `json:"commitMessageLanguage,omitempty"`
	CommitMessageLanguageResolved string `json:"commitMessageLanguageResolved,omitempty"`
}

// PromptTemplate is one independently persisted prompt injection entry.
type PromptTemplate struct {
	Name    string `json:"name"`
	Content string `json:"content,omitempty"`
	Enabled bool   `json:"enabled"`
}

// Status is returned to the Wails bridge without exposing credentials or full
// request URLs in errors.
type Status struct {
	Config
	CacheAvailable bool `json:"cacheAvailable"`
}

type Manager struct {
	mu   sync.RWMutex
	cfg  Config
	path string
}

func New() *Manager {
	return &Manager{path: filepath.Join(appdata.RootDir(), configName)}
}

func DefaultConfig() Config {
	return Config{Mode: ModeReplace, Repo: defaultRepo, Ref: defaultRef, SelectedTemplate: defaultFile, SourceLicense: "MIT (Codex-X, copyright 2026 yynxxxxx)"}
}

func normalizeConfig(cfg Config) Config {
	defaults := DefaultConfig()
	if strings.TrimSpace(cfg.Mode) != ModeAppend && strings.TrimSpace(cfg.Mode) != ModeReplace {
		cfg.Mode = defaults.Mode
	}
	if strings.TrimSpace(cfg.Repo) == "" {
		cfg.Repo = defaults.Repo
	}
	if strings.TrimSpace(cfg.Ref) == "" {
		cfg.Ref = defaults.Ref
	}
	if strings.TrimSpace(cfg.SelectedTemplate) == "" {
		cfg.SelectedTemplate = defaults.SelectedTemplate
	}
	if strings.TrimSpace(cfg.SourceLicense) == "" {
		cfg.SourceLicense = defaults.SourceLicense
	}
	// 提交信息本地化语言：空值视为 auto（跟随界面语言）。
	cfg.CommitMessageLanguage = strings.ToLower(strings.TrimSpace(cfg.CommitMessageLanguage))
	if cfg.CommitMessageLanguage == "" {
		cfg.CommitMessageLanguage = "auto"
	}
	// Migrate the legacy single-template representation into the new list.
	if len(cfg.Templates) == 0 && strings.TrimSpace(cfg.LocalContent) != "" {
		name := cfg.SelectedTemplate
		if name == "" {
			name = defaults.SelectedTemplate
		}
		cfg.Templates = []PromptTemplate{{Name: name, Content: cfg.LocalContent, Enabled: cfg.Enabled}}
	}
	for i := range cfg.Templates {
		cfg.Templates[i].Name = strings.TrimSpace(cfg.Templates[i].Name)
	}
	return cfg
}

func (m *Manager) Load() (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg.Mode != "" || m.cfg.Repo != "" || m.cfg.LastUpdated != "" || m.cfg.LocalContent != "" {
		return m.statusLocked(), nil
	}
	data, err := os.ReadFile(m.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	m.cfg = normalizeConfig(DefaultConfig())
	if len(data) > 0 {
		if err := json.Unmarshal(data, &m.cfg); err != nil {
			return Status{}, fmt.Errorf("read prompt injection config: %w", err)
		}
		m.cfg = normalizeConfig(m.cfg)
	}
	if strings.TrimSpace(m.cfg.CacheContent) == "" {
		if cache, readErr := os.ReadFile(filepath.Join(filepath.Dir(m.path), cacheName)); readErr == nil {
			m.cfg.CacheContent = string(cache)
		}
	}
	return m.statusLocked(), nil
}

func (m *Manager) statusLocked() Status {
	return Status{Config: m.cfg, CacheAvailable: strings.TrimSpace(m.cfg.CacheContent) != "" || strings.TrimSpace(m.cfg.LocalContent) != ""}
}

func (m *Manager) Status() (Status, error) { return m.Load() }

// SoftwareChineseEnabled 返回「软件使用中文化」开关状态（兼容旧字段）。
func (m *Manager) SoftwareChineseEnabled() bool {
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	if cfg.Mode == "" {
		if _, err := m.Load(); err == nil {
			m.mu.RLock()
			cfg = m.cfg
			m.mu.RUnlock()
		}
	}
	return cfg.SoftwareChineseEnabled
}

// CommitMessageLanguage 返回 Git 提交信息本地化的目标语言代码：
// auto（跟随界面）时返回前端解析好的具体语言（CommitMessageLanguageResolved）；
// 未启用本地化时返回空字符串。
func (m *Manager) CommitMessageLanguage() string {
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	if cfg.Mode == "" {
		if _, err := m.Load(); err == nil {
			m.mu.RLock()
			cfg = m.cfg
			m.mu.RUnlock()
		}
	}
	if !cfg.CommitMessageEnabled {
		return ""
	}
	lang := strings.ToLower(strings.TrimSpace(cfg.CommitMessageLanguage))
	if lang == "auto" {
		return strings.ToLower(strings.TrimSpace(cfg.CommitMessageLanguageResolved))
	}
	return lang
}

func (m *Manager) Save(cfg Config) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg.Mode == "" {
		if _, err := m.loadLocked(); err != nil {
			return Status{}, err
		}
	}
	cfg = normalizeConfig(cfg)
	// Keep a previously downloaded prompt if the UI only changes a toggle/mode.
	if strings.TrimSpace(cfg.CacheContent) == "" {
		cfg.CacheContent = m.cfg.CacheContent
	}
	if strings.TrimSpace(cfg.LocalContent) == "" {
		cfg.LocalContent = m.cfg.LocalContent
	}
	if len(cfg.Templates) == 0 {
		cfg.Templates = m.cfg.Templates
	}
	m.cfg = cfg
	if err := m.persistLocked(); err != nil {
		return Status{}, err
	}
	return m.statusLocked(), nil
}

func (m *Manager) loadLocked() (Status, error) {
	data, err := os.ReadFile(m.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	m.cfg = normalizeConfig(DefaultConfig())
	if len(data) > 0 {
		if err := json.Unmarshal(data, &m.cfg); err != nil {
			return Status{}, fmt.Errorf("read prompt injection config: %w", err)
		}
		m.cfg = normalizeConfig(m.cfg)
	}
	return m.statusLocked(), nil
}

func (m *Manager) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.path), ".prompt-injection-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, m.path)
}

func validTemplate(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && strings.HasSuffix(strings.ToLower(name), ".md") && !strings.ContainsAny(name, `/\\`) && name != "." && name != ".."
}

func rawURL(cfg Config) (string, error) {
	if strings.TrimSpace(cfg.SourceURL) != "" {
		u, err := url.Parse(strings.TrimSpace(cfg.SourceURL))
		if err != nil || u.Scheme != "https" || u.Host != "raw.githubusercontent.com" {
			return "", errors.New("source URL must be a public GitHub raw HTTPS URL")
		}
		return strings.TrimRight(u.String(), "/"), nil
	}
	parts := strings.Split(strings.TrimSpace(cfg.Repo), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || !validTemplate(cfg.SelectedTemplate) {
		return "", errors.New("invalid Codex-X repository or template")
	}
	return "https://raw.githubusercontent.com/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/" + url.PathEscape(strings.TrimSpace(cfg.Ref)) + "/examples/" + url.PathEscape(strings.TrimSpace(cfg.SelectedTemplate)), nil
}

func (m *Manager) Refresh(ctx context.Context) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.loadLocked(); err != nil {
		return Status{}, err
	}
	cfg := normalizeConfig(m.cfg)
	target, err := rawURL(cfg)
	if err != nil {
		cfg.LastError = err.Error()
		m.cfg = cfg
		_ = m.persistLocked()
		return m.statusLocked(), err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
	if err != nil {
		return m.statusLocked(), errors.New("create prompt update request failed")
	}
	req.Header.Set("User-Agent", "Cursor-Assistant-Prompt-Manager")
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		cfg.LastError = "prompt update network request failed"
		m.cfg = cfg
		_ = m.persistLocked()
		return m.statusLocked(), errors.New(cfg.LastError)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		cfg.LastError = fmt.Sprintf("prompt update failed (HTTP %d)", resp.StatusCode)
		m.cfg = cfg
		_ = m.persistLocked()
		return m.statusLocked(), errors.New(cfg.LastError)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil || len(strings.TrimSpace(string(body))) == 0 {
		cfg.LastError = "prompt update returned empty content"
		m.cfg = cfg
		_ = m.persistLocked()
		return m.statusLocked(), errors.New(cfg.LastError)
	}
	content := strings.TrimSpace(string(body))
	cachePath := filepath.Join(filepath.Dir(m.path), cacheName)
	if err := atomicWrite(cachePath, []byte(content)); err != nil {
		cfg.LastError = "prompt cache write failed"
		m.cfg = cfg
		_ = m.persistLocked()
		return m.statusLocked(), errors.New(cfg.LastError)
	}
	cfg.CacheContent, cfg.LocalContent, cfg.LastUpdated, cfg.LastError = content, content, time.Now().UTC().Format(time.RFC3339), ""
	updated := false
	for i := range cfg.Templates {
		if cfg.Templates[i].Name == cfg.SelectedTemplate {
			cfg.Templates[i].Content = content
			updated = true
			break
		}
	}
	if !updated {
		cfg.Templates = append(cfg.Templates, PromptTemplate{Name: cfg.SelectedTemplate, Content: content, Enabled: cfg.Enabled})
	}
	m.cfg = cfg
	if err := m.persistLocked(); err != nil {
		return Status{}, err
	}
	return m.statusLocked(), nil
}

// RefreshCatalog downloads the Markdown prompt catalog and each available prompt.
// Existing enabled flags are retained by template name.
func (m *Manager) RefreshCatalog(ctx context.Context) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.loadLocked(); err != nil {
		return Status{}, err
	}
	cfg := normalizeConfig(m.cfg)
	fail := func(err error) (Status, error) {
		if err == nil {
			return m.statusLocked(), nil
		}
		cfg.LastError = err.Error()
		m.cfg = cfg
		_ = m.persistLocked()
		return m.statusLocked(), err
	}
	parts := strings.Split(strings.TrimSpace(cfg.Repo), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fail(errors.New("invalid Codex-X repository"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	catalogURL := "https://api.github.com/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/contents/examples?ref=" + url.QueryEscape(cfg.Ref)
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return fail(errors.New("create prompt catalog request failed"))
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Cursor-Assistant-Prompt-Manager")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return fail(errors.New("prompt catalog network request failed"))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fail(fmt.Errorf("prompt catalog failed (HTTP %d)", resp.StatusCode))
	}
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&entries); err != nil {
		return fail(errors.New("decode prompt catalog failed"))
	}
	enabledByName := make(map[string]bool, len(cfg.Templates))
	for _, item := range cfg.Templates {
		enabledByName[item.Name] = item.Enabled
	}
	templates := make([]PromptTemplate, 0, len(entries))
	for _, entry := range entries {
		if entry.Type != "file" || !validTemplate(entry.Name) {
			continue
		}
		itemCfg := cfg
		itemCfg.SelectedTemplate = entry.Name
		target, err := rawURL(itemCfg)
		if err != nil {
			continue
		}
		contentReq, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
		if err != nil {
			continue
		}
		contentReq.Header.Set("User-Agent", "Cursor-Assistant-Prompt-Manager")
		contentResp, err := (&http.Client{Timeout: 12 * time.Second}).Do(contentReq)
		if err != nil || contentResp.StatusCode < 200 || contentResp.StatusCode >= 300 {
			if contentResp != nil {
				contentResp.Body.Close()
			}
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(contentResp.Body, 2<<20))
		contentResp.Body.Close()
		if readErr != nil || len(strings.TrimSpace(string(body))) == 0 {
			continue
		}
		templates = append(templates, PromptTemplate{Name: entry.Name, Content: strings.TrimSpace(string(body)), Enabled: enabledByName[entry.Name]})
	}
	if len(templates) == 0 {
		return fail(errors.New("prompt catalog returned no Markdown files"))
	}
	cfg.Templates = templates
	if cfg.SelectedTemplate == "" || !containsTemplate(templates, cfg.SelectedTemplate) {
		cfg.SelectedTemplate = templates[0].Name
	}
	for _, item := range templates {
		if item.Name == cfg.SelectedTemplate {
			cfg.LocalContent, cfg.CacheContent = item.Content, item.Content
			break
		}
	}
	cfg.LastUpdated, cfg.LastError = time.Now().UTC().Format(time.RFC3339), ""
	m.cfg = cfg
	if err := m.persistLocked(); err != nil {
		return Status{}, err
	}
	return m.statusLocked(), nil
}

func containsTemplate(items []PromptTemplate, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".prompt-cache-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Apply is the only prompt decision point. Disabled mode returns base byte-for-byte.
// The file reload keeps the bridge's Wails save operation visible to the forwarder
// without duplicating configuration services or requiring an app restart.
func (m *Manager) Apply(base string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.loadLocked(); err != nil {
		// Preserve the last known state if an optional settings file becomes unreadable.
	}
	if !m.cfg.Enabled && !m.cfg.SoftwareChineseEnabled && !m.cfg.CustomEnabled {
		return base
	}

	// 记录哪些注入开关处于激活状态，便于用户在日志中确认生效
	activeSwitches := make([]string, 0, 3)
	if m.cfg.Enabled {
		activeSwitches = append(activeSwitches, "codex-x")
	}
	if m.cfg.CustomEnabled {
		activeSwitches = append(activeSwitches, "custom")
	}
	if m.cfg.SoftwareChineseEnabled {
		activeSwitches = append(activeSwitches, "chinese")
	}
	logger.Infof("prompt injection applied switches=%s mode=%s base_len=%d", strings.Join(activeSwitches, ","), m.cfg.Mode, len(base))

	result := base
	if m.cfg.Enabled {
		if len(m.cfg.Templates) > 0 {
			parts := make([]string, 0, len(m.cfg.Templates))
			for _, template := range m.cfg.Templates {
				if template.Enabled && strings.TrimSpace(template.Content) != "" {
					parts = append(parts, strings.TrimSpace(template.Content))
				}
			}
			content := strings.Join(parts, "\n\n")
			if content != "" {
				if m.cfg.Mode == ModeAppend {
					result = result + "\n\n" + managedBegin + "\n" + content + "\n" + managedEnd
				} else {
					result = content
				}
			}
		} else {
			content := strings.TrimSpace(m.cfg.LocalContent)
			if content == "" {
				content = strings.TrimSpace(m.cfg.CacheContent)
			}
			if content != "" {
				if m.cfg.Mode == ModeAppend {
					result = result + "\n\n" + managedBegin + "\n" + content + "\n" + managedEnd
				} else {
					result = content
				}
			}
		}
	}
	if m.cfg.CustomEnabled {
		custom := strings.TrimSpace(m.cfg.CustomContent)
		if custom != "" {
			result = strings.TrimSpace(result) + "\n\n" + custom
		}
	}
	if m.cfg.SoftwareChineseEnabled {
		result = strings.TrimSpace(result) + "\n\n" + softwareChinesePrompt
	}
	return result
}

// SetPath is useful for embedding applications that already have a config root.
func (m *Manager) SetPath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(path) != "" {
		m.path = path
		m.cfg = Config{}
	}
}
