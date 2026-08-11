package backend

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"cursor/internal/backend/delegation"
	delegationexecutors "cursor/internal/backend/delegation/executors"
	"cursor/internal/processutil"
)

// delegationExecutorInstaller 只接受后端给出的固定包名，避免前端输入成为命令参数。
type delegationExecutorInstaller interface {
	Install(context.Context, string) error
}

type npmExecutorInstaller struct{}

func newNPMExecutorInstaller() delegationExecutorInstaller {
	return npmExecutorInstaller{}
}

func (npmExecutorInstaller) Install(ctx context.Context, packageName string) error {
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

var installableDelegationExecutorPackages = map[delegation.ExecutorID]string{
	delegationexecutors.ClaudeCodeExecutorID: "@anthropic-ai/claude-code",
	delegationexecutors.CodexCLIExecutorID:   "@openai/codex",
	delegationexecutors.GeminiCLIExecutorID:  "@google/gemini-cli",
}

// InstallDelegationExecutor 安装已审核的 npm CLI，并在完成后强制重新探测。
// Cursor 与 Kiro 的安装方式不在本地白名单中，必须使用其官方下载入口。
func (host *Host) InstallDelegationExecutor(ctx context.Context, rawID string) (delegation.ExecutorSnapshot, error) {
	if host == nil || host.executorRegistry == nil {
		return delegation.ExecutorSnapshot{}, fmt.Errorf("委派执行器服务未初始化")
	}
	id := delegation.ExecutorID(strings.TrimSpace(rawID))
	packageName, supported := installableDelegationExecutorPackages[id]
	if !supported {
		return delegation.ExecutorSnapshot{}, fmt.Errorf("执行器 %q 不支持应用内安装，请使用官方下载入口", id)
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
