package forwarder

import (
	"context"
	"testing"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
)

type providerDiagnosticsGatewayStub struct {
	snapshot    modeladapter.ProviderDiagnosticsSnapshot
	streamCalls int
}

func (stub *providerDiagnosticsGatewayStub) StartStream(context.Context, ProviderRequest, func(modeladapter.ModelEvent) error) error {
	stub.streamCalls++
	return nil
}

func (stub *providerDiagnosticsGatewayStub) ProviderDiagnostics(context.Context) modeladapter.ProviderDiagnosticsSnapshot {
	return stub.snapshot
}

func TestProviderDiagnosticsDelegatesThroughCachingGatewayAndService(t *testing.T) {
	inner := &providerDiagnosticsGatewayStub{snapshot: modeladapter.ProviderDiagnosticsSnapshot{
		GeneratedAtUnixMS: 1234,
		State:             modeladapter.ProviderDiagnosticsStateReady,
		RouterAvailable:   true,
		Channels: []modeladapter.ProviderChannelDiagnostic{{
			ChannelID:   "channel-a",
			HealthState: modeladapter.ProviderChannelHealthReady,
		}},
	}}
	gateway := newCachingProviderGateway(inner, func() (bool, time.Duration, int, bool) {
		return true, time.Minute, 64, false
	}, "")
	service := &Service{provider: gateway}

	got := service.ProviderDiagnostics(context.Background())
	if !got.RouterAvailable || len(got.Channels) != 1 || got.Channels[0].ChannelID != "channel-a" {
		t.Fatalf("unexpected diagnostics snapshot %#v", got)
	}
	if inner.streamCalls != 0 {
		t.Fatalf("diagnostics triggered %d provider stream calls", inner.streamCalls)
	}
}

func TestProviderDiagnosticsUnavailableForLegacyGateway(t *testing.T) {
	service := &Service{provider: providerGatewayFunc(func(context.Context, ProviderRequest, func(modeladapter.ModelEvent) error) error {
		return nil
	})}
	got := service.ProviderDiagnostics(context.Background())
	if got.RouterAvailable || got.State != modeladapter.ProviderDiagnosticsStateUnavailable || got.GeneratedAtUnixMS <= 0 {
		t.Fatalf("unexpected legacy gateway snapshot %#v", got)
	}
}

type providerGatewayFunc func(context.Context, ProviderRequest, func(modeladapter.ModelEvent) error) error

func (fn providerGatewayFunc) StartStream(ctx context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	return fn(ctx, req, sink)
}
