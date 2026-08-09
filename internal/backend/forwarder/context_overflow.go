// context_overflow.go 处理 provider 返回 context_length_exceeded 时的自救：
// 强制压缩上下文并重试 provider，避免因 contextWindowTokens 配置偏大（或模型真实窗口小于配置）
// 导致整轮直接失败、任务无法继续。每轮重试次数有上限（contextOverflowMaxCompactionRetries），
// 防止「压不动」时无限循环。
package forwarder

import (
	"context"
	"strings"
	"time"

	"cursor/internal/logger"
)

// contextOverflowMaxCompactionRetries 是单轮回合因 context_length_exceeded 触发强制压缩重试的次数上限。
const contextOverflowMaxCompactionRetries = 2

// contextWindowHalveFloor 是「遇错减半」上下文窗口的最低下限。
// 中转站限制自适应（遇 context_too_large 时减半）到此值后不再减半，避免上下文过小导致模型不可用。
const contextWindowHalveFloor = 32_000

// delegatedWindowLearnFactor 是 delegated worker 溢出时窗口收敛系数：
// 新窗口 = 失败发送量 × 该系数（下限 contextWindowHalveFloor）。
// 失败发送量本身已超窗（估算口径），按 0.75 收敛给 tokenizer 估算偏差留余量，
// 使下一次任务的首次预算即可落在真实窗口内。
const delegatedWindowLearnFactor = 0.75

// recoverFromContextOverflow 在 provider 返回 context_length_exceeded 时尝试强制压缩并重试。
// 返回 (recovered=true, nil) 表示已成功挂起压缩流程（压缩完成后会自动 resume provider）；
// 返回 (false, nil) 表示已达重试上限或无可压缩内容，交给调用方走正常失败路径。
func (service *Service) recoverFromContextOverflow(stream *ActiveStream, conversationID string, requestID string, accumulatedText string, accumulatedReasoning string) (bool, error) {
	if service == nil || stream == nil {
		return false, nil
	}
	stream.mu.Lock()
	attempts := stream.ContextOverflowCompactionAttempts
	modelID := stream.ModelID
	stream.mu.Unlock()
	if attempts >= contextOverflowMaxCompactionRetries {
		logger.Infof("forwarder context overflow recovery exhausted request_id=%s attempts=%d",
			strings.TrimSpace(requestID), attempts)
		return false, nil
	}

	// 中转站上下文自适应：context_too_large 往往说明 catalog 理论窗口（如 gpt-5.6 的 1M）
	// 大于中转站实际分配的窗口（如 Codex 的 272K）。在压缩之前先把该渠道的 contextWindowTokens
	// 减半并持久化（仅下调、按 channelID 修正该用户的该模型条目，不影响全局），
	// 这样后续压缩用的预算也是修正后的值，避免反复触发同样的超限。
	service.maybeHalveContextWindowForOverflow(stream, conversationID, requestID, modelID)

	// 重新快照会话并编译，得到与即将压缩一致的 compiled 视图。
	conversation, _, _, err := service.snapshotCheckpointConversation(stream)
	if err != nil {
		return false, err
	}
	stream.mu.Lock()
	mode := stream.Mode
	modelName := stream.ModelName
	latestUserText := stream.LatestUserText
	stream.mu.Unlock()
	conversation, err = service.syncConversationContextWindowTokens(stream, conversationID, conversation)
	if err != nil {
		return false, err
	}
	compiled, err := service.compiler.Compile(conversation, mode, latestUserText, modelName, stream.CustomSystemPrompt, stream.Goal != nil)
	if err != nil {
		return false, err
	}
	compiled = guardCompiledConversationForProvider(compiled)

	plan, err := service.buildForcedCompactionPlan(stream, conversation, compiled)
	if err != nil {
		return false, err
	}
	if plan == nil {
		// 已无可压缩内容：再压也无意义，交给正常失败路径。
		logger.Infof("forwarder context overflow recovery skipped (nothing to compact) request_id=%s attempts=%d",
			strings.TrimSpace(requestID), attempts)
		return false, nil
	}

	// 记一次尝试，并挂起压缩。压缩完成后会通过既有流程 requestProviderAction(providerActionResume) 自动继续。
	stream.mu.Lock()
	stream.ContextOverflowCompactionAttempts++
	newAttempts := stream.ContextOverflowCompactionAttempts
	stream.PendingProviderAction = providerActionNone
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()

	service.debug.LogRuntime(context.Background(), requestID, conversationID, "context_overflow_compaction_triggered", map[string]any{
		"attempt":             newAttempts,
		"max_attempts":        contextOverflowMaxCompactionRetries,
		"trigger":             plan.Trigger,
		"context_tokens":      plan.ContextTokens,
		"context_window":      plan.ContextWindowSize,
		"messages_to_compact": plan.MessagesToCompact,
	})
	logger.Infof("forwarder context overflow recovery trigger compaction request_id=%s attempt=%d/%d messages_to_compact=%d",
		strings.TrimSpace(requestID), newAttempts, contextOverflowMaxCompactionRetries, plan.MessagesToCompact)

	if err := service.beginPendingCompaction(stream, plan); err != nil {
		return false, err
	}
	return true, nil
}

// maybeHalveContextWindowForOverflow 在中转站返回 context_too_large / context_length_exceeded 时，
// 把命中渠道的 contextWindowTokens 减半（下限 contextWindowHalveFloor）并持久化到该用户的 config.yaml
// 对应 adapter 条目。用 ContextOverflowCompactionAttempts 计数器保证本回合最多减半一次（幂等），
// 避免同一回合反复减半。已降至下限或无可持久化后端时为 no-op。
//
// 设计：catalog 理论窗口（如 gpt-5.6 的 1M）可能远大于中转站实际分配窗口（如 Codex 的 272K），
// 减半能让系统快速收敛到中转站真实限制，配合后续压缩让请求重新落在窗口内。
func (service *Service) maybeHalveContextWindowForOverflow(stream *ActiveStream, conversationID string, requestID string, modelID string) {
	if service == nil || stream == nil || service.contextWindowPersister == nil {
		return
	}
	// 用 ContextOverflowCompactionAttempts 作为幂等守卫：本回合只在首次触发时减半一次。
	stream.mu.Lock()
	already := stream.ContextOverflowCompactionAttempts > 0
	stream.mu.Unlock()
	if already {
		return
	}
	channel, err := service.resolver.SelectChannelForModel(context.Background(), strings.TrimSpace(modelID))
	if err != nil || channel == nil {
		return
	}
	currentWindow := channel.ContextWindowTokens
	if currentWindow <= contextWindowHalveFloor {
		return
	}
	halved := currentWindow / 2
	if halved < contextWindowHalveFloor {
		halved = contextWindowHalveFloor
	}
	if halved >= currentWindow {
		return
	}
	channelID := strings.TrimSpace(channel.ID)
	if channelID == "" {
		return
	}
	if err := service.contextWindowPersister.PersistChannelContextWindow(context.Background(), channelID, halved); err != nil {
		logger.Errorf("forwarder context window halve persist failed request_id=%s channel_id=%s: %v",
			strings.TrimSpace(requestID), channelID, err)
		return
	}
	service.debug.LogRuntime(context.Background(), requestID, conversationID, "context_window_halved", map[string]any{
		"channel_id":     channelID,
		"model_id":       strings.TrimSpace(modelID),
		"before":         currentWindow,
		"after":          halved,
		"floor":          contextWindowHalveFloor,
	})
	logger.Infof("forwarder context window halved request_id=%s channel_id=%s model_id=%s %d -> %d",
		strings.TrimSpace(requestID), channelID, strings.TrimSpace(modelID), currentWindow, halved)
}

// learnContextWindowForDelegatedOverflow 在 delegated worker 因 context_length_exceeded
// 触发溢出重试时，把命中渠道的 contextWindowTokens 收敛为「失败发送量 ×
// delegatedWindowLearnFactor」（下限 contextWindowHalveFloor）并持久化到该用户的
// config.yaml 对应 adapter 条目。与 maybeHalveContextWindowForOverflow 同为「仅下调」
// 语义，但按实际失败发送量收敛，比逐次减半更快贴近真实窗口：下一次任务的首次预算
// 即可落在窗口内，避免每个任务都靠溢出重试兜底。
// 返回 (before, after, ok)；无可持久化后端、已至下限或目标未下调时为 (0,0,false)。
func (service *Service) learnContextWindowForDelegatedOverflow(ctx context.Context, modelID string, sentTokens int64) (int, int, bool) {
	if service == nil || service.resolver == nil || service.contextWindowPersister == nil {
		return 0, 0, false
	}
	if sentTokens <= 0 {
		return 0, 0, false
	}
	channel, err := service.resolver.SelectChannelForModel(ctx, strings.TrimSpace(modelID))
	if err != nil || channel == nil {
		return 0, 0, false
	}
	current := channel.ContextWindowTokens
	if current <= contextWindowHalveFloor {
		return 0, 0, false
	}
	target := int64(float64(sentTokens) * delegatedWindowLearnFactor)
	if target < contextWindowHalveFloor {
		target = contextWindowHalveFloor
	}
	if target >= int64(current) {
		target = int64(current) - 1
	}
	if target <= 0 {
		return 0, 0, false
	}
	channelID := strings.TrimSpace(channel.ID)
	if channelID == "" {
		return 0, 0, false
	}
	if err := service.contextWindowPersister.PersistChannelContextWindow(ctx, channelID, int(target)); err != nil {
		logger.Errorf("forwarder delegated context window learn persist failed channel_id=%s model_id=%s: %v",
			channelID, strings.TrimSpace(modelID), err)
		return 0, 0, false
	}
	service.debug.LogRuntime(ctx, "", "", "delegated_context_window_learned", map[string]any{
		"channel_id":  channelID,
		"model_id":    strings.TrimSpace(modelID),
		"sent_tokens": sentTokens,
		"before":      current,
		"after":       target,
		"floor":       contextWindowHalveFloor,
	})
	logger.Infof("forwarder delegated context window learned channel_id=%s model_id=%s sent_tokens=%d %d -> %d",
		channelID, strings.TrimSpace(modelID), sentTokens, current, target)
	return int(current), int(target), true
}
