// anthropic_request.go 承载 Anthropic 请求构造域：cache frontier/breakpoints、
// messages 归一化与 image 迁移、thinking/output config 与 compat 改写。
package modeladapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func anthropicEphemeralCacheControl() map[string]any {
	return map[string]any{"type": "ephemeral"}
}

type anthropicCacheFrontier struct {
	CanonicalBodyHash       string   `json:"canonical_body_hash,omitempty"`
	FrontierHash            string   `json:"frontier_hash,omitempty"`
	FrontierPath            string   `json:"frontier_path,omitempty"`
	BreakpointPositions     []string `json:"breakpoint_positions,omitempty"`
	BreakpointCount         int      `json:"breakpoint_count,omitempty"`
	ExpectedCacheRead       bool     `json:"expected_cache_read,omitempty"`
	PreviousFrontierMatched bool     `json:"previous_frontier_matched,omitempty"`
}

func buildAnthropicCacheFrontier(canonicalBody map[string]any, stableMessageCount int) anthropicCacheFrontier {
	frontier := anthropicCacheFrontier{
		CanonicalBodyHash:   anthropicCanonicalHash(canonicalBody),
		BreakpointPositions: anthropicCacheBreakpointPositions(canonicalBody, stableMessageCount),
	}
	if len(frontier.BreakpointPositions) > 0 {
		frontier.FrontierPath = frontier.BreakpointPositions[len(frontier.BreakpointPositions)-1]
		frontier.FrontierHash = anthropicCanonicalPrefixHash(canonicalBody, frontier.FrontierPath)
	}
	frontier.BreakpointCount = len(frontier.BreakpointPositions)
	return frontier
}

func anthropicCacheBreakpointPositions(body map[string]any, stableMessageCount int) []string {
	positions := make([]string, 0, 4)
	if tools, ok := body["tools"].([]anthropicTool); ok && len(tools) > 0 {
		positions = append(positions, fmt.Sprintf("tools[%d]", len(tools)-1))
	} else if tools, ok := body["tools"].([]any); ok && len(tools) > 0 {
		positions = append(positions, fmt.Sprintf("tools[%d]", len(tools)-1))
	}
	if system, ok := body["system"].([]map[string]any); ok && len(system) > 0 {
		positions = append(positions, fmt.Sprintf("system[%d]", len(system)-1))
	} else if system, ok := body["system"].([]any); ok && len(system) > 0 {
		positions = append(positions, fmt.Sprintf("system[%d]", len(system)-1))
	}
	if stableMessageCount > 0 {
		if path := anthropicMessageCacheBreakpointPath(body, stableMessageCount); path != "" {
			positions = append(positions, path)
		}
	}
	if path := anthropicMessageCacheBreakpointPath(body, anthropicBodyMessageCount(body)); path != "" {
		positions = append(positions, path)
	}
	return dedupeAnthropicCacheBreakpointPositions(positions)
}

func dedupeAnthropicCacheBreakpointPositions(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func anthropicBodyMessageCount(body map[string]any) int {
	messages, ok := body["messages"].([]anthropicMessage)
	if ok {
		return len(messages)
	}
	genericMessages, ok := body["messages"].([]any)
	if ok {
		return len(genericMessages)
	}
	return 0
}

func anthropicMessageCacheBreakpointPath(body map[string]any, messageCount int) string {
	messages, ok := anthropicMessagesFromBody(body)
	if !ok || len(messages) == 0 || messageCount <= 0 {
		return ""
	}
	if messageCount > len(messages) {
		messageCount = len(messages)
	}
	for messageIndex := messageCount - 1; messageIndex >= 0; messageIndex-- {
		message := messages[messageIndex]
		for blockIndex := len(message.Content) - 1; blockIndex >= 0; blockIndex-- {
			if isAnthropicCacheableBlock(message.Content[blockIndex]) {
				return fmt.Sprintf("messages[%d].content[%d]", messageIndex, blockIndex)
			}
		}
	}
	return ""
}

func anthropicMessagesFromBody(body map[string]any) ([]anthropicMessage, bool) {
	messages, ok := body["messages"].([]anthropicMessage)
	if ok {
		return messages, true
	}
	genericMessages, ok := body["messages"].([]any)
	if !ok {
		return nil, false
	}
	messages = make([]anthropicMessage, 0, len(genericMessages))
	for _, item := range genericMessages {
		messageMap, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		contentItems, ok := messageMap["content"].([]any)
		if !ok {
			return nil, false
		}
		content := make([]map[string]any, 0, len(contentItems))
		for _, contentItem := range contentItems {
			block, ok := contentItem.(map[string]any)
			if !ok {
				return nil, false
			}
			content = append(content, block)
		}
		messages = append(messages, anthropicMessage{
			Role:    strings.TrimSpace(anthropicStringValue(messageMap["role"])),
			Content: content,
		})
	}
	return messages, true
}

func applyAnthropicCacheBreakpoints(body map[string]any, positions []string) {
	for _, position := range positions {
		applyAnthropicCacheBreakpoint(body, position)
	}
}

func applyAnthropicCacheBreakpoint(body map[string]any, position string) {
	position = strings.TrimSpace(position)
	if strings.HasPrefix(position, "tools[") {
		index, ok := parseAnthropicBracketIndex(position, "tools")
		if !ok {
			return
		}
		tools, ok := body["tools"].([]any)
		if !ok || index < 0 || index >= len(tools) {
			return
		}
		tool, ok := tools[index].(map[string]any)
		if !ok {
			return
		}
		tool["cache_control"] = anthropicEphemeralCacheControl()
		return
	}
	if strings.HasPrefix(position, "system[") {
		index, ok := parseAnthropicBracketIndex(position, "system")
		if !ok {
			return
		}
		system, ok := body["system"].([]any)
		if !ok || index < 0 || index >= len(system) {
			return
		}
		block, ok := system[index].(map[string]any)
		if !ok {
			return
		}
		block["cache_control"] = anthropicEphemeralCacheControl()
		return
	}
	messageIndex, blockIndex, ok := parseAnthropicMessageBlockPath(position)
	if !ok {
		return
	}
	messages, ok := body["messages"].([]any)
	if !ok || messageIndex < 0 || messageIndex >= len(messages) {
		return
	}
	message, ok := messages[messageIndex].(map[string]any)
	if !ok {
		return
	}
	content, ok := message["content"].([]any)
	if !ok || blockIndex < 0 || blockIndex >= len(content) {
		return
	}
	block, ok := content[blockIndex].(map[string]any)
	if !ok {
		return
	}
	block["cache_control"] = anthropicEphemeralCacheControl()
}

func parseAnthropicBracketIndex(position string, prefix string) (int, bool) {
	start := prefix + "["
	if !strings.HasPrefix(position, start) || !strings.HasSuffix(position, "]") {
		return 0, false
	}
	value, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(position, start), "]"))
	return value, err == nil
}

func parseAnthropicMessageBlockPath(position string) (int, int, bool) {
	const messagePrefix = "messages["
	const contentPrefix = "].content["
	if !strings.HasPrefix(position, messagePrefix) || !strings.HasSuffix(position, "]") {
		return 0, 0, false
	}
	rest := strings.TrimPrefix(position, messagePrefix)
	parts := strings.Split(rest, contentPrefix)
	if len(parts) != 2 {
		return 0, 0, false
	}
	messageIndex, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	blockIndex, err := strconv.Atoi(strings.TrimSuffix(parts[1], "]"))
	if err != nil {
		return 0, 0, false
	}
	return messageIndex, blockIndex, true
}

func annotateAnthropicRequestKnobs(requestKnobs map[string]any, canonicalBody map[string]any, frontier anthropicCacheFrontier) map[string]any {
	if len(requestKnobs) == 0 {
		requestKnobs = map[string]any{}
	}
	previousHash := anthropicPreviousFrontierHash(requestKnobs)
	previousPath := anthropicPreviousFrontierPath(requestKnobs)
	currentPreviousFrontierHash := ""
	if previousPath != "" {
		currentPreviousFrontierHash = anthropicCanonicalPrefixHash(canonicalBody, previousPath)
	}
	frontier.PreviousFrontierMatched = previousHash != "" && currentPreviousFrontierHash == previousHash
	frontier.ExpectedCacheRead = frontier.PreviousFrontierMatched && frontier.BreakpointCount > 0
	requestKnobs["cache_frontier"] = map[string]any{
		"canonical_body_hash":          frontier.CanonicalBodyHash,
		"frontier_hash":                frontier.FrontierHash,
		"frontier_path":                frontier.FrontierPath,
		"breakpoint_positions":         append([]string(nil), frontier.BreakpointPositions...),
		"breakpoint_count":             frontier.BreakpointCount,
		"expected_cache_read":          frontier.ExpectedCacheRead,
		"previous_frontier_matched":    frontier.PreviousFrontierMatched,
		"previous_frontier_hash":       previousHash,
		"previous_frontier_path":       previousPath,
		"current_previous_prefix_hash": currentPreviousFrontierHash,
	}
	return requestKnobs
}

func anthropicPreviousFrontierHash(requestKnobs map[string]any) string {
	if len(requestKnobs) == 0 {
		return ""
	}
	value := strings.TrimSpace(anthropicStringValue(requestKnobs["previous_cache_frontier_hash"]))
	if value != "" {
		return value
	}
	previous, ok := requestKnobs["previous_cache_frontier"].(map[string]any)
	if !ok {
		return ""
	}
	return strings.TrimSpace(anthropicStringValue(previous["frontier_hash"]))
}

func anthropicPreviousFrontierPath(requestKnobs map[string]any) string {
	if len(requestKnobs) == 0 {
		return ""
	}
	previous, ok := requestKnobs["previous_cache_frontier"].(map[string]any)
	if !ok {
		return ""
	}
	return strings.TrimSpace(anthropicStringValue(previous["frontier_path"]))
}

func anthropicStringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func anthropicCanonicalHash(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])[:16]
}

func anthropicCanonicalPrefixHash(body map[string]any, frontierPath string) string {
	prefix := cloneRequestBodyOverride(body)
	if strings.TrimSpace(frontierPath) == "" {
		return anthropicCanonicalHash(prefix)
	}
	trimAnthropicBodyAfterFrontier(prefix, frontierPath)
	return anthropicCanonicalHash(prefix)
}

func trimAnthropicBodyAfterFrontier(body map[string]any, frontierPath string) {
	if strings.HasPrefix(frontierPath, "tools[") {
		return
	}
	if strings.HasPrefix(frontierPath, "system[") {
		delete(body, "messages")
		return
	}
	messageIndex, blockIndex, ok := parseAnthropicMessageBlockPath(frontierPath)
	if !ok {
		return
	}
	messages, ok := body["messages"].([]any)
	if !ok || messageIndex < 0 || messageIndex >= len(messages) {
		return
	}
	messages = messages[:messageIndex+1]
	message, ok := messages[messageIndex].(map[string]any)
	if ok {
		if content, ok := message["content"].([]any); ok && blockIndex >= 0 && blockIndex < len(content) {
			message["content"] = content[:blockIndex+1]
		}
	}
	body["messages"] = messages
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []map[string]any `json:"content"`
}

func anthropicStableProviderMessageCount(input []Message, stableReplayMessageCount int, thinkingEnabled bool) int {
	if len(input) == 0 || stableReplayMessageCount <= 0 {
		return 0
	}
	stableReplayMessages := make([]Message, 0, stableReplayMessageCount)
	for _, message := range input {
		if strings.TrimSpace(message.Role) == "system" {
			continue
		}
		if len(stableReplayMessages) >= stableReplayMessageCount {
			break
		}
		stableReplayMessages = append(stableReplayMessages, message)
	}
	if len(stableReplayMessages) == 0 {
		return 0
	}
	_, messages, err := normalizeAnthropicProviderMessages(stableReplayMessages, thinkingEnabled, false)
	if err != nil {
		return 0
	}
	return len(messages)
}

func applyAnthropicMessageCacheBreakpoints(messages []anthropicMessage, stableMessageCountOverride ...int) {
	if len(stableMessageCountOverride) == 0 || len(messages) == 0 {
		return
	}
	stableMessageCount := stableMessageCountOverride[0]
	if stableMessageCount > 0 {
		applyAnthropicMessageCacheBreakpointAt(messages, stableMessageCount)
	}
	if len(messages) > stableMessageCount {
		applyAnthropicMessageCacheBreakpointAt(messages, len(messages))
	}
}

func applyAnthropicMessageCacheBreakpointAt(messages []anthropicMessage, messageCount int) {
	if len(messages) == 0 || messageCount <= 0 {
		return
	}
	if messageCount > len(messages) {
		messageCount = len(messages)
	}
	for messageIndex := messageCount - 1; messageIndex >= 0; messageIndex-- {
		message := messages[messageIndex]
		for blockIndex := len(message.Content) - 1; blockIndex >= 0; blockIndex-- {
			block := message.Content[blockIndex]
			if !isAnthropicCacheableBlock(block) {
				continue
			}
			block["cache_control"] = anthropicEphemeralCacheControl()
			return
		}
	}
}

func isAnthropicCacheableBlock(block map[string]any) bool {
	if len(block) == 0 {
		return false
	}
	switch strings.TrimSpace(anthropicStringField(block, "type")) {
	case contentPartTypeText:
		return strings.TrimSpace(anthropicStringField(block, "text")) != ""
	case "tool_result":
		return strings.TrimSpace(anthropicStringField(block, "content")) != ""
	case "tool_use":
		return strings.TrimSpace(anthropicStringField(block, "id")) != "" && strings.TrimSpace(anthropicStringField(block, "name")) != ""
	default:
		return false
	}
}

func normalizeAnthropicProviderMessages(input []Message, thinkingEnabled bool, relocateImages bool) ([]string, []anthropicMessage, error) {
	systemParts := make([]string, 0, len(input))
	messages := make([]anthropicMessage, 0, len(input))
	pendingToolResults := make([]map[string]any, 0, 2)
	flushToolResults := func() {
		if len(pendingToolResults) == 0 {
			return
		}
		messages = append(messages, anthropicMessage{
			Role:    "user",
			Content: append([]map[string]any(nil), pendingToolResults...),
		})
		pendingToolResults = pendingToolResults[:0]
	}

	for _, message := range input {
		role := strings.TrimSpace(message.Role)
		switch role {
		case "system":
			if hasImageContentParts(message.ContentParts) {
				return nil, nil, fmt.Errorf("anthropic system message does not support image content")
			}
			content := message.Content
			if strings.TrimSpace(content) == "" && len(message.ContentParts) > 0 {
				content = collapseTextContentParts(message.ContentParts)
			}
			if strings.TrimSpace(content) != "" {
				systemParts = append(systemParts, content)
			}
		case "tool":
			toolUseID := providerToolCallID(message.ToolCallID)
			if toolUseID == "" {
				return nil, nil, fmt.Errorf("anthropic tool message requires tool_call_id")
			}
			pendingToolResults = append(pendingToolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": toolUseID,
				"content":     message.Content,
			})
		case "user", "assistant":
			flushToolResults()
			contentBlocks, err := anthropicProviderContentBlocks(message, thinkingEnabled)
			if err != nil {
				return nil, nil, err
			}
			blocks := make([]map[string]any, 0, len(contentBlocks)+len(message.ToolCalls))
			blocks = append(blocks, contentBlocks...)
			if role == "assistant" {
				for _, toolCall := range message.ToolCalls {
					inputJSON, err := decodeAnthropicToolInput(toolCall.Function.Arguments)
					if err != nil {
						return nil, nil, err
					}
					blocks = append(blocks, map[string]any{
						"type":  "tool_use",
						"id":    providerToolCallID(toolCall.ID),
						"name":  strings.TrimSpace(toolCall.Function.Name),
						"input": inputJSON,
					})
				}
			}
			if len(blocks) == 0 {
				continue
			}
			if role == "assistant" && mergeAnthropicAssistantToolUseWithPrevious(&messages, message, blocks) {
				continue
			}
			messages = append(messages, anthropicMessage{
				Role:    role,
				Content: blocks,
			})
		default:
			flushToolResults()
			if strings.TrimSpace(message.Content) == "" {
				continue
			}
			messages = append(messages, anthropicMessage{
				Role: "user",
				Content: []map[string]any{{
					"type": "text",
					"text": message.Content,
				}},
			})
		}
	}
	flushToolResults()
	if relocateImages {
		messages = relocateAnthropicImagesToLastUserMessage(messages)
	}
	return systemParts, messages, nil
}

// relocateAnthropicImagesToLastUserMessage 把所有 user 消息里的 image 块搬运到最后一条 user 消息的末尾。
//
// 背景：部分第三方中转站（如 Bedrock 代理）在 Anthropic→上游 的消息转换中，
// 会丢弃「后面还跟着大量文本/消息」的非末尾图片块。将图片统一移动到末条 user 消息
// 可规避该问题，同时保留图片信息本身。
//
// 数据流演变：
//
//	[user_info] [query + IMG] [reminder] [reminder] [current_request]
//	→ [user_info] [query] [reminder] [reminder] [current_request + IMG]
//
// 搬运后若某条 user 消息 content 变空，则丢弃该消息，避免 Anthropic 拒绝空内容消息。
func relocateAnthropicImagesToLastUserMessage(messages []anthropicMessage) []anthropicMessage {
	lastUserIndex := -1
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.TrimSpace(messages[index].Role) == "user" {
			lastUserIndex = index
			break
		}
	}
	if lastUserIndex < 0 {
		return messages
	}

	relocated := make([]map[string]any, 0, 2)
	for index := 0; index < len(messages); index++ {
		if index == lastUserIndex || strings.TrimSpace(messages[index].Role) != "user" {
			continue
		}
		kept := make([]map[string]any, 0, len(messages[index].Content))
		for _, block := range messages[index].Content {
			if isAnthropicImageBlock(block) {
				relocated = append(relocated, block)
				continue
			}
			kept = append(kept, block)
		}
		messages[index].Content = kept
	}
	if len(relocated) == 0 {
		return messages
	}
	messages[lastUserIndex].Content = append(messages[lastUserIndex].Content, relocated...)

	compacted := make([]anthropicMessage, 0, len(messages))
	for index, message := range messages {
		if index != lastUserIndex && strings.TrimSpace(message.Role) == "user" && len(message.Content) == 0 {
			continue
		}
		compacted = append(compacted, message)
	}
	return compacted
}

func isAnthropicImageBlock(block map[string]any) bool {
	return strings.TrimSpace(anthropicStringField(block, "type")) == "image"
}

func anthropicProviderContentBlocks(message Message, thinkingEnabled bool) ([]map[string]any, error) {
	blocks, err := anthropicContentBlocks(message)
	if err != nil {
		return nil, err
	}
	if !shouldIncludeAnthropicThinkingBlock(message, thinkingEnabled) {
		return blocks, nil
	}

	thinkingBlock := map[string]any{
		"type":     "thinking",
		"thinking": message.ReasoningContent,
	}
	if signature := anthropicThinkingSignature(message); signature != "" {
		thinkingBlock["signature"] = signature
	}
	return append([]map[string]any{thinkingBlock}, blocks...), nil
}

func mergeAnthropicAssistantToolUseWithPrevious(messages *[]anthropicMessage, message Message, blocks []map[string]any) bool {
	if messages == nil || len(*messages) == 0 {
		return false
	}
	if strings.TrimSpace(message.Role) != "assistant" || len(message.ToolCalls) == 0 {
		return false
	}
	if strings.TrimSpace(message.Content) != "" || len(message.ContentParts) > 0 {
		return false
	}
	reasoning := strings.TrimSpace(message.ReasoningContent)
	signature := anthropicThinkingSignature(message)
	if reasoning == "" {
		return false
	}
	last := &(*messages)[len(*messages)-1]
	if strings.TrimSpace(last.Role) != "assistant" || !anthropicMessageHasLeadingThinking(*last, reasoning, signature) {
		return false
	}
	toolUseBlocks := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		if strings.TrimSpace(anthropicStringField(block, "type")) == "thinking" {
			continue
		}
		toolUseBlocks = append(toolUseBlocks, block)
	}
	if len(toolUseBlocks) == 0 {
		return false
	}
	last.Content = append(last.Content, toolUseBlocks...)
	return true
}

func anthropicMessageHasLeadingThinking(message anthropicMessage, reasoning string, signature string) bool {
	if len(message.Content) == 0 {
		return false
	}
	first := message.Content[0]
	if strings.TrimSpace(anthropicStringField(first, "type")) != "thinking" {
		return false
	}
	return strings.TrimSpace(anthropicStringField(first, "thinking")) == reasoning && strings.TrimSpace(anthropicStringField(first, "signature")) == signature
}

func anthropicThinkingSignature(message Message) string {
	signature := strings.TrimSpace(message.ReasoningSignature)
	if signature == "" {
		return ""
	}
	source := strings.TrimSpace(message.ReasoningSignatureSource)
	if source == "" || source == ReasoningSignatureSourceAnthropic {
		return signature
	}
	return ""
}

func anthropicStringField(payload map[string]any, key string) string {
	if len(payload) == 0 || strings.TrimSpace(key) == "" {
		return ""
	}
	value, ok := payload[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func shouldIncludeAnthropicThinkingBlock(message Message, thinkingEnabled bool) bool {
	if !thinkingEnabled {
		return false
	}
	if strings.TrimSpace(message.Role) != "assistant" {
		return false
	}
	if strings.TrimSpace(message.ReasoningContent) == "" {
		return false
	}
	return true
}

func decodeAnthropicToolInput(arguments string) (any, error) {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return map[string]any{}, nil
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return nil, fmt.Errorf("decode anthropic tool input failed: %w", err)
	}
	return value, nil
}

func completedAnthropicToolArgsJSON(accumulator *anthropicToolAccumulator) ([]byte, error) {
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
		return nil, fmt.Errorf("anthropic returned incomplete or malformed tool input for %s: %w", toolName, err)
	}
	if value == nil {
		toolName := strings.TrimSpace(accumulator.Name)
		if toolName == "" {
			toolName = "tool"
		}
		return nil, fmt.Errorf("anthropic returned non-object tool input for %s", toolName)
	}
	return []byte(trimmed), nil
}

func applyAnthropicProviderCompatibility(body map[string]any, req StreamRequest, baseURL string, modelID string) {
	if len(body) == 0 {
		return
	}
	base := strings.ToLower(strings.TrimSpace(baseURL))
	model := strings.ToLower(strings.TrimSpace(modelID))
	if strings.Contains(base, "deepseek") || strings.Contains(model, "deepseek") {
		if normalizeRuntimeThinkingEffort(req.ThinkingEffort) == "disabled" || anthropicThinkingType(body) == "disabled" {
			delete(body, "output_config")
			delete(body, "reasoning_effort")
		}
	}
	if strings.Contains(base, "githubcopilot.com") || strings.Contains(base, "githubcopilot") {
		if cleaned, ok := stripAnthropicThinkingBlocks(body).(map[string]any); ok {
			for key := range body {
				delete(body, key)
			}
			for key, value := range cleaned {
				body[key] = value
			}
		}
	}
}

func anthropicThinkingType(body map[string]any) string {
	thinking, ok := body["thinking"].(map[string]any)
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(fmt.Sprint(thinking["type"])))
}

func stripAnthropicThinkingBlocks(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if blockType := strings.ToLower(strings.TrimSpace(fmt.Sprint(typed["type"]))); blockType == "thinking" || blockType == "redacted_thinking" {
			return nil
		}
		for key, child := range typed {
			if stripped := stripAnthropicThinkingBlocks(child); stripped == nil {
				delete(typed, key)
			} else {
				typed[key] = stripped
			}
		}
		return typed
	case []any:
		filtered := make([]any, 0, len(typed))
		for _, child := range typed {
			if stripped := stripAnthropicThinkingBlocks(child); stripped != nil {
				filtered = append(filtered, stripped)
			}
		}
		return filtered
	default:
		return value
	}
}

func buildAnthropicThinkingConfig(req StreamRequest) map[string]any {
	if normalizeRuntimeThinkingEffort(req.ThinkingEffort) == "disabled" {
		return map[string]any{
			"type": "disabled",
		}
	}
	if strings.TrimSpace(req.AnthropicThinkingEffort) == "" {
		return nil
	}
	return map[string]any{
		"type":    "adaptive",
		"display": "summarized",
	}
}

// applyAnthropicThinkingConfig 在请求体构造完成后（含 RequestBodyOverride 路径）无条件调用，
// 与 openai.go 的 applyOpenAIThinkingDisable 对称。它把 thinking 配置写入 body 并在 disabled
// 时清理与之冲突的字段，确保两条构造路径行为一致：
//   - disabled: 强制 thinking:{type:"disabled"}，删除 output_config / 残留 thinking adaptive 配置，
//     记录 thinking_disabled_provider_param=thinking.type knob
//   - adaptive: 按 AnthropicThinkingEffort 写 thinking:{type:adaptive,display:summarized} + output_config
//
// 在 override 路径下，上层若已在 override body 里塞了 thinking/output_config，disabled 时会被正确覆盖。
func applyAnthropicThinkingConfig(body map[string]any, req StreamRequest) {
	if len(body) == 0 {
		return
	}
	if normalizeRuntimeThinkingEffort(req.ThinkingEffort) != "disabled" {
		if strings.TrimSpace(req.AnthropicThinkingEffort) == "" {
			return
		}
		body["thinking"] = map[string]any{
			"type":    "adaptive",
			"display": "summarized",
		}
		body["output_config"] = buildAnthropicOutputConfig(req)
		return
	}
	body["thinking"] = map[string]any{"type": "disabled"}
	delete(body, "output_config")
	setRequestKnob(req, "thinking_disabled_provider_param", "thinking.type")
}

func buildAnthropicOutputConfig(req StreamRequest) map[string]any {
	return map[string]any{
		"effort": anthropicThinkingEffort(req),
	}
}

func anthropicThinkingEffort(req StreamRequest) string {
	switch strings.ToLower(strings.TrimSpace(req.AnthropicThinkingEffort)) {
	case "low", "medium", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(req.AnthropicThinkingEffort))
	default:
		return "xhigh"
	}
}
