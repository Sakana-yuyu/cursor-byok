//go:build windows

package forwarder

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

func processExists(pid int) bool {
	_, ok := processStartTime(pid)
	return ok
}

// processStartTime 返回目标进程的创建（启动）时间，以及该 PID 是否对应一个存活进程。
// 用于和会话锁里记录的 created_at 交叉验证，防止 Windows 上的 PID 复用导致
// 孤儿锁被误判为「仍被持有」。
func processStartTime(pid int) (time.Time, bool) {
	if pid <= 0 {
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
