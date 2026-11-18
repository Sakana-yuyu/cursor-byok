package forwarder

import (
	"context"
	"errors"
	"fmt"
	"strings"

	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/modelcontext"
)

const contextBudgetRetryLimit = 2

func (service *Service) resolveModelContextBudget(modelID, modelName string, requested, ceiling int) modelcontext.ContextBudget {
	input := modelcontext.ContextBudgetInput{
		ModelID: firstNonEmpty(modelName, modelID), RequestedTokens: requested, CeilingTokens: ceiling,
	}
	if service != nil && service.resolver != nil {
		if channel, err := service.resolver.SelectChannelForModel(context.Background(), strings.TrimSpace(modelID)); err == nil && channel != nil {
			input.ConfiguredTokens = channel.ContextWindowTokens
			input.ModelID = firstNonEmpty(channel.Model, modelName, modelID)
		}
	}
	return modelcontext.ResolveContextBudget(input)
}

func (service *Service) resolveStreamContextBudget(stream *ActiveStream) modelcontext.ContextBudget {
	if stream == nil {
		return service.resolveModelContextBudget("", "", 0, 0)
	}
	stream.mu.Lock()
	modelID, modelName := stream.ModelID, stream.ModelName
	requested, ceiling := stream.RequestedContextWindowTokens, stream.ContextBudgetCeilingTokens
	stream.mu.Unlock()
	return service.resolveModelContextBudget(modelID, modelName, requested, ceiling)
}

func applyConversationContextBudget(conversation *ConversationFile, tokens uint32) {
	if conversation == nil || tokens == 0 || conversation.TokenDetailsMaxTokens == tokens {
		return
	}
	conversation.TokenDetailsMaxTokens = tokens
	conversation.TokenDetailsUsageView = ""
	// Anchors and pending-pressure markers describe a particular provider view.
	// A changed budget must remeasure canonical history before reusing projections.
	clearConversationUsageAnchor(conversation)
	clearConversationAutoCompactionState(conversation)
}

func streamHasContextBudgetSelection(stream *ActiveStream) bool {
	if stream == nil {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.RequestedContextWindowTokens > 0 || stream.ContextBudgetCeilingTokens > 0
}

func (service *Service) streamCompactionReserveTokens(stream *ActiveStream, conversation *ConversationFile) int64 {
	window := compactionContextWindowSize(conversation)
	reserve := int64(compactionAutoReserveTokens)
	if window > 0 && reserve > window/4 {
		reserve = window / 4
	}
	if stream != nil {
		stream.mu.Lock()
		retryReserve := stream.ContextBudgetRetryReserveTokens
		stream.mu.Unlock()
		if retryReserve > reserve {
			reserve = retryReserve
		}
	}
	// Leave room for a real input even when wire-protocol overhead caused a retry.
	if window > 0 && reserve > window*7/10 {
		reserve = window * 7 / 10
	}
	if reserve < 1 {
		reserve = 1
	}
	return reserve
}

// recoverFromContextBudgetExceeded is only for local pre-send rejections. Unlike
// provider context errors it never rewrites canonical history or replays output.
func (service *Service) recoverFromContextBudgetExceeded(stream *ActiveStream, cause error) (bool, error) {
	var overflow *modeladapter.ContextBudgetExceededError
	if !errors.As(cause, &overflow) || overflow == nil || stream == nil {
		return false, nil
	}
	if overflow.OutputStarted || errors.Is(cause, modeladapter.ErrMidStreamInterrupted) || overflow.ContextWindowTokens <= 0 {
		return false, nil
	}
	stream.mu.Lock()
	if stream.ProviderTextDeltaCount > 0 || stream.ProviderThinkingDeltaCount > 0 || stream.ToolInvocationCount > 0 ||
		len(stream.ProviderAccumulatedText) > 0 || len(stream.ProviderAccumulatedReasoning) > 0 {
		stream.mu.Unlock()
		return false, nil
	}
	if stream.ContextBudgetRetryCount >= contextBudgetRetryLimit {
		stream.mu.Unlock()
		return true, cause
	}
	stream.ContextBudgetRetryCount++
	if stream.ContextBudgetCeilingTokens <= 0 || overflow.ContextWindowTokens < stream.ContextBudgetCeilingTokens {
		stream.ContextBudgetCeilingTokens = overflow.ContextWindowTokens
	}
	gap := overflow.EstimatedInputTokens + int64(overflow.MaxOutputTokens) + providerOutputSafetyTokens - int64(stream.ContextBudgetCeilingTokens)
	if gap < 1024 {
		gap = 1024
	}
	stream.ContextBudgetRetryReserveTokens += gap + int64(overflow.MaxOutputTokens)
	conversationID, requestID := stream.ConversationID, stream.RequestID
	mode, latestUserText, modelName, customSystemPrompt := stream.Mode, stream.LatestUserText, stream.ModelName, stream.CustomSystemPrompt
	attempt := stream.ContextBudgetRetryCount
	ceiling := stream.ContextBudgetCeilingTokens
	stream.mu.Unlock()

	conversation, _, _, err := service.snapshotCheckpointConversation(stream)
	if err != nil {
		return true, err
	}
	conversation, err = service.syncConversationContextWindowTokens(stream, conversationID, conversation)
	if err != nil {
		return true, err
	}
	if conversation == nil {
		return true, fmt.Errorf("cannot recompile context budget without conversation")
	}
	// A protocol expansion may overflow even when the window did not change.
	// Force a progressively smaller projection, rather than submitting the same body.
	compiled, err := service.compiler.Compile(conversation, mode, latestUserText, modelName, customSystemPrompt)
	if err != nil {
		return true, err
	}
	plan, err := buildContextProjectionSummaryPlanWithRecentTail(
		conversation, contextProjectionModelKey(stream), nil,
		max(overflow.EstimatedInputTokens, estimateCompiledPromptTokens(compiled)), int64(ceiling), service.streamCompactionReserveTokens(stream, conversation),
		max(1, contextProjectionRecentTailTurns-attempt), true,
	)
	if err != nil {
		return true, err
	}
	if plan == nil {
		return true, compactionTerminalError{code: compactionOverflowTerminalCode,
			message: fmt.Sprintf("cannot compact the current request into selected context window %d (estimated input=%d, output=%d)", ceiling, overflow.EstimatedInputTokens, overflow.MaxOutputTokens)}
	}
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), requestID, conversationID, "context_budget_recompile", map[string]any{
			"attempt": attempt, "context_window_tokens": ceiling,
			"input_tokens_before": overflow.EstimatedInputTokens, "output_budget_tokens": overflow.MaxOutputTokens,
		})
	}
	return true, service.beginPendingCompaction(stream, plan)
}

func (service *Service) persistPreparedContextUsage(stream *ActiveStream, estimated int64, projection *contextProjectionState) error {
	if service == nil || stream == nil || estimated <= 0 {
		return nil
	}
	stream.mu.Lock()
	conversationID := stream.ConversationID
	stream.mu.Unlock()
	_, err := service.updateConversationMetaAndCheckpoint(stream, conversationID, func(item *ConversationFile) error {
		if item != nil {
			item.TokenDetailsUsedTokens = clampInt64ToUint32(estimated)
			item.TokenDetailsUsageView = contextProjectionUsageView(projection)
			if projection != nil {
				clearConversationUsageAnchor(item)
			}
		}
		return nil
	})
	return err
}
