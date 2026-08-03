package forwarder

import (
	"context"
	"testing"
	"time"

	legacyruntime "cursor/internal/runtime"
)

// nilResolver 是 ChannelResolver 的最小桩实现，仅用于驱动 NewService 构造路径。
// 目的：回归测试 NewService（生产构造函数）是否正确初始化了视觉委派相关的 map。
type nilResolver struct{}

func (nilResolver) SelectChannelForModel(context.Context, string) (*legacyruntime.ResolvedChannel, error) {
	return nil, nil
}
func (nilResolver) ProviderStreamIdleTimeout(context.Context) time.Duration { return 0 }
func (nilResolver) TurnStaleTimeout(context.Context) time.Duration          { return 0 }
func (nilResolver) NativeDelegationProgressTimeout(context.Context) time.Duration {
	return 0
}

// TestNewServiceInitializesVisionMaps 回归 v0.0.78 的崩溃 bug：
// 生产构造函数 NewService 曾漏掉 visionRuns / visionCache / visionImageFiles
// 三个 map 的初始化，导致视觉委派一触发就在 beginVisionRun 向 nil map 写入，
// 抛出 "assignment to entry in nil map" panic，杀死整个 Wails 主进程。
//
// 此测试断言 NewService 返回的实例中这三个 map 均非 nil，且可安全写入。
func TestNewServiceInitializesVisionMaps(t *testing.T) {
	t.Parallel()
	service := NewService(t.TempDir(), nilResolver{})

	if service.visionRuns == nil {
		t.Fatalf("NewService 未初始化 visionRuns：视觉委派会触发 nil map 写入 panic")
	}
	if service.visionCache == nil {
		t.Fatalf("NewService 未初始化 visionCache：视觉委派缓存会触发 nil map 写入 panic")
	}
	if service.visionImageFiles == nil {
		t.Fatalf("NewService 未初始化 visionImageFiles：图片落地缓存会触发 nil map 写入 panic")
	}

	// 实际写入验证：曾经出 bug 的写法（向 nil map 写入）必须能正常工作。
	service.visionRuns["test-key"] = &visionDelegationRun{ID: "test-key"}
	service.visionCache["test-key"] = visionCacheEntry{text: "ok"}
	service.visionImageFiles["conv"] = []string{"/tmp/x.png"}
}

