//go:build windows

package forwarder

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// spawnAndReap 启动一个立即退出的子进程并等待其结束，返回其 PID。
// 该 PID 已退出，用于模拟「幽灵进程」场景（进程已死，但锁文件仍记录它）。
func spawnAndReap(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("cmd", "/c", "exit /b 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn child: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait child: %v", err)
	}
	return pid
}

// TestProcessTableContainsPID 验证进程表枚举：自身（存活）在表中，已退出的子进程不在。
func TestProcessTableContainsPID(t *testing.T) {
	if !processTableContainsPID(os.Getpid()) {
		t.Fatalf("processTableContainsPID(%d) = false, want true for self", os.Getpid())
	}
	deadPID := spawnAndReap(t)
	if processTableContainsPID(deadPID) {
		t.Fatalf("processTableContainsPID(%d) = true, want false for reaped child", deadPID)
	}
}

// TestProcessStartTime_DeadPID 验证 processStartTime 对已退出进程返回「不存活」，
// 而不是被残留的 EPROCESS 对象骗过（OpenProcess 仍能打开）。
func TestProcessStartTime_DeadPID(t *testing.T) {
	deadPID := spawnAndReap(t)
	startedAt, alive := processStartTime(deadPID)
	if alive {
		t.Fatalf("processStartTime(%d) alive=true, want false for reaped child", deadPID)
	}
	if !startedAt.IsZero() {
		t.Fatalf("processStartTime(%d) startedAt=%v, want zero for dead pid", deadPID, startedAt)
	}
}

// TestOtherProcessLockIsStale_ReapedPID 端到端验证孤儿锁回收：锁持有者已退出时，
// otherProcessLockIsStale 必须判为 stale，这样 acquireConversationFileLock 才能
// 立即删除锁继续获取，而不是等 30 分钟 mtime 兜底。
func TestOtherProcessLockIsStale_ReapedPID(t *testing.T) {
	deadPID := spawnAndReap(t)
	lockPath := filepath.Join(t.TempDir(), "conversation.lock")
	content := fmt.Sprintf("pid=%d\nowner=%d-1\ncreated_at=%s\n",
		deadPID, deadPID, time.Now().UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(lockPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
	if !otherProcessLockIsStale(deadPID, lockPath) {
		t.Fatalf("otherProcessLockIsStale(%d) = false, want true for reaped lock holder", deadPID)
	}
}

// TestOtherProcessLockIsStale_Self 验证活进程（自身）持有的锁不会被误判为 stale。
func TestOtherProcessLockIsStale_Self(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "conversation.lock")
	// created_at 用当前时间：自身进程启动必然早于锁创建时刻，锁是「活进程创建且持有」，
	// 不应被回收（既不是 PID 复用，也不是幽灵进程）。
	content := fmt.Sprintf("pid=%d\nowner=%d-1\ncreated_at=%s\n",
		os.Getpid(), os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(lockPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
	if otherProcessLockIsStale(os.Getpid(), lockPath) {
		t.Fatalf("otherProcessLockIsStale(self) = true, want false for live holder")
	}
}
