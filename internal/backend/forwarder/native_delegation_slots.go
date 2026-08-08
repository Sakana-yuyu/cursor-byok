package forwarder

import "cursor/internal/backend/delegation"

// acquireNativeDelegationSlot 非阻塞获取原生子代理并发槽位。
// 调用方必须已持有 delegationRuntimeMu（懒初始化与登记在同一临界区内完成）。
//
// 与旧的固定容量 channel 信号量不同：这里把信号量挂在 Service 上并在首次
// 调用时懒初始化，每次 tryAcquire 都会通过 resolve 动态读取当前
// delegation.maxConcurrency。配置热更新（含缩容/扩容）立即生效，不需要
// 替换信号量对象；运行中任务持有的槽位在释放前不受配置变化影响。
func (service *Service) acquireNativeDelegationSlotLocked() bool {
	if service == nil {
		return false
	}
	if service.nativeDelegationLimiter == nil {
		service.nativeDelegationLimiter = newNativeDelegationLimiter(service.nativeDelegationConcurrencyLimit, defaultNativeDelegationConcurrencyLimit)
	}
	return service.nativeDelegationLimiter.tryAcquire()
}

// releaseNativeDelegationSlotLocked 释放原生子代理并发槽位。
// 调用方必须已持有 delegationRuntimeMu；重复释放安全（见 nativeDelegationLimiter.release）。
func (service *Service) releaseNativeDelegationSlotLocked() {
	if service == nil || service.nativeDelegationLimiter == nil {
		return
	}
	service.nativeDelegationLimiter.release()
}

// nativeDelegationConcurrencyLimit 解析当前 delegation.maxConcurrency 配置；
// 未配置或非正值时返回 0，由限流器回退默认上限。
func (service *Service) nativeDelegationConcurrencyLimit() int {
	if service == nil || service.delegationConfig == nil {
		return 0
	}
	config := delegation.NormalizeRuntimeConfig(service.delegationConfig.DelegationRuntimeConfig())
	if config.MaxConcurrency > 0 {
		return config.MaxConcurrency
	}
	return 0
}
