package backend

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"cursor/internal/backend/delegation"
	delegationexecutors "cursor/internal/backend/delegation/executors"
	"cursor/internal/processutil"
)

// delegationExecutorInstaller 只接受后端给出的固定包名，避免前端输入成为命令参数。
type delegationExecutorInstaller interface {
	Install(context.Context, string) error
}

const (
	kiroCLIWindowsInstallTarget = "kiro-cli-windows"
	kiroCLIManifestURL          = "https://prod.download.cli.kiro.dev/stable/latest/manifest.json"
	kiroCLIDownloadBaseURL      = "https://prod.download.cli.kiro.dev/stable/"
	kiroCLIMaxInstallerSize     = int64(1024 * 1024 * 1024)
)

type builtInExecutorInstaller struct {
	httpClient *http.Client
}

type kiroCLIManifest struct {
	Packages []kiroCLIPackage `json:"packages"`
}

type kiroCLIPackage struct {
	Kind         string `json:"kind"`
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Download     string `json:"download"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
}

func newBuiltInExecutorInstaller() delegationExecutorInstaller {
	return builtInExecutorInstaller{httpClient: &http.Client{}}
}

func (installer builtInExecutorInstaller) Install(ctx context.Context, target string) error {
	if target == kiroCLIWindowsInstallTarget {
		return installer.installKiroCLIWindows(ctx)
	}
	return installNPMExecutor(ctx, target)
}

func installNPMExecutor(ctx context.Context, packageName string) error {
	npmPath, err := exec.LookPath("npm")
	if err != nil {
		return fmt.Errorf("未找到 npm；请先安装 Node.js LTS 后再安装执行器: %w", err)
	}
	command := exec.CommandContext(ctx, npmPath, "install", "--global", packageName)
	processutil.HideWindow(command)
	if err := command.Run(); err != nil {
		return fmt.Errorf("npm 安装执行器失败: %w", err)
	}
	return nil
}

// installKiroCLIWindows 从 Kiro 官方清单选择 MSI、校验哈希后安装，不执行远程脚本。
func (installer builtInExecutorInstaller) installKiroCLIWindows(ctx context.Context) error {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("Kiro CLI 应用内安装目前仅支持 Windows x64")
	}
	client := installer.httpClient
	if client == nil {
		client = &http.Client{}
	}
	manifestRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, kiroCLIManifestURL, nil)
	if err != nil {
		return fmt.Errorf("创建 Kiro CLI 清单请求失败: %w", err)
	}
	manifestResponse, err := client.Do(manifestRequest)
	if err != nil {
		return fmt.Errorf("获取 Kiro CLI 官方清单失败: %w", err)
	}
	defer manifestResponse.Body.Close()
	if manifestResponse.StatusCode != http.StatusOK {
		return fmt.Errorf("获取 Kiro CLI 官方清单失败: HTTP %d", manifestResponse.StatusCode)
	}
	var manifest kiroCLIManifest
	if err := json.NewDecoder(io.LimitReader(manifestResponse.Body, 2*1024*1024)).Decode(&manifest); err != nil {
		return fmt.Errorf("解析 Kiro CLI 官方清单失败: %w", err)
	}
	packageInfo, ok := selectKiroCLIWindowsPackage(manifest.Packages)
	if !ok {
		return fmt.Errorf("Kiro CLI 官方清单中没有 Windows x64 MSI 安装包")
	}
	if !isSafeKiroCLIPackage(packageInfo) {
		return fmt.Errorf("Kiro CLI 官方清单中的安装包信息无效")
	}

	installerFile, err := os.CreateTemp("", "cursor-byok-kiro-cli-*.msi")
	if err != nil {
		return fmt.Errorf("创建 Kiro CLI 安装临时文件失败: %w", err)
	}
	installerPath := installerFile.Name()
	defer os.Remove(installerPath)
	defer installerFile.Close()

	downloadRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, kiroCLIDownloadBaseURL+packageInfo.Download, nil)
	if err != nil {
		return fmt.Errorf("创建 Kiro CLI 下载请求失败: %w", err)
	}
	downloadResponse, err := client.Do(downloadRequest)
	if err != nil {
		return fmt.Errorf("下载 Kiro CLI 安装包失败: %w", err)
	}
	defer downloadResponse.Body.Close()
	if downloadResponse.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 Kiro CLI 安装包失败: HTTP %d", downloadResponse.StatusCode)
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(installerFile, hash), io.LimitReader(downloadResponse.Body, packageInfo.Size+1))
	if err != nil {
		return fmt.Errorf("写入 Kiro CLI 安装包失败: %w", err)
	}
	if written != packageInfo.Size {
		return fmt.Errorf("Kiro CLI 安装包大小校验失败")
	}
	if !strings.EqualFold(fmt.Sprintf("%x", hash.Sum(nil)), packageInfo.SHA256) {
		return fmt.Errorf("Kiro CLI 安装包校验失败")
	}
	if err := installerFile.Close(); err != nil {
		return fmt.Errorf("关闭 Kiro CLI 安装包失败: %w", err)
	}
	msiExecPath, err := windowsMSIExecPath()
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, msiExecPath, "/i", installerPath, "/quiet", "/norestart")
	processutil.HideWindow(command)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("Kiro CLI 安装失败: %w%s", err, compactInstallerOutput(output))
	}
	return nil
}

func selectKiroCLIWindowsPackage(packages []kiroCLIPackage) (kiroCLIPackage, bool) {
	for _, packageInfo := range packages {
		if packageInfo.OS == "windows" && packageInfo.Architecture == "x86_64" && packageInfo.Kind == "msi" {
			return packageInfo, true
		}
	}
	return kiroCLIPackage{}, false
}

func isSafeKiroCLIPackage(packageInfo kiroCLIPackage) bool {
	return strings.HasSuffix(packageInfo.Download, ".msi") &&
		path.Clean(packageInfo.Download) == packageInfo.Download &&
		!strings.HasPrefix(packageInfo.Download, "/") &&
		!hasUnsafeKiroCLIPathSegment(packageInfo.Download) &&
		!strings.ContainsAny(packageInfo.Download, "\\?#") &&
		len(packageInfo.SHA256) == sha256.Size*2 &&
		strings.Trim(packageInfo.SHA256, "0123456789abcdefABCDEF") == "" &&
		packageInfo.Size > 0 && packageInfo.Size <= kiroCLIMaxInstallerSize
}

func hasUnsafeKiroCLIPathSegment(download string) bool {
	for _, segment := range strings.Split(download, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func windowsMSIExecPath() (string, error) {
	systemRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	if systemRoot == "" {
		return "", fmt.Errorf("未找到 Windows 系统目录，无法启动 MSI 安装程序")
	}
	return filepath.Join(systemRoot, "System32", "msiexec.exe"), nil
}

func compactInstallerOutput(output []byte) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return ""
	}
	if len(text) > 512 {
		text = text[:512]
	}
	return ": " + text
}

var installableDelegationExecutorPackages = map[delegation.ExecutorID]string{
	delegationexecutors.ClaudeCodeExecutorID: "@anthropic-ai/claude-code",
	delegationexecutors.CodexCLIExecutorID:   "@openai/codex",
	delegationexecutors.GeminiCLIExecutorID:  "@google/gemini-cli",
	delegationexecutors.KiroCLIExecutorID:    kiroCLIWindowsInstallTarget,
}

// InstallDelegationExecutor 安装已审核的内置 CLI，并在完成后强制重新探测。
func (host *Host) InstallDelegationExecutor(ctx context.Context, rawID string) (delegation.ExecutorSnapshot, error) {
	if host == nil || host.executorRegistry == nil {
		return delegation.ExecutorSnapshot{}, fmt.Errorf("委派执行器服务未初始化")
	}
	id := delegation.ExecutorID(strings.TrimSpace(rawID))
	packageName, supported := installableDelegationExecutorPackages[id]
	if !supported {
		return delegation.ExecutorSnapshot{}, fmt.Errorf("执行器 %q 不支持应用内安装", id)
	}
	if _, exists := host.executorRegistry.Snapshot(id); !exists {
		return delegation.ExecutorSnapshot{}, fmt.Errorf("执行器 %q 未注册", id)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	host.executorMu.Lock()
	if host.executorInstalls == nil {
		host.executorInstalls = make(map[delegation.ExecutorID]struct{})
	}
	if _, running := host.executorInstalls[id]; running {
		host.executorMu.Unlock()
		return delegation.ExecutorSnapshot{}, fmt.Errorf("%s 正在安装中", id)
	}
	host.executorInstalls[id] = struct{}{}
	installer := host.executorInstaller
	host.executorMu.Unlock()
	defer func() {
		host.executorMu.Lock()
		delete(host.executorInstalls, id)
		host.executorMu.Unlock()
	}()

	if installer == nil {
		return delegation.ExecutorSnapshot{}, fmt.Errorf("执行器安装服务未初始化")
	}
	if err := installer.Install(ctx, packageName); err != nil {
		return delegation.ExecutorSnapshot{}, err
	}
	if _, err := host.executorRegistry.Probe(ctx, id, true); err != nil {
		return delegation.ExecutorSnapshot{}, fmt.Errorf("安装后检查 %s 失败: %w", id, err)
	}
	snapshot, _ := host.executorRegistry.Snapshot(id)
	return snapshot, nil
}
