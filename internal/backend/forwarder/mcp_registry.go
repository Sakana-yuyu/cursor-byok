package forwarder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strconv"
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
	mcpMaxRuntimeErrorRunes = 512
)

var mcpRuntimeURLPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>]+`)

type MCPRuntimeStatus string

const (
	MCPRuntimeDisconnected MCPRuntimeStatus = "disconnected"
	MCPRuntimeConnecting   MCPRuntimeStatus = "connecting"
	MCPRuntimeConnected    MCPRuntimeStatus = "connected"
	MCPRuntimeDegraded     MCPRuntimeStatus = "degraded"
	MCPRuntimeError        MCPRuntimeStatus = "error"
)

type MCPRuntimeSnapshot struct {
	Identifier string           `json:"identifier"`
	Name       string           `json:"name"`
	Source     string           `json:"source"`
	Scope      string           `json:"scope"`
	Transport  string           `json:"transport"`
	Command    string           `json:"command,omitempty"`
	URL        string           `json:"url,omitempty"`
	Status     MCPRuntimeStatus `json:"status"`
	// ConfigFingerprint is a coarse non-secret shape hash, not runtime identity.
	ConfigFingerprint string           `json:"configFingerprint"`
	CapabilityStatus  MCPRuntimeStatus `json:"capabilityStatus"`
	ToolCount         int              `json:"toolCount"`
	LastError         string           `json:"lastError,omitempty"`
	ConnectedAt       time.Time        `json:"connectedAt,omitempty"`
	LastCheckedAt     time.Time        `json:"lastCheckedAt,omitempty"`
	UpdatedAt         time.Time        `json:"updatedAt"`
	RuntimeScope      string           `json:"runtimeScope"`
}

type mcpRuntimeEntry struct {
	config                     MCPServerConfig
	status                     MCPRuntimeStatus
	capabilityStatus           MCPRuntimeStatus
	session                    *mcp.ClientSession
	tools                      []*agentv1.McpToolDescriptor
	lastError                  string
	connectedAt                time.Time
	lastCheckedAt              time.Time
	updatedAt                  time.Time
	lastConnectAttemptAt       time.Time
	generation                 uint64
	activeCapabilityOperations int
	capabilityBatchFailed      bool
	capabilityBatchError       string
}

type mcpCapabilityOperation struct {
	key        string
	owner      *mcpRuntimeEntry
	generation uint64
	session    *mcp.ClientSession
	config     MCPServerConfig
}

// MCPRuntimeRegistry owns explicitly connected MCP sessions. Sync is read-only:
// only Connect may start a process or perform network I/O.
type MCPRuntimeRegistry struct {
	mu             sync.RWMutex
	entries        map[string]*mcpRuntimeEntry
	closed         bool
	nextGeneration uint64
}

func NewMCPRuntimeRegistry() *MCPRuntimeRegistry {
	return &MCPRuntimeRegistry{entries: make(map[string]*mcpRuntimeEntry)}
}

var sharedMCPRuntimeRegistry = NewMCPRuntimeRegistry()

func SharedMCPRuntimeRegistry() *MCPRuntimeRegistry {
	return sharedMCPRuntimeRegistry
}

func (registry *MCPRuntimeRegistry) nextGenerationLocked() uint64 {
	registry.nextGeneration++
	if registry.nextGeneration == 0 {
		registry.nextGeneration++
	}
	return registry.nextGeneration
}

// ReplaceScope synchronizes one authoritative runtime scope without touching other workspaces.
func (registry *MCPRuntimeRegistry) ReplaceScope(scope string, configs []MCPServerConfig) {
	if registry == nil {
		return
	}
	scope = normalizeMCPRuntimeScope(scope)
	now := time.Now().UTC()
	next := make(map[string]MCPServerConfig, len(configs))
	for _, config := range configs {
		id := strings.ToLower(strings.TrimSpace(config.Identifier))
		if id == "" {
			continue
		}
		config = cloneMCPServerConfig(config)
		config.RuntimeScope = scope
		next[id] = config
	}
	var stale []*mcp.ClientSession
	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return
	}
	for key, entry := range registry.entries {
		if normalizeMCPRuntimeScope(entry.config.RuntimeScope) != scope {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(entry.config.Identifier))
		config, ok := next[id]
		if !ok || !sameMCPRuntimeConfig(entry.config, config) {
			entry.generation = registry.nextGenerationLocked()
			entry.activeCapabilityOperations = 0
			entry.capabilityBatchFailed = false
			entry.capabilityBatchError = ""
			if entry.session != nil {
				stale = append(stale, entry.session)
			}
			delete(registry.entries, key)
		}
	}
	for id, config := range next {
		key := mcpRuntimeEntryKey(scope, id)
		if _, ok := registry.entries[key]; ok {
			continue
		}
		registry.entries[key] = &mcpRuntimeEntry{
			config:           config,
			status:           MCPRuntimeDisconnected,
			capabilityStatus: MCPRuntimeDisconnected,
			updatedAt:        now,
			generation:       registry.nextGenerationLocked(),
		}
	}
	registry.mu.Unlock()
	for _, session := range stale {
		_ = session.Close()
	}
}

func (registry *MCPRuntimeRegistry) Connect(ctx context.Context, scope string, identifier string) error {
	if registry == nil {
		return fmt.Errorf("mcp runtime registry is nil")
	}
	scope = normalizeMCPRuntimeScope(scope)
	id := strings.ToLower(strings.TrimSpace(identifier))
	if id == "" {
		return fmt.Errorf("mcp server identifier is required")
	}
	registry.mu.Lock()
	key := mcpRuntimeEntryKey(scope, id)
	entry, ok := registry.entries[key]
	if !ok {
		registry.mu.Unlock()
		return fmt.Errorf("mcp server %q not found in runtime scope %q", identifier, scope)
	}
	if entry.status == MCPRuntimeConnected && entry.session != nil {
		registry.mu.Unlock()
		return nil
	}
	entry.generation = registry.nextGenerationLocked()
	generation := entry.generation
	owner := entry
	entry.status = MCPRuntimeConnecting
	entry.capabilityStatus = MCPRuntimeConnecting
	entry.activeCapabilityOperations = 0
	entry.capabilityBatchFailed = false
	entry.capabilityBatchError = ""
	entry.lastError = ""
	now := time.Now().UTC()
	entry.lastCheckedAt = now
	entry.updatedAt = now
	entry.lastConnectAttemptAt = now
	config := cloneMCPServerConfig(entry.config)
	registry.mu.Unlock()

	connectCtx, cancel := withDefaultTimeout(ctx, mcpConnectTimeout)
	defer cancel()
	session, tools, err := connectMCPRuntime(connectCtx, config)
	now = time.Now().UTC()
	registry.mu.Lock()
	entry, ok = registry.entries[key]
	if !ok || entry != owner || entry.generation != generation || !sameMCPRuntimeConfig(entry.config, config) || registry.closed {
		registry.mu.Unlock()
		if session != nil {
			_ = session.Close()
		}
		if err != nil {
			return fmt.Errorf("%s", sanitizeMCPRuntimeError(err, config))
		}
		return fmt.Errorf("mcp server %q changed while connecting in runtime scope %q", identifier, scope)
	}
	entry.lastCheckedAt = now
	entry.updatedAt = now
	if err != nil {
		sanitizedError := sanitizeMCPRuntimeError(err, config)
		entry.status = MCPRuntimeError
		entry.capabilityStatus = MCPRuntimeError
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
	entry.capabilityStatus = MCPRuntimeConnected
	entry.activeCapabilityOperations = 0
	entry.capabilityBatchFailed = false
	entry.capabilityBatchError = ""
	entry.lastError = ""
	entry.connectedAt = now
	entry.lastCheckedAt = now
	registry.mu.Unlock()
	if oldSession != nil && oldSession != session {
		_ = oldSession.Close()
	}
	return nil
}

// mcpAutoConnectCooldown 是磁盘扫描发现的 server 自动预连接失败后的冷却期：
// 冷却期内不在 enrich 热路径重复尝试，避免每次 run 都阻塞等待连接超时。
const mcpAutoConnectCooldown = 30 * time.Second

// TryAutoConnect 对未连接（或冷却期已过）的 server 发起连接，用于 enrich 热路径
// 自动拉取工具 schema。幂等：已连接且已有 tools 时直接返回；失败静默返回，
// 不影响请求主流程，下次尝试需等待冷却期。
func (registry *MCPRuntimeRegistry) TryAutoConnect(ctx context.Context, scope string, identifier string) error {
	if registry == nil {
		return nil
	}
	scope = normalizeMCPRuntimeScope(scope)
	id := strings.ToLower(strings.TrimSpace(identifier))
	registry.mu.Lock()
	entry, ok := registry.entries[mcpRuntimeEntryKey(scope, id)]
	if !ok {
		registry.mu.Unlock()
		return nil
	}
	if entry.status == MCPRuntimeConnected && entry.session != nil && len(entry.tools) > 0 {
		registry.mu.Unlock()
		return nil
	}
	if !entry.lastConnectAttemptAt.IsZero() && time.Since(entry.lastConnectAttemptAt) < mcpAutoConnectCooldown {
		registry.mu.Unlock()
		return nil
	}
	registry.mu.Unlock()
	return registry.Connect(ctx, scope, id)
}

func (registry *MCPRuntimeRegistry) Disconnect(scope string, identifier string) error {
	if registry == nil {
		return nil
	}
	scope = normalizeMCPRuntimeScope(scope)
	id := strings.ToLower(strings.TrimSpace(identifier))
	registry.mu.Lock()
	entry, ok := registry.entries[mcpRuntimeEntryKey(scope, id)]
	if !ok {
		registry.mu.Unlock()
		return fmt.Errorf("mcp server %q not found in runtime scope %q", identifier, scope)
	}
	entry.generation = registry.nextGenerationLocked()
	generation := entry.generation
	owner := entry
	key := mcpRuntimeEntryKey(scope, id)
	config := cloneMCPServerConfig(entry.config)
	session := entry.session
	entry.session = nil
	entry.tools = nil
	entry.status = MCPRuntimeDisconnected
	entry.capabilityStatus = MCPRuntimeDisconnected
	entry.activeCapabilityOperations = 0
	entry.capabilityBatchFailed = false
	entry.capabilityBatchError = ""
	entry.lastError = ""
	entry.connectedAt = time.Time{}
	now := time.Now().UTC()
	entry.lastCheckedAt = now
	entry.updatedAt = now
	registry.mu.Unlock()
	if session != nil {
		if err := session.Close(); err != nil {
			sanitizedError := sanitizeMCPRuntimeError(err, config)
			registry.mu.Lock()
			entry, ok := registry.entries[key]
			if ok && entry == owner && entry.generation == generation && sameMCPRuntimeConfig(entry.config, config) && entry.session == nil {
				now = time.Now().UTC()
				entry.status = MCPRuntimeError
				entry.capabilityStatus = MCPRuntimeError
				entry.lastError = sanitizedError
				entry.lastCheckedAt = now
				entry.updatedAt = now
			}
			registry.mu.Unlock()
			return fmt.Errorf("%s", sanitizedError)
		}
		now = time.Now().UTC()
		registry.mu.Lock()
		entry, ok := registry.entries[key]
		if ok && entry == owner && entry.generation == generation && sameMCPRuntimeConfig(entry.config, config) && entry.session == nil && entry.status == MCPRuntimeDisconnected {
			entry.lastCheckedAt = now
			entry.updatedAt = now
		}
		registry.mu.Unlock()
	}
	return nil
}

func (registry *MCPRuntimeRegistry) Snapshot(scope string) []MCPRuntimeSnapshot {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	scope = normalizeMCPRuntimeScope(scope)
	items := make([]MCPRuntimeSnapshot, 0, len(registry.entries))
	for _, entry := range registry.entries {
		if normalizeMCPRuntimeScope(entry.config.RuntimeScope) != scope {
			continue
		}
		items = append(items, snapshotMCPRuntimeEntry(entry))
	}
	registry.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].Identifier < items[j].Identifier })
	return items
}

func (registry *MCPRuntimeRegistry) Descriptor(scope string, identifier string) (*agentv1.McpDescriptor, bool) {
	if registry == nil {
		return nil, false
	}
	scope = normalizeMCPRuntimeScope(scope)
	id := strings.ToLower(strings.TrimSpace(identifier))
	registry.mu.RLock()
	entry, ok := registry.entries[mcpRuntimeEntryKey(scope, id)]
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

func (registry *MCPRuntimeRegistry) Descriptors(scope string) []*agentv1.McpDescriptor {
	items := registry.Snapshot(scope)
	result := make([]*agentv1.McpDescriptor, 0, len(items))
	for _, item := range items {
		if descriptor, ok := registry.Descriptor(scope, item.Identifier); ok {
			result = append(result, descriptor)
		}
	}
	return result
}

// ResolveScope prefers the requested workspace, then falls back to the shared user scope.
func (registry *MCPRuntimeRegistry) ResolveScope(preferredScope string, identifier string) string {
	preferredScope = normalizeMCPRuntimeScope(preferredScope)
	identifier = strings.TrimSpace(identifier)
	if registry == nil || identifier == "" {
		return preferredScope
	}
	registry.mu.RLock()
	_, preferredFound := registry.entries[mcpRuntimeEntryKey(preferredScope, identifier)]
	userScope := MCPRuntimeScope("")
	_, userFound := registry.entries[mcpRuntimeEntryKey(userScope, identifier)]
	registry.mu.RUnlock()
	if preferredFound || preferredScope == userScope || !userFound {
		return preferredScope
	}
	return userScope
}

func (registry *MCPRuntimeRegistry) CallTool(ctx context.Context, scope, identifier, name string, arguments map[string]any) (*mcp.CallToolResult, error) {
	operation, err := registry.beginCapabilityOperation(ctx, scope, identifier)
	if err != nil {
		return nil, err
	}
	operationCtx, cancel := withDefaultTimeout(ctx, mcpOperationTimeout)
	defer cancel()
	result, callErr := operation.session.CallTool(operationCtx, &mcp.CallToolParams{Name: strings.TrimSpace(name), Arguments: arguments})
	healthErr := callErr
	if healthErr == nil && result != nil && result.IsError {
		healthErr = fmt.Errorf("mcp tool call reported an error")
	}
	registry.finishCapabilityOperation(operation, healthErr)
	return result, callErr
}

func (registry *MCPRuntimeRegistry) ListResources(ctx context.Context, scope, identifier string) ([]*mcp.Resource, error) {
	operation, err := registry.beginCapabilityOperation(ctx, scope, identifier)
	if err != nil {
		return nil, err
	}
	operationCtx, cancel := withDefaultTimeout(ctx, mcpOperationTimeout)
	defer cancel()
	var resources []*mcp.Resource
	cursor := ""
	seen := make(map[string]struct{})
	for {
		result, listErr := operation.session.ListResources(operationCtx, &mcp.ListResourcesParams{Cursor: cursor})
		if listErr != nil {
			registry.finishCapabilityOperation(operation, listErr)
			return nil, listErr
		}
		resources = append(resources, result.Resources...)
		next := strings.TrimSpace(result.NextCursor)
		if next == "" {
			registry.finishCapabilityOperation(operation, nil)
			return resources, nil
		}
		if _, exists := seen[next]; exists {
			listErr = fmt.Errorf("mcp resources/list returned a repeated cursor")
			registry.finishCapabilityOperation(operation, listErr)
			return nil, listErr
		}
		seen[next] = struct{}{}
		cursor = next
	}
}

func (registry *MCPRuntimeRegistry) ReadResource(ctx context.Context, scope, identifier, uri string) (*mcp.ReadResourceResult, error) {
	operation, err := registry.beginCapabilityOperation(ctx, scope, identifier)
	if err != nil {
		return nil, err
	}
	operationCtx, cancel := withDefaultTimeout(ctx, mcpOperationTimeout)
	defer cancel()
	result, readErr := operation.session.ReadResource(operationCtx, &mcp.ReadResourceParams{URI: strings.TrimSpace(uri)})
	registry.finishCapabilityOperation(operation, readErr)
	return result, readErr
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
	now := time.Now().UTC()
	var sessions []*mcp.ClientSession
	for _, entry := range registry.entries {
		entry.generation = registry.nextGenerationLocked()
		if entry.session != nil {
			sessions = append(sessions, entry.session)
		}
		entry.session = nil
		entry.tools = nil
		entry.status = MCPRuntimeDisconnected
		entry.capabilityStatus = MCPRuntimeDisconnected
		entry.activeCapabilityOperations = 0
		entry.capabilityBatchFailed = false
		entry.capabilityBatchError = ""
		entry.lastError = ""
		entry.connectedAt = time.Time{}
		entry.lastCheckedAt = now
		entry.updatedAt = now
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

// beginCapabilityOperation 获取一次工具调用的会话句柄。
// 当 server 未连接（断线、进程重启等）且自动重连冷却期已过时，先尝试一次惰性重连，
// 让工具调用在短暂断线后自愈，而非直接失败要求用户手动重连。
// 重连受 mcpAutoConnectCooldown 冷却约束，避免每次调用都阻塞等待连接超时。
func (registry *MCPRuntimeRegistry) beginCapabilityOperation(ctx context.Context, scope string, identifier string) (mcpCapabilityOperation, error) {
	if registry == nil {
		return mcpCapabilityOperation{}, fmt.Errorf("mcp runtime registry is nil")
	}
	scope = normalizeMCPRuntimeScope(scope)
	id := strings.ToLower(strings.TrimSpace(identifier))
	key := mcpRuntimeEntryKey(scope, id)
	registry.mu.Lock()
	entry, ok := registry.entries[key]
	if !ok || entry.status != MCPRuntimeConnected || entry.session == nil {
		// 惰性重连：仅在冷却期外尝试一次，失败则回退到原有错误路径。
		// 复用 TryAutoConnect 的冷却语义，避免在 server 持续不可用时每次工具调用都卡住一个连接超时。
		canAutoReconnect := ok &&
			ctx != nil &&
			(entry.lastConnectAttemptAt.IsZero() || time.Since(entry.lastConnectAttemptAt) >= mcpAutoConnectCooldown)
		registry.mu.Unlock()
		if canAutoReconnect {
			// Connect 会自管 entry 状态与冷却时间戳；忽略其错误，重新进入 begin 取最新状态。
			_ = registry.Connect(ctx, scope, id)
			return registry.beginCapabilityOperation(ctx, scope, identifier)
		}
		err := fmt.Errorf("mcp server %q is not connected in runtime scope %q", identifier, scope)
		registry.mu.Lock()
		if entry, ok := registry.entries[key]; ok {
			now := time.Now().UTC()
			entry.capabilityStatus = entry.status
			entry.lastError = sanitizeMCPRuntimeError(err, entry.config)
			entry.lastCheckedAt = now
			entry.updatedAt = now
		}
		registry.mu.Unlock()
		return mcpCapabilityOperation{}, err
	}
	operation := mcpCapabilityOperation{
		key:        key,
		owner:      entry,
		generation: entry.generation,
		session:    entry.session,
		config:     cloneMCPServerConfig(entry.config),
	}
	if entry.activeCapabilityOperations == 0 {
		entry.capabilityBatchFailed = false
		entry.capabilityBatchError = ""
		entry.lastError = ""
	}
	entry.activeCapabilityOperations++
	now := time.Now().UTC()
	entry.capabilityStatus = MCPRuntimeConnecting
	entry.updatedAt = now
	registry.mu.Unlock()
	return operation, nil
}

func (registry *MCPRuntimeRegistry) finishCapabilityOperation(operation mcpCapabilityOperation, err error) {
	if registry == nil || operation.session == nil {
		return
	}
	now := time.Now().UTC()
	registry.mu.Lock()
	entry, ok := registry.entries[operation.key]
	if !ok || entry != operation.owner || entry.generation != operation.generation || entry.session != operation.session || !sameMCPRuntimeConfig(entry.config, operation.config) || entry.status != MCPRuntimeConnected {
		registry.mu.Unlock()
		return
	}
	if entry.activeCapabilityOperations <= 0 {
		registry.mu.Unlock()
		return
	}
	entry.activeCapabilityOperations--
	entry.lastCheckedAt = now
	entry.updatedAt = now
	if err != nil {
		entry.capabilityBatchFailed = true
		entry.capabilityBatchError = appendMCPRuntimeCapabilityError(entry.capabilityBatchError, sanitizeMCPRuntimeError(err, operation.config))
	}
	if entry.activeCapabilityOperations > 0 {
		entry.capabilityStatus = MCPRuntimeConnecting
		entry.lastError = entry.capabilityBatchError
	} else if entry.capabilityBatchFailed {
		entry.capabilityStatus = MCPRuntimeDegraded
		entry.lastError = entry.capabilityBatchError
	} else {
		entry.capabilityStatus = MCPRuntimeConnected
		entry.lastError = ""
	}
	registry.mu.Unlock()
}

func connectMCPRuntime(ctx context.Context, config MCPServerConfig) (*mcp.ClientSession, []*agentv1.McpToolDescriptor, error) {
	transport, stderr, err := mcpRuntimeTransport(config)
	if err != nil {
		return nil, nil, fmt.Errorf("%s%s", err, mcpStderrSuffix(stderr))
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "cursor-byok", Version: "dev"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("connect mcp server %q: %w%s", config.Name, err, mcpStderrSuffix(stderr))
	}
	tools, err := listBoundedMCPTools(ctx, session, config.Name)
	if err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("%w%s", err, mcpStderrSuffix(stderr))
	}
	return session, tools, nil
}

func mcpRuntimeTransport(config MCPServerConfig) (mcp.Transport, *mcpStderrBuffer, error) {
	transport := strings.ToLower(strings.TrimSpace(config.Transport))
	if transport == "" {
		transport = "stdio"
	}
	switch transport {
	case "stdio":
		if strings.TrimSpace(config.Command) == "" {
			return nil, nil, fmt.Errorf("mcp stdio server %q has no command", config.Name)
		}
		resolved, err := resolveMCPStdioCommand(config)
		if err != nil {
			return nil, nil, err
		}
		cmd := exec.Command(resolved, config.Args...)
		cmd.SysProcAttr = hiddenWindowAttr()
		if cwd := strings.TrimSpace(config.Cwd); cwd != "" {
			cmd.Dir = cwd
		}
		cmd.Env = append([]string{}, os.Environ()...)
		for key, value := range config.Env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		stderr := &mcpStderrBuffer{limit: mcpStderrTailLimit}
		cmd.Stderr = stderr
		return &mcp.CommandTransport{Command: cmd, TerminateDuration: mcpProcessCloseDuration}, stderr, nil
	case "http", "streamable-http", "streamable_http":
		if strings.TrimSpace(config.URL) == "" {
			return nil, nil, fmt.Errorf("mcp http server %q has no url", config.Name)
		}
		return &mcp.StreamableClientTransport{Endpoint: config.URL, HTTPClient: mcpHTTPClient(config.Headers)}, nil, nil
	case "sse":
		if strings.TrimSpace(config.URL) == "" {
			return nil, nil, fmt.Errorf("mcp sse server %q has no url", config.Name)
		}
		return &mcp.SSEClientTransport{Endpoint: config.URL, HTTPClient: mcpHTTPClient(config.Headers)}, nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported mcp transport %q", config.Transport)
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
		Identifier:        entry.config.Identifier,
		Name:              entry.config.Name,
		Source:            string(entry.config.Source),
		Scope:             string(entry.config.Scope),
		Transport:         entry.config.Transport,
		Command:           mcpCommandBasename(entry.config.Command),
		URL:               mcpURLOrigin(entry.config.URL),
		Status:            entry.status,
		ConfigFingerprint: mcpConfigShapeFingerprint(entry.config),
		CapabilityStatus:  entry.capabilityStatus,
		ToolCount:         len(entry.tools),
		LastError:         entry.lastError,
		ConnectedAt:       entry.connectedAt,
		LastCheckedAt:     entry.lastCheckedAt,
		UpdatedAt:         entry.updatedAt,
		RuntimeScope:      normalizeMCPRuntimeScope(entry.config.RuntimeScope),
	}
}

func normalizeMCPRuntimeScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return MCPRuntimeScope("")
	}
	return scope
}

func mcpRuntimeEntryKey(scope string, identifier string) string {
	return normalizeMCPRuntimeScope(scope) + "\x00" + strings.ToLower(strings.TrimSpace(identifier))
}

func sameMCPRuntimeConfig(left MCPServerConfig, right MCPServerConfig) bool {
	left.RuntimeScope = normalizeMCPRuntimeScope(left.RuntimeScope)
	right.RuntimeScope = normalizeMCPRuntimeScope(right.RuntimeScope)
	// Full config equality, including secret-bearing fields, is kept internal and
	// is the exact identity boundary for runtime replacement.
	return reflect.DeepEqual(left, right)
}

func appendMCPRuntimeCapabilityError(current string, next string) string {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if next == "" || current == next {
		return current
	}
	if current == "" {
		return truncateMCPRuntimeError(next)
	}
	return truncateMCPRuntimeError(current + "; " + next)
}

func sanitizeMCPRuntimeError(err error, config MCPServerConfig) string {
	if err == nil {
		return ""
	}
	message := mcpRuntimeURLPattern.ReplaceAllString(err.Error(), "[redacted-url]")
	redactions := make([]string, 0, len(config.Env)+len(config.Headers)+len(config.Args)+20)
	appendRedaction := func(values ...string) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" || value == "." || value == "/" || value == "\\" {
				continue
			}
			redactions = append(redactions, value)
		}
	}
	appendSecretRedaction := func(values ...string) {
		for _, value := range values {
			quoted := strconv.Quote(value)
			quotedASCII := strconv.QuoteToASCII(value)
			if len(quoted) >= 2 {
				quoted = quoted[1 : len(quoted)-1]
			}
			if len(quotedASCII) >= 2 {
				quotedASCII = quotedASCII[1 : len(quotedASCII)-1]
			}
			appendRedaction(value, url.QueryEscape(value), url.PathEscape(value), quoted, quotedASCII)
		}
	}
	rawURL := strings.TrimSpace(config.URL)
	appendSecretRedaction(rawURL)
	if parsed, parseErr := url.Parse(rawURL); parseErr == nil {
		appendSecretRedaction(parsed.Path, parsed.RawPath, parsed.EscapedPath(), parsed.RawQuery, parsed.Fragment)
		if parsed.User != nil {
			appendSecretRedaction(parsed.User.String(), parsed.User.Username())
			if password, ok := parsed.User.Password(); ok {
				appendSecretRedaction(password)
			}
		}
		for _, values := range parsed.Query() {
			appendSecretRedaction(values...)
		}
	}
	// 命令名、工作目录、配置文件路径用于可排查的错误提示，不视为 secret；
	// 只有 env/headers/args 里的值可能携带凭据，需要脱敏。
	for _, value := range config.Env {
		appendSecretRedaction(value)
	}
	for _, value := range config.Headers {
		appendSecretRedaction(value)
	}
	for _, value := range config.Args {
		appendSecretRedaction(value)
	}
	redactions = uniqueMCPRuntimeRedactions(redactions)
	sort.Slice(redactions, func(i, j int) bool { return len(redactions[i]) > len(redactions[j]) })
	for _, value := range redactions {
		pattern, compileErr := regexp.Compile(`(?i)` + regexp.QuoteMeta(value))
		if compileErr == nil {
			message = pattern.ReplaceAllString(message, "[redacted]")
		}
	}
	return truncateMCPRuntimeError(message)
}

func uniqueMCPRuntimeRedactions(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func truncateMCPRuntimeError(message string) string {
	runes := []rune(message)
	if len(runes) <= mcpMaxRuntimeErrorRunes {
		return message
	}
	return string(runes[:mcpMaxRuntimeErrorRunes]) + "..."
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
