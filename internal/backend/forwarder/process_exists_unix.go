//go:build !windows

package forwarder

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func processExists(pid int) bool {
	_, ok := processStartTime(pid)
	return ok
}

// processStartTime 返回目标进程的启动时间，以及该 PID 是否对应一个存活进程。
// Linux 通过 /proc/<pid>/stat 的 starttime（字段 22，单位 jiffies）+ 启动后流逝时间换算；
// 其他 Unix 平台读不到精确启动时间，退化为仅判断存活（返回零值 + 存活标志）。
// 该启动时间用于和会话锁里的 created_at 交叉验证，防止 PID 复用导致孤儿锁误判。
func processStartTime(pid int) (time.Time, bool) {
	if pid <= 0 {
		return time.Time{}, false
	}
	process, err := os.FindProcess(pid)
	if err != nil || process == nil {
		return time.Time{}, false
	}
	// signal 0 不实际发信号，只做存在性检查；EPERM 表示存在但无权限。
	if err := process.Signal(syscall.Signal(0)); err != nil && !errors.Is(err, syscall.EPERM) {
		return time.Time{}, false
	}

	// 尝试从 /proc 读取精确启动时间（Linux）。
	startTime, ok := readProcStartTime(pid)
	if ok {
		return startTime, true
	}
	// 非 Linux：拿不到启动时间，仅返回存活状态。
	return time.Time{}, true
}

// readProcStartTime 从 /proc/<pid>/stat 解析进程启动时间。
// 字段 22 (starttime) 是自系统启动以来的 jiffies；配合 btime（系统启动时刻）
// 换算成绝对时间。解析失败返回 ok=false。
func readProcStartTime(pid int) (time.Time, bool) {
	statBody, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return time.Time{}, false
	}
	// comm 字段可能含空格和括号，因此从最后一个 ')' 之后切分字段。
	s := string(statBody)
	rightParen := strings.LastIndexByte(s, ')')
	if rightParen < 0 || rightParen+1 >= len(s) {
		return time.Time{}, false
	}
	fields := strings.Fields(s[rightParen+1:])
	// stat 文件去掉前两个字段 (pid, comm) 后，字段 22 (starttime) 在 fields 中下标为 20。
	const starttimeIndex = 20
	if len(fields) <= starttimeIndex {
		return time.Time{}, false
	}
	starttimeHZ, err := strconv.ParseFloat(fields[starttimeIndex], 64)
	if err != nil || starttimeHZ < 0 {
		return time.Time{}, false
	}
	hz := float64(getClockTicks())
	if hz <= 0 {
		return time.Time{}, false
	}
	bootTime, ok := readBootTime()
	if !ok {
		return time.Time{}, false
	}
	secondsSinceBoot := starttimeHZ / hz
	return bootTime.Add(time.Duration(secondsSinceBoot * float64(time.Second))), true
}

// readBootTime 从 /proc/stat 的 btime 行读取系统启动时刻。
func readBootTime() (time.Time, bool) {
	body, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "btime ") {
			secs, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "btime ")), 10, 64)
			if err != nil || secs <= 0 {
				return time.Time{}, false
			}
			return time.Unix(secs, 0), true
		}
	}
	return time.Time{}, false
}

// getClockTicks 返回每秒时钟中断数（USER_HZ），Linux 常见为 100。
func getClockTicks() int {
	// syscall.Sysconf 在部分 Go 版本可用；为保持兼容，直接用标准值。
	// USER_HZ 几乎所有现代 Linux 都是 100；若解析出的启动时间偏差，也只是影响交叉校验精度，
	// 最坏情况退化为「无法判定」而非「误判存活」。
	return 100
}
