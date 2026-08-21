package config

import (
	"testing"

	"cursor/internal/modelchannel"
)

func TestNormalizeModelAdapterConfigsKeepsDistinctAnthropicAuthModes(t *testing.T) {
	input := []ModelAdapterConfig{
		{DisplayName: "Claude", GroupName: "Primary", Type: "anthropic", BaseURL: "https://gateway.example/v1", APIKey: "token", TooltipData: "test", ModelID: "claude-test", AnthropicAuthMode: modelchannel.AnthropicAuthModeBearer},
		{DisplayName: "Claude", GroupName: "Primary", Type: "anthropic", BaseURL: "https://gateway.example/v1", APIKey: "token", TooltipData: "test", ModelID: "claude-test", AnthropicAuthMode: modelchannel.AnthropicAuthModeAPIKey},
	}
	adapters, err := NormalizeModelAdapterConfigs(input)
	if err != nil {
		t.Fatalf("NormalizeModelAdapterConfigs() error = %v", err)
	}
	if len(adapters) != 2 || adapters[0].AnthropicAuthMode == adapters[1].AnthropicAuthMode || adapters[0].ID == adapters[1].ID {
		t.Fatalf("auth-mode-distinct adapters were collapsed: %#v", adapters)
	}
}

func TestResolvedChannelPreservesAnthropicAuthMode(t *testing.T) {
	adapters, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{{
		ID: "anthropic-channel", DisplayName: "Claude", Type: "anthropic", BaseURL: "https://api.anthropic.com/v1", APIKey: "token", TooltipData: "test", ModelID: "claude-test", AnthropicAuthMode: modelchannel.AnthropicAuthModeBearer,
	}})
	if err != nil {
		t.Fatalf("NormalizeModelAdapterConfigs() error = %v", err)
	}
	channel, err := resolveModelAdapterChannel(adapters, "anthropic-channel")
	if err != nil {
		t.Fatalf("resolveModelAdapterChannel() error = %v", err)
	}
	if channel.AnthropicAuthMode != modelchannel.AnthropicAuthModeBearer {
		t.Fatalf("resolved auth mode = %q", channel.AnthropicAuthMode)
	}
}

func TestNormalizeModelAdapterConfigsAnthropicAuthModeCompatibility(t *testing.T) {
	base := ModelAdapterConfig{
		DisplayName: "Claude", Type: "anthropic", BaseURL: "https://api.anthropic.com/v1", APIKey: "token", TooltipData: "test", ModelID: "claude-test",
	}
	legacy, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{base})
	if err != nil {
		t.Fatalf("NormalizeModelAdapterConfigs() legacy error = %v", err)
	}
	if legacy[0].AnthropicAuthMode != modelchannel.AnthropicAuthModeLegacyDual {
		t.Fatalf("legacy auth mode = %q", legacy[0].AnthropicAuthMode)
	}

	explicit, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{{
		DisplayName: "Claude", Type: "anthropic", BaseURL: "https://gateway.example/v1", APIKey: "token", TooltipData: "test", ModelID: "claude-test", AnthropicAuthMode: modelchannel.AnthropicAuthModeBearer,
	}})
	if err != nil {
		t.Fatalf("NormalizeModelAdapterConfigs() explicit error = %v", err)
	}
	if explicit[0].AnthropicAuthMode != modelchannel.AnthropicAuthModeBearer {
		t.Fatalf("explicit auth mode = %q", explicit[0].AnthropicAuthMode)
	}

	if _, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{{
		DisplayName: "Claude", Type: "anthropic", BaseURL: "https://gateway.example/v1", APIKey: "token", TooltipData: "test", ModelID: "claude-test", AnthropicAuthMode: "invalid",
	}}); err == nil {
		t.Fatal("invalid Anthropic auth mode was accepted")
	}
}
