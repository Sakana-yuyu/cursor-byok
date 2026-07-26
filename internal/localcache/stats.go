// Package localcache 暴露本地（进程内）LLM 响应缓存的轻量命中统计。
//
// 这些计数器仅统计 LOCAL 响应缓存的命中/未命中与由此节省的 token，
// 与 provider 侧 prompt-cache 命中率完全独立，绝不混淆。所有导出函数均线程安全。
package localcache

import "sync"

// LocalCacheStats 是本地响应缓存的统计快照，带 JSON 标签供前端展示。
type LocalCacheStats struct {
	// Hits 表示本地缓存命中次数（直接回放缓存事件、未调用 provider）。
	Hits int64 `json:"hits"`
	// Misses 表示本地缓存未命中次数（正常调用 provider）。
	Misses int64 `json:"misses"`
	// SavedInputTokens 表示因命中而估算节省的输入 token 总数。
	SavedInputTokens int64 `json:"savedInputTokens"`
	// SavedOutputTokens 表示因命中而估算节省的输出 token 总数。
	SavedOutputTokens int64 `json:"savedOutputTokens"`
}

var (
	mu      sync.Mutex
	current LocalCacheStats
)

// RecordHit 记录一次本地缓存命中，并累加此次估算节省的 token。
func RecordHit(savedInputTokens, savedOutputTokens int64) {
	mu.Lock()
	defer mu.Unlock()
	current.Hits++
	if savedInputTokens > 0 {
		current.SavedInputTokens += savedInputTokens
	}
	if savedOutputTokens > 0 {
		current.SavedOutputTokens += savedOutputTokens
	}
}

// RecordMiss 记录一次本地缓存未命中。
func RecordMiss() {
	mu.Lock()
	defer mu.Unlock()
	current.Misses++
}

// Snapshot 返回当前统计的一份拷贝。
func Snapshot() LocalCacheStats {
	mu.Lock()
	defer mu.Unlock()
	return current
}
