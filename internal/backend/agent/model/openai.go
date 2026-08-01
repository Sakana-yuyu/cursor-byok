// openai.go 实现 OpenAI 兼容流式适配器。
package modeladapter

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/modelchannel"
	"cursor/internal/netproxy"
)

// OpenAIAdapter 实现 OpenAI 兼容流式请求。
type OpenAIAdapter struct {
	// client 负责发送 HTTP 请求。
	client *http.Client
}

type openAIRequestBody struct {
	Model           string            `json:"model"`
	Tools           []json.RawMessage `json:"tools,omitempty"`
	Messages        []map[string]any  `json:"messages"`
	Stream          bool              `json:"stream"`
	MaxTokens       int               `json:"max_tokens,omitempty"`
	StreamOptions   map[string]any    `json:"stream_options"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	ServiceTier     string            `json:"service_tier,omitempty"`
	PromptCacheKey  string            `json:"prompt_cache_key,omitempty"`
}

type openAIResponsesRequestBody struct {
	Model           string                    `json:"model"`
	Instructions    string                    `json:"instructions,omitempty"`
	Input           []map[string]any          `json:"input"`
	Tools           []map[string]any          `json:"tools,omitempty"`
	Stream          bool                      `json:"stream"`
	MaxOutputTokens int                       `json:"max_output_tokens,omitempty"`
	Reasoning       *openAIResponsesReasoning `json:"reasoning,omitempty"`
	ServiceTier     string                    `json:"service_tier,omitempty"`
	Include         []string                  `json:"include,omitempty"`
	PromptCacheKey  string                    `json:"prompt_cache_key,omitempty"`
	Store           bool                      `json:"store"`
}

type openAIResponsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type openAIToolAccumulator struct {
	CallID                 string
	Name                   string
	Args                   strings.Builder
	LastEmittedPath        string
	LastStreamContent      string
	LastCreatePlanSnapshot string
	ProviderItemID         string
	ProviderCallID         string
	ProviderStatus         string
}

// completedOpenAIToolArgsJSON 校验累积的工具调用参数：必须是空或合法的 JSON 对象。
// 与 anthropic.go 的 completedAnthropicToolArgsJSON 对称：空参数归一化为 "{}"，
// 对残缺/畸形/非对象输入尽早产生可归因的错误，避免下游 dispatch 阶段才失败。
func completedOpenAIToolArgsJSON(accumulator *openAIToolAccumulator) ([]byte, error) {
	if accumulator == nil {
		return []byte("{}"), nil
	}
	trimmed := strings.TrimSpace(accumulator.Args.String())
	if trimmed == "" {
		return []byte("{}"), nil
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		toolName := strings.TrimSpace(accumulator.Name)
		if toolName == "" {
			toolName = "tool"
		}
		return nil, fmt.Errorf("openai returned incomplete or malformed tool input for %s: %w", toolName, err)
	}
	if value == nil {
		toolName := strings.TrimSpace(accumulator.Name)
		if toolName == "" {
			toolName = "tool"
		}
		return nil, fmt.Errorf("openai returned non-object tool input for %s", toolName)
	}
	return []byte(trimmed), nil
}

type openAIImageGenerationAccumulator struct {
	CallID         string
	ImageData      string
	OutputFormat   string
	ProviderItemID string
	ProviderStatus string
	StartedEmitted bool
}

const (
	openAIThinkOpenTag       = "<think>"
	openAIThinkCloseTag      = "</think>"
	openAIStreamMaxTokenSize = 64 * 1024 * 1024
)

var openAIPromptCacheKeyMaximumLengthPattern = regexp.MustCompile(`(?i)maximum\s+(?:string\s+)?length\s*(?:of|is|:)?\s*(\d+)`)

type openAIContentPartKind string

const (
	openAIContentPartText              openAIContentPartKind = "text"
	openAIContentPartReasoning         openAIContentPartKind = "reasoning"
	openAIContentPartThinkingCompleted openAIContentPartKind = "thinking_completed"
)

type openAIContentPart struct {
	Kind openAIContentPartKind
	Text string
}

// openAIThinkTagParser 负责把某些 OpenAI 兼容 provider 放进 content 的 <think> 标签拆回 reasoning 流。
type openAIThinkTagParser struct {
	carry   string
	inThink bool
}

func (parser *openAIThinkTagParser) Consume(text string) []openAIContentPart {
	if parser == nil || text == "" {
		return nil
	}
	input := parser.carry + text
	parser.carry = ""
	parts := make([]openAIContentPart, 0, 4)
	for input != "" {
		if parser.inThink {
			closeIndex := strings.Index(input, openAIThinkCloseTag)
			if closeIndex >= 0 {
				if closeIndex > 0 {
					parts = append(parts, openAIContentPart{
						Kind: openAIContentPartReasoning,
						Text: input[:closeIndex],
					})
				}
				parts = append(parts, openAIContentPart{Kind: openAIContentPartThinkingCompleted})
				parser.inThink = false
				input = input[closeIndex+len(openAIThinkCloseTag):]
				continue
			}
			carryLen := trailingTagPrefixLength(input, openAIThinkCloseTag)
			if emitText := input[:len(input)-carryLen]; emitText != "" {
				parts = append(parts, openAIContentPart{
					Kind: openAIContentPartReasoning,
					Text: emitText,
				})
			}
			parser.carry = input[len(input)-carryLen:]
			break
		}

		openIndex := strings.Index(input, openAIThinkOpenTag)
		if openIndex >= 0 {
			if openIndex > 0 {
				parts = append(parts, openAIContentPart{
					Kind: openAIContentPartText,
					Text: input[:openIndex],
				})
			}
			parser.inThink = true
			input = input[openIndex+len(openAIThinkOpenTag):]
			continue
		}
		carryLen := trailingTagPrefixLength(input, openAIThinkOpenTag)
		if emitText := input[:len(input)-carryLen]; emitText != "" {
			parts = append(parts, openAIContentPart{
				Kind: openAIContentPartText,
				Text: emitText,
			})
		}
		parser.carry = input[len(input)-carryLen:]
		break
	}
	return parts
}

func (parser *openAIThinkTagParser) Flush() []openAIContentPart {
	if parser == nil || parser.carry == "" {
		return nil
	}
	kind := openAIContentPartText
	if parser.inThink {
		kind = openAIContentPartReasoning
	}
	text := parser.carry
	parser.carry = ""
	return []openAIContentPart{{
		Kind: kind,
		Text: text,
	}}
}

// NewOpenAIAdapter 创建一个 OpenAI 兼容适配器。
func NewOpenAIAdapter() *OpenAIAdapter {
	return &OpenAIAdapter{
		client: netproxy.NewHTTPClient(0),
	}
}

// openAIModelSupportsPromptCacheKey 判断当前模型是否应携带 prompt_cache_key。
//
// 大多数 OpenAI 兼容服务（OpenAI 官方、xAI Grok、智谱 GLM、通义 Qwen、月之暗面 Kimi、
// DeepSeek 等）要么原生支持 prompt_cache_key，要么会忽略未知字段，因此默认对所有模型
// 发送。这样 provider 可以按 conversation 复用前缀，显著提升缓存命中率。
//
// 仅对极少数已知会因未知字段报错的 provider 关闭。
func openAIModelSupportsPromptCacheKey(modelID string) bool {
	return true
}

func openAIPromptCacheKey(req StreamRequest, modelID string, maximumLength int) string {
	conversationID := strings.TrimSpace(req.ConversationID)
	if conversationID == "" {
		return ""
	}
	key := "cursor:" + conversationID
	if maximumLength <= 0 || len(key) <= maximumLength {
		return key
	}

	// Providers do not agree on a key-size limit. Once a provider tells us its
	// limit, preserve a recognizable prefix when possible and shorten only the
	// generated key with a deterministic digest.
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(conversationID)))
	prefix := "cursor:"
	if maximumLength <= len(prefix) {
		return digest[:min(maximumLength, len(digest))]
	}
	digestLength := min(maximumLength-len(prefix), len(digest))
	return prefix + digest[:digestLength]
}

func applyOpenAIPromptCacheKeyOverride(body map[string]any, req StreamRequest, modelID string, maximumLength int) {
	if len(body) == 0 {
		return
	}
	if _, ok := body["prompt_cache_key"]; ok {
		return
	}
	if key := openAIPromptCacheKey(req, modelID, maximumLength); key != "" {
		body["prompt_cache_key"] = key
	}
}

func hasExplicitOpenAIPromptCacheKey(req StreamRequest) bool {
	if openAIExtraParamsHasKey(req, "prompt_cache_key") {
		return true
	}
	_, ok := req.RequestBodyOverride["prompt_cache_key"]
	return ok
}

func shouldExposeOpenAIResponsesImageGeneration(req StreamRequest, tools []map[string]any) bool {
	if !openAIResponsesToolNamePresent(tools, "GenerateImage") {
		return false
	}
	// 若用户最新消息已携带图片附件，说明这是识图请求而非生成请求，
	// 不应注入 image_generation 工具——否则账户无生成权限时会触发
	// "Image generation is not enabled for this group" 403 错误。
	if openAILatestUserMessageHasImageAttachment(req) {
		return false
	}
	return openAITextLooksLikeImageGenerationRequest(openAILatestUserRequestText(req))
}

// openAILatestUserMessageHasImageAttachment 判断最新用户消息是否携带图片附件。
func openAILatestUserMessageHasImageAttachment(req StreamRequest) bool {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if strings.TrimSpace(strings.ToLower(msg.Role)) != "user" {
			continue
		}
		return hasImageContentParts(msg.ContentParts)
	}
	return false
}

func ensureOpenAIResponsesImageGenerationTool(tools []map[string]any) []map[string]any {
	for _, tool := range tools {
		if strings.TrimSpace(fmt.Sprint(tool["type"])) == "image_generation" {
			return tools
		}
	}
	return append(tools, map[string]any{"type": "image_generation"})
}

func openAIResponsesToolNamePresent(tools []map[string]any, name string) bool {
	for _, tool := range tools {
		if strings.TrimSpace(fmt.Sprint(tool["name"])) == name {
			return true
		}
		if functionShape, ok := tool["function"].(map[string]any); ok {
			if strings.TrimSpace(fmt.Sprint(functionShape["name"])) == name {
				return true
			}
		}
	}
	return false
}

func openAILatestUserRequestText(req StreamRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		message := req.Messages[i]
		if strings.TrimSpace(strings.ToLower(message.Role)) != "user" {
			continue
		}
		text := message.Content
		if strings.TrimSpace(text) == "" && len(message.ContentParts) > 0 {
			text = collapseTextContentParts(message.ContentParts)
		}
		if tagged := textBetweenOpenAITag(text, "current_user_request"); tagged != "" {
			return tagged
		}
		if tagged := textBetweenOpenAITag(text, "user_query"); tagged != "" {
			return tagged
		}
		if strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func textBetweenOpenAITag(text string, tag string) string {
	openTag := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	start := strings.LastIndex(text, openTag)
	if start < 0 {
		return ""
	}
	start += len(openTag)
	end := strings.Index(text[start:], closeTag)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(text[start : start+end])
}

// openAITextLooksLikeImageGenerationRequest 判断用户请求文本是否像图像生成请求。
// 原策略只匹配图像名词，会把「分析这张图片」「识别图像」等识图句式也误判为生成请求。
// 新策略要求同时出现「生成动词」+「图像名词」，大幅降低识图请求的误触发率。
func openAITextLooksLikeImageGenerationRequest(text string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(text))
	if trimmed == "" {
		return false
	}
	generationVerbs := []string{
		// 中文生成动词
		"生成", "创建", "创作", "绘制", "画",
		// 英文生成动词
		"generate", "create", "draw", "render", "make", "design", "produce",
	}
	imageNouns := []string{
		// 中文图像名词
		"图片", "图像", "照片", "相片", "人像", "头像", "插画", "海报", "壁纸", "封面", "摄影", "真实摄影",
		// 英文图像名词
		"image", "picture", "photo", "portrait", "illustration", "poster", "wallpaper", "cover", "photorealistic",
	}
	hasVerb := false
	for _, verb := range generationVerbs {
		if strings.Contains(trimmed, verb) {
			hasVerb = true
			break
		}
	}
	if !hasVerb {
		return false
	}
	for _, noun := range imageNouns {
		if strings.Contains(trimmed, noun) {
			return true
		}
	}
	return false
}

func OpenAIEndpointURL(baseURL string, endpoint string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	normalizedEndpoint := strings.TrimSpace(endpoint)
	if normalizedEndpoint == "" {
		normalizedEndpoint = modelchannel.OpenAIEndpointResponses
	}
	if !strings.HasPrefix(normalizedEndpoint, "/") {
		normalizedEndpoint = "/" + normalizedEndpoint
	}
	// 规则0：自定义路径模式
	// - baseURL 已含 endpoint 后缀（/chat/completions 或 /responses）→ 直接用 base
	// - 否则追加 /chat/completions（默认协议形态，覆盖 Z.AI /v4 等场景）
	if normalizedEndpoint == modelchannel.OpenAIEndpointCustom {
		if OpenAIEndpointFromBaseURL(base) != "" {
			return base
		}
		return base + "/chat/completions"
	}
	// 规则1：baseURL 已含 endpoint 后缀 → 直接用 base
	if OpenAIEndpointFromBaseURL(base) != "" {
		return base
	}
	// 规则2：baseURL 以 /vN 结尾时，剥离 endpoint 的版本前缀（/v1/、/v2/ 等）
	// 这样 base=.../v4 + endpoint=/v1/chat/completions → .../v4/chat/completions
	if _, ok := trailingVersionSegment(base); ok {
		if rest, stripped := stripEndpointVersionPrefix(normalizedEndpoint); stripped {
			return base + rest
		}
	}
	// 规则3：兜底原样拼接
	return base + normalizedEndpoint
}

// trailingVersionSegment 检测 URL 末尾是否以 /vN 形式结尾（N 为数字），
// 返回版本段（如 "v4"）和是否匹配。用于通用版本段去重。
func trailingVersionSegment(base string) (string, bool) {
	idx := strings.LastIndex(base, "/")
	if idx < 0 {
		return "", false
	}
	seg := base[idx+1:]
	if len(seg) < 2 || seg[0] != 'v' {
		return "", false
	}
	for i := 1; i < len(seg); i++ {
		if seg[i] < '0' || seg[i] > '9' {
			return "", false
		}
	}
	return seg, true
}

// stripEndpointVersionPrefix 剥离 endpoint 路径开头的版本段前缀（/vN/），
// 返回剩余路径和是否成功剥离。
// /v1/chat/completions → ("/chat/completions", true)
// /chat/completions    → ("", false)
func stripEndpointVersionPrefix(endpoint string) (string, bool) {
	if len(endpoint) < 4 || endpoint[0] != '/' || endpoint[1] != 'v' {
		return "", false
	}
	i := 2
	for i < len(endpoint) && endpoint[i] >= '0' && endpoint[i] <= '9' {
		i++
	}
	if i == 2 || i >= len(endpoint) || endpoint[i] != '/' {
		return "", false
	}
	return endpoint[i:], true
}

func ResolveOpenAIEndpoint(baseURL string, endpoint string) string {
	if endpointFromURL := OpenAIEndpointFromBaseURL(baseURL); endpointFromURL != "" {
		return endpointFromURL
	}
	return modelchannel.NormalizeOpenAIEndpoint("openai", endpoint)
}

func OpenAIEndpointFromBaseURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(strings.ToLower(baseURL)), "/")
	switch {
	case strings.HasSuffix(base, "/responses"):
		return modelchannel.OpenAIEndpointResponses
	case strings.HasSuffix(base, "/chat/completions"):
		return modelchannel.OpenAIEndpointChatCompletions
	default:
		return ""
	}
}

func ProviderURLHasEndpoint(baseURL string, endpoints ...string) bool {
	base := strings.TrimRight(strings.TrimSpace(strings.ToLower(baseURL)), "/")
	if base == "" {
		return false
	}
	for _, endpoint := range endpoints {
		normalizedEndpoint := strings.TrimRight(strings.TrimSpace(strings.ToLower(endpoint)), "/")
		if normalizedEndpoint == "" {
			continue
		}
		if !strings.HasPrefix(normalizedEndpoint, "/") {
			normalizedEndpoint = "/" + normalizedEndpoint
		}
		if strings.HasSuffix(base, normalizedEndpoint) {
			return true
		}
	}
	return false
}

// Stream 发送 OpenAI 兼容流式请求，并解析统一模型事件。
func (adapter *OpenAIAdapter) Stream(ctx context.Context, req StreamRequest, sink func(ModelEvent) error) error {
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("openai base url is empty")
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		return fmt.Errorf("openai api key is empty")
	}
	modelID := strings.TrimSpace(req.ProviderModelID)
	if modelID == "" {
		modelID = strings.TrimSpace(req.ModelID)
	}
	if modelID == "" {
		return fmt.Errorf("openai model id is empty")
	}

	endpoint := ResolveOpenAIEndpoint(baseURL, req.OpenAIEndpoint)
	if endpoint == "" {
		return fmt.Errorf("openai endpoint is unsupported: %s", strings.TrimSpace(req.OpenAIEndpoint))
	}
	configuredGroup := strings.TrimSpace(req.ProtocolGroup)
	if configuredGroup == "" {
		configuredGroup = req.OpenAIRequestGroup
	}
	requestGroup := modelchannel.ResolveProtocolGroup(req.ProtocolMode, "openai", modelID, baseURL, endpoint, configuredGroup)
	if requestGroup == "" {
		return fmt.Errorf("openai request group is unsupported: %s", configuredGroup)
	}
	if endpoint != modelchannel.OpenAIEndpointCustom {
		endpoint = modelchannel.OpenAIEndpointForProtocolGroup(requestGroup, endpoint)
	}
	req.OpenAIEndpoint = endpoint
	req.OpenAIRequestGroup = requestGroup
	req.ProtocolGroup = requestGroup
	if req.RequestKnobs != nil {
		req.RequestKnobs["openai_endpoint"] = endpoint
		req.RequestKnobs["openai_request_group"] = requestGroup
		if modelchannel.OpenAIEndpointShape(endpoint) == "responses" {
			req.RequestKnobs["max_output_tokens"] = req.MaxTokens
		}
	}
	if requestGroup == modelchannel.OpenAIRequestGroupResponses {
		return adapter.streamResponsesWithReconnect(ctx, req, baseURL, apiKey, modelID, sink)
	}
	return adapter.streamChatCompletionsWithReconnect(ctx, req, baseURL, apiKey, modelID, sink)
}

// runOpenAIStreamWithReconnect 统一处理 OpenAI 两种流协议的连接级 pre-output 重连。
// 对自动生成的 prompt_cache_key，若上游 400 明确给出最大长度，会用该长度生成
// 确定性短键并重试一次；其他 HTTP 400 仍不在 adapter 层重试。
func (adapter *OpenAIAdapter) runOpenAIStreamWithReconnect(ctx context.Context, sink func(ModelEvent) error, adaptPromptCacheKey bool, stream func(int, func(ModelEvent) error) error) error {
	var connectionAttempt int
	promptCacheKeyMaximumLength := 0
	promptCacheKeyAdapted := false
	for {
		emitted := false
		wrappedSink := func(event ModelEvent) error {
			emitted = true
			return sink(event)
		}
		err := stream(promptCacheKeyMaximumLength, wrappedSink)
		if err == nil {
			return nil
		}
		if emitted {
			if IsStreamConnectionReset(err) {
				return fmt.Errorf("upstream stream interrupted mid-response (already forwarded partial content, will not reconnect to avoid duplicates): %w", err)
			}
			return err
		}
		if adaptPromptCacheKey && !promptCacheKeyAdapted {
			if maximumLength, ok := openAIPromptCacheKeyMaximumLength(err); ok {
				promptCacheKeyMaximumLength = maximumLength
				promptCacheKeyAdapted = true
				continue
			}
		}
		if !IsStreamConnectionReset(err) {
			return err
		}
		if ctx.Err() != nil {
			return err
		}
		connectionAttempt++
		if connectionAttempt > maxStreamReconnects {
			return fmt.Errorf("openai stream reconnect exhausted after %d attempts: %w", maxStreamReconnects, err)
		}
		backoff := providerRetryBaseDelay << (connectionAttempt - 1)
		if backoff > providerRetryMaxDelay {
			backoff = providerRetryMaxDelay
		}
		if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
			return sleepErr
		}
	}
}

func openAIPromptCacheKeyMaximumLength(err error) (int, bool) {
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr == nil || statusErr.StatusCode != http.StatusBadRequest {
		return 0, false
	}
	message := strings.ToLower(statusErr.Message + " " + statusErr.Body)
	if !strings.Contains(message, "prompt_cache_key") || !strings.Contains(message, "length") {
		return 0, false
	}
	matches := openAIPromptCacheKeyMaximumLengthPattern.FindStringSubmatch(message)
	if len(matches) != 2 {
		return 0, false
	}
	maximumLength, parseErr := strconv.Atoi(matches[1])
	if parseErr != nil || maximumLength <= 0 {
		return 0, false
	}
	return maximumLength, true
}

// streamChatCompletionsWithReconnect 包装 streamChatCompletions，实现 pre-output 透明重连。
// 当流式连接在转发任何有效内容给客户端之前断开（连接重置/EOF），
// 透明重试整个请求，客户端不会感知到中断。
// 一旦任何 ModelEvent 被转发给 sink，不再重连（避免重复输出）。
// 移植自 Reasonix streamWithReconnect 的 emitted 标记策略。
func (adapter *OpenAIAdapter) streamChatCompletionsWithReconnect(ctx context.Context, req StreamRequest, baseURL string, apiKey string, modelID string, sink func(ModelEvent) error) error {
	manualPromptCacheKey := hasExplicitOpenAIPromptCacheKey(req)
	return adapter.runOpenAIStreamWithReconnect(ctx, sink, !manualPromptCacheKey, func(maximumLength int, wrappedSink func(ModelEvent) error) error {
		return adapter.streamChatCompletions(ctx, req, baseURL, apiKey, modelID, maximumLength, manualPromptCacheKey, wrappedSink)
	})
}

// streamResponsesWithReconnect 为 Responses 路径补齐与 Chat Completions 相同的
// pre-output 保护；请求体在每次 streamResponses 调用中重新构建，因此不会复用已消费 body。
func (adapter *OpenAIAdapter) streamResponsesWithReconnect(ctx context.Context, req StreamRequest, baseURL string, apiKey string, modelID string, sink func(ModelEvent) error) error {
	manualPromptCacheKey := hasExplicitOpenAIPromptCacheKey(req)
	return adapter.runOpenAIStreamWithReconnect(ctx, sink, !manualPromptCacheKey, func(maximumLength int, wrappedSink func(ModelEvent) error) error {
		return adapter.streamResponses(ctx, req, baseURL, apiKey, modelID, maximumLength, manualPromptCacheKey, wrappedSink)
	})
}

func openAIChatRequestGroupUsesCompatShape(group string) bool {
	return strings.TrimSpace(group) == modelchannel.OpenAIRequestGroupChatCompletionsCompat
}

func openAIChatRequestBody(req StreamRequest, modelID string, promptCacheKeyMaximumLength int) openAIRequestBody {
	requestBody := openAIRequestBody{
		Model:    modelID,
		Stream:   true,
		Messages: []map[string]any{},
	}
	if shouldSendOpenAIMaxOutputTokens(modelID) {
		requestBody.MaxTokens = req.MaxTokens
	}
	if openAIChatRequestGroupUsesCompatShape(req.OpenAIRequestGroup) {
		return requestBody
	}
	requestBody.StreamOptions = map[string]any{"include_usage": true}
	if key := openAIPromptCacheKey(req, modelID, promptCacheKeyMaximumLength); key != "" {
		requestBody.PromptCacheKey = key
	}
	if strings.TrimSpace(req.ReasoningEffort) != "" {
		requestBody.ReasoningEffort = req.ReasoningEffort
	}
	return requestBody
}

func (adapter *OpenAIAdapter) streamChatCompletions(ctx context.Context, req StreamRequest, baseURL string, apiKey string, modelID string, promptCacheKeyMaximumLength int, manualPromptCacheKey bool, sink func(ModelEvent) error) error {
	startedAt := time.Now().UTC()
	finishedAt := time.Time{}
	overrideBody := cloneRequestBodyOverride(req.RequestBodyOverride)
	var body any = overrideBody
	if len(overrideBody) == 0 {
		normalizedMessages, err := normalizeOpenAIProviderMessages(req.Messages, strings.TrimSpace(req.ReasoningEffort) != "", isKimiOpenAIRequest(baseURL, modelID))
		if err != nil {
			finishedAt = time.Now().UTC()
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
			return err
		}
		requestBody := openAIChatRequestBody(req, modelID, promptCacheKeyMaximumLength)
		requestBody.Messages = normalizedMessages
		if len(req.Tools) > 0 {
			tools, err := normalizeOpenAIChatTools(req.Tools)
			if err != nil {
				finishedAt = time.Now().UTC()
				recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
				return err
			}
			requestBody.Tools = tools
		}
		body = requestBody
	} else {
		if !openAIChatRequestGroupUsesCompatShape(req.OpenAIRequestGroup) {

			applyOpenAIPromptCacheKeyOverride(overrideBody, req, modelID, promptCacheKeyMaximumLength)
		}
	}
	bodyMap, err := requestBodyToMap(body)
	if err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}
	applyOpenAIThinkingDisable(bodyMap, req, baseURL, modelID, req.OpenAIEndpoint)
	if err := ApplyOpenAIExtraParams(bodyMap, req.OpenAIExtraParamsEnabled, req.OpenAIExtraParamsJSON); err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}
	normalizeOpenAIRequestToolSchemas(bodyMap)
	applyOpenAIChatCompletionsCompatibility(bodyMap, baseURL, modelID, manualPromptCacheKey)
	if modelchannel.OpenAIRequestGroupSupportsAdvancedFields(req.OpenAIRequestGroup) {
		applyOpenAIServiceTier(bodyMap, req)
	}

	body = bodyMap
	requestURL := OpenAIEndpointURL(baseURL, req.OpenAIEndpoint)
	recordLLMRequestArtifact(req, "openai", modelID, "POST", requestURL, body)

	payload, err := json.Marshal(body)
	if err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}

	streamCtx, streamIdle := newProviderStreamIdleWatchdog(ctx, req.ProviderStreamIdleTimeout)
	defer streamIdle.Stop()

	resp, err := doProviderRequestWithGzipFallback(streamCtx, adapter.client, "openai", req.RequestID, req.ModelCallID, payload, requestURL, func(httpReq *http.Request) error {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("User-Agent", ClaudeCodeUserAgent)
		if err := ApplyCustomHeaders(httpReq, req.CustomHeadersEnabled, req.CustomHeadersJSON); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if idleErr := streamIdle.Err(); idleErr != nil {
			err = idleErr
		}
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}
	streamIdle.AttachBody(resp.Body)
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = buildHTTPStatusError("openai adapter", resp)
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}

	type openAIToolCallDelta struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	type openAIChunk struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Error     *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error,omitempty"`
		Choices []struct {
			Delta struct {
				Content          string                `json:"content"`
				ReasoningContent string                `json:"reasoning_content"`
				Reasoning        json.RawMessage       `json:"reasoning"`
				ReasoningDetails json.RawMessage       `json:"reasoning_details"`
				ToolCalls        []openAIToolCallDelta `json:"tool_calls"`
			} `json:"delta"`

			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Model string `json:"model"`
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			// DeepSeek 在顶层返回 prompt_cache_hit_tokens / prompt_cache_miss_tokens。
			PromptCacheHitTokens  int64 `json:"prompt_cache_hit_tokens"`
			PromptCacheMissTokens int64 `json:"prompt_cache_miss_tokens"`
			PromptTokensDetails   *struct {
				// OpenAI / MiMo 在嵌套结构里返回 cached_tokens。
				CachedTokens int64 `json:"cached_tokens"`
				// 部分中转（如包装 claude 的 OpenAI 兼容接口）在嵌套结构里返回 cache_creation_tokens，
				// 表示写入缓存的 token 数。对齐 anthropic.go 的语义记入 cacheWriteTokens。
				CacheCreationTokens int64 `json:"cache_creation_tokens"`
			} `json:"prompt_tokens_details,omitempty"`
		} `json:"usage,omitempty"`
	}

	tools := make(map[int]*openAIToolAccumulator)
	currentModel := modelID
	inputTokens := int64(0)
	outputTokens := int64(0)
	cacheReadTokens := int64(0)
	cacheWriteTokens := int64(0)
	usagePresent := false
	cacheReadPresent := false
	cacheWritePresent := false
	firstEventAt := time.Time{}
	finishReason := ""
	turnFinishedPending := false
	streamTerminated := false
	thinkingStarted := time.Time{}
	thinkingActive := false
	thinkParser := &openAIThinkTagParser{}
	flushThinkingCompleted := func() error {
		if !thinkingActive {
			return nil
		}
		duration := int32(time.Since(thinkingStarted).Milliseconds())
		if duration < 0 {
			duration = 0
		}
		if err := sink(ModelEvent{
			Kind:               ModelEventKindThinkingCompleted,
			OccurredAt:         time.Now().UTC(),
			Provider:           "openai",
			Model:              currentModel,
			ThinkingDurationMS: duration,
		}); err != nil {
			return err
		}
		thinkingActive = false
		thinkingStarted = time.Time{}
		return nil
	}
	flushTurnFinished := func() error {
		if !turnFinishedPending {
			return nil
		}
		turnFinishedPending = false
		return sink(ModelEvent{
			Kind:              ModelEventKindTurnFinished,
			OccurredAt:        time.Now().UTC(),
			Provider:          "openai",
			Model:             currentModel,
			InputTokens:       inputTokens,
			OutputTokens:      outputTokens,
			CacheReadTokens:   cacheReadTokens,
			CacheWriteTokens:  cacheWriteTokens,
			UsagePresent:      usagePresent,
			CacheReadPresent:  cacheReadPresent,
			CacheWritePresent: cacheWritePresent,
			FinishReason:      finishReason,
		})
	}
	emitTextDelta := func(text string) error {
		if text == "" {
			return nil
		}
		streamIdle.MarkEffectiveContent()
		return sink(ModelEvent{
			Kind:       ModelEventKindTextDelta,
			OccurredAt: time.Now().UTC(),
			Provider:   "openai",
			Model:      currentModel,
			Text:       text,
		})
	}

	emitThinkingDelta := func(reasoning string) error {
		if reasoning == "" {
			return nil
		}
		streamIdle.MarkEffectiveContent()
		if !thinkingActive {
			thinkingStarted = time.Now()
			thinkingActive = true
		}
		return sink(ModelEvent{
			Kind:          ModelEventKindThinkingDelta,
			OccurredAt:    time.Now().UTC(),
			Provider:      "openai",
			Model:         currentModel,
			Text:          reasoning,
			ThinkingStyle: agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT,
		})
	}
	emitTaggedContentParts := func(parts []openAIContentPart) error {
		for _, part := range parts {
			switch part.Kind {
			case openAIContentPartText:
				if err := emitTextDelta(part.Text); err != nil {
					return err
				}
			case openAIContentPartReasoning:
				if err := emitThinkingDelta(part.Text); err != nil {
					return err
				}
			case openAIContentPartThinkingCompleted:
				if err := flushThinkingCompleted(); err != nil {
					return err
				}
			}
		}
		return nil
	}
	flushTaggedContentTail := func() error {
		return emitTaggedContentParts(thinkParser.Flush())
	}
	fail := func(streamErr error) error {
		finishedAt = time.Now().UTC()
		logProviderStreamTiming("openai", currentModel, req, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, streamErr)
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", currentModel, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, streamErr))
		return streamErr
	}
	errorFromChunk := func(chunk openAIChunk) error {
		finishReason = "error"
		if chunk.Error != nil {
			parts := make([]string, 0, 4)
			if value := strings.TrimSpace(chunk.Error.Type); value != "" {
				parts = append(parts, "type="+value)
			}
			if value := strings.TrimSpace(chunk.Error.Code); value != "" {
				parts = append(parts, "code="+value)
			}
			if value := strings.TrimSpace(chunk.RequestID); value != "" {
				parts = append(parts, "request_id="+value)
			}
			if message := strings.TrimSpace(chunk.Error.Message); message != "" {
				if len(parts) > 0 {
					return fmt.Errorf("openai chat stream error %s: %s", strings.Join(parts, " "), message)
				}
				return fmt.Errorf("openai chat stream error: %s", message)
			}
			if len(parts) > 0 {
				return fmt.Errorf("openai chat stream error %s", strings.Join(parts, " "))
			}
		}
		return fmt.Errorf("openai chat stream error")
	}
	applyUsage := func(usage *struct {
		PromptTokens          int64 `json:"prompt_tokens"`
		CompletionTokens      int64 `json:"completion_tokens"`
		PromptCacheHitTokens  int64 `json:"prompt_cache_hit_tokens"`
		PromptCacheMissTokens int64 `json:"prompt_cache_miss_tokens"`
		PromptTokensDetails   *struct {
			CachedTokens        int64 `json:"cached_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_tokens"`
		} `json:"prompt_tokens_details,omitempty"`
	}) {
		if usage == nil {
			return
		}
		usagePresent = true
		promptTokens := usage.PromptTokens

		// 合并 DeepSeek 顶层格式和 OpenAI/MiMo 嵌套格式（参照 Reasonix normaliseUsage）。
		// DeepSeek: prompt_cache_hit_tokens / prompt_cache_miss_tokens（顶层）
		// OpenAI/MiMo: prompt_tokens_details.cached_tokens（嵌套）
		// 哪边非零用哪边。
		cachedTokens := int64(0)
		if usage.PromptCacheHitTokens > 0 {
			cacheReadPresent = true
			cachedTokens = usage.PromptCacheHitTokens
		} else if usage.PromptTokensDetails != nil {
			cacheReadPresent = true
			cachedTokens = usage.PromptTokensDetails.CachedTokens
		}

		// cache miss：DeepSeek 显式返回；OpenAI 无此字段时从 prompt-hit 推导。
		missTokens := int64(0)
		if usage.PromptCacheMissTokens > 0 {
			missTokens = usage.PromptCacheMissTokens
		} else if cachedTokens > 0 && promptTokens > cachedTokens {
			missTokens = promptTokens - cachedTokens
		}

		if promptTokens < 0 {
			promptTokens = 0
		}
		if cachedTokens < 0 {
			cachedTokens = 0
		}
		if cachedTokens > promptTokens {
			cachedTokens = promptTokens
		}
		inputTokens = missTokens
		if inputTokens <= 0 {
			inputTokens = promptTokens - cachedTokens
		}
		outputTokens = maxInt64(usage.CompletionTokens, 0)
		cacheReadTokens = cachedTokens
		// cache write：原生 OpenAI 协议无此概念（保持 0）；但包装 claude 的 OpenAI 兼容中转
		// 会在 prompt_tokens_details.cache_creation_tokens 返回缓存写入量，对齐 anthropic.go
		// 的语义（cache_creation → cacheWrite）记入 cacheWriteTokens，让统计准确。
		if usage.PromptTokensDetails != nil && usage.PromptTokensDetails.CacheCreationTokens > 0 {
			cacheWriteTokens = maxInt64(usage.PromptTokensDetails.CacheCreationTokens, 0)
		} else {
			cacheWriteTokens = 0
		}
		cacheWritePresent = true
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), openAIStreamMaxTokenSize)
	for scanner.Scan() {
		rawLine := scanner.Text()
		_, _ = appendLLMResponseArtifact(req, redactOpenAIStreamArtifactLine(rawLine)+"\n")
		line := strings.TrimSpace(rawLine)
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		if firstEventAt.IsZero() {
			firstEventAt = time.Now().UTC()
		}
		payloadLine := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payloadLine == "[DONE]" {
			streamTerminated = true
			if err := flushTaggedContentTail(); err != nil {
				return fail(err)
			}
			if err := flushThinkingCompleted(); err != nil {
				return fail(err)
			}
			if err := flushTurnFinished(); err != nil {
				return fail(err)
			}
			break
		}

		var chunk openAIChunk
		if err := json.Unmarshal([]byte(payloadLine), &chunk); err != nil {
			return fail(err)
		}
		if strings.TrimSpace(chunk.Type) == "error" || chunk.Error != nil {
			return fail(errorFromChunk(chunk))
		}
		if len(chunk.Choices) == 0 {
			if strings.TrimSpace(chunk.Model) != "" {
				currentModel = strings.TrimSpace(chunk.Model)
			}
			applyUsage(chunk.Usage)
			if err := flushTaggedContentTail(); err != nil {
				return fail(err)
			}
			if err := flushThinkingCompleted(); err != nil {
				return fail(err)
			}
			if err := flushTurnFinished(); err != nil {
				return fail(err)
			}
			continue
		}
		choice := chunk.Choices[0]
		if strings.TrimSpace(chunk.Model) != "" {
			currentModel = strings.TrimSpace(chunk.Model)
		}
		applyUsage(chunk.Usage)

		// Kimi K2 系列在思考阶段会把同一片段同时放进 content 与 reasoning_content，
		// 导致思考内容被当作正常文本重复输出。此处识别这种重复并跳过 content。
		reasoning := openAIChatDeltaReasoningText(choice.Delta.ReasoningContent, choice.Delta.Reasoning, choice.Delta.ReasoningDetails)
		if reasoning != "" {
			if err := emitThinkingDelta(reasoning); err != nil {
				return fail(err)
			}
		}
		// 当本片段同时携带 reasoning 与 content，且二者相同时，视为思考阶段的重复输出，跳过 content。
		skipContent := reasoning != "" && choice.Delta.Content == choice.Delta.ReasoningContent && choice.Delta.ReasoningContent != ""
		if text := choice.Delta.Content; text != "" && !skipContent {
			if err := emitTaggedContentParts(thinkParser.Consume(text)); err != nil {
				return fail(err)
			}
		}

		if len(choice.Delta.ToolCalls) > 0 && choice.Delta.Content == "" && choice.Delta.ReasoningContent == "" {
			if err := flushTaggedContentTail(); err != nil {
				return fail(err)
			}
			if err := flushThinkingCompleted(); err != nil {
				return fail(err)
			}
		}
		for _, item := range choice.Delta.ToolCalls {
			streamIdle.MarkEffectiveContent()
			accumulator, ok := tools[item.Index]
			if !ok {
				accumulator = &openAIToolAccumulator{}
				tools[item.Index] = accumulator
			}
			if strings.TrimSpace(item.ID) != "" {
				accumulator.CallID = namespaceToolCallID(req.ModelCallID, item.ID)
			}
			if strings.TrimSpace(item.Function.Name) != "" {
				accumulator.Name = strings.TrimSpace(item.Function.Name)
			}
			argsTextDelta := ""
			if item.Function.Arguments != "" {
				_, _ = accumulator.Args.WriteString(item.Function.Arguments)
				argsTextDelta = item.Function.Arguments
			}
			if argsTextDelta != "" || (strings.TrimSpace(accumulator.Name) == "CreatePlan" && accumulator.Args.Len() > 0) {
				if err := emitOpenAIToolProgress(sink, currentModel, accumulator, argsTextDelta); err != nil {
					return fail(err)
				}
			}
		}

		if choice.FinishReason != nil {
			streamTerminated = true
			if err := flushTaggedContentTail(); err != nil {
				return fail(err)
			}
			if err := flushThinkingCompleted(); err != nil {
				return fail(err)
			}
			for _, accumulator := range tools {
				argsJSON, argsErr := completedOpenAIToolArgsJSON(accumulator)
				if argsErr != nil {
					return fail(argsErr)
				}
				if err := sink(ModelEvent{
					Kind:       ModelEventKindToolLikeCompleted,
					OccurredAt: time.Now().UTC(),
					Provider:   "openai",
					Model:      currentModel,
					ToolInvocation: &runtimecore.ToolInvocation{
						CallID:   strings.TrimSpace(accumulator.CallID),
						ToolName: strings.TrimSpace(accumulator.Name),
						ArgsJSON: argsJSON,
					},
				}); err != nil {
					return fail(err)
				}
				streamIdle.MarkEffectiveContent()
			}
			tools = make(map[int]*openAIToolAccumulator)
			finishReason = strings.TrimSpace(*choice.FinishReason)
			turnFinishedPending = true
		}
	}
	for _, accumulator := range tools {
		argsJSON, argsErr := completedOpenAIToolArgsJSON(accumulator)
		if argsErr != nil {
			return fail(argsErr)
		}
		if err := sink(ModelEvent{
			Kind:       ModelEventKindToolLikeCompleted,
			OccurredAt: time.Now().UTC(),
			Provider:   "openai",
			Model:      currentModel,
			ToolInvocation: &runtimecore.ToolInvocation{
				CallID:   strings.TrimSpace(accumulator.CallID),
				ToolName: strings.TrimSpace(accumulator.Name),
				ArgsJSON: argsJSON,
			},
		}); err != nil {
			return fail(err)
		}
		streamIdle.MarkEffectiveContent()
	}
	if err := scanner.Err(); err != nil {
		if idleErr := streamIdle.Err(); idleErr != nil {
			return fail(idleErr)
		}
		return fail(err)
	}
	if !streamTerminated {
		return fail(fmt.Errorf("provider stream ended before terminal event"))
	}
	if err := flushTaggedContentTail(); err != nil {
		return fail(err)
	}
	if err := flushThinkingCompleted(); err != nil {
		return fail(err)
	}
	if err := flushTurnFinished(); err != nil {
		return fail(err)
	}
	finishedAt = time.Now().UTC()
	logProviderStreamTiming("openai", currentModel, req, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, nil)
	recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", currentModel, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, nil))
	return nil
}

func (adapter *OpenAIAdapter) streamResponses(ctx context.Context, req StreamRequest, baseURL string, apiKey string, modelID string, promptCacheKeyMaximumLength int, manualPromptCacheKey bool, sink func(ModelEvent) error) error {
	startedAt := time.Now().UTC()
	finishedAt := time.Time{}
	overrideBody := cloneRequestBodyOverride(req.RequestBodyOverride)
	var body any = overrideBody
	if len(overrideBody) == 0 {
		instructions, input, err := normalizeOpenAIResponsesInput(req.Messages)
		if err != nil {
			finishedAt = time.Now().UTC()
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
			return err
		}
		requestBody := openAIResponsesRequestBody{
			Model:        modelID,
			Instructions: instructions,
			Input:        input,
			Stream:       true,
			Store:        false,
		}
		if shouldSendOpenAIMaxOutputTokens(modelID) {
			requestBody.MaxOutputTokens = req.MaxTokens
		}
		if key := openAIPromptCacheKey(req, modelID, promptCacheKeyMaximumLength); key != "" {
			requestBody.PromptCacheKey = key
		}
		if len(req.Tools) > 0 {
			tools, err := normalizeOpenAIResponsesTools(req.Tools)
			if err != nil {
				finishedAt = time.Now().UTC()
				recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
				return err
			}
			if shouldExposeOpenAIResponsesImageGeneration(req, tools) {
				tools = ensureOpenAIResponsesImageGenerationTool(tools)
				if req.RequestKnobs != nil {
					req.RequestKnobs["openai_responses_image_generation_tool"] = "auto"
				}
			}
			requestBody.Tools = tools
		}
		if effort := strings.TrimSpace(req.ReasoningEffort); effort != "" {
			requestBody.Reasoning = &openAIResponsesReasoning{Effort: effort}
			requestBody.Include = []string{"reasoning.encrypted_content"}
		}
		body = requestBody
	} else {
		applyOpenAIPromptCacheKeyOverride(overrideBody, req, modelID, promptCacheKeyMaximumLength)
	}
	bodyMap, err := requestBodyToMap(body)
	if err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}
	applyOpenAIThinkingDisable(bodyMap, req, baseURL, modelID, req.OpenAIEndpoint)
	if err := ApplyOpenAIExtraParams(bodyMap, req.OpenAIExtraParamsEnabled, req.OpenAIExtraParamsJSON); err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}
	normalizeOpenAIResponsesRequestToolSchemas(bodyMap)
	applyOpenAIResponsesCompatibility(bodyMap, baseURL, modelID, manualPromptCacheKey)

	body = bodyMap

	requestURL := OpenAIEndpointURL(baseURL, req.OpenAIEndpoint)
	recordLLMRequestArtifact(req, "openai", modelID, "POST", requestURL, body)

	payload, err := json.Marshal(body)
	if err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}

	streamCtx, streamIdle := newProviderStreamIdleWatchdog(ctx, req.ProviderStreamIdleTimeout)
	defer streamIdle.Stop()

	resp, err := doProviderRequestWithGzipFallback(streamCtx, adapter.client, "openai", req.RequestID, req.ModelCallID, payload, requestURL, func(httpReq *http.Request) error {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("User-Agent", ClaudeCodeUserAgent)
		if err := ApplyCustomHeaders(httpReq, req.CustomHeadersEnabled, req.CustomHeadersJSON); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if idleErr := streamIdle.Err(); idleErr != nil {
			err = idleErr
		}
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}
	streamIdle.AttachBody(resp.Body)
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = buildHTTPStatusError("openai adapter", resp)
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}

	type openAIResponsesUsage struct {
		InputTokens        int64 `json:"input_tokens"`
		OutputTokens       int64 `json:"output_tokens"`
		InputTokensDetails *struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"input_tokens_details,omitempty"`
	}
	type openAIResponsesOutputContent struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type openAIResponsesOutputItem struct {
		ID               string                         `json:"id"`
		Type             string                         `json:"type"`
		Status           string                         `json:"status"`
		CallID           string                         `json:"call_id"`
		Name             string                         `json:"name"`
		Arguments        string                         `json:"arguments"`
		EncryptedContent string                         `json:"encrypted_content"`
		Summary          json.RawMessage                `json:"summary,omitempty"`
		Content          []openAIResponsesOutputContent `json:"content,omitempty"`
	}
	type openAIResponsesError struct {
		Message string          `json:"message"`
		Type    string          `json:"type"`
		Code    string          `json:"code"`
		Param   json.RawMessage `json:"param,omitempty"`
	}
	type openAIResponsesResponse struct {
		ID                string                      `json:"id"`
		Model             string                      `json:"model"`
		Status            string                      `json:"status"`
		Output            []openAIResponsesOutputItem `json:"output,omitempty"`
		OutputText        string                      `json:"output_text,omitempty"`
		Usage             *openAIResponsesUsage       `json:"usage,omitempty"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details,omitempty"`
		Error *openAIResponsesError `json:"error,omitempty"`
	}
	type openAIResponsesStreamEvent struct {
		Type            string                     `json:"type"`
		RequestID       string                     `json:"request_id"`
		Message         string                     `json:"message"`
		Code            string                     `json:"code"`
		Param           json.RawMessage            `json:"param,omitempty"`
		Delta           string                     `json:"delta"`
		Arguments       string                     `json:"arguments"`
		PartialImageB64 string                     `json:"partial_image_b64"`
		OutputFormat    string                     `json:"output_format"`
		OutputIndex     int                        `json:"output_index"`
		ItemID          string                     `json:"item_id"`
		Item            *openAIResponsesOutputItem `json:"item,omitempty"`
		Response        *openAIResponsesResponse   `json:"response,omitempty"`
		Error           *openAIResponsesError      `json:"error,omitempty"`
	}

	tools := make(map[string]*openAIToolAccumulator)
	completedTools := make(map[string]struct{})
	imageGenerations := make(map[string]*openAIImageGenerationAccumulator)
	completedImageGenerations := make(map[string]struct{})
	currentModel := modelID
	inputTokens := int64(0)
	outputTokens := int64(0)
	cacheReadTokens := int64(0)
	cacheWriteTokens := int64(0)
	usagePresent := false
	cacheReadPresent := false
	cacheWritePresent := false
	firstEventAt := time.Time{}
	finishReason := ""
	turnFinishedPending := false
	streamTerminated := false
	emittedToolInvocation := false
	emittedText := false
	thinkingStarted := time.Time{}
	thinkingActive := false
	emittedReasoningSignature := ""
	thinkParser := &openAIThinkTagParser{}
	toolKey := func(itemID string, outputIndex int) string {
		if strings.TrimSpace(itemID) != "" {
			return strings.TrimSpace(itemID)
		}
		return fmt.Sprintf("output:%d", outputIndex)
	}
	effectiveFinishReason := func() string {
		reason := strings.TrimSpace(finishReason)
		if emittedToolInvocation && (reason == "" || reason == "completed") {
			return "tool_calls"
		}
		return reason
	}
	flushThinkingCompleted := func() error {
		if !thinkingActive {
			return nil
		}
		duration := int32(time.Since(thinkingStarted).Milliseconds())
		if duration < 0 {
			duration = 0
		}
		if err := sink(ModelEvent{
			Kind:               ModelEventKindThinkingCompleted,
			OccurredAt:         time.Now().UTC(),
			Provider:           "openai",
			Model:              currentModel,
			ThinkingDurationMS: duration,
		}); err != nil {
			return err
		}
		thinkingActive = false
		thinkingStarted = time.Time{}
		return nil
	}
	flushTurnFinished := func() error {
		if !turnFinishedPending {
			return nil
		}
		turnFinishedPending = false
		return sink(ModelEvent{
			Kind:              ModelEventKindTurnFinished,
			OccurredAt:        time.Now().UTC(),
			Provider:          "openai",
			Model:             currentModel,
			InputTokens:       inputTokens,
			OutputTokens:      outputTokens,
			CacheReadTokens:   cacheReadTokens,
			CacheWriteTokens:  cacheWriteTokens,
			UsagePresent:      usagePresent,
			CacheReadPresent:  cacheReadPresent,
			CacheWritePresent: cacheWritePresent,
			FinishReason:      effectiveFinishReason(),
		})
	}
	emitTextDelta := func(text string) error {
		if text == "" {
			return nil
		}
		streamIdle.MarkEffectiveContent()
		if err := flushThinkingCompleted(); err != nil {
			return err
		}
		emittedText = true
		return sink(ModelEvent{
			Kind:       ModelEventKindTextDelta,
			OccurredAt: time.Now().UTC(),
			Provider:   "openai",
			Model:      currentModel,
			Text:       text,
		})
	}
	emitThinkingDelta := func(reasoning string) error {
		if reasoning == "" {
			return nil
		}
		streamIdle.MarkEffectiveContent()
		if !thinkingActive {
			thinkingStarted = time.Now()
			thinkingActive = true
		}
		return sink(ModelEvent{
			Kind:          ModelEventKindThinkingDelta,
			OccurredAt:    time.Now().UTC(),
			Provider:      "openai",
			Model:         currentModel,
			Text:          reasoning,
			ThinkingStyle: agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT,
		})
	}
	emitTaggedContentParts := func(parts []openAIContentPart) error {
		for _, part := range parts {
			switch part.Kind {
			case openAIContentPartText:
				if err := emitTextDelta(part.Text); err != nil {
					return err
				}
			case openAIContentPartReasoning:
				if err := emitThinkingDelta(part.Text); err != nil {
					return err
				}
			case openAIContentPartThinkingCompleted:
				if err := flushThinkingCompleted(); err != nil {
					return err
				}
			}
		}
		return nil
	}
	flushTaggedContentTail := func() error {
		return emitTaggedContentParts(thinkParser.Flush())
	}
	fail := func(streamErr error) error {
		finishedAt = time.Now().UTC()
		logProviderStreamTiming("openai", currentModel, req, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, streamErr)
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", currentModel, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, streamErr))
		return streamErr
	}
	applyUsage := func(usage *openAIResponsesUsage) {
		if usage == nil {
			return
		}
		usagePresent = true
		promptTokens := maxInt64(usage.InputTokens, 0)
		cachedTokens := int64(0)
		if usage.InputTokensDetails != nil {
			cacheReadPresent = true
			cachedTokens = maxInt64(usage.InputTokensDetails.CachedTokens, 0)
		}
		if cachedTokens > promptTokens {
			cachedTokens = promptTokens
		}
		inputTokens = promptTokens - cachedTokens
		outputTokens = maxInt64(usage.OutputTokens, 0)
		cacheReadTokens = cachedTokens
		cacheWriteTokens = 0
		cacheWritePresent = true
	}
	completeTool := func(key string, accumulator *openAIToolAccumulator) error {
		if accumulator == nil {
			return nil
		}
		completionKey := firstNonEmptyString(key, accumulator.CallID)
		if strings.TrimSpace(completionKey) == "" {
			completionKey = accumulator.Name + ":" + accumulator.Args.String()
		}
		if _, ok := completedTools[completionKey]; ok {
			return nil
		}
		if strings.TrimSpace(accumulator.CallID) != "" {
			if _, ok := completedTools[strings.TrimSpace(accumulator.CallID)]; ok {
				return nil
			}
		}
		completedTools[completionKey] = struct{}{}
		if strings.TrimSpace(accumulator.CallID) != "" {
			completedTools[strings.TrimSpace(accumulator.CallID)] = struct{}{}
		}
		argsJSON, argsErr := completedOpenAIToolArgsJSON(accumulator)
		if argsErr != nil {
			return argsErr
		}
		emittedToolInvocation = true
		if err := sink(ModelEvent{
			Kind:       ModelEventKindToolLikeCompleted,
			OccurredAt: time.Now().UTC(),
			Provider:   "openai",
			Model:      currentModel,
			ToolInvocation: &runtimecore.ToolInvocation{
				CallID:         strings.TrimSpace(accumulator.CallID),
				ToolName:       strings.TrimSpace(accumulator.Name),
				ArgsJSON:       argsJSON,
				ProviderItemID: strings.TrimSpace(accumulator.ProviderItemID),
				ProviderCallID: strings.TrimSpace(accumulator.ProviderCallID),
				ProviderStatus: strings.TrimSpace(accumulator.ProviderStatus),
			},
		}); err != nil {
			return err
		}
		streamIdle.MarkEffectiveContent()
		return nil
	}
	rememberImageGenerationItem := func(item openAIResponsesOutputItem, outputIndex int) *openAIImageGenerationAccumulator {
		key := toolKey(item.ID, outputIndex)
		accumulator, ok := imageGenerations[key]
		if !ok {
			accumulator = &openAIImageGenerationAccumulator{}
			imageGenerations[key] = accumulator
		}
		if itemID := strings.TrimSpace(item.ID); itemID != "" {
			accumulator.ProviderItemID = itemID
			accumulator.CallID = namespaceToolCallID(req.ModelCallID, itemID)
		}
		if status := strings.TrimSpace(item.Status); status != "" {
			accumulator.ProviderStatus = status
		}
		if strings.TrimSpace(accumulator.CallID) == "" {
			accumulator.CallID = namespaceToolCallID(req.ModelCallID, key)
		}
		return accumulator
	}
	emitImageGenerationStarted := func(accumulator *openAIImageGenerationAccumulator) error {
		if accumulator == nil || accumulator.StartedEmitted {
			return nil
		}
		callID := strings.TrimSpace(accumulator.CallID)
		if callID == "" {
			return nil
		}
		accumulator.StartedEmitted = true
		return sink(ModelEvent{
			Kind:       ModelEventKindPartialToolCall,
			OccurredAt: time.Now().UTC(),
			Provider:   "openai",
			Model:      currentModel,
			ToolCallID: callID,
			ToolCall: &agentv1.ToolCall{
				Tool: &agentv1.ToolCall_GenerateImageToolCall{
					GenerateImageToolCall: &agentv1.GenerateImageToolCall{
						Args: &agentv1.GenerateImageArgs{},
					},
				},
			},
		})
	}
	completeImageGeneration := func(key string, accumulator *openAIImageGenerationAccumulator) error {
		if accumulator == nil || strings.TrimSpace(accumulator.ImageData) == "" {
			return nil
		}
		completionKey := firstNonEmptyString(key, accumulator.CallID, accumulator.ProviderItemID)
		if strings.TrimSpace(completionKey) == "" {
			completionKey = accumulator.ImageData
		}
		if _, ok := completedImageGenerations[completionKey]; ok {
			return nil
		}
		if strings.TrimSpace(accumulator.CallID) != "" {
			if _, ok := completedImageGenerations[strings.TrimSpace(accumulator.CallID)]; ok {
				return nil
			}
		}
		completedImageGenerations[completionKey] = struct{}{}
		if strings.TrimSpace(accumulator.CallID) != "" {
			completedImageGenerations[strings.TrimSpace(accumulator.CallID)] = struct{}{}
		}
		argsPayload := map[string]string{"image_data": strings.TrimSpace(accumulator.ImageData)}
		argsJSON, err := json.Marshal(argsPayload)
		if err != nil {
			return err
		}
		emittedToolInvocation = true
		if err := sink(ModelEvent{
			Kind:       ModelEventKindToolLikeCompleted,
			OccurredAt: time.Now().UTC(),
			Provider:   "openai",
			Model:      currentModel,
			ToolInvocation: &runtimecore.ToolInvocation{
				CallID:         strings.TrimSpace(accumulator.CallID),
				ToolName:       "GenerateImage",
				ArgsJSON:       argsJSON,
				ProviderItemID: strings.TrimSpace(accumulator.ProviderItemID),
				ProviderStatus: strings.TrimSpace(accumulator.ProviderStatus),
			},
		}); err != nil {
			return err
		}
		streamIdle.MarkEffectiveContent()
		return nil
	}
	emitReasoningSignature := func(signature string, providerItemID string, providerStatus string, providerSummary json.RawMessage) error {
		trimmedSignature := strings.TrimSpace(signature)
		if trimmedSignature == "" || trimmedSignature == emittedReasoningSignature {
			return nil
		}
		duration := int32(0)
		if thinkingActive {
			duration = int32(time.Since(thinkingStarted).Milliseconds())
			if duration < 0 {
				duration = 0
			}
			thinkingActive = false
			thinkingStarted = time.Time{}
		}
		emittedReasoningSignature = trimmedSignature
		return sink(ModelEvent{
			Kind:                    ModelEventKindThinkingCompleted,
			OccurredAt:              time.Now().UTC(),
			Provider:                "openai",
			Model:                   currentModel,
			ThinkingDurationMS:      duration,
			ThinkingSignature:       trimmedSignature,
			ThinkingSignatureSource: ReasoningSignatureSourceOpenAIResponses,
			ProviderItemID:          strings.TrimSpace(providerItemID),
			ProviderStatus:          strings.TrimSpace(providerStatus),
			ProviderSummary:         cloneRawJSON(providerSummary),
		})
	}
	applyFunctionCallItem := func(item openAIResponsesOutputItem, outputIndex int, complete bool) error {
		if strings.TrimSpace(item.Type) != "function_call" {
			return nil
		}
		streamIdle.MarkEffectiveContent()
		key := toolKey(firstNonEmptyString(item.ID, item.CallID), outputIndex)
		accumulator, ok := tools[key]
		if !ok {
			accumulator = &openAIToolAccumulator{}
			tools[key] = accumulator
		}
		if strings.TrimSpace(item.ID) != "" {
			accumulator.ProviderItemID = strings.TrimSpace(item.ID)
		}
		if strings.TrimSpace(item.Status) != "" {
			accumulator.ProviderStatus = strings.TrimSpace(item.Status)
		}
		if strings.TrimSpace(item.CallID) != "" {
			accumulator.ProviderCallID = strings.TrimSpace(item.CallID)
			accumulator.CallID = namespaceToolCallID(req.ModelCallID, item.CallID)
		} else if strings.TrimSpace(item.ID) != "" {
			accumulator.CallID = namespaceToolCallID(req.ModelCallID, item.ID)
		}
		if strings.TrimSpace(item.Name) != "" {
			accumulator.Name = strings.TrimSpace(item.Name)
		}
		argsTextDelta := ""
		if item.Arguments != "" && accumulator.Args.Len() == 0 {
			_, _ = accumulator.Args.WriteString(item.Arguments)
			argsTextDelta = item.Arguments
		}
		if argsTextDelta != "" || (strings.TrimSpace(accumulator.Name) == "CreatePlan" && accumulator.Args.Len() > 0) {
			if err := emitOpenAIToolProgress(sink, currentModel, accumulator, argsTextDelta); err != nil {
				return err
			}
		}
		if complete {
			delete(tools, key)
			return completeTool(key, accumulator)
		}
		return nil
	}
	applyOutputItem := func(item openAIResponsesOutputItem, outputIndex int, complete bool) error {
		switch strings.TrimSpace(item.Type) {
		case "reasoning":
			return emitReasoningSignature(item.EncryptedContent, item.ID, item.Status, item.Summary)
		case "function_call":
			return applyFunctionCallItem(item, outputIndex, complete)
		case "image_generation_call":
			accumulator := rememberImageGenerationItem(item, outputIndex)
			if !complete {
				return emitImageGenerationStarted(accumulator)
			}
			key := toolKey(item.ID, outputIndex)
			delete(imageGenerations, key)
			return completeImageGeneration(key, accumulator)
		default:
			return nil
		}
	}
	errorFromEvent := func(event openAIResponsesStreamEvent) error {
		errorType := ""
		code := strings.TrimSpace(event.Code)
		message := strings.TrimSpace(event.Message)
		if event.Error != nil {
			errorType = strings.TrimSpace(event.Error.Type)
			code = firstNonEmptyString(strings.TrimSpace(event.Error.Code), code)
			message = firstNonEmptyString(strings.TrimSpace(event.Error.Message), message)
		}
		if event.Response != nil && event.Response.Error != nil {
			errorType = firstNonEmptyString(strings.TrimSpace(event.Response.Error.Type), errorType)
			code = firstNonEmptyString(strings.TrimSpace(event.Response.Error.Code), code)
			message = firstNonEmptyString(strings.TrimSpace(event.Response.Error.Message), message)
		}
		if message != "" {
			return fmt.Errorf("openai responses stream error %s: %s", openAIStreamErrorDetails(errorType, code, event.RequestID), message)
		}

		details := make([]string, 0, 5)
		if eventType := strings.TrimSpace(event.Type); eventType != "" {
			details = append(details, "event_type="+eventType)
		}
		if providerDetails := openAIStreamErrorDetails(errorType, code, event.RequestID); providerDetails != "provider_error" {
			details = append(details, providerDetails)
		}
		if event.Response != nil {
			if responseID := strings.TrimSpace(event.Response.ID); responseID != "" {
				details = append(details, "response_id="+responseID)
			}
			if status := strings.TrimSpace(event.Response.Status); status != "" {
				details = append(details, "status="+status)
			}
			if event.Response.IncompleteDetails != nil {
				if reason := strings.TrimSpace(event.Response.IncompleteDetails.Reason); reason != "" {
					details = append(details, "reason="+reason)
				}
			}
		}
		if len(details) == 0 {
			return fmt.Errorf("openai responses stream failed without error details")
		}
		return fmt.Errorf("openai responses stream failed %s", strings.Join(details, " "))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), openAIStreamMaxTokenSize)
	for scanner.Scan() {
		rawLine := scanner.Text()
		_, _ = appendLLMResponseArtifact(req, redactOpenAIStreamArtifactLine(rawLine)+"\n")
		line := strings.TrimSpace(rawLine)
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		if firstEventAt.IsZero() {
			firstEventAt = time.Now().UTC()
		}
		payloadLine := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payloadLine == "[DONE]" {
			streamTerminated = true
			if err := flushTaggedContentTail(); err != nil {
				return fail(err)
			}
			if err := flushThinkingCompleted(); err != nil {
				return fail(err)
			}
			for key, accumulator := range tools {
				if err := completeTool(key, accumulator); err != nil {
					return fail(err)
				}
			}
			for key, accumulator := range imageGenerations {
				if err := completeImageGeneration(key, accumulator); err != nil {
					return fail(err)
				}
			}
			if err := flushTurnFinished(); err != nil {
				return fail(err)
			}
			break
		}

		var event openAIResponsesStreamEvent
		if err := json.Unmarshal([]byte(payloadLine), &event); err != nil {
			return fail(err)
		}
		if event.Response != nil {
			if strings.TrimSpace(event.Response.Model) != "" {
				currentModel = strings.TrimSpace(event.Response.Model)
			}
			applyUsage(event.Response.Usage)
		}

		switch strings.TrimSpace(event.Type) {
		case "response.output_text.delta":
			if err := emitTaggedContentParts(thinkParser.Consume(event.Delta)); err != nil {
				return fail(err)
			}
		case "response.output_item.added":
			if event.Item != nil {
				if err := applyOutputItem(*event.Item, event.OutputIndex, false); err != nil {
					return fail(err)
				}
			}
		case "response.function_call_arguments.delta":
			key := toolKey(event.ItemID, event.OutputIndex)
			accumulator, ok := tools[key]
			if !ok {
				accumulator = &openAIToolAccumulator{}
				tools[key] = accumulator
			}
			if event.Delta != "" {
				_, _ = accumulator.Args.WriteString(event.Delta)
				streamIdle.MarkEffectiveContent()
				if err := emitOpenAIToolProgress(sink, currentModel, accumulator, event.Delta); err != nil {
					return fail(err)
				}
			}
		case "response.function_call_arguments.done":
			key := toolKey(event.ItemID, event.OutputIndex)
			accumulator, ok := tools[key]
			if !ok {
				accumulator = &openAIToolAccumulator{}
				tools[key] = accumulator
			}
			if event.Arguments != "" && accumulator.Args.Len() == 0 {
				_, _ = accumulator.Args.WriteString(event.Arguments)
				streamIdle.MarkEffectiveContent()
				if err := emitOpenAIToolProgress(sink, currentModel, accumulator, event.Arguments); err != nil {
					return fail(err)
				}
			}
		case "response.image_generation_call.partial_image":
			key := toolKey(event.ItemID, event.OutputIndex)
			accumulator, ok := imageGenerations[key]
			if !ok {
				accumulator = &openAIImageGenerationAccumulator{}
				imageGenerations[key] = accumulator
			}
			if itemID := strings.TrimSpace(event.ItemID); itemID != "" {
				accumulator.ProviderItemID = itemID
				accumulator.CallID = namespaceToolCallID(req.ModelCallID, itemID)
			}
			if strings.TrimSpace(accumulator.CallID) == "" {
				accumulator.CallID = namespaceToolCallID(req.ModelCallID, key)
			}
			if err := emitImageGenerationStarted(accumulator); err != nil {
				return fail(err)
			}
			if imageData := strings.TrimSpace(event.PartialImageB64); imageData != "" {
				accumulator.ImageData = imageData
				streamIdle.MarkEffectiveContent()
			}
			if outputFormat := strings.TrimSpace(event.OutputFormat); outputFormat != "" {
				accumulator.OutputFormat = outputFormat
			}
		case "response.output_item.done":
			if event.Item != nil {
				if err := applyOutputItem(*event.Item, event.OutputIndex, true); err != nil {
					return fail(err)
				}
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			streamIdle.MarkEffectiveContent()
			if err := emitThinkingDelta(event.Delta); err != nil {
				return fail(err)
			}
		case "response.completed", "response.incomplete":
			streamTerminated = true
			if event.Response != nil && !emittedText {
				if strings.TrimSpace(event.Response.OutputText) != "" {
					if err := emitTaggedContentParts(thinkParser.Consume(event.Response.OutputText)); err != nil {
						return fail(err)
					}
				} else {
					for _, item := range event.Response.Output {
						for _, content := range item.Content {
							if strings.TrimSpace(content.Type) != "output_text" && strings.TrimSpace(content.Type) != "text" {
								continue
							}
							if err := emitTaggedContentParts(thinkParser.Consume(content.Text)); err != nil {
								return fail(err)
							}
						}
					}
				}
			}
			if err := flushTaggedContentTail(); err != nil {
				return fail(err)
			}
			if err := flushThinkingCompleted(); err != nil {
				return fail(err)
			}
			if event.Response != nil {
				for index, item := range event.Response.Output {
					if err := applyOutputItem(item, index, true); err != nil {
						return fail(err)
					}
				}
				finishReason = strings.TrimSpace(event.Response.Status)
				if event.Response.IncompleteDetails != nil && strings.TrimSpace(event.Response.IncompleteDetails.Reason) != "" {
					finishReason = strings.TrimSpace(event.Response.IncompleteDetails.Reason)
				}
			}
			turnFinishedPending = true
		case "response.failed", "error":
			return fail(errorFromEvent(event))
		}
	}
	for key, accumulator := range tools {
		if err := completeTool(key, accumulator); err != nil {
			return fail(err)
		}
	}
	for key, accumulator := range imageGenerations {
		if err := completeImageGeneration(key, accumulator); err != nil {
			return fail(err)
		}
	}
	if err := scanner.Err(); err != nil {
		if idleErr := streamIdle.Err(); idleErr != nil {
			return fail(idleErr)
		}
		return fail(err)
	}
	if !streamTerminated {
		return fail(fmt.Errorf("provider stream ended before terminal event"))
	}
	if err := flushTaggedContentTail(); err != nil {
		return fail(err)
	}
	if err := flushThinkingCompleted(); err != nil {
		return fail(err)
	}
	if err := flushTurnFinished(); err != nil {
		return fail(err)
	}
	finishedAt = time.Now().UTC()
	logProviderStreamTiming("openai", currentModel, req, startedAt, firstEventAt, finishedAt, effectiveFinishReason(), inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, nil)
	recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", currentModel, startedAt, firstEventAt, finishedAt, effectiveFinishReason(), inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, nil))
	return nil
}

func trailingTagPrefixLength(text string, tag string) int {
	maxLen := len(text)
	if len(tag)-1 < maxLen {
		maxLen = len(tag) - 1
	}
	for size := maxLen; size > 0; size-- {
		if strings.HasSuffix(text, tag[:size]) {
			return size
		}
	}
	return 0
}

func maxInt64(value int64, floor int64) int64 {
	if value < floor {
		return floor
	}
	return value
}

func cloneRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func redactOpenAIStreamArtifactLine(rawLine string) string {
	line := strings.TrimSpace(rawLine)
	if !strings.HasPrefix(line, "data:") {
		return rawLine
	}
	payloadLine := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if !strings.Contains(payloadLine, "partial_image_b64") && !strings.Contains(payloadLine, "image_data") && !strings.Contains(payloadLine, "imageData") {
		return rawLine
	}
	var payload any
	if err := json.Unmarshal([]byte(payloadLine), &payload); err != nil {
		return rawLine
	}
	if !redactOpenAIImagePayloadFields(payload) {
		return rawLine
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return rawLine
	}
	return "data: " + string(encoded)
}

func redactOpenAIImagePayloadFields(value any) bool {
	changed := false
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if text, ok := child.(string); ok {
				switch key {
				case "partial_image_b64", "image_data", "imageData":
					item[key] = fmt.Sprintf("[base64 image data omitted from debug log; bytes=%d]", len(strings.TrimSpace(text)))
					changed = true
					continue
				}
			}
			if redactOpenAIImagePayloadFields(child) {
				changed = true
			}
		}
	case []any:
		for _, child := range item {
			if redactOpenAIImagePayloadFields(child) {
				changed = true
			}
		}
	}
	return changed
}

func emitOpenAIToolProgress(
	sink func(ModelEvent) error,
	model string,
	accumulator *openAIToolAccumulator,
	argsTextDelta string,
) error {
	if accumulator == nil {
		return nil
	}
	toolName := strings.TrimSpace(accumulator.Name)
	if toolName == "CreatePlan" {
		return emitCreatePlanToolProgress(
			sink,
			"openai",
			model,
			accumulator.CallID,
			accumulator.Args.String(),
			argsTextDelta,
			&accumulator.LastCreatePlanSnapshot,
		)
	}
	if toolName != "Write" && toolName != "PatchEdit" {
		return nil
	}

	rawArgs := accumulator.Args.String()
	path, pathFound, pathComplete := extractJSONStringFieldPrefix(rawArgs, "path")
	if !pathFound {
		path, pathFound, pathComplete = extractJSONStringFieldPrefix(rawArgs, "file_path")
	}
	if pathFound && pathComplete {
		trimmedPath := strings.TrimSpace(path)
		if trimmedPath != "" {
			pathChanged := trimmedPath != accumulator.LastEmittedPath
			accumulator.LastEmittedPath = trimmedPath
			if toolName == "PatchEdit" && pathChanged {
				if err := sink(ModelEvent{
					Kind:       ModelEventKindPartialToolCall,
					OccurredAt: time.Now().UTC(),
					Provider:   "openai",
					Model:      model,
					ToolCallID: strings.TrimSpace(accumulator.CallID),
					ToolCall: &agentv1.ToolCall{
						Tool: &agentv1.ToolCall_EditToolCall{
							EditToolCall: &agentv1.EditToolCall{
								Args: &agentv1.EditArgs{Path: trimmedPath},
							},
						},
					},
				}); err != nil {
					return err
				}
			}
			if toolName == "Write" && pathChanged {
				if err := sink(ModelEvent{
					Kind:          ModelEventKindPartialToolCall,
					OccurredAt:    time.Now().UTC(),
					Provider:      "openai",
					Model:         model,
					ToolCallID:    strings.TrimSpace(accumulator.CallID),
					ArgsTextDelta: argsTextDelta,
					ToolCall: &agentv1.ToolCall{
						Tool: &agentv1.ToolCall_EditToolCall{
							EditToolCall: &agentv1.EditToolCall{
								Args: &agentv1.EditArgs{Path: trimmedPath},
							},
						},
					},
				}); err != nil {
					return err
				}
			}
		}
	}
	streamContent, streamFound := extractToolStreamContentPrefix(rawArgs, toolName)
	if !streamFound {
		return nil
	}
	delta := suffixAfterCommonPrefix(accumulator.LastStreamContent, streamContent)
	if delta == "" {
		return nil
	}
	accumulator.LastStreamContent = streamContent
	return sink(ModelEvent{
		Kind:       ModelEventKindToolCallDelta,
		OccurredAt: time.Now().UTC(),
		Provider:   "openai",
		Model:      model,
		ToolCallID: strings.TrimSpace(accumulator.CallID),
		ToolCallDelta: &agentv1.ToolCallDelta{
			Delta: &agentv1.ToolCallDelta_EditToolCallDelta{
				EditToolCallDelta: &agentv1.EditToolCallDelta{
					StreamContentDelta: delta,
				},
			},
		},
	})
}

func normalizeOpenAIProviderMessages(messages []Message, thinkingEnabled bool, kimiReasoningReplay bool) ([]map[string]any, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	items := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		content, err := openAIContentValue(message)
		if err != nil {
			return nil, err
		}
		item := map[string]any{
			"role":    strings.TrimSpace(message.Role),
			"content": content,
		}
		if shouldIncludeOpenAIReasoningContent(message, thinkingEnabled, kimiReasoningReplay) {
			reasoningContent := message.ReasoningContent
			if kimiReasoningReplay && strings.TrimSpace(reasoningContent) == "" && len(message.ToolCalls) > 0 {
				reasoningContent = "tool call"
			}
			item["reasoning_content"] = reasoningContent
		}
		if len(message.ToolCalls) > 0 {
			item["tool_calls"] = normalizeToolCallDescriptors(message.ToolCalls)
		}
		if strings.TrimSpace(message.ToolCallID) != "" {
			item["tool_call_id"] = providerToolCallID(message.ToolCallID)
		}
		if strings.TrimSpace(message.Name) != "" {
			item["name"] = strings.TrimSpace(message.Name)
		}
		items = append(items, item)
	}
	return items, nil
}

func applyOpenAIServiceTier(body map[string]any, req StreamRequest) {
	if body == nil || req.Provider != "openai" {
		return
	}
	if _, exists := body["service_tier"]; exists {
		return
	}
	tier := strings.TrimSpace(req.OpenAIServiceTier)
	if req.FastMode {
		tier = "priority"
	}
	if tier != "" {
		body["service_tier"] = tier
	}
}

func openAIExtraParamsHasKey(req StreamRequest, key string) bool {
	if !req.OpenAIExtraParamsEnabled {
		return false
	}
	params, err := parseJSONMap(req.OpenAIExtraParamsJSON, "openai extra params json")
	if err != nil {
		return false
	}
	_, ok := params[strings.TrimSpace(key)]
	return ok
}

func applyOpenAIResponsesCompatibility(body map[string]any, baseURL string, modelID string, preservePromptCacheKey bool) {
	if len(body) == 0 {
		return
	}
	policy := classifyProviderCompatibility(baseURL, modelID)
	applyProviderCompatibilitySanitization(body, baseURL, modelID)
	if !policy.PromptCacheKey && !preservePromptCacheKey {
		delete(body, "prompt_cache_key")
	}
	if policy.Kind != "xai" {
		return
	}
	delete(body, "prompt_cache_retention")
	delete(body, "safety_identifier")
	delete(body, "reasoning_effort") // Grok/xAI Responses 端点同样不支持
	deleteOpenAIRequestKeyRecursive(body, "external_web_access")
	if policy.DropGrok45Sampling {
		delete(body, "presence_penalty")
		delete(body, "frequency_penalty")
		delete(body, "stop")
	}
	filterOpenAIResponsesTools(body)
}

func isXAIResponsesRequest(baseURL string, modelID string) bool {
	base := strings.ToLower(strings.TrimSpace(baseURL))
	model := strings.ToLower(strings.TrimSpace(modelID))
	return strings.Contains(base, "api.x.ai") || strings.Contains(model, "grok")
}

func isGrok45Model(modelID string) bool {
	model := strings.ToLower(strings.TrimSpace(modelID))
	return strings.Contains(model, "grok-4.5")
}

func deleteOpenAIRequestKeyRecursive(value any, key string) {
	switch typed := value.(type) {
	case map[string]any:
		delete(typed, key)
		for _, child := range typed {
			deleteOpenAIRequestKeyRecursive(child, key)
		}
	case []any:
		for _, child := range typed {
			deleteOpenAIRequestKeyRecursive(child, key)
		}
	}
}

func filterOpenAIResponsesTools(body map[string]any) {
	items, ok := body["tools"].([]any)
	if !ok {
		return
	}
	filtered := make([]any, 0, len(items))
	for _, item := range items {
		tool, ok := item.(map[string]any)
		if !ok || !openAIResponsesToolTypeAllowed(strings.TrimSpace(fmt.Sprint(tool["type"]))) {
			continue
		}
		filtered = append(filtered, tool)
	}
	if len(filtered) == 0 {
		delete(body, "tools")
		delete(body, "tool_choice")
		return
	}
	body["tools"] = filtered
	if !openAIResponsesToolChoiceValid(body["tool_choice"], filtered) {
		delete(body, "tool_choice")
	}
}

func openAIResponsesToolTypeAllowed(toolType string) bool {
	switch toolType {
	case "function", "web_search", "x_search", "image_generation", "collections_search", "file_search", "code_execution", "code_interpreter", "mcp", "shell":
		return true
	default:
		return false
	}
}

func openAIResponsesToolChoiceValid(choice any, tools []any) bool {
	if choice == nil {
		return true
	}
	if text, ok := choice.(string); ok {
		switch strings.TrimSpace(text) {
		case "", "auto", "none", "required":
			return true
		default:
			return false
		}
	}
	choiceMap, ok := choice.(map[string]any)
	if !ok {
		return false
	}
	if strings.TrimSpace(fmt.Sprint(choiceMap["type"])) != "function" {
		return false
	}
	name := strings.TrimSpace(asStringMapValue(choiceMap, "name"))
	if functionShape, ok := choiceMap["function"].(map[string]any); ok {
		name = strings.TrimSpace(asStringMapValue(functionShape, "name"))
	}
	if name == "" {
		return false
	}
	for _, item := range tools {
		tool, ok := item.(map[string]any)
		if ok && strings.TrimSpace(asStringMapValue(tool, "name")) == name {
			return true
		}
	}
	return false
}

func applyOpenAIChatCompletionsCompatibility(body map[string]any, baseURL string, modelID string, preservePromptCacheKey bool) {
	if len(body) == 0 {
		return
	}
	policy := classifyProviderCompatibility(baseURL, modelID)
	applyProviderCompatibilitySanitization(body, baseURL, modelID)
	kind := policy.Kind
	if !policy.PromptCacheKey && !preservePromptCacheKey {
		delete(body, "prompt_cache_key")
	}
	if kind == "" {
		return
	}
	if isKimiK27CodeModel(modelID) {
		body["thinking"] = map[string]any{"type": "enabled"}
	}
	effort, hasReasoningEffort := body["reasoning_effort"]
	if !hasReasoningEffort {
		return
	}
	switch kind {
	case "xai":
		// Grok/xAI 不使用 reasoning_effort 参数，直接删除避免 400
		// "Model xxx does not support parameter reasoningEffort"
		delete(body, "reasoning_effort")
	case "kimi":
		if isKimiK3Model(modelID) {
			body["reasoning_effort"] = kimiK3ReasoningEffort(effort)
		} else {
			delete(body, "reasoning_effort")
			ensureOpenAIThinkingEnabled(body)
		}
	case "openrouter":
		delete(body, "reasoning_effort")
		body["reasoning"] = map[string]any{"effort": openAIReasoningEffortString(effort)}
	case "siliconflow", "qwen":
		delete(body, "reasoning_effort")
		body["enable_thinking"] = true
	case "deepseek":
		ensureOpenAIThinkingEnabled(body)
	case "zhipu", "mimo", "minimax":
		delete(body, "reasoning_effort")
		ensureOpenAIThinkingEnabled(body)
	case "stepfun":
		if !stepFunModelSupportsReasoningEffort(modelID) {
			delete(body, "reasoning_effort")
			ensureOpenAIThinkingEnabled(body)
		} else {
			body["reasoning_effort"] = stepFunReasoningEffort(effort)
		}
	}
}

func ensureOpenAIThinkingEnabled(body map[string]any) {
	if len(body) == 0 {
		return
	}
	if _, ok := body["thinking"]; !ok {
		body["thinking"] = map[string]any{"type": "enabled"}
	}
}

func openAIChatCompatibilityKind(baseURL string, modelID string) string {
	base := strings.ToLower(strings.TrimSpace(baseURL))
	model := strings.ToLower(strings.TrimSpace(modelID))
	signal := base + " " + model
	switch {
	case strings.Contains(signal, "kimi") || strings.Contains(signal, "moonshot"):
		return "kimi"
	case strings.Contains(signal, "openrouter"):
		return "openrouter"
	case strings.Contains(signal, "siliconflow"):
		return "siliconflow"
	case strings.Contains(signal, "deepseek"):
		return "deepseek"
	case strings.Contains(signal, "bigmodel") || strings.Contains(signal, "z.ai") || strings.Contains(signal, "zhipu") || strings.Contains(model, "glm"):
		return "zhipu"
	case strings.Contains(signal, "dashscope") || strings.Contains(signal, "qwen") || strings.Contains(signal, "aliyun") || strings.Contains(signal, "bailian"):
		return "qwen"
	case strings.Contains(signal, "xiaomimimo") || strings.Contains(signal, "mimo"):
		return "mimo"
	case strings.Contains(signal, "minimax"):
		return "minimax"
	case strings.Contains(signal, "stepfun") || strings.Contains(signal, "step-"):
		return "stepfun"
	default:
		return ""
	}
}

func shouldSendOpenAIChatPromptCacheKey(baseURL string, modelID string) bool {
	return providerPromptCacheKeyAllowed(baseURL, modelID)
}

func isKimiOpenAIRequest(baseURL string, modelID string) bool {
	return openAIChatCompatibilityKind(baseURL, modelID) == "kimi"
}

func isKimiK27CodeModel(modelID string) bool {
	model := strings.ToLower(strings.TrimSpace(modelID))
	return strings.Contains(model, "kimi-k2.7-code")
}

func isKimiK3Model(modelID string) bool {
	model := strings.ToLower(strings.TrimSpace(modelID))
	return strings.Contains(model, "kimi-k3")
}

func openAIReasoningEffortString(value any) string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "low", "medium", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	default:
		return "high"
	}
}

func kimiK3ReasoningEffort(value any) string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "low":
		return "low"
	case "high", "medium", "xhigh":
		return "high"
	case "max":
		return "max"
	default:
		return "max"
	}
}

func stepFunModelSupportsReasoningEffort(modelID string) bool {
	model := strings.ToLower(strings.TrimSpace(modelID))
	return strings.Contains(model, "2603")
}

func stepFunReasoningEffort(value any) string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "low":
		return "low"
	case "high", "medium", "xhigh", "max":
		return "high"
	default:
		return "high"
	}
}

func shouldSendOpenAIMaxOutputTokens(modelID string) bool {
	return !strings.Contains(strings.ToLower(strings.TrimSpace(modelID)), "gpt")
}

func shouldIncludeOpenAIReasoningContent(message Message, thinkingEnabled bool, kimiReasoningReplay bool) bool {
	if strings.TrimSpace(message.ReasoningContent) != "" {
		return true
	}
	if !thinkingEnabled && !kimiReasoningReplay {
		return false
	}
	if strings.TrimSpace(message.Role) != "assistant" {
		return false
	}
	return len(message.ToolCalls) > 0
}

func applyOpenAIThinkingDisable(body map[string]any, req StreamRequest, baseURL string, modelID string, endpoint string) {
	if len(body) == 0 || normalizeRuntimeThinkingEffort(req.ThinkingEffort) != "disabled" {
		return
	}
	switch openAIThinkingDisableKind(baseURL, modelID, endpoint) {
	case "thinking_type":
		body["thinking"] = map[string]any{"type": "disabled"}
		delete(body, "reasoning_effort")
		setRequestKnob(req, "thinking_disabled_provider_param", "thinking.type")
	case "enable_thinking":
		body["enable_thinking"] = false
		delete(body, "reasoning_effort")
		setRequestKnob(req, "thinking_disabled_provider_param", "enable_thinking")
	case "reasoning_object_none":
		body["reasoning"] = map[string]any{"effort": "none"}
		delete(body, "reasoning_effort")
		setRequestKnob(req, "thinking_disabled_provider_param", "reasoning.effort")
	case "reasoning_none":
		if modelchannel.OpenAIEndpointShape(endpoint) == "responses" {
			body["reasoning"] = map[string]any{"effort": "none"}
		} else {
			body["reasoning_effort"] = "none"
		}
		setRequestKnob(req, "thinking_disabled_provider_param", "reasoning.effort")

	}
}

func openAIThinkingDisableKind(baseURL string, modelID string, endpoint string) string {
	_ = endpoint
	return classifyProviderCompatibility(baseURL, modelID).ThinkingDisableKind
}

func openAIModelSupportsReasoningNone(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(model, "gpt-6") {
		return true
	}
	if strings.Contains(model, "gpt-5.1") {
		return true
	}
	if !strings.HasPrefix(model, "gpt-5.") {
		return false
	}
	minorText := strings.TrimPrefix(model, "gpt-5.")
	minorEnd := 0
	for minorEnd < len(minorText) && minorText[minorEnd] >= '0' && minorText[minorEnd] <= '9' {
		minorEnd++
	}
	if minorEnd == 0 {
		return false
	}
	minor, err := strconv.Atoi(minorText[:minorEnd])
	return err == nil && minor >= 1
}

func setRequestKnob(req StreamRequest, key string, value any) {
	if req.RequestKnobs == nil {
		return
	}
	req.RequestKnobs[key] = value
}

func normalizeOpenAIResponsesInput(messages []Message) (string, []map[string]any, error) {
	if len(messages) == 0 {
		return "", nil, nil
	}
	instructionParts := make([]string, 0, 2)
	items := make([]map[string]any, 0, len(messages))
	responsesCallIDs := make(map[string]string)
	activeAssistantReasoningKey := ""
	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		if role == "system" {
			if text := openAIResponsesMessageText(message); strings.TrimSpace(text) != "" {
				instructionParts = append(instructionParts, strings.TrimSpace(text))
			}
			activeAssistantReasoningKey = ""
			continue
		}
		if role == "tool" && strings.TrimSpace(message.ToolCallID) != "" {
			callID := openAIResponsesToolMessageCallID(message, responsesCallIDs)
			items = append(items, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  openAIResponsesMessageText(message),
			})
			activeAssistantReasoningKey = ""
			continue
		}
		if role != "assistant" {
			activeAssistantReasoningKey = ""
		}
		if shouldIncludeOpenAIResponsesReasoningItem(message) {
			reasoningKey := openAIResponsesReasoningReplayKey(message)
			if reasoningKey != activeAssistantReasoningKey {
				items = append(items, openAIResponsesReasoningItem(message))
				activeAssistantReasoningKey = reasoningKey
			}
		}
		if strings.TrimSpace(message.Content) != "" || len(message.ContentParts) > 0 {
			content, err := openAIResponsesMessageContent(message, role == "assistant")
			if err != nil {
				return "", nil, err
			}
			if len(content) > 0 {
				items = append(items, map[string]any{
					"role":    openAIResponsesMessageRole(role),
					"content": content,
				})
			}
		}
		if role == "assistant" && len(message.ToolCalls) > 0 {
			for _, toolCall := range message.ToolCalls {
				name := strings.TrimSpace(toolCall.Function.Name)
				if name == "" {
					continue
				}
				callID := openAIResponsesToolCallCallID(toolCall)
				if strings.TrimSpace(callID) == "" {
					callID = openAIResponsesProviderCallID(name)
				}
				if internalID := strings.TrimSpace(toolCall.ID); internalID != "" && strings.TrimSpace(callID) != "" {
					responsesCallIDs[internalID] = strings.TrimSpace(callID)
				}
				toolItem := map[string]any{
					"type":      "function_call",
					"call_id":   callID,
					"name":      sanitizeOpenAIResponsesToolName(name),
					"arguments": toolCall.Function.Arguments,
				}
				if itemID := strings.TrimSpace(toolCall.OpenAIResponsesID); itemID != "" {
					toolItem["id"] = itemID
				}
				if status := strings.TrimSpace(toolCall.OpenAIResponsesStatus); status != "" {
					toolItem["status"] = status
				} else {
					toolItem["status"] = "completed"
				}
				items = append(items, toolItem)
			}
		}
	}
	return strings.Join(instructionParts, "\n\n"), items, nil
}

func openAIResponsesReasoningReplayKey(message Message) string {
	return strings.Join([]string{
		strings.TrimSpace(message.ReasoningSignature),
		strings.TrimSpace(message.OpenAIResponsesReasoningID),
		strings.TrimSpace(message.OpenAIResponsesReasoningStatus),
		string(message.OpenAIResponsesReasoningSummary),
	}, "\x00")
}

func openAIResponsesReasoningItem(message Message) map[string]any {
	reasoningItem := map[string]any{
		"type":              "reasoning",
		"encrypted_content": strings.TrimSpace(message.ReasoningSignature),
	}
	if reasoningID := strings.TrimSpace(message.OpenAIResponsesReasoningID); reasoningID != "" {
		reasoningItem["id"] = reasoningID
	}
	if reasoningStatus := strings.TrimSpace(message.OpenAIResponsesReasoningStatus); reasoningStatus != "" {
		reasoningItem["status"] = reasoningStatus
	}
	if len(message.OpenAIResponsesReasoningSummary) > 0 {
		reasoningItem["summary"] = json.RawMessage(append([]byte(nil), message.OpenAIResponsesReasoningSummary...))
	} else {
		reasoningItem["summary"] = []any{}
	}
	return reasoningItem
}

func shouldIncludeOpenAIResponsesReasoningItem(message Message) bool {
	if strings.TrimSpace(message.Role) != "assistant" || strings.TrimSpace(message.ReasoningSignature) == "" {
		return false
	}
	return strings.TrimSpace(message.ReasoningSignatureSource) == ReasoningSignatureSourceOpenAIResponses
}

func openAIResponsesToolMessageCallID(message Message, responsesCallIDs map[string]string) string {
	internalID := strings.TrimSpace(message.ToolCallID)
	if internalID == "" {
		return ""
	}
	if callID := strings.TrimSpace(responsesCallIDs[internalID]); callID != "" {
		return callID
	}
	return openAIResponsesProviderCallID(internalID)
}

func openAIResponsesToolCallCallID(toolCall ToolCallDescriptor) string {
	if callID := strings.TrimSpace(toolCall.OpenAIResponsesCallID); callID != "" {
		return callID
	}
	return openAIResponsesProviderCallID(toolCall.ID)
}

func openAIResponsesProviderCallID(toolCallID string) string {
	trimmed := strings.TrimSpace(toolCallID)
	if trimmed == "" {
		return ""
	}
	if _, raw, ok := splitLegacyToolCallID(trimmed); ok {
		return raw
	}
	if strings.HasPrefix(trimmed, "tc_") {
		parts := strings.SplitN(trimmed, "_", 3)
		if len(parts) == 3 && strings.TrimSpace(parts[2]) != "" {
			return strings.TrimSpace(parts[2])
		}
	}
	return providerToolCallID(trimmed)
}

func openAIResponsesMessageRole(role string) string {
	switch strings.TrimSpace(role) {
	case "assistant":
		return "assistant"
	default:
		return "user"
	}
}

func openAIResponsesMessageText(message Message) string {
	if strings.TrimSpace(message.Content) != "" {
		return message.Content
	}
	if len(message.ContentParts) > 0 {
		return collapseTextContentParts(message.ContentParts)
	}
	return ""
}

func openAIResponsesMessageContent(message Message, assistant bool) ([]map[string]any, error) {
	textType := "input_text"
	if assistant {
		textType = "output_text"
	}
	if !hasImageContentParts(message.ContentParts) {
		text := openAIResponsesMessageText(message)
		if text == "" {
			return nil, nil
		}
		return []map[string]any{{
			"type": textType,
			"text": text,
		}}, nil
	}
	parts := make([]map[string]any, 0, len(message.ContentParts)+1)
	if len(message.ContentParts) == 0 && strings.TrimSpace(message.Content) != "" {
		parts = append(parts, map[string]any{
			"type": textType,
			"text": message.Content,
		})
	}
	for _, part := range message.ContentParts {
		switch normalizeContentPartType(part.Type) {
		case contentPartTypeText:
			if part.Text == "" {
				continue
			}
			parts = append(parts, map[string]any{
				"type": textType,
				"text": part.Text,
			})
		case contentPartTypeImage:
			dataURL, err := imageContentDataURL(part.Image)
			if err != nil {
				return nil, err
			}
			parts = append(parts, map[string]any{
				"type":      "input_image",
				"image_url": dataURL,
			})
		default:
			return nil, fmt.Errorf("unsupported openai responses content part type: %s", strings.TrimSpace(part.Type))
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	return parts, nil
}

func normalizeOpenAIResponsesTools(items []json.RawMessage) ([]map[string]any, error) {
	if len(items) == 0 {
		return nil, nil
	}
	tools := make([]map[string]any, 0, len(items))
	for _, item := range items {
		var raw map[string]any
		if err := json.Unmarshal(item, &raw); err != nil {
			return nil, fmt.Errorf("decode openai responses tool descriptor failed: %w", err)
		}
		source := raw
		if functionShape, ok := raw["function"].(map[string]any); ok {
			source = functionShape
		}
		name := strings.TrimSpace(asStringMapValue(source, "name"))
		if name == "" {
			return nil, fmt.Errorf("openai responses tool descriptor name is required")
		}
		// OpenAI Responses API 要求工具名称只能包含 a-zA-Z0-9_-
		sanitizedName := sanitizeOpenAIResponsesToolName(name)
		tool := map[string]any{
			"type": "function",
			"name": sanitizedName,
		}
		if description := strings.TrimSpace(asStringMapValue(source, "description")); description != "" {
			tool["description"] = description
		}
		if parameters, ok := source["parameters"]; ok && parameters != nil {
			normalizeOpenAIToolParameterSchema(parameters)
			tool["parameters"] = parameters
		} else {
			tool["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}

		if strict, ok := source["strict"]; ok {
			tool["strict"] = strict
		} else if strict, ok := raw["strict"]; ok {
			tool["strict"] = strict
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

func normalizeOpenAIChatTools(items []json.RawMessage) ([]json.RawMessage, error) {
	if len(items) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(items))
	tools := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		var value map[string]any
		if err := json.Unmarshal(item, &value); err != nil {
			return nil, fmt.Errorf("decode openai chat tool descriptor failed: %w", err)
		}
		if !normalizeOpenAIToolDescriptor(value) {
			continue
		}
		name := openAIToolDescriptorName(value)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}

		payload, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode openai chat tool descriptor failed: %w", err)
		}
		tools = append(tools, payload)
	}
	return tools, nil
}

func normalizeOpenAIResponsesRequestToolSchemas(body map[string]any) {
	if len(body) == 0 {
		return
	}
	items, ok := body["tools"].([]any)
	if !ok {
		return
	}
	for _, item := range items {
		tool, ok := item.(map[string]any)
		if !ok || strings.TrimSpace(fmt.Sprint(tool["type"])) != "function" {
			continue
		}
		normalizeOpenAIToolParameters(tool)
	}
}

func normalizeOpenAIRequestToolSchemas(body map[string]any) {
	if len(body) == 0 {
		return
	}
	tools, ok := body["tools"]
	if !ok {
		return
	}
	filtered := normalizeOpenAIToolDescriptorList(tools)
	if len(filtered) == 0 {
		delete(body, "tools")
		delete(body, "tool_choice")
		delete(body, "parallel_tool_calls")
		return
	}
	body["tools"] = filtered
	if !openAIToolChoiceValid(body["tool_choice"], filtered) {
		delete(body, "tool_choice")
	}
}

func openAIToolChoiceValid(choice any, tools []any) bool {
	if choice == nil {
		return true
	}
	text := strings.TrimSpace(fmt.Sprint(choice))
	switch text {
	case "", "auto", "none", "required":
		return true
	}
	choiceMap, ok := choice.(map[string]any)
	if !ok {
		return false
	}
	if strings.TrimSpace(fmt.Sprint(choiceMap["type"])) != "function" {
		return false
	}
	name := strings.TrimSpace(asStringMapValue(choiceMap, "name"))
	if functionShape, ok := choiceMap["function"].(map[string]any); ok {
		name = strings.TrimSpace(asStringMapValue(functionShape, "name"))
	}
	if name == "" {
		return false
	}
	for _, item := range tools {
		tool, ok := item.(map[string]any)
		if ok && openAIToolDescriptorName(tool) == name {
			return true
		}
	}
	return false
}

func normalizeOpenAIToolDescriptorList(value any) []any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	filtered := make([]any, 0, len(items))
	for _, item := range items {
		tool, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if !normalizeOpenAIToolDescriptor(tool) {
			continue
		}
		name := openAIToolDescriptorName(tool)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		filtered = append(filtered, tool)
	}
	return filtered
}

func normalizeOpenAIToolDescriptor(tool map[string]any) bool {
	if len(tool) == 0 {
		return false
	}
	toolType := strings.TrimSpace(fmt.Sprint(tool["type"]))
	if toolType == "" {
		tool["type"] = "function"
		toolType = "function"
	}
	if toolType != "function" {
		return false
	}
	if functionShape, ok := tool["function"].(map[string]any); ok {
		if strings.TrimSpace(asStringMapValue(functionShape, "name")) == "" {
			return false
		}
		normalizeOpenAIToolParameters(functionShape)
		return true
	}
	if strings.TrimSpace(asStringMapValue(tool, "name")) == "" {
		return false
	}
	normalizeOpenAIToolParameters(tool)
	return true
}

func openAIToolDescriptorName(tool map[string]any) string {
	if len(tool) == 0 {
		return ""
	}
	if functionShape, ok := tool["function"].(map[string]any); ok {
		return strings.TrimSpace(asStringMapValue(functionShape, "name"))
	}
	return strings.TrimSpace(asStringMapValue(tool, "name"))
}

// sanitizeOpenAIResponsesToolName 规范化工具名称以符合 OpenAI Responses API 的要求：
// 只允许 a-zA-Z0-9_- 字符。非法字符替换为下划线，首尾非法字符删除。
func sanitizeOpenAIResponsesToolName(name string) string {
	if name == "" {
		return ""
	}
	var result strings.Builder
	result.Grow(len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			result.WriteRune(r)
		} else if result.Len() > 0 {
			// 非法字符替换为下划线，但避免连续下划线和开头下划线
			if lastChar := result.String()[result.Len()-1]; lastChar != '_' {
				result.WriteRune('_')
			}
		}
	}
	// 删除尾部下划线
	sanitized := strings.TrimRight(result.String(), "_")
	if sanitized == "" {
		return "tool"
	}
	return sanitized
}

func normalizeOpenAIToolParameters(tool map[string]any) {
	if len(tool) == 0 {
		return
	}
	parameters, ok := tool["parameters"]
	if !ok || parameters == nil {
		tool["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		return
	}
	if _, ok := parameters.(map[string]any); !ok {
		tool["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		return
	}
	normalizeOpenAIToolParameterSchema(parameters)
}

func normalizeOpenAIToolParameterSchema(value any) {
	switch typed := value.(type) {
	case map[string]any:
		if _, ok := typed["type"]; !ok {
			typed["type"] = "object"
		}
		if _, ok := typed["properties"]; !ok {
			typed["properties"] = map[string]any{}
		}
		normalizeOpenAIToolSchemaRequired(typed)
	case []any:
		for _, child := range typed {
			normalizeOpenAIToolParameterSchema(child)
		}
	}
}

func normalizeOpenAIToolSchemaRequired(value any) {
	switch typed := value.(type) {
	case map[string]any:
		if strings.TrimSpace(fmt.Sprint(typed["format"])) == "uri" {
			delete(typed, "format")
		}
		if required, ok := typed["required"]; ok && required == nil {
			typed["required"] = []any{}
		}
		// 对 type:object 的 schema，若完全缺失 required 字段，主动补空数组。
		// 省略 required 本是合法 JSON Schema，但部分中转网关（如 daoxe）转发给
		// 上游（xAI 等）时会自动补 required:null，触发严格校验的
		// [standard_violation] /required: null is not of type "array"。
		// 主动补 [] 可堵住这一转换路径。
		if strings.TrimSpace(fmt.Sprint(typed["type"])) == "object" {
			if _, ok := typed["required"]; !ok {
				typed["required"] = []any{}
			}
		}
		// 对 required 数组排序，使序列化后的字节表示稳定。
		// required 的顺序在语义上无意义，但不同的顺序会产生不同的 JSON 字节，
		// 导致 provider 侧 prefix cache 失效（移植自 Reasonix schema_canonicalize）。
		sortOpenAISchemaRequiredArray(typed)
		for _, child := range typed {
			normalizeOpenAIToolSchemaRequired(child)
		}
	case []any:
		for _, child := range typed {
			normalizeOpenAIToolSchemaRequired(child)
		}
	}
}

// sortOpenAISchemaRequiredArray 对 schema map 的 required 数组做确定性排序。
// 只在数组长度 > 1 时排序（单个元素无需排序）。
func sortOpenAISchemaRequiredArray(schema map[string]any) {
	raw, ok := schema["required"]
	if !ok {
		return
	}
	arr, ok := raw.([]any)
	if !ok || len(arr) <= 1 {
		return
	}
	strs := make([]string, 0, len(arr))
	for _, item := range arr {
		strs = append(strs, fmt.Sprint(item))
	}
	sort.Strings(strs)
	sorted := make([]any, len(strs))
	for i, s := range strs {
		sorted[i] = s
	}
	schema["required"] = sorted
}

func asStringMapValue(source map[string]any, key string) string {
	if len(source) == 0 {
		return ""
	}
	switch value := source[key].(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		return ""
	}
}

func openAIChatDeltaReasoningText(reasoningContent string, reasoning json.RawMessage, reasoningDetails json.RawMessage) string {
	parts := make([]string, 0, 3)
	if reasoningContent != "" {
		parts = append(parts, reasoningContent)
	}
	if text := openAIReasoningRawText(reasoning); text != "" {
		parts = append(parts, text)
	}
	if text := openAIReasoningRawText(reasoningDetails); text != "" {
		parts = append(parts, text)
	}
	return strings.Join(parts, "")
}

func openAIReasoningRawText(raw json.RawMessage) string {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return openAIReasoningValueText(value)
}

func openAIReasoningValueText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := openAIReasoningValueText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	case map[string]any:
		for _, key := range []string{"reasoning_content", "content", "text", "summary"} {
			if text := openAIReasoningValueText(typed[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func openAIStreamErrorDetails(errorType string, code string, requestID string) string {
	parts := make([]string, 0, 3)
	if value := strings.TrimSpace(errorType); value != "" {
		parts = append(parts, "type="+value)
	}
	if value := strings.TrimSpace(code); value != "" {
		parts = append(parts, "code="+value)
	}
	if value := strings.TrimSpace(requestID); value != "" {
		parts = append(parts, "request_id="+value)
	}
	if len(parts) == 0 {
		return "provider_error"
	}
	return strings.Join(parts, " ")
}
