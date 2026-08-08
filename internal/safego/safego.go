// Package safego 提供带 panic 兜底的后台 goroutine 启动封装。
//
// Go 语言中 goroutine 内未捕获的 panic 会导致整个进程崩溃。本项目大量使用
// 后台 goroutine（转发、心跳、维护、异步落盘等），任一 panic 都会拖垮整个
// Cursor 助手进程。本包统一捕获 panic 并记录堆栈，保证后台任务异常只降级、
// 不崩溃。
package safego

import (
	"runtime/debug"

	"cursor/internal/logger"
)

// Go 启动一个带 panic 兜底的 goroutine。name 用于日志定位（建议使用
// “模块:职责”格式）。fn 内的 panic 会被捕获并输出 error 级日志。
func Go(name string, fn func()) {
	if fn == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("safego %s panic recovered: %v\n%s", name, r, debug.Stack())
			}
		}()
		fn()
	}()
}
