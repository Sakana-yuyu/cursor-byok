package backend

import (
	"context"
	"testing"

	modeladapter "cursor/internal/backend/agent/model"
)

func TestProviderDiagnosticsUnavailableWithoutActiveModule(t *testing.T) {
	for _, host := range []*Host{nil, &Host{}} {
		snapshot := host.ProviderDiagnostics(context.Background())
		if snapshot.RouterAvailable || snapshot.State != modeladapter.ProviderDiagnosticsStateUnavailable || len(snapshot.Channels) != 0 || snapshot.GeneratedAtUnixMS <= 0 {
			t.Fatalf("unexpected unavailable snapshot %#v", snapshot)
		}
	}
}
