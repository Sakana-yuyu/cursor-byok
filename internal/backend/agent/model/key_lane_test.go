package modeladapter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeyLanesUnlimitedDoesNotTrackState(t *testing.T) {
	lanes := &keyLanes{}
	release, err := lanes.acquire(context.Background(), "key-a", 0, time.Second)
	if err != nil {
		t.Fatalf("unlimited acquire failed: %v", err)
	}
	release()
	lanes.mu.Lock()
	defer lanes.mu.Unlock()
	if len(lanes.lanes) != 0 {
		t.Fatalf("unlimited acquire created lane state: %d entries", len(lanes.lanes))
	}
}

func TestKeyLanesLimitConcurrentHolders(t *testing.T) {
	lanes := &keyLanes{}
	const limit = 2
	const workers = 8
	var active atomic.Int32
	var peak atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := lanes.acquire(context.Background(), "key-a", limit, 2*time.Second)
			if err != nil {
				t.Errorf("acquire failed: %v", err)
				return
			}
			current := active.Add(1)
			for {
				old := peak.Load()
				if current <= old || peak.CompareAndSwap(old, current) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			active.Add(-1)
			release()
		}()
	}
	wg.Wait()
	if peak.Load() > limit {
		t.Fatalf("peak concurrency %d exceeded limit %d", peak.Load(), limit)
	}
	lanes.mu.Lock()
	defer lanes.mu.Unlock()
	if len(lanes.lanes) != 0 {
		t.Fatalf("lanes map leaked entries after full release: %d", len(lanes.lanes))
	}
}

func TestKeyLanesQueueTimeoutReturnsBusy(t *testing.T) {
	lanes := &keyLanes{}
	release, err := lanes.acquire(context.Background(), "key-a", 1, time.Second)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer release()
	start := time.Now()
	_, err = lanes.acquire(context.Background(), "key-a", 1, 50*time.Millisecond)
	if !errors.Is(err, ErrKeyLaneBusy) {
		t.Fatalf("queue timeout error = %v, want ErrKeyLaneBusy", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("queue timeout took %s, want ~50ms", elapsed)
	}
}

func TestKeyLanesQueuedWaiterProceedsAfterRelease(t *testing.T) {
	lanes := &keyLanes{}
	first, err := lanes.acquire(context.Background(), "key-a", 1, time.Second)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	waiterDone := make(chan struct{})
	go func() {
		defer close(waiterDone)
		second, err := lanes.acquire(context.Background(), "key-a", 1, 2*time.Second)
		if err != nil {
			t.Errorf("queued acquire failed: %v", err)
			return
		}
		second()
	}()
	time.Sleep(30 * time.Millisecond)
	first()
	select {
	case <-waiterDone:
	case <-time.After(time.Second):
		t.Fatal("queued waiter was not woken after release")
	}
	lanes.mu.Lock()
	defer lanes.mu.Unlock()
	if len(lanes.lanes) != 0 {
		t.Fatalf("lanes map leaked entries after full release: %d", len(lanes.lanes))
	}
}

func TestKeyLanesWakeUpInFIFOOrder(t *testing.T) {
	lanes := &keyLanes{}
	first, err := lanes.acquire(context.Background(), "key-a", 1, time.Second)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	order := make(chan int, 2)
	go func() {
		release, err := lanes.acquire(context.Background(), "key-a", 1, 2*time.Second)
		if err != nil {
			t.Errorf("queued acquire failed: %v", err)
			order <- -1
			return
		}
		order <- 2
		release()
	}()
	go func() {
		time.Sleep(10 * time.Millisecond) // 确保第二个等待者后入队
		release, err := lanes.acquire(context.Background(), "key-a", 1, 2*time.Second)
		if err != nil {
			t.Errorf("queued acquire failed: %v", err)
			order <- -1
			return
		}
		order <- 3
		release()
	}()
	time.Sleep(60 * time.Millisecond)
	first()
	if got := <-order; got != 2 {
		t.Fatalf("first woken waiter = %d, want 2 (FIFO)", got)
	}
	if got := <-order; got != 3 {
		t.Fatalf("second woken waiter = %d, want 3 (FIFO)", got)
	}
}

func TestKeyLanesContextCancelWhileQueued(t *testing.T) {
	lanes := &keyLanes{}
	holder, err := lanes.acquire(context.Background(), "key-a", 1, time.Second)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer holder()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := lanes.acquire(ctx, "key-a", 1, 5*time.Second)
		errCh <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled acquire error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled acquire did not return")
	}
}

func TestKeyLanesDistinctKeysIndependent(t *testing.T) {
	lanes := &keyLanes{}
	first, err := lanes.acquire(context.Background(), "key-a", 1, time.Second)
	if err != nil {
		t.Fatalf("key-a acquire failed: %v", err)
	}
	defer first()
	second, err := lanes.acquire(context.Background(), "key-b", 1, time.Millisecond)
	if err != nil {
		t.Fatalf("key-b blocked by key-a saturation: %v", err)
	}
	second()
}

func TestKeyLanesRaisedLimitGrantsQueuedBeforeNewcomer(t *testing.T) {
	first := make(chan struct{}, 1)
	second := make(chan struct{}, 1)
	lanes := &keyLanes{lanes: map[string]*keyLane{
		"key-a": {limit: 1, busy: 1, queue: []chan struct{}{first, second}},
	}}
	// All additional capacity belongs to existing waiters, not this newcomer.
	release, err := lanes.acquire(context.Background(), "key-a", 3, time.Millisecond)
	if release != nil {
		release()
	}
	if !errors.Is(err, ErrKeyLaneBusy) {
		t.Fatalf("newcomer bypassed queued waiters: %v", err)
	}
	for i, notified := range []chan struct{}{first, second} {
		select {
		case <-notified:
		default:
			t.Fatalf("waiter %d did not receive raised capacity", i)
		}
	}
	for i := 0; i < 3; i++ {
		lanes.release("key-a")
	}
	if len(lanes.lanes) != 0 {
		t.Fatal("lane leaked after all holders released")
	}
}

func TestKeyLanesLoweredLimitWaitsForExistingHolders(t *testing.T) {
	notified := make(chan struct{}, 1)
	lanes := &keyLanes{lanes: map[string]*keyLane{
		"key-a": {limit: 3, busy: 3, queue: []chan struct{}{notified}},
	}}
	_, err := lanes.acquire(context.Background(), "key-a", 1, time.Millisecond)
	if !errors.Is(err, ErrKeyLaneBusy) {
		t.Fatalf("newcomer exceeded lowered limit: %v", err)
	}
	for i := 0; i < 2; i++ {
		lanes.release("key-a")
		select {
		case <-notified:
			t.Fatalf("waiter granted while %d existing holders still occupy the lane", 2-i)
		default:
		}
	}
	lanes.release("key-a")
	select {
	case <-notified:
	default:
		t.Fatal("waiter not granted after capacity became available")
	}
	lanes.release("key-a")
	if len(lanes.lanes) != 0 {
		t.Fatal("lane leaked after all holders released")
	}
}

func TestKeyLanesCancelledContextDoesNotTakeFreeSlot(t *testing.T) {
	lanes := &keyLanes{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, err := lanes.acquire(ctx, "key-a", 1, time.Second)
	if release != nil {
		release()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire = %v, want context.Canceled", err)
	}
	if len(lanes.lanes) != 0 {
		t.Fatal("cancelled request created lane state")
	}
}

func TestKeyLanesLimitHotReloadTakesNewestConfig(t *testing.T) {
	lanes := &keyLanes{}
	release, err := lanes.acquire(context.Background(), "key-a", 1, time.Second)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	// 旧持有者在途、配置热更新把上限提高到 2：新请求应立即通过而非排队。
	second, err := lanes.acquire(context.Background(), "key-a", 2, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("acquire after limit raise failed: %v", err)
	}
	second()
	release()
	lanes.mu.Lock()
	defer lanes.mu.Unlock()
	if len(lanes.lanes) != 0 {
		t.Fatalf("lanes map leaked entries after full release: %d", len(lanes.lanes))
	}
}
