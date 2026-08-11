package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	backend "cursor/internal/backend"
	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/certs"
	"cursor/internal/cursor"
	"cursor/internal/mitm"
	localruntime "cursor/internal/runtime"
	"gopkg.in/yaml.v3"
)

const (
	isolationConfigEnv  = "CURSOR_E2E_CONFIG"
	cursorExecutableEnv = "CURSOR_E2E_CURSOR_EXE"
)

type isolatedDirectories struct {
	root         string
	home         string
	appData      string
	localAppData string
}

func main() {
	configPath := strings.TrimSpace(os.Getenv(isolationConfigEnv))
	cursorPath := strings.TrimSpace(os.Getenv(cursorExecutableEnv))
	if configPath == "" || cursorPath == "" {
		fmt.Fprintln(os.Stderr, "CURSOR_E2E_CONFIG and CURSOR_E2E_CURSOR_EXE are required")
		os.Exit(2)
	}
	if err := run(configPath, cursorPath); err != nil {
		fmt.Fprintf(os.Stderr, "isolated Cursor E2E startup failed: %v\n", err)
		os.Exit(1)
	}
}

func run(sourceConfigPath string, cursorPath string) error {
	sourceConfig, err := readConfig(sourceConfigPath)
	if err != nil {
		return err
	}
	dirs, err := newIsolatedDirectories()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dirs.home, 0o700); err != nil {
		return fmt.Errorf("创建隔离用户目录失败: %w", err)
	}
	if err := applyIsolatedEnvironment(dirs); err != nil {
		return err
	}

	backendAddr, err := allocateLoopbackListener()
	if err != nil {
		return err
	}
	proxyAddr, err := allocateLoopbackListener()
	if err != nil {
		return err
	}
	isolatedConfig, err := buildIsolatedConfig(sourceConfig, backendAddr, proxyAddr)
	if err != nil {
		return err
	}
	configPath := filepath.Join(dirs.home, ".cursor-local-assistant-v2", "config.yaml")
	if err := writeConfig(configPath, isolatedConfig); err != nil {
		return err
	}

	certPath := filepath.Join(dirs.home, ".cursor-local-assistant-v2", "data", "ca.crt")
	keyPath := filepath.Join(dirs.home, ".cursor-local-assistant-v2", "data", "ca.key")
	certManager, err := certs.NewPersistentManager(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("创建隔离 CA 失败: %w", err)
	}

	store := serverconfig.NewStore(configPath, filepath.Join(dirs.home, ".cursor-local-assistant-v2", "logs"))
	host, err := backend.NewHost(store, nil)
	if err != nil {
		return fmt.Errorf("创建隔离 backend 失败: %w", err)
	}
	if err := host.Start(); err != nil {
		return fmt.Errorf("启动隔离 backend 失败: %w", err)
	}
	defer stopHost(host)
	if err := waitForHealth(host); err != nil {
		return err
	}

	proxy, err := mitm.NewProxyServer(isolatedConfig.ProxyListenAddr, host.BaseURL(), "", "", certManager)
	if err != nil {
		return fmt.Errorf("创建隔离 MITM 失败: %w", err)
	}
	if err := proxy.Start(); err != nil {
		return fmt.Errorf("启动隔离 MITM 失败: %w", err)
	}
	defer stopProxy(proxy)

	proxyURL := cursor.ProxyURLFromListenAddr(proxy.Snapshot().ListenAddr)
	if err := cursor.WriteUserProxySettings(proxyURL); err != nil {
		return fmt.Errorf("写入隔离 Cursor 代理设置失败: %w", err)
	}
	if err := cursor.InjectCursorUserInfo(localruntime.InjectAccountEmail, localruntime.InjectAuthToken); err != nil {
		return fmt.Errorf("注入隔离 Cursor 状态失败: %w", err)
	}

	command := exec.Command(cursorPath)
	command.Env = buildCursorChildEnvironment(os.Environ(), dirs, certPath)
	if err := command.Start(); err != nil {
		return fmt.Errorf("启动隔离 Cursor 失败: %w", err)
	}
	fmt.Printf("isolated_root=%s backend=%s proxy=%s cursor_pid=%d\n", dirs.root, host.ListenAddr(), proxy.Snapshot().ListenAddr, command.Process.Pid)
	if err := command.Wait(); err != nil {
		return fmt.Errorf("隔离 Cursor 已退出: %w", err)
	}
	return nil
}

func buildIsolatedConfig(input serverconfig.Config, backendAddr string, proxyAddr string) (serverconfig.Config, error) {
	if err := validateLoopbackListener(backendAddr); err != nil {
		return serverconfig.Config{}, fmt.Errorf("隔离 backend 地址无效: %w", err)
	}
	if err := validateLoopbackListener(proxyAddr); err != nil {
		return serverconfig.Config{}, fmt.Errorf("隔离 proxy 地址无效: %w", err)
	}
	if backendAddr == proxyAddr && !strings.HasSuffix(backendAddr, ":0") {
		return serverconfig.Config{}, errors.New("隔离 backend 与 proxy 不能使用同一监听地址")
	}
	input.BackendListenAddr = backendAddr
	input.ProxyListenAddr = proxyAddr
	input.Log = true
	return serverconfig.NormalizeConfig(input)
}

func validateLoopbackListener(value string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	if host != "127.0.0.1" || port == "" {
		return fmt.Errorf("仅允许 127.0.0.1 监听: %s", value)
	}
	return nil
}

func allocateLoopbackListener() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("分配隔离 loopback 端口失败: %w", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", fmt.Errorf("释放隔离 loopback 端口失败: %w", err)
	}
	if err := validateLoopbackListener(address); err != nil {
		return "", err
	}
	return address, nil
}

func newIsolatedDirectories() (isolatedDirectories, error) {
	root, err := os.MkdirTemp("", "cursor-byok-e2e-")
	if err != nil {
		return isolatedDirectories{}, fmt.Errorf("创建隔离根目录失败: %w", err)
	}
	return isolatedDirectories{
		root:         root,
		home:         filepath.Join(root, "home"),
		appData:      filepath.Join(root, "appdata", "roaming"),
		localAppData: filepath.Join(root, "appdata", "local"),
	}, nil
}

func applyIsolatedEnvironment(dirs isolatedDirectories) error {
	for key, value := range map[string]string{
		"USERPROFILE":  dirs.home,
		"HOME":         dirs.home,
		"APPDATA":      dirs.appData,
		"LOCALAPPDATA": dirs.localAppData,
	} {
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("设置隔离环境变量 %s 失败: %w", key, err)
		}
	}
	return nil
}

func buildCursorChildEnvironment(base []string, dirs isolatedDirectories, caPath string) []string {
	values := envValues(base)
	values["USERPROFILE"] = dirs.home
	values["HOME"] = dirs.home
	values["APPDATA"] = dirs.appData
	values["LOCALAPPDATA"] = dirs.localAppData
	values["NODE_EXTRA_CA_CERTS"] = caPath
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func envValues(items []string) map[string]string {
	values := make(map[string]string, len(items))
	for _, item := range items {
		key, value, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		values[strings.ToUpper(key)] = value
	}
	return values
}

func readConfig(path string) (serverconfig.Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return serverconfig.Config{}, fmt.Errorf("读取原配置失败: %w", err)
	}
	var config serverconfig.Config
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return serverconfig.Config{}, fmt.Errorf("解析原配置失败: %w", err)
	}
	return serverconfig.NormalizeConfig(config)
}

func writeConfig(path string, config serverconfig.Config) error {
	raw, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func waitForHealth(host *backend.Host) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := host.HealthCheck(ctx); err != nil {
		return fmt.Errorf("隔离 backend 健康检查失败: %w", err)
	}
	return nil
}

func stopHost(host *backend.Host) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = host.Stop(ctx)
}

func stopProxy(proxy *mitm.ProxyServer) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = proxy.Stop(ctx)
}
