// exec_subagent_await.go 承载 SubagentAwait 工具：模型用 agent_id 轮询分离（detached）
// 子代理的终态。请求经 exec bridge 透传给客户端，客户端返回 complete/not_found/error/
// still_running；still_running 不是终态，模型需续租继续等待。
package execbridge

import (
	"fmt"
	"strings"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

type subagentAwaitArgs struct {
	AgentID   string
	TimeoutMS uint32
}

func decodeSubagentAwaitArgs(raw []byte) (subagentAwaitArgs, error) {
	args, err := decodeArgsMap(raw)
	if err != nil {
		return subagentAwaitArgs{}, err
	}
	result := subagentAwaitArgs{
		AgentID:   strings.TrimSpace(readStringArg(args, "agent_id", "agentId", "task_id", "taskId")),
		TimeoutMS: 30000,
	}
	if result.AgentID == "" {
		return result, fmt.Errorf("SubagentAwait agent_id is required")
	}
	if value, found, err := runtimecore.ReadInt64Arg(args, "timeout_ms", "timeoutMs", "block_until_ms", "blockUntilMs"); err != nil {
		return result, err
	} else if found {
		if value < 0 || value > int64(^uint32(0)) {
			return result, fmt.Errorf("SubagentAwait timeout_ms is out of range")
		}
		result.TimeoutMS = uint32(value)
	}
	return result, nil
}

// openSubagentAwait 构造 SubagentAwait 对应的流式执行桥请求。
func (bridge *Bridge) openSubagentAwait(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	args, err := decodeSubagentAwaitArgs(toolCall.ArgsJSON)
	if err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode SubagentAwait args failed: %w", err)
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-subagent-await-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_SubagentAwaitArgs{
					SubagentAwaitArgs: &agentv1.SubagentAwaitArgs{
						AgentId:   args.AgentID,
						TimeoutMs: args.TimeoutMS,
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
		ExecKind:    "subagent_await",
		StreamState: "opened",
		OpenedAt:    time.Now().UTC(),
	}, nil
}

// ConvertSubagentAwaitResult 将新版 SubagentAwait 终态映射到既有 Task finalization 语义。
// still_running 返回 nil，调用方只能续租/继续等待，不能把它当作子代理结果。
func ConvertSubagentAwaitResult(result *agentv1.SubagentAwaitResult) *agentv1.SubagentResult {
	if result == nil {
		return nil
	}
	if complete := result.GetComplete(); complete != nil {
		finalMessage := complete.GetFinalMessage()
		return &agentv1.SubagentResult{Result: &agentv1.SubagentResult_Success{
			Success: &agentv1.SubagentSuccess{
				AgentId:        complete.GetAgentId(),
				FinalMessage:   stringPtr(finalMessage),
				TranscriptPath: complete.TranscriptPath,
			},
		}}
	}
	if notFound := result.GetNotFound(); notFound != nil {
		agentID := notFound.GetAgentId()
		return &agentv1.SubagentResult{Result: &agentv1.SubagentResult_Error{
			Error: &agentv1.SubagentError{
				AgentId: &agentID,
				Error:   "subagent not found",
			},
		}}
	}
	if failure := result.GetError(); failure != nil {
		agentID := failure.GetAgentId()
		return &agentv1.SubagentResult{Result: &agentv1.SubagentResult_Error{
			Error: &agentv1.SubagentError{
				AgentId: &agentID,
				Error:   failure.GetError(),
			},
		}}
	}
	return nil
}

func summarizeSubagentAwaitResult(result *agentv1.SubagentAwaitResult) string {
	if result == nil {
		return "subagent await result missing"
	}
	if stillRunning := result.GetStillRunning(); stillRunning != nil {
		return fmt.Sprintf("subagent still running agent_id=%s", strings.TrimSpace(stillRunning.GetAgentId()))
	}
	if complete := result.GetComplete(); complete != nil {
		if text := strings.TrimSpace(complete.GetFinalMessage()); text != "" {
			return text
		}
		return fmt.Sprintf("subagent completed agent_id=%s", strings.TrimSpace(complete.GetAgentId()))
	}
	if notFound := result.GetNotFound(); notFound != nil {
		return fmt.Sprintf("subagent not found agent_id=%s", strings.TrimSpace(notFound.GetAgentId()))
	}
	if failure := result.GetError(); failure != nil {
		if text := strings.TrimSpace(failure.GetError()); text != "" {
			return text
		}
		return "subagent await failed"
	}
	return "unknown subagent await result"
}
