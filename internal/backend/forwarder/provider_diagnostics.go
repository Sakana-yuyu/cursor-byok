package forwarder

import (
	"context"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
)

type providerDiagnosticsSource interface {
	ProviderDiagnostics(context.Context) modeladapter.ProviderDiagnosticsSnapshot
}

func unavailableProviderDiagnostics() modeladapter.ProviderDiagnosticsSnapshot {
	return modeladapter.ProviderDiagnosticsSnapshot{
		GeneratedAtUnixMS: time.Now().UnixMilli(),
		State:             modeladapter.ProviderDiagnosticsStateUnavailable,
		ErrorCode:         "provider_gateway_unavailable",
	}
}

// ProviderDiagnostics delegates to the live Router without changing ProviderGateway,
// so existing test fakes only implementing StartStream remain source-compatible.
func (gateway *DefaultProviderGateway) ProviderDiagnostics(ctx context.Context) modeladapter.ProviderDiagnosticsSnapshot {
	if gateway == nil || gateway.router == nil {
		return unavailableProviderDiagnostics()
	}
	source, ok := gateway.router.(providerDiagnosticsSource)
	if !ok {
		return unavailableProviderDiagnostics()
	}
	return source.ProviderDiagnostics(ctx)
}

func (gateway *cachingProviderGateway) ProviderDiagnostics(ctx context.Context) modeladapter.ProviderDiagnosticsSnapshot {
	if gateway == nil || gateway.inner == nil {
		return unavailableProviderDiagnostics()
	}
	source, ok := gateway.inner.(providerDiagnosticsSource)
	if !ok {
		return unavailableProviderDiagnostics()
	}
	return source.ProviderDiagnostics(ctx)
}

func (service *Service) ProviderDiagnostics(ctx context.Context) modeladapter.ProviderDiagnosticsSnapshot {
	if service == nil || service.provider == nil {
		return unavailableProviderDiagnostics()
	}
	source, ok := service.provider.(providerDiagnosticsSource)
	if !ok {
		return unavailableProviderDiagnostics()
	}
	return source.ProviderDiagnostics(ctx)
}
