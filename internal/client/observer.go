package client

import (
	"context"

	localruntime "cursor/internal/runtime"
)

// runtimeConfigSnapshot 用于处理与 runtimeConfigSnapshot 相关的逻辑。
func (s *ProxyService) runtimeConfigSnapshot(_ context.Context) (localruntime.RuntimeConfigSnapshot, error) {
	if s == nil {
		return localruntime.RuntimeConfigSnapshot{}, nil
	}
	if host := s.currentBackendHost(); host != nil && host.ConfigManager() != nil {
		return host.ConfigManager().LegacyRuntimeSnapshot(context.Background())
	}
	if s.store == nil {
		return localruntime.RuntimeConfigSnapshot{}, nil
	}
	return s.store.LegacyRuntimeSnapshot(context.Background())
}
