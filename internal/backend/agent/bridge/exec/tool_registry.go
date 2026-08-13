package execbridge

import "strings"

// toolRegistry 集中维护模型工具名到执行桥 canonical kind 的映射。
// 参数解码和结果收口仍由各 open/apply 方法负责，避免注册表承担业务逻辑。
var toolRegistry = map[string]string{
	"Read":      "read",
	"Write":     "write",
	"Delete":    "delete",
	"Glob":      "glob",
	"Grep":      "grep",
	"ReadLints": "diagnostics",
	// CanvasDiagnostics 不是模型可见工具，只由 forwarder 在写入 canvas 文件后内部派发。
	"CanvasDiagnostics":       "canvas_diagnostics",
	"Ls":                      "ls",
	"Shell":                   "shell",
	"WriteShellStdin":         "write_shell_stdin",
	"ForceBackgroundShell":    "force_background_shell",
	"Task":                    "subagent",
	"CallMcpTool":             "mcp",
	"ListMcpResources":        "list_mcp_resources",
	"FetchMcpResource":        "read_mcp_resource",
	"GetMcpTools":             "mcp_state",
	"SearchConversations":     "conversation_search",
	"GitDiff":                 "git_diff",
	"Fetch":                   "fetch",
	"RecordScreen":            "record_screen",
	"ComputerUse":             "computer_use",
	"ForceBackgroundSubagent": "force_background_subagent",
	"SubagentAwait":           "subagent_await",
}

func canonicalToolKind(name string) string {
	trimmed := strings.TrimSpace(name)
	if kind, ok := toolRegistry[trimmed]; ok {
		return kind
	}
	return ""
}
