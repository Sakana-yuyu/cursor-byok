package cursor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"cursor/internal/appdata"
	"cursor/internal/logger"
	"cursor/internal/terminalenv"
)

// injectedCursorSettingsKeys 表示当前模块中的 injectedCursorSettingsKeys 状态值。
var injectedCursorSettingsKeys = []string{
	"http.proxy",
	"http.proxyKerberosServicePrincipal",
	"http.proxySupport",
	"cursor.general.disableHttp2",
	"http.experimental.systemCertificatesV2",
}

// EnsureCACertFile 用于处理与 EnsureCACertFile 相关的逻辑。
func EnsureCACertFile(certPEM []byte, currentPath string) (string, error) {
	certPath := appdata.CACertFilePath()
	// 已存在：复用，绝不覆盖。data/ca.crt 是 MITM 的配对材料（与 ca.key
	// 同源）；用独立传入的 certPEM 覆盖会破坏配对，导致 MITM 签名失败、
	// Cursor 一直 reconnecting（配对校验见 certs.certKeyMatch）。
	if _, err := os.Stat(certPath); err == nil {
		logger.Infof("ensureCACertFile: reuse existing path=%s", certPath)
		return certPath, nil
	}

	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		return "", fmt.Errorf("创建证书配置目录失败: %w", err)
	}

	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return "", fmt.Errorf("写入内置 CA 证书失败: %w", err)
	}
	sum := sha256.Sum256(certPEM)
	logger.Infof(
		"ensureCACertFile: wrote path=%s sha256=%s size=%d",
		certPath,
		strings.ToUpper(hex.EncodeToString(sum[:])),
		len(certPEM),
	)
	return certPath, nil
}

// nodeExtraCACertsEnvName 是 Node.js 读取额外受信任 CA 的环境变量名。
const nodeExtraCACertsEnvName = "NODE_EXTRA_CA_CERTS"

// WriteUserProxySettings 在持久化原值后接管代理设置。
func WriteUserProxySettings(proxyURL string) error {
	return writeOwnedProxySettings(proxyURL)
}

// EnsureTerminalEnvironmentSettings writes a dedicated Cursor terminal profile
// without tying it to proxy lifecycle cleanup. Existing unrelated settings stay
// untouched; malformed settings are reported instead of being replaced.
func EnsureTerminalEnvironmentSettings() (terminalenv.Status, error) {
	status := terminalenv.Detect()
	if err := status.Validate(); err != nil {
		return status, err
	}
	settingsPath, err := resolveCursorSettingsPath()
	if err != nil {
		return status, err
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return status, fmt.Errorf("创建 Cursor 配置目录失败: %w", err)
	}

	settings := make(map[string]any)
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return status, fmt.Errorf("读取 Cursor 配置失败: %w", err)
		}
	} else if len(bytes.TrimSpace(data)) > 0 {
		settings, err = decodeCursorSettingsJSONC(data)
		if err != nil {
			return status, fmt.Errorf("Cursor 配置无法解析，未修改终端设置: %w", err)
		}
	}

	applyTerminalEnvironmentSettings(settings, status)
	if err := writeCursorSettings(settingsPath, settings); err != nil {
		return status, err
	}
	logger.Infof("ensureTerminalEnvironmentSettings: path=%s shell=%s python=%s", settingsPath, status.ShellPath, status.PythonPath)
	return status, nil
}

// applyTerminalEnvironmentSettings 注入 BYOK 终端 profile 与 UTF-8 环境变量。
// 仅当用户未显式配置 python.defaultInterpreterPath 与
// terminal.integrated.defaultProfile.windows 时才写入默认值，绝不覆盖用户已有配置。
func applyTerminalEnvironmentSettings(settings map[string]any, status terminalenv.Status) {
	if status.PythonPath != "" {
		if _, exists := settings["python.defaultInterpreterPath"]; !exists {
			settings["python.defaultInterpreterPath"] = status.PythonPath
		}
	}
	switch status.Platform {
	case "windows":
		profileName := "Cursor BYOK PowerShell"
		profiles := mergeCursorSettingsMap(settings["terminal.integrated.profiles.windows"])
		profiles[profileName] = map[string]any{
			"path": status.ShellPath,
			"args": []string{"-NoLogo", "-NoExit", "-Command", "[Console]::InputEncoding=[Text.UTF8Encoding]::new();[Console]::OutputEncoding=[Text.UTF8Encoding]::new();$OutputEncoding=[Console]::OutputEncoding"},
		}
		settings["terminal.integrated.profiles.windows"] = profiles
		if _, exists := settings["terminal.integrated.defaultProfile.windows"]; !exists {
			settings["terminal.integrated.defaultProfile.windows"] = profileName
		}
		env := mergeCursorSettingsMap(settings["terminal.integrated.env.windows"])
		env["PYTHONUTF8"] = "1"
		env["PYTHONIOENCODING"] = "utf-8"
		settings["terminal.integrated.env.windows"] = env
	case "darwin":
		env := mergeCursorSettingsMap(settings["terminal.integrated.env.osx"])
		env["PYTHONUTF8"] = "1"
		env["PYTHONIOENCODING"] = "utf-8"
		settings["terminal.integrated.env.osx"] = env
	default:
		env := mergeCursorSettingsMap(settings["terminal.integrated.env.linux"])
		env["PYTHONUTF8"] = "1"
		env["PYTHONIOENCODING"] = "utf-8"
		settings["terminal.integrated.env.linux"] = env
	}
}

func mergeCursorSettingsMap(value any) map[string]any {
	result := make(map[string]any)
	if current, ok := value.(map[string]any); ok {
		for key, entry := range current {
			result[key] = entry
		}
	}
	return result
}

func writeCursorSettings(settingsPath string, settings map[string]any) error {
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 Cursor 配置失败: %w", err)
	}
	encoded = append(encoded, '\n')
	tempPath := settingsPath + ".tmp"
	if err := os.WriteFile(tempPath, encoded, 0o644); err != nil {
		return fmt.Errorf("写入 Cursor 配置临时文件失败: %w", err)
	}
	if err := os.Rename(tempPath, settingsPath); err != nil {
		return fmt.Errorf("保存 Cursor 配置失败: %w", err)
	}
	return nil
}

// ClearUserProxySettings 仅恢复快照中仍由本应用持有的代理设置。
func ClearUserProxySettings() error {
	return restoreOwnedProxySettings()
}

// VerifyUserProxySettings 读取 Cursor settings.json，校验 http.proxy 是否已正确注入。
// 返回校验结果、配置文件路径与当前实际值。
func VerifyUserProxySettings(proxyURL string) (applied bool, settingsPath string, actualValue string, err error) {
	settingsPath, err = resolveCursorSettingsPath()
	if err != nil {
		return false, "", "", err
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, settingsPath, "", nil
		}
		return false, settingsPath, "", fmt.Errorf("读取 Cursor 配置失败: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return false, settingsPath, "", nil
	}

	settings, err := decodeCursorSettingsJSONC(data)
	if err != nil {
		return false, settingsPath, "", fmt.Errorf("解析 Cursor 配置失败: %w", err)
	}
	actual, _ := settings["http.proxy"].(string)
	return strings.TrimSpace(actual) == strings.TrimSpace(proxyURL), settingsPath, actual, nil
}

// resolveCursorSettingsPath 用于处理与 resolveCursorSettingsPath 相关的逻辑。
func resolveCursorSettingsPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户目录失败: %w", err)
	}

	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support", "Cursor", "User", "settings.json"), nil
	case "windows":
		appData := os.Getenv("APPDATA")
		if strings.TrimSpace(appData) == "" {
			appData = filepath.Join(homeDir, "AppData", "Roaming")
		}
		return filepath.Join(appData, "Cursor", "User", "settings.json"), nil
	case "linux":
		configDir := os.Getenv("XDG_CONFIG_HOME")
		if strings.TrimSpace(configDir) == "" {
			configDir = filepath.Join(homeDir, ".config")
		}
		return filepath.Join(configDir, "Cursor", "User", "settings.json"), nil
	default:
		return "", fmt.Errorf("不支持的系统: %s", runtime.GOOS)
	}
}

// decodeCursorSettingsJSONC 用于处理与 decodeCursorSettingsJSONC 相关的逻辑。
func decodeCursorSettingsJSONC(data []byte) (map[string]any, error) {
	result := make(map[string]any)
	normalized, err := normalizeJSONC(data)
	if err != nil {
		return nil, err
	}
	normalized = bytes.TrimSpace(normalized)
	if len(normalized) == 0 {
		return result, nil
	}
	if err := json.Unmarshal(normalized, &result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("Cursor 配置顶层必须是 JSON 对象，不能为 null")
	}
	return result, nil
}

// normalizeJSONC 用于处理与 normalizeJSONC 相关的逻辑。
func normalizeJSONC(data []byte) ([]byte, error) {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}
	withoutComments, err := stripJSONCComments(data)
	if err != nil {
		return nil, err
	}
	return stripJSONCTrailingCommas(withoutComments), nil
}

// stripJSONCComments 用于处理与 stripJSONCComments 相关的逻辑。
func stripJSONCComments(data []byte) ([]byte, error) {
	out := make([]byte, 0, len(data))
	inString := false
	inLineComment := false
	inBlockComment := false
	escaped := false

	for i := 0; i < len(data); i++ {
		ch := data[i]

		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				out = append(out, ch)
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < len(data) && data[i+1] == '/' {
				inBlockComment = false
				i++
				continue
			}
			if ch == '\n' {
				out = append(out, ch)
			}
			continue
		}
		if inString {
			out = append(out, ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}

		if ch == '"' {
			inString = true
			out = append(out, ch)
			continue
		}
		if ch == '/' && i+1 < len(data) {
			next := data[i+1]
			if next == '/' {
				inLineComment = true
				i++
				continue
			}
			if next == '*' {
				inBlockComment = true
				i++
				continue
			}
		}
		out = append(out, ch)
	}

	if inBlockComment {
		return nil, errors.New("JSONC 块注释未闭合")
	}
	return out, nil
}

// stripJSONCTrailingCommas 用于处理与 stripJSONCTrailingCommas 相关的逻辑。
func stripJSONCTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false

	for i := 0; i < len(data); i++ {
		ch := data[i]
		if inString {
			out = append(out, ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}

		if ch == '"' {
			inString = true
			out = append(out, ch)
			continue
		}

		if ch == ',' {
			j := i + 1
			for j < len(data) && isJSONWhitespace(data[j]) {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
		}

		out = append(out, ch)
	}

	return out
}

// isJSONWhitespace 用于处理与 isJSONWhitespace 相关的逻辑。
func isJSONWhitespace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n'
}

// ProxyURLFromListenAddr 用于处理与 ProxyURLFromListenAddr 相关的逻辑。
func ProxyURLFromListenAddr(listenAddr string) string {
	addr := strings.TrimSpace(listenAddr)
	if addr == "" {
		return "http://127.0.0.1:8080"
	}

	// :8189 -> 127.0.0.1:8189
	if strings.HasPrefix(addr, ":") {
		return "http://127.0.0.1" + addr
	}

	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
			host = "127.0.0.1"
		}
		return "http://" + net.JoinHostPort(host, port)
	}

	return "http://" + addr
}
