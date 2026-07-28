// context_overflow.go 处理 provider 返回 context_length_exceeded 时的自救：
// 强制压缩上下文并重试 provider，避免因 contextWindowTokens 配置偏大（或模型真实窗口小于配置）
// 导致整轮直接失败、任务无法继续。每轮重试次数有上限（contextOverflowMaxCompactionRetries），
// 防止「压不动」时无限循环。
package forwarder

import (
	"context"
	"log"
	"strings"
	"time"
)

// contextOverflowMaxCompactionRetries 是单轮回合因 context_length_exceeded 触发强制压缩重试的次数上限。
const contextOverflowMaxCompactionRetries = 2

// recoverFromContextOverflow 在 provider 返回 context_length_exceeded 时尝试强制压缩并重试。
// 返回 (recovered=true, nil) 表示已成功挂起压缩流程（压缩完成后会自动 resume provider）；
// 返回 (false, nil) 表示已达重试上限或无可压缩内容，交给调用方走正常失败路径。
func (service *Service) recoverFromContextOverflow(stream *ActiveStream, conversationID string, requestID string, accumulatedText string, accumulatedReasoning string) (bool, error) {
	if service == nil || stream == nil {
		return false, nil
	}
	stream.mu.Lock()
	attempts := stream.ContextOverflowCompactionAttempts
	stream.mu.Unlock()
	if attempts >= contextOverflowMaxCompactionRetries {
		log.Printf("forwarder context overflow recovery exhausted request_id=%s attempts=%d",
			strings.TrimSpace(requestID), attempts)
		return false, nil
	}

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
	compiled, err := service.compiler.Compile(conversation, mode, latestUserText, modelName)
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
		log.Printf("forwarder context overflow recovery skipped (nothing to compact) request_id=%s attempts=%d",
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
	log.Printf("forwarder context overflow recovery trigger compaction request_id=%s attempt=%d/%d messages_to_compact=%d",
		strings.TrimSpace(requestID), newAttempts, contextOverflowMaxCompactionRetries, plan.MessagesToCompact)

	if err := service.beginPendingCompaction(stream, plan); err != nil {
		return false, err
	}
	return true, nil
}
