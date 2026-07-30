package forwarder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"cursor/gen/agentv1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/types/known/structpb"
)

const mcpDiscoveryTimeout = 2 * time.Second

// discoverMCPTools uses the official MCP SDK to retrieve schemas from stdio servers.
// HTTP and SSE servers continue to execute through the Cursor client connection.
func discoverMCPTools(ctx context.Context, server normalizedMCPServer) ([]*agentv1.McpToolDescriptor, error) {
	if strings.TrimSpace(server.Command) == "" {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, mcpDiscoveryTimeout)
	defer cancel()

	cmd := exec.CommandContext(discoveryCtx, server.Command, server.Args...)
	if cwd := strings.TrimSpace(server.Cwd); cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = append([]string{}, os.Environ()...)
	for key, value := range server.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "cursor-byok",
		Version: "dev",
	}, nil)
	session, err := client.Connect(discoveryCtx, &mcp.CommandTransport{
		Command:           cmd,
		TerminateDuration: time.Second,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect mcp server %q: %w", server.ServerName, err)
	}
	defer session.Close()

	var output []*agentv1.McpToolDescriptor
	cursor := ""
	seenCursors := make(map[string]struct{})
	for {
		result, listErr := session.ListTools(discoveryCtx, &mcp.ListToolsParams{Cursor: cursor})
		if listErr != nil {
			return nil, fmt.Errorf("list mcp tools for %q: %w", server.ServerName, listErr)
		}
		for _, tool := range result.Tools {
			if descriptor := mcpToolDescriptor(tool); descriptor != nil {
				output = append(output, descriptor)
			}
		}

		nextCursor := strings.TrimSpace(result.NextCursor)
		if nextCursor == "" {
			break
		}
		if _, exists := seenCursors[nextCursor]; exists {
			return nil, fmt.Errorf("list mcp tools for %q returned a repeated cursor", server.ServerName)
		}
		seenCursors[nextCursor] = struct{}{}
		cursor = nextCursor
	}
	return output, nil
}

func mcpToolDescriptor(tool *mcp.Tool) *agentv1.McpToolDescriptor {
	if tool == nil {
		return nil
	}
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		return nil
	}
	descriptor := &agentv1.McpToolDescriptor{ToolName: name}
	if description := strings.TrimSpace(tool.Description); description != "" {
		descriptor.Description = &description
	}
	if tool.InputSchema != nil {
		if schema, err := structpb.NewValue(tool.InputSchema); err == nil {
			descriptor.InputSchema = schema
		}
	}
	return descriptor
}
