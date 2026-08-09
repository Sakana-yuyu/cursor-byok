package forwarder

// 回归测试：cacheKeyCache.fingerprint 必须对请求内容敏感。
// 视觉委派的识图消息 Content 为空、文本与图片都在 ContentParts 里，旧实现只按
// len(msg.Content) 做指纹，导致不同图片碰撞出同一指纹 → 命中上一张图的缓存，
// 表现为「第二次说的还是第一张图的内容」。本测试验证不同图片指纹必须不同。

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
)

// visionDescribeReq 构造一条与 describeImageOnce 相同形状的识图请求：
// Content 为空，文本 prompt + 图片 ContentPart。
func visionDescribeReq(data []byte, path string) ProviderRequest {
	return ProviderRequest{
		ModelID: "4f97a29883b70e79",
		Mode:    5, // agent mode
		Messages: []modeladapter.Message{{
			Role:    "user",
			Content: "", // 文本与图片都在 ContentParts，Content 为空 —— 旧指纹的盲区
			ContentParts: []modeladapter.ContentPart{
				modeladapter.NewTextContentPart("请按以下两点输出这张图片的内容：…"),
				modeladapter.NewImageContentPart(&modeladapter.ImageContent{MIMEType: "image/png", Data: data, Path: path}),
			},
		}},
		Tools:     nil,
		MaxTokens: 4000,
	}
}

// TestCacheKeyFingerprintDistinguishesImages 验证不同图片 → 不同指纹。
func TestCacheKeyFingerprintDistinguishesImages(t *testing.T) {
	kc := newCacheKeyCache(64)
	reqA := visionDescribeReq([]byte("first-image-bytes-AAAA"), "c:/a.png")
	reqB := visionDescribeReq([]byte("second-image-bytes-BBBB"), "c:/b.png")
	reqA2 := visionDescribeReq([]byte("first-image-bytes-AAAA"), "c:/a.png") // 与 A 完全相同

	fpA := kc.fingerprint(reqA)
	fpB := kc.fingerprint(reqB)
	fpA2 := kc.fingerprint(reqA2)

	if fpA == fpB {
		t.Fatalf("不同图片必须产生不同指纹，实际碰撞: %q == %q", fpA, fpB)
	}
	if fpA != fpA2 {
		t.Fatalf("相同图片必须产生相同指纹，实际不同: %q != %q", fpA, fpA2)
	}
}

// fingerprintProviderInner 记录调用次数，并按请求中图片字节返回对应描述文本。
type fingerprintProviderInner struct {
	calls atomic.Int32
}

func (p *fingerprintProviderInner) StartStream(_ context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	p.calls.Add(1)
	// 从图片 ContentPart 中取字节作为"识别结果"标识，不同图片返回不同文本。
	var id string
	for _, msg := range req.Messages {
		for _, part := range msg.ContentParts {
			if part.Image != nil && len(part.Image.Data) > 0 {
				id = string(part.Image.Data)
			}
		}
	}
	if err := sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "describe:" + id}); err != nil {
		return err
	}
	return sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTurnFinished})
}

func collectSink(acc *[]string) func(modeladapter.ModelEvent) error {
	return func(event modeladapter.ModelEvent) error {
		if event.Kind == modeladapter.ModelEventKindTextDelta {
			*acc = append(*acc, event.Text)
		}
		return nil
	}
}

// TestProviderCacheDistinguishesImageContent end-to-end：先请求图 A（真实调用并缓存），
// 再请求图 B（不同图片）。修复前图 B 命中图 A 的缓存条目回放 A 的描述（inner 只调 1 次）；
// 修复后图 B 必须再次调用 inner，得到自己的描述。
func TestProviderCacheDistinguishesImageContent(t *testing.T) {
	inner := &fingerprintProviderInner{}
	gateway := newCachingProviderGateway(inner, func() (bool, time.Duration, int, bool) {
		return true, time.Minute, 64, false
	}, "")

	reqA := visionDescribeReq([]byte("IMG-AAA"), "c:/a.png")
	reqB := visionDescribeReq([]byte("IMG-BBB"), "c:/b.png")

	var eventsA, eventsB []string
	if err := gateway.StartStream(context.Background(), reqA, collectSink(&eventsA)); err != nil {
		t.Fatalf("reqA StartStream err: %v", err)
	}
	if err := gateway.StartStream(context.Background(), reqB, collectSink(&eventsB)); err != nil {
		t.Fatalf("reqB StartStream err: %v", err)
	}

	if got := inner.calls.Load(); got != 2 {
		t.Fatalf("不同图片应各自调用上游，期望 inner 调用 2 次，实际 %d —— 不同图片碰撞了同一缓存 key，回放了错误描述", got)
	}
	if len(eventsB) != 1 || eventsB[0] != "describe:IMG-BBB" {
		t.Fatalf("图 B 应收到自己的描述，实际 %v", eventsB)
	}
	if len(eventsA) != 1 || eventsA[0] != "describe:IMG-AAA" {
		t.Fatalf("图 A 应收到自己的描述，实际 %v", eventsA)
	}

	// 再次请求图 A：应命中缓存，且不新增上游调用。
	var eventsA2 []string
	if err := gateway.StartStream(context.Background(), reqA, collectSink(&eventsA2)); err != nil {
		t.Fatalf("reqA replay err: %v", err)
	}
	if got := inner.calls.Load(); got != 2 {
		t.Fatalf("相同图片应命中缓存，期望 inner 仍为 2 次调用，实际 %d", got)
	}
	if len(eventsA2) != 1 || eventsA2[0] != "describe:IMG-AAA" {
		t.Fatalf("图 A 重放描述不符，实际 %v", eventsA2)
	}
}

// TestProviderCacheKeyTextOnlyStable 相同纯文本请求指纹稳定（回归旧行为不破坏）。
func TestProviderCacheKeyTextOnlyStable(t *testing.T) {
	kc := newCacheKeyCache(64)
	req := ProviderRequest{
		ModelID: "deepseek-v4-flash",
		Messages: []modeladapter.Message{
			{Role: "user", Content: fmt.Sprintf("你好，%d", 1)},
		},
	}
	if kc.fingerprint(req) != kc.fingerprint(req) {
		t.Fatal("相同纯文本请求指纹必须稳定")
	}
}

// TestCacheKeyFingerprintIsolatesConversations 完全相同 provider-visible 请求但属于
// 不同 conversation 时，本地响应缓存必须给出不同键：不能把 A 会话的完成流回放给 B。
func TestCacheKeyFingerprintIsolatesConversations(t *testing.T) {
	kc := newCacheKeyCache(64)
	base := ProviderRequest{
		ModelID:   "deepseek-v4-flash",
		ModelName: "deepseek-v4-flash",
		Messages: []modeladapter.Message{
			{Role: "user", Content: "帮我修复这个 bug"},
		},
		MaxTokens: 4000,
	}
	reqA := base
	reqA.ConversationID = "conversation-a"
	reqB := base
	reqB.ConversationID = "conversation-b"

	fpA := kc.fingerprint(reqA)
	fpB := kc.fingerprint(reqB)
	if fpA == "" || fpB == "" {
		t.Fatalf("conversation-scoped fingerprints must be non-empty: %q %q", fpA, fpB)
	}
	if fpA == fpB {
		t.Fatalf("不同 conversation 必须产生不同缓存指纹，实际碰撞: %q == %q", fpA, fpB)
	}
	reqA2 := base
	reqA2.ConversationID = "conversation-a"
	if kc.fingerprint(reqA) != kc.fingerprint(reqA2) {
		t.Fatal("同一 conversation 相同请求必须稳定命中")
	}
	empty := base
	if kc.fingerprint(empty) != kc.fingerprint(empty) {
		t.Fatal("空 conversation 请求指纹必须稳定")
	}
}

func TestCacheKeyFingerprintTracksAllCacheKeySemantics(t *testing.T) {
	kc := newCacheKeyCache(64)
	base := ProviderRequest{
		ModelID: "model-a",
		Messages: []modeladapter.Message{{
			Role: "assistant",
			ToolCalls: []modeladapter.ToolCallDescriptor{{
				Type: "function",
				Function: modeladapter.ToolCallFunctionShape{
					Name:      "search",
					Arguments: `{"query":"one"}`,
				},
			}},
		}},
		MaxTokens: 4_000,
	}
	toolVariant := base
	toolVariant.Messages = append([]modeladapter.Message(nil), base.Messages...)
	toolVariant.Messages[0] = base.Messages[0]
	toolVariant.Messages[0].ToolCalls = append([]modeladapter.ToolCallDescriptor(nil), base.Messages[0].ToolCalls...)
	toolVariant.Messages[0].ToolCalls[0].Function.Arguments = `{"query":"two"}`
	if kc.fingerprint(base) == kc.fingerprint(toolVariant) {
		t.Fatal("different tool call semantics must produce different cache lookup fingerprints")
	}

	budgetVariant := base
	budgetVariant.MaxTokens = 25_600
	if kc.fingerprint(base) == kc.fingerprint(budgetVariant) {
		t.Fatal("different output budgets must produce different cache lookup fingerprints")
	}
}
