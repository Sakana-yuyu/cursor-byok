// mcp_scanner.go 扫描各主流编码工具的 MCP 配置文件，归一化为 McpDescriptor，
// 作为 RequestContext.McpFileSystemOptions 的补充来源注入，让模型通过原生
// <mcp_file_system> user message 得知可用的 MCP server。
//
// 覆盖格式（按主流程度）：
//   - JSON（Cursor / Claude / Cline / 共享 .agents / ZCode 嵌套 mcp.servers）—— encoding.json
//   - TOML（Codex ~/.codex/config.toml 的 [mcp_servers.*]）—— 轻量定向解析，不引入第三方依赖
//
// 注意：本文件只做只读配置发现。连接、schema 获取和调用由 MCPRuntimeRegistry
// 在用户显式连接后负责，扫描本身绝不启动命令或发起网络请求。
package forwarder

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"cursor/gen/agentv1"
)

// MCPSource 标识一个 MCP server 配置来自哪个工具。
type MCPSource string

const (
	MCPSourceCursor   MCPSource = "cursor"   // ~/.cursor/mcp.json 或 <ws>/.cursor/mcp.json
	MCPSourceClaude   MCPSource = "claude"   // ~/.claude.json、~/.claude/settings.json、<ws>/.mcp.json
	MCPSourceShared   MCPSource = "shared"   // <ws>/.agents/mcp.json 或 ~/.agents/mcp.json
	MCPSourceZCode    MCPSource = "zcode"    // ~/.zcode/cli/config.json、<ws>/.zcode/config.json（嵌套 mcp.servers）
	MCPSourceCodex    MCPSource = "codex"    // ~/.codex/config.toml 的 [mcp_servers.*]
	MCPSourceCline    MCPSource = "cline"    // cline_mcp_settings.json
	MCPSourceWindsurf MCPSource = "windsurf" // <ws>/.windsurf/mcp_config.json 或 ~/.codeium/windsurf/mcp_config.json
	MCPSourceVSCode   MCPSource = "vscode"   // <ws>/.vscode/mcp.json 或 ~/.vscode/mcp.json（VS Code 原生 MCP）
)

// mcpScanCache 缓存一次 MCP 扫描结果（同 skill 扫描，按 mtime 失效）。
var (
	mcpScanCacheMu sync.RWMutex
	mcpScanRunMu   sync.Mutex
	mcpScanCache   []MCPServerConfig
	mcpScanCacheFp string
)

type MCPConfigScope string

const (
	MCPConfigScopeUser      MCPConfigScope = "user"
	MCPConfigScopeWorkspace MCPConfigScope = "workspace"
)

// MCPServerConfig is the complete in-memory configuration for one discovered server.
// Secret-bearing fields are deliberately excluded from JSON serialization.
type MCPServerConfig struct {
	Identifier        string            `json:"identifier"`
	Name              string            `json:"name"`
	Source            MCPSource         `json:"source"`
	ConfigPath        string            `json:"configPath"`
	Scope             MCPConfigScope    `json:"scope"`
	Transport         string            `json:"transport"`
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"-"`
	Env               map[string]string `json:"-"`
	Cwd               string            `json:"cwd,omitempty"`
	URL               string            `json:"url,omitempty"`
	Headers           map[string]string `json:"-"`
	ConfiguredEnabled bool              `json:"configuredEnabled"`
	Enabled           bool              `json:"enabled"`
	RuntimeScope      string            `json:"-"`
}

// MCPRuntimeScope returns the stable runtime identity for user or workspace MCP sessions.
func MCPRuntimeScope(workspaceRoot string) string {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return "user"
	}
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	root = filepath.Clean(root)
	root = filepath.ToSlash(root)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
	}
	return "workspace:" + root
}

// mcpConfigFile 是一个待扫描的配置文件及其解析方式。
type mcpConfigFile struct {
	Path   string
	Source MCPSource
	Scope  MCPConfigScope
	// Parser 把文件内容解析成 map[serverName]normalizedMCPServer。
	Parser func([]byte) (map[string]normalizedMCPServer, error)
}

// normalizedMCPServer 是归一化后的单个 MCP server 配置（跨工具统一）。
type normalizedMCPServer struct {
	ServerName       string
	ServerIdentifier string
	Transport        string // "stdio" | "http" | "sse"
	Command          string
	Args             []string
	Env              map[string]string
	URL              string
	Headers          map[string]string
	Cwd              string
	Enabled          bool
	// RawJSON 保留原始 server 块，供调试/未来扩展（如手动 tool schema）。
	RawJSON map[string]any
}

func scanMCPServers(workspaceRoot string, enabledSources map[string]bool, disabledServers map[string]bool) []*agentv1.McpDescriptor {
	settings := SkillMCPScanSettings{
		Enabled:            true,
		MCPSources:         enabledSources,
		DisabledMCPServers: disabledServers,
	}
	configs := ScanMCPServerConfigs(workspaceRoot, settings)
	return mcpDescriptorsFromConfigs(configs, nil)
}

// ScanMCPServerConfigs performs read-only discovery and returns complete runtime configs.
func ScanMCPServerConfigs(workspaceRoot string, settings SkillMCPScanSettings) []MCPServerConfig {
	files := orderedMCPConfigFiles(workspaceRoot)
	workspaceScope := MCPRuntimeScope(workspaceRoot)
	fingerprint := mcpScanFingerprint(files, settings.Enabled, settings.MCPSources, settings.DisabledMCPServers)
	if cached, ok := loadCachedMCPServers(fingerprint); ok {
		return cached
	}
	mcpScanRunMu.Lock()
	defer mcpScanRunMu.Unlock()
	if cached, ok := loadCachedMCPServers(fingerprint); ok {
		return cached
	}

	seen := make(map[string]struct{}, 16)
	merged := make([]MCPServerConfig, 0, 16)
	for _, f := range files {
		if !sourceEnabled(settings.MCPSources, string(f.Source)) {
			continue
		}
		data, err := os.ReadFile(f.Path)
		if err != nil {
			continue
		}
		servers, err := f.Parser(data)
		if err != nil || len(servers) == 0 {
			continue
		}
		// 按 server 名排序，保证稳定。
		names := make([]string, 0, len(servers))
		for name := range servers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			srv := servers[name]
			identifier := firstNonEmpty(srv.ServerIdentifier, srv.ServerName, name)
			if identifier == "" {
				continue
			}
			runtimeScope := MCPRuntimeScope("")
			if f.Scope == MCPConfigScopeWorkspace {
				runtimeScope = workspaceScope
			}
			identifierKey := strings.ToLower(identifier)
			key := mcpRuntimeEntryKey(runtimeScope, identifier)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, MCPServerConfig{
				Identifier:        identifier,
				Name:              firstNonEmpty(srv.ServerName, identifier),
				Source:            f.Source,
				ConfigPath:        f.Path,
				Scope:             f.Scope,
				Transport:         firstNonEmpty(srv.Transport, "stdio"),
				Command:           srv.Command,
				Args:              append([]string(nil), srv.Args...),
				Env:               cloneStringMap(srv.Env),
				Cwd:               srv.Cwd,
				URL:               srv.URL,
				Headers:           cloneStringMap(srv.Headers),
				ConfiguredEnabled: srv.Enabled,
				Enabled:           settings.Enabled && srv.Enabled && !boolMapContains(settings.DisabledMCPServers, identifierKey),
				RuntimeScope:      runtimeScope,
			})
		}
	}

	mcpScanCacheMu.Lock()
	mcpScanCache = cloneMCPServerConfigs(merged)
	mcpScanCacheFp = fingerprint
	mcpScanCacheMu.Unlock()
	return cloneMCPServerConfigs(merged)
}

func mcpDescriptorsFromConfigs(configs []MCPServerConfig, tools map[string][]*agentv1.McpToolDescriptor) []*agentv1.McpDescriptor {
	configs = effectiveMCPServerConfigs(configs)
	output := make([]*agentv1.McpDescriptor, 0, len(configs))
	for _, config := range configs {
		if !config.Enabled {
			continue
		}
		descriptor := buildMCPDescriptor(config)
		if serverTools := tools[strings.ToLower(config.Identifier)]; len(serverTools) > 0 {
			descriptor.Tools = cloneMCPToolDescriptors(serverTools)
		}
		output = append(output, descriptor)
	}
	return output
}

// buildMCPDescriptor returns a schema-free descriptor until an explicit connection discovers tools.
func buildMCPDescriptor(config MCPServerConfig) *agentv1.McpDescriptor {
	desc := &agentv1.McpDescriptor{
		ServerName:       firstNonEmpty(config.Name, config.Identifier),
		ServerIdentifier: config.Identifier,
	}
	if folder := strings.TrimSpace(config.Cwd); folder != "" {
		f := folder
		desc.FolderPath = &f
	}
	if hint := buildMCPUseHint(config); hint != "" {
		h := hint
		desc.ServerUseInstructions = &h
	}
	return desc
}

// buildMCPUseHint 给模型一句简短的能力/调用提示，弥补缺少 tool schema 的信息缺口。
func buildMCPUseHint(config MCPServerConfig) string {
	transport := strings.TrimSpace(config.Transport)
	if transport == "" {
		transport = "stdio"
	}
	var b strings.Builder
	b.WriteString("MCP server \"")
	b.WriteString(firstNonEmpty(config.Name, config.Identifier))
	b.WriteString("\" (")
	b.WriteString(transport)
	b.WriteString(")")
	if config.Command != "" {
		b.WriteString(", command: ")
		b.WriteString(config.Command)
	}
	if config.URL != "" {
		b.WriteString(", url: ")
		b.WriteString(redactMCPURL(config.URL))
	}
	b.WriteString(". 调用前请用 CallMcpTool 并在 server 字段填该 server 名；具体可用工具需在运行时发现。")
	return b.String()
}

// orderedMCPConfigFiles 按优先级返回所有待扫描配置文件。
func orderedMCPConfigFiles(workspaceRoot string) []mcpConfigFile {
	home, _ := os.UserHomeDir()
	home = strings.TrimSpace(home)
	ws := strings.TrimSpace(workspaceRoot)

	var files []mcpConfigFile
	addJSON := func(path string, source MCPSource, scope MCPConfigScope, nested bool) {
		if strings.TrimSpace(path) == "" {
			return
		}
		parser := parseMCPJSON
		if nested {
			parser = parseZCodeMCPJSON
		}
		files = append(files, mcpConfigFile{Path: path, Source: source, Scope: scope, Parser: parser})
	}
	addTOML := func(path string, source MCPSource, scope MCPConfigScope) {
		if strings.TrimSpace(path) == "" {
			return
		}
		files = append(files, mcpConfigFile{Path: path, Source: source, Scope: scope, Parser: parseCodexMCPTOML})
	}

	// Cursor
	if home != "" {
		addJSON(filepath.Join(home, ".cursor", "mcp.json"), MCPSourceCursor, MCPConfigScopeUser, false)
	}
	if ws != "" {
		addJSON(filepath.Join(ws, ".cursor", "mcp.json"), MCPSourceCursor, MCPConfigScopeWorkspace, false)
	}
	// Claude Code
	if ws != "" {
		addJSON(filepath.Join(ws, ".mcp.json"), MCPSourceClaude, MCPConfigScopeWorkspace, false)
	}
	if home != "" {
		addJSON(filepath.Join(home, ".claude.json"), MCPSourceClaude, MCPConfigScopeUser, false)
		addJSON(filepath.Join(home, ".claude", "settings.json"), MCPSourceClaude, MCPConfigScopeUser, false)
	}
	// 共享 .agents
	if ws != "" {
		addJSON(filepath.Join(ws, ".agents", "mcp.json"), MCPSourceShared, MCPConfigScopeWorkspace, false)
	}
	if home != "" {
		addJSON(filepath.Join(home, ".agents", "mcp.json"), MCPSourceShared, MCPConfigScopeUser, false)
	}
	// ZCode（嵌套 mcp.servers）
	if home != "" {
		addJSON(filepath.Join(home, ".zcode", "cli", "config.json"), MCPSourceZCode, MCPConfigScopeUser, true)
	}
	if ws != "" {
		addJSON(filepath.Join(ws, ".zcode", "config.json"), MCPSourceZCode, MCPConfigScopeWorkspace, true)
		addJSON(filepath.Join(ws, "zcode.json"), MCPSourceZCode, MCPConfigScopeWorkspace, true)
	}
	// Codex（TOML）
	if home != "" {
		addTOML(filepath.Join(home, ".codex", "config.toml"), MCPSourceCodex, MCPConfigScopeUser)
	}
	if ws != "" {
		addTOML(filepath.Join(ws, ".codex", "config.toml"), MCPSourceCodex, MCPConfigScopeWorkspace)
	}
	// Cline
	if cline := clineMCPSettingsPath(); cline != "" {
		addJSON(cline, MCPSourceCline, MCPConfigScopeUser, false)
	}
	// Windsurf（mcp_config.json，mcpServers 键）
	if ws != "" {
		addJSON(filepath.Join(ws, ".windsurf", "mcp_config.json"), MCPSourceWindsurf, MCPConfigScopeWorkspace, false)
	}
	if home != "" {
		addJSON(filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), MCPSourceWindsurf, MCPConfigScopeUser, false)
	}
	// VS Code 原生 MCP（servers 键，与 mcpServers 不同）
	if ws != "" {
		files = append(files, mcpConfigFile{Path: filepath.Join(ws, ".vscode", "mcp.json"), Source: MCPSourceVSCode, Scope: MCPConfigScopeWorkspace, Parser: parseVSCodeMCPJSON})
	}
	if home != "" {
		files = append(files, mcpConfigFile{Path: filepath.Join(home, ".vscode", "mcp.json"), Source: MCPSourceVSCode, Scope: MCPConfigScopeUser, Parser: parseVSCodeMCPJSON})
	}
	return files
}

// parseMCPJSON 解析顶层 { "mcpServers": { ... } } 格式（Cursor/Claude/Cline/Windsurf/共享）。
func parseMCPJSON(data []byte) (map[string]normalizedMCPServer, error) {
	return parseMCPJSONUnderKey(data, "mcpServers")
}

// parseVSCodeMCPJSON 解析 VS Code 原生 MCP 的 { "servers": { ... } } 格式（.vscode/mcp.json）。
func parseVSCodeMCPJSON(data []byte) (map[string]normalizedMCPServer, error) {
	return parseMCPJSONUnderKey(data, "servers")
}

// parseMCPJSONUnderKey 解析顶层对象下指定 key 的 servers 映射。
func parseMCPJSONUnderKey(data []byte, key string) (map[string]normalizedMCPServer, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	raw, ok := root[key]
	if !ok {
		return nil, nil
	}
	var servers map[string]map[string]any
	if err := json.Unmarshal(raw, &servers); err != nil {
		return nil, err
	}
	return normalizeMCPServerMap(servers), nil
}

// parseZCodeMCPJSON 解析嵌套 { "mcp": { "servers": { ... } } } 格式（ZCode）。
func parseZCodeMCPJSON(data []byte) (map[string]normalizedMCPServer, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	mcpRaw, ok := root["mcp"]
	if !ok {
		return nil, nil
	}
	var mcp map[string]json.RawMessage
	if err := json.Unmarshal(mcpRaw, &mcp); err != nil {
		return nil, err
	}
	srvRaw, ok := mcp["servers"]
	if !ok {
		return nil, nil
	}
	var servers map[string]map[string]any
	if err := json.Unmarshal(srvRaw, &servers); err != nil {
		return nil, err
	}
	return normalizeMCPServerMap(servers), nil
}

// normalizeMCPServerMap 把原始 map[string]map[string]any 归一化为 normalizedMCPServer。
// 处理跨工具字段差异：environment→env、http_headers→headers、enable→enabled、remote→http。
func normalizeMCPServerMap(servers map[string]map[string]any) map[string]normalizedMCPServer {
	out := make(map[string]normalizedMCPServer, len(servers))
	for name, raw := range servers {
		srv := normalizeOneMCPServer(name, raw)
		out[name] = srv
	}
	return out
}

// normalizeOneMCPServer 归一化单个 server 条目。
func normalizeOneMCPServer(name string, raw map[string]any) normalizedMCPServer {
	srv := normalizedMCPServer{
		ServerName:       name,
		ServerIdentifier: name,
		Enabled:          true,
		RawJSON:          raw,
	}
	// transport / type
	if t, ok := raw["type"].(string); ok {
		srv.Transport = normalizeTransport(t)
	}
	// command / args / env
	if c, ok := raw["command"].(string); ok {
		srv.Command = strings.TrimSpace(c)
	}
	srv.Args = toStringSlice(raw["args"])
	// env 兼容 environment（Codex 遗留）
	srv.Env = toStringMap(firstNonEmptyMap(raw["env"], raw["environment"]))
	// url / headers 兼容 http_headers（遗留）
	if u, ok := raw["url"].(string); ok {
		srv.URL = strings.TrimSpace(u)
	}
	srv.Headers = toStringMap(firstNonEmptyMap(raw["headers"], raw["http_headers"]))
	if cwd, ok := raw["cwd"].(string); ok {
		srv.Cwd = strings.TrimSpace(cwd)
	}
	// transport 推断：有 command → stdio；有 url → http/sse
	if srv.Transport == "" {
		switch {
		case srv.Command != "":
			srv.Transport = "stdio"
		case srv.URL != "":
			// 默认 http（streamable-http）；sse 较少见，保留显式 type 才走 sse
			srv.Transport = "http"
		}
	}
	// enabled 开关（默认 true）
	if enabled, ok := raw["enabled"].(bool); ok && !enabled {
		srv.Enabled = false
	}
	if e, ok := raw["enable"].(bool); ok && !e {
		srv.Enabled = false
	}
	if disabled, ok := raw["disabled"].(bool); ok && disabled {
		srv.Enabled = false
	}
	return srv
}

// normalizeTransport 归一化 transport 类型名。
func normalizeTransport(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "stdio":
		return "stdio"
	case "sse":
		return "sse"
	case "http", "streamable-http", "streamable_http", "streamablehttp":
		return "http"
	case "remote": // 遗留
		return "http"
	default:
		return strings.ToLower(strings.TrimSpace(t))
	}
}

// parseCodexMCPTOML 定向解析 Codex config.toml 的 [mcp_servers.<name>] 块。
// 不引入第三方 TOML 库：只按行扫描 [mcp_servers.xxx] 段落与 key = value。
func parseCodexMCPTOML(data []byte) (map[string]normalizedMCPServer, error) {
	lines := strings.Split(string(data), "\n")
	servers := make(map[string]normalizedMCPServer)
	var current string
	currentTable := ""
	currentFields := map[string]any{}

	flush := func() {
		if current == "" {
			return
		}
		if _, exists := servers[current]; !exists {
			servers[current] = normalizeOneMCPServer(current, currentFields)
		}
		current = ""
		currentTable = ""
		currentFields = map[string]any{}
	}

	for _, raw := range lines {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			header := strings.TrimSpace(line[1 : len(line)-1])
			if strings.HasPrefix(header, "mcp_servers.") {
				remainder := strings.TrimSpace(strings.TrimPrefix(header, "mcp_servers."))
				serverName := remainder
				tableName := ""
				for _, table := range []string{"http_headers", "headers", "env"} {
					suffix := "." + table
					if strings.HasSuffix(remainder, suffix) {
						serverName = strings.TrimSpace(strings.TrimSuffix(remainder, suffix))
						tableName = table
						break
					}
				}
				if serverName == "" {
					flush()
					continue
				}
				if current != serverName {
					flush()
					current = serverName
					currentFields = map[string]any{}
				}
				currentTable = strings.ToLower(tableName)
				continue
			}
			// 进入其他表，结束当前 server 块
			flush()
			current = ""
			currentTable = ""
			continue
		}
		if current == "" {
			continue
		}
		key, value, ok := splitTOMLKeyValue(line)
		if !ok {
			continue
		}
		if currentTable == "env" || currentTable == "headers" || currentTable == "http_headers" {
			fieldName := "env"
			if currentTable != "env" {
				fieldName = "headers"
			}
			fields, _ := currentFields[fieldName].(map[string]any)
			if fields == nil {
				fields = make(map[string]any)
				currentFields[fieldName] = fields
			}
			fields[strings.Trim(strings.TrimSpace(key), "\"'")] = unquoteTOML(value)
			continue
		}
		switch strings.ToLower(key) {
		case "command":
			currentFields["command"] = unquoteTOML(value)
		case "url":
			currentFields["url"] = unquoteTOML(value)
		case "cwd":
			currentFields["cwd"] = unquoteTOML(value)
		case "type":
			currentFields["type"] = unquoteTOML(value)
		case "args":
			currentFields["args"] = parseTOMLStringArray(value)
		case "env":
			if envMap, ok := parseTOMLInlineTable(value); ok {
				currentFields["env"] = envMap
			}
		case "headers", "http_headers":
			if headers, ok := parseTOMLInlineTable(value); ok {
				currentFields["headers"] = headers
			}
		case "enabled", "enable":
			if enabled, ok := parseTOMLBool(value); ok {
				currentFields[strings.ToLower(key)] = enabled
			}
		}
	}
	flush()

	// 过滤禁用的（normalizeOneMCPServer 把禁用项 transport 置空）
	out := make(map[string]normalizedMCPServer, len(servers))
	for name, srv := range servers {
		if srv.Transport == "" && srv.Command == "" && srv.URL == "" {
			continue
		}
		out[name] = srv
	}
	return out, nil
}

// splitTOMLKeyValue 分割 "key = value" 行。
func splitTOMLKeyValue(line string) (key, value string, ok bool) {
	idx := strings.Index(line, "=")
	if idx <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
}

// unquoteTOML 去掉 TOML 字符串引号。
func unquoteTOML(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"') {
		return s[1 : len(s)-1]
	}
	if len(s) >= 2 && (s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

func parseTOMLBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

// parseTOMLStringArray 解析 ["a", "b"] 形式。
func parseTOMLStringArray(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimSuffix(s, "]"), "[")
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, unquoteTOML(p))
	}
	return out
}

// parseTOMLInlineTable 解析 { KEY = "v", KEY2 = "v2" } 形式（Codex env）。
func parseTOMLInlineTable(s string) (map[string]any, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimSuffix(s, "}"), "{")
	if strings.TrimSpace(s) == "" {
		return nil, false
	}
	out := map[string]any{}
	for _, pair := range strings.Split(s, ",") {
		idx := strings.Index(pair, "=")
		if idx <= 0 {
			continue
		}
		k := strings.TrimSpace(pair[:idx])
		v := strings.TrimSpace(pair[idx+1:])
		k = strings.Trim(k, "\"'")
		out[k] = unquoteTOML(v)
	}
	return out, true
}

// --- 工具函数 ---

func toStringSlice(v any) []string {
	if values, ok := v.([]string); ok {
		return append([]string(nil), values...)
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func toStringMap(v any) map[string]string {
	if values, ok := v.(map[string]string); ok {
		return cloneStringMap(values)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		if s, ok := val.(string); ok {
			out[k] = s
		}
	}
	return out
}

func firstNonEmptyMap(maps ...any) any {
	for _, m := range maps {
		if m != nil {
			return m
		}
	}
	return nil
}

// clineMCPSettingsPath 返回 Cline 的 MCP 设置文件路径（VS Code globalStorage）。
// 跨平台：Windows 在 %APPDATA%/Code/User/globalStorage/saoudrizwan.claude-dev/settings/。
func clineMCPSettingsPath() string {
	home, _ := os.UserHomeDir()
	home = strings.TrimSpace(home)
	if home == "" {
		return ""
	}
	candidates := []string{
		filepath.Join(home, "AppData", "Roaming", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"),
		filepath.Join(home, ".cline", "data", "settings", "cline_mcp_settings.json"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// mcpScanFingerprint 把各配置文件的 mtime 拼成指纹。
func mcpScanFingerprint(files []mcpConfigFile, enabled bool, enabledSources map[string]bool, disabledServers map[string]bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("enabled|%t\n", enabled))
	for _, f := range files {
		b.WriteString(string(f.Source))
		b.WriteByte('|')
		b.WriteString(f.Path)
		info, err := os.Stat(f.Path)
		if err != nil {
			b.WriteString("missing")
		} else {
			b.WriteString(fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size()))
		}
		b.WriteByte('\n')
	}
	appendBoolMapFingerprint(&b, "source", enabledSources)
	appendBoolMapFingerprint(&b, "disabled", disabledServers)
	return b.String()
}

func appendBoolMapFingerprint(builder *strings.Builder, prefix string, values map[string]bool) {
	if len(values) == 0 {
		return
	}
	normalized := make(map[string]bool, len(values))
	for key, value := range values {
		key = strings.ToLower(strings.TrimSpace(key))
		if key != "" {
			normalized[key] = value
		}
	}
	keys := make([]string, 0, len(normalized))
	for key := range normalized {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		builder.WriteString(prefix)
		builder.WriteByte('|')
		builder.WriteString(key)
		builder.WriteByte('|')
		if normalized[key] {
			builder.WriteString("true")
		} else {
			builder.WriteString("false")
		}
		builder.WriteByte('\n')
	}
}

func loadCachedMCPServers(fingerprint string) ([]MCPServerConfig, bool) {
	mcpScanCacheMu.RLock()
	defer mcpScanCacheMu.RUnlock()
	if mcpScanCacheFp == fingerprint && mcpScanCache != nil {
		return cloneMCPServerConfigs(mcpScanCache), true
	}
	return nil, false
}

// InvalidateMCPScanCache 清除 MCP 扫描缓存，供管理界面「重新扫描」按钮调用。
func InvalidateMCPScanCache() {
	mcpScanCacheMu.Lock()
	mcpScanCache = nil
	mcpScanCacheFp = ""
	mcpScanCacheMu.Unlock()
}

func cloneMCPServerConfigs(configs []MCPServerConfig) []MCPServerConfig {
	if len(configs) == 0 {
		return nil
	}
	cloned := make([]MCPServerConfig, len(configs))
	for index, config := range configs {
		cloned[index] = config
		cloned[index].Args = append([]string(nil), config.Args...)
		cloned[index].Env = cloneStringMap(config.Env)
		cloned[index].Headers = cloneStringMap(config.Headers)
	}
	return cloned
}

func cloneMCPServerConfig(config MCPServerConfig) MCPServerConfig {
	cloned := cloneMCPServerConfigs([]MCPServerConfig{config})
	if len(cloned) == 0 {
		return MCPServerConfig{}
	}
	return cloned[0]
}

func boolMapContains(values map[string]bool, key string) bool {
	if len(values) == 0 {
		return false
	}
	return values[strings.ToLower(strings.TrimSpace(key))]
}

func redactMCPURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme != "" {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.ForceQuery = false
		parsed.Fragment = ""
		return parsed.String()
	}
	if index := strings.Index(trimmed, "?"); index >= 0 {
		trimmed = trimmed[:index]
	}
	if index := strings.Index(trimmed, "#"); index >= 0 {
		trimmed = trimmed[:index]
	}
	if scheme := strings.Index(trimmed, "://"); scheme >= 0 {
		rest := trimmed[scheme+3:]
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			trimmed = trimmed[:scheme+3] + rest[at+1:]
		}
	}
	return trimmed
}

func mcpCommandBasename(command string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(command), "\\", "/")
	if normalized == "" {
		return ""
	}
	basename := path.Base(normalized)
	if basename == "." || basename == "/" {
		return ""
	}
	return strings.ToLower(basename)
}

func mcpCommandShapeClass(command string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(command), "\\", "/")
	if normalized == "" {
		return ""
	}
	switch strings.ToLower(path.Ext(normalized)) {
	case "":
		return "executable"
	case ".exe", ".com", ".bin":
		return "native"
	case ".bat", ".cmd", ".ps1", ".sh", ".bash", ".zsh", ".fish":
		return "shell-script"
	case ".js", ".cjs", ".mjs", ".ts", ".py", ".pyw", ".rb", ".php", ".pl", ".lua":
		return "script"
	default:
		return "other"
	}
}

func mcpURLSchemeClass(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "unknown"
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return strings.ToLower(parsed.Scheme)
	case "ws", "wss":
		return "websocket"
	case "file", "unix":
		return "local"
	case "":
		return "unknown"
	default:
		return "other"
	}
}

func mcpURLOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
}

func mcpSourceKind(source MCPSource) string {
	switch source {
	case MCPSourceCursor, MCPSourceClaude, MCPSourceShared, MCPSourceZCode, MCPSourceCodex, MCPSourceCline, MCPSourceWindsurf, MCPSourceVSCode:
		return string(source)
	default:
		return "other"
	}
}

func mcpConfigScopeKind(scope MCPConfigScope) string {
	switch scope {
	case MCPConfigScopeUser:
		return "user"
	case MCPConfigScopeWorkspace:
		return "workspace"
	default:
		return "other"
	}
}

func mcpTransportKind(transport string) string {
	switch normalizeTransport(transport) {
	case "", "stdio":
		return "stdio"
	case "http":
		return "http"
	case "sse":
		return "sse"
	default:
		return "other"
	}
}

func mcpRuntimeScopeKind(config MCPServerConfig) string {
	if config.Scope == MCPConfigScopeWorkspace || strings.HasPrefix(normalizeMCPRuntimeScope(config.RuntimeScope), "workspace:") {
		return "workspace"
	}
	return "user"
}

// mcpConfigShapeFingerprint identifies only coarse, non-secret config structure.
// Exact runtime replacement identity is handled by sameMCPRuntimeConfig.
func mcpConfigShapeFingerprint(config MCPServerConfig) string {
	payload, _ := json.Marshal(struct {
		SchemaVersion     int    `json:"schemaVersion"`
		SourceKind        string `json:"sourceKind"`
		ConfigScopeKind   string `json:"configScopeKind"`
		RuntimeScopeKind  string `json:"runtimeScopeKind"`
		TransportKind     string `json:"transportKind"`
		HasCommand        bool   `json:"hasCommand"`
		CommandClass      string `json:"commandClass,omitempty"`
		ArgumentCount     int    `json:"argumentCount"`
		EnvironmentCount  int    `json:"environmentCount"`
		HeaderCount       int    `json:"headerCount"`
		HasCwd            bool   `json:"hasCwd"`
		HasURL            bool   `json:"hasUrl"`
		URLSchemeClass    string `json:"urlSchemeClass,omitempty"`
		ConfiguredEnabled bool   `json:"configuredEnabled"`
		Enabled           bool   `json:"enabled"`
	}{
		SchemaVersion:     3,
		SourceKind:        mcpSourceKind(config.Source),
		ConfigScopeKind:   mcpConfigScopeKind(config.Scope),
		RuntimeScopeKind:  mcpRuntimeScopeKind(config),
		TransportKind:     mcpTransportKind(config.Transport),
		HasCommand:        strings.TrimSpace(config.Command) != "",
		CommandClass:      mcpCommandShapeClass(config.Command),
		ArgumentCount:     len(config.Args),
		EnvironmentCount:  len(config.Env),
		HeaderCount:       len(config.Headers),
		HasCwd:            strings.TrimSpace(config.Cwd) != "",
		HasURL:            strings.TrimSpace(config.URL) != "",
		URLSchemeClass:    mcpURLSchemeClass(config.URL),
		ConfiguredEnabled: config.ConfiguredEnabled,
		Enabled:           config.Enabled,
	})
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum)
}

// MCPServerSnapshotItem is the sanitized management view. It never exposes
// environment values, arguments, headers, credentials, or URL query strings.
type MCPServerSnapshotItem struct {
	Name              string           `json:"name"`
	Identifier        string           `json:"identifier"`
	Transport         string           `json:"transport"`
	Command           string           `json:"command,omitempty"`
	URL               string           `json:"url,omitempty"`
	Source            string           `json:"source"`
	Scope             string           `json:"scope"`
	ConfigPath        string           `json:"configPath,omitempty"`
	ConfiguredEnabled bool             `json:"configuredEnabled"`
	Enabled           bool             `json:"enabled"`
	HasTools          bool             `json:"hasTools"`
	ToolCount         int              `json:"toolCount"`
	Status            MCPRuntimeStatus `json:"status"`
	// ConfigFingerprint is a coarse non-secret shape hash, not runtime identity.
	ConfigFingerprint string           `json:"configFingerprint"`
	CapabilityStatus  MCPRuntimeStatus `json:"capabilityStatus"`
	LastError         string           `json:"lastError,omitempty"`
	LastCheckedAt     time.Time        `json:"lastCheckedAt,omitempty"`
	SourceLabel       string           `json:"sourceLabel"`
	RuntimeScope      string           `json:"runtimeScope"`
}

func SnapshotMCPServersWithSettings(workspaceRoot string, settings SkillMCPScanSettings) []MCPServerSnapshotItem {
	configs := ScanMCPServerConfigs(workspaceRoot, settings)
	registry := SharedMCPRuntimeRegistry()
	SyncMCPRuntimeForWorkspace(registry, workspaceRoot, enabledMCPServerConfigs(configs))
	configs = effectiveMCPServerConfigs(configs)
	runtimeByID := make(map[string]MCPRuntimeSnapshot)
	for _, scope := range mcpRuntimeTargetScopes(workspaceRoot) {
		for _, item := range registry.Snapshot(scope) {
			runtimeByID[mcpRuntimeEntryKey(item.RuntimeScope, item.Identifier)] = item
		}
	}
	items := make([]MCPServerSnapshotItem, 0, len(configs))
	for _, config := range configs {
		configFingerprint := mcpConfigShapeFingerprint(config)
		runtimeItem, connected := runtimeByID[mcpRuntimeEntryKey(config.RuntimeScope, config.Identifier)]
		// ReplaceScope already uses full config equality. The public shape fingerprint
		// must not be reused as exact runtime identity here.
		connected = connected && runtimeItem.Source == string(config.Source) && runtimeItem.Scope == string(config.Scope)
		status := MCPRuntimeDisconnected
		capabilityStatus := MCPRuntimeDisconnected
		toolCount := 0
		lastError := ""
		lastCheckedAt := time.Time{}
		if connected {
			status = runtimeItem.Status
			configFingerprint = runtimeItem.ConfigFingerprint
			capabilityStatus = runtimeItem.CapabilityStatus
			toolCount = runtimeItem.ToolCount
			lastError = runtimeItem.LastError
			lastCheckedAt = runtimeItem.LastCheckedAt
		}
		items = append(items, MCPServerSnapshotItem{
			Name:              config.Name,
			Identifier:        config.Identifier,
			Transport:         config.Transport,
			Command:           mcpCommandBasename(config.Command),
			URL:               mcpURLOrigin(config.URL),
			Source:            string(config.Source),
			Scope:             string(config.Scope),
			ConfigPath:        config.ConfigPath,
			ConfiguredEnabled: config.ConfiguredEnabled,
			Enabled:           config.Enabled,
			HasTools:          toolCount > 0,
			ToolCount:         toolCount,
			Status:            status,
			ConfigFingerprint: configFingerprint,
			CapabilityStatus:  capabilityStatus,
			LastError:         lastError,
			LastCheckedAt:     lastCheckedAt,
			SourceLabel:       string(config.Source),
			RuntimeScope:      config.RuntimeScope,
		})
	}
	return items
}

func enabledMCPServerConfigs(configs []MCPServerConfig) []MCPServerConfig {
	result := make([]MCPServerConfig, 0, len(configs))
	for _, config := range configs {
		if config.Enabled {
			result = append(result, config)
		}
	}
	return result
}

func mcpDescriptorsWithRuntime(configs []MCPServerConfig, registry *MCPRuntimeRegistry) []*agentv1.McpDescriptor {
	configs = effectiveMCPServerConfigs(configs)
	tools := make(map[string][]*agentv1.McpToolDescriptor)
	if registry != nil {
		for _, config := range configs {
			descriptor, ok := registry.Descriptor(config.RuntimeScope, config.Identifier)
			if !ok || descriptor == nil {
				continue
			}
			tools[mcpRuntimeEntryKey(config.RuntimeScope, descriptor.GetServerIdentifier())] = descriptor.GetTools()
		}
	}
	output := make([]*agentv1.McpDescriptor, 0, len(configs))
	for _, config := range configs {
		if !config.Enabled {
			continue
		}
		descriptor := buildMCPDescriptor(config)
		if serverTools := tools[mcpRuntimeEntryKey(config.RuntimeScope, config.Identifier)]; len(serverTools) > 0 {
			descriptor.Tools = cloneMCPToolDescriptors(serverTools)
		}
		output = append(output, descriptor)
	}
	return output
}

// effectiveMCPServerConfigs keeps one model-visible identifier while allowing
// user and workspace runtimes with the same identifier to coexist internally.
// Workspace configuration overrides user configuration for the active request.
func effectiveMCPServerConfigs(configs []MCPServerConfig) []MCPServerConfig {
	if len(configs) == 0 {
		return nil
	}
	result := make([]MCPServerConfig, 0, len(configs))
	indexByIdentifier := make(map[string]int, len(configs))
	for _, config := range configs {
		identifier := strings.ToLower(strings.TrimSpace(config.Identifier))
		if identifier == "" {
			continue
		}
		index, exists := indexByIdentifier[identifier]
		if !exists {
			indexByIdentifier[identifier] = len(result)
			result = append(result, config)
			continue
		}
		if result[index].Scope != MCPConfigScopeWorkspace && config.Scope == MCPConfigScopeWorkspace {
			result[index] = config
		}
	}
	return result
}

// SyncMCPRuntimeForWorkspace replaces only the user and current workspace runtime views.
func SyncMCPRuntimeForWorkspace(registry *MCPRuntimeRegistry, workspaceRoot string, configs []MCPServerConfig) {
	if registry == nil {
		return
	}
	byScope := make(map[string][]MCPServerConfig)
	for _, config := range configs {
		scope := normalizeMCPRuntimeScope(config.RuntimeScope)
		config.RuntimeScope = scope
		byScope[scope] = append(byScope[scope], config)
	}
	for _, scope := range mcpRuntimeTargetScopes(workspaceRoot) {
		registry.ReplaceScope(scope, byScope[scope])
	}
}

func mcpRuntimeTargetScopes(workspaceRoot string) []string {
	scopes := []string{MCPRuntimeScope("")}
	if workspaceScope := MCPRuntimeScope(workspaceRoot); workspaceScope != scopes[0] {
		scopes = append(scopes, workspaceScope)
	}
	return scopes
}
