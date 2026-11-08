// key_lane.go 每密钥并发闸门（借鉴 magpie 的 lanes 设计）：对同一渠道凭据
// （指纹含 APIKey，即精确到密钥）限制同时在途的模型流数量，超出上限后进入
// FIFO 队列等待。关键语义：
//   - 排队等待不是失败：不触发渠道冷却、不消耗重试退避预算；
//   - 等待超时返回 ErrKeyLaneBusy，路由层据此跳过该候选换下一个；
//   - 等待期间可被 ctx 取消（返回 ctx.Err()）；
//   - 上限为 0 表示不限制（完全不参与闸门，零开销）。
//
// 并发正确性说明：busy/queue 的所有读写都在 mu 临界区内完成；唤醒采用
// 「名额直接移交」协议——release 在临界区内把名额记到队首条目头上再发信号，
// 被唤醒者无需二次竞争，天然避免惊群与插队。超时/取消与被唤醒的竞争窗口
// 通过 removeQueued 的返回值裁决：发现自己已被移交名额则代为归还。
package modeladapter

import (
	"context"
	"errors"
	"sync"
	"time"

	legacyruntime "cursor/internal/runtime"
)

// ErrKeyLaneBusy 表示渠道并发闸门排队超时，路由层应跳过该候选且不计失败、
// 不冷却。ctx 取消不走该哨兵，直接返回 ctx.Err()。
var ErrKeyLaneBusy = errors.New("modeladapter: key lane queue timeout")

// keyLaneQueueWait 是并发闸门排队的最长等待。取值权衡：太短会让饱和密钥
// 频繁被跳过（失去排队意义），太长会拖高首个 token 的时延；30s 与 router
// 的静默重试阈值同量级，且期间上游可能在处理其他流并腾出名额。
var keyLaneQueueWait = 30 * time.Second

// keyLane 是单个密钥维度的并发闸门。
type keyLane struct {
	// limit 是在途流上限；<=0 表示不限制。
	limit int
	// busy 是当前持有的名额数（含已移交给队列条目、尚未被取走的）。
	busy int
	// queue 是等待名额的 FIFO 队列，每条目一个容量 1 的信号 channel。
	queue []chan struct{}
}

// keyLanes 维护全部密钥闸门。零值可用。
type keyLanes struct {
	mu    sync.Mutex
	lanes map[string]*keyLane
}

// acquire 获取 key 的一个并发名额。返回的 release 必须恰好调用一次；
// 返回错误时不占用名额、无需释放。
//   - limit<=0：不限制，release 为空操作；
//   - 名额紧张：进入 FIFO 队列，最长等待 wait；
//   - 超时：返回 ErrKeyLaneBusy；等待期间 ctx 结束：返回 ctx.Err()。
func (l *keyLanes) acquire(ctx context.Context, key string, limit int, wait time.Duration) (func(), error) {
	if l == nil || key == "" || limit <= 0 {
		return func() {}, nil
	}
	l.mu.Lock()
	if l.lanes == nil {
		l.lanes = make(map[string]*keyLane)
	}
	lane := l.lanes[key]
	if lane == nil {
		lane = &keyLane{}
		l.lanes[key] = lane
	}
	// 配置热更新后以最新上限为准（同指纹不同 limit 的渠道共存时取后到者）。
	lane.limit = limit
	if lane.busy < lane.limit {
		lane.busy++
		l.mu.Unlock()
		return func() { l.release(key) }, nil
	}
	// 已达上限：进入 FIFO 队列。等待期间不占用名额、不触发任何冷却。
	notified := make(chan struct{}, 1)
	lane.queue = append(lane.queue, notified)
	l.mu.Unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-notified:
		// release 方已在临界区内完成 busy++（名额移交），这里直接持有。
		return func() { l.release(key) }, nil
	case <-timer.C:
		if !l.removeQueued(key, notified) {
			// 与 release 的移交竞争失败：名额已被记到本条目头上，
			// 消耗信号并代为归还，仍按「忙」返回。
			drainNotified(notified)
			l.release(key)
		}
		return nil, ErrKeyLaneBusy
	case <-ctx.Done():
		if !l.removeQueued(key, notified) {
			drainNotified(notified)
			l.release(key)
		}
		return nil, ctx.Err()
	}
}

// drainNotified 非阻塞地取走可能存在的移交信号。
func drainNotified(notified chan struct{}) {
	select {
	case <-notified:
	default:
	}
}

// removeQueued 把尚未被移交的队列条目摘除；返回 false 表示条目已被 release
// 移交名额（调用方需代为归还）。
func (l *keyLanes) removeQueued(key string, ch chan struct{}) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	lane := l.lanes[key]
	if lane == nil {
		return false
	}
	for i, queued := range lane.queue {
		if queued == ch {
			lane.queue = append(lane.queue[:i], lane.queue[i+1:]...)
			return true
		}
	}
	return false
}

// release 归还一个名额：优先把名额直接移交给队首等待者，无等待者且名额
// 归零时移除闸门（惰性回收，防止 map 随历史密钥配置无界增长）。
func (l *keyLanes) release(key string) {
	if l == nil || key == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	lane := l.lanes[key]
	if lane == nil {
		return
	}
	if lane.busy > 0 {
		lane.busy--
	}
	if len(lane.queue) > 0 {
		next := lane.queue[0]
		lane.queue = lane.queue[1:]
		// 名额直接移交队首：busy 立即回到占用态，被唤醒者取走即持有。
		lane.busy++
		next <- struct{}{}
		return
	}
	if lane.busy == 0 {
		delete(l.lanes, key)
	}
}

// acquireKeyLane 是 streamChannel 的闸门入口：按渠道指纹（含 APIKey）获取
// 并发名额。MaxConcurrencyPerKey<=0 的渠道完全不参与闸门。
func (router *Router) acquireKeyLane(ctx context.Context, channel *legacyruntime.ResolvedChannel) (func(), error) {
	if router == nil || router.keyLanes == nil || channel == nil || channel.MaxConcurrencyPerKey <= 0 {
		return func() {}, nil
	}
	return router.keyLanes.acquire(ctx, channelHealthMapKey(channel), channel.MaxConcurrencyPerKey, keyLaneQueueWait)
}
