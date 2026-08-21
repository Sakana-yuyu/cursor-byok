package client

import (
	"context"
	"time"
)

// ProviderDiagnosticsSnapshot is the client-layer aggregate returned to the desktop bridge.
type ProviderDiagnosticsSnapshot struct {
	GeneratedAtUnixMS int64                       `json:"generatedAtUnixMs"`
	State             string                      `json:"state"`
	ErrorCode         string                      `json:"errorCode,omitempty"`
	RouterAvailable   bool                        `json:"routerAvailable"`
	Channels          []ProviderChannelDiagnostic `json:"channels"`
	ModelCatalogCache ModelCatalogCacheDiagnostic `json:"modelCatalogCache"`
}

type ProviderChannelDiagnostic struct {
	ChannelID               string `json:"channelId"`
	DisplayName             string `json:"displayName"`
	GroupName               string `json:"groupName,omitempty"`
	Provider                string `json:"provider"`
	ProtocolMode            string `json:"protocolMode"`
	ProtocolGroup           string `json:"protocolGroup"`
	ModelID                 string `json:"modelId"`
	EndpointScheme          string `json:"endpointScheme,omitempty"`
	EndpointHost            string `json:"endpointHost,omitempty"`
	ContextWindowTokens     int    `json:"contextWindowTokens,omitempty"`
	MaxCompletionTokens     int    `json:"maxCompletionTokens,omitempty"`
	CredentialConfigured    bool   `json:"credentialConfigured"`
	CustomHeadersConfigured bool   `json:"customHeadersConfigured"`
	HealthState             string `json:"healthState"`
	CooldownUntilUnixMS     int64  `json:"cooldownUntilUnixMs,omitempty"`
}

type ModelCatalogCacheDiagnostic struct {
	EntryCount           int   `json:"entryCount"`
	TTLSeconds           int64 `json:"ttlSeconds"`
	OldestStoredAtUnixMS int64 `json:"oldestStoredAtUnixMs,omitempty"`
	NextExpiryAtUnixMS   int64 `json:"nextExpiryAtUnixMs,omitempty"`
}

// GetProviderDiagnostics is read-only: it performs no provider requests, probes,
// cache invalidation, cooldown clearing, or channel selection.
func (s *ProxyService) GetProviderDiagnostics() ProviderDiagnosticsSnapshot {
	now := time.Now()
	result := ProviderDiagnosticsSnapshot{
		GeneratedAtUnixMS: now.UnixMilli(),
		State:             "unavailable",
		ErrorCode:         "client_service_unavailable",
	}
	if s == nil {
		return result
	}
	s.lifecycleMu.Lock()
	host := s.backendHost
	s.lifecycleMu.Unlock()
	if host != nil {
		router := host.ProviderDiagnostics(context.Background())
		if router.GeneratedAtUnixMS > 0 {
			result.GeneratedAtUnixMS = router.GeneratedAtUnixMS
		}
		result.State = router.State
		result.ErrorCode = router.ErrorCode
		result.RouterAvailable = router.RouterAvailable
		result.Channels = make([]ProviderChannelDiagnostic, 0, len(router.Channels))
		for _, channel := range router.Channels {
			result.Channels = append(result.Channels, ProviderChannelDiagnostic{
				ChannelID:               channel.ChannelID,
				DisplayName:             channel.DisplayName,
				GroupName:               channel.GroupName,
				Provider:                channel.Provider,
				ProtocolMode:            channel.ProtocolMode,
				ProtocolGroup:           channel.ProtocolGroup,
				ModelID:                 channel.ModelID,
				EndpointScheme:          channel.EndpointScheme,
				EndpointHost:            channel.EndpointHost,
				ContextWindowTokens:     channel.ContextWindowTokens,
				MaxCompletionTokens:     channel.MaxCompletionTokens,
				CredentialConfigured:    channel.CredentialConfigured,
				CustomHeadersConfigured: channel.CustomHeadersConfigured,
				HealthState:             channel.HealthState,
				CooldownUntilUnixMS:     channel.CooldownUntilUnixMS,
			})
		}
	} else {
		result.ErrorCode = "backend_host_unavailable"
	}
	cache := s.modelCatalogCache.diagnostics(now)
	result.ModelCatalogCache = ModelCatalogCacheDiagnostic{
		EntryCount:           cache.EntryCount,
		TTLSeconds:           cache.TTLSeconds,
		OldestStoredAtUnixMS: cache.OldestStoredAtUnixMS,
		NextExpiryAtUnixMS:   cache.NextExpiryAtUnixMS,
	}
	return result
}
