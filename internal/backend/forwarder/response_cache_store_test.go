package forwarder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
)

func TestResponseCacheStoreLRUEviction(t *testing.T) {
	store := newResponseCacheStore("", nil)
	now := time.Now()
	for i := 0; i < 5; i++ {
		key := string(rune('a' + i))
		store.put(key, &responseCacheEntry{expiresAt: now.Add(time.Hour)}, 3)
	}
	// 容量 3：放入 5 个后应只剩 c/d/e（FIFO 顺序下 a/b 被淘汰）
	for _, key := range []string{"a", "b"} {
		if _, ok := store.get(key, now); ok {
			t.Fatalf("LRU: 预期 %q 被淘汰", key)
		}
	}
	// 访问 c（移到最近使用），再放入 f → 淘汰 d（最久未使用）
	if _, ok := store.get("c", now); !ok {
		t.Fatalf("c 应存在")
	}
	store.put("f", &responseCacheEntry{expiresAt: now.Add(time.Hour)}, 3)
	if _, ok := store.get("d", now); ok {
		t.Fatalf("LRU: 访问过 c 后，应淘汰 d 而非 c")
	}
	for _, key := range []string{"c", "e", "f"} {
		if _, ok := store.get(key, now); !ok {
			t.Fatalf("LRU: %q 应存在", key)
		}
	}
}

func TestResponseCacheStorePersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	store := newResponseCacheStore(path, nil)
	now := time.Now()

	events := []modeladapter.ModelEvent{
		{Kind: modeladapter.ModelEventKindTextDelta, Text: "你好", OccurredAt: now, Provider: "demo", Model: "m1"},
		{Kind: modeladapter.ModelEventKindTurnFinished, FinishReason: "end_turn", OccurredAt: now.Add(time.Second)},
	}
	store.put("key1", &responseCacheEntry{
		events:            events,
		savedInputTokens:  100,
		savedOutputTokens: 50,
		expiresAt:         now.Add(24 * time.Hour),
		lastAccessAt:      now,
	}, 10)
	store.flushToDisk()

	// 新 store 从磁盘加载，应能命中并恢复事件与节省统计
	reloaded := newResponseCacheStore(path, nil)
	entry, ok := reloaded.get("key1", now.Add(time.Minute))
	if !ok {
		t.Fatalf("持久化后应能命中 key1")
	}
	if len(entry.events) != 2 {
		t.Fatalf("事件数不符: got %d want 2", len(entry.events))
	}
	if entry.events[0].Text != "你好" || entry.events[0].Kind != modeladapter.ModelEventKindTextDelta {
		t.Fatalf("事件往返损坏: %+v", entry.events[0])
	}
	if entry.savedInputTokens != 100 || entry.savedOutputTokens != 50 {
		t.Fatalf("节省 token 统计丢失: %+v", entry)
	}

	// 过期条目加载时应被丢弃
	expiredStore := newResponseCacheStore(path, nil)
	expiredStore.put("expired", &responseCacheEntry{
		events:       events,
		expiresAt:    now.Add(-time.Minute),
		lastAccessAt: now.Add(-time.Hour),
	}, 10)
	expiredStore.flushToDisk()
	reloaded2 := newResponseCacheStore(path, nil)
	if _, ok := reloaded2.get("expired", now); ok {
		t.Fatalf("过期条目不应从磁盘加载")
	}
	if _, ok := reloaded2.get("key1", now); !ok {
		t.Fatalf("未过期条目应保留")
	}
}

func TestResponseCacheStoreCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	if err := os.WriteFile(path, []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newResponseCacheStore(path, nil)
	if _, ok := store.get("anything", time.Now()); ok {
		t.Fatalf("损坏文件不应产生命中")
	}
	// 损坏文件不应阻止后续写入
	store.put("k", &responseCacheEntry{events: []modeladapter.ModelEvent{{Kind: modeladapter.ModelEventKindTurnFinished}}, expiresAt: time.Now().Add(time.Hour)}, 10)
	store.flushToDisk()
}

// TestProviderCacheKeyNormalizationStability 验证缓存键对 provider 临时字段免疫：
// 两条语义相同、仅 ReasoningSignature / ToolCall ID / OpenAIResponsesReasoningID 不同的消息，
// 必须产出相同的缓存键；否则历史里一出现 thinking/工具调用，命中率就会塌缩到 0。
func TestProviderCacheKeyNormalizationStability(t *testing.T) {
	base := ProviderRequest{
		ModelID:        "claude-test",
		Mode:           0,
		ThinkingEffort: "high",
		Messages: []modeladapter.Message{
			{Role: "user", Content: "hello"},
			{
				Role:             "assistant",
				ReasoningContent: "thinking about it",
				Content:          "answer",
				ToolCalls: []modeladapter.ToolCallDescriptor{
					{
						Index: 0,
						Type:  "function",
						Function: modeladapter.ToolCallFunctionShape{
							Name:      "search",
							Arguments: `{"q":"x"}`,
						},
					},
				},
			},
		},
	}

	// 副本：注入 provider 每次调用都会变化的临时字段。
	variant := base
	variant.Messages = make([]modeladapter.Message, len(base.Messages))
	copy(variant.Messages, base.Messages)
	variant.Messages[1] = modeladapter.Message{
		Role:                        base.Messages[1].Role,
		ReasoningContent:            base.Messages[1].ReasoningContent,
		Content:                     base.Messages[1].Content,
		ReasoningSignature:          "sig-abc-123", // provider 每次签发不同
		ReasoningSignatureSource:    "anthropic",
		OpenAIResponsesReasoningID:  "resp_xyz",
		OpenAIResponsesReasoningStatus: "completed",
		OpenAIResponsesReasoningSummary: json.RawMessage(`["summary"]`),
		ToolCalls: []modeladapter.ToolCallDescriptor{{
			Index:                 0,
			Type:                  "function",
			Function:              base.Messages[1].ToolCalls[0].Function,
			ID:                    "call_001",       // provider 生成的临时 ID
			OpenAIResponsesID:     "respitem_001",   // provider 临时 ID
			OpenAIResponsesCallID: "callclient_001", // provider 临时 ID
			OpenAIResponsesStatus: "completed",
		}},
	}

	keyBase := providerCacheKey(base)
	keyVariant := providerCacheKey(variant)
	if keyBase == "" {
		t.Fatal("base key 不应为空")
	}
	if keyBase != keyVariant {
		t.Fatalf("归一化失效：语义相同的请求得到不同缓存键\nbase=%s\nvariant=%s", keyBase, keyVariant)
	}

	// 真正改变语义（推理正文），key 必须随之变化。
	semanticVariant := base
	semanticVariant.Messages = make([]modeladapter.Message, len(base.Messages))
	copy(semanticVariant.Messages, base.Messages)
	semanticVariant.Messages[1] = base.Messages[1]
	semanticVariant.Messages[1].ReasoningContent = "thinking about something DIFFERENT"
	if keySemantic := providerCacheKey(semanticVariant); keySemantic == keyBase {
		t.Fatal("改变推理正文必须改变缓存键，否则归一化过度")
	}
}