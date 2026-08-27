package config

import (
	"context"
	"path/filepath"
	"testing"
)

// 验证备用密钥池（apiKeys）的行为：
//  1. SelectChannelsForModel 把带密钥池的 adapter 展开成逐密钥候选；
//  2. 轮换游标在展开后的完整列表上推进（单 adapter 多密钥也能轮换）；
//  3. 规范化会去重/去空，并把主 apiKey 从池中剔除。
func TestSelectChannelsForModelExpandsAPIKeyPool(t *testing.T) {
	manager, err := NewManager(context.Background(), NewStore(filepath.Join(t.TempDir(), "config.yaml"), ""))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	cfg := manager.Current()
	cfg.ModelAdapters = []ModelAdapterConfig{
		{
			ID:           "pooled-channel",
			DisplayName:  "Pooled Channel",
			Type:         "openai",
			ProtocolMode: "auto",
			TooltipData:  "key pool test",
			BaseURL:      "https://pool.example.com/v1",
			APIKey:       "sk-primary",
			APIKeys:      []string{"sk-secondary", "", "sk-primary", "sk-third"},
			ModelID:      "shared-model",
		},
	}
	if _, err := manager.Save(context.Background(), cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	pool := manager.Current().ModelAdapters[0].APIKeys
	if len(pool) != 2 || pool[0] != "sk-secondary" || pool[1] != "sk-third" {
		t.Fatalf("normalized APIKeys = %v, want [sk-secondary sk-third] (deduped, primary excluded)", pool)
	}

	for i := 0; i < 3; i++ {
		channels, err := manager.SelectChannelsForModel(context.Background(), "shared-model")
		if err != nil {
			t.Fatalf("SelectChannelsForModel() call %d error = %v", i, err)
		}
		if len(channels) != 3 {
			t.Fatalf("call %d len(channels) = %d, want 3 (primary + two pool keys)", i, len(channels))
		}
		keys := map[string]bool{}
		for _, channel := range channels {
			keys[channel.APIKey] = true
		}
		for _, want := range []string{"sk-primary", "sk-secondary", "sk-third"} {
			if !keys[want] {
				t.Fatalf("call %d missing key %q in candidate list %v", i, want, channels)
			}
		}
	}

	// 第二轮起点应与第一轮不同：游标在展开列表上推进
	first, err := manager.SelectChannelsForModel(context.Background(), "shared-model")
	if err != nil {
		t.Fatalf("SelectChannelsForModel() error = %v", err)
	}
	second, err := manager.SelectChannelsForModel(context.Background(), "shared-model")
	if err != nil {
		t.Fatalf("SelectChannelsForModel() error = %v", err)
	}
	if first[0].APIKey == second[0].APIKey {
		t.Fatalf("rotation cursor did not advance: two consecutive selects both started with %q", first[0].APIKey)
	}
}

func TestExpandAdapterKeyPoolsClonesDoNotShareSlices(t *testing.T) {
	adapters := []ModelAdapterConfig{
		{
			ID:      "a",
			APIKey:  "sk-a",
			APIKeys: []string{"sk-b", "sk-c"},
		},
	}
	candidates := expandAdapterKeyPools(adapters, []int{0})
	if len(candidates) != 3 {
		t.Fatalf("len(candidates) = %d, want 3", len(candidates))
	}
	if candidates[1].APIKey == candidates[2].APIKey {
		t.Fatalf("clones share APIKey value")
	}
	if candidates[0].APIKeys == nil {
		t.Fatalf("single-key clone lost its pool metadata")
	}
}
