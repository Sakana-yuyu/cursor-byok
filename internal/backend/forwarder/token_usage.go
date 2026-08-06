package forwarder

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
)

type turnUsageSnapshot struct {
	Provider          string
	Model             string
	Role              string
	ParentModel       string
	LogicalModel      string
	ProviderModel     string
	ModelGroupID      string
	TaskID            string
	ExecutionMode     string
	SupervisorModel   string
	ReviewerModel     string
	BaseURL           string
	GroupName         string
	ErrorCode         string
	// Degraded 标记「能返回但体验降级」的请求原因（移植自 cursor2api 的 degraded 诊断）。
	// 空串表示正常；取值见 degradedReason* 常量。
	Degraded          string
	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheWriteTokens  int64
	UsagePresent      bool
	CacheReadPresent  bool
	CacheWritePresent bool
}

// degradedReason* 是 provider_call 事件的 degraded 取值：请求「成功返回」但质量/可信度降级，
// 与 status=provider_error（失败）区分开，供请求明细展示。
const (
	// degradedReasonSyntheticShellResult：shell 工具超时/断流后注入了合成 <shell-incomplete> 结果，
	// 模型看到的并非真实执行结果（工具「假成功」）。
	degradedReasonSyntheticShellResult = "synthetic_shell_result"
)

// consumeStreamDegradedReason 读取并清零流上的 degraded 标记，返回本次要透传的原因。
// 标记为「一次性消费」：只对紧随其后的那一次 provider_call 生效，避免同回合后续请求重复标注。
func consumeStreamDegradedReason(stream *ActiveStream) string {
	if stream == nil {
		return ""
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if !stream.ShellSyntheticRecoveryInTurn {
		return ""
	}
	stream.ShellSyntheticRecoveryInTurn = false
	return degradedReasonSyntheticShellResult
}

func (snapshot turnUsageSnapshot) hasAny() bool {
	return snapshot.InputTokens > 0 ||
		snapshot.OutputTokens > 0 ||
		snapshot.CacheReadTokens > 0 ||
		snapshot.CacheWriteTokens > 0
}

func (snapshot turnUsageSnapshot) cacheUsageComplete() bool {
	return snapshot.CacheReadPresent && snapshot.CacheWritePresent
}

func (snapshot turnUsageSnapshot) promptTokensTotal() int64 {
	return nonNegativeInt64(snapshot.InputTokens) +
		nonNegativeInt64(snapshot.CacheReadTokens) +
		nonNegativeInt64(snapshot.CacheWriteTokens)
}

func (snapshot turnUsageSnapshot) requestTokensTotal() int64 {
	return snapshot.promptTokensTotal() + nonNegativeInt64(snapshot.OutputTokens)
}


func (service *Service) updateConversationTokenState(stream *ActiveStream, conversationID string, usage turnUsageSnapshot, modelCallID string, finalizeAutoCompaction bool) error {
	if service == nil || !usage.hasAny() {
		return nil
	}
	now := time.Now().UTC()
	autoCompactionReserveTokens := int64(compactionAutoReserveTokens)
	if finalizeAutoCompaction {
		autoCompactionReserveTokens = service.resolveCompactionReserveTokens(activeStreamModelID(stream))
		if autoCompactionReserveTokens <= 0 {
			autoCompactionReserveTokens = compactionAutoReserveTokens
		}
	}
	_, err := service.updateConversationMetaAndCheckpoint(stream, conversationID, func(item *ConversationFile) error {
		if item == nil {
			return nil
		}
		promptTokensTotal := usage.promptTokensTotal()
		if promptTokensTotal > 0 {
			item.TokenDetailsUsedTokens = clampInt64ToUint32(promptTokensTotal)
		}
		if item.TokenDetailsMaxTokens == 0 {
			item.TokenDetailsMaxTokens = projectedConversationMaxTokens
		}
		if finalizeAutoCompaction {
			updateConversationAutoCompactionState(item, usage.requestTokensTotal(), autoCompactionReserveTokens, modelCallID, now)
		}
		return nil
	})
	return err
}

func activeStreamModelID(stream *ActiveStream) string {
	if stream == nil {
		return ""
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.ModelID
}

func updateConversationAutoCompactionState(conversation *ConversationFile, promptTokensTotal int64, reserveTokens int64, modelCallID string, triggeredAt time.Time) {
	if conversation == nil {
		return
	}
	if promptTokensTotal <= 0 {
		clearConversationAutoCompactionState(conversation)
		return
	}
	contextWindowTokens := int64(conversation.TokenDetailsMaxTokens)
	if contextWindowTokens <= 0 {
		contextWindowTokens = projectedConversationMaxTokens
	}
	if reserveTokens <= 0 {
		reserveTokens = compactionAutoReserveTokens
	}
	remainingTokens := contextWindowTokens - promptTokensTotal
	if remainingTokens > reserveTokens {
		clearConversationAutoCompactionState(conversation)
		return
	}
	conversation.AutoCompactionPending = true
	conversation.AutoCompactionPromptTokens = promptTokensTotal
	conversation.AutoCompactionReserveTokens = reserveTokens
	conversation.AutoCompactionTriggeredAt = triggeredAt.UTC().Format(time.RFC3339Nano)
	conversation.AutoCompactionSourceModelCallID = strings.TrimSpace(modelCallID)
}

func clearConversationAutoCompactionState(conversation *ConversationFile) {
	if conversation == nil {
		return
	}
	conversation.AutoCompactionPending = false
	conversation.AutoCompactionPromptTokens = 0
	conversation.AutoCompactionReserveTokens = 0
	conversation.AutoCompactionTriggeredAt = ""
	conversation.AutoCompactionSourceModelCallID = ""
}

func (service *Service) recordTurnUsageSnapshot(stream *ActiveStream, conversationID string, turnSeq int64, requestID string, modelCallID string, status string, usage turnUsageSnapshot, errorText string, turnFinalized bool) error {
	_ = turnFinalized
	if service == nil || strings.TrimSpace(requestID) == "" {
		return nil
	}
	modelID := ""
	modelName := ""
	startedAt := time.Time{}
	lastEventAt := time.Now().UTC()
	var timing *providerCallTiming
	if stream != nil {
		stream.mu.Lock()
		modelID = strings.TrimSpace(stream.ModelID)
		modelName = strings.TrimSpace(stream.ModelName)
		startedAt = stream.CreatedAt
		if !stream.UpdatedAt.IsZero() {
			lastEventAt = stream.UpdatedAt
		}
		timing = stream.LastProviderTiming
		stream.mu.Unlock()
	}
	if strings.TrimSpace(modelName) == "" {
		modelName = modelID
	}
	role := strings.TrimSpace(usage.Role)
	if role == "" {
		role = "parent"
	}
	logicalModel := firstNonEmpty(strings.TrimSpace(usage.LogicalModel), modelName, modelID)
	providerModel := firstNonEmpty(strings.TrimSpace(usage.ProviderModel), strings.TrimSpace(usage.Model))
	if providerModel == "" {
		providerModel = logicalModel
	}
	provider := strings.TrimSpace(usage.Provider)
	if providerModel != "" {
		modelName = providerModel
	}
	channelBaseURL := strings.TrimSpace(usage.BaseURL)
	channelGroupName := strings.TrimSpace(usage.GroupName)
	// 失败时常没有 TurnFinished，usage 里可能缺 provider/渠道信息；用当前模型渠道补齐，避免明细空白。
	if (provider == "" || channelBaseURL == "" || channelGroupName == "") && strings.TrimSpace(modelID) != "" && service.resolver != nil {
		if channel, err := service.resolver.SelectChannelForModel(context.Background(), modelID); err == nil && channel != nil {
			if provider == "" {
				provider = strings.TrimSpace(channel.Provider)
			}
			if channelBaseURL == "" {
				channelBaseURL = strings.TrimSpace(channel.BaseURL)
			}
			if channelGroupName == "" {
				channelGroupName = strings.TrimSpace(channel.GroupName)
			}
			if strings.TrimSpace(usage.Model) == "" && strings.TrimSpace(channel.Model) != "" {
				modelName = strings.TrimSpace(channel.Model)
				providerModel = strings.TrimSpace(channel.Model)
			}
		}
	}
	errorCode := strings.TrimSpace(usage.ErrorCode)
	if errorCode == "" {
		errorCode = extractUsageErrorCode(errorText)
	}
	effectiveModelCallID := firstNonEmpty(strings.TrimSpace(modelCallID), strings.TrimSpace(requestID))
	normalizedStatus := normalizeUsageProviderStatus(status, usage.UsagePresent)
	// 消费本回合的降级标记（shell 合成恢复等「假成功」场景，只标记紧随其后的这一次调用）：
	// 仅 completed 时写入；失败时同样清零丢弃——失败本身是更严重的异常（前端异常优先展示），
	// 且避免标记写进 provider_error 事件造成计数口径混乱，或残留到下一轮。
	if degraded := consumeStreamDegradedReason(stream); degraded != "" {
		if normalizedStatus == "completed" && usage.Degraded == "" {
			usage.Degraded = degraded
		}
	}
	// 时序只在正常完成的调用上有意义：失败/无 usage 时保留零值。
	var ttftMS, durationMS int64
	if timing != nil && normalizedStatus == "completed" {
		ttftMS = timing.TTFTMS
		durationMS = timing.DurationMS
	}
	if service.usageStore != nil {
		if err := service.usageStore.UpsertEvent(usageFileEvent{
			EventID:          usageEventID(requestID, effectiveModelCallID),
			Kind:             usageEventKindProvider,
			Status:           normalizedStatus,
			At:               lastEventAt,
			Model:            modelName,
			Role:             role,
			ParentModel:      strings.TrimSpace(usage.ParentModel),
			LogicalModel:     logicalModel,
			ProviderModel:    providerModel,
			ModelGroupID:     strings.TrimSpace(usage.ModelGroupID),
			TaskID:           strings.TrimSpace(usage.TaskID),
			ExecutionMode:    strings.TrimSpace(usage.ExecutionMode),
			SupervisorModel:  strings.TrimSpace(usage.SupervisorModel),
			ReviewerModel:    strings.TrimSpace(usage.ReviewerModel),
			Provider:         provider,
			BaseURL:          channelBaseURL,
			GroupName:        channelGroupName,
			ErrorCode:        errorCode,
			Degraded:         strings.TrimSpace(usage.Degraded),
			TTFTMS:           ttftMS,
			DurationMS:       durationMS,
			InputTokens:      usage.InputTokens,
			OutputTokens:     usage.OutputTokens,
			CacheReadTokens:  usage.CacheReadTokens,
			CacheWriteTokens: usage.CacheWriteTokens,
			UsagePresent:     usage.UsagePresent,
		}); err != nil {
			return err
		}
	}
	if strings.TrimSpace(conversationID) != "" {
		_, err := service.updateConversationMetaAndCheckpoint(stream, conversationID, func(item *ConversationFile) error {
			if item == nil {
				return nil
			}
			item.LastProviderCall = &ConversationProviderCall{
				RequestID:   strings.TrimSpace(requestID),
				ModelCallID: effectiveModelCallID,
				Provider:    provider,
				Model:       modelName,
				Status:      strings.TrimSpace(status),
				ErrorText:   strings.TrimSpace(errorText),
				Degraded:    strings.TrimSpace(usage.Degraded),
				UpdatedAt:   lastEventAt,
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	_ = turnSeq
	_ = startedAt
	return nil
}

// upsertStandaloneUsageSnapshot records provider calls that do not own an
// ActiveStream, such as local delegated workers and supervisor reviews.
func upsertStandaloneUsageSnapshot(store *UsageFileStore, requestID, modelCallID, status string, usage turnUsageSnapshot, errorText string) error {
	if store == nil || strings.TrimSpace(requestID) == "" {
		return nil
	}
	role := firstNonEmpty(strings.TrimSpace(usage.Role), "parent")
	logicalModel := firstNonEmpty(strings.TrimSpace(usage.LogicalModel), strings.TrimSpace(usage.Model))
	providerModel := firstNonEmpty(strings.TrimSpace(usage.ProviderModel), strings.TrimSpace(usage.Model), logicalModel)
	model := firstNonEmpty(providerModel, logicalModel)
	eventID := usageEventID(requestID, modelCallID)
	errorCode := strings.TrimSpace(usage.ErrorCode)
	if errorCode == "" {
		errorCode = extractUsageErrorCode(errorText)
	}
	return store.UpsertEvent(usageFileEvent{
		EventID:          eventID,
		Kind:             usageEventKindProvider,
		Status:           normalizeUsageProviderStatus(status, usage.UsagePresent),
		At:               time.Now().UTC(),
		Model:            model,
		Role:             role,
		ParentModel:      strings.TrimSpace(usage.ParentModel),
		LogicalModel:     logicalModel,
		ProviderModel:    providerModel,
		ModelGroupID:     strings.TrimSpace(usage.ModelGroupID),
		TaskID:           strings.TrimSpace(usage.TaskID),
		ExecutionMode:    strings.TrimSpace(usage.ExecutionMode),
		SupervisorModel:  strings.TrimSpace(usage.SupervisorModel),
		ReviewerModel:    strings.TrimSpace(usage.ReviewerModel),
		Provider:         strings.TrimSpace(usage.Provider),
		BaseURL:          strings.TrimSpace(usage.BaseURL),
		GroupName:        strings.TrimSpace(usage.GroupName),
		ErrorCode:        errorCode,
		Degraded:         strings.TrimSpace(usage.Degraded),
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
		UsagePresent:     usage.UsagePresent,
	})
}

func (service *Service) recordTurnFinalizedSnapshot(stream *ActiveStream, conversationID string, turnSeq int64, requestID string, status string, errorText string) error {
	_ = stream
	_ = errorText
	if service == nil || service.usageStore == nil || strings.TrimSpace(requestID) == "" {
		return nil
	}
	eventID := turnUsageEventID(conversationID, turnSeq, requestID)
	usage := usageFileEvent{}
	if aggregate, ok, err := service.usageStore.LookupEvent(strings.TrimSpace(requestID)); err != nil {
		return err
	} else if ok {
		usage = aggregate
	}
	return service.usageStore.UpsertEvent(usageFileEvent{
		EventID:          eventID,
		Kind:             usageEventKindTurn,
		Status:           normalizeUsageTurnStatus(status),
		At:               time.Now().UTC(),
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
		UsagePresent:     usage.UsagePresent,
	})
}

func normalizeUsageTurnStatus(status string) string {
	if strings.TrimSpace(status) == usageTurnStatusDone {
		return usageTurnStatusDone
	}
	return strings.TrimSpace(status)
}

// normalizeUsageProviderStatus 把 provider_call 事件的 status 规整为前端可识别的值。
//   - "completed" / "provider_error" 来自上游调用方
//   - 当 status 为空但 provider 明确返回了 usage → 视为 completed
//   - 当 status 为空且没有 usage → 视为 "no_usage"（通常是流被中断/取消）
func normalizeUsageProviderStatus(status string, usagePresent bool) string {
	trimmed := strings.TrimSpace(status)
	switch trimmed {
	case "completed", "provider_error":
		return trimmed
	case "":
		if usagePresent {
			return "completed"
		}
		return "no_usage"
	default:
		return trimmed
	}
}

// extractUsageErrorCode 从错误文本提取请求明细可展示的错误码（message 中的 status=N）。
func extractUsageErrorCode(errorText string) string {
	trimmed := strings.TrimSpace(errorText)
	if trimmed == "" {
		return ""
	}
	if code := parseUsageErrorStatusFromText(trimmed); code > 0 {
		return strconv.Itoa(code)
	}
	return ""
}

// extractUsageErrorCodeFromCause 从真实 error 链提取错误码（HTTP 状态或 terminal code）。
func extractUsageErrorCodeFromCause(cause error) string {
	if cause == nil {
		return ""
	}
	var httpErr *modeladapter.HTTPStatusError
	if errors.As(cause, &httpErr) && httpErr != nil && httpErr.Status() > 0 {
		return strconv.Itoa(httpErr.Status())
	}
	if code := parseUsageErrorStatusFromText(cause.Error()); code > 0 {
		return strconv.Itoa(code)
	}
	var coded interface{ TerminalCode() string }
	if errors.As(cause, &coded) {
		if text := strings.TrimSpace(coded.TerminalCode()); text != "" {
			return text
		}
	}
	return ""
}

// isContextLengthExceededError 判断错误是否为 provider 返回的「输入超出上下文窗口」错误。
// 兼容 OpenAI 的 code=context_length_exceeded（responses stream 与 chat completions 均会出现），
// 以及常见的同义文本描述。触发条件：检测到该错误时，转发层会尝试强制压缩上下文并重试。
func isContextLengthExceededError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "context_length_exceeded") {
		return true
	}
	// 兼容其它 provider 的同义表达。
	return strings.Contains(message, "maximum context length") ||
		strings.Contains(message, "exceeds the context window") ||
		strings.Contains(message, "exceeds context window")
}

func parseUsageErrorStatusFromText(message string) int {
	const marker = "status="
	index := strings.Index(message, marker)
	if index < 0 {
		return 0
	}
	rest := message[index+len(marker):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	status, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return status
}

func turnUsageEventID(conversationID string, turnSeq int64, requestID string) string {
	conversationID = strings.TrimSpace(conversationID)
	requestID = strings.TrimSpace(requestID)
	if conversationID != "" && turnSeq > 0 {
		return fmt.Sprintf("turn::%s::%d", conversationID, turnSeq)
	}
	return "turn::" + requestID
}

func usageEventID(requestID string, modelCallID string) string {
	requestID = strings.TrimSpace(requestID)
	modelCallID = strings.TrimSpace(modelCallID)
	if modelCallID == "" || modelCallID == requestID {
		return requestID
	}
	return requestID + "::" + modelCallID
}

func clampInt64ToUint32(value int64) uint32 {
	if value <= 0 {
		return 0
	}
	if value > int64(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(value)
}

func nonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func maxPositiveInt64(values ...int64) int64 {
	maxValue := int64(0)
	for _, value := range values {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}
