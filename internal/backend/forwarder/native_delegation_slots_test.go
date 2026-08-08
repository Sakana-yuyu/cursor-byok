package forwarder

import (
	"sync/atomic"
	"testing"

	"cursor/internal/backend/delegation"
)

// fakeDelegationConfigProvider 是可变的 delegation.RuntimeConfigProvider 测试替身。
type fakeDelegationConfigProvider struct {
	value atomic.Int32
}

func (fake *fakeDelegationConfigProvider) DelegationRuntimeConfig() delegation.RuntimeConfig {
	return delegation.RuntimeConfig{MaxConcurrency: int(fake.value.Load())}
}

func TestServiceNativeDelegationSlotDynamicLimit(t *testing.T) {
	provider := &fakeDelegationConfigProvider{}
	provider.value.Store(2)
	service := &Service{delegationConfig: provider}

	// 首次调用懒初始化限流器并读取配置。
	service.delegationRuntimeMu.Lock()
	if !service.acquireNativeDelegationSlotLocked() || !service.acquireNativeDelegationSlotLocked() {
		t.Fatalf("two acquires at configured limit 2 should succeed")
	}
	if service.acquireNativeDelegationSlotLocked() {
		t.Fatalf("third acquire at configured limit 2 must fail")
	}
	service.delegationRuntimeMu.Unlock()

	// 配置热更新扩容：无需重启或替换信号量即可放行。
	provider.value.Store(5)
	service.delegationRuntimeMu.Lock()
	for i := 0; i < 3; i++ {
		if !service.acquireNativeDelegationSlotLocked() {
			t.Fatalf("acquire %d after expand to 5 should succeed", i+3)
		}
	}
	if service.acquireNativeDelegationSlotLocked() {
		t.Fatalf("acquire beyond expanded limit 5 must fail")
	}
	service.delegationRuntimeMu.Unlock()

	// 缩容：active(5) 高于新上限 3，不再放行；运行中任务释放后恢复。
	provider.value.Store(3)
	service.delegationRuntimeMu.Lock()
	if service.acquireNativeDelegationSlotLocked() {
		t.Fatalf("acquire while active(5) > shrunk limit(3) must fail")
	}
	service.delegationRuntimeMu.Unlock()

	service.delegationRuntimeMu.Lock()
	service.releaseNativeDelegationSlotLocked()
	service.releaseNativeDelegationSlotLocked()
	service.releaseNativeDelegationSlotLocked()
	// active 降到 2（<= 新上限 3）后应恢复放行。
	if !service.acquireNativeDelegationSlotLocked() {
		t.Fatalf("acquire after active dropped below shrunk limit should succeed")
	}
	service.delegationRuntimeMu.Unlock()

	// 清理：释放剩余槽位。
	service.delegationRuntimeMu.Lock()
	service.releaseNativeDelegationSlotLocked()
	service.releaseNativeDelegationSlotLocked()
	service.releaseNativeDelegationSlotLocked()
	service.delegationRuntimeMu.Unlock()
}

func TestServiceNativeDelegationSlotDoubleReleaseSafe(t *testing.T) {
	service := &Service{}
	service.delegationRuntimeMu.Lock()
	defer service.delegationRuntimeMu.Unlock()
	if !service.acquireNativeDelegationSlotLocked() {
		t.Fatalf("acquire should succeed with fallback limit")
	}
	service.releaseNativeDelegationSlotLocked()
	service.releaseNativeDelegationSlotLocked()
	service.releaseNativeDelegationSlotLocked()
	if !service.acquireNativeDelegationSlotLocked() {
		t.Fatalf("acquire after double release should still succeed")
	}
	service.releaseNativeDelegationSlotLocked()
}
