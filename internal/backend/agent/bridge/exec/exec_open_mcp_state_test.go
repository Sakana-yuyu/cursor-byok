package execbridge

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func TestSummarizeMcpStateResultPreservesBoundedServerError(t *testing.T) {
	server := mcpStateServer("needs-login", 0, "")
	wire, err := proto.Marshal(server)
	if err != nil {
		t.Fatal(err)
	}
	wire = protowire.AppendTag(wire, 8, protowire.BytesType)
	wire = protowire.AppendString(wire, "authorization expired "+strings.Repeat("界", 5000))
	if err := proto.Unmarshal(wire, server); err != nil {
		t.Fatal(err)
	}
	result := &agentv1.McpStateExecResult{Result: &agentv1.McpStateExecResult_Success{
		Success: &agentv1.McpStateSuccess{Servers: []*agentv1.McpStateServer{server}},
	}}
	for _, args := range []string{`{}`, `{"server":"needs-login"}`} {
		got := summarizeMcpStateResult(result, []byte(args))
		if !strings.Contains(got, "authorization expired") {
			t.Errorf("服务诊断丢失（%s）：%q", args, got)
		}
		if !strings.Contains(got, "truncated") || len(got) > 6000 {
			t.Errorf("服务诊断未按单项上限截断（%s）：bytes=%d", args, len(got))
		}
	}
}

func TestSummarizeMcpStateResultSpecificToolKeepsTargetServerFailure(t *testing.T) {
	for _, test := range []struct {
		name string
		args string
	}{
		{"identifier", `{"server":"needs-login","tool_name":"search"}`},
		{"server_name", `{"server":"NEEDS-LOGIN SERVER","tool_name":"search"}`},
		{"unmatched_pattern", `{"server":"needs-login","tool_name":"search","pattern":"search"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, toolCount := range []int{0, 1} {
				target := mcpStateServer("needs-login", toolCount, "target tool must stay filtered")
				target.ErrorMessage = stringPtr("authorization expired")
				other := mcpStateServer("other-server", 1, "other tool must stay excluded")
				other.Tools[0].ToolName = "search"
				other.ErrorMessage = stringPtr("other diagnostic must stay excluded")
				result := &agentv1.McpStateExecResult{Result: &agentv1.McpStateExecResult_Success{
					Success: &agentv1.McpStateSuccess{Servers: []*agentv1.McpStateServer{target, other}},
				}}
				got := summarizeMcpStateResult(result, []byte(test.args))
				if !strings.Contains(got, "authorization expired") || !strings.Contains(got, "matched servers=1 tools=0") {
					t.Errorf("目标服务错误被工具过滤吞掉（tools=%d）：%q", toolCount, got)
				}
				for _, excluded := range []string{"other-server", "other diagnostic", "other tool", "target tool"} {
					if strings.Contains(got, excluded) {
						t.Errorf("定向查询混入不匹配内容 %q：%q", excluded, got)
					}
				}
			}
		})
	}
}

func TestFilterMcpStateServerDoesNotKeepUnselectedServerFailure(t *testing.T) {
	server := mcpStateServer("other-server", 1, "")
	server.Tools[0].ToolName = "search"
	server.ErrorMessage = stringPtr("authorization expired")
	if _, matched := filterMcpStateServer(server, getMcpToolsArgs{Server: "needs-login", ToolName: "search"}, nil); matched {
		t.Fatal("非目标服务不能因错误诊断绕过服务筛选")
	}
}

func mcpStateServer(identifier string, toolCount int, description string) *agentv1.McpStateServer {
	schema, _ := structpb.NewValue(map[string]any{
		"type":       "object",
		"properties": map[string]any{"query": map[string]any{"type": "string"}},
	})
	tools := make([]*agentv1.McpToolDefinition, 0, toolCount)
	for index := 0; index < toolCount; index++ {
		tools = append(tools, &agentv1.McpToolDefinition{
			ToolName:    "tool" + string(rune('a'+index%26)),
			Description: description,
			InputSchema: schema,
		})
	}
	status := "connected"
	return &agentv1.McpStateServer{
		ServerName:       identifier + " server",
		ServerIdentifier: identifier,
		Status:           &status,
		Tools:            tools,
	}
}

func TestOpenGetMcpToolsRequestsNamedServerAndWaitsForIt(t *testing.T) {
	bridge := NewBridge()
	serverMessage, pending, err := bridge.OpenExec(OpenExecContext{}, runtimecore.ToolInvocation{
		CallID: "call-1", ToolName: "GetMcpTools", ArgsJSON: []byte(`{"server":" linear ","tool_name":"search"}`),
	})
	if err != nil {
		t.Fatalf("OpenExec(GetMcpTools): %v", err)
	}
	if pending.ExecKind != "mcp_state" {
		t.Fatalf("exec kind = %q", pending.ExecKind)
	}
	request := serverMessage.GetExecServerMessage().GetMcpStateExecArgs()
	if request == nil {
		t.Fatal("mcp state exec arm is not selected")
	}
	if got := request.GetServerIdentifiers(); len(got) != 1 || got[0] != "linear" {
		t.Errorf("server_identifiers = %#v, want the trimmed server", got)
	}
	// kick_only=true tells the client not to wait for a lazily started server, which
	// would return state that is still missing the schemas this tool exists to fetch.
	if request.GetKickOnly() {
		t.Error("kick_only must stay false so the client waits for the requested server")
	}
}

func TestOpenGetMcpToolsWithoutServerRequestsFullState(t *testing.T) {
	bridge := NewBridge()
	serverMessage, _, err := bridge.OpenExec(OpenExecContext{}, runtimecore.ToolInvocation{
		CallID: "call-2", ToolName: "GetMcpTools", ArgsJSON: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("OpenExec(GetMcpTools): %v", err)
	}
	if got := serverMessage.GetExecServerMessage().GetMcpStateExecArgs().GetServerIdentifiers(); len(got) != 0 {
		t.Errorf("server_identifiers = %#v, want empty for a full-catalog request", got)
	}
}

func TestOpenGetMcpToolsRejectsInvalidPattern(t *testing.T) {
	bridge := NewBridge()
	_, _, err := bridge.OpenExec(OpenExecContext{}, runtimecore.ToolInvocation{
		CallID: "call-3", ToolName: "GetMcpTools", ArgsJSON: []byte(`{"pattern":"("}`),
	})
	if err == nil {
		t.Fatal("an invalid regex must fail before the request is dispatched")
	}
	if !strings.Contains(err.Error(), "pattern") {
		t.Errorf("error = %v, want it to name the pattern argument", err)
	}
}

func TestSummarizeMcpStateResultCatalogShortensDescriptionsAndOmitsSchemas(t *testing.T) {
	longDescription := strings.Repeat("d", mcpStateCatalogDescriptionLimit+200)
	summary := summarizeMcpStateResult(&agentv1.McpStateExecResult{
		Result: &agentv1.McpStateExecResult_Success{Success: &agentv1.McpStateSuccess{
			Servers: []*agentv1.McpStateServer{mcpStateServer("linear", 2, longDescription)},
		}},
	}, []byte(`{}`))
	if strings.Contains(summary, "input_schema") {
		t.Errorf("a catalog listing must not carry input schemas:\n%s", summary)
	}
	if !strings.Contains(summary, "... [truncated]") {
		t.Errorf("a catalog listing must shorten long descriptions:\n%s", summary)
	}
	if strings.Contains(summary, strings.Repeat("d", mcpStateCatalogDescriptionLimit+1)) {
		t.Errorf("description was not shortened to %d bytes:\n%s", mcpStateCatalogDescriptionLimit, summary)
	}
}

func TestSummarizeMcpStateResultServerLookupIncludesFullSchema(t *testing.T) {
	longDescription := strings.Repeat("d", mcpStateCatalogDescriptionLimit+200)
	summary := summarizeMcpStateResult(&agentv1.McpStateExecResult{
		Result: &agentv1.McpStateExecResult_Success{Success: &agentv1.McpStateSuccess{
			Servers: []*agentv1.McpStateServer{mcpStateServer("linear", 1, longDescription)},
		}},
	}, []byte(`{"server":"linear"}`))
	if !strings.Contains(summary, "input_schema") {
		t.Errorf("a server lookup must return input schemas:\n%s", summary)
	}
	if strings.Contains(summary, "... [truncated]") {
		t.Errorf("a server lookup must keep full descriptions:\n%s", summary)
	}
}

func TestSummarizeMcpStateResultFiltersServersAndToolsLocally(t *testing.T) {
	summary := summarizeMcpStateResult(&agentv1.McpStateExecResult{
		Result: &agentv1.McpStateExecResult_Success{Success: &agentv1.McpStateSuccess{
			Servers: []*agentv1.McpStateServer{
				mcpStateServer("linear", 3, "linear tool"),
				mcpStateServer("sentry", 3, "sentry tool"),
			},
		}},
	}, []byte(`{"server":"linear","tool_name":"toola"}`))
	if strings.Contains(summary, "sentry") {
		t.Errorf("server filter must drop other servers:\n%s", summary)
	}
	if !strings.Contains(summary, "toola") {
		t.Errorf("tool filter must keep the requested tool:\n%s", summary)
	}
	if strings.Contains(summary, "toolb") {
		t.Errorf("tool filter must drop other tools:\n%s", summary)
	}
}

func TestSummarizeMcpStateResultPatternMatchesServerAndToolNames(t *testing.T) {
	summary := summarizeMcpStateResult(&agentv1.McpStateExecResult{
		Result: &agentv1.McpStateExecResult_Success{Success: &agentv1.McpStateSuccess{
			Servers: []*agentv1.McpStateServer{
				mcpStateServer("linear", 2, "x"),
				mcpStateServer("sentry", 2, "x"),
			},
		}},
	}, []byte(`{"pattern":"^sen"}`))
	if strings.Contains(summary, "linear") {
		t.Errorf("pattern filter must drop non-matching servers:\n%s", summary)
	}
	if !strings.Contains(summary, "sentry") {
		t.Errorf("pattern filter must keep matching servers:\n%s", summary)
	}
}

func TestSummarizeMcpStateResultReportsNoMatchWithoutClaimingNoServers(t *testing.T) {
	summary := summarizeMcpStateResult(&agentv1.McpStateExecResult{
		Result: &agentv1.McpStateExecResult_Success{Success: &agentv1.McpStateSuccess{
			Servers: []*agentv1.McpStateServer{mcpStateServer("linear", 1, "x")},
		}},
	}, []byte(`{"server":"missing"}`))
	if !strings.Contains(summary, "loaded servers=1") {
		t.Errorf("an empty filter result must still report how many servers are loaded:\n%s", summary)
	}
}

func TestSummarizeMcpStateResultTruncatesWithNoticeAtTheStart(t *testing.T) {
	servers := make([]*agentv1.McpStateServer, 0, mcpStateReplayServerLimit+10)
	for index := 0; index < mcpStateReplayServerLimit+10; index++ {
		servers = append(servers, mcpStateServer(strings.Repeat("s", 30)+string(rune('a'+index%26)), 20, strings.Repeat("d", 180)))
	}
	summary := summarizeMcpStateResult(&agentv1.McpStateExecResult{
		Result: &agentv1.McpStateExecResult_Success{Success: &agentv1.McpStateSuccess{Servers: servers}},
	}, []byte(`{}`))
	if len(summary) > mcpStateReplayContentLimit {
		t.Fatalf("summary is %d bytes, want at most %d", len(summary), mcpStateReplayContentLimit)
	}
	// The overall cap cuts the tail, so a notice placed at the end would be cut with it.
	head := summary
	if len(head) > 600 {
		head = head[:600]
	}
	if !strings.Contains(head, "[truncated: listed") {
		t.Errorf("the server-count notice must survive at the head of the output:\n%s", head)
	}
	if !strings.Contains(head, "server=") {
		t.Errorf("the truncation notice must tell the model how to narrow the query:\n%s", head)
	}
}

func TestSummarizeMcpStateResultErrorArms(t *testing.T) {
	if got := summarizeMcpStateResult(nil, []byte(`{}`)); got != "mcp state result missing" {
		t.Errorf("summary = %q", got)
	}
	failed := summarizeMcpStateResult(&agentv1.McpStateExecResult{
		Result: &agentv1.McpStateExecResult_Error{Error: &agentv1.McpStateError{Error: "boom"}},
	}, []byte(`{}`))
	if !strings.Contains(failed, "boom") {
		t.Errorf("summary = %q", failed)
	}
	rejected := summarizeMcpStateResult(&agentv1.McpStateExecResult{
		Result: &agentv1.McpStateExecResult_Rejected{Rejected: &agentv1.McpStateRejected{Reason: "denied"}},
	}, []byte(`{}`))
	if !strings.Contains(rejected, "denied") {
		t.Errorf("summary = %q", rejected)
	}
}

func TestApplyMcpStateResultBuildsGetMcpToolsToolCall(t *testing.T) {
	bridge := NewBridge()
	_, pending, err := bridge.OpenExec(OpenExecContext{}, runtimecore.ToolInvocation{
		CallID: "call-4", ToolName: "GetMcpTools", ArgsJSON: []byte(`{"server":"linear"}`),
	})
	if err != nil {
		t.Fatalf("OpenExec(GetMcpTools): %v", err)
	}
	applied, err := bridge.ApplyExecClientMessage(&agentv1.ExecClientMessage{
		Id: pending.MessageID, ExecId: pending.ExecID,
		Message: &agentv1.ExecClientMessage_McpStateExecResult{McpStateExecResult: &agentv1.McpStateExecResult{
			Result: &agentv1.McpStateExecResult_Success{Success: &agentv1.McpStateSuccess{
				Servers: []*agentv1.McpStateServer{mcpStateServer("linear", 1, "x")},
			}},
		}},
	}, pending)
	if err != nil {
		t.Fatalf("ApplyExecClientMessage(GetMcpTools): %v", err)
	}
	if !applied.IsTerminal {
		t.Error("mcp state result must be terminal")
	}
	toolCall := applied.ToolCall.GetGetMcpToolsToolCall()
	if toolCall == nil {
		t.Fatalf("GetMcpTools must render through its own ToolCall arm: %#v", applied.ToolCall)
	}
	if toolCall.GetArgs().GetServer() != "linear" || toolCall.GetArgs().GetToolCallId() != "call-4" {
		t.Errorf("tool call args = %#v", toolCall.GetArgs())
	}
	if toolCall.GetResult().GetSuccess().GetContent() != applied.ToolResultPayload {
		t.Error("the rendered card content must match the payload the model sees")
	}
}

func TestApplyMcpStateResultCarriesErrorIntoToolCall(t *testing.T) {
	bridge := NewBridge()
	_, pending, err := bridge.OpenExec(OpenExecContext{}, runtimecore.ToolInvocation{
		CallID: "call-5", ToolName: "GetMcpTools", ArgsJSON: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("OpenExec(GetMcpTools): %v", err)
	}
	applied, err := bridge.ApplyExecClientMessage(&agentv1.ExecClientMessage{
		Id: pending.MessageID, ExecId: pending.ExecID,
		Message: &agentv1.ExecClientMessage_McpStateExecResult{McpStateExecResult: &agentv1.McpStateExecResult{
			Result: &agentv1.McpStateExecResult_Error{Error: &agentv1.McpStateError{Error: "boom"}},
		}},
	}, pending)
	if err != nil {
		t.Fatalf("ApplyExecClientMessage(GetMcpTools): %v", err)
	}
	if got := applied.ToolCall.GetGetMcpToolsToolCall().GetResult().GetError().GetError(); !strings.Contains(got, "boom") {
		t.Errorf("tool call error = %q", got)
	}
}
