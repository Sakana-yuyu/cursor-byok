package upstream

import (
	"bytes"
	"compress/gzip"
	"testing"

	"cursor/gen/agentv1"
	legacyruntime "cursor/internal/runtime"

	"google.golang.org/protobuf/proto"
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
		// auto 占位：Cursor 客户端 Auto 模式的 run_request 携带 model_id=default，
		// 官方 GetUsableModels 也返回 modelId=default（displayModelId=auto）条目，
		// hybrid 模式下应透传官方解析。
		{"default", true},
		{"auto", true},
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
	// default 不应进入模型目录（不可展示），但透传判定仍视为官方（auto 对话）。
	if OfficialModelEntriesContains(t, "default") {
		t.Fatal("default must be excluded from model entries")
	}
	if !IsOfficialModel("default") {
		t.Fatal("default (auto) must be treated as official for routing")
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

// TestRefreshOfficialModelsFromResponseProto 官方二进制 proto 响应（agent.v1 格式，
// 实测 Accept application/proto 时的实际编码）也必须能刷新动态目录。
// 回归：曾用 aiserver.v1 解析导致 wire type 不匹配、刷新永远失败。
func TestRefreshOfficialModelsFromResponseProto(t *testing.T) {
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

	body, err := proto.Marshal(&agentv1.GetUsableModelsResponse{
		Models: []*agentv1.ModelDetails{
			{ModelId: "default", DisplayModelId: "auto", DisplayName: "Auto"},
			{ModelId: "gpt-5.3-codex", DisplayModelId: "gpt-5.3-codex", DisplayName: "Codex 5.3"},
			{ModelId: "claude-sonnet-4-5", DisplayModelId: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5", MaxMode: boolPtr(false)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := RefreshOfficialModelsFromResponse(body); err != nil {
		t.Fatalf("refresh from proto: %v", err)
	}
	// default 不应进入模型目录（不可展示），但透传判定仍视为官方（auto 对话）。
	if OfficialModelEntriesContains(t, "default") {
		t.Fatal("default must be excluded from model entries")
	}
	if !IsOfficialModel("default") {
		t.Fatal("default (auto) must be treated as official for routing")
	}
	if !IsOfficialModel("gpt-5.3-codex") || !IsOfficialModel("claude-sonnet-4-5") {
		t.Fatal("proto-refreshed models should be recognized")
	}
	entries := OfficialModelEntries()
	if len(entries) != 2 {
		t.Fatalf("entries after proto refresh: got %d, want 2", len(entries))
	}

	// gzip 压缩的 proto 响应（官方对 Accept-Encoding: gzip 请求的返回）也必须能解析。
	var gzBuf bytes.Buffer
	gzWriter := gzip.NewWriter(&gzBuf)
	if _, err := gzWriter.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := gzWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RefreshOfficialModelsFromResponse(gzBuf.Bytes()); err != nil {
		t.Fatalf("refresh from gzip proto: %v", err)
	}
	if !IsOfficialModel("gpt-5.3-codex") {
		t.Fatal("gzip-refreshed model should be recognized")
	}
}

func boolPtr(b bool) *bool { return &b }

// OfficialModelEntriesContains 检查官方模型目录是否含指定模型名（测试 helper）。
func OfficialModelEntriesContains(t *testing.T, name string) bool {
	t.Helper()
	for _, entry := range OfficialModelEntries() {
		if n, _ := entry["name"].(string); n == name {
			return true
		}
	}
	return false
}

// TestBuildAvailableModelEntriesHybridModeExcludesCustomAdapters 混合模式下模型
// 列表排除 BYOK 自定义 adapter，只输出官方模型（auto 对话只会在官方模型中选）。
func TestBuildAvailableModelEntriesHybridModeExcludesCustomAdapters(t *testing.T) {
	adapters := []legacyruntime.ModelAdapterConfig{
		{ID: "deepseek", DisplayName: "DeepSeek", ModelID: "deepseek-v4-flash"},
	}
	entries := buildAvailableModelEntries(adapters, true)
	if len(entries) != len(OfficialModelEntries()) {
		t.Fatalf("expected only official entries in hybrid mode, got %d (official=%d)", len(entries), len(OfficialModelEntries()))
	}
	for _, entry := range entries {
		name, _ := entry["name"].(string)
		if name == "deepseek" {
			t.Fatal("custom adapter must not appear in hybrid mode")
		}
		if !IsOfficialModel(name) {
			t.Fatalf("entry %q is not an official model", name)
		}
	}
	if len(entries) == 0 || entries[0]["name"] != firstOfficialModelRef() {
		t.Fatalf("first entry should be the official default model, got %#v", entries[0]["name"])
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
