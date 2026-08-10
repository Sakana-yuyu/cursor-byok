// openai_request.go 承载 OpenAI 请求归一化域：messages/input 归一化、
// compat 兼容改写（chat/responses）、thinking 开关、toolschema 归一化。
package modeladapter

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"cursor/internal/modelchannel"
)

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

// openAIModelSupportsParallelToolCalls 判断该模型是否已知支持 Responses parallel_tool_calls。
// 仅对已验证的 gpt-5.6 系列开启；未知兼容端点保持原行为（不发送该字段）。
func openAIModelSupportsParallelToolCalls(modelID string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelID)), "gpt-5.6")
}

// applyOpenAIParallelToolCalls 为 gpt-5.6 Responses 请求选择安全的串行默认值。
// v0.0.85 自动开启并行后，模型会在同一响应中偶发返回截断或错误转义的工具参数；
// 用户通过 extra params 显式选择并行时仍尊重其配置。
func applyOpenAIParallelToolCalls(body map[string]any, modelID string) {
	if !openAIModelSupportsParallelToolCalls(modelID) {
		return
	}
	if _, isResponsesRequest := body["input"]; !isResponsesRequest {
		return
	}
	if _, explicit := body["parallel_tool_calls"]; explicit {
		return
	}
	switch tools := body["tools"].(type) {
	case []any:
		if len(tools) == 0 {
			return
		}
	case []map[string]any:
		if len(tools) == 0 {
			return
		}
	default:
		return
	}
	body["parallel_tool_calls"] = false
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
