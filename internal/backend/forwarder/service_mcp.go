// service_mcp.go 承载 MCP 工具改写域：请求上下文中的 MCP server/tool 路由提取、
// 直连 MCP 调用改写（rewriteDirectMCPToolInvocation）与 CallMcpTool 归一化。
package forwarder

import (
	"encoding/json"
	"strings"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func (service *Service) updateStreamMCPToolServers(stream *ActiveStream, requestContext *agentv1.RequestContext) {
	if stream == nil {
		return
	}
	servers, toolNames := collectMCPToolRoutes(requestContext)
	stream.mu.Lock()
	stream.MCPToolServers = make(map[string]string, len(servers))
	stream.MCPToolNames = make(map[string]string, len(toolNames))
	for toolName, serverIdentifier := range servers {
		trimmedToolName := strings.TrimSpace(toolName)
		trimmedServerIdentifier := strings.TrimSpace(serverIdentifier)
		if trimmedToolName == "" || trimmedServerIdentifier == "" {
			continue
		}
		stream.MCPToolServers[trimmedToolName] = trimmedServerIdentifier
		if routedToolName := strings.TrimSpace(toolNames[toolName]); routedToolName != "" {
			stream.MCPToolNames[trimmedToolName] = routedToolName
		}
	}
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
}

func (service *Service) rewriteDirectMCPToolInvocation(stream *ActiveStream, invocation runtimecore.ToolInvocation) runtimecore.ToolInvocation {
	toolName := strings.TrimSpace(invocation.ToolName)
	if toolName == "" || isKnownBuiltInToolName(toolName) {
		return invocation
	}
	serverIdentifier, routedToolName := lookupMCPToolRoute(stream, toolName)
	if serverIdentifier == "" {
		return invocation
	}
	if routedToolName == "" {
		routedToolName = toolName
	}

	arguments := make(map[string]any)
	if len(invocation.ArgsJSON) > 0 {
		_ = json.Unmarshal(invocation.ArgsJSON, &arguments)
	}
	payload := struct {
		Server    string         `json:"server"`
		ToolName  string         `json:"toolName"`
		Arguments map[string]any `json:"arguments,omitempty"`
	}{
		Server:    serverIdentifier,
		ToolName:  routedToolName,
		Arguments: arguments,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return invocation
	}
	invocation.ToolName = "CallMcpTool"
	invocation.ArgsJSON = encoded
	return invocation
}

func (service *Service) normalizeCallMCPToolInvocation(stream *ActiveStream, invocation runtimecore.ToolInvocation) runtimecore.ToolInvocation {
	if strings.TrimSpace(invocation.ToolName) != "CallMcpTool" {
		return invocation
	}

	payload, err := runtimecore.DecodeMCPToolPayload(invocation.ArgsJSON)
	if err != nil {
		return invocation
	}

	serverIdentifier := firstNonEmpty(payload.Server, payload.ProviderIdentifier)
	toolName := strings.TrimSpace(payload.ToolName)
	name := strings.TrimSpace(payload.Name)
	if toolName == "" {
		toolName = runtimecore.InferMCPToolName(serverIdentifier, name)
	}
	if serverIdentifier == "" {
		serverIdentifier = lookupMCPToolServer(stream, toolName)
		if serverIdentifier == "" && name != "" {
			serverIdentifier = runtimecore.InferMCPServerIdentifier(name)
		}
	}

	if toolName == "" {
		return invocation
	}

	normalized := struct {
		Server    string         `json:"server"`
		ToolName  string         `json:"toolName"`
		Arguments map[string]any `json:"arguments,omitempty"`
	}{
		Server:    serverIdentifier,
		ToolName:  toolName,
		Arguments: payload.Arguments,
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return invocation
	}
	invocation.ArgsJSON = encoded
	return invocation
}

func lookupMCPToolServer(stream *ActiveStream, toolName string) string {
	serverIdentifier, _ := lookupMCPToolRoute(stream, toolName)
	return serverIdentifier
}

func lookupMCPToolRoute(stream *ActiveStream, toolName string) (string, string) {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return "", ""
	}
	if stream != nil {
		stream.mu.Lock()
		serverIdentifier := strings.TrimSpace(stream.MCPToolServers[trimmedToolName])
		routedToolName := strings.TrimSpace(stream.MCPToolNames[trimmedToolName])
		stream.mu.Unlock()
		if serverIdentifier != "" {
			return serverIdentifier, routedToolName
		}
	}
	return "", ""
}
