//go:build windows

package forwarder

import (
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func processExists(pid int) bool {
	_, ok := processStartTime(pid)
	return ok
}

// processTableContainsPID 判断 pid 是否出现在系统进程表中。
//
// 与 OpenProcess 不同，进程表枚举（CreateToolhelp32Snapshot）只包含真实存活的进程。
// Windows 上已退出的进程其 EPROCESS 对象仍可能残留在对象管理器里（例如有驱动或
// 句柄还引用着它），此时 OpenProcess 仍能成功、GetProcessTimes 也能返回旧的启动时间，
// 但该 PID 不会出现在进程表枚举中——这正是「死进程持有的会话锁被误判为仍被持有」
// 的根因：锁逻辑靠 OpenProcess 判定持有者存活，于是每 30 秒的获取尝试都超时，
// 只能等 30 分钟的 mtime 兜底才自动回收。
//
// 进程表枚举快照失败时保守返回 true（不把进程判死），等待 mtime 兜底。
func processTableContainsPID(pid int) bool {
	if pid <= 0 {
		return false
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return true
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err := windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if entry.ProcessID == uint32(pid) {
			return true
		}
	}
	return false
}

// processStartTime 返回目标进程的创建（启动）时间，以及该 PID 是否对应一个存活进程。
// 用于和会话锁里记录的 created_at 交叉验证，防止 Windows 上的 PID 复用导致
// 孤儿锁被误判为「仍被持有」。
func processStartTime(pid int) (time.Time, bool) {
	if pid <= 0 {
		return time.Time{}, false
	}
	// 先查进程表：能通过 OpenProcess 打开但不在进程表中的 PID 是已退出的
	// 「幽灵进程」（EPROCESS 残留），直接判定为死亡。会话锁的持有者总是本程序
	// （cursor-byok），不会是 PPL 保护进程，因此「进程表未命中 = 已死」在这里安全。
	if !processTableContainsPID(pid) {
		return time.Time{}, false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// ERROR_ACCESS_DENIED 表示进程存在但权限不足，仍视为存活，只是拿不到启动时间。
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return time.Time{}, true
		}
		return time.Time{}, false
	}
	defer windows.CloseHandle(handle)

	var creation, exitTime, kernelTime, userTime windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exitTime, &kernelTime, &userTime); err != nil {
		// 拿不到启动时间，但句柄能打开，说明进程存在。
		return time.Time{}, true
	}
	// windows.Filetime.Nanoseconds() 返回自 1601-01-01 起的纳秒，
	// time.Unix(0, ns) 会正确解释为绝对时间。
	nanoseconds := creation.Nanoseconds()
	if nanoseconds <= 0 {
		return time.Time{}, true
	}
	return time.Unix(0, nanoseconds).UTC(), true
}
