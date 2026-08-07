// bridge.go 实现 MVP 阶段的执行桥协议映射。
package execbridge

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/logger"
)

// ExecApplyResult 表示一次执行桥结果归一化后的最小产物。
type ExecApplyResult struct {
	// ToolCallID 表示结果所属工具调用标识。
	ToolCallID string
	// ExecID 表示结果所属执行桥标识。
	ExecID string
	// IsTerminal 表示该执行桥是否已经收口。
	IsTerminal bool
	// ShellOutputDelta 保存 shell 流输出的增量事件。
	ShellOutputDelta *agentv1.ShellOutputDeltaUpdate
	// HookAdditionalContexts 保存客户端在 shell 流开始时带上的 hook 附加上下文。
	HookAdditionalContexts []*agentv1.HookAdditionalContext
	// ToolResultPayload 保存可回写给模型的工具结果摘要。
	ToolResultPayload string
	// ToolCall 保存可用于发 ToolCallCompletedUpdate 的工具调用对象；当前仅对支持 ToolCall 的执行型工具可用。
	ToolCall *agentv1.ToolCall
	// ExecuteHookResponse 保存 execute hook 的结构化响应。
	ExecuteHookResponse *agentv1.ExecuteHookResponse
}

// OpenExecContext 表示执行桥打开请求时需要的最小上下文。
type OpenExecContext struct {
	ConversationID               string
	RootConversationID           string
	ModelID                      string
	WorkspaceHint                string
	DirectMetaParentChild        bool
	SubagentModelOverrides       map[string]runtimecore.SubagentModelOverrideSelection
	SelectedSubagentModels       []*agentv1.RequestedModel
	SelectedSubagentModelDetails []*agentv1.ModelDetails
}

// ExecBridge 定义执行桥接口。
type ExecBridge interface {
	// OpenExec 打开一条执行桥请求。
	OpenExec(openContext OpenExecContext, toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error)
	// OpenExecuteHook 打开一条 execute hook 请求。
	OpenExecuteHook(request *agentv1.ExecuteHookRequest, execKind string) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error)
	// ApplyExecClientMessage 处理客户端执行结果。
	ApplyExecClientMessage(msg *agentv1.ExecClientMessage, pending runtimecore.PendingExec) (ExecApplyResult, error)
	// ApplyExecClientControl 处理客户端执行控制消息。
	ApplyExecClientControl(msg *agentv1.ExecClientControlMessage, pending runtimecore.PendingExec) (ExecApplyResult, error)
}

// Bridge 实现当前 MVP 阶段的执行桥。
type Bridge struct {
	// nextMessageID 生成 uint32 级别的桥消息编号。
	nextMessageID atomic.Uint32
}

// NewBridge 创建一个执行桥实例。
func NewBridge() *Bridge {
	return &Bridge{}
}

// OpenExec 打开一条执行型工具调用。
func (bridge *Bridge) OpenExec(openContext OpenExecContext, toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	switch canonicalToolKind(toolCall.ToolName) {
	case "read":
		return bridge.openRead(toolCall)
	case "write":
		return bridge.openWrite(toolCall)
	case "delete":
		return bridge.openDelete(toolCall)
	case "glob":
		return bridge.openGlob(toolCall)
	case "grep":
		return bridge.openGrep(toolCall)
	case "diagnostics":
		return bridge.openReadLints(toolCall)
	case "ls":
		return bridge.openLs(toolCall)
	case "shell":
		return bridge.openShell(toolCall)
	case "write_shell_stdin":
		return bridge.openWriteShellStdin(toolCall)
	case "force_background_shell":
		return bridge.openForceBackgroundShell(toolCall)
	case "subagent":
		return bridge.openTask(openContext, toolCall)
	case "mcp":
		return bridge.openMcp(toolCall)
	case "list_mcp_resources":
		return bridge.openListMcpResources(toolCall)
	case "read_mcp_resource":
		return bridge.openReadMcpResource(toolCall)
	case "fetch":
		return bridge.openFetch(toolCall)
	case "record_screen":
		return bridge.openRecordScreen(toolCall)
	case "computer_use":
		return bridge.openComputerUse(toolCall)
	case "force_background_subagent":
		return bridge.openForceBackgroundSubagent(toolCall)
	case "subagent_await":
		return bridge.openSubagentAwait(toolCall)
	default:
		return nil, runtimecore.PendingExec{}, fmt.Errorf("unsupported exec tool: %s", toolCall.ToolName)
	}
}

// OpenExecuteHook 打开一条 execute hook 请求。
func (bridge *Bridge) OpenExecuteHook(request *agentv1.ExecuteHookRequest, execKind string) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	if request == nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("execute hook request is required")
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-hook-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_ExecuteHookArgs{
					ExecuteHookArgs: &agentv1.ExecuteHookArgs{
						Request: request,
					},
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingExec{
		MessageID:   messageID,
		ExecID:      execID,
		ExecKind:    strings.TrimSpace(execKind),
		StreamState: "opened",
		OpenedAt:    time.Now().UTC(),
	}, nil
}

// ApplyExecClientMessage 处理客户端执行结果消息。
func (bridge *Bridge) ApplyExecClientMessage(msg *agentv1.ExecClientMessage, pending runtimecore.PendingExec) (ExecApplyResult, error) {
	if msg == nil {
		return ExecApplyResult{}, fmt.Errorf("exec client message is required")
	}

	result := ExecApplyResult{
		ToolCallID: pending.ToolCallID,
		ExecID:     pending.ExecID,
	}
	switch pending.ExecKind {
	case "read":
		readResult := normalizeReadResultForModel(msg.GetReadResult())
		result.ToolResultPayload = summarizeReadResult(readResult)
		result.ToolCall = buildReadCompletedToolCall(pending.ToolCallID, pending.ArgsJSON, readResult)
		result.IsTerminal = true
		return result, nil
	case "write":
		result.ToolResultPayload = summarizeWriteResult(msg.GetWriteResult())
		result.ToolCall = buildWriteCompletedToolCall(pending.ToolCallID, pending.ArgsJSON, msg.GetWriteResult())
		result.IsTerminal = true
		return result, nil
	case "delete":
		result.ToolResultPayload = summarizeDeleteResult(msg.GetDeleteResult())
		result.ToolCall = buildDeleteCompletedToolCall(pending.ToolCallID, pending.ArgsJSON, msg.GetDeleteResult())
		result.IsTerminal = true
		return result, nil
	case "glob":
		truncatedResult := truncateGlobResultForReplay(msg.GetGrepResult())
		result.ToolResultPayload = summarizeGlobContinuationPayload(truncatedResult, pending.ArgsJSON)
		result.ToolCall = buildGlobCompletedToolCall(pending.ToolCallID, pending.ArgsJSON, truncatedResult)
		result.IsTerminal = true
		return result, nil
	case "grep":
		truncatedResult := truncateGrepResultForReplay(msg.GetGrepResult())
		result.ToolResultPayload = summarizeGrepResult(truncatedResult)
		result.ToolCall = buildGrepCompletedToolCall(pending.ToolCallID, pending.ArgsJSON, truncatedResult)
		result.IsTerminal = true
		return result, nil
	case "diagnostics":
		result.ToolResultPayload = summarizeDiagnosticsResult(msg.GetDiagnosticsResult())
		result.ToolCall = buildReadLintsCompletedToolCall(pending.ArgsJSON, msg.GetDiagnosticsResult())
		result.IsTerminal = true
		return result, nil
	case "ls":
		result.ToolResultPayload = summarizeLsResult(msg.GetLsResult())
		result.ToolCall = buildLsCompletedToolCall(pending.ToolCallID, pending.ArgsJSON, msg.GetLsResult())
		result.IsTerminal = true
		return result, nil
	case "mcp":
		toolResult := truncateMcpToolResultForReplay(convertMcpResult(msg.GetMcpResult()))
		result.ToolResultPayload = summarizeMcpResult(toolResult)
		result.ToolCall = buildMcpCompletedToolCall(pending.ToolCallID, pending.ArgsJSON, toolResult)
		result.IsTerminal = true
		return result, nil
	case "list_mcp_resources":
		truncatedResult := truncateListMcpResourcesResultForReplay(msg.GetListMcpResourcesExecResult())
		result.ToolResultPayload = summarizeListMcpResourcesResult(truncatedResult)
		result.ToolCall = buildListMcpResourcesCompletedToolCall(pending.ArgsJSON, truncatedResult)
		result.IsTerminal = true
		return result, nil
	case "read_mcp_resource":
		truncatedResult := truncateReadMcpResourceResultForReplay(msg.GetReadMcpResourceExecResult())
		result.ToolResultPayload = summarizeReadMcpResourceResult(truncatedResult)
		result.ToolCall = buildReadMcpResourceCompletedToolCall(pending.ArgsJSON, truncatedResult)
		result.IsTerminal = true
		return result, nil
	case "fetch":
		if msg.GetFetchResult() == nil {
			return ExecApplyResult{}, fmt.Errorf("fetch result is required")
		}
		result.ToolResultPayload = summarizeFetchResult(msg.GetFetchResult())
		result.ToolCall = &agentv1.ToolCall{
			Tool: &agentv1.ToolCall_FetchToolCall{
				FetchToolCall: &agentv1.FetchToolCall{
					Args: &agentv1.FetchArgs{
						ToolCallId: pending.ToolCallID,
						Url:        readJSONStringArg(pending.ArgsJSON, "url"),
					},
					Result: msg.GetFetchResult(),
				},
			},
		}
		result.IsTerminal = true
		return result, nil
	case "record_screen":
		if msg.GetRecordScreenResult() == nil {
			return ExecApplyResult{}, fmt.Errorf("record screen result is required")
		}
		result.ToolResultPayload = summarizeRecordScreenResult(msg.GetRecordScreenResult())
		result.ToolCall = &agentv1.ToolCall{
			Tool: &agentv1.ToolCall_RecordScreenToolCall{
				RecordScreenToolCall: &agentv1.RecordScreenToolCall{
					Args: &agentv1.RecordScreenArgs{
						ToolCallId: pending.ToolCallID,
						Mode:       recordingModeFromString(readJSONStringArg(pending.ArgsJSON, "mode")),
					},
					Result: msg.GetRecordScreenResult(),
				},
			},
		}
		result.IsTerminal = true
		return result, nil
	case "computer_use":
		if msg.GetComputerUseResult() == nil {
			return ExecApplyResult{}, fmt.Errorf("computer use result is required")
		}
		result.ToolResultPayload = summarizeComputerUseResult(msg.GetComputerUseResult())
		result.ToolCall = &agentv1.ToolCall{
			Tool: &agentv1.ToolCall_ComputerUseToolCall{
				ComputerUseToolCall: &agentv1.ComputerUseToolCall{
					Args: &agentv1.ComputerUseArgs{
						ToolCallId: pending.ToolCallID,
					},
					Result: msg.GetComputerUseResult(),
				},
			},
		}
		result.IsTerminal = true
		return result, nil
	case "force_background_subagent":
		if msg.GetForceBackgroundSubagentResult() == nil {
			return ExecApplyResult{}, fmt.Errorf("force background subagent result is required")
		}
		result.ToolResultPayload = summarizeForceBackgroundSubagentResult(msg.GetForceBackgroundSubagentResult())
		result.IsTerminal = true
		return result, nil
	case "subagent":
		result.ToolResultPayload = summarizeSubagentResult(msg.GetSubagentResult())
		result.ToolCall = buildTaskCompletedToolCall(pending.ArgsJSON, msg.GetSubagentResult())
		result.IsTerminal = true
		return result, nil
	case "subagent_await":
		awaitResult := msg.GetSubagentAwaitResult()
		if awaitResult == nil {
			return result, nil
		}
		result.ToolResultPayload = summarizeSubagentAwaitResult(awaitResult)
		// still_running 不是终态：模型只能续租/继续等待，不能把它当作子代理结果。
		result.IsTerminal = awaitResult.GetStillRunning() == nil
		return result, nil
	case "write_shell_stdin":
		writeResult := msg.GetWriteShellStdinResult()
		if writeResult == nil {
			return ExecApplyResult{}, fmt.Errorf("write shell stdin result is required")
		}
		result.ToolResultPayload = summarizeWriteShellStdinResult(writeResult)
		result.ToolCall = buildWriteShellStdinCompletedToolCall(pending.ArgsJSON, writeResult)
		result.IsTerminal = true
		return result, nil
	case "force_background_shell":
		forceResult := msg.GetForceBackgroundShellResult()
		if forceResult == nil {
			return ExecApplyResult{}, fmt.Errorf("force background shell result is required")
		}
		result.ToolResultPayload = summarizeForceBackgroundShellResult(forceResult)
		result.IsTerminal = true
		return result, nil
	case "execute_hook_pre_compact", "execute_hook":
		hookResult := msg.GetExecuteHookResult()
		if hookResult == nil {
			return ExecApplyResult{}, fmt.Errorf("execute hook result is required")
		}
		result.ExecuteHookResponse = hookResult.GetResponse()
		result.ToolResultPayload = summarizeExecuteHookResponse(hookResult.GetResponse())
		result.IsTerminal = true
		return result, nil
	case "shell":
		shellResult := msg.GetShellStream()
		if shellResult == nil {
			return ExecApplyResult{}, fmt.Errorf("shell stream payload is required")
		}
		switch event := shellResult.GetEvent().(type) {
		case *agentv1.ShellStream_Stdout:
			stdoutText := DecodeShellStdout(event.Stdout)
			result.ShellOutputDelta = &agentv1.ShellOutputDeltaUpdate{
				Event: &agentv1.ShellOutputDeltaUpdate_Stdout{
					Stdout: event.Stdout,
				},
			}
			result.ToolResultPayload = stdoutText
			return result, nil
		case *agentv1.ShellStream_Stderr:
			result.ShellOutputDelta = &agentv1.ShellOutputDeltaUpdate{
				Event: &agentv1.ShellOutputDeltaUpdate_Stderr{
					Stderr: event.Stderr,
				},
			}
			result.ToolResultPayload = event.Stderr.GetData()
			return result, nil
		case *agentv1.ShellStream_Start:
			result.ShellOutputDelta = &agentv1.ShellOutputDeltaUpdate{
				Event: &agentv1.ShellOutputDeltaUpdate_Start{
					Start: event.Start,
				},
			}
			return result, nil
		case *agentv1.ShellStream_Exit:
			result.ShellOutputDelta = &agentv1.ShellOutputDeltaUpdate{
				Event: &agentv1.ShellOutputDeltaUpdate_Exit{
					Exit: event.Exit,
				},
			}
			stdout, stderr := truncateShellStreamsForReplay(pending.StdoutBuffer, pending.StderrBuffer)
			result.ToolResultPayload = summarizeShellTerminalPayload(stdout, stderr, event.Exit, false)
			result.ToolCall = buildShellCompletedToolCall(pending.ToolCallID, pending.ArgsJSON, stdout, stderr, event.Exit)
			result.IsTerminal = true
			return result, nil
		case *agentv1.ShellStream_Rejected:
			result.ToolResultPayload = fmt.Sprintf("shell rejected: %s", strings.TrimSpace(event.Rejected.GetReason()))
			result.ToolCall = buildShellRejectedToolCall(pending.ToolCallID, pending.ArgsJSON, event.Rejected)
			result.IsTerminal = true
			return result, nil
		case *agentv1.ShellStream_PermissionDenied:
			result.ToolResultPayload = fmt.Sprintf("shell permission denied: %s", strings.TrimSpace(event.PermissionDenied.GetError()))
			result.ToolCall = buildShellPermissionDeniedToolCall(pending.ToolCallID, pending.ArgsJSON, event.PermissionDenied)
			result.IsTerminal = true
			return result, nil
		case *agentv1.ShellStream_Backgrounded:
			result.ToolResultPayload = fmt.Sprintf("shell backgrounded: %d", event.Backgrounded.GetShellId())
			result.ToolCall = buildShellBackgroundedToolCall(pending.ToolCallID, pending.ArgsJSON, event.Backgrounded)
			result.IsTerminal = true
			return result, nil
		case *agentv1.ShellStream_HookContext:
			// 客户端在 shell 流开始前可能发送 hook 附加上下文事件；
			// 它只是旁路信息，不影响流式收口，忽略并继续等待后续事件。
			result.HookAdditionalContexts = append([]*agentv1.HookAdditionalContext(nil), event.HookContext.GetHookAdditionalContexts()...)
			return result, nil
		case *agentv1.ShellStream_SandboxUnsupported:
			// 客户端请求了当前 host 不支持的 sandbox policy，shell 无法执行，按终态收口。
			reason := strings.TrimSpace(event.SandboxUnsupported.GetReason())
			policyType := strings.TrimSpace(event.SandboxUnsupported.GetSandboxPolicyType())
			if reason == "" {
				reason = "unknown reason"
			}
			if policyType == "" {
				policyType = "unknown"
			}
			result.ToolResultPayload = fmt.Sprintf("shell sandbox policy unsupported on this host: %s (policy=%s)", reason, policyType)
			result.IsTerminal = true
			return result, nil
		case nil:
			// 未知字段在 protobuf 解码后可能表现为空 oneof；它不是终态，
			// 继续等待后续 shell 事件或 watchdog 收口。
			logger.Infof("exec bridge ignored empty shell stream event exec_id=%s tool_call_id=%s", strings.TrimSpace(pending.ExecID), strings.TrimSpace(pending.ToolCallID))
			return result, nil
		default:
			// 客户端协议可能领先于本仓库 proto（例如未来新增的 shell 流事件）。
			// 未知事件按非终态忽略，等待后续事件或 watchdog 收口，避免整个请求被打断。
			logger.Infof("exec bridge ignored unsupported shell stream event exec_id=%s tool_call_id=%s event_type=%T", strings.TrimSpace(pending.ExecID), strings.TrimSpace(pending.ToolCallID), event)
			return result, nil
		}
	default:
		return ExecApplyResult{}, fmt.Errorf("unsupported pending exec kind: %s", pending.ExecKind)
	}
}

// ApplyExecClientControl 处理客户端执行控制消息。
func (bridge *Bridge) ApplyExecClientControl(msg *agentv1.ExecClientControlMessage, pending runtimecore.PendingExec) (ExecApplyResult, error) {
	if msg == nil {
		return ExecApplyResult{}, fmt.Errorf("exec client control message is required")
	}

	result := ExecApplyResult{
		ToolCallID: pending.ToolCallID,
		ExecID:     pending.ExecID,
	}
	switch message := msg.GetMessage().(type) {
	case *agentv1.ExecClientControlMessage_StreamClose:
		if isStreamingExecKind(pending.ExecKind) {
			result.IsTerminal = false
			result.ToolResultPayload = fmt.Sprintf("exec stream closed: id=%d", message.StreamClose.GetId())
			return result, nil
		}

		// Non-streaming exec kinds frequently emit streamClose as a transport-level
		// ack before the actual result arrives. Treating it as terminal corrupts
		// the pending tool result (for example Read -> "exec stream closed").
		result.IsTerminal = false
		result.ToolResultPayload = ""
		return result, nil
	case *agentv1.ExecClientControlMessage_Throw:
		result.IsTerminal = true
		result.ToolResultPayload = fmt.Sprintf("exec throw: %s", strings.TrimSpace(message.Throw.GetError()))
		return result, nil
	case *agentv1.ExecClientControlMessage_Heartbeat:
		result.ToolResultPayload = "exec heartbeat"
		return result, nil
	default:
		return ExecApplyResult{}, fmt.Errorf("unsupported exec client control payload")
	}
}

// isStreamingExecKind 判断当前 exec kind 是否属于依赖后续数据面终态的流式执行桥。
func isStreamingExecKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "shell":
		return true
	default:
		return false
	}
}

// nextID 返回下一个桥消息编号。
func (bridge *Bridge) nextID() uint32 {
	current := bridge.nextMessageID.Add(1)
	if current == 0 {
		current = bridge.nextMessageID.Add(1)
	}
	return current
}

// openTask 构造 Task 对应的执行桥请求。

// openGrep 构造 Grep 对应的执行桥请求。

// buildReadCompletedToolCall 构造 Read 对应的完成态 ToolCall。
