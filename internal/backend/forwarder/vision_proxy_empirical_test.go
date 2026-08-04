package forwarder

// 实证测试：定位「视觉委派 pass 0-1ms 完成、18 张图未被真实识别」的静默路径。
// 构造 Service + 记录型 fake provider，直接调用 synthesizeMessageImages，
// 观察 Path-only / Data-only 图片在 goroutine 中的真实行为。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cursor/internal/backend/delegation"
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



// visionEnabledConfigProvider 返回启用了视觉委派的运行时配置，供端到端测试注入。
type visionEnabledConfigProvider struct{}

func (visionEnabledConfigProvider) DelegationRuntimeConfig() delegation.RuntimeConfig {
	return delegation.NormalizeRuntimeConfig(delegation.RuntimeConfig{
		Enabled:                 true,
		VisionDelegationEnabled: true,
		VisionModelID:           "gpt-5.6-luna",
		VisionMode:              "auto",
	})
}

// TestVisionSynthesizeReusesArchiveAcrossInterruption 端到端回归「被打断后继续」：
// 第一次 synthesize 识图并写入归档；随后模拟历史恢复（图片只剩空 part，由落地缓存
// 补回 Path）再次 synthesize——第二次必须命中归档，不新增识图模型调用，输出不含图片。
func TestVisionSynthesizeReusesArchiveAcrossInterruption(t *testing.T) {
	t.Parallel()
	service, provider := newEmpiricalService(t)
	service.multitaskDelegation.configProvider = visionEnabledConfigProvider{}

	content := []byte("user-uploaded-screenshot-bytes")
	first := []modeladapter.Message{
		{Role: "user", ContentParts: []modeladapter.ContentPart{
			modeladapter.NewTextContentPart("看看这张图有什么问题"),
			imagePartByData(content),
		}},
	}

	// 第一次：真实识图，调用识图模型并归档。
	out1 := service.synthesizeImageDescriptions(context.Background(), "req-interrupt-1", "conv-interrupt", first, "deepseek-v4-flash")
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("首次识图应调用 1 次识图模型，实际 %d", got)
	}
	if modeladapter.MessageHasImage(out1[0]) {
		t.Fatalf("首次识图后图片应被替换为识图结果文本，仍含图片 part")
	}
	if len(service.visionArchive) == 0 {
		t.Fatalf("首次识图后应写入会话级归档")
	}

	// 第二次：模拟被打断后用户"继续"——历史 checkpoint 只保留空 image part，
	// synthesize 内部按会话顺序从落地缓存补回 Path（与线上恢复路径一致）。
	second := []modeladapter.Message{
		{Role: "user", ContentParts: []modeladapter.ContentPart{
			modeladapter.NewTextContentPart("继续"),
			modeladapter.NewImageContentPart(&modeladapter.ImageContent{}), // 空 part，触发历史恢复
		}},
	}
	out2 := service.synthesizeImageDescriptions(context.Background(), "req-interrupt-2", "conv-interrupt", second, "deepseek-v4-flash")
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("第二次应命中归档不再调识图模型，StartStream_calls=%d（期望仍为 1）", got)
	}
	if modeladapter.MessageHasImage(out2[0]) {
		t.Fatalf("第二次输出不应再含图片 part（应替换为归档识图文本）")
	}
	text := ""
	for _, part := range out2[0].ContentParts {
		if part.Type == "text" {
			text += part.Text
		}
	}
	if !strings.Contains(text, "视觉委派") {
		t.Fatalf("第二次输出应包含归档识图文本，got %q", text)
	}
	if service.visionRuns["req-interrupt-2"] != nil {
		t.Fatalf("全归档命中时不应注册视觉委派运行")
	}
}

// TestVisionArchiveSurvivesServiceRestart 验证归档落盘后跨 Service 实例（模拟进程重启）
// 仍能命中：同一会话同一图片内容，新实例懒加载磁盘归档后直接引用识图结果，不重新识图。
func TestVisionArchiveSurvivesServiceRestart(t *testing.T) {
	t.Parallel()
	historyRoot := t.TempDir()

	content := []byte("restart-persisted-image-bytes")
	image := &modeladapter.ImageContent{Data: content, MIMEType: "image/png"}
	keys := func(service *Service) []string {
		return service.visionArchiveKeys("conv-restart", image)
	}

	// 第一个实例：识图并落盘归档。
	first := NewService(historyRoot, nilResolver{})
	first.storeVisionArchive("conv-restart", keys(first), "[图片识图结果（视觉委派）]\n重启后仍应命中")

	// 第二个实例（模拟进程重启）：归档不在内存，应从磁盘懒加载后命中。
	second := NewService(historyRoot, nilResolver{})
	if len(second.visionArchive) != 0 {
		t.Fatalf("新实例不应自带内存归档，got %d 条", len(second.visionArchive))
	}
	text, ok := second.lookupVisionArchive("conv-restart", keys(second))
	if !ok {
		t.Fatalf("新实例应从磁盘加载归档并命中")
	}
	if !strings.Contains(text, "重启后仍应命中") {
		t.Fatalf("命中文本与落盘内容不一致，got %q", text)
	}
}

func shortLog(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}
