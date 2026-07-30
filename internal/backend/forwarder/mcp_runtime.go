package forwarder

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"cursor/gen/agentv1"
	"google.golang.org/protobuf/types/known/structpb"
)

type mcpRPCRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int            `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type mcpRPCResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type mcpToolsResult struct {
	Tools []struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"inputSchema"`
	} `json:"tools"`
}

// discoverMCPTools 通过 MCP stdio transport 获取工具 schema。
// HTTP/SSE server 继续由 Cursor 客户端执行，避免在本地重复实现连接复用。
func discoverMCPTools(ctx context.Context, server normalizedMCPServer) ([]*agentv1.McpToolDescriptor, error) {
	if strings.TrimSpace(server.Command) == "" {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	commandCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, server.Command, server.Args...)
	if strings.TrimSpace(server.Cwd) != "" {
		cmd.Dir = server.Cwd
	}
	env := append([]string{}, os.Environ()...)
	for key, value := range server.Env {
		env = append(env, key+"="+value)
	}
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mcp server %q: %w", server.ServerName, err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	reader := bufio.NewReader(stdout)
	writeRequest := func(request mcpRPCRequest) error {
		payload, marshalErr := json.Marshal(request)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err := fmt.Fprintf(stdin, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
			return err
		}
		_, err := stdin.Write(payload)
		return err
	}
	readResponse := func(expectedID int) (mcpRPCResponse, error) {
		for {
			response, err := readMCPRPCResponse(reader)
			if err != nil {
				return mcpRPCResponse{}, err
			}
			if response.ID == expectedID {
				return response, nil
			}
		}
	}
	if err := writeRequest(mcpRPCRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "cursor-byok", "version": "dev"},
	}}); err != nil {
		return nil, err
	}
	initialized, err := readResponse(1)
	if err != nil {
		return nil, err
	}
	if len(initialized.Error) > 0 && string(initialized.Error) != "null" {
		return nil, fmt.Errorf("mcp initialize failed: %s", initialized.Error)
	}
	if err := writeRequest(mcpRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"}); err != nil {
		return nil, err
	}
	if err := writeRequest(mcpRPCRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list", Params: map[string]any{}}); err != nil {
		return nil, err
	}
	toolsResponse, err := readResponse(2)
	if err != nil {
		return nil, err
	}
	if len(toolsResponse.Error) > 0 && string(toolsResponse.Error) != "null" {
		return nil, fmt.Errorf("mcp tools/list failed: %s", toolsResponse.Error)
	}
	var result mcpToolsResult
	if err := json.Unmarshal(toolsResponse.Result, &result); err != nil {
		return nil, fmt.Errorf("decode mcp tools/list: %w", err)
	}
	output := make([]*agentv1.McpToolDescriptor, 0, len(result.Tools))
	for _, tool := range result.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		descriptor := &agentv1.McpToolDescriptor{ToolName: name}
		if description := strings.TrimSpace(tool.Description); description != "" {
			descriptor.Description = &description
		}
		if len(tool.InputSchema) > 0 {
			value, valueErr := structpb.NewValue(tool.InputSchema)
			if valueErr == nil {
				descriptor.InputSchema = value
			}
		}
		output = append(output, descriptor)
	}
	return output, nil
}

func readMCPRPCResponse(reader *bufio.Reader) (mcpRPCResponse, error) {
	var length int
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return mcpRPCResponse{}, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			parsed, parseErr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.ToLower(line), "content-length:")))
			if parseErr != nil {
				return mcpRPCResponse{}, parseErr
			}
			length = parsed
		}
	}
	if length <= 0 {
		return mcpRPCResponse{}, fmt.Errorf("mcp response content length is missing")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return mcpRPCResponse{}, err
	}
	var response mcpRPCResponse
	if err := json.Unmarshal(bytes.TrimSpace(payload), &response); err != nil {
		return mcpRPCResponse{}, err
	}
	return response, nil
}
