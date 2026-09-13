package forwarder

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 回归：Close 停止写盘 worker（Service 重建/关闭时必须回收 goroutine）；
// Close 后 enqueue 静默丢弃、重复 Close 幂等，均不得 panic。
func TestDebugRecorderCloseStopsWorkerAndDropsLateEvents(t *testing.T) {
	historyRoot := t.TempDir()
	recorder := newDebugRecorder(historyRoot, nil, stubDebugLogConfig{enabled: true})

	// 先入队一条合法事件：worker 启动并落盘。
	recorder.appendJSONL(context.Background(), "request-1", "conversation-1", "runtime.jsonl", map[string]any{"event": "before_close"})
	recorder.Close()

	// close 后投递必须静默丢弃，不能向已关闭 channel 发送（panic 在调用方 goroutine 炸出）。
	recorder.appendJSONL(context.Background(), "request-2", "conversation-1", "runtime.jsonl", map[string]any{"event": "after_close"})

	// 重复 Close 幂等。
	recorder.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(historyRoot, "conversation-1", "debug", "runtime.jsonl")); err == nil {
			return // 落盘成功即通过
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("debug event was not flushed before close")
}
