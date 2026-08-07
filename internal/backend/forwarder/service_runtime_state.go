// service_runtime_state.go 承载 checkpoint/运行状态域：委派运行状态同步（delegation_aggregate 进度上屏）、
// token 展示口径（max/used/breakdown）与 checkpoint 编译缓存。
package forwarder

import (
	"context"
	"strings"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/logger"
)

// attachDelegationRunStates 把本地委派（delegation_aggregate）的运行状态写入
// checkpoint 的 subagent_runs_by_parent_tool_call_id。Cursor 客户端在
// conversation_checkpoint_update 到达时按 toolCallId 查该 map 渲染 Task 卡片：
// 有记录时按 status 显示 running/succeeded/error/aborted；无记录时不更新
// （bubble 创建失败或本地委派场景下卡片会停留在 stopped）。byok 本地委派
// 必须通过该字段把真实进度同步给客户端，保证 UI 与 byok 实际状态一致。
func attachDelegationRunStates(stream *ActiveStream, state *agentv1.ConversationStateStructure) {
	if stream == nil || state == nil {
		return
	}
	stream.mu.Lock()
	runs := make(map[string]*agentv1.SubagentRunState)
	for _, pending := range stream.PendingExecs {
		execKind := strings.TrimSpace(pending.ExecKind)
		if execKind != "delegation_aggregate" && execKind != "subagent" {
			continue
		}
		toolCallID := strings.TrimSpace(pending.ToolCallID)
		if toolCallID == "" {
			continue
		}
		title := "委派任务"
		if execKind == "subagent" {
			title = "Cursor 子代理"
		}
		runs[toolCallID] = &agentv1.SubagentRunState{
			ParentToolCallId: toolCallID,
			SubagentId:       stringPtr(delegationSubagentID(toolCallID)),
			Environment:      agentv1.SubagentExecutionEnvironment_SUBAGENT_EXECUTION_ENVIRONMENT_LOCAL,
			Status:           agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_RUNNING,
			Title:            stringPtr(title),
		}
	}
	for toolCallID, terminal := range stream.DelegationRunTerminals {
		if terminal != nil && strings.TrimSpace(toolCallID) != "" {
			runs[toolCallID] = terminal
		}
	}
	stream.mu.Unlock()
	if len(runs) > 0 {
		state.SubagentRunsByParentToolCallId = runs
	}
}

// recordDelegationRunTerminal 在委派收尾时记录终态 SubagentRun，供后续
// checkpoint 同步给 Cursor 客户端（UI 从 stopped 恢复为 succeeded/error）。
func (service *Service) recordDelegationRunTerminal(stream *ActiveStream, pending runtimecore.PendingExec, status agentv1.SubagentRunStatus, title string, detail string) {
	if stream == nil || strings.TrimSpace(pending.ToolCallID) == "" {
		return
	}
	now := uint64(time.Now().UTC().UnixMilli())
	completionReason := agentv1.BackgroundTaskCompletionReason_BACKGROUND_TASK_COMPLETION_REASON_TASK_FINISHED
	run := &agentv1.SubagentRunState{
		ParentToolCallId:     strings.TrimSpace(pending.ToolCallID),
		SubagentId:           stringPtr(delegationSubagentID(pending.ToolCallID)),
		Environment:          agentv1.SubagentExecutionEnvironment_SUBAGENT_EXECUTION_ENVIRONMENT_LOCAL,
		Status:               status,
		Title:                stringPtr(strings.TrimSpace(title)),
		CompletedTimestampMs: &now,
		CompletionReason:     &completionReason,
	}
	if detail := strings.TrimSpace(detail); detail != "" {
		run.Detail = stringPtr(detail)
	}
	stream.mu.Lock()
	if stream.DelegationRunTerminals == nil {
		stream.DelegationRunTerminals = make(map[string]*agentv1.SubagentRunState)
	}
	stream.DelegationRunTerminals[strings.TrimSpace(pending.ToolCallID)] = run
	stream.mu.Unlock()
	logger.Infof("forwarder delegation run terminal recorded request_id=%s conversation_id=%s exec_id=%s tool_call_id=%s status=%s completion_reason=%s detail=%q",
		strings.TrimSpace(stream.RequestID), strings.TrimSpace(stream.ConversationID), strings.TrimSpace(pending.ExecID), strings.TrimSpace(pending.ToolCallID), status.String(), completionReason.String(), strings.TrimSpace(detail))
	if service != nil && service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "delegation_run_terminal_recorded", map[string]any{
			"exec_id":           strings.TrimSpace(pending.ExecID),
			"tool_call_id":      strings.TrimSpace(pending.ToolCallID),
			"status":            status.String(),
			"completion_reason": completionReason.String(),
			"detail":            strings.TrimSpace(detail),
		})
	}
}

func (service *Service) rewriteCheckpointTokenDetailsForClient(stream *ActiveStream, conversation *ConversationFile, state *agentv1.ConversationStateStructure) {
	if state == nil {
		return
	}
	if state.TokenDetails == nil {
		state.TokenDetails = &agentv1.ConversationTokenDetails{}
	}
	state.TokenDetails.MaxTokens = clampInt64ToUint32(service.checkpointDisplayMaxTokens(stream, conversation))
	compiled, hasCompiled := service.checkpointCompiledConversation(stream, conversation)
	state.TokenDetails.UsedTokens = clampInt64ToUint32(service.checkpointDisplayUsedTokens(conversation, state, compiled, hasCompiled))
	state.TokenDetails.Breakdown = estimateCheckpointPromptTokenBreakdown(compiled, hasCompiled, state.TokenDetails.UsedTokens, state.TokenDetails.MaxTokens)
}

func (service *Service) checkpointCompiledConversation(stream *ActiveStream, conversation *ConversationFile) (CompiledConversation, bool) {
	if service == nil || service.compiler == nil || conversation == nil {
		return CompiledConversation{}, false
	}
	_, modelName, latestUserText, mode := checkpointPromptContext(stream)
	compiled, err := service.compiler.Compile(conversation, mode, latestUserText, modelName, stream.CustomSystemPrompt)
	if err != nil {
		logger.Errorf("forwarder checkpoint token estimate failed request_id=%s conversation_id=%s err=%v", strings.TrimSpace(activeStreamRequestID(stream)), strings.TrimSpace(conversation.ConversationID), err)
		return CompiledConversation{}, false
	}
	return guardCompiledConversationForProvider(compiled), true
}

func (service *Service) checkpointDisplayMaxTokens(stream *ActiveStream, conversation *ConversationFile) int64 {
	_ = stream
	maxTokens := int64(conversationTokenDetailsMaxTokens(conversation))
	if maxTokens < 1 {
		return 1
	}
	return maxTokens
}

func (service *Service) checkpointDisplayUsedTokens(conversation *ConversationFile, state *agentv1.ConversationStateStructure, compiled CompiledConversation, hasCompiled bool) int64 {
	usedTokens := int64(0)
	if state != nil && state.TokenDetails != nil {
		usedTokens = int64(state.TokenDetails.GetUsedTokens())
	}
	if conversation != nil && int64(conversation.TokenDetailsUsedTokens) > usedTokens {
		usedTokens = int64(conversation.TokenDetailsUsedTokens)
	}
	if hasCompiled {
		if estimatedTokens := estimateCompiledPromptTokens(compiled); estimatedTokens > usedTokens {
			usedTokens = estimatedTokens
		}
	}
	return usedTokens
}

func checkpointPromptContext(stream *ActiveStream) (string, string, string, agentv1.AgentMode) {
	if stream == nil {
		return "", "", "", agentv1.AgentMode_AGENT_MODE_AGENT
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.ModelID, stream.ModelName, stream.LatestUserText, stream.Mode
}

func activeStreamRequestID(stream *ActiveStream) string {
	if stream == nil {
		return ""
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.RequestID
}

