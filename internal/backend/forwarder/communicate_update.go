package forwarder

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

// updateCurrentStepToolName 是 ToolCall.communicate_update_tool_call 对应的模型工具名。
// 官方抓包里这条 arm 只出现在 Task 子会话的 RunSSE 流上（4 个子代理共 12 次调用），
// 承担的是父会话 Task 卡片上的「当前步骤 / 完成副标题 / 最终总结」展示，
// 不是内部记账。
const updateCurrentStepToolName = "UpdateCurrentStep"

// decodeUpdateCurrentStepArgs 解析 UpdateCurrentStep 参数。三个字段都是字符串，
// 同时接受 snake_case（官方线上写法）与 camelCase 别名。current_step 为必填：
// 客户端拿它当 Task 卡片的实时标签，空值会让卡片退回泛化文案。
func decodeUpdateCurrentStepArgs(raw []byte) (*agentv1.CommunicateUpdateArgs, error) {
	decoded, err := runtimecore.DecodeArgsMap(raw)
	if err != nil {
		return nil, err
	}
	currentStep := strings.TrimSpace(runtimecore.ReadStringArg(decoded, "current_step", "currentStep"))
	if currentStep == "" {
		return nil, &runtimecore.InvalidToolArgumentsError{
			Err: errors.New("UpdateCurrentStep requires a non-empty current_step"),
		}
	}
	args := &agentv1.CommunicateUpdateArgs{CurrentStep: stringPtr(currentStep)}
	if summary := strings.TrimSpace(runtimecore.ReadStringArg(decoded, "final_summary", "finalSummary")); summary != "" {
		args.FinalSummary = stringPtr(summary)
	}
	if subtitle := strings.TrimSpace(runtimecore.ReadStringArg(decoded, "completed_subtitle", "completedSubtitle")); subtitle != "" {
		args.CompletedSubtitle = stringPtr(subtitle)
	}
	return args, nil
}

// buildUpdateCurrentStepSuccessResult 构造成功结果与面向模型的文本。
// 文本状态行放在开头：工具结果整体截断砍的是结尾。
func buildUpdateCurrentStepSuccessResult(currentStep string, messageIndex uint32) (*agentv1.CommunicateUpdateResult, string) {
	step := strings.TrimSpace(currentStep)
	result := &agentv1.CommunicateUpdateResult{
		Result: &agentv1.CommunicateUpdateResult_Success{
			Success: &agentv1.CommunicateUpdateSuccess{
				CurrentStep:  step,
				MessageIndex: messageIndex,
			},
		},
	}
	return result, "progress recorded: " + step
}

func buildUpdateCurrentStepErrorResult(message string) (*agentv1.CommunicateUpdateResult, string) {
	text := strings.TrimSpace(message)
	if text == "" {
		text = "progress update failed"
	}
	return &agentv1.CommunicateUpdateResult{
		Result: &agentv1.CommunicateUpdateResult_Error{
			Error: &agentv1.CommunicateUpdateError{Error: text},
		},
	}, "progress update rejected: " + text
}

func buildUpdateCurrentStepToolCall(args *agentv1.CommunicateUpdateArgs, result *agentv1.CommunicateUpdateResult) *agentv1.ToolCall {
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_CommunicateUpdateToolCall{
			CommunicateUpdateToolCall: &agentv1.CommunicateUpdateToolCall{
				Args:   args,
				Result: result,
			},
		},
	}
}

// handleUpdateCurrentStepToolInvocation 就地完成 UpdateCurrentStep。
// 与 send_final_summary 相反，这是非终结工具：记录进度后模型继续本轮工作，
// 因此不调用 markProviderTerminalToolInvocation。
func (service *Service) handleUpdateCurrentStepToolInvocation(stream *ActiveStream, invocation runtimecore.ToolInvocation) error {
	args, err := decodeUpdateCurrentStepArgs(invocation.ArgsJSON)
	if err != nil {
		result, payload := buildUpdateCurrentStepErrorResult(err.Error())
		return service.completeImmediateToolResult(stream, invocation, payload, buildUpdateCurrentStepToolCall(&agentv1.CommunicateUpdateArgs{}, result))
	}
	result, payload := buildUpdateCurrentStepSuccessResult(args.GetCurrentStep(), nextCommunicateUpdateMessageIndex(stream))
	return service.completeImmediateToolResult(stream, invocation, payload, buildUpdateCurrentStepToolCall(args, result))
}

// nextCommunicateUpdateMessageIndex 复刻官方 message_index 的语义：子会话历史里
// 当前进度所处的条目序号。客户端按最大 message_index 挑「当前步骤」，只要求单调递增。
func nextCommunicateUpdateMessageIndex(stream *ActiveStream) uint32 {
	if stream == nil {
		return 0
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.CheckpointConversation == nil {
		return 0
	}
	return uint32(len(stream.CheckpointConversation.Entries))
}

// communicateUpdateStateKey 复刻官方 map key 写法：tool_call_id 内部含换行
//（"call-…-N\nfc_…_N"），服务端写进 key 时把换行剥掉。
func communicateUpdateStateKey(parentToolCallID string) string {
	return strings.ReplaceAll(strings.TrimSpace(parentToolCallID), "\n", "")
}

// projectCommunicateUpdateTurnState 从子会话历史里重建 Task 卡片进度状态。
// 官方只在子会话自己的 checkpoint 上带这张表，key 是父会话的 Task tool_call_id，
// history 累积全部步骤，final_summary / completed_subtitle 取最后一次带值的调用。
func projectCommunicateUpdateTurnState(conversation *ConversationFile) *agentv1.CommunicateUpdateTurnState {
	if conversation == nil {
		return nil
	}
	state := &agentv1.CommunicateUpdateTurnState{}
	for _, entry := range conversation.Entries {
		if strings.TrimSpace(entry.Kind) != "tool_result" {
			continue
		}
		toolCall := decodeCommunicateUpdateEntryToolCall(entry)
		if toolCall == nil {
			continue
		}
		args := toolCall.GetArgs()
		step := strings.TrimSpace(args.GetCurrentStep())
		if step == "" {
			continue
		}
		state.History = append(state.History, &agentv1.CommunicateUpdateHistoryEntry{
			Step:         step,
			MessageIndex: toolCall.GetResult().GetSuccess().GetMessageIndex(),
		})
		if summary := strings.TrimSpace(args.GetFinalSummary()); summary != "" {
			state.FinalSummary = stringPtr(summary)
		}
		if subtitle := strings.TrimSpace(args.GetCompletedSubtitle()); subtitle != "" {
			state.CompletedSubtitle = stringPtr(subtitle)
		}
	}
	if len(state.History) == 0 {
		return nil
	}
	return state
}

// updateCurrentStepEntryMarker 让投影在完整解码前先廉价排除其它工具结果：
// checkpoint 每次发布都要遍历全量历史，而工具结果正文可能非常大。
var updateCurrentStepEntryMarker = []byte(`"tool_name":"` + updateCurrentStepToolName + `"`)

func decodeCommunicateUpdateEntryToolCall(entry HistoryEntry) *agentv1.CommunicateUpdateToolCall {
	if !bytes.Contains(entry.Payload, updateCurrentStepEntryMarker) {
		return nil
	}
	var payload toolResultEntryPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		return nil
	}
	if strings.TrimSpace(payload.ToolName) != updateCurrentStepToolName || len(payload.ToolCall) == 0 {
		return nil
	}
	toolCall := &agentv1.ToolCall{}
	if err := protojson.Unmarshal(payload.ToolCall, toolCall); err != nil {
		return nil
	}
	return toolCall.GetCommunicateUpdateToolCall()
}
