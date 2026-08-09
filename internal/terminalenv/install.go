// install.go 提供 PowerShell 7 / Python 3 的自动安装能力（Windows，基于 winget）。
// 安装是长时操作：RPC 立即返回，进度通过 wails application Event 推送，
// 前端 EventsOn 监听 terminalenv:install-* 事件刷新 UI（与 updater 一致）。
package terminalenv

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"cursor/internal/logger"
	"cursor/internal/processutil"
)

const (
	// 事件名：前端用 EventsOn 监听。
	EventInstallProgress = "terminalenv:install-progress"
	EventInstallDone     = "terminalenv:install-done"

	// 安装目标。
	TargetPowerShell = "powershell"
	TargetPython     = "python"

	// 进度阶段。
	stageDownloading = "downloading"
	stageInstalling  = "installing"
	stageDone        = "done"
	stageError       = "error"
)

// winget 包 ID。
var wingetPackageIDs = map[string]string{
	TargetPowerShell: "Microsoft.PowerShell",
	TargetPython:     "Python.Python.3.13",
}

// installTargetName 返回目标的中文名，用于进度文案。
func installTargetName(target string) string {
	switch target {
	case TargetPowerShell:
		return "PowerShell 7"
	case TargetPython:
		return "Python 3"
	default:
		return target
	}
}

// ErrInstallAlreadyRunning 同一目标正在安装中，拒绝重复触发。
var ErrInstallAlreadyRunning = errors.New("安装已在进行中，请等待当前任务完成")

// activeInstalls 记录正在进行的安装目标，避免重复触发。
var activeInstalls sync.Map

// InstallProgress 是推送给前端的进度 payload。
type InstallProgress struct {
	Target  string `json:"target"`            // powershell / python
	Stage   string `json:"stage"`             // downloading / installing / done / error
	Message string `json:"message"`           // 人类可读文案
	Status  Status `json:"status,omitempty"`  // 仅 stage=done 时携带重新探测的结果
}

// Install 通过 winget 安装指定依赖。异步推送进度事件，成功后返回 nil。
// 调用方应在 goroutine 中调用（bridge 层负责），本函数本身是阻塞的。
func Install(ctx context.Context, target string) error {
	target = strings.ToLower(strings.TrimSpace(target))
	pkgID, ok := wingetPackageIDs[target]
	if !ok {
		return fmt.Errorf("不支持的安装目标 %q（仅支持 powershell / python）", target)
	}

	// 防重复：同目标正在装则拒绝。
	if _, loaded := activeInstalls.LoadOrStore(target, struct{}{}); loaded {
		return ErrInstallAlreadyRunning
	}
	defer activeInstalls.Delete(target)

	emitProgress := func(stage, message string) {
		emit(InstallProgress{Target: target, Stage: stage, Message: message})
	}

	emitProgress("pending", fmt.Sprintf("准备通过 winget 安装 %s...", installTargetName(target)))

	wingetPath, err := resolveWinget()
	if err != nil {
		emitProgress(stageError, "未找到 winget，请先从 Microsoft Store 安装「App Installer」后再试。")
		return err
	}

	// winget 静默安装；会触发 UAC 提权弹窗（系统级安装的正常行为）。
	cmd := exec.CommandContext(ctx, wingetPath,
		"install", "--id", pkgID, "-e",
		"--silent",
		"--accept-package-agreements",
		"--accept-source-agreements",
	)
	processutil.HideWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		emitProgress(stageError, fmt.Sprintf("启动 winget 失败: %v", err))
		return err
	}
	cmd.Stderr = cmd.Stdout // 合并到 stdout 一起读
	if err := cmd.Start(); err != nil {
		emitProgress(stageError, fmt.Sprintf("启动 winget 失败: %v", err))
		return err
	}

	// 逐行读 winget 输出，按关键词推送阶段性进度。
	// winget 不同版本/语言输出有差异，做宽松匹配；保留末尾输出便于失败诊断。
	lastLines := newRingBuffer(8)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	go func() {
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			lastLines.push(line)
			classifyAndEmit(line, target, emitProgress)
			logger.Infof("winget[%s] %s", target, line)
		}
	}()

	waitErr := cmd.Wait()
	if waitErr != nil {
		tail := strings.Join(lastLines.lines(), "\n")
		msg := fmt.Sprintf("安装 %s 失败（winget 退出错误: %v）。", installTargetName(target), waitErr)
		if tail != "" {
			msg += "\nwinget 输出末尾:\n" + tail
		}
		emitProgress(stageError, msg)
		return fmt.Errorf("winget install %s failed: %w\n%s", pkgID, waitErr, tail)
	}

	// 成功：重新探测，把新 Status 随 done 事件推送。
	status := Detect()
	emit(InstallProgress{
		Target:  target,
		Stage:   stageDone,
		Message: fmt.Sprintf("%s 安装完成。", installTargetName(target)),
		Status:  status,
	})
	return nil
}

// classifyAndEmit 根据 winget 输出行关键词推断阶段并推送进度。
func classifyAndEmit(line, target string, emit func(stage, message string)) {
	lower := strings.ToLower(line)
	name := installTargetName(target)
	switch {
	case containsAny(lower, "downloading", "已下载", "下载", "下载中", "starting package install"):
		emit(stageDownloading, fmt.Sprintf("正在下载 %s...", name))
	case containsAny(lower, "installing", "正在安装", "安装中", "successfully installed", "已成功安装"):
		emit(stageInstalling, fmt.Sprintf("正在安装 %s...", name))
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if sub != "" && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// resolveWinget 定位 winget 可执行文件：优先 PATH，再回退常见安装位置。
func resolveWinget() (string, error) {
	if p := lookupExecutable("winget.exe", "winget"); p != "" {
		return p, nil
	}
	candidates := []string{
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WindowsApps", "winget.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "WindowsApps", "winget.exe"),
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c, nil
		}
	}
	return "", errors.New("winget 未找到（需要 Windows 10 1809+ 预装或从 Microsoft Store 安装 App Installer）")
}

// emit 通过 wails application 推送进度事件；app 未就绪时仅记日志（不阻塞）。
func emit(progress InstallProgress) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventInstallProgress, progress)
		return
	}
	logger.Infof("terminalenv install event (no app): target=%s stage=%s", progress.Target, progress.Stage)
}

// ringBuffer 是一个固定容量的环形缓冲，保留最近 N 行输出用于失败诊断。
type ringBuffer struct {
	data []string
	size int
	idx  int
	full bool
}

func newRingBuffer(size int) *ringBuffer {
	if size < 1 {
		size = 1
	}
	return &ringBuffer{data: make([]string, size), size: size}
}

func (r *ringBuffer) push(line string) {
	r.data[r.idx] = line
	r.idx = (r.idx + 1) % r.size
	if r.idx == 0 {
		r.full = true
	}
}

func (r *ringBuffer) lines() []string {
	if !r.full {
		return r.data[:r.idx]
	}
	out := make([]string, 0, r.size)
	out = append(out, r.data[r.idx:]...)
	out = append(out, r.data[:r.idx]...)
	return out
}
