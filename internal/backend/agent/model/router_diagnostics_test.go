package modeladapter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	legacyruntime "cursor/internal/runtime"
)

type diagnosticsResolverStub struct {
	channels       []*legacyruntime.ResolvedChannel
	diagnosticsErr error
	selectCalls    int
}

func (resolver *diagnosticsResolverStub) SelectChannelForModel(context.Context, string) (*legacyruntime.ResolvedChannel, error) {
	resolver.selectCalls++
	if len(resolver.channels) == 0 {
		return nil, legacyruntime.ErrChannelNotAvailable
	}
	return resolver.channels[0], nil
}

func (resolver *diagnosticsResolverStub) SelectChannelsForModel(context.Context, string) ([]*legacyruntime.ResolvedChannel, error) {
	resolver.selectCalls++
	return resolver.channels, nil
}

func (resolver *diagnosticsResolverStub) ResolvedChannelsForDiagnostics(context.Context) ([]*legacyruntime.ResolvedChannel, error) {
	return resolver.channels, resolver.diagnosticsErr
}

func (*diagnosticsResolverStub) ProviderStreamIdleTimeout(context.Context) time.Duration {
	return time.Minute
}
func (*diagnosticsResolverStub) TurnStaleTimeout(context.Context) time.Duration { return time.Minute }
func (*diagnosticsResolverStub) NativeDelegationProgressTimeout(context.Context) time.Duration {
	return time.Minute
}

func TestRouterProviderDiagnosticsReportsSafeReadyAndCoolingChannels(t *testing.T) {
	resolver := &diagnosticsResolverStub{channels: []*legacyruntime.ResolvedChannel{
		{
			ID:                       "ready-channel",
			Name:                     "Ready",
			GroupName:                "Primary",
			Provider:                 "openai",
			ProtocolMode:             "auto",
			ProtocolGroup:            "responses",
			BaseURL:                  "https://user:password@provider.example:8443/v1?access_token=query-secret#fragment",
			APIKey:                   "sk-router-secret",
			Model:                    "provider-model",
			ContextWindowTokens:      200000,
			MaxTokens:                65536,
			CustomHeadersEnabled:     true,
			CustomHeadersJSON:        `{"Authorization":"Bearer header-secret"}`,
			OpenAIExtraParamsJSON:    `{"secret":"nested-secret"}`,
			AnthropicExtraParamsJSON: `{"secret":"anthropic-secret"}`,
		},
		{
			ID:       "cooling-channel",
			Name:     "Cooling",
			Provider: "anthropic",
			BaseURL:  "not a valid endpoint with embedded-secret",
			APIKey:   "sk-other-secret",
			Model:    "claude-model",
		},
	}}
	future := time.Now().Add(5 * time.Minute)
	router := &Router{
		resolver: resolver,
		healthByChannel: map[string]channelHealth{
			channelHealthMapKey(resolver.channels[1]): {cooldownUntil: future},
			"expired-health-key":                      {cooldownUntil: time.Now().Add(-time.Minute)},
		},
	}

	snapshot := router.ProviderDiagnostics(context.Background())
	if !snapshot.RouterAvailable || snapshot.State != ProviderDiagnosticsStateReady {
		t.Fatalf("expected ready router snapshot, got %#v", snapshot)
	}
	if resolver.selectCalls != 0 {
		t.Fatalf("diagnostics changed runtime selection state with %d selection calls", resolver.selectCalls)
	}
	if len(snapshot.Channels) != 2 {
		t.Fatalf("len(channels) = %d, want 2", len(snapshot.Channels))
	}
	ready := snapshot.Channels[0]
	if ready.EndpointScheme != "https" || ready.EndpointHost != "provider.example:8443" {
		t.Fatalf("safe endpoint projection = %q://%q", ready.EndpointScheme, ready.EndpointHost)
	}
	if !ready.CredentialConfigured || !ready.CustomHeadersConfigured || ready.HealthState != ProviderChannelHealthReady {
		t.Fatalf("unexpected ready channel %#v", ready)
	}
	cooling := snapshot.Channels[1]
	if cooling.HealthState != ProviderChannelHealthCooldown || cooling.CooldownUntilUnixMS <= snapshot.GeneratedAtUnixMS {
		t.Fatalf("unexpected cooling channel %#v", cooling)
	}
	if cooling.EndpointScheme != "" || cooling.EndpointHost != "" {
		t.Fatalf("invalid endpoint should not be returned: %#v", cooling)
	}

	router.healthMu.Lock()
	_, expiredStillPresent := router.healthByChannel["expired-health-key"]
	router.healthMu.Unlock()
	if !expiredStillPresent {
		t.Fatal("read-only diagnostics mutated expired cooldown state")
	}

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	serialized := string(encoded)
	for _, forbidden := range []string{
		"sk-router-secret", "sk-other-secret", "query-secret", "header-secret", "nested-secret", "anthropic-secret",
		"user:password", "embedded-secret", "apiKey", "baseURL", "customHeadersJSON", "openAIExtraParamsJSON", "anthropicExtraParamsJSON",
	} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("diagnostics JSON leaked %q: %s", forbidden, serialized)
		}
	}
}

func TestRouterProviderDiagnosticsReportsResolverFailure(t *testing.T) {
	router := NewRouter(&diagnosticsResolverStub{diagnosticsErr: errors.New("invalid config with secret")})
	snapshot := router.ProviderDiagnostics(context.Background())
	if snapshot.State != ProviderDiagnosticsStateError || snapshot.ErrorCode != "channel_resolution_failed" || snapshot.RouterAvailable {
		t.Fatalf("unexpected resolver failure snapshot %#v", snapshot)
	}
}

func TestRouterProviderDiagnosticsUnavailableIsStable(t *testing.T) {
	var router *Router
	snapshot := router.ProviderDiagnostics(context.Background())
	if snapshot.RouterAvailable || snapshot.State != ProviderDiagnosticsStateUnavailable || len(snapshot.Channels) != 0 || snapshot.GeneratedAtUnixMS <= 0 {
		t.Fatalf("unexpected unavailable snapshot %#v", snapshot)
	}
}
