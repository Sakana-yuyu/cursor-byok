// tool_catalog_test.go 验证工具资产（prompt/*/tools.json）与 mode 白名单的一致性，
// 并在 Cursor proto 更新后输出「新工具待接入」报告，防止升级后工具静默失效。
package forwarder

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"cursor/gen/agentv1"
	execbridge "cursor/internal/backend/agent/bridge/exec"
	interactionbridge "cursor/internal/backend/agent/bridge/interaction"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/prompt"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// modeWhitelistTests 列出每个静态工具资产对应的白名单。
// 断言：资产中出现的工具名必须被该 mode 的白名单放行，否则模型永远拿不到该工具。
var modeWhitelistTests = []struct {
	mode      prompt.Mode
	whitelist map[string]struct{}
}{
	{prompt.ModeAgent, agentModeToolNames},
	{prompt.ModeAsk, askModeToolNames},
	{prompt.ModePlan, planModeToolNames},
	{prompt.ModeDebug, debugModeToolNames},
	{prompt.ModeMultitask, multitaskModeToolNames},
}

func loadAssetToolNames(t *testing.T, mode prompt.Mode) []string {
	t.Helper()
	rawTools, err := prompt.ReadTools(mode)
	if err != nil {
		t.Fatalf("read %s/tools.json: %v", mode, err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(rawTools, &items); err != nil {
		t.Fatalf("decode %s/tools.json: %v", mode, err)
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		name, err := extractToolName(item)
		if err != nil {
			t.Fatalf("extract tool name in %s/tools.json: %v", mode, err)
		}
		names = append(names, name)
	}
	return names
}

func TestShellRequiredPermissionsSchemaIsOptionalAndScoped(t *testing.T) {
	for _, mode := range []prompt.Mode{prompt.ModeAgent, prompt.ModeAsk, prompt.ModeDebug, prompt.ModeMultitask, prompt.ModePlan} {
		t.Run(string(mode), func(t *testing.T) {
			rawTools, err := prompt.ReadTools(mode)
			if err != nil {
				t.Fatalf("read %s/tools.json: %v", mode, err)
			}
			var items []map[string]any
			if err := json.Unmarshal(rawTools, &items); err != nil {
				t.Fatalf("decode %s/tools.json: %v", mode, err)
			}
			var shell map[string]any
			for _, item := range items {
				function, _ := item["function"].(map[string]any)
				if function != nil && function["name"] == "Shell" {
					shell = function
					break
				}
			}
			if shell == nil {
				t.Fatal("Shell schema is missing")
			}
			parameters, _ := shell["parameters"].(map[string]any)
			properties, _ := parameters["properties"].(map[string]any)
			permissions, _ := properties["required_permissions"].(map[string]any)
			if permissions["type"] != "array" {
				t.Fatalf("required_permissions type = %v, want array", permissions["type"])
			}
			itemsSchema, _ := permissions["items"].(map[string]any)
			gotEnum, _ := itemsSchema["enum"].([]any)
			if len(gotEnum) != 2 || gotEnum[0] != "full_network" || gotEnum[1] != "all" {
				t.Fatalf("required_permissions enum = %#v, want [full_network all]", gotEnum)
			}
			required, _ := parameters["required"].([]any)
			for _, field := range required {
				if field == "required_permissions" {
					t.Fatal("required_permissions must remain optional")
				}
			}
		})
	}
	for _, name := range loadAssetToolNames(t, prompt.ModeSubagent) {
		if name == "Shell" {
			t.Fatal("subagent/tools.json must not add Shell")
		}
	}
}

func TestToolAssetsConsistentWithModeWhitelists(t *testing.T) {
	for _, tc := range modeWhitelistTests {
		t.Run(string(tc.mode), func(t *testing.T) {
			names := loadAssetToolNames(t, tc.mode)
			assetSet := make(map[string]struct{}, len(names))
			for _, name := range names {
				assetSet[name] = struct{}{}
			}
			// 资产 ⊆ 白名单：资产里出现但白名单不放行的工具永远不可用（白写）。
			// childConversationOnlyToolNames 例外：它们复用 agent 资产的 schema，
			// 但只对 Task 子会话放行，顶层 mode 白名单里不该出现。
			for _, name := range names {
				if _, childOnly := childConversationOnlyToolNames[name]; childOnly {
					continue
				}
				if _, ok := tc.whitelist[name]; !ok {
					t.Errorf("工具 %q 已加入 %s/tools.json 资产但未加入 %s 白名单（tool_catalog.go），模型将无法使用该工具", name, tc.mode, tc.mode)
				}
			}
			// 白名单 ⊆ 资产：白名单放行但该 mode 资产没有 schema 的工具，
			// Load 阶段会被 selectToolsByOrderedNames 过滤/报错，同样不可用。
			for name := range tc.whitelist {
				if _, ok := assetSet[name]; !ok {
					t.Errorf("%s 白名单工具 %q 缺少 %s/tools.json schema，模型将无法使用该工具", tc.mode, name, tc.mode)
				}
			}
		})
	}
	// subagent/tools.json 是只读子代理的最小工具集（独立资产），
	// 其中每个工具都必须属于 agent 全量白名单。
	for _, name := range loadAssetToolNames(t, prompt.ModeSubagent) {
		if _, ok := agentModeToolNames[name]; !ok {
			t.Errorf("subagent 资产工具 %q 不在 agent 白名单中，子代理会话将无法使用该工具", name)
		}
	}
	// 子会话专属工具走 agent 资产的 schema，且不得泄漏进任何顶层 mode 白名单。
	agentAssetNames := make(map[string]struct{})
	for _, name := range loadAssetToolNames(t, prompt.ModeAgent) {
		agentAssetNames[name] = struct{}{}
	}
	for name := range childConversationOnlyToolNames {
		if _, ok := agentAssetNames[name]; !ok {
			t.Errorf("子会话专属工具 %q 缺少 agent/tools.json schema，子会话将无法使用该工具", name)
		}
		for _, tc := range modeWhitelistTests {
			if _, ok := tc.whitelist[name]; ok {
				t.Errorf("子会话专属工具 %q 不应出现在 %s 顶层白名单中", name, tc.mode)
			}
		}
	}
}

func TestReadonlyExploreShellSchemaRemovesEscalationAndUnsupportedFields(t *testing.T) {
	tools, names, err := NewToolCatalog().Load(agentv1.AgentMode_AGENT_MODE_PLAN, "explore")
	if err != nil {
		t.Fatalf("load readonly explore tools: %v", err)
	}
	var shell json.RawMessage
	for index, name := range names {
		if name == "Shell" {
			shell = tools[index]
			break
		}
	}
	if shell == nil {
		t.Fatal("readonly explore catalog must contain Shell")
	}
	var tool map[string]any
	if err := json.Unmarshal(shell, &tool); err != nil {
		t.Fatalf("decode readonly Shell schema: %v", err)
	}
	function, _ := tool["function"].(map[string]any)
	parameters, _ := function["parameters"].(map[string]any)
	properties, _ := parameters["properties"].(map[string]any)
	for _, field := range []string{"required_permissions", "notify_on_output", "profile"} {
		if _, found := properties[field]; found {
			t.Fatalf("readonly Shell schema still exposes %q", field)
		}
	}
	for _, field := range []string{"command", "working_directory", "block_until_ms"} {
		if _, found := properties[field]; !found {
			t.Fatalf("readonly Shell schema lost existing field %q", field)
		}
	}
	required, _ := parameters["required"].([]any)
	for _, field := range required {
		if field == "required_permissions" || field == "notify_on_output" || field == "profile" {
			t.Fatalf("readonly Shell required list still exposes %q", field)
		}
	}
}

func TestChildConversationCannotDispatchSubagents(t *testing.T) {
	tools, names, err := NewToolCatalog().Load(agentv1.AgentMode_AGENT_MODE_PLAN, "explore")
	if err != nil {
		t.Fatalf("load child conversation tools: %v", err)
	}
	if len(tools) != len(names) {
		t.Fatalf("loaded tool descriptors = %d, names = %d", len(tools), len(names))
	}
	exposed := make(map[string]struct{}, len(names))
	for _, name := range names {
		exposed[name] = struct{}{}
	}

	for _, toolName := range []string{"Task", "ForceBackgroundSubagent", "SubagentAwait", "send_final_summary"} {
		if _, ok := exposed[toolName]; ok {
			t.Errorf("child tool catalog must not expose %q", toolName)
		}
		if isToolAllowedInMode(agentv1.AgentMode_AGENT_MODE_PLAN, "explore", toolName) {
			t.Errorf("child invocation guard must reject %q", toolName)
		}
	}
	for _, toolName := range []string{"Read", "Grep", "Shell"} {
		if _, ok := exposed[toolName]; !ok {
			t.Errorf("child tool catalog should retain %q", toolName)
		}
		if !isToolAllowedInMode(agentv1.AgentMode_AGENT_MODE_PLAN, "explore", toolName) {
			t.Errorf("child invocation guard should allow %q", toolName)
		}
	}
}

func TestConversationSearchIsTopLevelOnlyWhileMcpDiscoveryIsNot(t *testing.T) {
	_, names, err := NewToolCatalog().Load(agentv1.AgentMode_AGENT_MODE_AGENT, "")
	if err != nil {
		t.Fatalf("load agent tools: %v", err)
	}
	exposed := make(map[string]struct{}, len(names))
	for _, name := range names {
		exposed[name] = struct{}{}
	}
	for _, toolName := range []string{"GetMcpTools", "SearchConversations"} {
		if _, ok := exposed[toolName]; !ok {
			t.Errorf("top-level agent catalog must expose %q", toolName)
		}
	}

	// The client only serves conversation search to a top-level IDE agent, so a
	// child would call an arm the client refuses; MCP discovery has no such gate.
	if isToolAllowedInMode(agentv1.AgentMode_AGENT_MODE_AGENT, "explore", "SearchConversations") {
		t.Error("child invocation guard must reject SearchConversations")
	}
	if !isToolAllowedInMode(agentv1.AgentMode_AGENT_MODE_AGENT, "explore", "GetMcpTools") {
		t.Error("child invocation guard should allow GetMcpTools")
	}
}

func TestCursorCapabilityMapClassifiesReachableProtocolEntries(t *testing.T) {
	entries := CursorCapabilityMap()
	byProtocol := make(map[string]CursorCapabilityEntry, len(entries))
	for _, entry := range entries {
		byProtocol[entry.ProtocolName] = entry
	}

	for _, protocolName := range []string{
		"ExecServerMessage.shell_stream_args (ShellArgs)",
		"ExecServerMessage.mcp_args (McpArgs)",
		"ExecServerMessage.subagent_args (SubagentArgs)",
		"PromptTool.TodoWrite",
	} {
		entry, ok := byProtocol[protocolName]
		if !ok || entry.Class != CursorCapabilityExecutableTool {
			t.Errorf("%s must be an executable protocol capability: %#v", protocolName, entry)
		}
	}
	for protocolName, want := range map[string]CursorCapabilityClass{
		"ExecServerMessage.agent_store_conflict_args (AgentStoreConflictArgs)": CursorCapabilityControlMessage,
		"AbortArgs": CursorCapabilitySharedArgument,
		"ToolCall.sem_search_tool_call (SemSearchToolCall)": CursorCapabilityProtocolSupport,
	} {
		if got := byProtocol[protocolName]; got.Class != want {
			t.Errorf("%s class = %q, want %q", protocolName, got.Class, want)
		}
	}
}

func TestCursorCapabilityMapExecutableEntriesAreAuditable(t *testing.T) {
	if gaps := CursorCapabilityHandlerGaps(CursorCapabilityMap()); len(gaps) > 0 {
		t.Fatalf("only executable entries are handler gaps: %#v", gaps)
	}
}

func TestCursorCapabilityMapCoversProtocolArmsAndPromptTools(t *testing.T) {
	entries := CursorCapabilityMap()
	byProtocol := make(map[string]CursorCapabilityEntry, len(entries))
	for _, entry := range entries {
		if _, exists := byProtocol[entry.ProtocolName]; exists {
			t.Errorf("duplicate capability identity %q", entry.ProtocolName)
		}
		byProtocol[entry.ProtocolName] = entry
	}

	assertOneofCoverage := func(messageName string, message interface{ ProtoReflect() protoreflect.Message }) {
		t.Helper()
		descriptor := message.ProtoReflect().Descriptor()
		oneof := descriptor.Oneofs().Get(0)
		fields := oneof.Fields()
		for index := 0; index < fields.Len(); index++ {
			field := fields.Get(index)
			identity := fmt.Sprintf("%s.%s (%s)", messageName, field.Name(), field.Message().Name())
			if _, ok := byProtocol[identity]; !ok {
				t.Errorf("%s is absent from the capability map", identity)
			}
		}
	}
	assertOneofCoverage("ExecServerMessage", &agentv1.ExecServerMessage{})
	assertOneofCoverage("ToolCall", &agentv1.ToolCall{})

	promptTools := map[string]struct{}{}
	for _, tc := range modeWhitelistTests {
		for _, name := range loadAssetToolNames(t, tc.mode) {
			promptTools[name] = struct{}{}
		}
	}
	for _, name := range loadAssetToolNames(t, prompt.ModeSubagent) {
		promptTools[name] = struct{}{}
	}
	for name := range promptTools {
		identity := "PromptTool." + name
		if _, ok := byProtocol[identity]; !ok {
			t.Errorf("%s is absent from the capability map", identity)
		}
	}
}

func capabilityOneofIdentity(messageName string, message interface{ ProtoReflect() protoreflect.Message }) string {
	descriptor := message.ProtoReflect().Descriptor()
	oneof := descriptor.Oneofs().Get(0)
	field := message.ProtoReflect().WhichOneof(oneof)
	if field == nil {
		return ""
	}
	return fmt.Sprintf("%s.%s (%s)", messageName, field.Name(), field.Message().Name())
}

func TestCursorCapabilityImplementedRoutesReachMappedProtocol(t *testing.T) {
	reached := map[string]bool{}
	reachToolCall := func(toolCall *agentv1.ToolCall) {
		t.Helper()
		identity := capabilityOneofIdentity("ToolCall", toolCall)
		if identity == "" {
			t.Fatalf("ToolCall has no selected oneof: %#v", toolCall)
		}
		reached[identity] = true
	}

	execCases := []struct {
		toolName string
		argsJSON string
		wantArm  string
	}{
		{"Read", `{"path":"x"}`, "ExecServerMessage.read_args (ReadArgs)"},
		{"Write", `{"path":"x","contents":"x"}`, "ExecServerMessage.write_args (WriteArgs)"},
		{"Delete", `{"path":"x"}`, "ExecServerMessage.delete_args (DeleteArgs)"},
		{"Glob", `{"glob_pattern":"*.go","target_directory":"."}`, "ExecServerMessage.grep_args (GrepArgs)"},
		{"Grep", `{"pattern":"x"}`, "ExecServerMessage.grep_args (GrepArgs)"},
		{"ReadLints", "{}", "ExecServerMessage.diagnostics_args (DiagnosticsArgs)"},
		{"Ls", `{"path":"."}`, "ExecServerMessage.ls_args (LsArgs)"},
		{"Shell", `{"command":"echo ok"}`, "ExecServerMessage.shell_stream_args (ShellArgs)"},
		{"WriteShellStdin", `{"shell_id":1,"chars":"x"}`, "ExecServerMessage.write_shell_stdin_args (WriteShellStdinArgs)"},
		{"ForceBackgroundShell", `{"tool_call_id":"target"}`, "ExecServerMessage.force_background_shell_args (ForceBackgroundShellArgs)"},
		{"Task", `{"subagent_type":"explore","access_mode":"inspect","prompt":"x"}`, "ExecServerMessage.subagent_args (SubagentArgs)"},
		{"CallMcpTool", `{"server":"s","toolName":"t","arguments":{}}`, "ExecServerMessage.mcp_args (McpArgs)"},
		{"ListMcpResources", `{"server":"s"}`, "ExecServerMessage.list_mcp_resources_exec_args (ListMcpResourcesExecArgs)"},
		{"FetchMcpResource", `{"server":"s","uri":"x"}`, "ExecServerMessage.read_mcp_resource_exec_args (ReadMcpResourceExecArgs)"},
		{"Fetch", `{"url":"https://example.com"}`, "ExecServerMessage.fetch_args (FetchArgs)"},
		{"GitDiff", `{"base_ref":"main","merge_base":true}`, "ExecServerMessage.git_diff_request (GetDiffRequest)"},
		{"GetMcpTools", `{"server":"linear"}`, "ExecServerMessage.mcp_state_exec_args (McpStateExecArgs)"},
		{"SearchConversations", `{"query":"auth bug"}`, "ExecServerMessage.conversation_search_args (ConversationSearchArgs)"},
		{"RecordScreen", "{}", "ExecServerMessage.record_screen_args (RecordScreenArgs)"},
		{"ComputerUse", `{"actions":[{"type":"screenshot"}]}`, "ExecServerMessage.computer_use_args (ComputerUseArgs)"},
		{"ForceBackgroundSubagent", `{"tool_call_id":"target"}`, "ExecServerMessage.force_background_subagent_args (ForceBackgroundSubagentArgs)"},
		{"SubagentAwait", `{"agent_id":"agent-1"}`, "ExecServerMessage.subagent_await_args (SubagentAwaitArgs)"},
	}
	execBridge := execbridge.NewBridge()
	for _, tc := range execCases {
		t.Run("exec_"+tc.toolName, func(t *testing.T) {
			if tc.toolName != "ListMcpResources" && !isExecTool(tc.toolName) {
				t.Fatalf("%s is not admitted by the service exec route", tc.toolName)
			}
			serverMessage, pending, err := execBridge.OpenExec(execbridge.OpenExecContext{
				ConversationID: "conversation", ModelID: "model", WorkspaceHint: ".",
			}, runtimecore.ToolInvocation{CallID: "call-" + tc.toolName, ToolName: tc.toolName, ArgsJSON: []byte(tc.argsJSON)})
			if err != nil {
				t.Fatalf("OpenExec(%s): %v", tc.toolName, err)
			}
			execMessage := serverMessage.GetExecServerMessage()
			if identity := capabilityOneofIdentity("ExecServerMessage", execMessage); identity != tc.wantArm {
				t.Fatalf("OpenExec(%s) arm = %q, want %q", tc.toolName, identity, tc.wantArm)
			}
			reached[tc.wantArm] = true
			// These arms are only built when the client result comes back, so the
			// started-tool-call table below cannot reach them.
			var clientMessage *agentv1.ExecClientMessage
			switch tc.toolName {
			case "ReadLints":
				clientMessage = &agentv1.ExecClientMessage{
					Message: &agentv1.ExecClientMessage_DiagnosticsResult{DiagnosticsResult: &agentv1.DiagnosticsResult{}},
				}
			case "GetMcpTools":
				clientMessage = &agentv1.ExecClientMessage{
					Message: &agentv1.ExecClientMessage_McpStateExecResult{McpStateExecResult: &agentv1.McpStateExecResult{
						Result: &agentv1.McpStateExecResult_Success{Success: &agentv1.McpStateSuccess{}},
					}},
				}
			case "SearchConversations":
				clientMessage = &agentv1.ExecClientMessage{
					Message: &agentv1.ExecClientMessage_ConversationSearchResult{ConversationSearchResult: &agentv1.ConversationSearchResult{
						Result: &agentv1.ConversationSearchResult_Success{Success: &agentv1.ConversationSearchSuccess{}},
					}},
				}
			}
			if clientMessage == nil {
				return
			}
			clientMessage.Id = pending.MessageID
			clientMessage.ExecId = pending.ExecID
			applied, err := execBridge.ApplyExecClientMessage(clientMessage, pending)
			if err != nil {
				t.Fatalf("ApplyExecClientMessage(%s): %v", tc.toolName, err)
			}
			reachToolCall(applied.ToolCall)
		})
	}

	// CanvasDiagnostics 不是模型可见工具：Write/PatchEdit 命中 canvas 路径后由 forwarder 内部派发，
	// 因此不进 execCases 表（那张表同时断言工具已被 prompt 侧的 exec 路由准入）。
	canvasMessage, _, err := execBridge.OpenExec(execbridge.OpenExecContext{
		ConversationID: "conversation",
	}, runtimecore.ToolInvocation{
		CallID:   "call-CanvasDiagnostics",
		ToolName: "CanvasDiagnostics",
		ArgsJSON: []byte(`{"path":"/ws/.cursor/projects/p/canvases/a.canvas.tsx"}`),
	})
	if err != nil {
		t.Fatalf("OpenExec(CanvasDiagnostics): %v", err)
	}
	canvasIdentity := capabilityOneofIdentity("ExecServerMessage", canvasMessage.GetExecServerMessage())
	if canvasIdentity != "ExecServerMessage.canvas_diagnostics_args (CanvasDiagnosticsArgs)" {
		t.Fatalf("OpenExec(CanvasDiagnostics) arm = %q", canvasIdentity)
	}
	reached[canvasIdentity] = true

	hookMessage, _, err := execBridge.OpenExecuteHook(&agentv1.ExecuteHookRequest{
		Request: &agentv1.ExecuteHookRequest_PreCompact{PreCompact: &agentv1.PreCompactRequestQuery{}},
	}, "execute_hook_pre_compact")
	if err != nil {
		t.Fatalf("OpenExecuteHook: %v", err)
	}
	hookIdentity := capabilityOneofIdentity("ExecServerMessage", hookMessage.GetExecServerMessage())
	if hookIdentity != "ExecServerMessage.execute_hook_args (ExecuteHookArgs)" {
		t.Fatalf("OpenExecuteHook arm = %q", hookIdentity)
	}
	reached[hookIdentity] = true

	interactionCases := []struct {
		toolName string
		argsJSON string
	}{
		{"AskQuestion", "{}"},
		{"CreatePlan", "{}"},
		{"WebSearch", `{"search_term":"x"}`},
		{"WebFetch", `{"url":"https://example.com"}`},
		{"SwitchMode", `{"target_mode_id":"agent"}`},
		{"CreatePr", `{"title":"x"}`},
		{"UpdatePr", "{}"},
	}
	interactionBridge := interactionbridge.NewBridge()
	for _, tc := range interactionCases {
		t.Run("interaction_"+tc.toolName, func(t *testing.T) {
			if !isInteractionTool(tc.toolName) {
				t.Fatalf("%s is not admitted by the service interaction route", tc.toolName)
			}
			if _, _, err := interactionBridge.OpenQuery(runtimecore.ToolInvocation{
				CallID: "call-" + tc.toolName, ToolName: tc.toolName, ArgsJSON: []byte(tc.argsJSON),
			}); err != nil {
				t.Fatalf("OpenQuery(%s): %v", tc.toolName, err)
			}
		})
	}

	startedCases := []struct {
		toolName string
		argsJSON string
		wantArm  string
	}{
		{"Shell", `{"command":"echo ok"}`, "ToolCall.shell_tool_call (ShellToolCall)"},
		{"Delete", `{"path":"x"}`, "ToolCall.delete_tool_call (DeleteToolCall)"},
		{"Glob", `{"glob_pattern":"*.go"}`, "ToolCall.glob_tool_call (GlobToolCall)"},
		{"Grep", `{"pattern":"x"}`, "ToolCall.grep_tool_call (GrepToolCall)"},
		{"Read", `{"path":"x"}`, "ToolCall.read_tool_call (ReadToolCall)"},
		{"TodoWrite", `{"todos":[]}`, "ToolCall.update_todos_tool_call (UpdateTodosToolCall)"},
		{"ReadTodos", "{}", "ToolCall.read_todos_tool_call (ReadTodosToolCall)"},
		{"Write", `{"path":"x","contents":"x"}`, "ToolCall.edit_tool_call (EditToolCall)"},
		{"Ls", `{"path":"."}`, "ToolCall.ls_tool_call (LsToolCall)"},
		{"CallMcpTool", `{"server":"s","toolName":"t","arguments":{}}`, "ToolCall.mcp_tool_call (McpToolCall)"},
		{"CreatePlan", "{}", "ToolCall.create_plan_tool_call (CreatePlanToolCall)"},
		{"WebSearch", `{"search_term":"x"}`, "ToolCall.web_search_tool_call (WebSearchToolCall)"},
		{"Task", `{"subagent_type":"explore","access_mode":"inspect","prompt":"x"}`, "ToolCall.task_tool_call (TaskToolCall)"},
		{"ListMcpResources", `{"server":"s"}`, "ToolCall.list_mcp_resources_tool_call (ListMcpResourcesToolCall)"},
		{"FetchMcpResource", `{"server":"s","uri":"x"}`, "ToolCall.read_mcp_resource_tool_call (ReadMcpResourceToolCall)"},
		{"AskQuestion", "{}", "ToolCall.ask_question_tool_call (AskQuestionToolCall)"},
		{"Fetch", `{"url":"https://example.com"}`, "ToolCall.fetch_tool_call (FetchToolCall)"},
		{"SwitchMode", `{"target_mode_id":"agent"}`, "ToolCall.switch_mode_tool_call (SwitchModeToolCall)"},
		{"GenerateImage", "{}", "ToolCall.generate_image_tool_call (GenerateImageToolCall)"},
		{"RecordScreen", "{}", "ToolCall.record_screen_tool_call (RecordScreenToolCall)"},
		{"ComputerUse", `{"actions":[{"type":"screenshot"}]}`, "ToolCall.computer_use_tool_call (ComputerUseToolCall)"},
		{"WriteShellStdin", `{"shell_id":1,"chars":"x"}`, "ToolCall.write_shell_stdin_tool_call (WriteShellStdinToolCall)"},
		{"WebFetch", `{"url":"https://example.com"}`, "ToolCall.web_fetch_tool_call (WebFetchToolCall)"},
		{"CreatePr", `{"title":"x"}`, "ToolCall.pr_management_tool_call (PrManagementToolCall)"},
		{"AwaitShell", `{"shell_id":1}`, "ToolCall.await_tool_call (AwaitToolCall)"},
		{"send_final_summary", `{"final_summary":"done"}`, "ToolCall.send_final_summary_tool_call (SendFinalSummaryToolCall)"},
		{"UpdateCurrentStep", `{"current_step":"Running CLI tests"}`, "ToolCall.communicate_update_tool_call (CommunicateUpdateToolCall)"},
	}
	for _, tc := range startedCases {
		toolCall := buildStartedToolCall(runtimecore.ToolInvocation{CallID: "call-" + tc.toolName, ToolName: tc.toolName, ArgsJSON: []byte(tc.argsJSON)})
		identity := capabilityOneofIdentity("ToolCall", toolCall)
		if identity != tc.wantArm {
			t.Fatalf("buildStartedToolCall(%s) arm = %q, want %q", tc.toolName, identity, tc.wantArm)
		}
		reached[identity] = true
	}

	for _, toolName := range []string{"GenerateImage", "AwaitShell", "SeeImage", "send_final_summary", "UpdateCurrentStep"} {
		if !isImmediateNativeTool(toolName) {
			t.Errorf("%s is not admitted by the immediate-native route", toolName)
		}
	}
	if buildStartedToolCall(runtimecore.ToolInvocation{CallID: "see-image", ToolName: "SeeImage", ArgsJSON: []byte(`{"image_path":"x.png"}`)}) != nil {
		t.Error("SeeImage intentionally has no dedicated ToolCall arm")
	}
	for _, toolName := range []string{"TodoWrite", "ReadTodos"} {
		if !isLocalStateTool(toolName) {
			t.Errorf("%s is not admitted by the local-state route", toolName)
		}
	}
	if !isPatchEditToolName("PatchEdit") {
		t.Error("PatchEdit is not admitted by the patch-edit route")
	}

	for _, tc := range execCases {
		if tc.toolName != "ListMcpResources" {
			reached["PromptTool."+tc.toolName] = true
		}
	}
	for _, tc := range interactionCases {
		reached["PromptTool."+tc.toolName] = true
	}
	for _, toolName := range []string{"GenerateImage", "AwaitShell", "SeeImage", "send_final_summary", "UpdateCurrentStep", "TodoWrite", "ReadTodos", "PatchEdit"} {
		reached["PromptTool."+toolName] = true
	}

	for _, entry := range CursorCapabilityMap() {
		if entry.Status == "implemented" && !reached[entry.ProtocolName] {
			t.Errorf("implemented capability %s was not reached through its real route", entry.ProtocolName)
		}
	}
}

func TestCursorCapabilityMapDistinguishesReusedExecArms(t *testing.T) {
	want := []string{
		"ExecServerMessage.read_args (ReadArgs)",
		"ExecServerMessage.redacted_read_args (ReadArgs)",
		"ExecServerMessage.shell_args (ShellArgs)",
		"ExecServerMessage.shell_stream_args (ShellArgs)",
		"ExecServerMessage.mini_swe_agent_bash_args (ShellArgs)",
	}
	byProtocol := map[string]CursorCapabilityEntry{}
	for _, entry := range CursorCapabilityMap() {
		byProtocol[entry.ProtocolName] = entry
	}
	for _, identity := range want {
		if _, ok := byProtocol[identity]; !ok {
			t.Errorf("missing distinct exec arm %s", identity)
		}
	}
}

func TestCursorCapabilityMapKnownMappingsAreFactuallyReachable(t *testing.T) {
	byProtocol := map[string]CursorCapabilityEntry{}
	for _, entry := range CursorCapabilityMap() {
		byProtocol[entry.ProtocolName] = entry
		if entry.Status == "implemented" {
			if entry.ReachabilityTest != "internal/backend/forwarder/tool_catalog_test.go: TestCursorCapabilityImplementedRoutesReachMappedProtocol" {
				t.Errorf("implemented capability %s cites non-semantic reachability evidence %q", entry.ProtocolName, entry.ReachabilityTest)
			}
		}
	}

	listResources := byProtocol["ExecServerMessage.list_mcp_resources_exec_args (ListMcpResourcesExecArgs)"]
	if listResources.Status != "implemented" || !strings.Contains(listResources.Handler, "openListMcpResources") {
		t.Errorf("ListMcpResources mapping is not the real exec bridge route: %#v", listResources)
	}
	backgroundSpawn := byProtocol["ExecServerMessage.background_shell_spawn_args (BackgroundShellSpawnArgs)"]
	if backgroundSpawn.Status != "unsupported" || strings.TrimSpace(backgroundSpawn.UnsupportedReason) == "" {
		t.Errorf("BackgroundShellSpawn must not be marked implemented without an emitting route: %#v", backgroundSpawn)
	}
	todo := byProtocol["PromptTool.TodoWrite"]
	if !strings.Contains(todo.Handler, "interaction_tools.go: Service.handleLocalStateToolInvocation") {
		t.Errorf("TodoWrite mapping points at the wrong handler: %#v", todo)
	}
}

func TestRenderCursorCapabilityMapIsDeterministic(t *testing.T) {
	first := RenderCursorCapabilityMap()
	second := RenderCursorCapabilityMap()
	if first != second {
		t.Fatal("capability map rendering is not deterministic")
	}
	if !strings.Contains(first, "| ExecServerMessage.shell_stream_args (ShellArgs) | executable tool |") {
		t.Fatalf("rendered map is missing the Shell exec arm: %s", first)
	}
}
