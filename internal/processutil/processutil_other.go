//go:build !windows

package processutil

import "os/exec"

// HideWindow is a no-op on platforms without Windows console windows.
func HideWindow(_ *exec.Cmd) {}
