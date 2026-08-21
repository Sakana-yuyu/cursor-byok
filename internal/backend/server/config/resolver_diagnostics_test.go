package config

import (
	"context"
	"path/filepath"
	"testing"
)

func TestResolvedChannelsForDiagnosticsPreservesOrderWithoutAdvancingSelection(t *testing.T) {
	manager, err := NewManager(context.Background(), NewStore(filepath.Join(t.TempDir(), "config.yaml"), ""))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	cfg := manager.Current()
	cfg.ModelAdapters = []ModelAdapterConfig{
		{
			ID:           "channel-a",
			DisplayName:  "Channel A",
			GroupName:    "Primary",
			Type:         "openai",
			ProtocolMode: "auto",
			TooltipData:  "diagnostics test",
			BaseURL:      "https://a.example.com/v1",
			APIKey:       "sk-a",
			ModelID:      "shared-model",
		},
		{
			ID:           "channel-b",
			DisplayName:  "Channel B",
			GroupName:    "Fallback",
			Type:         "anthropic",
			ProtocolMode: "auto",
			TooltipData:  "diagnostics test",
			BaseURL:      "https://b.example.com/v1",
			APIKey:       "sk-b",
			ModelID:      "shared-model",
		},
	}
	if _, err := manager.Save(context.Background(), cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if _, err := manager.SelectChannelsForModel(context.Background(), "shared-model"); err != nil {
		t.Fatalf("SelectChannelsForModel() error = %v", err)
	}
	manager.selectionMu.Lock()
	before := manager.selectionOffsets["shared-model"]
	manager.selectionMu.Unlock()

	notifications := 0
	unsubscribe := manager.Subscribe(func(Config) { notifications++ })
	defer unsubscribe()
	external := manager.currentConfig()
	external.ModelAdapters = append([]ModelAdapterConfig(nil), external.ModelAdapters...)
	external.ModelAdapters[0].DisplayName = "Externally Edited Channel"
	if _, err := manager.store.Save(context.Background(), external); err != nil {
		t.Fatalf("external store Save() error = %v", err)
	}

	channels, err := manager.ResolvedChannelsForDiagnostics(context.Background())
	if err != nil {
		t.Fatalf("ResolvedChannelsForDiagnostics() error = %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("len(channels) = %d, want 2", len(channels))
	}
	if channels[0].ID != "channel-a" || channels[1].ID != "channel-b" {
		t.Fatalf("channel order = [%q, %q], want [channel-a, channel-b]", channels[0].ID, channels[1].ID)
	}
	if channels[0].Name != "Channel A" || notifications != 0 {
		t.Fatalf("diagnostics hot-reloaded external config or notified listeners: name=%q notifications=%d", channels[0].Name, notifications)
	}

	manager.selectionMu.Lock()
	after := manager.selectionOffsets["shared-model"]
	manager.selectionMu.Unlock()
	if after != before {
		t.Fatalf("selection offset changed from %d to %d during diagnostics", before, after)
	}
}
