package forwarder

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
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
	if service.visionArchive == nil {
		t.Fatalf("NewService 未初始化 visionArchive：图片识图归档会触发 nil map 写入 panic")
	}
	if service.visionArchiveLimit <= 0 {
		t.Fatalf("NewService 未设置 visionArchiveLimit，归档无上限会无限增长")
	}

	// 实际写入验证：曾经出 bug 的写法（向 nil map 写入）必须能正常工作。
	service.visionRuns["test-key"] = &visionDelegationRun{ID: "test-key"}
	service.visionCache["test-key"] = visionCacheEntry{text: "ok"}
	service.visionImageFiles["conv"] = []string{"/tmp/x.png"}
}

// TestVisionArchiveRoundTrip 验证会话级图片识图归档的写入与命中：
// 同一会话同一图片内容（Data 字节）写入后能命中；不同会话不串档。
func TestVisionArchiveRoundTrip(t *testing.T) {
	t.Parallel()
	service := NewService(t.TempDir(), nilResolver{})
	image := &modeladapter.ImageContent{Data: []byte("fake-image-bytes-01"), MIMEType: "image/png"}

	keys := service.visionArchiveKeys("conv-a", image)
	if len(keys) == 0 {
		t.Fatalf("visionArchiveKeys 返回空键列表")
	}
	if text, ok := service.lookupVisionArchive("conv-a", keys); ok {
		t.Fatalf("未写入前不应命中归档，got %q", text)
	}

	service.storeVisionArchive("conv-a", keys, "[图片识图结果（视觉委派 · 由 gpt-5.6-luna 提供）]\n画面描述")

	if text, ok := service.lookupVisionArchive("conv-a", keys); !ok || text == "" {
		t.Fatalf("写入后应命中归档，ok=%v text=%q", ok, text)
	}
	// 不同会话相同图片内容不应串档（键含 conversationID）。
	otherKeys := service.visionArchiveKeys("conv-b", image)
	if text, ok := service.lookupVisionArchive("conv-b", otherKeys); ok {
		t.Fatalf("不同会话不应命中归档，got %q", text)
	}
}

// TestVisionArchivePathRecovery 验证历史恢复场景（图片字节丢失只剩 Path）仍能命中归档：
// 同一文件内容在 Data 形态与 Path 形态下应命中同一归档条目。
func TestVisionArchivePathRecovery(t *testing.T) {
	t.Parallel()
	service := NewService(t.TempDir(), nilResolver{})

	content := []byte("same-image-content-for-archive-test")
	file := filepath.Join(t.TempDir(), "img.png")
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatalf("write temp image: %v", err)
	}

	// 首次以 Data 形态识图并归档。
	dataKeys := service.visionArchiveKeys("conv-a", &modeladapter.ImageContent{Data: content, MIMEType: "image/png"})
	service.storeVisionArchive("conv-a", dataKeys, "[图片识图结果（视觉委派）]\n归档内容")

	// 历史恢复后只剩 Path：读取同一文件内容应命中同一归档。
	pathKeys := service.visionArchiveKeys("conv-a", &modeladapter.ImageContent{Path: file})
	if text, ok := service.lookupVisionArchive("conv-a", pathKeys); !ok {
		t.Fatalf("Path 形态应命中 Data 形态写入的归档（内容哈希相同）")
	} else if text == "" {
		t.Fatalf("命中归档但文本为空")
	}
}

// TestVisionAllArchivedAndReplace 验证「被打断后继续」的归档引用路径：
// 全部图片已归档时 visionAllArchived 返回 true，替换后消息不再含图片 part，
// 且不触发识图模型调用（纯引用上下文）。
func TestVisionAllArchivedAndReplace(t *testing.T) {
	t.Parallel()
	service, provider := newEmpiricalService(t)

	contentA := []byte("archived-image-a")
	contentB := []byte("archived-image-b")
	service.storeVisionArchive(
		"conv-arch",
		service.visionArchiveKeys("conv-arch", &modeladapter.ImageContent{Data: contentA, MIMEType: "image/png"}),
		"[图片识图结果（视觉委派）]\nA 的内容",
	)
	service.storeVisionArchive(
		"conv-arch",
		service.visionArchiveKeys("conv-arch", &modeladapter.ImageContent{Data: contentB, MIMEType: "image/png"}),
		"[图片识图结果（视觉委派）]\nB 的内容",
	)

	messages := []modeladapter.Message{
		{Role: "user", ContentParts: []modeladapter.ContentPart{imagePartByData(contentA), imagePartByData(contentB)}},
	}
	if !service.visionAllArchived("conv-arch", messages) {
		t.Fatalf("全部图片已归档，visionAllArchived 应返回 true")
	}

	out := service.visionReplaceFromArchive("conv-arch", messages)
	if len(out) != 1 {
		t.Fatalf("输出消息数 = %d，期望 1", len(out))
	}
	for i, part := range out[0].ContentParts {
		if part.Type != "text" {
			t.Errorf("parts[%d] 仍是 type=%q（应替换为归档文本）", i, part.Type)
		}
	}
	if got := provider.calls.Load(); got != 0 {
		t.Fatalf("归档引用不应调用识图模型，StartStream_calls=%d", got)
	}

	// 部分未归档时不满足 all-archived。
	mixed := []modeladapter.Message{
		{Role: "user", ContentParts: []modeladapter.ContentPart{imagePartByData(contentA), imagePartByData([]byte("never-seen-image"))}},
	}
	if service.visionAllArchived("conv-arch", mixed) {
		t.Fatalf("含未归档图片时 visionAllArchived 应返回 false")
	}
}

