package upstream

import (
	"testing"

	legacyruntime "cursor/internal/runtime"
)

func TestIsOfficialModel(t *testing.T) {
	cases := []struct {
		modelID string
		want    bool
	}{
		{"claude-sonnet-4-5", true},
		{"claude-sonnet-4-5:thinking", true},
		{"claude-sonnet-4-5:high", true},
		{"gpt-5.3-codex", true},
		{"gpt-5.3-codex:high", true},
		{"deepseek-v4-flash", false},
		{"", false},
		{"claude-sonnet", false},
	}
	for _, tc := range cases {
		if got := IsOfficialModel(tc.modelID); got != tc.want {
			t.Errorf("IsOfficialModel(%q) = %v, want %v", tc.modelID, got, tc.want)
		}
	}
}

func TestOfficialModelEntries(t *testing.T) {
	entries := OfficialModelEntries()
	if len(entries) == 0 {
		t.Fatal("expected non-empty official model entries")
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		name, _ := entry["name"].(string)
		if name == "" {
			t.Fatalf("entry missing name: %+v", entry)
		}
		if seen[name] {
			t.Fatalf("duplicate official model: %s", name)
		}
		seen[name] = true
		if !IsOfficialModel(name) {
			t.Fatalf("entry %s not in official model set", name)
		}
		if _, ok := entry["contextTokenLimit"]; !ok {
			t.Errorf("entry %s missing contextTokenLimit", name)
		}
		if _, ok := entry["supportsAgent"]; !ok {
			t.Errorf("entry %s missing supportsAgent", name)
		}
	}
	if !seen["claude-sonnet-4-5"] {
		t.Error("expected claude-sonnet-4-5 in entries")
	}
	if !seen["gpt-5"] {
		t.Error("expected gpt-5 in entries")
	}
}

func TestRefreshOfficialModelsFromResponse(t *testing.T) {
	// 保存并恢复全局目录，避免污染其他测试。
	officialModels.mu.RLock()
	saved := append([]officialModelInfo(nil), officialModels.models...)
	savedRefreshed := officialModels.refreshed
	officialModels.mu.RUnlock()
	defer func() {
		officialModels.mu.Lock()
		officialModels.models = saved
		officialModels.refreshed = savedRefreshed
		officialModels.mu.Unlock()
	}()

	// 官方 GetUsableModels JSON 样例（真实结构）。
	body := []byte(`{"models":[
		{"modelId":"default","displayModelId":"auto","displayName":"Auto"},
		{"modelId":"gpt-5.6-sol-xhigh","displayModelId":"gpt-5.6-sol-xhigh","displayName":"GPT-5.6 Sol 1M Extra High"},
		{"modelId":"claude-opus-4-8","displayModelId":"claude-opus-4-8","displayName":"Opus 4.8"},
		{"modelId":"gpt-5.6-sol-xhigh","displayModelId":"gpt-5.6-sol-xhigh","displayName":"dup"}
	]}`)
	if err := RefreshOfficialModelsFromResponse(body); err != nil {
		t.Fatal(err)
	}
	// default 应被排除，重复应去重。
	if IsOfficialModel("default") {
		t.Fatal("default must be excluded")
	}
	if !IsOfficialModel("gpt-5.6-sol-xhigh") {
		t.Fatal("dynamic model should be recognized")
	}
	if !IsOfficialModel("claude-opus-4-8") {
		t.Fatal("dynamic model should be recognized")
	}
	entries := OfficialModelEntries()
	found := map[string]bool{}
	for _, entry := range entries {
		name, _ := entry["name"].(string)
		found[name] = true
	}
	if !found["gpt-5.6-sol-xhigh"] {
		t.Fatal("dynamic model missing from entries")
	}
	// 1M 模型应识别大上下文窗口。
	for _, entry := range entries {
		name, _ := entry["name"].(string)
		if name == "gpt-5.6-sol-xhigh" {
			if tokens, ok := entry["contextTokenLimit"].(int); !ok || tokens < 1000000 {
				t.Fatalf("expected 1M context for sol model, got %#v", entry["contextTokenLimit"])
			}
		}
	}

	// 无效 body 应报错且不破坏目录。
	if err := RefreshOfficialModelsFromResponse([]byte("{not-json")); err == nil {
		t.Fatal("expected error for invalid body")
	}
	if !IsOfficialModel("gpt-5.6-sol-xhigh") {
		t.Fatal("catalog must survive invalid refresh")
	}
}

func TestBuildAvailableModelEntriesIncludesOfficial(t *testing.T) {
	adapters := []legacyruntime.ModelAdapterConfig{
		{ID: "deepseek", DisplayName: "DeepSeek", ModelID: "deepseek-v4-flash"},
	}
	entries := buildAvailableModelEntries(adapters, true)
	names := map[string]bool{}
	for _, entry := range entries {
		name, _ := entry["name"].(string)
		names[name] = true
	}
	if !names["deepseek"] {
		t.Fatal("expected custom adapter entry")
	}
	if !names["claude-sonnet-4-5"] {
		t.Fatal("expected official model entry merged into AvailableModels")
	}
}

// TestBuildAvailableModelEntriesEmptyAdapters 空配置时不展示官方模型（避免误导）。
func TestBuildAvailableModelEntriesEmptyAdapters(t *testing.T) {
	entries := buildAvailableModelEntries(nil, false)
	if len(entries) != 0 {
		t.Fatalf("expected empty entries for nil adapters, got %d", len(entries))
	}
}

// TestBuildAvailableModelEntriesHybridModeDisabled 混合模式关闭时官方条目不合并
// （回归：开关关闭后模型选择器不得混入官方模型）。
func TestBuildAvailableModelEntriesHybridModeDisabled(t *testing.T) {
	adapters := []legacyruntime.ModelAdapterConfig{
		{ID: "deepseek", DisplayName: "DeepSeek", ModelID: "deepseek-v4-flash"},
	}
	entries := buildAvailableModelEntries(adapters, false)
	if len(entries) != 1 {
		t.Fatalf("expected only custom adapter entry, got %d", len(entries))
	}
	if name, _ := entries[0]["name"].(string); name != "deepseek" {
		t.Fatalf("expected deepseek entry, got %q", name)
	}
}

// TestBuildAvailableModelEntriesHybridModeEnabledNoAdapters 混合模式开启且无自定义
// adapter 时仅展示官方模型（用户可只用官方模型）。
func TestBuildAvailableModelEntriesHybridModeEnabledNoAdapters(t *testing.T) {
	entries := buildAvailableModelEntries(nil, true)
	if len(entries) == 0 {
		t.Fatal("expected official entries when hybrid mode enabled without adapters")
	}
}
