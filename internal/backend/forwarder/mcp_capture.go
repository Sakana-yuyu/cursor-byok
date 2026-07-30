// mcp_capture.go 提供 MCP 调用链的两个捕获点，便于后续读取日志针对性修复已知限制：
//
//  1. MCP schema 缺失：注入的 McpDescriptor 没有 tool schema（descriptor.Tools 为空）。
//     模型只知道 server 名、不知道具体工具/参数 -> 调用失败。捕获后可统计哪些 server 缺 schema。
//
//  2. MCP 执行结果/失败：CallMcpTool 经 exec bridge 发回 Cursor 客户端执行后的结果。
//     捕获 success/error/tool_not_found/server_not_found/rejected/permission_denied 等模式，
//     便于定位执行层失败根因（目前执行仍依赖 Cursor 客户端）。
//
// 捕获走两路：debugRecorder.LogRuntime（结构化 JSONL，log:true 时开启，可按 conversationId 查询）
// + log.Printf（无条件 app.log 一行），与现有 MCP/tool 诊断代码的日志惯例一致。
package forwarder

import (
	"context"
	"log"
	"strings"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

// captureMCPSchemaGap 在 enrich 注入 MCP descriptor 后，记录哪些 server 缺少 tool schema。
// 这是已知限制：磁盘配置只含 server 启动信息，不含每个工具的 input_schema。
func (service *Service) captureMCPSchemaGap(requestID, conversationID string, descriptors []*agentv1.McpDescriptor) {
	if service == nil || len(descriptors) == 0 {
		return
	}
	missing := make([]string, 0, len(descriptors))
	for _, d := range descriptors {
		if d == nil {
			continue
		}
		// Tools 为空或全部无 ToolName 即视为 schema 缺失。
		hasTools := false
		for _, t := range d.GetTools() {
			if t != nil && strings.TrimSpace(t.GetToolName()) != "" {
				hasTools = true
				break
			}
		}
		if !hasTools {
			missing = append(missing, d.GetServerIdentifier())
		}
	}
	if len(missing) == 0 {
		return
	}
	service.debug.LogRuntime(context.Background(), requestID, conversationID, "mcp_schema_gap", map[string]any{
		"missing_schema_servers": missing,
		"total_servers":          len(descriptors),
		"note":                   "注入的 MCP server 缺少 tool schema（磁盘配置不含 input_schema），模型仅知 server 名",
	})
	log.Printf("forwarder mcp schema gap request_id=%s conversation_id=%s missing=%d/%d servers=%s",
		strings.TrimSpace(requestID), strings.TrimSpace(conversationID), len(missing), len(descriptors), strings.Join(missing, ","))
}

// captureMCPExecResult 捕获 MCP 执行的终态结果，区分成功与各类失败模式。
func (service *Service) captureMCPExecResult(intent InboundIntent, pending runtimecore.PendingExec, result execTerminalResult) {
	if service == nil {
		return
	}
	outcome := classifyMCPResultOutcome(intent.ExecClientMessage)
	server, toolName := decodeMCPExecTarget(pending.ArgsJSON)
	fields := map[string]any{
		"outcome":      outcome,
		"server":       server,
		"tool_name":    toolName,
		"tool_call_id": strings.TrimSpace(pending.ToolCallID),
		"exec_kind":    strings.TrimSpace(pending.ExecKind),
	}
	if payload := strings.TrimSpace(result.ToolResultPayload); payload != "" {
		fields["result_payload"] = truncateForLog(payload, 512)
	}
	service.debug.LogRuntime(context.Background(), intent.RequestID, intent.ConversationID, "mcp_exec_result", fields)
	// 失败模式额外打一行无条件 app.log，便于无 log:true 时也能排查。
	if outcome != "success" {
		log.Printf("forwarder mcp exec %s request_id=%s conversation_id=%s server=%s tool=%s payload=%s",
			outcome, strings.TrimSpace(intent.RequestID), strings.TrimSpace(intent.ConversationID), server, toolName,
			truncateForLog(strings.TrimSpace(result.ToolResultPayload), 200))
	}
}

// execTerminalResult 是 ApplyExecClientMessage 返回的终态结果的子集（避免循环导入 bridge 类型）。
type execTerminalResult struct {
	ToolCallID        string
	ToolResultPayload string
}

// classifyMCPResultOutcome 从 ExecClientMessage 的 McpResult oneof 推断结果类别。
func classifyMCPResultOutcome(msg *agentv1.ExecClientMessage) string {
	if msg == nil {
		return "unknown"
	}
	result := msg.GetMcpResult()
	if result == nil {
		// 非 MCP 结果（如 shell/read）但 ExecKind==mcp 时也可能走到；按 payload 兜底。
		return "non_mcp_result"
	}
	switch result.GetResult().(type) {
	case *agentv1.McpResult_Success:
		return "success"
	case *agentv1.McpResult_Error:
		return "error"
	case *agentv1.McpResult_Rejected:
		return "rejected"
	case *agentv1.McpResult_PermissionDenied:
		return "permission_denied"
	case *agentv1.McpResult_ToolNotFound:
		return "tool_not_found"
	case *agentv1.McpResult_ServerNotFound:
		return "server_not_found"
	case *agentv1.McpResult_Approved:
		return "approved"
	default:
		return "unknown"
	}
}

// decodeMCPExecTarget 从 CallMcpTool 的 ArgsJSON 解析出 server / toolName。
func decodeMCPExecTarget(argsJSON []byte) (server, toolName string) {
	payload, err := runtimecore.DecodeMCPToolPayload(argsJSON)
	if err != nil {
		return "", ""
	}
	server = strings.TrimSpace(firstNonEmpty(payload.Server, payload.ProviderIdentifier))
	toolName = strings.TrimSpace(firstNonEmpty(payload.ToolName, payload.Name))
	return server, toolName
}

// truncateForLog 截断过长的日志文本。
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
