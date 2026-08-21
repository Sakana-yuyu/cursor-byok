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
