package forwarder

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setHomeForSkillTest 让 os.UserHomeDir 指向临时目录（Windows 读 USERPROFILE，Unix 读 HOME）。
func setHomeForSkillTest(t *testing.T, dir string) {
	t.Helper()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("USERPROFILE", dir)
	default:
		t.Setenv("HOME", dir)
	}
}

func writeTestSkill(t *testing.T, root, name, description string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill %s: %v", name, err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill %s: %v", name, err)
	}
}

// TestGoalModeForcesGoalLoopActivation 验证 /goal 命令在命令前缀被剥离后，
// goal 模式仍强制注入 goal-loop 技能；非 goal 模式下不相关请求不会激活它。
func TestGoalModeForcesGoalLoopActivation(t *testing.T) {
	home := t.TempDir()
	setHomeForSkillTest(t, home)
	cursorSkills := filepath.Join(home, ".cursor", "skills")
	writeTestSkill(t, cursorSkills, "goal-loop", "Execute /goal commands to completion with verification gates.")
	writeTestSkill(t, cursorSkills, "unrelated-skill", "Do unrelated bookkeeping for the weather report.")

	store := NewSkillStore(cursorSkills)

	// 非 goal 模式：请求文本与 goal-loop 无关，不应注入（即使无任何技能被激活）。
	prompt, count, err := store.BuildActivatedSkillsPromptSection("整理今天的天气报告", nil)
	if err != nil {
		t.Fatalf("non-goal activation: %v", err)
	}
	_ = count
	if strings.Contains(prompt, "goal-loop") {
		t.Fatalf("non-goal mode must not inject goal-loop, got: %s", prompt)
	}

	// goal 模式：即使请求文本不含 goal 关键词，也必须注入 goal-loop。
	goalPrompt, goalCount, err := store.BuildActivatedSkillsPromptSectionGoal("整理今天的天气报告", nil)
	if err != nil {
		t.Fatalf("goal activation: %v", err)
	}
	if goalCount == 0 || !strings.Contains(goalPrompt, "goal-loop") {
		t.Fatalf("goal mode must force inject goal-loop, count=%d prompt=%s", goalCount, goalPrompt)
	}
}
