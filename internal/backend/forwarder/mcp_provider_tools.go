package forwarder

import (
	"encoding/json"
	"fmt"
	"strings"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"

	"google.golang.org/protobuf/encoding/protojson"
)

func appendConversationMCPTools(base []json.RawMessage, conversation *ConversationFile) ([]json.RawMessage, int, error) {
	definitions, err := conversationMCPToolDefinitions(conversation)
	if err != nil || len(definitions) == 0 {
		return base, 0, err
	}
	seen := make(map[string]struct{}, len(base)+len(definitions))
	for _, raw := range base {
		name, nameErr := extractToolName(raw)
		if nameErr != nil {
			return nil, 0, nameErr
		}
		seen[name] = struct{}{}
	}
	result := append([]json.RawMessage(nil), base...)
	added := 0
	for _, definition := range definitions {
		if definition == nil {
			continue
		}
		name := strings.TrimSpace(definition.GetName())
		if name == "" || isKnownBuiltInToolName(name) {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		raw, buildErr := buildProviderMCPTool(definition)
		if buildErr != nil {
			return nil, 0, buildErr
		}
		seen[name] = struct{}{}
		result = append(result, raw)
		added++
	}
	return result, added, nil
}

func conversationMCPToolDefinitions(conversation *ConversationFile) ([]*agentv1.McpToolDefinition, error) {
	if conversation == nil {
		return nil, nil
	}
	ordered := make([]string, 0)
	byName := make(map[string]*agentv1.McpToolDefinition)
	addDefinition := func(definition *agentv1.McpToolDefinition) {
		if definition == nil {
			return
		}
		name := strings.TrimSpace(definition.GetName())
		if name == "" {
			return
		}
		if _, exists := byName[name]; !exists {
			ordered = append(ordered, name)
		}
		byName[name] = definition
	}
	if conversation.MCPToolsInitialized {
		for _, definition := range conversation.MCPTools {
			addDefinition(definition)
		}
		result := make([]*agentv1.McpToolDefinition, 0, len(ordered))
		for _, name := range ordered {
			result = append(result, byName[name])
		}
		return result, nil
	}
	for _, entry := range conversation.Entries {
		if strings.TrimSpace(entry.Kind) != "request_context" || len(entry.Payload) == 0 {
			continue
		}
		requestContext := &agentv1.RequestContext{}
		if err := protojson.Unmarshal(entry.Payload, requestContext); err != nil {
			return nil, fmt.Errorf("decode request context MCP tools: %w", err)
		}
		for _, definition := range requestContext.GetTools() {
			addDefinition(definition)
		}
	}
	result := make([]*agentv1.McpToolDefinition, 0, len(ordered))
	for _, name := range ordered {
		result = append(result, byName[name])
	}
	return result, nil
}

func conversationMCPToolNameSet(conversation *ConversationFile) (map[string]struct{}, error) {
	definitions, err := conversationMCPToolDefinitions(conversation)
	if err != nil || len(definitions) == 0 {
		return nil, err
	}
	result := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if definition == nil {
			continue
		}
		if name := strings.TrimSpace(definition.GetName()); name != "" {
			if isKnownBuiltInToolName(name) {
				continue
			}
			result[name] = struct{}{}
		}
	}
	return result, nil
}

func rewriteConversationMCPToolInvocation(conversation *ConversationFile, invocation runtimecore.ToolInvocation) (runtimecore.ToolInvocation, bool, error) {
	name := strings.TrimSpace(invocation.ToolName)
	if name == "" || isKnownBuiltInToolName(name) {
		return invocation, false, nil
	}
	definitions, err := conversationMCPToolDefinitions(conversation)
	if err != nil {
		return invocation, false, err
	}
	for _, definition := range definitions {
		if definition == nil || strings.TrimSpace(definition.GetName()) != name {
			continue
		}
		arguments := make(map[string]any)
		if len(invocation.ArgsJSON) > 0 {
			if err := json.Unmarshal(invocation.ArgsJSON, &arguments); err != nil {
				return invocation, false, fmt.Errorf("decode MCP tool %q arguments: %w", name, err)
			}
		}
		payload := map[string]any{
			"server":    strings.TrimSpace(definition.GetProviderIdentifier()),
			"toolName":  firstNonEmpty(definition.GetToolName(), definition.GetName()),
			"arguments": arguments,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return invocation, false, fmt.Errorf("encode MCP tool %q invocation: %w", name, err)
		}
		invocation.ToolName = "CallMcpTool"
		invocation.ArgsJSON = encoded
		return invocation, true, nil
	}
	return invocation, false, nil
}

func buildProviderMCPTool(definition *agentv1.McpToolDefinition) (json.RawMessage, error) {
	parameters := any(map[string]any{"type": "object", "properties": map[string]any{}})
	if schema := definition.GetInputSchema(); schema != nil {
		if object, ok := schema.AsInterface().(map[string]any); ok {
			parameters = object
		}
	}
	descriptor := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        strings.TrimSpace(definition.GetName()),
			"description": strings.TrimSpace(definition.GetDescription()),
			"parameters":  parameters,
		},
	}
	raw, err := json.Marshal(descriptor)
	if err != nil {
		return nil, fmt.Errorf("encode MCP provider tool %q: %w", definition.GetName(), err)
	}
	return raw, nil
}
