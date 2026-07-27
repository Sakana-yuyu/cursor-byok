package modeladapter

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CanonicalMessagesToOpenAIChat converts canonical Anthropic-style messages to
// an OpenAI Chat Completions request body map. The returned map contains
// messages and, when supplied, tools and tool_choice; callers may add model,
// stream, and provider-specific fields before JSON encoding.
func CanonicalMessagesToOpenAIChat(messages []Message, tools []json.RawMessage, toolChoice any) (map[string]any, error) {
	chatMessages := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		if role == "" {
			return nil, fmt.Errorf("message role is required")
		}
		item := map[string]any{"role": role}
		if message.Name != "" {
			item["name"] = message.Name
		}
		if message.ToolCallID != "" {
			item["tool_call_id"] = message.ToolCallID
		}
		if role == "assistant" && len(message.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				calls = append(calls, openAIChatToolCallMap(call))
			}
			item["tool_calls"] = calls
		}
		if message.Content != "" || len(message.ContentParts) > 0 {
			content, err := protocolChatContent(message)
			if err != nil {
				return nil, err
			}
			item["content"] = content
		}
		if message.ReasoningContent != "" && role == "assistant" {
			item["reasoning_content"] = message.ReasoningContent
		}
		chatMessages = append(chatMessages, item)
	}
	body := map[string]any{"messages": chatMessages}
	if normalized, err := normalizeProtocolChatTools(tools); err != nil {
		return nil, err
	} else if len(normalized) > 0 {
		body["tools"] = normalized
	}
	if toolChoice != nil {
		body["tool_choice"] = toolChoice
	}
	return body, nil
}

// CanonicalMessagesToOpenAIResponses converts canonical Anthropic-style
// messages to an OpenAI Responses request body map. System messages become
// instructions, user/assistant content becomes input items, tool results
// become function_call_output items, and assistant tool calls become
// function_call items. Reasoning metadata is copied only when its source is
// ReasoningSignatureSourceOpenAIResponses.
func CanonicalMessagesToOpenAIResponses(messages []Message, tools []json.RawMessage, toolChoice any) (map[string]any, error) {
	instructions := make([]string, 0, 1)
	input := make([]map[string]any, 0, len(messages))
	callIDs := make(map[string]string)
	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		switch role {
		case "system":
			if text := protocolMessageText(message); text != "" {
				instructions = append(instructions, text)
			}
			continue
		case "tool":
			if strings.TrimSpace(message.ToolCallID) == "" {
				return nil, fmt.Errorf("tool message call id is required")
			}
			callID := callIDs[message.ToolCallID]
			if callID == "" {
				callID = protocolResponsesCallID(message.ToolCallID)
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": callID, "output": protocolMessageText(message)})
			continue
		case "user", "assistant":
		default:
			return nil, fmt.Errorf("unsupported responses message role: %s", role)
		}
		if message.ReasoningSignatureSource == ReasoningSignatureSourceOpenAIResponses && message.ReasoningSignature != "" {
			reasoning := map[string]any{"type": "reasoning", "encrypted_content": message.ReasoningSignature, "summary": []any{}}
			if message.OpenAIResponsesReasoningID != "" {
				reasoning["id"] = message.OpenAIResponsesReasoningID
			}
			if message.OpenAIResponsesReasoningStatus != "" {
				reasoning["status"] = message.OpenAIResponsesReasoningStatus
			}
			if len(message.OpenAIResponsesReasoningSummary) > 0 {
				reasoning["summary"] = json.RawMessage(append([]byte(nil), message.OpenAIResponsesReasoningSummary...))
			}
			input = append(input, reasoning)
		}
		if message.Content != "" || len(message.ContentParts) > 0 {
			content, err := protocolResponsesContent(message)
			if err != nil {
				return nil, err
			}
			if len(content) > 0 {
				input = append(input, map[string]any{"role": role, "content": content})
			}
		}
		for _, call := range message.ToolCalls {
			callID := strings.TrimSpace(call.OpenAIResponsesCallID)
			if callID == "" {
				callID = protocolResponsesCallID(call.ID)
			}
			if call.ID != "" {
				callIDs[call.ID] = callID
			}
			item := map[string]any{"type": "function_call", "call_id": callID, "name": call.Function.Name, "arguments": call.Function.Arguments, "status": "completed"}
			if call.OpenAIResponsesID != "" {
				item["id"] = call.OpenAIResponsesID
			}
			if call.OpenAIResponsesStatus != "" {
				item["status"] = call.OpenAIResponsesStatus
			}
			input = append(input, item)
		}
	}
	body := map[string]any{"input": input}
	if len(instructions) > 0 {
		body["instructions"] = strings.Join(instructions, "\n\n")
	}
	if normalized, err := normalizeProtocolResponsesTools(tools); err != nil {
		return nil, err
	} else if len(normalized) > 0 {
		body["tools"] = normalized
	}
	if toolChoice != nil {
		body["tool_choice"] = toolChoice
	}
	return body, nil
}

// NormalizeOpenAIChatMessages converts Chat message JSON maps to canonical
// messages. It preserves assistant tool calls, tool results, content parts,
// and reasoning_content when present.
func NormalizeOpenAIChatMessages(items []json.RawMessage) ([]Message, error) {
	result := make([]Message, 0, len(items))
	for _, raw := range items {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("decode chat message: %w", err)
		}
		var message Message
		if err := decodeStringField(item, "role", &message.Role); err != nil {
			return nil, err
		}
		if err := decodeChatContent(item["content"], &message); err != nil {
			return nil, err
		}
		_ = decodeStringField(item, "reasoning_content", &message.ReasoningContent)
		_ = decodeStringField(item, "tool_call_id", &message.ToolCallID)
		_ = decodeStringField(item, "name", &message.Name)
		if calls, ok := item["tool_calls"]; ok {
			if err := json.Unmarshal(calls, &message.ToolCalls); err != nil {
				return nil, fmt.Errorf("decode chat tool calls: %w", err)
			}
		}
		result = append(result, message)
	}
	return result, nil
}

// NormalizeOpenAIResponsesItems converts Responses output-item JSON maps to
// canonical messages. It recognizes message, output_text, reasoning,
// function_call, and function_call_output items; unknown item types are
// ignored so newer provider items can be handled by future adapters.
func NormalizeOpenAIResponsesItems(items []json.RawMessage) ([]Message, error) {
	result := make([]Message, 0, len(items))
	for _, raw := range items {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("decode responses item: %w", err)
		}
		var kind string
		_ = decodeStringField(item, "type", &kind)
		switch kind {
		case "message":
			var role string
			_ = decodeStringField(item, "role", &role)
			message := Message{Role: role}
			if err := decodeResponsesContent(item["content"], &message); err != nil {
				return nil, err
			}
			result = append(result, message)
		case "output_text":
			var text string
			_ = decodeStringField(item, "text", &text)
			result = append(result, Message{Role: "assistant", Content: text})
		case "reasoning":
			var message Message
			message.Role = "assistant"
			message.ReasoningSignatureSource = ReasoningSignatureSourceOpenAIResponses
			_ = decodeStringField(item, "id", &message.OpenAIResponsesReasoningID)
			_ = decodeStringField(item, "status", &message.OpenAIResponsesReasoningStatus)
			_ = decodeStringField(item, "encrypted_content", &message.ReasoningSignature)
			if summary, ok := item["summary"]; ok {
				message.OpenAIResponsesReasoningSummary = json.RawMessage(append([]byte(nil), summary...))
			}
			result = append(result, message)
		case "function_call":
			var call ToolCallDescriptor
			call.Type = "function"
			_ = decodeStringField(item, "id", &call.OpenAIResponsesID)
			_ = decodeStringField(item, "call_id", &call.OpenAIResponsesCallID)
			_ = decodeStringField(item, "status", &call.OpenAIResponsesStatus)
			_ = decodeStringField(item, "name", &call.Function.Name)
			_ = decodeStringField(item, "arguments", &call.Function.Arguments)
			call.ID = call.OpenAIResponsesCallID
			result = append(result, Message{Role: "assistant", ToolCalls: []ToolCallDescriptor{call}})
		case "function_call_output":
			var message Message
			message.Role = "tool"
			_ = decodeStringField(item, "call_id", &message.ToolCallID)
			_ = decodeStringField(item, "output", &message.Content)
			result = append(result, message)
		}
	}
	return result, nil
}

func openAIChatToolCallMap(call ToolCallDescriptor) map[string]any {
	id := call.ID
	if id == "" {
		id = call.OpenAIResponsesCallID
	}
	return map[string]any{"id": id, "type": "function", "function": map[string]any{"name": call.Function.Name, "arguments": call.Function.Arguments}}
}

func protocolMessageText(message Message) string {
	if message.Content != "" {
		return message.Content
	}
	return collapseTextContentParts(message.ContentParts)
}

func protocolChatContent(message Message) (any, error) { return openAIContentValue(message) }
func protocolResponsesContent(message Message) ([]map[string]any, error) {
	return openAIResponsesMessageContent(message, message.Role == "assistant")
}

func protocolResponsesCallID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if _, raw, ok := splitLegacyToolCallID(id); ok {
		return raw
	}
	if strings.HasPrefix(id, "tc_") {
		parts := strings.SplitN(id, "_", 3)
		if len(parts) == 3 && parts[2] != "" {
			return parts[2]
		}
	}
	return id
}

func normalizeProtocolChatTools(items []json.RawMessage) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(items))
	for _, raw := range items {
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("decode chat tool: %w", err)
		}
		if _, ok := value["function"]; !ok {
			name, _ := value["name"].(string)
			fn := map[string]any{"name": name, "parameters": value["parameters"]}
			if description, ok := value["description"]; ok {
				fn["description"] = description
			}
			value = map[string]any{"type": "function", "function": fn}
		}
		result = append(result, value)
	}
	return result, nil
}

func normalizeProtocolResponsesTools(items []json.RawMessage) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(items))
	for _, raw := range items {
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("decode responses tool: %w", err)
		}
		source := value
		if fn, ok := value["function"].(map[string]any); ok {
			source = fn
		}
		tool := map[string]any{"type": "function", "name": source["name"]}
		for _, key := range []string{"description", "parameters", "strict"} {
			if v, ok := source[key]; ok {
				tool[key] = v
			}
		}
		if _, ok := tool["parameters"]; !ok {
			tool["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		result = append(result, tool)
	}
	return result, nil
}

func decodeStringField(fields map[string]json.RawMessage, key string, target *string) error {
	if raw, ok := fields[key]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, target); err != nil {
			return fmt.Errorf("decode %s: %w", key, err)
		}
	}
	return nil
}

func decodeChatContent(raw json.RawMessage, message *Message) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if raw[0] == '"' {
		return json.Unmarshal(raw, &message.Content)
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return fmt.Errorf("decode chat content: %w", err)
	}
	for _, part := range parts {
		var kind string
		_ = decodeStringField(part, "type", &kind)
		switch kind {
		case "text", "input_text", "output_text", "":
			var text string
			_ = decodeStringField(part, "text", &text)
			if text != "" {
				message.ContentParts = append(message.ContentParts, ContentPart{Type: contentPartTypeText, Text: text})
				message.Content += text
			}
		case "image_url":
			var imageURL struct {
				URL string `json:"url"`
			}
			if rawImage, ok := part["image_url"]; ok && string(rawImage) != "null" {
				if err := json.Unmarshal(rawImage, &imageURL); err != nil {
					return fmt.Errorf("decode chat image_url: %w", err)
				}
			}
			message.ContentParts = append(message.ContentParts, ContentPart{Type: contentPartTypeImage, Image: &ImageContent{Path: imageURL.URL}})
		}
	}
	return nil
}

func decodeResponsesContent(raw json.RawMessage, message *Message) error {
	if len(raw) == 0 {
		return nil
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return fmt.Errorf("decode responses content: %w", err)
	}
	for _, part := range parts {
		var kind string
		_ = decodeStringField(part, "type", &kind)
		if kind == "output_text" || kind == "text" {
			var text string
			_ = decodeStringField(part, "text", &text)
			if text != "" {
				message.ContentParts = append(message.ContentParts, ContentPart{Type: contentPartTypeText, Text: text})
			}
			message.Content += text
		}
	}
	return nil
}
