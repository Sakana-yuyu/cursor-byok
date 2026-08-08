package forwarder

import (
	"sync"
	"sync/atomic"
	"testing"
)

// fakeLimiterLimit 是测试用可变 limit provider。
type fakeLimiterLimit struct {
	value atomic.Int32
}

func (fake *fakeLimiterLimit) current() int {
	return int(fake.value.Load())
}

func (fake *fakeLimiterLimit) set(limit int) {
	fake.value.Store(int32(limit))
}

func TestNativeDelegationLimiterFixedLimit(t *testing.T) {
	limiter := newNativeDelegationLimiter(nil, 2)
	if limiter.limit() != 2 {
		t.Fatalf("fallback limit = %d, want 2", limiter.limit())
	}
	if !limiter.tryAcquire() || !limiter.tryAcquire() {
		t.Fatalf("first two acquires should succeed")
	}
	if limiter.tryAcquire() {
		t.Fatalf("third acquire must fail at limit 2")
	}
	if limiter.activeCount() != 2 {
		t.Fatalf("active = %d, want 2", limiter.activeCount())
	}
	limiter.release()
	if limiter.activeCount() != 1 {
		t.Fatalf("active after release = %d, want 1", limiter.activeCount())
	}
	if !limiter.tryAcquire() {
		t.Fatalf("acquire after release should succeed")
	}
}

func TestNativeDelegationLimiterExpand(t *testing.T) {
	fake := &fakeLimiterLimit{}
	fake.set(2)
	limiter := newNativeDelegationLimiter(fake.current, 4)

	if !limiter.tryAcquire() || !limiter.tryAcquire() {
		t.Fatalf("two acquires at limit 2 should succeed")
	}
	if limiter.tryAcquire() {
		t.Fatalf("third acquire at limit 2 must fail")
	}

	// 扩容立即生效，不重启服务。
	fake.set(5)
	for i := 0; i < 3; i++ {
		if !limiter.tryAcquire() {
			t.Fatalf("acquire %d after expand to 5 should succeed", i+3)
		}
	}
	if limiter.activeCount() != 5 {
		t.Fatalf("active after expand = %d, want 5", limiter.activeCount())
	}
	if limiter.tryAcquire() {
		t.Fatalf("acquire beyond expanded limit 5 must fail")
	}
}

func TestNativeDelegationLimiterShrink(t *testing.T) {
	fake := &fakeLimiterLimit{}
	fake.set(4)
	limiter := newNativeDelegationLimiter(fake.current, 4)

	for i := 0; i < 4; i++ {
		if !limiter.tryAcquire() {
			t.Fatalf("acquire %d at limit 4 should succeed", i+1)
		}
	}

	// 缩容：active(4) 高于新上限 2，不再放行，但不超发、不中断运行中任务。
	fake.set(2)
	if limiter.tryAcquire() {
		t.Fatalf("acquire while active(4) > shrunk limit(2) must fail")
	}

	// 运行中任务结束（release）后降到新上限内，恢复放行。
	limiter.release()
	limiter.release()
	limiter.release()
	if limiter.activeCount() != 1 {
		t.Fatalf("active after three releases = %d, want 1", limiter.activeCount())
	}
	if !limiter.tryAcquire() {
		t.Fatalf("acquire after active dropped below shrunk limit should succeed")
	}
	if limiter.activeCount() != 2 {
		t.Fatalf("active = %d, want 2", limiter.activeCount())
	}
}

func TestNativeDelegationLimiterRunningTasksKeepSlots(t *testing.T) {
	fake := &fakeLimiterLimit{}
	fake.set(2)
	limiter := newNativeDelegationLimiter(fake.current, 2)

	if !limiter.tryAcquire() || !limiter.tryAcquire() {
		t.Fatalf("two acquires should succeed")
	}
	// 模拟两个运行中任务各自持有一个槽位：任意配置变化都不能释放它们的槽位。
	fake.set(1)
	if limiter.tryAcquire() {
		t.Fatalf("no slot available while running tasks hold limit")
	}
	if limiter.activeCount() != 2 {
		t.Fatalf("running tasks must keep their slots, active = %d", limiter.activeCount())
	}
	// 任务结束释放各自槽位。
	limiter.release()
	limiter.release()
	if limiter.activeCount() != 0 {
		t.Fatalf("active after both releases = %d, want 0", limiter.activeCount())
	}
	if !limiter.tryAcquire() {
		t.Fatalf("acquire after all slots released should succeed")
	}
}

func TestNativeDelegationLimiterDoubleRelease(t *testing.T) {
	limiter := newNativeDelegationLimiter(nil, 2)
	if !limiter.tryAcquire() {
		t.Fatalf("acquire should succeed")
	}
	limiter.release()
	limiter.release()
	limiter.release()
	if limiter.activeCount() != 0 {
		t.Fatalf("active after double release = %d, want 0", limiter.activeCount())
	}
	// 重复释放不能吞掉后续正常 acquire 的槽位。
	if !limiter.tryAcquire() {
		t.Fatalf("acquire after double release should still succeed")
	}
	if limiter.activeCount() != 1 {
		t.Fatalf("active = %d, want 1", limiter.activeCount())
	}
}

func TestNativeDelegationLimiterResolveFallback(t *testing.T) {
	// resolve 返回非正值时回退兜底上限。
	limiter := newNativeDelegationLimiter(func() int { return 0 }, 3)
	if limiter.limit() != 3 {
		t.Fatalf("limit = %d, want fallback 3", limiter.limit())
	}
	// resolve 返回负值同样回退。
	limiter = newNativeDelegationLimiter(func() int { return -1 }, 3)
	if limiter.limit() != 3 {
		t.Fatalf("limit = %d, want fallback 3", limiter.limit())
	}
}

func TestNativeDelegationLimiterConcurrent(t *testing.T) {
	fake := &fakeLimiterLimit{}
	fake.set(4)
	limiter := newNativeDelegationLimiter(fake.current, 4)

	const workers = 32
	const attempts = 100
	var acquired atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < attempts; j++ {
				if limiter.tryAcquire() {
					acquired.Add(1)
					limiter.release()
				}
			}
		}()
	}
	wg.Wait()
	if limiter.activeCount() != 0 {
		t.Fatalf("active after concurrent churn = %d, want 0", limiter.activeCount())
	}
	// 并发场景下不能出现超发：任意时刻 active <= limit 由互斥锁保证，
	// 结束时槽位必须全部归还。
	if limiter.activeCount() != 0 {
		t.Fatalf("leaked slots: active = %d", limiter.activeCount())
	}
}
