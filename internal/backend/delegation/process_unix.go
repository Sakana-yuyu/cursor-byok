//go:build !windows

package delegation

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

type unixProcessTree struct {
	mu              sync.Mutex
	pid             int
	cancelRequested bool
}

func prepareProcessTree(command *exec.Cmd) (processTreeController, error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	tree := &unixProcessTree{}
	command.Cancel = tree.Kill
	return tree, nil
}

func (tree *unixProcessTree) Attach(process *os.Process) error {
	if tree == nil || process == nil {
		return errors.New("process tree or process is nil")
	}
	tree.mu.Lock()
	defer tree.mu.Unlock()
	tree.pid = process.Pid
	if tree.cancelRequested {
		return killUnixProcessGroup(tree.pid)
	}
	return nil
}

func (tree *unixProcessTree) Kill() error {
	if tree == nil {
		return nil
	}
	tree.mu.Lock()
	defer tree.mu.Unlock()
	tree.cancelRequested = true
	if tree.pid == 0 {
		return nil
	}
	return killUnixProcessGroup(tree.pid)
}

func (tree *unixProcessTree) Close() error {
	if tree == nil {
		return nil
	}
	tree.mu.Lock()
	defer tree.mu.Unlock()
	pid := tree.pid
	tree.pid = 0
	if pid == 0 {
		return nil
	}
	err := killUnixProcessGroup(pid)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func killUnixProcessGroup(pid int) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
