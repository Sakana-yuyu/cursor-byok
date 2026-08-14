package forwarder

import "cursor/internal/backend/runtimeconfig"

// Shared runtime DTO aliases keep external call sites stable while config
// imports runtimeconfig instead of forwarder.
type MCPTrustRecord = runtimeconfig.MCPTrustRecord
type GoalRuntimeConfig = runtimeconfig.GoalRuntimeConfig
