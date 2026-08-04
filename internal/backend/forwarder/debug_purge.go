package forwarder

import (
	"sync"
	"sync/atomic"
)

// debugPurge 是进程内唯一的 debug 落盘/清理协调器。
//
// debug 事件是异步落盘的（writeLoop 在写前 MkdirAll），若清理与落盘并发，
// 清理前已入队的旧事件会在目录被删除后立刻把它重建回来，用户看到的就是
// 「清理没生效」。协调方式是世代号（epoch）+ 读写锁：
//   - 落盘 worker 写文件期间持读锁，并携带入队时的世代号；
//   - 清理期间持写锁并递增世代号，此时没有任何写盘在进行；
//   - 清理结束后，世代号落后的事件被 worker 丢弃。
//
// recorder 与清理入口（bridge 的历史管理）在同一进程内，所以包级单例足够。
var debugPurge = &debugPurgeGate{}

type debugPurgeGate struct {
	mu    sync.RWMutex
	epoch atomic.Uint64
}

// currentEpoch 返回当前世代号，用于给入队事件打标记。
// 无锁读，不让主链路的 enqueue 被清理阻塞。
func (gate *debugPurgeGate) currentEpoch() uint64 {
	return gate.epoch.Load()
}

// beginWrite 申请写盘许可。返回 false 表示该事件属于已被清理的世代，应当丢弃；
// 返回 true 时调用方必须配对调用 endWrite。
func (gate *debugPurgeGate) beginWrite(epoch uint64) bool {
	gate.mu.RLock()
	if gate.epoch.Load() != epoch {
		gate.mu.RUnlock()
		return false
	}
	return true
}

// endWrite 释放写盘许可。
func (gate *debugPurgeGate) endWrite() {
	gate.mu.RUnlock()
}

// purge 在「无写盘进行中」的窗口内执行 remove，并让先前入队的事件失效。
func (gate *debugPurgeGate) purge(remove func() error) error {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.epoch.Add(1)
	if remove == nil {
		return nil
	}
	return remove()
}

// PurgeDebugLogs 在暂停 debug 日志落盘的窗口内执行 remove，保证刚删掉的目录
// 不会被清理前已入队的旧事件重建。remove 只应做文件删除，不要在其中执行
// 网络请求或等待 UI，否则会拖住落盘 worker。
func PurgeDebugLogs(remove func() error) error {
	return debugPurge.purge(remove)
}
