// preflight_forced_compaction.go 处理 provider 请求前预算校验（preflight）超限时的最后一搏：
// 自动执行一次 /summarize 式 legacy 压缩（覆盖所有非当前轮次并 ReplaceEntries 重写 canonical），
// 而不是直接以 context_overflow_after_compaction 终态失败。
//
// 背景：自动/强制压缩链（context projection sidecar）结构性无法压缩 head 历史或被冻结的
// covered 区域（只有 recent tail 可压）；当 covered 冻结、估算涨过预算后投影 builder 找不到
// 可压缩轮次，只能返回 overflow 终态错误。手动 /summarize 有效是因为 legacy 路径压缩所有
// 非当前轮次并真正重写 canonical。本模块把这条路径作为每回合最多一次
// （preflightForcedCompactionMaxAttempts）的兜底，压缩完成后 resume 继续原请求。
package forwarder

import (
	"context"
	"strings"
	"time"

	"cursor/internal/logger"
)

// compactionPreflightForcedTrigger 是强制兜底压缩的 trigger。
// 必须 != "manual"（handleCompactionEvent 只对 manual 终结回合、压缩后不 resume）
// 且 != contextProjectionTrigger（beginPendingCompaction 对 projection 走进程内摘要、
// 不走 pre-compact hook）。
const compactionPreflightForcedTrigger = "preflight_forced"

// preflightForcedCompactionMaxAttempts 是单回合 preflight 兜底 legacy 压缩的最大次数。
// legacy 压缩会把所有非当前轮次折叠成一条摘要，一次即可压到最小；再压也压不动，
// 因此 1 次足够，之后仍超限就按原样报终态错误。
const preflightForcedCompactionMaxAttempts = 1

// buildLegacyCompactionPlanBase 构造 legacy 压缩计划的公共 base（窗口/基线/用量/当前轮字段）。
// 供 buildManualCompactionPlan（/summarize）与 buildForcedPreflightCompactionPlan（自动兜底）
// 复用，保证两条路径的压缩语义完全一致。
func (service *Service) buildLegacyCompactionPlanBase(stream *ActiveStream, conversation *ConversationFile, compiled CompiledConversation, trigger string, preserveCurrentTurnInputs bool, manualInstruction string) (*compactionPlan, error) {
	if stream == nil || conversation == nil {
		return nil, nil
	}
	contextWindowSize := compactionContextWindowSize(conversation)
	if contextWindowSize <= 0 {
		return nil, nil
	}
	contextTokens, err := service.resolveCompactionBaselineTokens(stream.ConversationID, compiled, conversation)
	if err != nil {
		return nil, err
	}
	if contextTokens <= 0 {
		return nil, nil
	}
	usagePercent := 0.0
	if contextWindowSize > 0 {
		usagePercent = float64(contextTokens) / float64(contextWindowSize)
	}
	return &compactionPlan{
		Trigger:                   trigger,
		ContextTokens:             contextTokens,
		ContextWindowSize:         contextWindowSize,
		ContextUsagePercent:       usagePercent,
		ReserveTokens:             compactionAutoReserveTokens,
		MessageCount:              clampInt64ToInt32(int64(len(compiled.Messages))),
		IsFirstCompaction:         len(compactionSummaryTexts(conversation)) == 0,
		ExistingSummary:           existingConversationSummaryText(conversation),
		ManualInstruction:         strings.TrimSpace(manualInstruction),
		CurrentTurnSeq:            stream.TurnSeq,
		CurrentRequestID:          strings.TrimSpace(stream.RequestID),
		CurrentUserText:           strings.TrimSpace(stream.LatestUserText),
		PreserveCurrentTurnInputs: preserveCurrentTurnInputs,
	}, nil
}

// buildForcedPreflightCompactionPlan 构建「preflight 超限兜底」的 legacy 压缩计划：
// 与 /summarize 的 buildManualCompactionPlan 同一条 buildLegacyCompactionPlan 路径
// （压缩所有非当前轮次），但 trigger 为 preflight_forced（压缩后 resume 而非终结回合），
// 且 PreserveCurrentTurnInputs=true（重建 canonical 时保留用户当前问题）。
func (service *Service) buildForcedPreflightCompactionPlan(stream *ActiveStream, conversation *ConversationFile, compiled CompiledConversation) (*compactionPlan, error) {
	base, err := service.buildLegacyCompactionPlanBase(stream, conversation, compiled, compactionPreflightForcedTrigger, true, "")
	if err != nil || base == nil {
		return base, err
	}
	return service.buildLegacyCompactionPlan(base, conversation, true, 0)
}

// escalateForcedPreflightCompaction 在 driveProvider 即将以 context_overflow_after_compaction
// 终态失败时，尝试执行一次强制 legacy 兜底压缩。
// 返回 (true, nil) 表示已挂起压缩（调用方应设置阶段并结束本轮 driveProvider，等待
// handleCompactionEvent 完成后 resume 重跑）；
// 返回 (false, nil) 表示不需要/不允许/无可压缩内容，调用方按原错误走失败路径。
func (service *Service) escalateForcedPreflightCompaction(stream *ActiveStream, conversation *ConversationFile, compiled CompiledConversation, cause error) (bool, error) {
	if service == nil || stream == nil || conversation == nil || cause == nil {
		return false, nil
	}
	if resolveTerminalCode("unknown", cause) != compactionOverflowTerminalCode {
		return false, nil
	}
	_, manualCompactionRequested := streamManualCompactionDirective(stream)
	if manualCompactionRequested {
		return false, nil
	}
	stream.mu.Lock()
	attempts := stream.PreflightForcedCompactionAttempts
	pendingCompaction := stream.PendingCompaction != nil
	stream.mu.Unlock()
	if pendingCompaction || attempts >= preflightForcedCompactionMaxAttempts {
		return false, nil
	}
	plan, err := service.buildForcedPreflightCompactionPlan(stream, conversation, compiled)
	if err != nil {
		return false, err
	}
	if plan == nil {
		return false, nil
	}
	stream.mu.Lock()
	stream.PreflightForcedCompactionAttempts++
	newAttempts := stream.PreflightForcedCompactionAttempts
	stream.PendingProviderAction = providerActionNone
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()

	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "preflight_forced_compaction_triggered", map[string]any{
			"attempt":             newAttempts,
			"max_attempts":        preflightForcedCompactionMaxAttempts,
			"trigger":             plan.Trigger,
			"context_tokens":      plan.ContextTokens,
			"context_window":      plan.ContextWindowSize,
			"messages_to_compact": plan.MessagesToCompact,
		})
	}
	logger.Infof("forwarder preflight forced compaction trigger request_id=%s attempt=%d/%d messages_to_compact=%d",
		strings.TrimSpace(stream.RequestID), newAttempts, preflightForcedCompactionMaxAttempts, plan.MessagesToCompact)

	if err := service.beginPendingCompaction(stream, plan); err != nil {
		return false, err
	}
	return true, nil
}

// finalizeCompactionAdmission 完成 driveProvider 内压缩准入后的阶段迁移：
// 依据终态状态 / 是否存在挂起压缩设置回合阶段。与 maybeCompactBeforeProvider 返回
// compacted=true 时的处理共用同一逻辑。
func (service *Service) finalizeCompactionAdmission(stream *ActiveStream) {
	if service == nil || stream == nil {
		return
	}
	stream.mu.Lock()
	stream.UpdatedAt = time.Now().UTC()
	hasPendingCompaction := stream.PendingCompaction != nil
	status := stream.Status
	stream.mu.Unlock()
	switch {
	case isTerminalStreamStatus(status):
		switch status {
		case StreamStatusCompleted:
			service.setTurnPhase(stream, TurnPhaseCompleted)
		case StreamStatusCanceled:
			service.setTurnPhase(stream, TurnPhaseCanceled)
		default:
			service.setTurnPhase(stream, TurnPhaseFailed)
		}
	case hasPendingCompaction:
		service.setTurnPhase(stream, TurnPhaseCompacting)
	default:
		service.setTurnPhase(stream, TurnPhaseIdle)
	}
}
