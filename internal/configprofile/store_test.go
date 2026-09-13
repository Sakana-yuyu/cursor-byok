package configprofile

import (
	"encoding/json"
	"strings"
	"testing"

	serverconfig "cursor/internal/backend/server/config"
)

func TestSaveOmitsAPIKeys(t *testing.T) {
	store := New(t.TempDir())
	cfg := serverconfig.DefaultConfig()
	cfg.ModelAdapters = []serverconfig.ModelAdapterConfig{{
		ID:          "adapter-1",
		DisplayName: "Test",
		Type:        "openai",
		BaseURL:     "https://example.invalid/v1",
		APIKey:      "sk-secret",
		ModelID:     "gpt-test",
	}}
	summary, err := store.SaveCurrent("demo", "desc", []string{"models", "routing"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := store.Export(summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-secret") {
		t.Fatalf("export leaked key: %s", raw)
	}
	preview, err := store.Preview(summary.ID, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.CanApply {
		t.Fatalf("preview = %#v", preview)
	}
}

// 路径遍历防护：带遍历序列/分隔符的 ID 不得参与路径拼接。
// Delete/Preview 走 load（拒绝），Import 的 ID 来自外部文件内容（重新生成）。
func TestProfileIDRejectsPathTraversal(t *testing.T) {
	store := New(t.TempDir())
	for _, id := range []string{"..\\..\\evil", "../../evil", "a/b", "a\\b", "..", "."} {
		if _, err := store.load(id); err == nil {
			t.Fatalf("load(%q) unexpectedly succeeded", id)
		}
	}
	// 写侧兜底：非法 ID 的 profile 必须被拒绝，不能落盘到 profiles 之外。
	for _, id := range []string{"..\\..\\evil", "../../evil", "a/b", ".."} {
		if err := store.write(storedProfile{SchemaVersion: schemaVersion, Summary: Summary{ID: id}}); err == nil {
			t.Fatalf("write with id %q unexpectedly succeeded", id)
		}
	}
}

func TestImportRegeneratesUnsafeProfileID(t *testing.T) {
	store := New(t.TempDir())
	content := `{"schemaVersion":1,"summary":{"id":"..\\..\\evil","name":"evil","domains":["models"]},"payload":{}}`
	preview, err := store.Import(content)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Profile.ID == "" || preview.Profile.ID == "..\\..\\evil" {
		t.Fatalf("import kept unsafe id: %q", preview.Profile.ID)
	}
	if !validProfileID(preview.Profile.ID) {
		t.Fatalf("regenerated id is invalid: %q", preview.Profile.ID)
	}
}
