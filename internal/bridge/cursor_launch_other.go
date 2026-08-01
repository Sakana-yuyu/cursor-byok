//go:build !windows

package bridge

import "os/exec"

func configureCursorCommand(_ *exec.Cmd) {}
