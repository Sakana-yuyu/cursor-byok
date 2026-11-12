package execbridge

import "strings"

// toolRegistry 集中维护模型工具名到执行桥 canonical kind 的映射。
// key 一律小写；模型输出的大小写变体（Shell/SHELL/shell）与 Claude Code 习惯
// 的 bash 别名统一归一到同一 kind（参照上游 9e2d2241 的修法）。
// 参数解码和结果收口仍由各 open/apply 方法负责，避免注册表承担业务逻辑。
var toolRegistry = map[string]string{
	"read":      "read",
	"write":     "write",
	"delete":    "delete",
	"glob":      "glob",
	"grep":      "grep",
	"readlints": "diagnostics",
	// CanvasDiagnostics 不是模型可见工具，只由 forwarder 在写入 canvas 文件后内部派发。
	"canvasdiagnostics":       "canvas_diagnostics",
	"ls":                      "ls",
	"shell":                   "shell",
	"bash":                    "shell",
	"writeshellstdin":         "write_shell_stdin",
	"forcebackgroundshell":    "force_background_shell",
	"task":                    "subagent",
	"callmcptool":             "mcp",
	"listmcpresources":        "list_mcp_resources",
	"fetchmcpresource":        "read_mcp_resource",
	"getmcptools":             "mcp_state",
	"searchconversations":     "conversation_search",
	"gitdiff":                 "git_diff",
	"fetch":                   "fetch",
	"recordscreen":            "record_screen",
	"computeruse":             "computer_use",
	"forcebackgroundsubagent": "force_background_subagent",
	"subagentawait":           "subagent_await",
}

func canonicalToolKind(name string) string {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	if kind, ok := toolRegistry[trimmed]; ok {
		return kind
	}
	return ""
}
