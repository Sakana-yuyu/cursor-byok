package config

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// 验证自定义思考档位（customThinkingEfforts）的配置归一化行为：
//  1. openai 渠道保留（去重/小写化），reasoningEffort 允许选中自定义档位作默认值；
//  2. anthropic / gemini 渠道忽略该字段；
//  3. 非法档位在保存时给出可定位的错误；
//  4. 渠道解析（ResolvedChannel）把自定义档位带给运行时。
func TestCustomThinkingEffortsNormalizeForOpenAIOnly(t *testing.T) {
	manager, err := NewManager(context.Background(), NewStore(filepath.Join(t.TempDir(), "config.yaml"), ""))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	cfg := manager.Current()
	cfg.ModelAdapters = []ModelAdapterConfig{
		{
			DisplayName:  "Custom Effort",
			Type:         "openai",
			ProtocolMode: "auto",
			TooltipData:  "custom efforts",
			BaseURL:      "https://custom.example.com/v1",
			APIKey:       "sk-custom",
			ModelID:      "vendor-model",
			// reasoningEffort 直接选自定义档位作为渠道默认。
			ReasoningEffort:       "  Adaptive ",
			CustomThinkingEfforts: []string{"adaptive", "ADAPTIVE", "deep-think", "max"},
		},
		{
			DisplayName:  "Anthropic No Custom",
			Type:         "anthropic",
			ProtocolMode: "auto",
			TooltipData:  "anthropic",
			BaseURL:      "https://anthropic.example.com",
			APIKey:       "sk-ant",
			ModelID:      "claude-fable-5",
			// anthropic 渠道即使误配也一并忽略。
			CustomThinkingEfforts: []string{"adaptive"},
		},
	}
	if _, err := manager.Save(context.Background(), cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	adapters := manager.Current().ModelAdapters
	if len(adapters) != 2 {
		t.Fatalf("normalized adapters = %d, want 2", len(adapters))
	}
	openai := adapters[0]
	if !reflect.DeepEqual(openai.CustomThinkingEfforts, []string{"adaptive", "deep-think"}) {
		t.Fatalf("openai CustomThinkingEfforts = %#v, want [adaptive deep-think] (deduped, standard dropped)", openai.CustomThinkingEfforts)
	}
	if openai.ReasoningEffort != "adaptive" {
		t.Fatalf("openai ReasoningEffort = %q, want custom default adaptive", openai.ReasoningEffort)
	}
	if adapters[1].CustomThinkingEfforts != nil {
		t.Fatalf("anthropic CustomThinkingEfforts = %#v, want nil (ignored)", adapters[1].CustomThinkingEfforts)
	}

	channels, err := manager.SelectChannelsForModel(context.Background(), "vendor-model")
	if err != nil {
		t.Fatalf("SelectChannelsForModel() error = %v", err)
	}
	if len(channels) == 0 {
		t.Fatal("no channel resolved for vendor-model")
	}
	if !reflect.DeepEqual(channels[0].CustomThinkingEfforts, []string{"adaptive", "deep-think"}) {
		t.Fatalf("ResolvedChannel CustomThinkingEfforts = %#v, want [adaptive deep-think]", channels[0].CustomThinkingEfforts)
	}
}

func TestCustomThinkingEffortsRejectInvalidEntry(t *testing.T) {
	manager, err := NewManager(context.Background(), NewStore(filepath.Join(t.TempDir(), "config.yaml"), ""))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	cfg := manager.Current()
	cfg.ModelAdapters = []ModelAdapterConfig{
		{
			DisplayName:           "Bad Custom",
			Type:                  "openai",
			ProtocolMode:          "auto",
			TooltipData:           "bad custom",
			BaseURL:               "https://bad.example.com/v1",
			APIKey:                "sk-bad",
			ModelID:               "vendor-model",
			ReasoningEffort:       "medium",
			CustomThinkingEfforts: []string{"has space"},
		},
	}
	_, saveErr := manager.Save(context.Background(), cfg)
	if saveErr == nil {
		t.Fatal("Save() must reject invalid custom thinking effort entry")
	}
	if !strings.Contains(saveErr.Error(), "has space") {
		t.Fatalf("Save() error should name the invalid entry, got: %v", saveErr)
	}
}
