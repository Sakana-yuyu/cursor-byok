// exec_open_misc.go 承载其余 open 工具：Grep/ReadLints/Ls/Mcp/ListMcpResources/ReadMcpResource 请求构造。
package execbridge

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"


	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func (bridge *Bridge) openGrep(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	input, err := DecodeGrepToolArgs(toolCall.ArgsJSON, toolCall.CallID)
	if err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode Grep args failed: %w", err)
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-grep-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_GrepArgs{
					GrepArgs: input,
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingExec{
		MessageID:   messageID,
		ExecID:      execID,
		ArgsJSON:    append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:  toolCall.CallID,
		ExecKind:    "grep",
		StreamState: "opened",
		OpenedAt:    time.Now().UTC(),
	}, nil
}

// openReadLints 构造 ReadLints 对应的执行桥请求。
func (bridge *Bridge) openReadLints(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	args, err := decodeArgsMap(toolCall.ArgsJSON)
	if err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode ReadLints args failed: %w", err)
	}
	paths := readStringSliceArg(args, "paths")
	path := ""
	if len(paths) > 0 {
		path = strings.TrimSpace(paths[0])
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-diagnostics-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_DiagnosticsArgs{
					DiagnosticsArgs: &agentv1.DiagnosticsArgs{
						Path:       path,
						ToolCallId: toolCall.CallID,
					},
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingExec{
		MessageID:   messageID,
		ExecID:      execID,
		ArgsJSON:    append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:  toolCall.CallID,
		ExecKind:    "diagnostics",
		StreamState: "opened",
	}, nil
}

// openLs 构造 Ls 对应的执行桥请求。
func (bridge *Bridge) openLs(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	var input struct {
		Path   string   `json:"path"`
		Ignore []string `json:"ignore,omitempty"`
	}
	if err := json.Unmarshal(toolCall.ArgsJSON, &input); err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode Ls args failed: %w", err)
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-ls-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_LsArgs{
					LsArgs: &agentv1.LsArgs{
						Path:       strings.TrimSpace(input.Path),
						Ignore:     append([]string(nil), input.Ignore...),
						ToolCallId: toolCall.CallID,
					},
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingExec{
		MessageID:   messageID,
		ExecID:      execID,
		ArgsJSON:    append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:  toolCall.CallID,
		ExecKind:    "ls",
		StreamState: "opened",
		OpenedAt:    time.Now().UTC(),
	}, nil
}

// openMcp 构造 CallMcpTool 对应的执行桥请求。
func (bridge *Bridge) openMcp(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	input, err := runtimecore.DecodeMCPToolPayload(toolCall.ArgsJSON)
	if err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode CallMcpTool args failed: %w", err)
	}
	serverIdentifier := strings.TrimSpace(input.Server)
	if serverIdentifier == "" {
		serverIdentifier = strings.TrimSpace(input.ProviderIdentifier)
	}
	toolName := strings.TrimSpace(input.ToolName)
	if toolName == "" {
		toolName = runtimecore.InferMCPToolName(serverIdentifier, input.Name)
	}
	if serverIdentifier == "" && strings.TrimSpace(input.Name) != "" {
		serverIdentifier = runtimecore.InferMCPServerIdentifier(input.Name)
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-mcp-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_McpArgs{
					McpArgs: &agentv1.McpArgs{
						Name:               canonicalMCPToolLookupName(serverIdentifier, toolName),
						Args:               buildStructValueMap(input.Arguments),
						ToolCallId:         toolCall.CallID,
						ProviderIdentifier: serverIdentifier,
						ToolName:           toolName,
					},
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingExec{
		MessageID:   messageID,
		ExecID:      execID,
		ArgsJSON:    append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:  toolCall.CallID,
		ExecKind:    "mcp",
		StreamState: "opened",
	}, nil
}

// openListMcpResources 构造 ListMcpResources 对应的执行桥请求。
func (bridge *Bridge) openListMcpResources(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	var input struct {
		Server string `json:"server,omitempty"`
	}
	if err := json.Unmarshal(toolCall.ArgsJSON, &input); err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode ListMcpResources args failed: %w", err)
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-list-mcp-resources-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_ListMcpResourcesExecArgs{
					ListMcpResourcesExecArgs: &agentv1.ListMcpResourcesExecArgs{
						Server: stringPtr(strings.TrimSpace(input.Server)),
					},
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingExec{
		MessageID:   messageID,
		ExecID:      execID,
		ArgsJSON:    append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:  toolCall.CallID,
		ExecKind:    "list_mcp_resources",
		StreamState: "opened",
	}, nil
}

// openReadMcpResource 构造 FetchMcpResource 对应的执行桥请求。
func (bridge *Bridge) openReadMcpResource(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	var input struct {
		Server       string `json:"server"`
		URI          string `json:"uri"`
		DownloadPath string `json:"downloadPath,omitempty"`
	}
	if err := json.Unmarshal(toolCall.ArgsJSON, &input); err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode FetchMcpResource args failed: %w", err)
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-read-mcp-resource-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_ReadMcpResourceExecArgs{
					ReadMcpResourceExecArgs: &agentv1.ReadMcpResourceExecArgs{
						Server:       strings.TrimSpace(input.Server),
						Uri:          strings.TrimSpace(input.URI),
						DownloadPath: stringPtr(strings.TrimSpace(input.DownloadPath)),
					},
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingExec{
		MessageID:   messageID,
		ExecID:      execID,
		ArgsJSON:    append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:  toolCall.CallID,
		ExecKind:    "read_mcp_resource",
		StreamState: "opened",
	}, nil
}
