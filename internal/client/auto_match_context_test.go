package client

import (
	"context"
	"path/filepath"
	"testing"

	serverconfig "cursor/internal/backend/server/config"
)

// newAutoMatchTestService 构造仅依赖 store 的 ProxyService：
// 目录命中分支不触发 /models 探测（探测需要真实 HTTP 客户端）。
func newAutoMatchTestService(t *testing.T) *ProxyService {
	t.Helper()
	return &ProxyService{
		store: serverconfig.NewStore(filepath.Join(t.TempDir(), "config.yml"), t.TempDir()),
	}
}

func autoMatchTestAdapter(modelID string, contextWindow int) serverconfig.ModelAdapterConfig {
	return serverconfig.ModelAdapterConfig{
		DisplayName:         "DeepSeek",
		Type:                "openai",
		SupplierID:          "deepseek",
		ProtocolMode:        "auto",
		BaseURL:             "https://relay.example.com/v1",
		APIKey:              "test-key",
		TooltipData:         "test",
		ModelID:             modelID,
		ReasoningEffort:     "medium",
		OpenAIEndpoint:      "/v1/chat/completions",
		OpenAIRequestGroup:  "chat_completions",
		ContextWindowTokens: contextWindow,
	}
}

// TestAutoMatchKeepsUserSmallerWindow 回归：用户主动把目录 1M 的 deepseek-v4-flash 限制为 200K，
// 自动配对不得覆盖该手动设置（修复前目录命中会强制覆盖回 1M，导致 Cursor 显示 1M）。
func TestAutoMatchKeepsUserSmallerWindow(t *testing.T) {
	service := newAutoMatchTestService(t)
	cfg := serverconfig.DefaultConfig()
	cfg.AutoMatchContextWindow = true
	cfg.ModelAdapters = []serverconfig.ModelAdapterConfig{autoMatchTestAdapter("deepseek-v4-flash", 200_000)}
	if err := service.SaveUserConfig(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	result, err := service.AutoMatchContextWindows(context.Background(), false)
	if err != nil {
		t.Fatalf("auto match: %v", err)
	}
	if result.FromCatalog != 0 {
		t.Fatalf("expected no catalog override, got from_catalog=%d details=%+v", result.FromCatalog, result.Details)
	}
	if result.Changed {
		t.Fatalf("expected no config change, got details=%+v", result.Details)
	}
	loaded, err := service.LoadUserConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := loaded.ModelAdapters[0].ContextWindowTokens; got != 200_000 {
		t.Fatalf("context window = %d, want 200000 (user manual setting must be preserved)", got)
	}
}

// TestAutoMatchDowngradesOverfilledWindow 保留防误填保护：用户把窗口误填为 5M（大于目录 1M），
// 自动配对应下调到目录真实值，避免 context_length_exceeded。
func TestAutoMatchDowngradesOverfilledWindow(t *testing.T) {
	service := newAutoMatchTestService(t)
	cfg := serverconfig.DefaultConfig()
	cfg.AutoMatchContextWindow = true
	cfg.ModelAdapters = []serverconfig.ModelAdapterConfig{autoMatchTestAdapter("deepseek-v4-flash", 5_000_000)}
	if err := service.SaveUserConfig(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	result, err := service.AutoMatchContextWindows(context.Background(), false)
	if err != nil {
		t.Fatalf("auto match: %v", err)
	}
	if result.FromCatalog != 1 {
		t.Fatalf("expected catalog downgrade, got from_catalog=%d details=%+v", result.FromCatalog, result.Details)
	}
	loaded, err := service.LoadUserConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := loaded.ModelAdapters[0].ContextWindowTokens; got != 1_000_000 {
		t.Fatalf("context window = %d, want 1000000 (catalog value)", got)
	}
}