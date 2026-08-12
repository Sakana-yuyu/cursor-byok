package config

import (
	"context"
	"strings"

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

// ModelSourceForModel 返回当前配置中模型应使用的来源，仅供缓存等不应跨来源共享
// 的本地元数据域使用。它不推进轮询游标，也不触发任何网络请求。
func (manager *Manager) ModelSourceForModel(_ context.Context, modelID string) string {
	if manager == nil {
		return legacyruntime.ModelSourceThirdParty
	}
	adapters, err := NormalizeModelAdapterConfigs(manager.Current().ModelAdapters)
	if err != nil {
		return legacyruntime.ModelSourceThirdParty
	}
	matches := modelchannel.ResolveAdapterIndexes(
		adapters,
		modelID,
		func(adapter ModelAdapterConfig) string { return adapter.ID },
		func(adapter ModelAdapterConfig) string { return adapter.ModelID },
		func(adapter ModelAdapterConfig) string {
			return modelchannel.BuildLegacyChannelID(adapter.BaseURL, adapter.ModelID, adapter.APIKey, adapter.DisplayName)
		},
		func(adapter ModelAdapterConfig) string {
			return modelchannel.BuildChannelID(adapter.BaseURL, adapter.ModelID, adapter.APIKey, adapter.DisplayName, adapter.OpenAIEndpoint)
		},
	)
	if len(matches) == 0 {
		return legacyruntime.ModelSourceThirdParty
	}
	if source := selectModelSource(adapters, matches, modelID); source != "" {
		return source
	}
	return legacyruntime.ModelSourceThirdParty
}

func (manager *Manager) SelectChannelsForModel(_ context.Context, modelID string) ([]*legacyruntime.ResolvedChannel, error) {
	if manager == nil {
		return nil, legacyruntime.ErrChannelNotAvailable
	}
	adapters, err := NormalizeModelAdapterConfigs(manager.Current().ModelAdapters)
	if err != nil {
		return nil, err
	}
	matches := modelchannel.ResolveAdapterIndexes(
		adapters,
		modelID,
		func(adapter ModelAdapterConfig) string { return adapter.ID },
		func(adapter ModelAdapterConfig) string { return adapter.ModelID },
		func(adapter ModelAdapterConfig) string {
			return modelchannel.BuildLegacyChannelID(adapter.BaseURL, adapter.ModelID, adapter.APIKey, adapter.DisplayName)
		},
		func(adapter ModelAdapterConfig) string {
			return modelchannel.BuildChannelID(adapter.BaseURL, adapter.ModelID, adapter.APIKey, adapter.DisplayName, adapter.OpenAIEndpoint)
		},
	)
	if len(matches) == 0 {
		return nil, legacyruntime.ErrChannelNotAvailable
	}
	selectedSource := selectModelSource(adapters, matches, modelID)
	if selectedSource == "" {
		return nil, legacyruntime.ErrChannelNotAvailable
	}
	filteredMatches := make([]int, 0, len(matches))
	for _, index := range matches {
		if adapters[index].Source == selectedSource {
			filteredMatches = append(filteredMatches, index)
		}
	}
	if len(filteredMatches) == 0 {
		return nil, legacyruntime.ErrChannelNotAvailable
	}
	manager.selectionMu.Lock()
	if manager.selectionOffsets == nil {
		manager.selectionOffsets = make(map[string]int)
	}
	key := selectedSource + "\x00" + strings.TrimSpace(modelID)
	offset := manager.selectionOffsets[key] % len(filteredMatches)
	manager.selectionOffsets[key] = (offset + 1) % len(filteredMatches)
	manager.selectionMu.Unlock()
	channels := make([]*legacyruntime.ResolvedChannel, 0, len(filteredMatches))
	for index := 0; index < len(filteredMatches); index++ {
		adapter := adapters[filteredMatches[(offset+index)%len(filteredMatches)]]
		channel, resolveErr := resolveModelAdapterChannel([]ModelAdapterConfig{adapter}, adapter.ID)
		if resolveErr != nil {
			return nil, resolveErr
		}
		channels = append(channels, channel)
	}
	return channels, nil
}

func selectModelSource(adapters []ModelAdapterConfig, matches []int, requestedModel string) string {
	requestedModel = strings.TrimSpace(requestedModel)
	for _, index := range matches {
		if strings.TrimSpace(adapters[index].ID) == requestedModel {
			return adapters[index].Source
		}
	}
	for _, index := range matches {
		if adapters[index].Source == legacyruntime.ModelSourceThirdParty {
			return legacyruntime.ModelSourceThirdParty
		}
	}
	return adapters[matches[0]].Source
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
		func(adapter ModelAdapterConfig) string {
			return modelchannel.BuildChannelID(adapter.BaseURL, adapter.ModelID, adapter.APIKey, adapter.DisplayName, adapter.OpenAIEndpoint)
		},
	)
	if len(matchIndexes) == 0 {
		return nil, legacyruntime.ErrChannelNotAvailable
	}
	matchIndex := matchIndexes[0]
	matched := adapters[matchIndex]

	resolved := &legacyruntime.ResolvedChannel{
		ID:                          strings.TrimSpace(matched.ID),
		Source:                      strings.TrimSpace(matched.Source),
		CredentialScope:             strings.TrimSpace(matched.CredentialScope),
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
		ContextWindowTokens:         defaultChannelContextWindowTokens,
		MaxTokens:                   defaultChannelMaxTokens,
		ReasoningEffort:             strings.TrimSpace(matched.ReasoningEffort),
		AnthropicMaxTokens:          defaultChannelMaxTokens,
		AnthropicThinkingEffort:     defaultChannelAnthropicEffort,
		FastMode:                    matched.FastMode,
		OpenAIServiceTier:           strings.TrimSpace(matched.OpenAIServiceTier),
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
