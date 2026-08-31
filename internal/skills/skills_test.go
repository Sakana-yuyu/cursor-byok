package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSyncToCursorSkillsDirSkipsRemovedGoalLoop 验证自研 goal-loop 不再随包释放，
// 其他内置技能仍会同步，且再次同步时内容未变化则全部跳过。
func TestSyncToCursorSkillsDirSkipsRemovedGoalLoop(t *testing.T) {
	target := t.TempDir()

	first, err := SyncToCursorSkillsDir(target)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if first.Failed != 0 {
		t.Fatalf("first sync failed files: %d", first.Failed)
	}

	if _, err := os.Stat(filepath.Join(target, "goal-loop", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("goal-loop must not be released, stat error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "technical-stacks", "SKILL.md")); err != nil {
		t.Fatalf("expected another bundled skill to be released: %v", err)
	}
	if first.Written == 0 {
		t.Fatalf("expected bundled skills to be written on first sync, result=%+v", first)
	}

	second, err := SyncToCursorSkillsDir(target)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if second.Written != 0 {
		t.Fatalf("second sync must skip unchanged files, written=%d", second.Written)
	}
}

// TestSyncToCursorSkillsDirUpgradesOverwrittenFile 验证内置版本更新会覆盖用户改动，
// 但不会删除用户自建的其他技能。
func TestSyncToCursorSkillsDirUpgradesOverwrittenFile(t *testing.T) {
	target := t.TempDir()

	if _, err := SyncToCursorSkillsDir(target); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	skillPath := filepath.Join(target, "technical-stacks", "SKILL.md")
	original, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read initial bundled skill: %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("# user edited\n"), 0o644); err != nil {
		t.Fatalf("simulate user edit: %v", err)
	}

	userOwned := filepath.Join(target, "user-own-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userOwned), 0o755); err != nil {
		t.Fatalf("mkdir user skill: %v", err)
	}
	if err := os.WriteFile(userOwned, []byte("---\nname: user-own-skill\n---\n# user\n"), 0o644); err != nil {
		t.Fatalf("write user skill: %v", err)
	}

	result, err := SyncToCursorSkillsDir(target)
	if err != nil {
		t.Fatalf("sync after user edit: %v", err)
	}
	if result.Written == 0 {
		t.Fatalf("expected overwritten bundled file to be restored, result=%+v", result)
	}
	restored, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read restored bundled skill: %v", err)
	}
	if string(restored) != string(original) {
		t.Fatalf("bundled file not restored to built-in version")
	}
	if _, err := os.Stat(userOwned); err != nil {
		t.Fatalf("user-owned skill must be preserved: %v", err)
	}
}
