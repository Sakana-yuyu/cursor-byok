// tool_catalog.go 负责从静态 prompt 资产中装载并筛选 canonical tool catalog。
package forwarder

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"cursor/gen/agentv1"
	promptassets "cursor/prompt"
)

// CursorCapabilityClass separates executable protocol paths from bookkeeping
// messages. Only executable_tool entries are handler-coverage obligations.
type CursorCapabilityClass string

const (
	CursorCapabilityExecutableTool  CursorCapabilityClass = "executable tool"
	CursorCapabilityControlMessage  CursorCapabilityClass = "control message"
	CursorCapabilitySharedArgument  CursorCapabilityClass = "shared argument"
	CursorCapabilityProtocolSupport CursorCapabilityClass = "protocol support type"
)

// CursorCapabilityEntry is an auditable mapping from a protocol arm or prompt
// tool to the local path that handles it. Unsupported executable entries must
// say why, so a new Cursor build cannot be mistaken for a missing implementation.
type CursorCapabilityEntry struct {
	ProtocolName      string
	Class             CursorCapabilityClass
	Handler           string
	ReachabilityTest  string
	Status            string
	UnsupportedReason string
}

const (
	capabilityRouteTest = "internal/backend/forwarder/tool_catalog_test.go: TestCursorCapabilityImplementedRoutesReachMappedProtocol"
)

// CursorCapabilityMap returns the stable inventory used by the catalog sync
// command. Identities retain their oneof field because Cursor reuses message
// types for semantically different protocol arms. Prompt tools are listed
// separately so exposure, routing, and presentation remain independently
// auditable.
func CursorCapabilityMap() []CursorCapabilityEntry {
	implemented := func(protocolName string, class CursorCapabilityClass, handler string) CursorCapabilityEntry {
		return CursorCapabilityEntry{
			ProtocolName: protocolName, Class: class, Handler: handler,
			ReachabilityTest: capabilityRouteTest, Status: "implemented",
		}
	}
	// unsupported means "this repository implements no sender for the arm", not
	// "the Cursor client cannot execute it". The client's exec registry wires most
	// of these arms with the same mechanism it uses for arms we do drive, so an
	// entry here is a statement about our side only. Reasons must be worded that way.
	unsupported := func(protocolName, reason string) CursorCapabilityEntry {
		return CursorCapabilityEntry{
			ProtocolName: protocolName, Class: CursorCapabilityExecutableTool,
			Status: "unsupported", UnsupportedReason: reason,
		}
	}
	control := func(protocolName, reason string) CursorCapabilityEntry {
		return CursorCapabilityEntry{
			ProtocolName: protocolName, Class: CursorCapabilityControlMessage,
			Status: "control", UnsupportedReason: reason,
		}
	}
	support := func(protocolName, reason string) CursorCapabilityEntry {
		return CursorCapabilityEntry{
			ProtocolName: protocolName, Class: CursorCapabilityProtocolSupport,
			Status: "support", UnsupportedReason: reason,
		}
	}
	exec := func(protocolName, handler string) CursorCapabilityEntry {
		return implemented(protocolName, CursorCapabilityExecutableTool, handler)
	}
	toolCall := func(protocolName, handler string) CursorCapabilityEntry {
		return implemented(protocolName, CursorCapabilityProtocolSupport, handler)
	}
	promptTool := func(name, handler string) CursorCapabilityEntry {
		return implemented("PromptTool."+name, CursorCapabilityExecutableTool, handler)
	}

	entries := []CursorCapabilityEntry{
		// ExecServerMessage.oneof inventory.
		unsupported("ExecServerMessage.shell_args (ShellArgs)", "The current Shell route emits shell_stream_args; no local sender emits the legacy non-streaming arm."),
		exec("ExecServerMessage.write_args (WriteArgs)", "internal/backend/agent/bridge/exec/exec_open_fs.go: Bridge.openWrite"),
		exec("ExecServerMessage.delete_args (DeleteArgs)", "internal/backend/agent/bridge/exec/exec_open_fs.go: Bridge.openDelete"),
		exec("ExecServerMessage.grep_args (GrepArgs)", "internal/backend/agent/bridge/exec/exec_open_fs.go and exec_open_misc.go: Bridge.openGrep / Bridge.openGlob"),
		exec("ExecServerMessage.read_args (ReadArgs)", "internal/backend/agent/bridge/exec/exec_open_fs.go: Bridge.openRead"),
		unsupported("ExecServerMessage.redacted_read_args (ReadArgs)", "The bridge accepts redacted read results but has no sender for redacted read requests."),
		exec("ExecServerMessage.ls_args (LsArgs)", "internal/backend/agent/bridge/exec/exec_open_misc.go: Bridge.openLs"),
		exec("ExecServerMessage.diagnostics_args (DiagnosticsArgs)", "internal/backend/agent/bridge/exec/exec_open_misc.go: Bridge.openReadLints"),
		control("ExecServerMessage.request_context_args (RequestContextArgs)", "Cursor host context request; no provider tool exposes this control arm."),
		exec("ExecServerMessage.mcp_args (McpArgs)", "internal/backend/agent/bridge/exec/exec_open_misc.go: Bridge.openMcp"),
		exec("ExecServerMessage.shell_stream_args (ShellArgs)", "internal/backend/agent/bridge/exec/exec_open_shell.go: Bridge.openShell"),
		unsupported("ExecServerMessage.background_shell_spawn_args (BackgroundShellSpawnArgs)", "Deliberately not sent: the local Shell route reaches the same background shell through shell_stream_args with timeout 0, which additionally streams output before backgrounding. A second sender would register a duplicate shell in the same BackgroundShellState machinery with no output stream."),
		exec("ExecServerMessage.list_mcp_resources_exec_args (ListMcpResourcesExecArgs)", "internal/backend/agent/bridge/exec/exec_open_misc.go: Bridge.openListMcpResources"),
		exec("ExecServerMessage.read_mcp_resource_exec_args (ReadMcpResourceExecArgs)", "internal/backend/agent/bridge/exec/exec_open_misc.go: Bridge.openReadMcpResource"),
		exec("ExecServerMessage.mcp_state_exec_args (McpStateExecArgs)", "internal/backend/agent/bridge/exec/exec_open_mcp_state.go: Bridge.openGetMcpTools"),
		exec("ExecServerMessage.fetch_args (FetchArgs)", "internal/backend/agent/bridge/exec/exec_open_task.go: Bridge.openFetch"),
		exec("ExecServerMessage.record_screen_args (RecordScreenArgs)", "internal/backend/agent/bridge/exec/exec_open_task.go: Bridge.openRecordScreen"),
		exec("ExecServerMessage.computer_use_args (ComputerUseArgs)", "internal/backend/agent/bridge/exec/exec_open_task.go: Bridge.openComputerUse"),
		exec("ExecServerMessage.write_shell_stdin_args (WriteShellStdinArgs)", "internal/backend/agent/bridge/exec/exec_open_shell.go: Bridge.openWriteShellStdin"),
		exec("ExecServerMessage.execute_hook_args (ExecuteHookArgs)", "internal/backend/agent/bridge/exec/bridge.go: Bridge.OpenExecuteHook"),
		exec("ExecServerMessage.subagent_args (SubagentArgs)", "internal/backend/agent/bridge/exec/exec_open_task.go: Bridge.openTask"),
		exec("ExecServerMessage.force_background_shell_args (ForceBackgroundShellArgs)", "internal/backend/agent/bridge/exec/exec_open_shell.go: Bridge.openForceBackgroundShell"),
		exec("ExecServerMessage.force_background_subagent_args (ForceBackgroundSubagentArgs)", "internal/backend/agent/bridge/exec/exec_open_task.go: Bridge.openForceBackgroundSubagent"),
		exec("ExecServerMessage.subagent_await_args (SubagentAwaitArgs)", "internal/backend/agent/bridge/exec/exec_subagent_await.go: Bridge.openSubagentAwait"),
		control("ExecServerMessage.smart_mode_classifier_args (SmartModeClassifierArgs)", "Cursor mode-selection control is not a provider tool."),
		exec("ExecServerMessage.canvas_diagnostics_args (CanvasDiagnosticsArgs)", "internal/backend/agent/bridge/exec/exec_open_misc.go: Bridge.openCanvasDiagnostics"),
		control("ExecServerMessage.shell_allowlist_precheck_args (ShellAllowlistPrecheckArgs)", "Client allowlist precheck control arm."),
		control("ExecServerMessage.mcp_allowlist_precheck_args (McpAllowlistPrecheckArgs)", "Client allowlist precheck control arm."),
		control("ExecServerMessage.web_fetch_allowlist_precheck_args (WebFetchAllowlistPrecheckArgs)", "Client allowlist precheck control arm."),
		exec("ExecServerMessage.git_diff_request (GetDiffRequest)", "internal/backend/agent/bridge/exec/exec_open_git.go: Bridge.openGitDiff"),
		unsupported("ExecServerMessage.pi_read_args (PiReadExecArgs)", "Pi compatibility transport is not routed by the local exec bridge."),
		unsupported("ExecServerMessage.pi_bash_args (PiBashExecArgs)", "Pi compatibility transport is not routed by the local exec bridge."),
		unsupported("ExecServerMessage.pi_edit_args (PiEditExecArgs)", "Pi compatibility transport is not routed by the local exec bridge."),
		unsupported("ExecServerMessage.pi_write_args (PiWriteExecArgs)", "Pi compatibility transport is not routed by the local exec bridge."),
		unsupported("ExecServerMessage.pi_grep_args (PiGrepExecArgs)", "Pi compatibility transport is not routed by the local exec bridge."),
		unsupported("ExecServerMessage.pi_find_args (PiFindExecArgs)", "Pi compatibility transport is not routed by the local exec bridge."),
		unsupported("ExecServerMessage.pi_ls_args (PiLsExecArgs)", "Pi compatibility transport is not routed by the local exec bridge."),
		unsupported("ExecServerMessage.mini_swe_agent_bash_args (ShellArgs)", "The local Shell route emits shell_stream_args and does not opt into the mini-SWE transport."),
		exec("ExecServerMessage.conversation_search_args (ConversationSearchArgs)", "internal/backend/agent/bridge/exec/exec_open_conversation_search.go: Bridge.openSearchConversations"),
		control("ExecServerMessage.agent_store_conflict_args (AgentStoreConflictArgs)", "Agent-store conflict synchronization control arm."),

		// ToolCall.oneof inventory. Implemented entries are constructed by real
		// started/completed routes; the remainder are retained as reviewed protocol support.
		toolCall("ToolCall.shell_tool_call (ShellToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(Shell)"),
		toolCall("ToolCall.delete_tool_call (DeleteToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(Delete)"),
		toolCall("ToolCall.glob_tool_call (GlobToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(Glob)"),
		toolCall("ToolCall.grep_tool_call (GrepToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(Grep)"),
		toolCall("ToolCall.read_tool_call (ReadToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(Read)"),
		toolCall("ToolCall.update_todos_tool_call (UpdateTodosToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(TodoWrite)"),
		toolCall("ToolCall.read_todos_tool_call (ReadTodosToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(ReadTodos)"),
		toolCall("ToolCall.edit_tool_call (EditToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(Write/PatchEdit)"),
		toolCall("ToolCall.ls_tool_call (LsToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(Ls)"),
		toolCall("ToolCall.read_lints_tool_call (ReadLintsToolCall)", "internal/backend/agent/bridge/exec/exec_build_toolcall.go: buildReadLintsCompletedToolCall"),
		toolCall("ToolCall.mcp_tool_call (McpToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(CallMcpTool)"),
		support("ToolCall.sem_search_tool_call (SemSearchToolCall)", "No prompt or local result builder emits semantic-search presentation."),
		toolCall("ToolCall.create_plan_tool_call (CreatePlanToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(CreatePlan)"),
		toolCall("ToolCall.web_search_tool_call (WebSearchToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(WebSearch)"),
		toolCall("ToolCall.task_tool_call (TaskToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(Task)"),
		toolCall("ToolCall.list_mcp_resources_tool_call (ListMcpResourcesToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(ListMcpResources)"),
		toolCall("ToolCall.read_mcp_resource_tool_call (ReadMcpResourceToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(FetchMcpResource)"),
		support("ToolCall.apply_agent_diff_tool_call (ApplyAgentDiffToolCall)", "No local route emits Cursor apply-agent-diff presentation."),
		toolCall("ToolCall.ask_question_tool_call (AskQuestionToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(AskQuestion)"),
		toolCall("ToolCall.fetch_tool_call (FetchToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(Fetch)"),
		toolCall("ToolCall.switch_mode_tool_call (SwitchModeToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(SwitchMode)"),
		toolCall("ToolCall.generate_image_tool_call (GenerateImageToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(GenerateImage)"),
		toolCall("ToolCall.record_screen_tool_call (RecordScreenToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(RecordScreen)"),
		toolCall("ToolCall.computer_use_tool_call (ComputerUseToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(ComputerUse)"),
		toolCall("ToolCall.write_shell_stdin_tool_call (WriteShellStdinToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(WriteShellStdin)"),
		support("ToolCall.reflect_tool_call (ReflectToolCall)", "No prompt or local route emits reflection presentation."),
		support("ToolCall.setup_vm_environment_tool_call (SetupVmEnvironmentToolCall)", "VM environment setup is not owned by the local runtime."),
		support("ToolCall.truncated_tool_call (TruncatedToolCall)", "Cursor-generated truncation presentation is not a provider tool."),
		support("ToolCall.start_grind_execution_tool_call (StartGrindExecutionToolCall)", "Grind execution is not exposed by the local runtime."),
		support("ToolCall.start_grind_planning_tool_call (StartGrindPlanningToolCall)", "Grind planning is not exposed by the local runtime."),
		toolCall("ToolCall.web_fetch_tool_call (WebFetchToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(WebFetch)"),
		support("ToolCall.report_bugfix_results_tool_call (ReportBugfixResultsToolCall)", "Bugfix reporting is not exposed by the local prompt catalog."),
		support("ToolCall.ai_attribution_tool_call (AiAttributionToolCall)", "AI attribution is Cursor bookkeeping, not a local provider tool."),
		toolCall("ToolCall.pr_management_tool_call (PrManagementToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(CreatePr/UpdatePr)"),
		support("ToolCall.mcp_auth_tool_call (McpAuthToolCall)", "MCP authentication presentation has no provider-callable local route."),
		toolCall("ToolCall.await_tool_call (AwaitToolCall)", "internal/backend/forwarder/await_shell_tool.go: buildAwaitShellToolCall"),
		support("ToolCall.blame_by_file_path_tool_call (BlameByFilePathToolCall)", "Blame presentation is not exposed by the local prompt catalog."),
		toolCall("ToolCall.get_mcp_tools_tool_call (GetMcpToolsToolCall)", "internal/backend/agent/bridge/exec/exec_open_mcp_state.go: buildGetMcpToolsCompletedToolCall"),
		support("ToolCall.report_bug_tool_call (ReportBugToolCall)", "Bug reporting is not exposed by the local prompt catalog."),
		support("ToolCall.set_active_branch_tool_call (SetActiveBranchToolCall)", "Branch-selection presentation is not exposed by the local prompt catalog."),
		toolCall("ToolCall.communicate_update_tool_call (CommunicateUpdateToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(UpdateCurrentStep)"),
		toolCall("ToolCall.send_final_summary_tool_call (SendFinalSummaryToolCall)", "internal/backend/forwarder/events.go: buildStartedToolCall(send_final_summary)"),
		support("ToolCall.update_pr_code_tour_tool_call (UpdatePrCodeTourToolCall)", "PR code-tour updates are not exposed by the local prompt catalog."),
		support("ToolCall.replace_env_tool_call (ReplaceEnvToolCall)", "Environment replacement is not exposed by the local prompt catalog."),
		support("ToolCall.edit_pr_labels_tool_call (EditPrLabelsToolCall)", "Standalone PR-label editing is not exposed by the local prompt catalog."),
		support("ToolCall.record_ci_investigation_findings_tool_call (RecordCiInvestigationFindingsToolCall)", "CI investigation recording is not exposed by the local prompt catalog."),
		// Arms first observed in Cursor 3.15.6. Every reason below states what this
		// repository does not implement; none of them claims the client cannot execute the arm.
		support("ToolCall.send_message_tool_call (SendMessageToolCall)", "Cloud-agent messaging presentation; no local route builds it."),
		support("ToolCall.fetch_cloud_agent_data_tool_call (FetchCloudAgentDataToolCall)", "Cloud-agent data retrieval presentation; no local route builds it."),
		support("ToolCall.send_to_user_tool_call (SendToUserToolCall)", "Direct user-message presentation; no local prompt tool emits it."),
		support("ToolCall.pi_read_tool_call (PiReadToolCall)", "Pi transport presentation; the local exec bridge implements no pi_read_args sender."),
		support("ToolCall.pi_bash_tool_call (PiBashToolCall)", "Pi transport presentation; the local exec bridge implements no pi_bash_args sender."),
		support("ToolCall.pi_edit_tool_call (PiEditToolCall)", "Pi transport presentation; the local exec bridge implements no pi_edit_args sender."),
		support("ToolCall.pi_write_tool_call (PiWriteToolCall)", "Pi transport presentation; the local exec bridge implements no pi_write_args sender."),
		support("ToolCall.pi_grep_tool_call (PiGrepToolCall)", "Pi transport presentation; the local exec bridge implements no pi_grep_args sender."),
		support("ToolCall.pi_find_tool_call (PiFindToolCall)", "Pi transport presentation; the local exec bridge implements no pi_find_args sender."),
		support("ToolCall.pi_ls_tool_call (PiLsToolCall)", "Pi transport presentation; the local exec bridge implements no pi_ls_args sender."),
		support("ToolCall.connect_scm_tool_call (ConnectScmToolCall)", "SCM connect presentation; no local prompt tool or route drives SCM onboarding."),
		toolCall("ToolCall.search_conversations_tool_call (SearchConversationsToolCall)", "internal/backend/agent/bridge/exec/exec_open_conversation_search.go: buildSearchConversationsCompletedToolCall"),
		support("ToolCall.create_goal_tool_call (CreateGoalToolCall)", "Goal creation presentation; the local runtime keeps no goal store."),
		support("ToolCall.update_goal_tool_call (UpdateGoalToolCall)", "Goal update presentation; the local runtime keeps no goal store."),

		// Every tool schema exposed by any static prompt catalog.
		promptTool("AskQuestion", "internal/backend/forwarder/interaction_tools.go: Service.handleInteractionToolInvocation -> interaction.Bridge.OpenQuery"),
		promptTool("AwaitShell", "internal/backend/forwarder/await_shell_tool.go: Service.handleAwaitShellToolInvocation"),
		promptTool("CallMcpTool", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("ComputerUse", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("CreatePlan", "internal/backend/forwarder/interaction_tools.go: Service.handleInteractionToolInvocation -> interaction.Bridge.OpenQuery"),
		promptTool("CreatePr", "internal/backend/forwarder/interaction_tools.go: Service.handleInteractionToolInvocation -> interaction.Bridge.OpenQuery"),
		promptTool("Delete", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("Fetch", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("FetchMcpResource", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("ForceBackgroundShell", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("ForceBackgroundSubagent", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("GenerateImage", "internal/backend/forwarder/interaction_tools.go: Service.handleImmediateNativeToolInvocation"),
		promptTool("GetMcpTools", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("GitDiff", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("Glob", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("Grep", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("Ls", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("PatchEdit", "internal/backend/forwarder/patch_edit_tool.go: Service.handlePatchEditToolInvocation"),
		promptTool("Read", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("ReadLints", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("ReadTodos", "internal/backend/forwarder/interaction_tools.go: Service.handleLocalStateToolInvocation"),
		promptTool("RecordScreen", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("SearchConversations", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("SeeImage", "internal/backend/forwarder/vision_proxy.go: Service.handleSeeImageToolInvocation"),
		promptTool("send_final_summary", "internal/backend/forwarder/interaction_tools.go: Service.handleSendFinalSummaryToolInvocation"),
		promptTool("Shell", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("SubagentAwait", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),
		promptTool("SwitchMode", "internal/backend/forwarder/interaction_tools.go: Service.handleInteractionToolInvocation -> interaction.Bridge.OpenQuery"),
		promptTool("Task", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> delegation or exec.Bridge.OpenExec"),
		promptTool("TodoWrite", "internal/backend/forwarder/interaction_tools.go: Service.handleLocalStateToolInvocation"),
		promptTool("UpdateCurrentStep", "internal/backend/forwarder/communicate_update.go: Service.handleUpdateCurrentStepToolInvocation"),
		promptTool("UpdatePr", "internal/backend/forwarder/interaction_tools.go: Service.handleInteractionToolInvocation -> interaction.Bridge.OpenQuery"),
		promptTool("WebFetch", "internal/backend/forwarder/interaction_tools.go: Service.handleInteractionToolInvocation -> interaction.Bridge.OpenQuery"),
		promptTool("WebSearch", "internal/backend/forwarder/interaction_tools.go: Service.handleInteractionToolInvocation -> interaction.Bridge.OpenQuery"),
		promptTool("Write", "internal/backend/forwarder/write_tool.go: Service.handleWriteToolInvocation"),
		promptTool("WriteShellStdin", "internal/backend/forwarder/service.go: Service.handleToolInvocation -> exec.Bridge.OpenExec"),

		{ProtocolName: "AbortArgs", Class: CursorCapabilitySharedArgument, Status: "shared"},
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ProtocolName < entries[j].ProtocolName })
	return entries
}

// CursorCapabilityHandlerGaps returns only executable entries that have no
// implementation evidence and no reviewed unsupported decision. Control and
// support types deliberately never appear here.
func CursorCapabilityHandlerGaps(entries []CursorCapabilityEntry) []CursorCapabilityEntry {
	gaps := make([]CursorCapabilityEntry, 0)
	for _, entry := range entries {
		if entry.Class != CursorCapabilityExecutableTool {
			continue
		}
		if strings.TrimSpace(entry.Handler) != "" && strings.TrimSpace(entry.ReachabilityTest) != "" {
			continue
		}
		if strings.TrimSpace(entry.UnsupportedReason) != "" {
			continue
		}
		gaps = append(gaps, entry)
	}
	return gaps
}

// RenderCursorCapabilityMap writes a stable Markdown document for code review
// and Cursor upgrade audits. Fields cannot contain pipes in the curated map.
func RenderCursorCapabilityMap() string {
	var builder strings.Builder
	builder.WriteString("# Cursor Capability Map\n\n")
	builder.WriteString("Generated by `go run ./cmd/sync-tool-catalog --write`. The inventory preserves every `ExecServerMessage.oneof` and `ToolCall.oneof` field plus every exposed prompt tool. Implemented rows cite a semantic route test; suffixes alone do not make a message executable.\n\n")
	builder.WriteString("| Protocol name | Class | Handler | Reachability test | Status |\n")
	builder.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, entry := range CursorCapabilityMap() {
		status := entry.Status
		if entry.UnsupportedReason != "" {
			status += ": " + entry.UnsupportedReason
		}
		fmt.Fprintf(&builder, "| %s | %s | %s | %s | %s |\n", entry.ProtocolName, entry.Class, entry.Handler, entry.ReachabilityTest, status)
	}
	return builder.String()
}

type DefaultToolCatalog struct {
}

// NewToolCatalog 创建默认工具目录实现。
func NewToolCatalog() *DefaultToolCatalog {
	return &DefaultToolCatalog{}
}

// Load 按 mode 读取工具资产，并过滤出当前阶段真正允许暴露的工具。
func (catalog *DefaultToolCatalog) Load(mode agentv1.AgentMode, subagentTypeName string) ([]json.RawMessage, []string, error) {
	assetMode, err := toolAssetModeForConversation(mode, subagentTypeName)
	if err != nil {
		return nil, nil, err
	}
	rawTools, err := promptassets.ReadTools(assetMode)
	if err != nil {
		return nil, nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(rawTools, &items); err != nil {
		return nil, nil, fmt.Errorf("decode tools asset failed: %w", err)
	}
	filtered := make([]json.RawMessage, 0, len(items))
	names := make([]string, 0, len(items))
	for _, item := range items {
		name, err := extractToolName(item)
		if err != nil {
			return nil, nil, err
		}
		if !isToolAllowedInMode(mode, subagentTypeName, name) {
			continue
		}
		// inspect 子代理（Task 子会话 + PLAN 模式）的 Shell 描述改写为受控只读语义，
		// 并从 schema 删除 notify_on_output/profile：模型看不到的字段就不会填写，
		// 服务端剥离逻辑只兜底旧缓存请求。
		if name == "Shell" && isChildConversationSubagentTypeName(subagentTypeName) && normalizeMode(mode) == agentv1.AgentMode_AGENT_MODE_PLAN {
			item, err = rewriteReadonlyShellTool(item)
			if err != nil {
				return nil, nil, err
			}
		}
		filtered = append(filtered, item)
		names = append(names, name)
	}
	return filtered, names, nil
}

var agentModeToolNames = map[string]struct{}{
	"AskQuestion":             {},
	"CallMcpTool":             {},
	"Delete":                  {},
	"FetchMcpResource":        {},
	"GenerateImage":           {},
	"Glob":                    {},
	"Grep":                    {},
	"Ls":                      {},
	"PatchEdit":               {},
	"Read":                    {},
	"ReadLints":               {},
	"Shell":                   {},
	"AwaitShell":              {},
	"WriteShellStdin":         {},
	"ForceBackgroundShell":    {},
	"SwitchMode":              {},
	"Task":                    {},
	"TodoWrite":               {},
	"ReadTodos":               {},
	"GitDiff":                 {},
	"GetMcpTools":             {},
	"SearchConversations":     {},
	"WebFetch":                {},
	"WebSearch":               {},
	"Write":                   {},
	"Fetch":                   {},
	"RecordScreen":            {},
	"ComputerUse":             {},
	"ForceBackgroundSubagent": {},
	"SubagentAwait":           {},
	"CreatePr":                {},
	"UpdatePr":                {},
	"SeeImage":                {},
	"send_final_summary":      {},
}

var multitaskModeToolNames = map[string]struct{}{
	"AskQuestion":             {},
	"CallMcpTool":             {},
	"Delete":                  {},
	"FetchMcpResource":        {},
	"GenerateImage":           {},
	"Glob":                    {},
	"Grep":                    {},
	"Ls":                      {},
	"PatchEdit":               {},
	"Read":                    {},
	"ReadLints":               {},
	"Shell":                   {},
	"AwaitShell":              {},
	"WriteShellStdin":         {},
	"ForceBackgroundShell":    {},
	"SwitchMode":              {},
	"Task":                    {},
	"TodoWrite":               {},
	"ReadTodos":               {},
	"GitDiff":                 {},
	"GetMcpTools":             {},
	"SearchConversations":     {},
	"WebFetch":                {},
	"WebSearch":               {},
	"Write":                   {},
	"Fetch":                   {},
	"RecordScreen":            {},
	"ComputerUse":             {},
	"ForceBackgroundSubagent": {},
	"SubagentAwait":           {},
	"CreatePr":                {},
	"UpdatePr":                {},
	"SeeImage":                {},
}

var debugModeToolNames = map[string]struct{}{
	"AskQuestion":             {},
	"CallMcpTool":             {},
	"Delete":                  {},
	"FetchMcpResource":        {},
	"Glob":                    {},
	"Grep":                    {},
	"Ls":                      {},
	"PatchEdit":               {},
	"Read":                    {},
	"ReadLints":               {},
	"Shell":                   {},
	"AwaitShell":              {},
	"WriteShellStdin":         {},
	"ForceBackgroundShell":    {},
	"Task":                    {},
	"TodoWrite":               {},
	"ReadTodos":               {},
	"GitDiff":                 {},
	"GetMcpTools":             {},
	"SearchConversations":     {},
	"WebFetch":                {},
	"WebSearch":               {},
	"Write":                   {},
	"Fetch":                   {},
	"RecordScreen":            {},
	"ComputerUse":             {},
	"ForceBackgroundSubagent": {},
	"SubagentAwait":           {},
	"CreatePr":                {},
	"UpdatePr":                {},
}

var askModeToolNames = map[string]struct{}{
	"AskQuestion":             {},
	"CallMcpTool":             {},
	"Delete":                  {},
	"FetchMcpResource":        {},
	"Glob":                    {},
	"Grep":                    {},
	"Ls":                      {},
	"PatchEdit":               {},
	"Read":                    {},
	"ReadLints":               {},
	"Shell":                   {},
	"AwaitShell":              {},
	"WriteShellStdin":         {},
	"ForceBackgroundShell":    {},
	"Task":                    {},
	"TodoWrite":               {},
	"ReadTodos":               {},
	"GitDiff":                 {},
	"GetMcpTools":             {},
	"SearchConversations":     {},
	"WebFetch":                {},
	"WebSearch":               {},
	"Write":                   {},
	"Fetch":                   {},
	"RecordScreen":            {},
	"ComputerUse":             {},
	"ForceBackgroundSubagent": {},
	"SubagentAwait":           {},
	"CreatePr":                {},
	"UpdatePr":                {},
	"SeeImage":                {},
}

var planModeToolNames = map[string]struct{}{
	"AskQuestion":          {},
	"CallMcpTool":          {},
	"CreatePlan":           {},
	"FetchMcpResource":     {},
	"Glob":                 {},
	"Grep":                 {},
	"Ls":                   {},
	"Read":                 {},
	"ReadLints":            {},
	"Shell":                {},
	"AwaitShell":           {},
	"WriteShellStdin":      {},
	"ForceBackgroundShell": {},
	"Task":                 {},
	"TodoWrite":            {},
	"ReadTodos":            {},
	"GitDiff":              {},
	"GetMcpTools":          {},
	"SearchConversations":  {},
	"WebFetch":             {},
	"WebSearch":            {},
	"SeeImage":             {},
	"SubagentAwait":        {},
}

var childConversationDisallowedAgentToolNames = map[string]struct{}{
	"AskQuestion": {},
	// A child Task conversation must not be able to create another child.
	// Otherwise one model pass can recursively fan out into an unbounded
	// delegation tree (the parent concurrency limit only limits active runs).
	"Task":                    {},
	"ForceBackgroundSubagent": {},
	"SubagentAwait":           {},
	// This tool is a short history-list summary and terminates the provider
	// pass. A child must return its full final response through SubagentResult.
	"send_final_summary": {},
	// The client only enables conversation search for a top-level IDE agent
	// (it checks for the absence of subagentConfig / subagentInstanceId), so a
	// child would call an arm the client refuses to serve.
	"SearchConversations": {},
}

// childConversationOnlyToolNames 是只对 Task 子会话开放的工具。它们写的是父会话
// Task 卡片上的展示状态，顶层会话没有 Task 卡片可写，暴露出去只会浪费 token；
// 保持顶层不可见也让顶层的 tools 前缀不受影响。
var childConversationOnlyToolNames = map[string]struct{}{
	updateCurrentStepToolName: {},
}

func supportedToolNamesForMode(mode agentv1.AgentMode) map[string]struct{} {
	switch normalizeMode(mode) {
	case agentv1.AgentMode_AGENT_MODE_AGENT:
		return agentModeToolNames
	case agentv1.AgentMode_AGENT_MODE_ASK:
		return askModeToolNames
	case agentv1.AgentMode_AGENT_MODE_PLAN:
		return planModeToolNames
	case agentv1.AgentMode_AGENT_MODE_DEBUG:
		return debugModeToolNames
	case agentv1.AgentMode_AGENT_MODE_MULTITASK:
		return multitaskModeToolNames
	default:
		return nil
	}
}

func isKnownBuiltInToolName(toolName string) bool {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return false
	}
	for _, names := range []map[string]struct{}{
		agentModeToolNames,
		childConversationOnlyToolNames,
		multitaskModeToolNames,
		debugModeToolNames,
		askModeToolNames,
		planModeToolNames,
	} {
		if _, exists := names[name]; exists {
			return true
		}
	}
	return false
}

func isToolAllowedInMode(mode agentv1.AgentMode, subagentTypeName string, toolName string) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return false
	}
	if isChildConversationSubagentTypeName(subagentTypeName) {
		if _, disallowed := childConversationDisallowedAgentToolNames[trimmedToolName]; disallowed {
			return false
		}
		if _, childOnly := childConversationOnlyToolNames[trimmedToolName]; childOnly {
			return true
		}
		_, ok := agentModeToolNames[trimmedToolName]
		return ok
	}
	supported := supportedToolNamesForMode(mode)
	if supported == nil {
		return false
	}
	_, ok := supported[trimmedToolName]
	return ok
}

func isChildConversationSubagentTypeName(subagentTypeName string) bool {
	return strings.TrimSpace(subagentTypeName) != ""
}

func selectToolsByOrderedNames(items []json.RawMessage, orderedNames []string) ([]json.RawMessage, []string, error) {
	byName := make(map[string]json.RawMessage, len(items))
	for _, item := range items {
		name, err := extractToolName(item)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := byName[name]; !exists {
			byName[name] = item
		}
	}
	filtered := make([]json.RawMessage, 0, len(orderedNames))
	names := make([]string, 0, len(orderedNames))
	for _, name := range orderedNames {
		item, ok := byName[name]
		if !ok {
			return nil, nil, fmt.Errorf("tool descriptor %q not found in prompt asset", name)
		}
		filtered = append(filtered, item)
		names = append(names, name)
	}
	return filtered, names, nil
}

func toolAssetModeForConversation(mode agentv1.AgentMode, subagentTypeName string) (promptassets.Mode, error) {
	if isChildConversationSubagentTypeName(subagentTypeName) {
		return promptassets.ModeAgent, nil
	}
	return mapPromptMode(mode)
}

func promptAssetModeForConversation(mode agentv1.AgentMode, subagentTypeName string) (promptassets.Mode, error) {
	if isChildConversationSubagentTypeName(subagentTypeName) {
		return promptassets.ModeSubagent, nil
	}
	return mapPromptMode(mode)
}

// mapPromptMode 把协议 mode 映射为静态 prompt 资产对应的目录名。
func mapPromptMode(mode agentv1.AgentMode) (promptassets.Mode, error) {
	switch normalizeMode(mode) {
	case agentv1.AgentMode_AGENT_MODE_AGENT:
		return promptassets.ModeAgent, nil
	case agentv1.AgentMode_AGENT_MODE_ASK:
		return promptassets.ModeAsk, nil
	case agentv1.AgentMode_AGENT_MODE_PLAN:
		return promptassets.ModePlan, nil
	case agentv1.AgentMode_AGENT_MODE_DEBUG:
		return promptassets.ModeDebug, nil
	case agentv1.AgentMode_AGENT_MODE_MULTITASK:
		return promptassets.ModeMultitask, nil
	default:
		return "", fmt.Errorf("unsupported prompt asset mode: %s", mode.String())
	}
}

// rewriteReadonlyShellTool 把 inspect child 的 Shell 描述改写为受控白名单语义，与服务端校验保持一致；
// 同时从 schema 删除 notify_on_output/profile：模型看不到的字段就不会填写，
// 服务端剥离逻辑只兜底旧缓存请求（曾因 schema 宣告 + 服务端拒绝的矛盾导致模型无限重试 Skipped）。
func rewriteReadonlyShellTool(item json.RawMessage) (json.RawMessage, error) {
	var tool map[string]any
	if err := json.Unmarshal(item, &tool); err != nil {
		return nil, fmt.Errorf("decode Shell tool descriptor: %w", err)
	}
	function, _ := tool["function"].(map[string]any)
	if function == nil {
		return nil, fmt.Errorf("Shell tool descriptor function is missing")
	}
	function["description"] = "Restricted read-only Shell for inspect subagents. Server-enforced whitelist: a single simple command only (no pipes, redirection, variables, or command substitution; double or single quotes around arguments such as paths with spaces are allowed), working_directory must stay inside the workspace, and only a short foreground window is allowed (no backgrounding). Allowed commands: read-only git evidence (status/diff/log/show/blame/rev-parse/merge-base/ls-tree/ls-files/grep/describe/shortlog/cherry/count-objects, stash list, reflog show, plus flag-only tag/branch/remote listings; --no-pager --no-optional-locks are injected automatically), process/port queries (tasklist, netstat, ps, ss, lsof), and file hashing (sha256sum/sha1sum/md5sum/shasum, certutil -hashfile). Anything else is rejected before dispatch; do not attempt network access, builds, tests, script interpreters, or writes. Independent read-only checks should be issued as multiple Shell calls batched in the same reply."
	if parameters, ok := function["parameters"].(map[string]any); ok {
		if properties, ok := parameters["properties"].(map[string]any); ok {
			delete(properties, "notify_on_output")
			delete(properties, "profile")
			delete(properties, "required_permissions")
		}
		if required, ok := parameters["required"].([]any); ok {
			parameters["required"] = removeSchemaFields(required, "notify_on_output", "profile", "required_permissions")
		}
	}
	return json.Marshal(tool)
}

// removeSchemaFields 从 required 列表中移除指定字段，与 appendRequiredSchemaFields 对称。
func removeSchemaFields(required []any, fields ...string) []any {
	drop := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		drop[field] = struct{}{}
	}
	result := make([]any, 0, len(required))
	for _, field := range required {
		if text, ok := field.(string); ok {
			if _, found := drop[text]; found {
				continue
			}
		}
		result = append(result, field)
	}
	return result
}

// extractToolName 从原始 tool descriptor JSON 中提取函数名。
func extractToolName(raw json.RawMessage) (string, error) {
	var wrapper struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return "", fmt.Errorf("decode tool descriptor failed: %w", err)
	}
	name := strings.TrimSpace(wrapper.Function.Name)
	if name == "" {
		return "", fmt.Errorf("tool descriptor name is required")
	}
	return name, nil
}

// sanitizePromptAsset 去掉资产文件中的说明性标题，只保留真正的 prompt 文本。
func sanitizePromptAsset(text string, modelName string) string {
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "# 通用系统提示词", "# 模式静态补充", "---":
			continue
		default:
			filtered = append(filtered, line)
		}
	}
	return promptassets.RenderPromptTemplate(strings.TrimSpace(strings.Join(filtered, "\n")), modelName)
}
