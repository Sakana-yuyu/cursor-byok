package backend

import (
	"context"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
)

// ProviderDiagnostics returns the active forwarder's live Router snapshot.
// It never reconstructs health from configuration or raw debug logs.
func (host *Host) ProviderDiagnostics(ctx context.Context) modeladapter.ProviderDiagnosticsSnapshot {
	unavailable := func(code string) modeladapter.ProviderDiagnosticsSnapshot {
		return modeladapter.ProviderDiagnosticsSnapshot{
			GeneratedAtUnixMS: time.Now().UnixMilli(),
			State:             modeladapter.ProviderDiagnosticsStateUnavailable,
			ErrorCode:         code,
		}
	}
	if host == nil {
		return unavailable("backend_host_unavailable")
	}
	host.runMu.RLock()
	module := host.agentModule
	running := host.httpServer != nil
	host.runMu.RUnlock()
	if !running {
		return unavailable("backend_not_running")
	}
	if module == nil || module.Service == nil {
		return unavailable("provider_module_unavailable")
	}
	return module.Service.ProviderDiagnostics(ctx)
}
