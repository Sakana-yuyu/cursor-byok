package client

import (
	"errors"

	serverconfig "cursor/internal/backend/server/config"
	legacyruntime "cursor/internal/runtime"
)

var errCursorAccountModelOperationUnavailable = errors.New("Cursor 账户模型执行通道尚未完成真实协议验证，不能使用第三方模型测试、探测或测速")

func isCursorAccountModelAdapter(adapter serverconfig.ModelAdapterConfig) bool {
	return legacyruntime.NormalizeModelSource(adapter.Source) == legacyruntime.ModelSourceCursorAccount
}
