//go:build windows

package execbridge

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"cursor/internal/processutil"
)

func TestExplicitShellProfileWithoutConsole(t *testing.T) {
	// 真实执行两层 PowerShell，并从内层查询控制台，覆盖子进程重新分配窗口的情况。
	probe := `Add-Type -Name ConsoleProbe -Namespace Test -MemberDefinition '[System.Runtime.InteropServices.DllImport("kernel32.dll")] public static extern System.IntPtr GetConsoleWindow();'; if ([Test.ConsoleProbe]::GetConsoleWindow() -ne [IntPtr]::Zero) { exit 23 }; Write-Output 'console-free'; exit 0`
	wrapped, err := buildExplicitShellProfileCommand("powershell", probe)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(wrapped)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	processutil.HideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "console-free") {
		t.Fatalf("Shell 子进程未静默执行: %v, %s", err, output)
	}
}
