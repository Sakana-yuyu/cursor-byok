package backend

import (
	"context"
	"sync/atomic"
	"testing"

	"cursor/internal/backend/delegation"
)

func TestRefreshDelegationExecutorProbesForcesRegistryProbe(t *testing.T) {
	registry := delegation.NewExecutorRegistry(delegation.ExecutorRegistryConfig{})
	var calls atomic.Int32
	if err := registry.Register(delegation.ExecutorRegistration{
		ID:      "test-cli",
		Enabled: true,
		Probe: func(context.Context) (delegation.ExecutorProbeResult, error) {
			calls.Add(1)
			return delegation.ExecutorProbeResult{State: delegation.ExecutorProbeReady}, nil
		},
		Execute: func(context.Context, delegation.TaskRequest) delegation.TaskResult { return delegation.TaskResult{} },
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	host := &Host{executorRegistry: registry}
	if _, err := host.RefreshDelegationExecutorProbes(t.Context()); err != nil {
		t.Fatalf("first refresh error = %v", err)
	}
	if _, err := host.RefreshDelegationExecutorProbes(t.Context()); err != nil {
		t.Fatalf("second refresh error = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("probe calls = %d, want 2 forced probes", calls.Load())
	}
}
