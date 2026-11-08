// router_affinity_stream_test.go 验证会话粘性与每密钥并发闸门在 Router.Stream
// 全链路上的接线（scripted resolver 模拟 config.Manager 的轮换游标）：
//   - 成功渠道建立粘性后，下一轮即使游标回到其他渠道也优先复用粘性渠道；
//   - 粘性渠道失败后清除粘性（unanswered 语义），新成功渠道接管粘性；
//   - 闸门排队超时的渠道被按「忙」跳过：不计冷却、不退避，直接换下一候选。
package modeladapter

import (
	"context"
	"errors"
	"testing"
	"time"

	legacyruntime "cursor/internal/runtime"
)

// scriptedResolver 按脚本顺序返回候选列表，模拟轮换游标推进。
type scriptedResolver struct {
	orders [][]*legacyruntime.ResolvedChannel
	call   int
}

func (r *scriptedResolver) SelectChannelForModel(context.Context, string) (*legacyruntime.ResolvedChannel, error) {
	if len(r.orders) == 0 {
		return nil, legacyruntime.ErrChannelNotAvailable
	}
	return r.orders[0][0], nil
}

func (r *scriptedResolver) SelectChannelsForModel(context.Context, string) ([]*legacyruntime.ResolvedChannel, error) {
	if r.call >= len(r.orders) || len(r.orders[r.call]) == 0 {
		return nil, legacyruntime.ErrChannelNotAvailable
	}
	order := r.orders[r.call]
	r.call++
	return order, nil
}

func (*scriptedResolver) ProviderStreamIdleTimeout(context.Context) time.Duration {
	return 90 * time.Second
}
func (*scriptedResolver) TurnStaleTimeout(context.Context) time.Duration { return time.Minute }
func (*scriptedResolver) NativeDelegationProgressTimeout(context.Context) time.Duration {
	return time.Minute
}

// perKeyAdapter 记录每次收到的渠道 APIKey，可按密钥注入失败
// （connection reset 类错误不产生冷却，便于控制游标与粘性的独立验证）。
type perKeyAdapter struct {
	calls    []string
	failKeys map[string]error
}

func (a *perKeyAdapter) Stream(_ context.Context, req StreamRequest, _ func(ModelEvent) error) error {
	a.calls = append(a.calls, req.APIKey)
	if err, ok := a.failKeys[req.APIKey]; ok {
		return err
	}
	return nil
}

func affinityTestChannel(id, apiKey string, maxPerKey int) *legacyruntime.ResolvedChannel {
	return &legacyruntime.ResolvedChannel{
		ID:                   id,
		Name:                 id,
		GroupName:            "test-group",
		Provider:             "openai",
		ProtocolMode:         "auto",
		ProtocolGroup:        "responses",
		BaseURL:              "https://" + id + ".example/v1",
		APIKey:               apiKey,
		Model:                "provider-model",
		OpenAIEndpoint:       "/responses",
		MaxConcurrencyPerKey: maxPerKey,
	}
}

func newAffinityTestRouter(adapter ModelAdapter, orders [][]*legacyruntime.ResolvedChannel) *Router {
	return &Router{
		openai:          adapter,
		anthropic:       NewAnthropicAdapter(),
		gemini:          NewGeminiAdapter(),
		resolver:        &scriptedResolver{orders: orders},
		healthByChannel: make(map[string]channelHealth),
		affinity:        &sessionAffinity{},
		keyLanes:        &keyLanes{},
	}
}

func affinityTestStreamRequest() StreamRequest {
	return StreamRequest{
		RequestID:      "req-affinity",
		ModelCallID:    "call-affinity",
		ModelID:        "gpt-5.6-sol",
		ConversationID: "conversation-1",
	}
}

func TestRouterStreamBuildsSessionAffinityAcrossRotations(t *testing.T) {
	channelA := affinityTestChannel("channel-a", "sk-a", 0)
	channelB := affinityTestChannel("channel-b", "sk-b", 0)
	adapter := &perKeyAdapter{failKeys: map[string]error{
		"sk-a": errors.New("connection reset"), // 不冷却，仅触发 failover
	}}
	router := newAffinityTestRouter(adapter, [][]*legacyruntime.ResolvedChannel{
		{channelA, channelB}, // 第一轮第一选：游标在 A，A 失败
		{channelB, channelA}, // 游标推进到 B，B 成功并建立粘性
		{channelA, channelB}, // 第二轮游标回到 A：粘性应压过游标直接选 B
	})

	if err := router.Stream(context.Background(), affinityTestStreamRequest(), func(ModelEvent) error { return nil }); err != nil {
		t.Fatalf("first Stream failed: %v", err)
	}
	if len(adapter.calls) != 2 || adapter.calls[0] != "sk-a" || adapter.calls[1] != "sk-b" {
		t.Fatalf("first stream calls = %v, want [sk-a sk-b]", adapter.calls)
	}
	if err := router.Stream(context.Background(), affinityTestStreamRequest(), func(ModelEvent) error { return nil }); err != nil {
		t.Fatalf("second Stream failed: %v", err)
	}
	if len(adapter.calls) != 3 || adapter.calls[2] != "sk-b" {
		t.Fatalf("second stream ignored session affinity: calls = %v, want single sk-b", adapter.calls)
	}
}

func TestRouterStreamClearsAffinityOnStickyFailureAndRebuilds(t *testing.T) {
	channelA := affinityTestChannel("channel-a", "sk-a", 0)
	channelB := affinityTestChannel("channel-b", "sk-b", 0)
	adapter := &perKeyAdapter{}
	router := newAffinityTestRouter(adapter, [][]*legacyruntime.ResolvedChannel{
		{channelA, channelB}, // 第一轮：A 成功，粘性指向 A
		{channelA, channelB}, // 第二轮：粘性 A 失败 → 清除粘性
		{channelB, channelA}, // 游标推进到 B：B 成功并接管粘性
		{channelB, channelA}, // 第三轮：粘性应指向 B
	})

	if err := router.Stream(context.Background(), affinityTestStreamRequest(), func(ModelEvent) error { return nil }); err != nil {
		t.Fatalf("first Stream failed: %v", err)
	}
	if len(adapter.calls) != 1 || adapter.calls[0] != "sk-a" {
		t.Fatalf("first stream calls = %v, want single sk-a", adapter.calls)
	}
	// 从第二轮起 A 注入失败（connection reset 不产生冷却，仅触发 failover）
	adapter.failKeys = map[string]error{"sk-a": errors.New("connection reset")}
	if err := router.Stream(context.Background(), affinityTestStreamRequest(), func(ModelEvent) error { return nil }); err != nil {
		t.Fatalf("second Stream failed: %v", err)
	}
	// 第二轮：粘性 A 失败被清除，B 接管
	if len(adapter.calls) != 3 || adapter.calls[1] != "sk-a" || adapter.calls[2] != "sk-b" {
		t.Fatalf("second stream calls = %v, want [sk-a sk-b]", adapter.calls[1:])
	}
	if err := router.Stream(context.Background(), affinityTestStreamRequest(), func(ModelEvent) error { return nil }); err != nil {
		t.Fatalf("third Stream failed: %v", err)
	}
	// 第三轮：粘性已由 B 接管，游标在 B 也应直接选 B
	if len(adapter.calls) != 4 || adapter.calls[3] != "sk-b" {
		t.Fatalf("third stream ignored rebuilt affinity: calls = %v, want single sk-b", adapter.calls[3:])
	}
}

func TestRouterStreamSkipsLaneBusyChannelWithoutCooldown(t *testing.T) {
	channelA := affinityTestChannel("channel-a", "sk-a", 1)
	channelB := affinityTestChannel("channel-b", "sk-b", 0)
	adapter := &perKeyAdapter{}
	router := newAffinityTestRouter(adapter, [][]*legacyruntime.ResolvedChannel{
		{channelA, channelB},
		{channelB, channelA},
	})

	// 预占 A 的唯一闸门名额，模拟该密钥在途流已满
	releaseSlot, err := router.keyLanes.acquire(context.Background(), channelHealthMapKey(channelA), 1, 0)
	if err != nil {
		t.Fatalf("pre-acquire failed: %v", err)
	}
	defer releaseSlot()

	originalWait := keyLaneQueueWait
	keyLaneQueueWait = 100 * time.Millisecond
	defer func() { keyLaneQueueWait = originalWait }()

	if err := router.Stream(context.Background(), affinityTestStreamRequest(), func(ModelEvent) error { return nil }); err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	// A 被按「忙」跳过，仅 B 被实际调用
	if len(adapter.calls) != 1 || adapter.calls[0] != "sk-b" {
		t.Fatalf("adapter calls = %v, want single sk-b (sk-a skipped as busy)", adapter.calls)
	}
	// 忙跳过不产生冷却：A 的健康记录应为空
	router.healthMu.Lock()
	_, cooling := router.healthByChannel[channelHealthMapKey(channelA)]
	router.healthMu.Unlock()
	if cooling {
		t.Fatal("lane-busy skip recorded a channel cooldown")
	}
}

func TestRouterStreamLaneBusySurfacesWhenAllCandidatesBusy(t *testing.T) {
	channelA := affinityTestChannel("channel-a", "sk-a", 1)
	channelB := affinityTestChannel("channel-b", "sk-b", 1)
	adapter := &perKeyAdapter{}
	router := newAffinityTestRouter(adapter, [][]*legacyruntime.ResolvedChannel{
		{channelA, channelB},
		{channelB, channelA},
		{channelA, channelB},
	})

	releaseA, err := router.keyLanes.acquire(context.Background(), channelHealthMapKey(channelA), 1, 0)
	if err != nil {
		t.Fatalf("pre-acquire A failed: %v", err)
	}
	defer releaseA()
	releaseB, err := router.keyLanes.acquire(context.Background(), channelHealthMapKey(channelB), 1, 0)
	if err != nil {
		t.Fatalf("pre-acquire B failed: %v", err)
	}
	defer releaseB()

	originalWait := keyLaneQueueWait
	keyLaneQueueWait = 50 * time.Millisecond
	defer func() { keyLaneQueueWait = originalWait }()

	err = router.Stream(context.Background(), affinityTestStreamRequest(), func(ModelEvent) error { return nil })
	if err == nil || !errors.Is(err, ErrKeyLaneBusy) {
		t.Fatalf("all-busy Stream error = %v, want ErrKeyLaneBusy", err)
	}
	if len(adapter.calls) != 0 {
		t.Fatalf("adapter called despite saturated lanes: %v", adapter.calls)
	}
	router.healthMu.Lock()
	_, coolingA := router.healthByChannel[channelHealthMapKey(channelA)]
	_, coolingB := router.healthByChannel[channelHealthMapKey(channelB)]
	router.healthMu.Unlock()
	if coolingA || coolingB {
		t.Fatal("lane-busy failures recorded cooldowns")
	}
}
