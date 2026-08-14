//go:build benchmark

package client

import (
	"strings"

	serverconfig "cursor/internal/backend/server/config"
)

// RunModelAdapterThroughputProbe 只测试当前渠道已配置的协议组合。
// 它不触发协议回退、不保存测速结果，也不会改写用户配置，适合隔离性能取证。
// 该入口仅随 -tags benchmark 编译，避免测速 CLI 逻辑进入桌面发行物。
func (s *ProxyService) RunModelAdapterThroughputProbe(adapter serverconfig.ModelAdapterConfig) (ModelAdapterTestResult, error) {
	normalized, err := normalizeSingleModelAdapterConfig(adapter)
	if err != nil {
		return buildErroredModelAdapterTestResult(strings.TrimSpace(adapter.ID), buildModelAdapterTestRequestHash(adapter), err), err
	}
	requestHash := buildModelAdapterTestRequestHash(adapter)
	return s.runModelAdapterTestWithFallback(normalized, requestHash, false)
}
