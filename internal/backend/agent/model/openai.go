// openai.go 实现 OpenAI 兼容流式适配器。
package modeladapter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cursor/gen/agentv1"
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
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
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
		// 容错恢复：部分上游模型流式输出工具参数时会重复拼接多份对象草稿
		// （{"paths":[...]}{"paths":[...]}），且 Windows 路径等反斜杠漏转义。
		// 取最后一个完整对象并修复非法转义后重试；恢复失败再报原始解析错误。
		if recovered, rerr := recoverOpenAIToolArgsJSON(trimmed); rerr == nil {
			return recovered, nil
		}
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

// recoverOpenAIToolArgsJSON 尝试修复畸形工具参数 JSON。
func recoverOpenAIToolArgsJSON(input string) ([]byte, error) {
	candidates := make([]string, 0, 4)
	candidates = append(candidates, input)
	// 场景一：多份对象重复拼接，取最后一个 '{' 开始的片段
	if strings.Contains(input, "}{") {
		if last := strings.LastIndex(input, "{"); last >= 0 {
			candidates = append(candidates, input[last:])
		}
	}
	for _, candidate := range candidates {
		for _, text := range []string{candidate, repairOpenAIToolArgsEscapes(candidate)} {
			var value map[string]any
			if err := json.Unmarshal([]byte(text), &value); err == nil && value != nil {
				return []byte(text), nil
			}
		}
	}
	return nil, fmt.Errorf("unrecoverable malformed tool input")
}

// repairOpenAIToolArgsEscapes 修复 JSON 字符串里漏转义的反斜杠：
// 反斜杠后跟非合法转义字符（如 Windows 路径 "e:\MyProject" 的 \M）时补一个反斜杠。
func repairOpenAIToolArgsEscapes(input string) string {
	var b strings.Builder
	b.Grow(len(input) + 8)
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if ch == '\\' && i+1 < len(input) {
			next := input[i+1]
			if isJSONEscapeChar(next) {
				b.WriteByte(ch)
				b.WriteByte(next)
				i++
				continue
			}
			b.WriteString(`\\`)
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func isJSONEscapeChar(ch byte) bool {
	switch ch {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
		return true
	}
	return false
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

// toJSONAnySlice 将 []map[string]any 转为 json.Unmarshal 到 map[string]any 后
// 的等价 []any 形态（元素保持 map[string]any 引用），保证下游 apply 函数的
// 类型断言（.([]any) / .(map[string]any)）行为与 requestBodyToMap 往返后一致。
// 输入为 nil 时返回 nil（对应 json 中的 null），与无 omitempty 字段的输出一致。
func toJSONAnySlice(items []map[string]any) []any {
	if items == nil {
		return nil
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

// rawMessagesToJSONAny 将 []json.RawMessage 逐个解码为 json.Unmarshal 后的
// 等价 any 形态（工具描述经 json 往返后为 []any），供直接构造请求体 map 使用。
func rawMessagesToJSONAny(items []json.RawMessage) ([]any, error) {
	if items == nil {
		return nil, nil
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		var value any
		if err := json.Unmarshal(item, &value); err != nil {
			return nil, fmt.Errorf("decode raw tool descriptor: %w", err)
		}
		out = append(out, value)
	}
	return out, nil
}

// normalizeOpenAIChatMessageToolCallsJSONShape 把 normalizeOpenAIProviderMessages
// 产出的 messages 中 tool_calls（[]providerToolCallDescriptor 强类型 slice）就地转成
// json.Unmarshal 到 map 后的 []any 形态。原 requestBodyToMap 路径经 marshal+unmarshal
// 会把 tool_calls 元素变为 map[string]any（键按字典序），而直接构造 map 时若保持
// struct slice 会按字段声明序序列化，导致请求体字节与旧路径不一致。此函数仅处理
// tool_calls 小字段，messages 主体保持零拷贝。
func normalizeOpenAIChatMessageToolCallsJSONShape(messages []map[string]any) {
	for _, message := range messages {
		raw, ok := message["tool_calls"]
		if !ok {
			continue
		}
		payload, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var decoded []any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			continue
		}
		message["tool_calls"] = decoded
	}
}

// buildOpenAIResponsesBodyMap 直接构造 OpenAI Responses 请求体的 map[string]any
// 形态（与 json.Marshal(openAIResponsesRequestBody) 后 Unmarshal 的结果等价），
// 消除 requestBodyToMap 的 marshal+unmarshal 双序列化开销。
// 注意保持无 omitempty 字段（model/input/stream/store）与 omitempty 字段
// （instructions/max_output_tokens/reasoning/include/prompt_cache_key/tools）
// 的输出语义：input 为 nil 时仍写入 key（值为 null），供 applyOpenAIParallelToolCalls
// 的 body["input"] 存在性判断保持原行为。
func buildOpenAIResponsesBodyMap(req StreamRequest, modelID string, promptCacheKeyMaximumLength int) (map[string]any, error) {
	instructions, input, err := normalizeOpenAIResponsesInput(req.Messages)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model":  modelID,
		"input":  toJSONAnySlice(input),
		"stream": true,
		"store":  false,
	}
	if instructions != "" {
		body["instructions"] = instructions
	}
	if shouldSendOpenAIMaxOutputTokens(modelID) && req.MaxTokens != 0 {
		body["max_output_tokens"] = req.MaxTokens
	}
	if key := openAIPromptCacheKey(req, modelID, promptCacheKeyMaximumLength); key != "" {
		body["prompt_cache_key"] = key
	}
	if len(req.Tools) > 0 {
		tools, err := normalizeOpenAIResponsesTools(req.Tools)
		if err != nil {
			return nil, err
		}
		if shouldExposeOpenAIResponsesImageGeneration(req, tools) {
			tools = ensureOpenAIResponsesImageGenerationTool(tools)
			if req.RequestKnobs != nil {
				req.RequestKnobs["openai_responses_image_generation_tool"] = "auto"
			}
		}
		body["tools"] = toJSONAnySlice(tools)
	}
	if effort := strings.TrimSpace(req.ReasoningEffort); effort != "" {
		body["reasoning"] = map[string]any{"effort": effort, "summary": "auto"}
		body["include"] = []any{"reasoning.summary", "reasoning.encrypted_content"}
	}
	return body, nil
}

// buildOpenAIChatBodyMap 直接构造 OpenAI Chat 请求体的 map[string]any 形态
// （与 json.Marshal(openAIRequestBody) 后 Unmarshal 的结果等价），消除
// requestBodyToMap 的 marshal+unmarshal 双序列化开销。
// 保持无 omitempty 字段（model/messages/stream/stream_options）的输出语义：
// messages 为 nil 时仍写入 key（值为 null），stream_options 在 compat shape
// 下为 nil（对应原 struct 中 nil map 输出 null）。
func buildOpenAIChatBodyMap(req StreamRequest, baseURL string, modelID string, promptCacheKeyMaximumLength int) (map[string]any, error) {
	normalizedMessages, err := normalizeOpenAIProviderMessages(req.Messages, strings.TrimSpace(req.ReasoningEffort) != "", isKimiOpenAIRequest(baseURL, modelID))
	if err != nil {
		return nil, err
	}
	normalizeOpenAIChatMessageToolCallsJSONShape(normalizedMessages)
	body := map[string]any{
		"model":    modelID,
		"messages": toJSONAnySlice(normalizedMessages),
		"stream":   true,
	}
	if shouldSendOpenAIMaxOutputTokens(modelID) && req.MaxTokens != 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if openAIChatRequestGroupUsesCompatShape(req.OpenAIRequestGroup) {
		body["stream_options"] = nil
	} else {
		body["stream_options"] = map[string]any{"include_usage": true}
		if key := openAIPromptCacheKey(req, modelID, promptCacheKeyMaximumLength); key != "" {
			body["prompt_cache_key"] = key
		}
		if strings.TrimSpace(req.ReasoningEffort) != "" {
			body["reasoning_effort"] = req.ReasoningEffort
		}
	}
	if len(req.Tools) > 0 {
		tools, err := normalizeOpenAIChatTools(req.Tools)
		if err != nil {
			return nil, err
		}
		if len(tools) > 0 {
			rawTools, err := rawMessagesToJSONAny(tools)
			if err != nil {
				return nil, err
			}
			body["tools"] = rawTools
		}
	}
	return body, nil
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

// extractOpenAIResponsesReasoningSummary converts the Responses reasoning
// output item's summary array into readable text. OpenAI currently returns
// entries such as [{"type":"summary_text","text":"..."}], but
// accepting any entry with a text field keeps compatible gateways working.
func extractOpenAIResponsesReasoningSummary(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var entries []struct {
		Type string `json:"type,omitempty"`
		Text string `json:"text,omitempty"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return ""
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		if text := strings.TrimSpace(entry.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
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

