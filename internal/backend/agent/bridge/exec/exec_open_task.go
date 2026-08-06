// exec_open_task.go 承载 Task/Subagent 工具域：Task/Fetch/RecordScreen/ComputerUse 的 open 请求构造与 subagent 模型解析。
package execbridge

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"


	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/modelchannel"
)

func (bridge *Bridge) openTask(openContext OpenExecContext, toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	args, err := decodeArgsMap(toolCall.ArgsJSON)
	if err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode Task args failed: %w", err)
	}
	subagentType := strings.TrimSpace(readStringArg(args, "subagent_type", "subagentType"))
	if subagentType == "" {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("task subagent_type is required")
	}
	capability, err := runtimecore.ResolveTaskSubagentCapabilityFromArgs(args)
	if err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("invalid Task authorization: %w", err)
	}
	messageID := bridge.nextID()
	now := time.Now().UTC()
	execID := fmt.Sprintf("exec-subagent-%d", now.UnixNano())
	readonly := capability.Readonly
	runInBackground := readBoolArg(args, "run_in_background", "runInBackground")
	interrupt := readBoolArg(args, "interrupt")
	parentConversationID := strings.TrimSpace(openContext.ConversationID)
	rootParentConversationID := strings.TrimSpace(openContext.RootConversationID)
	workspaceHint := strings.TrimSpace(openContext.WorkspaceHint)
	taskRequestedModelID := strings.TrimSpace(readStringArg(args, "model", "model_id", "modelId"))
	modelID := taskRequestedModelID
	// Cursor emits fast/default/auto for a Task that should inherit its parent
	// model. Leaving the alias intact makes the channel resolver select the first
	// configured adapter, which can send child tasks to an unrelated account.
	if modelchannel.IsMetaModelAlias(modelID) {
		modelID = strings.TrimSpace(openContext.ModelID)
	}
	if override, _, ok := runtimecore.LookupSubagentModelOverride(openContext.SubagentModelOverrides, subagentType); ok {
		switch strings.TrimSpace(override.Selection) {
		case "disabled":
			return nil, runtimecore.PendingExec{}, fmt.Errorf("subagent type %q is disabled by model override", subagentType)
		case "model":
			modelID = strings.TrimSpace(override.ModelID)
		case "inherit":
			modelID = strings.TrimSpace(openContext.ModelID)
		}
	}
	if modelID == "" {
		for _, selected := range openContext.SelectedSubagentModels {
			if selected != nil && strings.TrimSpace(selected.GetModelId()) != "" {
				modelID = resolveSelectedSubagentModelID(selected.GetModelId(), openContext.SelectedSubagentModelDetails)
				break
			}
		}
	}
	if modelID == "" {
		modelID = strings.TrimSpace(openContext.ModelID)
	}
	selectedContext := buildTaskSelectedContext(workspaceHint)
	directMetaParentChild := openContext.DirectMetaParentChild
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_SubagentArgs{
					SubagentArgs: &agentv1.SubagentArgs{
						ToolCallId:                    toolCall.CallID,
						SubagentType:                  subagentType,
						ModelId:                       modelID,
						Prompt:                        augmentSubagentPrompt(readStringArg(args, "prompt"), readStringArg(args, "description")),
						Readonly:                      readonly,
						ResumeAgentId:                 stringPtr(strings.TrimSpace(readStringArg(args, "resume"))),
						RunInBackground:               boolPtrIfTrue(runInBackground),
						ParentConversationId:          stringPtrIfNonEmpty(parentConversationID),
						Interrupt:                     boolPtrIfTrue(interrupt),
						ForkAgentId:                   stringPtrIfNonEmpty(strings.TrimSpace(readStringArg(args, "fork_agent_id", "forkAgentId"))),
						RootParentConversationId:      stringPtrIfNonEmpty(firstNonEmptyString(rootParentConversationID, parentConversationID)),
						Mode:                          taskModeFromReadonly(readonly),
						SelectedContext:               selectedContext,
						DirectMetaParentChildSubagent: boolPtrIfTrue(directMetaParentChild),
						Environment:                   agentv1.SubagentExecutionEnvironment_SUBAGENT_EXECUTION_ENVIRONMENT_LOCAL,
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
		ExecKind:    "subagent",
		StreamState: "opened",
		OpenedAt:    now,
	}, nil
}

// augmentSubagentPrompt 要求子代理输出面向用户的工作摘要，避免界面只显示连续工具调用。
// 这不是要求暴露隐藏思维链；模型只需给出简短计划、阶段发现和最终结论。
func augmentSubagentPrompt(prompt string, description string) string {
	prompt = strings.TrimSpace(prompt)
	description = strings.TrimSpace(description)
	parts := []string{
		"请把工作过程中的可见进度写给用户：开始执行前先用1-2句话说明任务目标、检查范围和下一步；每完成一组关键检查后，用一句话总结当前发现和下一步；结束时给出简洁结论。不要输出隐藏思维链，只输出面向用户的工作摘要，然后再调用工具。",
	}
	if description != "" {
		parts = append(parts, "任务简述："+description)
	}
	if prompt != "" {
		parts = append(parts, "具体任务："+prompt)
	}
	return strings.Join(parts, "\n\n")
}

// openFetch 构造 Fetch 对应的执行桥请求。
func (bridge *Bridge) openFetch(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(toolCall.ArgsJSON, &args); err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode Fetch args failed: %w", err)
	}
	if strings.TrimSpace(args.URL) == "" {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("Fetch url is required")
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-fetch-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_FetchArgs{
					FetchArgs: &agentv1.FetchArgs{
						Url:        strings.TrimSpace(args.URL),
						ToolCallId: toolCall.CallID,
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
		ExecKind:    "fetch",
		StreamState: "opened",
		OpenedAt:    time.Now().UTC(),
	}, nil
}

// openRecordScreen 构造 RecordScreen 对应的执行桥请求。
func (bridge *Bridge) openRecordScreen(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	var args struct {
		Mode           string `json:"mode"`
		SaveAsFilename string `json:"save_as_filename"`
		SaveAsFileName string `json:"saveAsFilename"`
	}
	if err := json.Unmarshal(toolCall.ArgsJSON, &args); err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode RecordScreen args failed: %w", err)
	}
	mode := recordingModeFromString(args.Mode)
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-record-screen-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_RecordScreenArgs{
					RecordScreenArgs: &agentv1.RecordScreenArgs{
						Mode:           mode,
						ToolCallId:     toolCall.CallID,
						SaveAsFilename: stringPtrIfNonEmpty(firstNonEmptyString(args.SaveAsFilename, args.SaveAsFileName)),
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
		ExecKind:    "record_screen",
		StreamState: "opened",
		OpenedAt:    time.Now().UTC(),
	}, nil
}

// openComputerUse 构造 ComputerUse 对应的执行桥请求。
func (bridge *Bridge) openComputerUse(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	actions, err := decodeComputerUseActions(toolCall.ArgsJSON)
	if err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode ComputerUse args failed: %w", err)
	}
	if len(actions) == 0 {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("ComputerUse actions are required")
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-computer-use-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_ComputerUseArgs{
					ComputerUseArgs: &agentv1.ComputerUseArgs{
						ToolCallId: toolCall.CallID,
						Actions:    actions,
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
		ExecKind:    "computer_use",
		StreamState: "opened",
		OpenedAt:    time.Now().UTC(),
	}, nil
}

// openForceBackgroundSubagent 构造 ForceBackgroundSubagent 对应的执行桥请求。
func (bridge *Bridge) openForceBackgroundSubagent(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	targetToolCallID := strings.TrimSpace(readJSONStringArg(toolCall.ArgsJSON, "tool_call_id", "toolCallId"))
	if targetToolCallID == "" {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("ForceBackgroundSubagent tool_call_id is required")
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-force-background-subagent-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_ForceBackgroundSubagentArgs{
					ForceBackgroundSubagentArgs: &agentv1.ForceBackgroundSubagentArgs{
						ToolCallId: targetToolCallID,
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
		ExecKind:    "force_background_subagent",
		StreamState: "opened",
		OpenedAt:    time.Now().UTC(),
	}, nil
}

func resolveSelectedSubagentModelID(selectedModelID string, details []*agentv1.ModelDetails) string {
	selected := strings.TrimSpace(selectedModelID)
	for _, detail := range details {
		if detail == nil {
			continue
		}
		modelID := strings.TrimSpace(detail.GetModelId())
		displayID := strings.TrimSpace(detail.GetDisplayModelId())
		if selected == modelID || selected == displayID {
			return firstNonEmptyString(modelID, selected)
		}
	}
	return selected
}

func buildTaskSelectedContext(workspaceHint string) *agentv1.SelectedContext {
	workspaceHint = strings.TrimSpace(workspaceHint)
	if workspaceHint == "" {
		return nil
	}
	return &agentv1.SelectedContext{
		Folders: []*agentv1.SelectedFolder{
			{
				Path: workspaceHint,
			},
		},
	}
}

func boolPtrIfTrue(value bool) *bool {
	if !value {
		return nil
	}
	copy := value
	return &copy
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
