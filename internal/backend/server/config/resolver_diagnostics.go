package config

import (
	"context"

	legacyruntime "cursor/internal/runtime"
)

// ResolvedChannelsForDiagnostics resolves every configured channel in stable
// configuration order without advancing the runtime round-robin cursor.
func (manager *Manager) ResolvedChannelsForDiagnostics(_ context.Context) ([]*legacyruntime.ResolvedChannel, error) {
	if manager == nil {
		return nil, legacyruntime.ErrChannelNotAvailable
	}
	adapters, err := NormalizeModelAdapterConfigs(manager.currentConfig().ModelAdapters)
	if err != nil {
		return nil, err
	}
	channels := make([]*legacyruntime.ResolvedChannel, 0, len(adapters))
	for _, adapter := range adapters {
		channel, resolveErr := resolveModelAdapterChannel([]ModelAdapterConfig{adapter}, adapter.ID)
		if resolveErr != nil {
			return nil, resolveErr
		}
		channels = append(channels, channel)
	}
	return channels, nil
}
