// anthropic.go 实现 Anthropic Messages 兼容流式适配器。
package modeladapter

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"cursor/gen/agentv1"
	"cursor/internal/netproxy"
)

// AnthropicAdapter 实现 Anthropic 兼容流式请求。
type AnthropicAdapter struct {
	// client 负责发送 HTTP 请求。
	client *http.Client
}

type anthropicToolAccumulator struct {
	CallID                 string
	Name                   string
	Args                   strings.Builder
	LastEmittedPath        string
	LastStreamContent      string
	LastCreatePlanSnapshot string
}

type anthropicTool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema"`
	CacheControl map[string]any `json:"cache_control,omitempty"`
}

const (
	anthropicThinkOpenTag            = "<think>"
	anthropicThinkCloseTag           = "</think>"
	anthropicBillingHeaderSystemText = "x-anthropic-billing-header: cc_version=2.1.179.61a; cc_entrypoint=cli; cch=37703;"
)

type anthropicContentPartKind string

const (
	anthropicContentPartText              anthropicContentPartKind = "text"
	anthropicContentPartReasoning         anthropicContentPartKind = "reasoning"
	anthropicContentPartThinkingCompleted anthropicContentPartKind = "thinking_completed"
)

type anthropicContentPart struct {
	Kind anthropicContentPartKind
	Text string
}

// anthropicThinkTagParser 负责把 text_delta 里的 <think> 标签拆回 reasoning 流。
type anthropicThinkTagParser struct {
	carry   string
	inThink bool
}

func (parser *anthropicThinkTagParser) Consume(text string) []anthropicContentPart {
	if parser == nil || text == "" {
		return nil
	}
	input := parser.carry + text
	parser.carry = ""
	parts := make([]anthropicContentPart, 0, 4)
	for input != "" {
		if parser.inThink {
			closeIndex := strings.Index(input, anthropicThinkCloseTag)
			if closeIndex >= 0 {
				if closeIndex > 0 {
					parts = append(parts, anthropicContentPart{
						Kind: anthropicContentPartReasoning,
						Text: input[:closeIndex],
					})
				}
				parts = append(parts, anthropicContentPart{Kind: anthropicContentPartThinkingCompleted})
				parser.inThink = false
				input = input[closeIndex+len(anthropicThinkCloseTag):]
				continue
			}
			carryLen := anthropicTrailingTagPrefixLength(input, anthropicThinkCloseTag)
			if emitText := input[:len(input)-carryLen]; emitText != "" {
				parts = append(parts, anthropicContentPart{
					Kind: anthropicContentPartReasoning,
					Text: emitText,
				})
			}
			parser.carry = input[len(input)-carryLen:]
			break
		}

		openIndex := strings.Index(input, anthropicThinkOpenTag)
		if openIndex >= 0 {
			if openIndex > 0 {
				parts = append(parts, anthropicContentPart{
					Kind: anthropicContentPartText,
					Text: input[:openIndex],
				})
			}
			parser.inThink = true
			input = input[openIndex+len(anthropicThinkOpenTag):]
			continue
		}
		carryLen := anthropicTrailingTagPrefixLength(input, anthropicThinkOpenTag)
		if emitText := input[:len(input)-carryLen]; emitText != "" {
			parts = append(parts, anthropicContentPart{
				Kind: anthropicContentPartText,
				Text: emitText,
			})
		}
		parser.carry = input[len(input)-carryLen:]
		break
	}
	return parts
}

func (parser *anthropicThinkTagParser) Flush() []anthropicContentPart {
	if parser == nil || parser.carry == "" {
		return nil
	}
	kind := anthropicContentPartText
	if parser.inThink {
		kind = anthropicContentPartReasoning
	}
	text := parser.carry
	parser.carry = ""
	return []anthropicContentPart{{
		Kind: kind,
		Text: text,
	}}
}

// NewAnthropicAdapter 创建一个 Anthropic 兼容适配器。
func NewAnthropicAdapter() *AnthropicAdapter {
	return &AnthropicAdapter{
		client: netproxy.NewHTTPClient(0),
	}
}

func anthropicEndpointURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if ProviderURLHasEndpoint(base, "/v1/messages", "/messages") {
		return base
	}
	// baseURL 已含版本前缀（如 https://api.example.com/v1）时，
	// 只追加 /messages，避免拼出 /v1/v1/messages 导致 404。
	if anthropicBaseURLHasVersionSuffix(base) {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

// anthropicBaseURLHasVersionSuffix 判断 URL path 末段是否为 /v1、/v2 等版本前缀。
func anthropicBaseURLHasVersionSuffix(base string) bool {
	lower := strings.ToLower(strings.TrimSpace(base))
	idx := strings.LastIndex(lower, "/")
	if idx < 0 || idx >= len(lower)-1 {
		return false
	}
	segment := lower[idx+1:]
	if len(segment) < 2 || segment[0] != 'v' {
		return false
	}
	for i := 1; i < len(segment); i++ {
		if segment[i] < '0' || segment[i] > '9' {
			return false
		}
	}
	return true
}

// shouldRelocateAnthropicImages 判断是否需要把图片块搬运到末条 user 消息。
//
// 官方 Anthropic 端点（api.anthropic.com）可正确处理任意位置的图片，保持原样；
// 其余第三方中转站默认启用搬运，规避「非末尾图片被丢弃」的转换问题。
func shouldRelocateAnthropicImages(baseURL string) bool {
	base := strings.ToLower(strings.TrimSpace(baseURL))
	if base == "" {
		return false
	}
	return !strings.Contains(base, "api.anthropic.com")
}

// ApplyAnthropicCompatibleAuthHeaders 同时兼容 Anthropic 原生 x-api-key 和 Bearer token 代理。
func ApplyAnthropicCompatibleAuthHeaders(httpReq *http.Request, apiKey string) {
	if httpReq == nil {
		return
	}
	token := anthropicCompatibleAuthToken(apiKey)
	if token == "" {
		return
	}
	httpReq.Header.Set("x-api-key", token)
	httpReq.Header.Set("Authorization", "Bearer "+token)
}

func anthropicCompatibleAuthToken(apiKey string) string {
	token := strings.TrimSpace(apiKey)
	if len(token) >= len("Bearer ") && strings.EqualFold(token[:len("Bearer ")], "Bearer ") {
		token = strings.TrimSpace(token[len("Bearer "):])
	}
	return token
}

func anthropicProviderSystemBlocks(systemParts []string) []map[string]any {
	blocks := []map[string]any{{
		"type": "text",
		"text": anthropicBillingHeaderSystemText,
	}}
	if len(systemParts) > 0 {
		blocks = append(blocks, map[string]any{
			"type": "text",
			"text": strings.Join(systemParts, "\n\n"),
		})
	}
	return blocks
}

// Stream 发送 Messages 流式请求，并解析统一模型事件。

func anthropicTrailingTagPrefixLength(text string, tag string) int {
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


func emitAnthropicToolProgress(
	sink func(ModelEvent) error,
	model string,
	accumulator *anthropicToolAccumulator,
	argsTextDelta string,
) error {
	if accumulator == nil {
		return nil
	}
	toolName := strings.TrimSpace(accumulator.Name)
	if toolName == "CreatePlan" {
		return emitCreatePlanToolProgress(
			sink,
			"anthropic",
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
					Provider:   "anthropic",
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
					Provider:      "anthropic",
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
		Provider:   "anthropic",
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

func isEmptyAnthropicToolInput(input any) bool {
	if input == nil {
		return true
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return false
	}
	switch string(encoded) {
	case "", "null", "{}", "[]":
		return true
	default:
		return false
	}
}

func extractJSONStringFieldPrefix(input string, field string) (string, bool, bool) {
	keyToken := `"` + strings.TrimSpace(field) + `"`
	start := strings.Index(input, keyToken)
	if start < 0 {
		return "", false, false
	}
	index := start + len(keyToken)
	for index < len(input) && isJSONWhitespace(input[index]) {
		index++
	}
	if index >= len(input) || input[index] != ':' {
		return "", false, false
	}
	index++
	for index < len(input) && isJSONWhitespace(input[index]) {
		index++
	}
	if index >= len(input) || input[index] != '"' {
		return "", false, false
	}
	index++

	var builder strings.Builder
	for index < len(input) {
		character := input[index]
		if character == '"' {
			return builder.String(), true, true
		}
		if character != '\\' {
			builder.WriteByte(character)
			index++
			continue
		}
		if index+1 >= len(input) {
			return builder.String(), true, false
		}
		next := input[index+1]
		switch next {
		case '"', '\\', '/':
			builder.WriteByte(next)
			index += 2
		case 'b':
			builder.WriteByte('\b')
			index += 2
		case 'f':
			builder.WriteByte('\f')
			index += 2
		case 'n':
			builder.WriteByte('\n')
			index += 2
		case 'r':
			builder.WriteByte('\r')
			index += 2
		case 't':
			builder.WriteByte('\t')
			index += 2
		case 'u':
			if index+6 > len(input) {
				return builder.String(), true, false
			}
			runeValue, width, ok := decodeUnicodeEscape(input[index:])
			if !ok {
				return builder.String(), true, false
			}
			builder.WriteRune(runeValue)
			index += width
		default:
			builder.WriteByte(next)
			index += 2
		}
	}
	return builder.String(), true, false
}

func decodeUnicodeEscape(input string) (rune, int, bool) {
	if len(input) < 6 || input[0] != '\\' || input[1] != 'u' {
		return 0, 0, false
	}
	value, err := strconv.ParseUint(input[2:6], 16, 16)
	if err != nil {
		return 0, 0, false
	}
	r := rune(value)
	if utf16.IsSurrogate(r) {
		if len(input) < 12 || input[6] != '\\' || input[7] != 'u' {
			return 0, 0, false
		}
		nextValue, nextErr := strconv.ParseUint(input[8:12], 16, 16)
		if nextErr != nil {
			return 0, 0, false
		}
		decoded := utf16.DecodeRune(r, rune(nextValue))
		if decoded == '\uFFFD' {
			return 0, 0, false
		}
		return decoded, 12, true
	}
	return r, 6, true
}

func suffixAfterCommonPrefix(previous string, current string) string {
	if previous == "" {
		return current
	}
	maxPrefix := len(previous)
	if len(current) < maxPrefix {
		maxPrefix = len(current)
	}
	index := 0
	for index < maxPrefix && previous[index] == current[index] {
		index++
	}
	return current[index:]
}

func extractToolStreamContentPrefix(rawArgs string, toolName string) (string, bool) {
	switch strings.TrimSpace(toolName) {
	case "PatchEdit":
		if value, found, _ := extractJSONStringFieldPrefix(rawArgs, "new_string"); found {
			return value, true
		}
	case "Write":
		for _, field := range []string{"contents", "content", "stream_content", "streamContent"} {
			if value, found, _ := extractJSONStringFieldPrefix(rawArgs, field); found {
				return value, true
			}
		}
	}
	return "", false
}

func isJSONWhitespace(character byte) bool {
	switch character {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}

// anthropicThinkingBudget 计算当前 Anthropic thinking 预算。
func anthropicThinkingBudget(maxTokens int) int {
	if maxTokens <= 0 {
		return 2048
	}
	budget := maxTokens / 2
	if budget < 1024 {
		budget = 1024
	}
	return budget
}
