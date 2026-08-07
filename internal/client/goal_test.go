package client

import (
	"path/filepath"
	"testing"

	serverconfig "cursor/internal/backend/server/config"
)

// TestGoalBindingsEmpty 验证 goal bindings 在无 backend 时安全返回空结果。
func TestGoalBindingsEmpty(t *testing.T) {
	service := &ProxyService{store: serverconfig.NewStore(filepath.Join(t.TempDir(), "config.yml"), t.TempDir())}
	goals := service.GetGoals()
	if goals == nil {
		t.Fatal("GetGoals must return non-nil slice")
	}
	if len(goals) != 0 {
		t.Fatalf("expected empty goals, got %d", len(goals))
	}
	conversationID, err := service.StartGoal("", "")
	if err != nil || conversationID != "" {
		t.Fatalf("StartGoal with empty args must no-op, got (%q, %v)", conversationID, err)
	}
	if err := service.StopGoal(""); err != nil {
		t.Fatalf("StopGoal empty must no-op, got %v", err)
	}
}