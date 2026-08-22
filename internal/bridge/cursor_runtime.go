package bridge

import (
	"context"

	"cursor/internal/cursoraccount"
)

type cursorProcessRuntime struct {
	windows *WindowService
}

func newCursorProcessRuntime(windows *WindowService) cursoraccount.CursorRuntime {
	return cursorProcessRuntime{windows: windows}
}

func (runtime cursorProcessRuntime) Running() bool {
	return runtime.windows != nil && runtime.windows.IsCursorRunning()
}

func (runtime cursorProcessRuntime) Stop(context.Context) error {
	return killCursorProcesses()
}

func (runtime cursorProcessRuntime) Start(context.Context) error {
	return launchCursorProcess("", "")
}
