package modeladapter

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"cursor/internal/safego"
)

const (
	// defaultProviderStreamIdleTimeout 是流式请求无有效内容时的默认静默超时。
	// 取 90s：上游半开连接/静默卡死时，用户体感从 4 分钟「卡死」缩短为 90 秒后明确报错。
	// 用户可在配置中调大（最高无上限）或调小（最低 minProviderStreamIdleTimeout）。
	defaultProviderStreamIdleTimeout = 90 * time.Second
	minProviderStreamIdleTimeout     = 30 * time.Second

	// chunkTimeout 是 SSE 单块（单次读）之间的最大间隔；超过即视为流卡死。
	// 这是父代理默认（90s 空闲超时）对应的逐块阈值；委派/子代理流由
	// providerStreamChunkTimeout 按请求空闲超时放宽，避免长 thinking 静默被误杀。
	chunkTimeout = 30 * time.Second
)

type providerStreamIdleWatchdog struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	timeout time.Duration
	timer   *time.Timer
	// cancelDone 在监听 parent 取消的 goroutine 退出时关闭，供 Stop 等待，避免 goroutine 泄漏。
	cancelDone chan struct{}

	mu       sync.Mutex
	body     io.Closer
	stopped  bool
	timedOut bool
	// canceledByParent 标记因 parent ctx 取消而主动关闭 body（用户中止请求），
	// 与 timedOut 区分：前者不应报「静默超时」错误，只是及时释放连接。
	canceledByParent bool
	err              error
}

func newProviderStreamIdleWatchdog(parent context.Context, timeout time.Duration) (context.Context, *providerStreamIdleWatchdog) {
	if parent == nil {
		parent = context.Background()
	}
	timeout = normalizeProviderStreamIdleTimeoutDuration(timeout)
	ctx, cancel := context.WithCancelCause(parent)
	watchdog := &providerStreamIdleWatchdog{
		ctx:        ctx,
		cancel:     cancel,
		timeout:    timeout,
		err:        providerStreamIdleTimeoutError(timeout),
		cancelDone: make(chan struct{}),
	}
	watchdog.timer = time.AfterFunc(watchdog.timeout, watchdog.expire)
	// 监听 parent 取消：用户中止请求时立即关闭响应体，让阻塞中的 scanner.Scan() 返回，
	// 而不必等到 30s 的 chunkTimeout 才释放上游连接与连接池槽位。
	// 监听 parent（而非 watchdog.ctx）是为了避免正常 Stop() 的 cancel(nil) 误触发本路径。
	safego.Go("model:stream-parent-cancel", func() {
		watchdog.watchParentCancel(parent)
	})
	return ctx, watchdog
}

// watchParentCancel 在 parent ctx 取消时关闭已 Attach 的 body。
// 用 mu 与 stopped 协调，保证 body 只被关闭一次；与 expire 路径互斥。
func (watchdog *providerStreamIdleWatchdog) watchParentCancel(parent context.Context) {
	defer close(watchdog.cancelDone)
	select {
	case <-parent.Done():
	case <-watchdog.ctx.Done():
		// watchdog 自身 ctx 已结束（Stop 或 expire），无需再监听。
		return
	}
	watchdog.mu.Lock()
	if watchdog.stopped || watchdog.timedOut {
		watchdog.mu.Unlock()
		return
	}
	// 标记并由本路径关闭 body；不设置 timedOut，避免误报静默超时错误。
	watchdog.canceledByParent = true
	body := watchdog.body
	watchdog.mu.Unlock()
	if body != nil {
		_ = body.Close()
	}
}

func (watchdog *providerStreamIdleWatchdog) AttachBody(body io.Closer) {
	if watchdog == nil || body == nil {
		return
	}
	watchdog.mu.Lock()
	watchdog.body = body
	shouldClose := watchdog.timedOut || watchdog.stopped
	watchdog.mu.Unlock()
	if shouldClose {
		_ = body.Close()
	}
}

func (watchdog *providerStreamIdleWatchdog) MarkEffectiveContent() {
	if watchdog == nil {
		return
	}
	watchdog.mu.Lock()
	defer watchdog.mu.Unlock()
	if watchdog.stopped || watchdog.timedOut || watchdog.timer == nil {
		return
	}
	watchdog.timer.Reset(watchdog.timeout)
}

func (watchdog *providerStreamIdleWatchdog) Stop() {
	if watchdog == nil {
		return
	}
	watchdog.mu.Lock()
	if watchdog.stopped {
		watchdog.mu.Unlock()
		return
	}
	watchdog.stopped = true
	watchdog.body = nil
	if watchdog.timer != nil {
		watchdog.timer.Stop()
	}
	watchdog.mu.Unlock()
	watchdog.cancel(nil)
	// 等待监听 parent 取消的 goroutine 退出，避免泄漏。
	// cancel(nil) 让 watchdog.ctx.Done() 触发该 goroutine 的第二个 select case 而返回。
	if watchdog.cancelDone != nil {
		<-watchdog.cancelDone
	}
}

func (watchdog *providerStreamIdleWatchdog) Err() error {
	if watchdog == nil {
		return nil
	}
	watchdog.mu.Lock()
	defer watchdog.mu.Unlock()
	if watchdog.timedOut {
		return watchdog.err
	}
	return nil
}

func (watchdog *providerStreamIdleWatchdog) expire() {
	watchdog.mu.Lock()
	if watchdog.stopped || watchdog.timedOut {
		watchdog.mu.Unlock()
		return
	}
	watchdog.timedOut = true
	body := watchdog.body
	err := watchdog.err
	watchdog.mu.Unlock()

	watchdog.cancel(err)
	if body != nil {
		_ = body.Close()
	}
}

func normalizeProviderStreamIdleTimeoutDuration(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultProviderStreamIdleTimeout
	}
	if timeout < minProviderStreamIdleTimeout {
		return minProviderStreamIdleTimeout
	}
	return timeout
}

func providerStreamIdleTimeoutError(timeout time.Duration) error {
	seconds := int(timeout / time.Second)
	if seconds > 0 && timeout == time.Duration(seconds)*time.Second {
		return fmt.Errorf("provider stream idle timeout after %ds without effective content", seconds)
	}
	return fmt.Errorf("provider stream idle timeout after %s without effective content", timeout)
}

// providerStreamChunkTimeout 由请求级空闲超时派生 SSE 逐块读超时：
// 取空闲超时的 1/4，但不低于默认 30s 阈值。父代理默认 90s 空闲 → 30s 逐块
// （行为与历史一致）；委派/子代理流放宽空闲超时后，逐块间隙容忍同步放大。
func providerStreamChunkTimeout(idleTimeout time.Duration) time.Duration {
	derived := time.Duration(0)
	if idleTimeout > 0 {
		derived = idleTimeout / 4
	}
	if derived < chunkTimeout {
		return chunkTimeout
	}
	return derived
}

// resetStreamReadDeadline 在每次 SSE 块读取前设置读超时，块到达后清除。
// 客户端 *http.Response 没有服务端 ResponseController 的读 deadline 能力，
// 因此改为定时看护：timeout 内无块到达即关闭响应体，使阻塞中的读返回
// 错误。超时触发会先原子记录标记再关闭 body；返回的 disarm 在每次 Scan 返回后
// 调用，报告本次是否发生过读超时。调用方据此把扫描错误转换为可被
// IsStreamConnectionReset 识别的读超时错误（net.Error），从而触发 pre-output
// 流式重连。
// 响应体不可用时静默忽略（fallback，不改变原有行为）。
func resetStreamReadDeadline(resp *http.Response, timeout time.Duration) (disarm func() bool, ok bool) {
	if resp == nil || resp.Body == nil {
		return func() bool { return false }, false
	}
	if timeout <= 0 {
		timeout = chunkTimeout
	}
	var timedOut atomic.Bool
	timer := time.AfterFunc(timeout, func() {
		timedOut.Store(true)
		_ = resp.Body.Close()
	})
	return func() bool {
		stopped := timer.Stop()
		// !stopped 覆盖回调已触发或正在触发、但原子标记尚未可见的竞态窗口。
		return timedOut.Load() || !stopped
	}, true
}

// streamChunkTimeoutError 构造可被 IsStreamConnectionReset 识别的逐块读超时错误。
// *net.OpError 实现 net.Error 接口，errors.As 在 IsStreamConnectionReset 中命中，
// 使逐块超时与连接重置一样触发 pre-output 流式重连。
func streamChunkTimeoutError() error {
	return &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}
}
