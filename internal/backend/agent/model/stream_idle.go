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
)

const (
	// defaultProviderStreamIdleTimeout 是流式请求无有效内容时的默认静默超时。
	// 取 90s：上游半开连接/静默卡死时，用户体感从 4 分钟「卡死」缩短为 90 秒后明确报错。
	// 用户可在配置中调大（最高无上限）或调小（最低 minProviderStreamIdleTimeout）。
	defaultProviderStreamIdleTimeout = 90 * time.Second
	minProviderStreamIdleTimeout     = 30 * time.Second

	// chunkTimeout 是 SSE 单块（单次读）之间的最大间隔；超过即视为流卡死。
	chunkTimeout = 30 * time.Second
)

type providerStreamIdleWatchdog struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	timeout time.Duration
	timer   *time.Timer

	mu       sync.Mutex
	body     io.Closer
	stopped  bool
	timedOut bool
	err      error
}

func newProviderStreamIdleWatchdog(parent context.Context, timeout time.Duration) (context.Context, *providerStreamIdleWatchdog) {
	if parent == nil {
		parent = context.Background()
	}
	timeout = normalizeProviderStreamIdleTimeoutDuration(timeout)
	ctx, cancel := context.WithCancelCause(parent)
	watchdog := &providerStreamIdleWatchdog{
		ctx:     ctx,
		cancel:  cancel,
		timeout: timeout,
		err:     providerStreamIdleTimeoutError(timeout),
	}
	watchdog.timer = time.AfterFunc(watchdog.timeout, watchdog.expire)
	return ctx, watchdog
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

// resetStreamReadDeadline 在每次 SSE 块读取前设置读超时，块到达后清除。
// 客户端 *http.Response 没有服务端 ResponseController 的读 deadline 能力，
// 因此改为定时看护：chunkTimeout 内无块到达即关闭响应体，使阻塞中的读返回
// 错误。超时触发会先原子记录标记再关闭 body；返回的 disarm 在每次 Scan 返回后
// 调用，报告本次是否发生过读超时。调用方据此把扫描错误转换为可被
// IsStreamConnectionReset 识别的读超时错误（net.Error），从而触发 pre-output
// 流式重连。
// 响应体不可用时静默忽略（fallback，不改变原有行为）。
func resetStreamReadDeadline(resp *http.Response) (disarm func() bool, ok bool) {
	if resp == nil || resp.Body == nil {
		return func() bool { return false }, false
	}
	var timedOut atomic.Bool
	timer := time.AfterFunc(chunkTimeout, func() {
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
