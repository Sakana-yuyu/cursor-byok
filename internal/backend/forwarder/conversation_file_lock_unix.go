//go:build !windows

package forwarder

import (
	"os"
	"syscall"
)

func tryLockConversationFileExclusive(file *os.File) (bool, error) {
	if file == nil {
		return false, nil
	}
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == syscall.EWOULDBLOCK {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func lockConversationFileExclusiveBlocking(file *os.File) error {
	if file == nil {
		return syscall.EINVAL
	}
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
}

func releaseConversationFileExclusive(file *os.File) error {
	if file == nil {
		return nil
	}
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
