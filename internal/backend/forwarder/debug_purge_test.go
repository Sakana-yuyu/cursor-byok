package forwarder

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestPurgeDebugLogsDropsStaleWrites 覆盖清理与落盘的协调：清理前入队（世代号更旧）
// 的事件必须被丢弃，否则它会把刚删掉的 debug 目录重建回来，用户看到「清理没生效」。
func TestPurgeDebugLogsDropsStaleWrites(t *testing.T) {
	staleEpoch := debugPurge.currentEpoch()

	if err := PurgeDebugLogs(func() error { return nil }); err != nil {
		t.Fatalf("PurgeDebugLogs: %v", err)
	}

	if debugPurge.beginWrite(staleEpoch) {
		debugPurge.endWrite()
		t.Fatal("清理前入队的事件应被丢弃，实际拿到了写盘许可")
	}

	fresh := debugPurge.currentEpoch()
	if !debugPurge.beginWrite(fresh) {
		t.Fatal("清理后入队的事件应能正常落盘")
	}
	debugPurge.endWrite()
}

// TestPurgeDebugLogsExcludesConcurrentWrites 验证清理执行期间没有落盘在进行：
// remove 回调看到的目录状态就是最终状态。
func TestPurgeDebugLogsExcludesConcurrentWrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "debug")
	recorder := &debugRecorder{historyRoot: dir}

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recorder.writeJob(debugWriteJob{
				dir:      dir,
				filename: "runtime.jsonl",
				payload:  []byte(`{"event":"test"}`),
				epoch:    debugPurge.currentEpoch(),
			})
		}()
	}

	var removedEmpty bool
	err := PurgeDebugLogs(func() error {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		// 持写锁期间不可能有并发落盘，删除后目录必然不存在。
		_, statErr := os.Stat(dir)
		removedEmpty = os.IsNotExist(statErr)
		return nil
	})
	wg.Wait()

	if err != nil {
		t.Fatalf("PurgeDebugLogs: %v", err)
	}
	if !removedEmpty {
		t.Fatal("清理窗口内目录应已删除且不被并发落盘重建")
	}
}

// TestPurgeDebugLogsNilRemove 空回调只推进世代号，不应 panic。
func TestPurgeDebugLogsNilRemove(t *testing.T) {
	before := debugPurge.currentEpoch()
	if err := PurgeDebugLogs(nil); err != nil {
		t.Fatalf("PurgeDebugLogs(nil): %v", err)
	}
	if debugPurge.currentEpoch() == before {
		t.Fatal("世代号应推进，使先前入队的事件失效")
	}
}
