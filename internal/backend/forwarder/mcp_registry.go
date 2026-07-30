package forwarder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"cursor/gen/agentv1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/proto"
)

const (
	mcpConnectTimeout       = 10 * time.Second
	mcpOperationTimeout     = 60 * time.Second
	mcpMaxDiscoveredTools   = 128
	mcpMaxSchemaBytes       = 256 << 10
	mcpProcessCloseDuration = 2 * time.Second
)

type MCPRuntimeStatus string

const (
	MCPRuntimeDisconnected MCPRuntimeStatus = "disconnected"
	MCPRuntimeConnecting   MCPRuntimeStatus = "connecting"
	MCPRuntimeConnected    MCPRuntimeStatus = "connected"
	MCPRuntimeError        MCPRuntimeStatus = "error"
)

type MCPRuntimeSnapshot struct {
	Identifier  string           `json:"identifier"`
	Name        string           `json:"name"`
	Source      string           `json:"source"`
	Scope       string           `json:"scope"`
	Transport   string           `json:"transport"`
	Command     string           `json:"command,omitempty"`
	URL         string           `json:"url,omitempty"`
	Status      MCPRuntimeStatus `json:"status"`
	ToolCount   int              `json:"toolCount"`
	LastError   string           `json:"lastError,omitempty"`
	ConnectedAt time.Time        `json:"connectedAt,omitempty"`
	UpdatedAt   time.Time        `json:"updatedAt"`
}

type mcpRuntimeEntry struct {
	config      MCPServerConfig
	status      MCPRuntimeStatus
	session     *mcp.ClientSession
	tools       []*agentv1.McpToolDescriptor
	lastError   string
	connectedAt time.Time
	updatedAt   time.Time
	generation  uint64
}

// MCPRuntimeRegistry owns explicitly connected MCP sessions. Sync is read-only:
// only Connect may start a process or perform network I/O.
type MCPRuntimeRegistry struct {
	mu      sync.RWMutex
	entries map[string]*mcpRuntimeEntry
	closed  bool
}

func NewMCPRuntimeRegistry() *MCPRuntimeRegistry {
	return &MCPRuntimeRegistry{entries: make(map[string]*mcpRuntimeEntry)}
}

var sharedMCPRuntimeRegistry = NewMCPRuntimeRegistry()

func SharedMCPRuntimeRegistry() *MCPRuntimeRegistry {
	return sharedMCPRuntimeRegistry
}

func (registry *MCPRuntimeRegistry) Sync(configs []MCPServerConfig) {
	registry.sync(configs, false, nil)
}

// Replace synchronizes an authoritative active-workspace view and disconnects
// entries that are no longer enabled in that view.
func (registry *MCPRuntimeRegistry) Replace(configs []MCPServerConfig) {
	registry.sync(configs, true, nil)
}

// ReplaceScopes applies an authoritative view only to the requested scopes.
func (registry *MCPRuntimeRegistry) ReplaceScopes(configs []MCPServerConfig, scopes ...MCPConfigScope) {
	filter := make(map[MCPConfigScope]bool, len(scopes))
	for _, scope := range scopes {
		filter[scope] = true
	}
	registry.sync(configs, true, filter)
}

func (registry *MCPRuntimeRegistry) sync(configs []MCPServerConfig, replace bool, replaceScopes map[MCPConfigScope]bool) {
	if registry == nil {
		return
	}
	now := time.Now().UTC()
	next := make(map[string]MCPServerConfig, len(configs))
	for _, config := range configs {
		id := strings.ToLower(strings.TrimSpace(config.Identifier))
		if id == "" {
			continue
		}
		config = cloneMCPServerConfig(config)
		next[id] = config
	}
	var stale []*mcp.ClientSession
	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return
	}
	for id, entry := range registry.entries {
		config, ok := next[id]
		removeMissing := replace && !ok && (len(replaceScopes) == 0 || replaceScopes[entry.config.Scope])
		if removeMissing || (ok && !reflect.DeepEqual(entry.config, config)) {
			if entry.session != nil {
				stale = append(stale, entry.session)
			}
			delete(registry.entries, id)
		}
	}
	for id, config := range next {
		if _, ok := registry.entries[id]; ok {
			continue
		}
		registry.entries[id] = &mcpRuntimeEntry{
			config:    config,
			status:    MCPRuntimeDisconnected,
			updatedAt: now,
		}
	}
	registry.mu.Unlock()
	for _, session := range stale {
		_ = session.Close()
	}
}

func (registry *MCPRuntimeRegistry) Connect(ctx context.Context, identifier string) error {
	if registry == nil {
		return fmt.Errorf("mcp runtime registry is nil")
	}
	id := strings.ToLower(strings.TrimSpace(identifier))
	if id == "" {
		return fmt.Errorf("mcp server identifier is required")
	}
	registry.mu.Lock()
	entry, ok := registry.entries[id]
	if !ok {
		registry.mu.Unlock()
		return fmt.Errorf("mcp server %q not found", identifier)
	}
	if entry.status == MCPRuntimeConnected && entry.session != nil {
		registry.mu.Unlock()
		return nil
	}
	entry.generation++
	generation := entry.generation
	entry.status = MCPRuntimeConnecting
	entry.lastError = ""
	entry.updatedAt = time.Now().UTC()
	config := cloneMCPServerConfig(entry.config)
	registry.mu.Unlock()

	connectCtx, cancel := withDefaultTimeout(ctx, mcpConnectTimeout)
	defer cancel()
	session, tools, err := connectMCPRuntime(connectCtx, config)
	now := time.Now().UTC()
	registry.mu.Lock()
	entry, ok = registry.entries[id]
	if !ok || entry.generation != generation || registry.closed {
		registry.mu.Unlock()
		if session != nil {
			_ = session.Close()
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("mcp server %q changed while connecting", identifier)
	}
	entry.updatedAt = now
	if err != nil {
		sanitizedError := sanitizeMCPRuntimeError(err, config)
		entry.status = MCPRuntimeError
		entry.lastError = sanitizedError
		entry.session = nil
		entry.tools = nil
		registry.mu.Unlock()
		return fmt.Errorf("%s", sanitizedError)
	}
	oldSession := entry.session
	entry.session = session
	entry.tools = cloneMCPToolDescriptors(tools)
	entry.status = MCPRuntimeConnected
	entry.lastError = ""
	entry.connectedAt = now
	registry.mu.Unlock()
	if oldSession != nil && oldSession != session {
		_ = oldSession.Close()
	}
	return nil
}

func (registry *MCPRuntimeRegistry) Disconnect(identifier string) error {
	if registry == nil {
		return nil
	}
	id := strings.ToLower(strings.TrimSpace(identifier))
	registry.mu.Lock()
	entry, ok := registry.entries[id]
	if !ok {
		registry.mu.Unlock()
		return fmt.Errorf("mcp server %q not found", identifier)
	}
	entry.generation++
	session := entry.session
	entry.session = nil
	entry.tools = nil
	entry.status = MCPRuntimeDisconnected
	entry.lastError = ""
	entry.connectedAt = time.Time{}
	entry.updatedAt = time.Now().UTC()
	registry.mu.Unlock()
	if session != nil {
		return session.Close()
	}
	return nil
}

func (registry *MCPRuntimeRegistry) Snapshot() []MCPRuntimeSnapshot {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	items := make([]MCPRuntimeSnapshot, 0, len(registry.entries))
	for _, entry := range registry.entries {
		items = append(items, snapshotMCPRuntimeEntry(entry))
	}
	registry.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].Identifier < items[j].Identifier })
	return items
}

func (registry *MCPRuntimeRegistry) Descriptor(identifier string) (*agentv1.McpDescriptor, bool) {
	if registry == nil {
		return nil, false
	}
	id := strings.ToLower(strings.TrimSpace(identifier))
	registry.mu.RLock()
	entry, ok := registry.entries[id]
	if !ok || entry.status != MCPRuntimeConnected || entry.session == nil {
		registry.mu.RUnlock()
		return nil, false
	}
	config := cloneMCPServerConfig(entry.config)
	tools := cloneMCPToolDescriptors(entry.tools)
	registry.mu.RUnlock()
	descriptor := buildMCPDescriptor(config)
	descriptor.Tools = tools
	return descriptor, true
}

func (registry *MCPRuntimeRegistry) Descriptors() []*agentv1.McpDescriptor {
	items := registry.Snapshot()
	result := make([]*agentv1.McpDescriptor, 0, len(items))
	for _, item := range items {
		if descriptor, ok := registry.Descriptor(item.Identifier); ok {
			result = append(result, descriptor)
		}
	}
	return result
}

func (registry *MCPRuntimeRegistry) CallTool(ctx context.Context, identifier, name string, arguments map[string]any) (*mcp.CallToolResult, error) {
	session, err := registry.session(identifier)
	if err != nil {
		return nil, err
	}
	operationCtx, cancel := withDefaultTimeout(ctx, mcpOperationTimeout)
	defer cancel()
	return session.CallTool(operationCtx, &mcp.CallToolParams{Name: strings.TrimSpace(name), Arguments: arguments})
}

func (registry *MCPRuntimeRegistry) ListResources(ctx context.Context, identifier string) ([]*mcp.Resource, error) {
	session, err := registry.session(identifier)
	if err != nil {
		return nil, err
	}
	operationCtx, cancel := withDefaultTimeout(ctx, mcpOperationTimeout)
	defer cancel()
	var resources []*mcp.Resource
	cursor := ""
	seen := make(map[string]struct{})
	for {
		result, listErr := session.ListResources(operationCtx, &mcp.ListResourcesParams{Cursor: cursor})
		if listErr != nil {
			return nil, listErr
		}
		resources = append(resources, result.Resources...)
		next := strings.TrimSpace(result.NextCursor)
		if next == "" {
			return resources, nil
		}
		if _, exists := seen[next]; exists {
			return nil, fmt.Errorf("mcp resources/list returned a repeated cursor")
		}
		seen[next] = struct{}{}
		cursor = next
	}
}

func (registry *MCPRuntimeRegistry) ReadResource(ctx context.Context, identifier, uri string) (*mcp.ReadResourceResult, error) {
	session, err := registry.session(identifier)
	if err != nil {
		return nil, err
	}
	operationCtx, cancel := withDefaultTimeout(ctx, mcpOperationTimeout)
	defer cancel()
	return session.ReadResource(operationCtx, &mcp.ReadResourceParams{URI: strings.TrimSpace(uri)})
}

func (registry *MCPRuntimeRegistry) Close() {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return
	}
	registry.closed = true
	var sessions []*mcp.ClientSession
	for _, entry := range registry.entries {
		entry.generation++
		if entry.session != nil {
			sessions = append(sessions, entry.session)
		}
		entry.session = nil
		entry.tools = nil
		entry.status = MCPRuntimeDisconnected
	}
	registry.mu.Unlock()
	for _, session := range sessions {
		_ = session.Close()
	}
	registry.mu.Lock()
	registry.entries = make(map[string]*mcpRuntimeEntry)
	registry.closed = false
	registry.mu.Unlock()
}

func (registry *MCPRuntimeRegistry) session(identifier string) (*mcp.ClientSession, error) {
	if registry == nil {
		return nil, fmt.Errorf("mcp runtime registry is nil")
	}
	id := strings.ToLower(strings.TrimSpace(identifier))
	registry.mu.RLock()
	entry, ok := registry.entries[id]
	if !ok || entry.status != MCPRuntimeConnected || entry.session == nil {
		registry.mu.RUnlock()
		return nil, fmt.Errorf("mcp server %q is not connected", identifier)
	}
	session := entry.session
	registry.mu.RUnlock()
	return session, nil
}

func connectMCPRuntime(ctx context.Context, config MCPServerConfig) (*mcp.ClientSession, []*agentv1.McpToolDescriptor, error) {
	transport, err := mcpRuntimeTransport(config)
	if err != nil {
		return nil, nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "cursor-byok", Version: "dev"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("connect mcp server %q: %w", config.Name, err)
	}
	tools, err := listBoundedMCPTools(ctx, session, config.Name)
	if err != nil {
		_ = session.Close()
		return nil, nil, err
	}
	return session, tools, nil
}

func mcpRuntimeTransport(config MCPServerConfig) (mcp.Transport, error) {
	transport := strings.ToLower(strings.TrimSpace(config.Transport))
	if transport == "" {
		transport = "stdio"
	}
	switch transport {
	case "stdio":
		if strings.TrimSpace(config.Command) == "" {
			return nil, fmt.Errorf("mcp stdio server %q has no command", config.Name)
		}
		cmd := exec.Command(config.Command, config.Args...)
		if cwd := strings.TrimSpace(config.Cwd); cwd != "" {
			cmd.Dir = cwd
		}
		cmd.Env = append([]string{}, os.Environ()...)
		for key, value := range config.Env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		return &mcp.CommandTransport{Command: cmd, TerminateDuration: mcpProcessCloseDuration}, nil
	case "http", "streamable-http", "streamable_http":
		if strings.TrimSpace(config.URL) == "" {
			return nil, fmt.Errorf("mcp http server %q has no url", config.Name)
		}
		return &mcp.StreamableClientTransport{Endpoint: config.URL, HTTPClient: mcpHTTPClient(config.Headers)}, nil
	case "sse":
		if strings.TrimSpace(config.URL) == "" {
			return nil, fmt.Errorf("mcp sse server %q has no url", config.Name)
		}
		return &mcp.SSEClientTransport{Endpoint: config.URL, HTTPClient: mcpHTTPClient(config.Headers)}, nil
	default:
		return nil, fmt.Errorf("unsupported mcp transport %q", config.Transport)
	}
}

func listBoundedMCPTools(ctx context.Context, session *mcp.ClientSession, serverName string) ([]*agentv1.McpToolDescriptor, error) {
	var output []*agentv1.McpToolDescriptor
	cursor := ""
	seen := make(map[string]struct{})
	schemaBytes := 0
	for {
		result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("list mcp tools for %q: %w", serverName, err)
		}
		for _, tool := range result.Tools {
			if len(output) >= mcpMaxDiscoveredTools {
				return nil, fmt.Errorf("mcp server %q exceeds the %d tool limit", serverName, mcpMaxDiscoveredTools)
			}
			if tool != nil && tool.InputSchema != nil {
				encoded, marshalErr := json.Marshal(tool.InputSchema)
				if marshalErr != nil {
					return nil, fmt.Errorf("marshal mcp tool schema for %q: %w", serverName, marshalErr)
				}
				schemaBytes += len(encoded)
				if schemaBytes > mcpMaxSchemaBytes {
					return nil, fmt.Errorf("mcp server %q exceeds the %d byte schema limit", serverName, mcpMaxSchemaBytes)
				}
			}
			if descriptor := mcpToolDescriptor(tool); descriptor != nil {
				output = append(output, descriptor)
			}
		}
		next := strings.TrimSpace(result.NextCursor)
		if next == "" {
			return output, nil
		}
		if _, exists := seen[next]; exists {
			return nil, fmt.Errorf("list mcp tools for %q returned a repeated cursor", serverName)
		}
		seen[next] = struct{}{}
		cursor = next
	}
}

type mcpHeaderTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (transport mcpHeaderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	for key, value := range transport.headers {
		cloned.Header.Set(key, value)
	}
	return transport.base.RoundTrip(cloned)
}

func mcpHTTPClient(headers map[string]string) *http.Client {
	base := http.DefaultTransport
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		base = transport.Clone()
	}
	return &http.Client{Transport: mcpHeaderTransport{base: base, headers: cloneStringMap(headers)}}
}

func snapshotMCPRuntimeEntry(entry *mcpRuntimeEntry) MCPRuntimeSnapshot {
	if entry == nil {
		return MCPRuntimeSnapshot{}
	}
	return MCPRuntimeSnapshot{
		Identifier:  entry.config.Identifier,
		Name:        entry.config.Name,
		Source:      string(entry.config.Source),
		Scope:       string(entry.config.Scope),
		Transport:   entry.config.Transport,
		Command:     entry.config.Command,
		URL:         entry.config.URL,
		Status:      entry.status,
		ToolCount:   len(entry.tools),
		LastError:   entry.lastError,
		ConnectedAt: entry.connectedAt,
		UpdatedAt:   entry.updatedAt,
	}
}

func sanitizeMCPRuntimeError(err error, config MCPServerConfig) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	redactions := make([]string, 0, len(config.Env)+len(config.Headers)+len(config.Args)+1)
	redactions = append(redactions, strings.TrimSpace(config.URL))
	for _, value := range config.Env {
		redactions = append(redactions, strings.TrimSpace(value))
	}
	for _, value := range config.Headers {
		redactions = append(redactions, strings.TrimSpace(value))
	}
	for _, value := range config.Args {
		redactions = append(redactions, strings.TrimSpace(value))
	}
	for _, value := range redactions {
		if value != "" {
			message = strings.ReplaceAll(message, value, "[redacted]")
		}
	}
	if len(message) > 512 {
		message = message[:512] + "..."
	}
	return message
}

func cloneMCPToolDescriptors(input []*agentv1.McpToolDescriptor) []*agentv1.McpToolDescriptor {
	if len(input) == 0 {
		return nil
	}
	output := make([]*agentv1.McpToolDescriptor, 0, len(input))
	for _, descriptor := range input {
		if descriptor == nil {
			continue
		}
		cloned, _ := proto.Clone(descriptor).(*agentv1.McpToolDescriptor)
		if cloned != nil {
			output = append(output, cloned)
		}
	}
	return output
}

func withDefaultTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}
