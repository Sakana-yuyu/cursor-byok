package forwarder

// 实证测试：定位「视觉委派 pass 0-1ms 完成、18 张图未被真实识别」的静默路径。
// 构造 Service + 记录型 fake provider，直接调用 synthesizeMessageImages，
// 观察 Path-only / Data-only 图片在 goroutine 中的真实行为。

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
)

// countingProvider 记录 StartStream 调用次数与收到的文本增量，模拟真实 provider。
type countingProvider struct {
	calls   atomic.Int32
	mu      sync.Mutex
	deltas  []string
	blockMs time.Duration
}

func (p *countingProvider) StartStream(_ context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	p.calls.Add(1)
	// 模拟一次真实的识图子调用：一小段网络延迟 + 文本增量。
	if p.blockMs > 0 {
		time.Sleep(p.blockMs)
	}
	if err := sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "画面主体是浏览器截图，包含终端与代码编辑器"}); err != nil {
		return err
	}
	return nil
}

func newEmpiricalService(t *testing.T) (*Service, *countingProvider) {
	t.Helper()
	service := NewService(t.TempDir(), nilResolver{})
	provider := &countingProvider{}
	service.provider = provider
	return service, provider
}

func imagePartByPath(path string) modeladapter.ContentPart {
	return modeladapter.NewImageContentPart(&modeladapter.ImageContent{Path: path})
}

func imagePartByData(data []byte) modeladapter.ContentPart {
	return modeladapter.NewImageContentPart(&modeladapter.ImageContent{MIMEType: "image/png", Data: data})
}

// TestVisionProxyEmpiricalPathOnlyImages 模拟历史快照恢复后的场景：
// 18 张图只有 Path、无 Data。验证 goroutine 是否真的调用 StartStream。
func TestVisionProxyEmpiricalPathOnlyImages(t *testing.T) {
	service, provider := newEmpiricalService(t)
	parts := make([]modeladapter.ContentPart, 0, 18)
	for i := 0; i < 18; i++ {
		parts = append(parts, imagePartByPath(fmt.Sprintf("C:/tmp/screenshot_%d.png", i)))
	}
	message := modeladapter.Message{Role: "user", ContentParts: parts}
	config := visionProxyConfig{enabled: true, visionID: "vid", visionName: "gpt-5.6-luna", mode: "auto"}

	started := time.Now()
	out := service.synthesizeMessageImages(context.Background(), "req-emp-path", "conv-emp", message, config)
	elapsed := time.Since(started)

	t.Logf("elapsed_ms=%d StartStream_calls=%d", elapsed.Milliseconds(), provider.calls.Load())
	if got := provider.calls.Load(); got == 0 {
		t.Fatalf("Path-only 图片：StartStream 一次都没被调用 —— 识图 goroutine 根本没干活，pass 秒回但图从未识别")
	}
	for i, part := range out.ContentParts {
		if i >= 18 {
			break
		}
		if part.Type != "text" {
			t.Errorf("parts[%d] 仍是 type=%q（未被替换为识图结果文本）", i, part.Type)
		} else {
			t.Logf("parts[%d] = %s", i, shortLog(part.Text))
		}
	}
}

// TestVisionProxyEmpiricalDataImages 模拟真实上传（字节在内存中）的场景。
func TestVisionProxyEmpiricalDataImages(t *testing.T) {
	service, provider := newEmpiricalService(t)
	parts := make([]modeladapter.ContentPart, 0, 18)
	for i := 0; i < 18; i++ {
		parts = append(parts, imagePartByData([]byte(fmt.Sprintf("fake-png-bytes-%d", i))))
	}
	message := modeladapter.Message{Role: "user", ContentParts: parts}
	config := visionProxyConfig{enabled: true, visionID: "vid", visionName: "gpt-5.6-luna", mode: "auto"}

	started := time.Now()
	out := service.synthesizeMessageImages(context.Background(), "req-emp-data", "conv-emp", message, config)
	elapsed := time.Since(started)

	t.Logf("elapsed_ms=%d StartStream_calls=%d", elapsed.Milliseconds(), provider.calls.Load())
	if got := provider.calls.Load(); got != 18 {
		t.Fatalf("Data-only 图片：期望 18 次 StartStream，实际 %d", got)
	}
	if len(out.ContentParts) != 18 {
		t.Fatalf("输出 part 数量 = %d，期望 18", len(out.ContentParts))
	}
	for i, part := range out.ContentParts {
		if part.Type != "text" {
			t.Errorf("parts[%d] 仍是 type=%q", i, part.Type)
		}
	}
	t.Logf("识别结果已替换为文本（示例）：%s", shortLog(out.ContentParts[0].Text))
}



func shortLog(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}
