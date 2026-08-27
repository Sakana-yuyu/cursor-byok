package config

import (
	"context"

	legacyruntime "cursor/internal/runtime"
)

func (store *Store) LegacyRuntimeSnapshot(ctx context.Context) (legacyruntime.RuntimeConfigSnapshot, error) {
	cfg, err := store.Load(ctx)
	if err != nil {
		return legacyruntime.RuntimeConfigSnapshot{}, err
	}

	adapters := make([]legacyruntime.ModelAdapterConfig, 0, len(cfg.ModelAdapters))
	for _, item := range cfg.ModelAdapters {
		adapters = append(adapters, legacyruntime.ModelAdapterConfig{
			ID:              item.ID,
			Source:          item.Source,
			CredentialScope: item.CredentialScope,
			DisplayName:     item.DisplayName,
			Type:            item.Type,
			SupplierID:      item.SupplierID,
			BaseURL:         item.BaseURL,

			APIKey:                      item.APIKey,
			TooltipData:                 item.TooltipData,
			ModelID:                     item.ModelID,
			ReasoningEffort:             item.ReasoningEffort,
			OpenAIEndpoint:              item.OpenAIEndpoint,
			OpenAIExtraParamsEnabled:    item.OpenAIExtraParamsEnabled,
			OpenAIExtraParamsJSON:       item.OpenAIExtraParamsJSON,
			CustomHeadersEnabled:        item.CustomHeadersEnabled,
			CustomHeadersJSON:           item.CustomHeadersJSON,
			AnthropicExtraParamsEnabled: item.AnthropicExtraParamsEnabled,
			AnthropicExtraParamsJSON:    item.AnthropicExtraParamsJSON,
			AnthropicAuthMode:           item.AnthropicAuthMode,
			ContextWindowTokens:         item.ContextWindowTokens,
			MaxCompletionTokens:         item.MaxCompletionTokens,
			AnthropicMaxTokens:          item.AnthropicMaxTokens,
			AnthropicThinkingEffort:     item.AnthropicThinkingEffort,
			ThinkingBudgetTokens:        item.ThinkingBudgetTokens,
			Pricing:                     item.Pricing,
			FastMode:                    item.FastMode,
			OpenAIServiceTier:           item.OpenAIServiceTier,
			Disabled:                    item.Disabled,
		})
	}

	return legacyruntime.RuntimeConfigSnapshot{
		ObservabilityLogEnabled:   cfg.Log,
		ProviderStreamIdleTimeout: cfg.ProviderStreamIdleTimeout,
		ModelAdapters:             adapters,
	}, nil
}
