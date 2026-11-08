package modeladapter

import (
	"context"
	"fmt"
	"testing"
	"time"

	legacyruntime "cursor/internal/runtime"
)

func affinityStickyChannel() *legacyruntime.ResolvedChannel {
	return &legacyruntime.ResolvedChannel{ID: "c-sticky", Provider: "openai", BaseURL: "https://a.example/v1", APIKey: "sk-a", Model: "provider-model"}
}

func affinityOtherChannel() *legacyruntime.ResolvedChannel {
	return &legacyruntime.ResolvedChannel{ID: "c-other", Provider: "openai", BaseURL: "https://b.example/v1", APIKey: "sk-b", Model: "provider-model"}
}

func TestSessionAffinityKeyRequiresConversation(t *testing.T) {
	if sessionAffinityKey("", "model-a") != "" {
		t.Fatal("empty conversation must disable affinity")
	}
	if sessionAffinityKey("conv-1", "") != "conv-1\x00" {
		t.Fatalf("unexpected key with empty model: %q", sessionAffinityKey("conv-1", ""))
	}
}

func TestPickStickyChannelReturnsRecordedChannel(t *testing.T) {
	sticky := affinityStickyChannel()
	other := affinityOtherChannel()
	router := NewRouter(nil)
	req := StreamRequest{ConversationID: "conv-1", ModelID: "model-a"}
	router.recordSessionAffinity(req, sticky)

	got := router.pickStickyChannel([]*legacyruntime.ResolvedChannel{other, sticky}, "conv-1", "model-a", nil)
	if got != sticky {
		t.Fatalf("sticky channel not picked: %#v", got)
	}
	// 不同会话无粘性
	if got := router.pickStickyChannel([]*legacyruntime.ResolvedChannel{other, sticky}, "conv-2", "model-a", nil); got != nil {
		t.Fatalf("untracked conversation picked a channel: %#v", got)
	}
}

func TestPickStickyChannelSkipsTriedChannel(t *testing.T) {
	sticky := affinityStickyChannel()
	router := NewRouter(nil)
	req := StreamRequest{ConversationID: "conv-1", ModelID: "model-a"}
	router.recordSessionAffinity(req, sticky)

	tried := map[string]struct{}{channelHealthMapKey(sticky): {}}
	if got := router.pickStickyChannel([]*legacyruntime.ResolvedChannel{sticky}, "conv-1", "model-a", tried); got != nil {
		t.Fatalf("tried sticky channel was picked again: %#v", got)
	}
}

func TestPickStickyChannelSkipsCoolingChannel(t *testing.T) {
	sticky := affinityStickyChannel()
	router := NewRouter(nil)
	req := StreamRequest{ConversationID: "conv-1", ModelID: "model-a"}
	router.recordSessionAffinity(req, sticky)
	router.recordChannelFailure(sticky, fmt.Errorf("provider status=429"))

	if got := router.pickStickyChannel([]*legacyruntime.ResolvedChannel{sticky}, "conv-1", "model-a", nil); got != nil {
		t.Fatalf("cooling sticky channel was picked: %#v", got)
	}
	// 冷却过期的粘性渠道应恢复粘性（preferHealthyChannel 的清理语义）
	router.healthMu.Lock()
	router.healthByChannel[channelHealthMapKey(sticky)] = channelHealth{cooldownUntil: time.Now().Add(-time.Minute), version: 1}
	router.healthMu.Unlock()
	if got := router.pickStickyChannel([]*legacyruntime.ResolvedChannel{sticky}, "conv-1", "model-a", nil); got != sticky {
		t.Fatalf("expired-cooldown sticky channel was not picked: %#v", got)
	}
}

func TestSessionAffinityExpiresAfterTTL(t *testing.T) {
	affinity := &sessionAffinity{}
	key := sessionAffinityKey("conv-1", "model-a")
	affinity.record(key, "fingerprint", time.Now())
	if got := affinity.fingerprintOf(key, time.Now()); got != "fingerprint" {
		t.Fatalf("fresh affinity fingerprint = %q", got)
	}
	if got := affinity.fingerprintOf(key, time.Now().Add(sessionAffinityTTL+time.Second)); got != "" {
		t.Fatalf("expired affinity still sticky: %q", got)
	}
	// 过期条目应被清理，不残留
	affinity.mu.Lock()
	defer affinity.mu.Unlock()
	if _, exists := affinity.byKey[key]; exists {
		t.Fatal("expired affinity entry was not cleaned up")
	}
}

func TestDropSessionAffinityOnlyDropsFailedChannel(t *testing.T) {
	sticky := affinityStickyChannel()
	other := affinityOtherChannel()
	router := NewRouter(nil)
	req := StreamRequest{ConversationID: "conv-1", ModelID: "model-a"}
	router.recordSessionAffinity(req, sticky)

	// 失败的是其他渠道：粘性保留
	router.dropSessionAffinity(req, other)
	if got := router.pickStickyChannel([]*legacyruntime.ResolvedChannel{sticky}, "conv-1", "model-a", nil); got != sticky {
		t.Fatal("affinity to healthy channel was dropped by unrelated failure")
	}
	// 失败的是粘性渠道：粘性清除
	router.dropSessionAffinity(req, sticky)
	if got := router.pickStickyChannel([]*legacyruntime.ResolvedChannel{sticky}, "conv-1", "model-a", nil); got != nil {
		t.Fatal("failed sticky channel still holds affinity")
	}
}

func TestSelectChannelWithAffinityPrefersStickyThenFallsBack(t *testing.T) {
	sticky := affinityStickyChannel()
	other := affinityOtherChannel()
	resolver := &diagnosticsResolverStub{channels: []*legacyruntime.ResolvedChannel{other, sticky}}
	router := NewRouter(resolver)
	req := StreamRequest{ConversationID: "conv-1", ModelID: "model-a"}
	router.recordSessionAffinity(req, sticky)

	got, err := router.selectChannelWithAffinity(context.Background(), "model-a", "conv-1", nil)
	if err != nil {
		t.Fatalf("selectChannelWithAffinity failed: %v", err)
	}
	if got != sticky {
		t.Fatalf("expected sticky channel, got %#v", got)
	}
	// 空会话：回退 preferHealthyChannel（取第一个健康渠道）
	got, err = router.selectChannelWithAffinity(context.Background(), "model-a", "", nil)
	if err != nil {
		t.Fatalf("fallback select failed: %v", err)
	}
	if got != other {
		t.Fatalf("expected first healthy channel without conversation, got %#v", got)
	}
}

func TestRecordSessionAffinityWithoutConversationIsNoop(t *testing.T) {
	router := NewRouter(nil)
	router.recordSessionAffinity(StreamRequest{ModelID: "model-a"}, affinityStickyChannel())
	router.affinity.mu.Lock()
	defer router.affinity.mu.Unlock()
	if len(router.affinity.byKey) != 0 {
		t.Fatalf("affinity recorded without conversation: %d entries", len(router.affinity.byKey))
	}
}
