package forwarder

import "sync"

// nativeDelegationLimiter 是直接 Task→Cursor 原生子代理的全局并发限流器。
//
// 与基于 channel 的信号量不同，本实现不固定容量：每次 acquire 都动态读取
// 当前 delegation.maxConcurrency（通过 limit provider 解析），因此配置热更新
// 立即生效，无需替换 channel（替换会带来运行中任务的释放错配/超发风险）。
//
// 语义：
//   - acquire 非阻塞：已达当前上限时立即返回 false，不排队。
//   - 缩容：active 数高于新上限时不再放行，直到运行中任务结束把 active
//     降到新上限以下；不会主动中断或超发。
//   - 扩容：新上限立即生效，后续 acquire 按新上限放行。
//   - release 幂等：active 已为 0 时重复释放无副作用（防重复释放）。
type nativeDelegationLimiter struct {
	mu       sync.Mutex
	active   int
	resolve  func() int
	fallback int
}

// newNativeDelegationLimiter 创建限流器。resolve 返回当前配置上限（<=0 视为
// 未配置）；fallback 是配置缺失时的兜底上限，必须 > 0。
func newNativeDelegationLimiter(resolve func() int, fallback int) *nativeDelegationLimiter {
	if fallback <= 0 {
		fallback = defaultNativeDelegationConcurrencyLimit
	}
	return &nativeDelegationLimiter{resolve: resolve, fallback: fallback}
}

// limit 返回当前生效上限：优先取 resolve 的正值，否则取兜底值。
func (limiter *nativeDelegationLimiter) limit() int {
	if limiter == nil {
		return 0
	}
	if limiter.resolve != nil {
		if limit := limiter.resolve(); limit > 0 {
			return limit
		}
	}
	return limiter.fallback
}

// tryAcquire 非阻塞尝试占用一个并发槽位。
func (limiter *nativeDelegationLimiter) tryAcquire() bool {
	if limiter == nil {
		return false
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.active >= limiter.limit() {
		return false
	}
	limiter.active++
	return true
}

// release 释放一个并发槽位。重复释放安全：active 已为 0 时直接忽略。
func (limiter *nativeDelegationLimiter) release() {
	if limiter == nil {
		return
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.active > 0 {
		limiter.active--
	}
}

// activeCount 返回当前占用槽位数（测试与诊断用）。
func (limiter *nativeDelegationLimiter) activeCount() int {
	if limiter == nil {
		return 0
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	return limiter.active
}
