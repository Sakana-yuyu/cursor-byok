// router.go 按模型标识选择 OpenAI 或 Anthropic 兼容适配器。
package modeladapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"cursor/internal/modelcontext"
	legacyruntime "cursor/internal/runtime"
)

// Router 是 MVP 阶段的模型适配路由器。
type Router struct {
	// openai 负责 OpenAI 兼容流式请求。
	openai ModelAdapter
	// anthropic 负责 Anthropic 兼容流式请求。
	anthropic ModelAdapter
	// gemini 负责 Gemini native 流式请求。
	gemini ModelAdapter
	// resolver 负责从本地配置中解析实际模型通道。
	resolver        ChannelResolver
	healthMu        sync.Mutex
	healthByChannel map[string]channelHealth
}

type channelHealth struct {
	cooldownUntil time.Time
}

type ChannelResolver interface {
	SelectChannelForModel(context.Context, string) (*legacyruntime.ResolvedChannel, error)
	ProviderStreamIdleTimeout(context.Context) time.Duration
	// TurnStaleTimeout 表示一轮回合进入「等待外部（工具/交互结果）」后，
	// 在无任何进展时由 turn-staleness 看门狗触发自救的阈值。
	TurnStaleTimeout(context.Context) time.Duration
	// NativeDelegationProgressTimeout 表示 native Cursor 子代理「无有效进展」看门狗的阈值：
	// 子代理既无工具结果、又无模型输出/思考活动超过此时长时判定超时。
	NativeDelegationProgressTimeout(context.Context) time.Duration
}

type multiChannelResolver interface {
	SelectChannelsForModel(context.Context, string) ([]*legacyruntime.ResolvedChannel, error)
}

// NewRouter 创建模型适配路由器。
func NewRouter(resolver ChannelResolver) *Router {
	return &Router{
		openai:          NewOpenAIAdapter(),
		anthropic:       NewAnthropicAdapter(),
		gemini:          NewGeminiAdapter(),
		resolver:        resolver,
		healthByChannel: make(map[string]channelHealth),
	}
}

const (
	// routerMaxStreamAttempts 是跨渠道故障切换的尝试上限。
	// 内层 providerRetryMaxAttempts 已降为 1（不再自行重试 429/5xx），
	// 因此本值即单次 Stream 调用的总尝试上限；耗尽后直接返回错误。
	routerMaxStreamAttempts = 10
	// routerRetryFixedInterval 是渠道切换/重试之间的固定重试间隔。
	// 每 10 秒重试一次、共重试 routerMaxStreamAttempts(10) 次，全部失败才上报错误；
	// 给上游瞬时故障（如中转站 Codex OAuth 认证恢复、连接超时恢复）留出恢复窗口。
	routerRetryFixedInterval = 10 * time.Second
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
	// streamErr 保存最近一次渠道失败的错误，供提前返回前的 404 判定
	//（提前返回检查发生在 streamChannel 调用之前，因此需在循环外声明）。
	var streamErr error

	for attempt := 0; attempt < routerMaxStreamAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if firstErr != nil {
				return firstErr
			}
			return err
		}

		channel, err := router.selectChannel(ctx, req.ModelID)
		if err != nil {
			return err
		}
		if channel == nil {
			return fmt.Errorf("no available channel for model %q", req.ModelID)
		}
		channelID := strings.TrimSpace(channel.ID)

		if attempt > 0 {
			// 游标回到已尝试过的渠道，说明没有其它可用端点。
			if _, seen := tried[channelID]; seen {
				// OpenAI 兼容端点的 404：已无新渠道可换（单渠道必然如此；多渠道
				// 路径下等于轮转一圈、全部渠道已 404），继续重试无意义，直接返回。
				if isOpenAINotFoundError(streamErr) {
					if isOpenAINotFoundError(firstErr) {
						return openAINotFoundReadableError(req.ModelID, firstErr)
					}
					return firstErr
				}
				// 其它永久错误同样立即返回；若 firstErr 为 OpenAI 404（如后续渠道
				// 返回非 404 永久错误），提前返回时同样给出可读文案。
				if lastErrPermanent {
					if isOpenAINotFoundError(firstErr) {
						return openAINotFoundReadableError(req.ModelID, firstErr)
					}
					return firstErr
				}
			}
			if err := sleepWithContext(ctx, routerRetryBackoff(attempt)); err != nil {
				if firstErr != nil {
					return firstErr
				}
				return err
			}
		}

		streamErr = router.streamChannel(ctx, req, channel, sink)
		if streamErr == nil || ctx.Err() != nil {
			if streamErr == nil {
				router.clearChannelFailure(channelID)
			}
			return streamErr
		}
		router.recordChannelFailure(channelID, streamErr)
		if firstErr == nil {
			firstErr = streamErr
		}
		tried[channelID] = struct{}{}
		lastErrPermanent = isPermanentProviderError(streamErr)
	}

	if firstErr != nil {
		if isOpenAINotFoundError(firstErr) {
			return openAINotFoundReadableError(req.ModelID, firstErr)
		}
		return firstErr
	}
	return fmt.Errorf("all model channels failed")
}

func (router *Router) selectChannel(ctx context.Context, modelID string) (*legacyruntime.ResolvedChannel, error) {
	if resolver, ok := router.resolver.(multiChannelResolver); ok {
		channels, err := resolver.SelectChannelsForModel(ctx, modelID)
		if err != nil || len(channels) == 0 {
			return nil, err
		}
		return router.preferHealthyChannel(channels), nil
	}
	return router.resolver.SelectChannelForModel(ctx, modelID)
}

func (router *Router) preferHealthyChannel(channels []*legacyruntime.ResolvedChannel) *legacyruntime.ResolvedChannel {
	if router == nil || len(channels) == 0 {
		return nil
	}
	now := time.Now()
	router.healthMu.Lock()
	defer router.healthMu.Unlock()
	var earliest *legacyruntime.ResolvedChannel
	var earliestUntil time.Time
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		id := strings.TrimSpace(channel.ID)
		health, cooled := router.healthByChannel[id]
		if !cooled || !health.cooldownUntil.After(now) {
			if cooled {
				delete(router.healthByChannel, id)
			}
			return channel
		}
		if earliest == nil || health.cooldownUntil.Before(earliestUntil) {
			earliest = channel
			earliestUntil = health.cooldownUntil
		}
	}
	return earliest
}

func (router *Router) recordChannelFailure(channelID string, err error) {
	if router == nil || strings.TrimSpace(channelID) == "" || err == nil {
		return
	}
	cooldown := channelFailureCooldown(err)
	if cooldown <= 0 {
		return
	}
	router.healthMu.Lock()
	router.healthByChannel[strings.TrimSpace(channelID)] = channelHealth{cooldownUntil: time.Now().Add(cooldown)}
	router.healthMu.Unlock()
}

func (router *Router) clearChannelFailure(channelID string) {
	if router == nil || strings.TrimSpace(channelID) == "" {
		return
	}
	router.healthMu.Lock()
	delete(router.healthByChannel, strings.TrimSpace(channelID))
	router.healthMu.Unlock()
}

func channelFailureCooldown(err error) time.Duration {
	status := parseProviderErrorStatus(err.Error())
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) && statusErr != nil {
		status = statusErr.StatusCode
	}
	switch status {
	case 401, 402, 403:
		return 10 * time.Minute
	case 429, 500, 502, 503, 504:
		return time.Minute
	default:
		return 0
	}
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
	// Max Mode: 使用目录中该模型的理论最大上下文窗口。
	if req.MaxMode {
		if catalogMax := modelcontext.WindowTokens(req.ModelID); catalogMax > 0 {
			resolved.ResolvedContextWindowTokens = catalogMax
		}
	}
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
	// 智能优化：type=openai 的渠道若实际承载 claude 模型，运行时自动升级到 Anthropic 原生协议。
	// claude 的前缀缓存依赖 cache_control 断点（Anthropic /v1/messages 协议），OpenAI 的
	// prompt_cache_key 对 claude 无效。升级须在下面的 knobs 填充之前完成，使 max_tokens /
	// thinking 等 knobs 按 anthropic 分支处理。AnthropicAdapter 会把请求发往 {baseURL}/messages。
	// 若 anthropic 协议请求失败（如中转商不支持 /v1/messages），streamChannel 会降级回 openai。
	// protocolMode=fixed 表示用户明确锁定协议，跳过自动升级（逃生口）。
	upgraded := upgradeOpenAIClaudeToAnthropic(&resolved)
	resolved.Messages = sanitizeProviderMessages(req.Messages)
	// 对明确不支持视觉的模型，把图片 ContentPart 替换为包含本地文件路径的文字占位，
	// 让模型通过用户自己配置的读图工具（MCP）自行读取；exe 自身不做识图。
	resolved.Messages = placeholderImagesFromMessages(ctx, resolved.Messages, resolved.ProviderModelID)
	applyStreamKnobs(&resolved, runtimeThinkingEffort)

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
	streamErr = router.dispatchByProvider(ctx, resolved, identitySink)
	// 失败回退：claude-on-openai 升级到 anthropic 后，若错误表明中转商不支持 /v1/messages
	// 端点（404/405/400），降级回 openai 协议重试一次，避免渠道因升级而不可用。
	if streamErr != nil && upgraded && shouldFallbackToOpenAI(streamErr) {
		downgraded := downgradeAnthropicBackToOpenAI(&resolved, channel)
		if downgraded {
			applyStreamKnobs(&resolved, runtimeThinkingEffort)
			streamErr = router.dispatchByProvider(ctx, resolved, identitySink)
		}
	}
	// 把命中渠道的身份信息附在错误上，供转发层在错误处理时定位具体渠道
	//（如 max_tokens 超限时只持久化修正该渠道配置，而非全局）。
	// 仅包装非空错误；通过 Unwrap 保留底层错误链，errors.As 仍可提取 *HTTPStatusError。
	if streamErr != nil {
		if _, ok := streamErr.(*ChannelError); !ok {
			streamErr = &ChannelError{
				Cause:     streamErr,
				ChannelID: strings.TrimSpace(channel.ID),
				BaseURL:   channelBaseURL,
				GroupName: channelGroupName,
				Provider:  strings.TrimSpace(channel.Provider),
				Model:     strings.TrimSpace(req.ModelID),
			}
		}
	}
	return streamErr
}

// dispatchByProvider 按 resolved.Provider 选择对应适配器执行流式请求。
func (router *Router) dispatchByProvider(ctx context.Context, resolved StreamRequest, sink func(ModelEvent) error) error {
	switch resolved.Provider {
	case "anthropic":
		return router.anthropic.Stream(ctx, resolved, sink)
	case "openai":
		return router.openai.Stream(ctx, resolved, sink)
	case "gemini":
		return router.gemini.Stream(ctx, resolved, sink)
	default:
		return fmt.Errorf("unsupported provider %q", resolved.Provider)
	}
}

// applyStreamKnobs 把 provider 相关运行参数写入 RequestKnobs，按 resolved.Provider 分支处理。
// 升级/降级导致 Provider 变化后需重新调用，使 knobs 与当前协议一致。
func applyStreamKnobs(resolved *StreamRequest, runtimeThinkingEffort string) {
	if resolved.RequestKnobs == nil {
		return
	}
	resolved.RequestKnobs["max_tokens"] = resolved.MaxTokens
	resolved.RequestKnobs["protocol_mode"] = resolved.ProtocolMode
	resolved.RequestKnobs["protocol_group"] = resolved.ProtocolGroup
	if runtimeThinkingEffort != "" {
		resolved.RequestKnobs["runtime_thinking_effort"] = runtimeThinkingEffort
	} else {
		delete(resolved.RequestKnobs, "runtime_thinking_effort")
	}
	if resolved.Provider == "openai" || resolved.Provider == "gemini" {
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
		anthropicMaxTokens := maxAnthropicTokens(*resolved)
		resolved.RequestKnobs["max_tokens"] = anthropicMaxTokens
		resolved.RequestKnobs["anthropic_max_tokens"] = anthropicMaxTokens
		if strings.TrimSpace(resolved.AnthropicThinkingEffort) != "" {
			resolved.RequestKnobs["anthropic_thinking_effort"] = anthropicThinkingEffort(*resolved)
		} else {
			delete(resolved.RequestKnobs, "anthropic_thinking_effort")
		}
	}
}

// shouldFallbackToOpenAI 判断一次 anthropic 协议请求失败后是否应降级回 openai 协议。
// 仅当错误表明 messages 端点不存在/不支持（404/405/400）时才回退；5xx/429/网络错误不属于
// 「协议不支持」，交给外层正常重试，避免误回退掩盖真实错误。
func shouldFallbackToOpenAI(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	switch statusErr.StatusCode {
	case 400, 404, 405:
		return true
	}
	return false
}

// downgradeAnthropicBackToOpenAI 把已升级到 anthropic 的 resolved 降级回 openai 协议。
// 从渠道配置重读 openai endpoint/group，返回 true 表示降级成功。
func downgradeAnthropicBackToOpenAI(resolved *StreamRequest, channel *legacyruntime.ResolvedChannel) bool {
	if resolved.Provider != "anthropic" {
		return false
	}
	resolved.Provider = "openai"
	resolved.ProtocolGroup = strings.TrimSpace(channel.ProtocolGroup)
	if resolved.ProtocolGroup == "" {
		resolved.ProtocolGroup = "chat_completions"
	}
	resolved.OpenAIEndpoint = strings.TrimSpace(channel.OpenAIEndpoint)
	resolved.OpenAIRequestGroup = strings.TrimSpace(channel.OpenAIRequestGroup)
	return true
}

// isPermanentProviderError 判断 provider 错误是否为永久错误（4xx，429 除外）。
// 适配器错误由 buildHTTPStatusError 生成，形如 "... status=<code> ..."。
func isPermanentProviderError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// 流式 SSE 层的确定性错误：OpenAI Responses adapter 把流内 error event（如
	// context_too_large / context_length_exceeded）包装成 "openai responses stream error
	// code=..."，不含 HTTP status，因此下面的 status 解析会返回 0 被当作瞬时错误。
	// 但这类错误由输入本身决定（上下文超限），重试不可能改变结果——若不识别为永久错误，
	// 单渠道下 router 会盲目重试 routerMaxStreamAttempts(=10) 次，每次都收到同样错误，
	// 造成约一分钟空转后才冒泡到 forwarder 的 context-overflow 压缩恢复。
	// 这里提前识别，让 router 立即放弃重试，把控制权交给 forwarder 的恢复机制。
	if isContextOverflowStreamError(msg) {
		return true
	}
	status := parseProviderErrorStatus(msg)
	if status < 400 || status >= 500 {
		return false
	}
	if status == 429 {
		return false
	}
	// 某些中转网关（如 daoxe.com）对同一合法请求偶发返回
	// 400 "Invalid request for the selected model"，属于服务端瞬时故障。
	// 将该特定消息视为非永久错误，允许路由器在退避后重试同一渠道。
	if status == 400 && strings.Contains(msg, "Invalid request for the selected model") {
		return false
	}
	return true
}

// isOpenAINotFoundError 判断是否为 OpenAI 兼容端点的 404（模型名/路径未就绪）。
// 仅对 OpenAI 兼容端点放宽为可继续尝试其他渠道；Anthropic 协议 404 仍视为永久。
// 通过错误链中 ChannelError.Provider 识别协议（streamChannel 总会在错误上附加渠道身份），
// 无法识别协议时保守视为非 OpenAI 端点（保持原有提前返回行为）。
func isOpenAINotFoundError(err error) bool {
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr == nil || statusErr.StatusCode != http.StatusNotFound {
		return false
	}
	var channelErr *ChannelError
	if !errors.As(err, &channelErr) || channelErr == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(channelErr.Provider), "openai")
}

// openAINotFoundReadableError 生成 OpenAI 兼容端点 404 的可读错误，提示用户检查
// 模型名或中转站是否支持该模型。文案与 brief 保持一致，供提前返回路径与循环
// 耗尽路径共用。
func openAINotFoundReadableError(modelID string, err error) error {
	return fmt.Errorf("model %q not found at provider (404)：请检查模型名或中转站是否支持该模型（%w）", modelID, err)
}

// isContextOverflowStreamError 判断流式 SSE error event 包装出的错误文本是否表示
// 上下文超限（输入超过模型窗口）。这类错误是确定性的：同一输入重试必然得到同样结果。
// 匹配 OpenAI Responses 的 "code=context_too_large" 与 OpenAI Chat 的
// "context_length_exceeded" / "maximum context length" / "exceeds ... context window"。
func isContextOverflowStreamError(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "context_too_large") ||
		strings.Contains(lower, "context_length_exceeded") ||
		strings.Contains(lower, "maximum context length") ||
		strings.Contains(lower, "exceeds the context window") ||
		strings.Contains(lower, "exceeds context window")
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

// routerRetryBackoff 返回渠道切换/重试之间的固定重试间隔（10 秒）。
// 用户要求：每 10 秒重试一次、共重试 10 次，全部失败才上报错误。
// 固定间隔给上游瞬时故障（如中转站 Codex OAuth 认证恢复、连接超时恢复）留出恢复窗口，
// 且避免指数退避在多次失败后间隔过短导致过早耗尽。
func routerRetryBackoff(_ int) time.Duration {
	return routerRetryFixedInterval
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
	// 注意：不再因 ReasoningContent 非空而保留消息。
	// Kimi 等模型可能产出"仅思考、无正文、无工具调用"的残缺回合（finish=stop），
	// 这类消息只有 reasoning_content 而没有实质输出。将其回放给上游没有意义，
	// 反而会污染上下文、并让部分严格网关（如 daoxe 转发的 Kimi）返回 400。
	// 因此当消息没有正文/工具调用/工具结果时，即视为占位消息予以过滤。
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
	if len(current.ToolCalls) == 0 {
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
