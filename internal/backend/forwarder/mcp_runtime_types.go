package forwarder

import "cursor/internal/backend/runtimeconfig"

// MCPTrustRecord 是 workspace MCP 信任批准记录的共享 DTO。
// 使用类型别名保持 forwarder 接口与 server/config 返回值一致，避免重新引入配置包循环依赖。
type MCPTrustRecord = runtimeconfig.MCPTrustRecord
