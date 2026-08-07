package bridge

import (
	"cursor/internal/client"
	"testing"
)

func TestIsCursorProxyReady(t *testing.T) {
	tests := []struct {
		name  string
		state client.ProxyState
		want  bool
	}{
		{
			name: "all ready",
			state: client.ProxyState{
				BackendRunning:        true,
				ProxyRunning:          true,
				CursorSettingsApplied: true,
			},
			want: true,
		},
		{
			name: "proxy listener missing",
			state: client.ProxyState{
				BackendRunning:        true,
				CursorSettingsApplied: true,
			},
		},
		{
			name: "cursor settings not verified",
			state: client.ProxyState{
				BackendRunning: true,
				ProxyRunning:   true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCursorProxyReady(tt.state); got != tt.want {
				t.Fatalf("isCursorProxyReady() = %t, want %t", got, tt.want)
			}
		})
	}
}
