package config

import (
	"context"
	"strings"

	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/modelchannel"
	legacyruntime "cursor/internal/runtime"
)

const (
	defaultChannelContextWindowTokens = 200_000
	defaultChannelMaxTokens           = 65_536
	defaultChannelAnthropicEffort     = "xhigh"
)

func (manager *Manager) SelectChannelForModel(ctx context.Context, modelID string) (*legacyruntime.ResolvedChannel, error) {
	channels, err := manager.SelectChannelsForModel(ctx, modelID)
	if err != nil || len(channels) == 0 {
		return nil, err
	}
	return channels[0], nil
}

func (manager *Manager) SelectChannelsForModel(_ context.Context, modelID string) ([]*legacyruntime.ResolvedChannel, error) {
	if manager == nil {
		return nil, legacyruntime.ErrChannelNotAvailable
	}
	adapters, err := NormalizeModelAdapterConfigs(manager.Current().ModelAdapters)
	if err != nil {
		return nil, err
	}
	syncExplicitCompatibilityKindOverrides(adapters)
	matches := modelchannel.ResolveAdapterIndexes(
		adapters,
		modelID,
		func(adapter ModelAdapterConfig) string { return adapter.ID },
		func(adapter ModelAdapterConfig) string { return adapter.ModelID },
		func(adapter ModelAdapterConfig) string {
			return modelchannel.BuildLegacyChannelID(adapter.BaseURL, adapter.ModelID, adapter.APIKey, adapter.DisplayName)
		},
	)
	if len(matches) == 0 {
		return nil, legacyruntime.ErrChannelNotAvailable
	}
	manager.selectionMu.Lock()
	if manager.selectionOffsets == nil {
		manager.selectionOffsets = make(map[string]int)
	}
	key := strings.TrimSpace(modelID)
	offset := manager.selectionOffsets[key] % len(matches)
	manager.selectionOffsets[key] = (offset + 1) % len(matches)
	manager.selectionMu.Unlock()
	channels := make([]*legacyruntime.ResolvedChannel, 0, len(matches))
	for index := 0; index < len(matches); index++ {
		adapter := adapters[matches[(offset+index)%len(matches)]]
		channel, resolveErr := resolveModelAdapterChannel([]ModelAdapterConfig{adapter}, adapter.ID)
		if resolveErr != nil {
			return nil, resolveErr
		}
		channels = append(channels, channel)
	}
	return channels, nil
}

func resolveModelAdapterChannel(adapters []ModelAdapterConfig, requestedModel string) (*legacyruntime.ResolvedChannel, error) {
	matchIndexes := modelchannel.ResolveAdapterIndexes(
		adapters,
		requestedModel,
		func(adapter ModelAdapterConfig) string { return adapter.ID },
		func(adapter ModelAdapterConfig) string { return adapter.ModelID },
		func(adapter ModelAdapterConfig) string {
			return modelchannel.BuildLegacyChannelID(adapter.BaseURL, adapter.ModelID, adapter.APIKey, adapter.DisplayName)
		},
	)
	if len(matchIndexes) == 0 {
		return nil, legacyruntime.ErrChannelNotAvailable
	}
	matchIndex := matchIndexes[0]
	matched := adapters[matchIndex]

	resolved := &legacyruntime.ResolvedChannel{
		ID:                          strings.TrimSpace(matched.ID),
		Name:                        strings.TrimSpace(matched.DisplayName),
		GroupName:                   strings.TrimSpace(matched.GroupName),
		Provider:                    strings.TrimSpace(matched.Type),
		ProtocolMode:                strings.TrimSpace(matched.ProtocolMode),
		ProtocolGroup:               strings.TrimSpace(matched.ProtocolGroup),
		BaseURL:                     strings.TrimSpace(matched.BaseURL),
		APIKey:                      strings.TrimSpace(matched.APIKey),
		Model:                       strings.TrimSpace(matched.ModelID),
		OpenAIEndpoint:              strings.TrimSpace(matched.OpenAIEndpoint),
		OpenAIRequestGroup:          strings.TrimSpace(matched.OpenAIRequestGroup),
		OpenAIExtraParamsEnabled:    matched.OpenAIExtraParamsEnabled,
		OpenAIExtraParamsJSON:       strings.TrimSpace(matched.OpenAIExtraParamsJSON),
		CustomHeadersEnabled:        matched.CustomHeadersEnabled,
		CustomHeadersJSON:           strings.TrimSpace(matched.CustomHeadersJSON),
		AnthropicExtraParamsEnabled: matched.AnthropicExtraParamsEnabled,
		AnthropicExtraParamsJSON:    strings.TrimSpace(matched.AnthropicExtraParamsJSON),
		AnthropicAuthMode:           strings.TrimSpace(matched.AnthropicAuthMode),
		ContextWindowTokens:         defaultChannelContextWindowTokens,
		MaxTokens:                   defaultChannelMaxTokens,
		ReasoningEffort:             strings.TrimSpace(matched.ReasoningEffort),
		AnthropicMaxTokens:          defaultChannelMaxTokens,
		AnthropicThinkingEffort:     defaultChannelAnthropicEffort,
		FastMode:                    matched.FastMode,
		OpenAIServiceTier:           strings.TrimSpace(matched.OpenAIServiceTier),
		ToolCallMode:                strings.TrimSpace(matched.ToolCallMode),
	}
	if matched.ContextWindowTokens > 0 {
		resolved.ContextWindowTokens = matched.ContextWindowTokens
	}
	if matched.MaxCompletionTokens > 0 {
		resolved.MaxTokens = matched.MaxCompletionTokens
	}
	if matched.AnthropicMaxTokens > 0 {
		resolved.AnthropicMaxTokens = matched.AnthropicMaxTokens
	}
	if matched.ThinkingBudgetTokens > 0 {
		resolved.ThinkingBudgetTokens = matched.ThinkingBudgetTokens
	}
	if strings.TrimSpace(matched.AnthropicThinkingEffort) != "" {
		resolved.AnthropicThinkingEffort = strings.TrimSpace(matched.AnthropicThinkingEffort)
	}
	return resolved, nil
}

// syncExplicitCompatibilityKindOverrides 把当前配置中的显式 compatibilityKind
// 全量同步到 modeladapter 运行时覆盖表。每次渠道选择都执行，保证热加载/删除渠道后
// 覆盖表自动收敛（用户清空字段或删除适配器不会残留旧值）。键为 baseURL+modelID，
// 与 classify 请求侧可用的信号一致。
func syncExplicitCompatibilityKindOverrides(adapters []ModelAdapterConfig) {
	overrides := make(map[string]string, len(adapters))
	for _, adapter := range adapters {
		if strings.TrimSpace(adapter.CompatibilityKind) == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(adapter.BaseURL)) + "\n" + strings.ToLower(strings.TrimSpace(adapter.ModelID))
		overrides[key] = adapter.CompatibilityKind
	}
	modeladapter.SetExplicitCompatibilityKindOverrides(overrides)
}
