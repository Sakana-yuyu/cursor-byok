package bridge

import (
	"time"

	"cursor/internal/client"
)

// ProviderDiagnosticsSnapshot is the Wails-safe provider runtime view.
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

// GetProviderDiagnostics performs no upstream request and exposes no raw channel object.
func (s *ProxyService) GetProviderDiagnostics() ProviderDiagnosticsSnapshot {
	if s == nil || s.core == nil {
		return ProviderDiagnosticsSnapshot{
			GeneratedAtUnixMS: time.Now().UnixMilli(),
			State:             "unavailable",
			ErrorCode:         "bridge_service_unavailable",
		}
	}
	return projectProviderDiagnostics(s.core.GetProviderDiagnostics())
}

func projectProviderDiagnostics(source client.ProviderDiagnosticsSnapshot) ProviderDiagnosticsSnapshot {
	result := ProviderDiagnosticsSnapshot{
		GeneratedAtUnixMS: source.GeneratedAtUnixMS,
		State:             source.State,
		ErrorCode:         source.ErrorCode,
		RouterAvailable:   source.RouterAvailable,
		Channels:          make([]ProviderChannelDiagnostic, 0, len(source.Channels)),
		ModelCatalogCache: ModelCatalogCacheDiagnostic{
			EntryCount:           source.ModelCatalogCache.EntryCount,
			TTLSeconds:           source.ModelCatalogCache.TTLSeconds,
			OldestStoredAtUnixMS: source.ModelCatalogCache.OldestStoredAtUnixMS,
			NextExpiryAtUnixMS:   source.ModelCatalogCache.NextExpiryAtUnixMS,
		},
	}
	for _, channel := range source.Channels {
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
	return result
}
