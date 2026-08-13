// exec_open_mcp_state.go 承载 GetMcpTools：向客户端查询 MCP 运行态并渲染工具 schema。
//
// 本地模式此前只能从磁盘 mcp.json 推导 MCP descriptor，那里没有 input_schema
// （见 mcp_capture.go 的 mcp_schema_gap），只能靠本仓库自己再连一遍 MCP server 拿 schema。
// 客户端的 mcp_state_exec handler 直接返回权威运行态：server 列表、状态、每个工具的
// 名称/描述/input_schema，以及 server 级 instructions。
package execbridge

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

const (
	// mcpStateReplayContentLimit 是回放给模型的总字节上限。
	mcpStateReplayContentLimit = 32 * replayKiB
	// mcpStateReplaySchemaLimit 是单个工具 input_schema 的字节上限。
	mcpStateReplaySchemaLimit = 4 * replayKiB
	// mcpStateReplayServerLimit 是列出的 server 数上限。
	mcpStateReplayServerLimit = 50
	// mcpStateReplayToolLimit 是单个 server 列出的工具数上限。
	mcpStateReplayToolLimit = 200
	// mcpStateCatalogDescriptionLimit 是目录/搜索模式下单条工具描述的字节上限。
	// 目录模式是「找到哪个工具」，不是「怎么调用它」，描述给一行足够。
	mcpStateCatalogDescriptionLimit = 200
	// mcpStateDetailDescriptionLimit 是定向查询模式下单条工具描述的字节上限。
	mcpStateDetailDescriptionLimit = 4 * replayKiB
)

// getMcpToolsArgs 是 GetMcpTools 工具的模型侧参数。
type getMcpToolsArgs struct {
	Server   string
	ToolName string
	Pattern  string
}

// detailed 表示是否为定向查询：指定了 server 才回完整描述与 input_schema。
func (args getMcpToolsArgs) detailed() bool {
	return args.Server != ""
}

func decodeGetMcpToolsArgs(raw []byte) (getMcpToolsArgs, error) {
	args, err := decodeArgsMap(raw)
	if err != nil {
		return getMcpToolsArgs{}, err
	}
	return getMcpToolsArgs{
		Server:   strings.TrimSpace(readStringArg(args, "server", "server_identifier", "serverIdentifier")),
		ToolName: strings.TrimSpace(readStringArg(args, "tool_name", "toolName")),
		Pattern:  strings.TrimSpace(readStringArg(args, "pattern")),
	}, nil
}

func compileMcpStatePattern(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	expr, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, fmt.Errorf("GetMcpTools pattern is not a valid regular expression: %w", err)
	}
	return expr, nil
}

// openGetMcpTools 构造 GetMcpTools 对应的执行桥请求。
func (bridge *Bridge) openGetMcpTools(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	input, err := decodeGetMcpToolsArgs(toolCall.ArgsJSON)
	if err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode GetMcpTools args failed: %w", err)
	}
	// 无效正则在派发前就拒绝：让模型立刻拿到可修的参数错误，
	// 而不是等客户端回一份它没法过滤的全量状态。
	if _, err := compileMcpStatePattern(input.Pattern); err != nil {
		return nil, runtimecore.PendingExec{}, err
	}
	var serverIdentifiers []string
	if input.Server != "" {
		serverIdentifiers = []string{input.Server}
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-mcp-state-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_McpStateExecArgs{
					McpStateExecArgs: &agentv1.McpStateExecArgs{
						ServerIdentifiers: serverIdentifiers,
						// kick_only=true 只踢一下懒加载的 server 并立刻返回当前状态；
						// 这个工具的价值就是拿到 schema，必须等加载完成。
						KickOnly: false,
					},
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingExec{
		MessageID:   messageID,
		ExecID:      execID,
		ArgsJSON:    append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:  toolCall.CallID,
		ExecKind:    "mcp_state",
		StreamState: "opened",
		OpenedAt:    time.Now().UTC(),
	}, nil
}

// summarizeMcpStateResult 把客户端 MCP 运行态渲染成模型可读文本。
//
// 截断分四层：单条描述上限、单个 input_schema 上限、server/工具条目数上限、整体字节上限。
// 条目数提示写在输出开头——整体截断砍掉的正是结尾，提示放末尾会连同内容一起消失。
func summarizeMcpStateResult(result *agentv1.McpStateExecResult, argsJSON []byte) string {
	if result == nil {
		return "mcp state result missing"
	}
	if failure := mcpStateFailureMessage(result); failure != "" {
		return failure
	}
	input, err := decodeGetMcpToolsArgs(argsJSON)
	if err != nil {
		input = getMcpToolsArgs{}
	}
	pattern, err := compileMcpStatePattern(input.Pattern)
	if err != nil {
		pattern = nil
	}

	loaded := result.GetSuccess().GetServers()
	matched := make([]*agentv1.McpStateServer, 0, len(loaded))
	matchedTools := 0
	for _, server := range loaded {
		filtered, ok := filterMcpStateServer(server, input, pattern)
		if !ok {
			continue
		}
		matched = append(matched, filtered)
		matchedTools += len(filtered.GetTools())
	}

	header := fmt.Sprintf("mcp state: loaded servers=%d, matched servers=%d tools=%d%s",
		len(loaded), len(matched), matchedTools, describeMcpStateFilter(input))
	if len(matched) == 0 {
		return header + "\n\nNo loaded MCP server matched this query. Widen it with pattern= or call GetMcpTools with no arguments to see the full catalog."
	}

	shown := matched
	notices := make([]string, 0, 2)
	if len(shown) > mcpStateReplayServerLimit {
		shown = shown[:mcpStateReplayServerLimit]
		notices = append(notices, fmt.Sprintf("[truncated: listed %d of %d servers]", len(shown), len(matched)))
	}
	sections := make([]string, 0, len(shown))
	for _, server := range shown {
		sections = append(sections, renderMcpStateServer(server, input.detailed()))
	}
	body := strings.Join(sections, "\n\n")

	const guidance = "[hint: pass server=<identifier> for one server's full tool schemas, or pattern=<regex> to search server and tool names]"
	prefix := header + "\n"
	truncatedPrefix := prefix + strings.Join(append(notices, guidance), "\n") + "\n\n"
	budget := mcpStateReplayContentLimit - len(truncatedPrefix)
	if budget < replayKiB {
		budget = replayKiB
	}
	truncatedBody := truncateReplayText("GetMcpTools", body, budget)
	if len(notices) == 0 && truncatedBody == body {
		return prefix + "\n" + body
	}
	return truncatedPrefix + truncatedBody
}

// mcpStateFailureMessage 返回非 success 分支的模型可读文本；success 时返回空串。
func mcpStateFailureMessage(result *agentv1.McpStateExecResult) string {
	switch item := result.GetResult().(type) {
	case *agentv1.McpStateExecResult_Error:
		return "mcp state query failed: " + strings.TrimSpace(item.Error.GetError())
	case *agentv1.McpStateExecResult_Rejected:
		return "mcp state query rejected: " + strings.TrimSpace(item.Rejected.GetReason())
	default:
		return ""
	}
}

func describeMcpStateFilter(input getMcpToolsArgs) string {
	parts := make([]string, 0, 3)
	if input.Server != "" {
		parts = append(parts, "server="+input.Server)
	}
	if input.ToolName != "" {
		parts = append(parts, "tool_name="+input.ToolName)
	}
	if input.Pattern != "" {
		parts = append(parts, "pattern="+input.Pattern)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (filter: " + strings.Join(parts, " ") + ")"
}

// filterMcpStateServer 在本地做 server/工具过滤。客户端只保证按 server_identifiers
// 预加载，是否在服务端过滤取决于它的构造选项，因此过滤必须由我方兜底。
func filterMcpStateServer(server *agentv1.McpStateServer, input getMcpToolsArgs, pattern *regexp.Regexp) (*agentv1.McpStateServer, bool) {
	if server == nil {
		return nil, false
	}
	identifier := strings.TrimSpace(server.GetServerIdentifier())
	name := strings.TrimSpace(server.GetServerName())
	if input.Server != "" && !strings.EqualFold(identifier, input.Server) && !strings.EqualFold(name, input.Server) {
		return nil, false
	}
	serverMatchesPattern := pattern == nil || pattern.MatchString(identifier) || pattern.MatchString(name)

	tools := make([]*agentv1.McpToolDefinition, 0, len(server.GetTools()))
	for _, tool := range server.GetTools() {
		if tool == nil {
			continue
		}
		toolName := mcpStateToolName(tool)
		if input.ToolName != "" && !strings.EqualFold(toolName, input.ToolName) {
			continue
		}
		if !serverMatchesPattern && !pattern.MatchString(toolName) {
			continue
		}
		tools = append(tools, tool)
	}
	if len(tools) == 0 && (!serverMatchesPattern || input.ToolName != "") {
		return nil, false
	}
	if len(tools) > mcpStateReplayToolLimit {
		tools = tools[:mcpStateReplayToolLimit]
	}
	filtered := &agentv1.McpStateServer{
		ServerName:       server.GetServerName(),
		ServerIdentifier: server.GetServerIdentifier(),
		Plugin:           server.Plugin,
		Marketplace:      server.Marketplace,
		Tools:            tools,
		Instructions:     server.GetInstructions(),
		Status:           server.Status,
	}
	return filtered, true
}

func renderMcpStateServer(server *agentv1.McpStateServer, detailed bool) string {
	identifier := strings.TrimSpace(server.GetServerIdentifier())
	name := strings.TrimSpace(server.GetServerName())
	if name == "" {
		name = identifier
	}
	attributes := []string{"id=" + identifier}
	if status := strings.TrimSpace(server.GetStatus()); status != "" {
		attributes = append(attributes, "status="+status)
	}
	if plugin := strings.TrimSpace(server.GetPlugin()); plugin != "" {
		attributes = append(attributes, "plugin="+plugin)
	}
	attributes = append(attributes, fmt.Sprintf("tools=%d", len(server.GetTools())))

	var builder strings.Builder
	fmt.Fprintf(&builder, "%s (%s)", name, strings.Join(attributes, ", "))
	if detailed {
		if instructions := mcpStateInstructions(server); instructions != "" {
			builder.WriteString("\n  instructions: ")
			builder.WriteString(truncateReplayText("GetMcpTools instructions", instructions, mcpStateDetailDescriptionLimit))
		}
	}
	for _, tool := range server.GetTools() {
		builder.WriteString("\n")
		builder.WriteString(renderMcpStateTool(tool, detailed))
	}
	return builder.String()
}

func renderMcpStateTool(tool *agentv1.McpToolDefinition, detailed bool) string {
	name := mcpStateToolName(tool)
	description := strings.TrimSpace(tool.GetDescription())
	var builder strings.Builder
	builder.WriteString("  ")
	builder.WriteString(name)
	if description != "" {
		builder.WriteString(": ")
		if detailed {
			builder.WriteString(truncateReplayText("GetMcpTools description", description, mcpStateDetailDescriptionLimit))
		} else {
			builder.WriteString(shortenMcpStateDescription(description))
		}
	}
	if !detailed {
		return builder.String()
	}
	if schema := mcpStateToolSchema(tool); schema != "" {
		builder.WriteString("\n    input_schema: ")
		builder.WriteString(truncateReplayText("GetMcpTools input_schema", schema, mcpStateReplaySchemaLimit))
	}
	return builder.String()
}

// shortenMcpStateDescription 把目录模式的描述压成单行短摘要。
func shortenMcpStateDescription(description string) string {
	single := strings.Join(strings.Fields(description), " ")
	if len(single) <= mcpStateCatalogDescriptionLimit {
		return single
	}
	return truncateUTF8Bytes(single, mcpStateCatalogDescriptionLimit) + "... [truncated]"
}

func mcpStateToolName(tool *agentv1.McpToolDefinition) string {
	if name := strings.TrimSpace(tool.GetToolName()); name != "" {
		return name
	}
	return strings.TrimSpace(tool.GetName())
}

func mcpStateToolSchema(tool *agentv1.McpToolDefinition) string {
	if raw := strings.TrimSpace(tool.GetInputSchemaJson()); raw != "" {
		return raw
	}
	schema := tool.GetInputSchema()
	if schema == nil {
		return ""
	}
	encoded, err := protojson.Marshal(schema)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func mcpStateInstructions(server *agentv1.McpStateServer) string {
	entries := make([]string, 0, len(server.GetInstructions()))
	for _, instruction := range server.GetInstructions() {
		if text := strings.TrimSpace(instruction.GetInstructions()); text != "" {
			entries = append(entries, text)
		}
	}
	if len(entries) == 0 {
		return ""
	}
	sort.Strings(entries)
	return strings.Join(entries, " ")
}

// buildGetMcpToolsCompletedToolCall 构造 GetMcpTools 对应的完成态 ToolCall。
func buildGetMcpToolsCompletedToolCall(toolCallID string, argsJSON []byte, result *agentv1.McpStateExecResult, content string) *agentv1.ToolCall {
	input, err := decodeGetMcpToolsArgs(argsJSON)
	if err != nil {
		input = getMcpToolsArgs{}
	}
	agentResult := &agentv1.GetMcpToolsAgentResult{
		Result: &agentv1.GetMcpToolsAgentResult_Success{
			Success: &agentv1.GetMcpToolsSuccess{Content: content},
		},
	}
	if failure := mcpStateFailureMessage(result); failure != "" {
		agentResult = &agentv1.GetMcpToolsAgentResult{
			Result: &agentv1.GetMcpToolsAgentResult_Error{
				Error: &agentv1.GetMcpToolsError{Error: failure},
			},
		}
	}
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_GetMcpToolsToolCall{
			GetMcpToolsToolCall: &agentv1.GetMcpToolsToolCall{
				Args: &agentv1.GetMcpToolsArgs{
					Server:     stringPtr(input.Server),
					ToolName:   stringPtr(input.ToolName),
					Pattern:    stringPtr(input.Pattern),
					ToolCallId: toolCallID,
				},
				Result: agentResult,
			},
		},
	}
}
