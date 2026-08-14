//go:build windows

package forwarder

import (
	"os"

	"golang.org/x/sys/windows"
)

const (
	lockFileFailImmediately = 0x00000001
	lockFileExclusiveLock   = 0x00000002
)

func tryLockConversationFileExclusive(file *os.File) (bool, error) {
	if file == nil {
		return false, nil
	}
	var overlapped windows.Overlapped
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		lockFileExclusiveLock|lockFileFailImmediately,
		0,
		1,
		0,
		&overlapped,
	)
	if err == windows.ERROR_LOCK_VIOLATION {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func lockConversationFileExclusiveBlocking(file *os.File) error {
	if file == nil {
		return windows.ERROR_INVALID_HANDLE
	}
	var overlapped windows.Overlapped
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	overlapped.HEvent = event

	handle := windows.Handle(file.Fd())
	err = windows.LockFileEx(handle, lockFileExclusiveLock, 0, 1, 0, &overlapped)
	if err != nil && err != windows.ERROR_IO_PENDING {
		return err
	}
	_, err = windows.WaitForSingleObject(event, windows.INFINITE)
	if err != nil {
		return err
	}
	var transferred uint32
	return windows.GetOverlappedResult(handle, &overlapped, &transferred, false)
}

func releaseConversationFileExclusive(file *os.File) error {
	if file == nil {
		return nil
	}
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}
