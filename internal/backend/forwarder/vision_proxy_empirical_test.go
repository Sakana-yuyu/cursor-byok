package forwarder

// 实证测试：定位「视觉委派 pass 0-1ms 完成、18 张图未被真实识别」的静默路径。
// 构造 Service + 记录型 fake provider，直接调用 synthesizeMessageImages，
// 观察 Path-only / Data-only 图片在 goroutine 中的真实行为。

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
)

// countingProvider 记录 StartStream 调用次数与收到的文本增量，模拟真实 provider。
type countingProvider struct {
	calls                 atomic.Int32
	active                atomic.Int32
	peak                  atomic.Int32
	mu                    sync.Mutex
	deltas                []string
	blockMs               time.Duration
	blockUntilContextDone bool
	canceled              atomic.Bool
}

func (p *countingProvider) StartStream(ctx context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	p.calls.Add(1)
	active := p.active.Add(1)
	defer p.active.Add(-1)
	for {
		peak := p.peak.Load()
		if active <= peak || p.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	// 模拟一次真实的识图子调用：一小段网络延迟 + 文本增量。
	if p.blockUntilContextDone {
		<-ctx.Done()
		p.canceled.Store(true)
		return ctx.Err()
	}
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

// TestVisionSynthesizeUsesShortBudgetBeforeProviderStart 防止慢识图在主模型请求前
// 长时间占用首字时间。预算耗尽后必须保留本地图片路径，让主模型可以调用读图工具兜底。
func TestVisionSynthesizeUsesShortBudgetBeforeProviderStart(t *testing.T) {
	service, provider := newEmpiricalService(t)
	service.multitaskDelegation.configProvider = visionEnabledConfigProvider{}
	service.visionProxyPassBudget = func() time.Duration { return 20 * time.Millisecond }
	provider.blockUntilContextDone = true

	imagePath := t.TempDir() + "/slow-vision.png"
	if err := os.WriteFile(imagePath, []byte("slow-vision-image"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	messages := []modeladapter.Message{{
		Role: "user",
		ContentParts: []modeladapter.ContentPart{
			modeladapter.NewTextContentPart("检查这张图"),
			imagePartByPath(imagePath),
		},
	}}

	started := time.Now()
	out := service.synthesizeImageDescriptions(context.Background(), "req-short-budget", "conv-short-budget", messages, "deepseek-v4-flash")
	elapsed := time.Since(started)

	if elapsed > 250*time.Millisecond {
		t.Fatalf("慢识图不应阻塞主请求首字预算，elapsed=%s", elapsed)
	}
	if !provider.canceled.Load() {
		t.Fatal("识图预算耗尽后必须取消子调用")
	}
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("应仅发起一次被取消的识图调用，实际 %d", got)
	}
	if modeladapter.MessageHasImage(out[0]) {
		t.Fatal("预算耗尽后不得将原始图片发送给纯文本主模型")
	}
	text := modeladapter.CollapseTextContentParts(out[0].ContentParts)
	if !strings.Contains(text, imagePath) || !strings.Contains(text, "读图工具") {
		t.Fatalf("预算耗尽后应保留图片路径和读图兜底说明，got %q", text)
	}
}

// TestVisionSynthesizeKeepsCompletedDescription 验证首字预算只中断慢调用，
// 正常完成的识图结果仍按原契约注入到当前请求。
func TestVisionSynthesizeKeepsCompletedDescription(t *testing.T) {
	service, provider := newEmpiricalService(t)
	service.multitaskDelegation.configProvider = visionEnabledConfigProvider{}
	service.visionProxyPassBudget = func() time.Duration { return 100 * time.Millisecond }

	messages := []modeladapter.Message{{
		Role: "user",
		ContentParts: []modeladapter.ContentPart{
			modeladapter.NewTextContentPart("描述这张图"),
			imagePartByData([]byte("fast-vision-image")),
		},
	}}
	out := service.synthesizeImageDescriptions(context.Background(), "req-fast-budget", "conv-fast-budget", messages, "deepseek-v4-flash")

	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("正常识图应调用一次视觉模型，实际 %d", got)
	}
	if provider.canceled.Load() {
		t.Fatal("正常识图不应被首字预算取消")
	}
	text := modeladapter.CollapseTextContentParts(out[0].ContentParts)
	if !strings.Contains(text, "浏览器截图") || strings.Contains(text, "图片识图未完成") {
		t.Fatalf("正常识图结果未按原契约注入，got %q", text)
	}
}

// TestVisionSynthesizeSharesParallelismAcrossMessages 锁定自动识图的并发上限必须
// 覆盖整个 provider pass。多条消息各含一张图时，旧实现会逐条等待，直接把首字
// 延迟叠加；新实现应在同一轮预算内并行启动，同时仍由全局令牌限制总并发。
func TestVisionSynthesizeSharesParallelismAcrossMessages(t *testing.T) {
	service, provider := newEmpiricalService(t)
	service.multitaskDelegation.configProvider = visionEnabledConfigProvider{}
	service.visionProxyPassBudget = func() time.Duration { return 500 * time.Millisecond }
	provider.blockMs = 80 * time.Millisecond

	messages := []modeladapter.Message{
		{Role: "user", ContentParts: []modeladapter.ContentPart{imagePartByData([]byte("first-image"))}},
		{Role: "user", ContentParts: []modeladapter.ContentPart{imagePartByData([]byte("second-image"))}},
	}

	started := time.Now()
	out := service.synthesizeImageDescriptions(context.Background(), "req-cross-message", "conv-cross-message", messages, "deepseek-v4-flash")
	elapsed := time.Since(started)
	t.Logf("cross-message vision elapsed=%s", elapsed)

	if got := provider.calls.Load(); got != 2 {
		t.Fatalf("StartStream calls=%d, want 2", got)
	}
	if got := provider.peak.Load(); got < 2 {
		t.Fatalf("跨消息识图应并行执行，峰值并发=%d", got)
	}
	for index, message := range out {
		if modeladapter.MessageHasImage(message) {
			t.Fatalf("messages[%d] should have its image replaced", index)
		}
	}
}

// TestVisionSynthesizeCoalescesDuplicateImagesInOnePass 锁定同轮内重复截图的
// 合并语义：结果写入缓存前，并发协程也必须共享在途请求，避免重复占用识图模型。
func TestVisionSynthesizeCoalescesDuplicateImagesInOnePass(t *testing.T) {
	service, provider := newEmpiricalService(t)
	service.multitaskDelegation.configProvider = visionEnabledConfigProvider{}
	service.visionProxyPassBudget = func() time.Duration { return 500 * time.Millisecond }
	provider.blockMs = 80 * time.Millisecond

	duplicate := []byte("same-screenshot-bytes")
	messages := []modeladapter.Message{{
		Role: "user",
		ContentParts: []modeladapter.ContentPart{
			imagePartByData(duplicate),
			imagePartByData(duplicate),
		},
	}}

	out := service.synthesizeImageDescriptions(context.Background(), "req-duplicate-image", "conv-duplicate-image", messages, "deepseek-v4-flash")

	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("重复图片必须共享一个在途识图调用，实际 %d 次", got)
	}
	if len(out) != 1 || len(out[0].ContentParts) != 2 {
		t.Fatalf("输出结构不符合预期：%#v", out)
	}
	for index, part := range out[0].ContentParts {
		if part.Type != "text" || !strings.Contains(part.Text, "浏览器截图") {
			t.Fatalf("parts[%d] = %#v，应为共享的识图描述", index, part)
		}
	}
}

// TestPrepareVisionImagePathsKeepsConversationOrder 锁定 data-only 图片在并行识图
// 前按会话顺序落地和登记，避免历史恢复时用错另一张图的临时文件。
func TestPrepareVisionImagePathsKeepsConversationOrder(t *testing.T) {
	service, _ := newEmpiricalService(t)
	messages := []modeladapter.Message{
		{Role: "user", ContentParts: []modeladapter.ContentPart{imagePartByData([]byte("first-image-bytes"))}},
		{Role: "user", ContentParts: []modeladapter.ContentPart{imagePartByData([]byte("second-image-bytes"))}},
	}

	service.prepareVisionImagePaths(context.Background(), "conv-image-order", messages)

	service.visionImageMu.Lock()
	paths := append([]string{}, service.visionImageFiles["conv-image-order"]...)
	service.visionImageMu.Unlock()
	if len(paths) != 2 {
		t.Fatalf("registered paths=%d, want 2", len(paths))
	}
	for index, want := range [][]byte{[]byte("first-image-bytes"), []byte("second-image-bytes")} {
		got, err := os.ReadFile(paths[index])
		if err != nil {
			t.Fatalf("read paths[%d]: %v", index, err)
		}
		if string(got) != string(want) {
			t.Fatalf("paths[%d] content=%q, want %q", index, got, want)
		}
	}
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
