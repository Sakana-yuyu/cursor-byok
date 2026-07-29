// max_tokens_recovery.go 处理 provider 返回 max_tokens 超限 400 时的自救：
// 从错误文本解析中转站真实 max_tokens 限制，降到该值以下重试 provider，
// 避免 catalog 静态上限高于中转站实际限制（如 GLM-5.2 catalog=8192 但 Neurons 限 4096）
// 导致整轮直接失败。每轮重试次数有上限（maxTokensRecoveryAttempts），防止无限循环。
//
// 镜像 context_overflow.go 的恢复模式：检测 -> 设恢复态 -> requestProviderAction(resume) 重入 driveProvider。
package forwarder

import (
	"context"
	"errors"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
)

// maxTokensRecoveryAttempts 是单轮回合因 max_tokens 超限触发降级重试的次数上限。
const maxTokensRecoveryAttempts = 2

// maxTokensExceededLimitRe 从 "max_tokens (8192) exceeds limit (4096)" 解析中转站真实限制。
// 兼容 "max_tokens ... limit (N)" 与 "limit: N" 两种常见格式。
var maxTokensExceededLimitRe = regexp.MustCompile(`exceeds limit\s*\((\d+)\)`)

// isMaxTokensExceededError 判断错误是否为 provider 返回的「max_tokens 超过中转站限制」400 错误。
// 触发条件：HTTP 400 且错误体含 max_tokens + exceeds limit（或 code=max_tokens_too_large）。
func isMaxTokensExceededError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *modeladapter.HTTPStatusError
	if errors.As(err, &httpErr) && httpErr != nil {
		if httpErr.StatusCode != 400 {
			return false
		}
		message := strings.ToLower(httpErr.Message)
		return strings.Contains(message, "max_tokens") &&
			(strings.Contains(message, "exceeds limit") || strings.Contains(message, "max_tokens_too_large"))
	}
	// 兜底：错误链无法结构化提取 HTTPStatusError 时，按文本匹配（部分中转站包装层会丢失状态码）。
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "max_tokens") &&
		strings.Contains(message, "exceeds limit") &&
		(strings.Contains(message, "status=400") || strings.Contains(message, "max_tokens_too_large"))
}

// parseMaxTokensLimitFromError 从错误文本解析中转站的 max_tokens 真实限制值。
// 解析失败时返回 0（调用方回退到「当前值减半」策略）。
func parseMaxTokensLimitFromError(err error) int {
	if err == nil {
		return 0
	}
	text := err.Error()
	if match := maxTokensExceededLimitRe.FindStringSubmatch(text); len(match) >= 2 {
		if value, parseErr := strconv.Atoi(strings.TrimSpace(match[1])); parseErr == nil && value > 0 {
			return value
		}
	}
	return 0
}

// recoverFromMaxTokensExceeded 在 provider 返回 max_tokens 超限 400 时尝试降级重试。
// 返回 (recovered=true, nil) 表示已挂起重试（driveProvider 会用更小的 max_tokens 重新发起）；
// 返回 (false, nil) 表示已达重试上限或无法解析限制，交给调用方走正常失败路径。
func (service *Service) recoverFromMaxTokensExceeded(stream *ActiveStream, requestID string, cause error) (bool, error) {
	if service == nil || stream == nil {
		return false, nil
	}
	var channelErr *modeladapter.ChannelError
	if !errors.As(cause, &channelErr) || channelErr == nil || strings.TrimSpace(channelErr.ChannelID) == "" {
		return false, nil
	}
	stream.mu.Lock()
	attempts := stream.MaxTokensRecoveryAttempts
	existingCap := stream.MaxTokensRecoveryCap
	stream.mu.Unlock()
	if attempts >= maxTokensRecoveryAttempts {
		log.Printf("forwarder max_tokens recovery exhausted request_id=%s attempts=%d",
			strings.TrimSpace(requestID), attempts)
		return false, nil
	}

	// 解析中转站真实限制；失败则基于已有 cap 减半，确保每次重试都更保守。
	limit := parseMaxTokensLimitFromError(cause)
	if limit <= 0 {
		if existingCap > 0 {
			limit = existingCap / 2
		} else {
			// 无可解析限制且无前次 cap：取一个安全的小值兜底。
			limit = 2048
		}
	}
	if limit < 1 {
		limit = 1
	}

	stream.mu.Lock()
	stream.MaxTokensRecoveryAttempts++
	newAttempts := stream.MaxTokensRecoveryAttempts
	stream.MaxTokensRecoveryCap = limit
	stream.PendingProviderAction = providerActionNone
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()

	if service.maxTokensPersister != nil {
		if err := service.maxTokensPersister.PersistChannelMaxTokensCap(context.Background(), channelErr.ChannelID, limit); err != nil {
			log.Printf("forwarder persist max_tokens cap failed request_id=%s channel_id=%s: %v", strings.TrimSpace(requestID), channelErr.ChannelID, err)
		}
	}

	service.debug.LogRuntime(context.Background(), requestID, stream.ConversationID, "max_tokens_recovery_triggered", map[string]any{
		"attempt":      newAttempts,
		"max_attempts": maxTokensRecoveryAttempts,
		"recovery_cap": limit,
		"cause":        cause.Error(),
	})
	log.Printf("forwarder max_tokens recovery retry request_id=%s attempt=%d/%d recovery_cap=%d",
		strings.TrimSpace(requestID), newAttempts, maxTokensRecoveryAttempts, limit)

	// 重入 driveProvider：它会在 resolveProviderOutputBudget 之后读取 MaxTokensRecoveryCap 覆盖预算。
	if err := service.requestProviderAction(stream, providerActionResume); err != nil {
		return false, err
	}
	return true, nil
}
