// Package cursorcapabilities 只读汇总已安装 Cursor 的静态能力标记。
package cursorcapabilities

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const ScannerVersion = "1"

var ErrInstallNotFound = errors.New("cursor_install_not_found")

type Extension struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
}

// Report 不保存安装目录或 bundle 正文，只保留脱敏后的静态能力摘要。
type Report struct {
	ScannerVersion     string      `json:"scannerVersion"`
	CursorVersion      string      `json:"cursorVersion,omitempty"`
	InstallRootHash    string      `json:"installRootHash"`
	Extensions         []Extension `json:"extensions"`
	ProtocolMarkers    []string    `json:"protocolMarkers"`
	BrowserToolMarkers []string    `json:"browserToolMarkers"`
	Warnings           []string    `json:"warnings"`
}

type extensionSpec struct {
	id       string
	protocol []string
	browser  []string
}

var extensionSpecs = []extensionSpec{
	{
		id:       "cursor-agent-exec",
		protocol: []string{"computer_use", "force_background_subagent", "subagent_await", "shell_allowlist_precheck", "mcp_allowlist_precheck"},
	},
	{
		id:       "cursor-agent-host",
		protocol: []string{"computer_use", "force_background_subagent", "subagent_await"},
	},
	{
		id:      "cursor-browser-automation",
		browser: []string{"cursor-ide-browser", "browser_tabs", "browser_navigate", "browser_lock", "browser_snapshot", "browser_click", "browser_type", "browser_fill", "browser_select_option", "browser_press_key", "browser_scroll", "browser_drag", "browser_take_screenshot", "browser_cdp", "browser_mouse_click_xy"},
	},
}

// Scan 只读取固定安装结构中的元数据与已知 marker，不执行、加载或修改任何 Cursor 代码。
func Scan(root string) (Report, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" {
		return Report{}, ErrInstallNotFound
	}
	if info, err := os.Stat(filepath.Join(root, "Cursor.exe")); err != nil || info.IsDir() {
		return Report{}, ErrInstallNotFound
	}

	report := Report{
		ScannerVersion:  ScannerVersion,
		InstallRootHash: hashString(root),
	}
	if version, err := cursorExecutableVersion(filepath.Join(root, "Cursor.exe")); err == nil {
		report.CursorVersion = version
	} else {
		report.Warnings = append(report.Warnings, "cursor_metadata_unreadable")
	}

	protocol := make(map[string]struct{})
	browser := make(map[string]struct{})
	for _, spec := range extensionSpecs {
		extensionDir := filepath.Join(root, "resources", "app", "extensions", spec.id)
		manifest, err := readExtensionManifest(filepath.Join(extensionDir, "package.json"))
		if err != nil {
			report.Warnings = append(report.Warnings, "extension_manifest_unreadable:"+spec.id)
			continue
		}
		report.Extensions = append(report.Extensions, Extension{ID: spec.id, Version: manifest.Version})
		bundle, err := readExtensionBundle(filepath.Join(extensionDir, "dist"))
		if err != nil {
			report.Warnings = append(report.Warnings, "extension_bundle_unreadable:"+spec.id)
			continue
		}
		for _, marker := range spec.protocol {
			if strings.Contains(bundle, marker) {
				protocol[marker] = struct{}{}
			}
		}
		for _, marker := range spec.browser {
			if strings.Contains(bundle, marker) {
				browser[marker] = struct{}{}
			}
		}
	}
	report.ProtocolMarkers = sortedKeys(protocol)
	report.BrowserToolMarkers = sortedKeys(browser)
	sort.Slice(report.Extensions, func(i, j int) bool { return report.Extensions[i].ID < report.Extensions[j].ID })
	sort.Strings(report.Warnings)
	return report, nil
}

type extensionManifest struct {
	Version string `json:"version"`
}

func readExtensionManifest(path string) (extensionManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return extensionManifest{}, err
	}
	var manifest extensionManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return extensionManifest{}, err
	}
	return manifest, nil
}

func readExtensionBundle(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".js" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", err
		}
		parts = append(parts, string(raw))
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("no JavaScript bundle")
	}
	return strings.Join(parts, "\n"), nil
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func sortedKeys(values map[string]struct{}) []string {
	items := make([]string, 0, len(values))
	for value := range values {
		items = append(items, value)
	}
	sort.Strings(items)
	return items
}
