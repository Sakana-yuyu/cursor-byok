// router.go 按模型标识选择 OpenAI 或 Anthropic 兼容适配器。
package modeladapter

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	legacyruntime "cursor/internal/runtime"
)

// Router 是 MVP 阶段的模型适配路由器。
type Router struct {
	// openai 负责 OpenAI 兼容流式请求。
	openai ModelAdapter
	// anthropic 负责 Anthropic 兼容流式请求。
	anthropic ModelAdapter
	// resolver 负责从本地配置中解析实际模型通道。
	resolver ChannelResolver
}

type ChannelResolver interface {
	SelectChannelForModel(context.Context, string) (*legacyruntime.ResolvedChannel, error)
	ProviderStreamIdleTimeout(context.Context) time.Duration
}

// NewRouter 创建模型适配路由器。
func NewRouter(resolver ChannelResolver) *Router {
	return &Router{
		openai:    NewOpenAIAdapter(),
		anthropic: NewAnthropicAdapter(),
		resolver:  resolver,
	}
}

const (
	// routerMaxStreamAttempts 是跨渠道故障切换的尝试上限。
	routerMaxStreamAttempts = 8
	// routerRetryBaseDelay 是渠道切换/重试之间的基准退避间隔。
	routerRetryBaseDelay = 150 * time.Millisecond
	// routerRetryMaxDelay 是渠道切换/重试之间的最大退避间隔。
	routerRetryMaxDelay = 2 * time.Second
)

// Stream 根据模型标识选择具体 provider，并在 provider 失败时按需切换渠道或退避重试。
//
// 故障切换策略：
//   - 多渠道时，轮询游标会在每次失败后切到下一个渠道，优先尝试不同端点；
//   - 单渠道（游标重新回到已尝试过的渠道）且错误为永久错误（4xx，429 除外）时立即返回，
//     不再对同一端点做无意义的重试；
//   - 瞬时错误在下一次尝试前施加小幅退避；
//   - routerMaxStreamAttempts 仍作为尝试次数上限。
func (router *Router) Stream(ctx context.Context, req StreamRequest, sink func(ModelEvent) error) error {
	if router == nil || router.resolver == nil {
		return fmt.Errorf("model adapter resolver is unavailable")
	}

	tried := make(map[string]struct{})
	var firstErr error
	var lastErrPermanent bool

	for attempt := 0; attempt < routerMaxStreamAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if firstErr != nil {
				return firstErr
			}
			return err
		}

		channel, err := router.resolver.SelectChannelForModel(ctx, req.ModelID)
		if err != nil {
			return err
		}
		if channel == nil {
			return fmt.Errorf("no available channel for model %q", req.ModelID)
		}
		channelID := strings.TrimSpace(channel.ID)

		if attempt > 0 {
			// 游标回到已尝试过的渠道，说明没有其它可用端点。
			if _, seen := tried[channelID]; seen && lastErrPermanent {
				return firstErr
			}
			if err := sleepWithContext(ctx, routerRetryBackoff(attempt)); err != nil {
				if firstErr != nil {
					return firstErr
				}
				return err
			}
		}

		streamErr := router.streamChannel(ctx, req, channel, sink)
		if streamErr == nil || ctx.Err() != nil {
			return streamErr
		}
		if firstErr == nil {
			firstErr = streamErr
		}
		tried[channelID] = struct{}{}
		lastErrPermanent = isPermanentProviderError(streamErr)
	}

	if firstErr != nil {
		return firstErr
	}
	return fmt.Errorf("all model channels failed")
}

// streamChannel 使用已解析的渠道构造请求并驱动对应 provider 适配器。此函数不改变模型可见的请求内容。
func (router *Router) streamChannel(ctx context.Context, req StreamRequest, channel *legacyruntime.ResolvedChannel, sink func(ModelEvent) error) error {
	resolved := req
	resolved.Provider = strings.TrimSpace(channel.Provider)
	resolved.ProtocolMode = strings.TrimSpace(channel.ProtocolMode)
	resolved.ProtocolGroup = strings.TrimSpace(channel.ProtocolGroup)
	resolved.BaseURL = strings.TrimSpace(channel.BaseURL)
	resolved.APIKey = strings.TrimSpace(channel.APIKey)
	resolved.ProviderModelID = strings.TrimSpace(channel.Model)
	resolved.ResolvedChannelID = strings.TrimSpace(channel.ID)
	resolved.ResolvedChannelName = strings.TrimSpace(channel.Name)
	resolved.ResolvedContextWindowTokens = channel.ContextWindowTokens
	resolved.ReasoningEffort = openAIReasoningEffortFromRuntime(channel.ReasoningEffort)
	resolved.OpenAIEndpoint = strings.TrimSpace(channel.OpenAIEndpoint)
	resolved.OpenAIRequestGroup = strings.TrimSpace(channel.OpenAIRequestGroup)
	resolved.OpenAIExtraParamsEnabled = channel.OpenAIExtraParamsEnabled
	resolved.OpenAIExtraParamsJSON = strings.TrimSpace(channel.OpenAIExtraParamsJSON)
	resolved.FastMode = channel.FastMode
	resolved.OpenAIServiceTier = strings.TrimSpace(channel.OpenAIServiceTier)
	resolved.CustomHeadersEnabled = channel.CustomHeadersEnabled
	resolved.CustomHeadersJSON = strings.TrimSpace(channel.CustomHeadersJSON)
	resolved.AnthropicExtraParamsEnabled = channel.AnthropicExtraParamsEnabled
	resolved.AnthropicExtraParamsJSON = strings.TrimSpace(channel.AnthropicExtraParamsJSON)
	resolved.AnthropicMaxTokens = channel.AnthropicMaxTokens
	resolved.AnthropicThinkingEffort = strings.TrimSpace(channel.AnthropicThinkingEffort)
	resolved.ThinkingBudgetTokens = channel.ThinkingBudgetTokens
	resolved.ProviderStreamIdleTimeout = router.resolver.ProviderStreamIdleTimeout(ctx)
	runtimeThinkingEffort := normalizeRuntimeThinkingEffort(req.ThinkingEffort)
	if runtimeThinkingEffort != "" {
		resolved.ThinkingEffort = runtimeThinkingEffort
		if runtimeThinkingEffort == "disabled" {
			resolved.ReasoningEffort = ""
			resolved.AnthropicThinkingEffort = ""
		} else {
			resolved.ReasoningEffort = openAIReasoningEffortFromRuntime(runtimeThinkingEffort)
			resolved.AnthropicThinkingEffort = runtimeThinkingEffort
		}
	} else {
		resolved.ThinkingEffort = ""
	}
	if resolved.MaxTokens <= 0 && channel.MaxTokens > 0 {
		resolved.MaxTokens = channel.MaxTokens
	}
	if req.MaxTokens > 0 && (resolved.AnthropicMaxTokens <= 0 || req.MaxTokens < resolved.AnthropicMaxTokens) {
		resolved.AnthropicMaxTokens = req.MaxTokens
	}
	if resolved.AnthropicMaxTokens <= 0 && resolved.MaxTokens > 0 {
		resolved.AnthropicMaxTokens = resolved.MaxTokens
	}
	if resolved.ProviderModelID == "" {
		resolved.ProviderModelID = strings.TrimSpace(req.ModelID)
	}
	resolved.Messages = sanitizeProviderMessages(req.Messages)
	if resolved.RequestKnobs != nil {
		resolved.RequestKnobs["max_tokens"] = resolved.MaxTokens
		resolved.RequestKnobs["protocol_mode"] = resolved.ProtocolMode
		resolved.RequestKnobs["protocol_group"] = resolved.ProtocolGroup
		if runtimeThinkingEffort != "" {
			resolved.RequestKnobs["runtime_thinking_effort"] = runtimeThinkingEffort
		} else {
			delete(resolved.RequestKnobs, "runtime_thinking_effort")
		}
		if resolved.Provider == "openai" {
			if strings.TrimSpace(resolved.ReasoningEffort) != "" {
				resolved.RequestKnobs["reasoning_effort"] = strings.TrimSpace(resolved.ReasoningEffort)
			} else {
				delete(resolved.RequestKnobs, "reasoning_effort")
			}
			resolved.RequestKnobs["openai_endpoint"] = resolved.OpenAIEndpoint
			resolved.RequestKnobs["openai_request_group"] = resolved.OpenAIRequestGroup
			resolved.RequestKnobs["openai_extra_params_enabled"] = resolved.OpenAIExtraParamsEnabled
			resolved.RequestKnobs["openai_fast_mode"] = resolved.FastMode
			if resolved.FastMode {
				resolved.RequestKnobs["openai_service_tier"] = "priority"
			} else if strings.TrimSpace(resolved.OpenAIServiceTier) != "" {
				resolved.RequestKnobs["openai_service_tier"] = strings.TrimSpace(resolved.OpenAIServiceTier)
			} else {
				delete(resolved.RequestKnobs, "openai_service_tier")
			}
			resolved.RequestKnobs["custom_headers_enabled"] = resolved.CustomHeadersEnabled
		} else if resolved.Provider == "anthropic" {
			delete(resolved.RequestKnobs, "reasoning_effort")
			resolved.RequestKnobs["custom_headers_enabled"] = resolved.CustomHeadersEnabled
			resolved.RequestKnobs["anthropic_extra_params_enabled"] = resolved.AnthropicExtraParamsEnabled
			anthropicMaxTokens := maxAnthropicTokens(resolved)
			resolved.RequestKnobs["max_tokens"] = anthropicMaxTokens
			resolved.RequestKnobs["anthropic_max_tokens"] = anthropicMaxTokens
			if strings.TrimSpace(resolved.AnthropicThinkingEffort) != "" {
				resolved.RequestKnobs["anthropic_thinking_effort"] = anthropicThinkingEffort(resolved)
			} else {
				delete(resolved.RequestKnobs, "anthropic_thinking_effort")
			}
		}
	}

	var streamErr error
	channelBaseURL := strings.TrimSpace(channel.BaseURL)
	channelGroupName := strings.TrimSpace(channel.GroupName)
	identitySink := func(event ModelEvent) error {
		if event.BaseURL == "" {
			event.BaseURL = channelBaseURL
		}
		if event.GroupName == "" {
			event.GroupName = channelGroupName
		}
		return sink(event)
	}
	switch resolved.Provider {
	case "anthropic":
		streamErr = router.anthropic.Stream(ctx, resolved, identitySink)
	case "openai":
		streamErr = router.openai.Stream(ctx, resolved, identitySink)
	default:
		streamErr = fmt.Errorf("unsupported provider %q", resolved.Provider)
	}
	return streamErr
}

// isPermanentProviderError 判断 provider 错误是否为永久错误（4xx，429 除外）。
// 适配器错误由 buildHTTPStatusError 生成，形如 "... status=<code> ..."。
func isPermanentProviderError(err error) bool {
	if err == nil {
		return false
	}
	status := parseProviderErrorStatus(err.Error())
	if status < 400 || status >= 500 {
		return false
	}
	return status != 429
}

// parseProviderErrorStatus 从错误文本中解析 "status=<code>" 的状态码；解析失败返回 0。
func parseProviderErrorStatus(message string) int {
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

// routerRetryBackoff 计算渠道切换/重试之间的退避时长（指数加抖动，带上限）。
func routerRetryBackoff(attempt int) time.Duration {
	backoff := routerRetryBaseDelay << attempt
	if backoff <= 0 || backoff > routerRetryMaxDelay {
		backoff = routerRetryMaxDelay
	}
	jitter := time.Duration(rand.Int63n(int64(backoff)/2 + 1))
	return backoff/2 + jitter
}

// sanitizeProviderMessages removes replay-only placeholders and trims trailing
// assistant prefill so providers that require a user/tool terminal message do
// not reject the request.
func sanitizeProviderMessages(input []Message) []Message {
	if len(input) == 0 {
		return nil
	}

	filtered := make([]Message, 0, len(input))
	for _, message := range input {
		if isAssistantPlaceholderMessage(message) {
			continue
		}
		filtered = append(filtered, message)
	}
	filtered = mergeAdjacentAssistantToolCallMessages(filtered)
	filtered = trimDanglingAssistantToolCalls(filtered)
	for len(filtered) > 0 && isAssistantPrefillMessage(filtered[len(filtered)-1]) {
		filtered = filtered[:len(filtered)-1]
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func isAssistantPlaceholderMessage(message Message) bool {
	if strings.TrimSpace(message.Role) != "assistant" {
		return false
	}
	if len(message.ToolCalls) > 0 || len(message.ContentParts) > 0 {
		return false
	}
	if strings.TrimSpace(message.ToolCallID) != "" || strings.TrimSpace(message.Name) != "" {
		return false
	}
	if strings.TrimSpace(message.ReasoningContent) != "" {
		return false
	}
	if strings.TrimSpace(message.ReasoningSignature) != "" {
		return false
	}
	switch strings.TrimSpace(message.Content) {
	case "":
		return true
	default:
		return false
	}
}

func isAssistantPrefillMessage(message Message) bool {
	if strings.TrimSpace(message.Role) != "assistant" {
		return false
	}
	if len(message.ToolCalls) > 0 {
		return false
	}
	if strings.TrimSpace(message.ToolCallID) != "" || strings.TrimSpace(message.Name) != "" {
		return false
	}
	return strings.TrimSpace(message.Content) != "" || strings.TrimSpace(message.ReasoningContent) != ""
}

func mergeAdjacentAssistantToolCallMessages(input []Message) []Message {
	if len(input) == 0 {
		return nil
	}
	merged := make([]Message, 0, len(input))
	for _, raw := range input {
		message := cloneProviderMessage(raw)
		if mergeProviderAssistantToolCalls(&merged, message) {
			continue
		}
		merged = append(merged, message)
	}
	return merged
}

func cloneProviderMessage(message Message) Message {
	cloned := message
	if len(message.ContentParts) > 0 {
		cloned.ContentParts = append([]ContentPart(nil), message.ContentParts...)
	}
	if len(message.ToolCalls) > 0 {
		cloned.ToolCalls = append([]ToolCallDescriptor(nil), message.ToolCalls...)
	}
	if len(message.OpenAIResponsesReasoningSummary) > 0 {
		cloned.OpenAIResponsesReasoningSummary = append([]byte(nil), message.OpenAIResponsesReasoningSummary...)
	}
	return cloned
}

func mergeProviderAssistantToolCalls(messages *[]Message, message Message) bool {
	if len(*messages) == 0 {
		return false
	}
	last := &(*messages)[len(*messages)-1]
	if !canMergeProviderAssistantToolCalls(*last, message) {
		return false
	}
	startIndex := len(last.ToolCalls)
	for index, toolCall := range message.ToolCalls {
		item := toolCall
		item.Index = startIndex + index
		last.ToolCalls = append(last.ToolCalls, item)
	}
	last.ReasoningContent = mergeProviderReasoning(last.ReasoningContent, message.ReasoningContent)
	mergeProviderReasoningMetadata(last, message)
	return true
}

func canMergeProviderAssistantToolCalls(last Message, current Message) bool {
	if strings.TrimSpace(last.Role) != "assistant" || strings.TrimSpace(current.Role) != "assistant" {
		return false
	}
	if len(last.ToolCalls) == 0 || len(current.ToolCalls) == 0 {
		return false
	}
	if strings.TrimSpace(last.ToolCallID) != "" || strings.TrimSpace(last.Name) != "" {
		return false
	}
	if strings.TrimSpace(current.ToolCallID) != "" || strings.TrimSpace(current.Name) != "" {
		return false
	}
	if strings.TrimSpace(current.Content) != "" || len(current.ContentParts) > 0 {
		return false
	}
	return true
}

func mergeProviderReasoning(left string, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	switch {
	case left == "":
		return right
	case right == "", right == left:
		return left
	default:
		return left + "\n\n" + right
	}
}

func mergeProviderReasoningSignature(left string, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	switch {
	case left == "":
		return right
	case right == "", right == left:
		return left
	default:
		return ""
	}
}

func mergeProviderReasoningSignatureSource(left string, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	switch {
	case left == "":
		return right
	case right == "", right == left:
		return left
	default:
		return ""
	}
}

func mergeProviderReasoningMetadata(last *Message, current Message) {
	if last == nil {
		return
	}
	leftSignature := strings.TrimSpace(last.ReasoningSignature)
	rightSignature := strings.TrimSpace(current.ReasoningSignature)
	mergedSignature := mergeProviderReasoningSignature(leftSignature, rightSignature)
	last.ReasoningSignature = mergedSignature
	if mergedSignature == "" {
		last.ReasoningSignatureSource = ""
		last.OpenAIResponsesReasoningID = ""
		last.OpenAIResponsesReasoningStatus = ""
		last.OpenAIResponsesReasoningSummary = nil
		return
	}
	if leftSignature == "" && rightSignature != "" {
		last.ReasoningSignatureSource = strings.TrimSpace(current.ReasoningSignatureSource)
		last.OpenAIResponsesReasoningID = current.OpenAIResponsesReasoningID
		last.OpenAIResponsesReasoningStatus = current.OpenAIResponsesReasoningStatus
		last.OpenAIResponsesReasoningSummary = append([]byte(nil), current.OpenAIResponsesReasoningSummary...)
		return
	}
	if leftSignature == rightSignature {
		last.ReasoningSignatureSource = mergeProviderReasoningSignatureSource(last.ReasoningSignatureSource, current.ReasoningSignatureSource)
		if strings.TrimSpace(last.OpenAIResponsesReasoningID) == "" {
			last.OpenAIResponsesReasoningID = current.OpenAIResponsesReasoningID
		}
		if strings.TrimSpace(last.OpenAIResponsesReasoningStatus) == "" {
			last.OpenAIResponsesReasoningStatus = current.OpenAIResponsesReasoningStatus
		}
		if len(last.OpenAIResponsesReasoningSummary) == 0 {
			last.OpenAIResponsesReasoningSummary = append([]byte(nil), current.OpenAIResponsesReasoningSummary...)
		}
	}
}

func trimDanglingAssistantToolCalls(input []Message) []Message {
	if len(input) == 0 {
		return nil
	}
	trimmed := make([]Message, 0, len(input))
	for index := 0; index < len(input); index++ {
		message := cloneProviderMessage(input[index])
		if strings.TrimSpace(message.Role) != "assistant" || len(message.ToolCalls) == 0 {
			trimmed = append(trimmed, message)
			continue
		}

		end := index + 1
		responded := make(map[string]struct{}, len(message.ToolCalls))
		for end < len(input) && strings.TrimSpace(input[end].Role) == "tool" {
			toolCallID := strings.TrimSpace(input[end].ToolCallID)
			if toolCallID != "" {
				responded[toolCallID] = struct{}{}
			}
			end++
		}

		nextToolCalls := make([]ToolCallDescriptor, 0, len(message.ToolCalls))
		allowedToolCallIDs := make(map[string]struct{}, len(message.ToolCalls))
		for _, toolCall := range message.ToolCalls {
			toolCallID := strings.TrimSpace(toolCall.ID)
			if _, ok := responded[toolCallID]; !ok {
				continue
			}
			item := toolCall
			item.Index = len(nextToolCalls)
			nextToolCalls = append(nextToolCalls, item)
			allowedToolCallIDs[toolCallID] = struct{}{}
		}

		if len(nextToolCalls) > 0 {
			message.ToolCalls = nextToolCalls
			trimmed = append(trimmed, message)
			for toolIndex := index + 1; toolIndex < end; toolIndex++ {
				toolMessage := cloneProviderMessage(input[toolIndex])
				if _, ok := allowedToolCallIDs[strings.TrimSpace(toolMessage.ToolCallID)]; !ok {
					continue
				}
				trimmed = append(trimmed, toolMessage)
			}
		} else if strings.TrimSpace(message.Content) != "" || len(message.ContentParts) > 0 || strings.TrimSpace(message.ReasoningContent) != "" {
			message.ToolCalls = nil
			trimmed = append(trimmed, message)
		}

		index = end - 1
	}
	return trimmed
}

func normalizeRuntimeThinkingEffort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "disabled", "low", "medium", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(raw))
	case "disable", "off", "none", "false", "no", "0":
		return "disabled"
	case "very_high", "very-high", "veryhigh", "x-high", "extra_high", "extra-high", "extrahigh":
		return "xhigh"
	case "maximum":
		return "max"
	default:
		return ""
	}
}

func openAIReasoningEffortFromRuntime(runtimeThinkingEffort string) string {
	switch normalizeRuntimeThinkingEffort(runtimeThinkingEffort) {
	case "low", "medium", "high", "xhigh", "max":
		return normalizeRuntimeThinkingEffort(runtimeThinkingEffort)
	default:
		return ""
	}
}
