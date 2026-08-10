//go:build !windows

package delegation

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func processAliveForTest(pid int) bool {
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
			fields := strings.Fields(string(data))
			if len(fields) > 2 && fields[2] == "Z" {
				return false
			}
		}
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func killProcessForTest(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}
