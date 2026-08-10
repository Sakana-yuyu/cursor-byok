//go:build windows

package delegation

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsCreateNoWindow = 0x08000000

type windowsProcessTree struct {
	mu              sync.Mutex
	job             windows.Handle
	attached        bool
	cancelRequested bool
}

func prepareProcessTree(command *exec.Cmd) (processTreeController, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.HideWindow = true
	command.SysProcAttr.CreationFlags |= windowsCreateNoWindow
	tree := &windowsProcessTree{job: job}
	command.Cancel = tree.Kill
	return tree, nil
}

func (tree *windowsProcessTree) Attach(process *os.Process) error {
	if tree == nil || process == nil {
		return errors.New("process tree or process is nil")
	}
	tree.mu.Lock()
	defer tree.mu.Unlock()
	if tree.job == 0 {
		return os.ErrProcessDone
	}
	var attachErr error
	if err := process.WithHandle(func(handle uintptr) {
		attachErr = windows.AssignProcessToJobObject(tree.job, windows.Handle(handle))
	}); err != nil {
		return err
	}
	if attachErr != nil {
		return attachErr
	}
	tree.attached = true
	if tree.cancelRequested {
		return windows.TerminateJobObject(tree.job, 1)
	}
	return nil
}

func (tree *windowsProcessTree) Kill() error {
	if tree == nil {
		return nil
	}
	tree.mu.Lock()
	defer tree.mu.Unlock()
	tree.cancelRequested = true
	if tree.job == 0 {
		return os.ErrProcessDone
	}
	if !tree.attached {
		return nil
	}
	return windows.TerminateJobObject(tree.job, 1)
}

func (tree *windowsProcessTree) Close() error {
	if tree == nil {
		return nil
	}
	tree.mu.Lock()
	defer tree.mu.Unlock()
	if tree.job == 0 {
		return nil
	}
	job := tree.job
	tree.job = 0
	return windows.CloseHandle(job)
}
