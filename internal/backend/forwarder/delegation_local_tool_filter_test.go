package forwarder

import "testing"

// UpdateCurrentStep 只对 Cursor 客户端托管的子会话有意义：它写的是父会话 Task
// 卡片的状态行。本地委派 worker 同样以子会话身份编译工具，但没有 Task 卡片，
// 经 exec bridge 只会拿到 "unsupported exec tool"。
func TestDelegatedWorkerCannotReachCursorOnlyTools(t *testing.T) {
	for _, toolName := range []string{
		"Shell",
		"AwaitShell",
		"WriteShellStdin",
		"ForceBackgroundShell",
		"ForceBackgroundSubagent",
		updateCurrentStepToolName,
	} {
		if !delegatedToolNeedsCursorInteraction(toolName) {
			t.Errorf("delegatedToolNeedsCursorInteraction(%q) = false, want true", toolName)
		}
	}

	for _, toolName := range []string{"Read", "Grep", "Glob", "Ls", "Write"} {
		if delegatedToolNeedsCursorInteraction(toolName) {
			t.Errorf("delegatedToolNeedsCursorInteraction(%q) = true, want false", toolName)
		}
	}
}
