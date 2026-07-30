// mcp_scanner.go 扫描各主流编码工具的 MCP 配置文件，归一化为 McpDescriptor，
// 作为 RequestContext.McpFileSystemOptions 的补充来源注入，让模型通过原生
// <mcp_file_system> user message 得知可用的 MCP server。
//
// 覆盖格式（按主流程度）：
//   - JSON（Cursor / Claude / Cline / 共享 .agents / ZCode 嵌套 mcp.servers）—— encoding.json
//   - TOML（Codex ~/.codex/config.toml 的 [mcp_servers.*]）—— 轻量定向解析，不引入第三方依赖
//
// 注意：磁盘配置只含 server 启动信息（command/args/env/url），不含每个工具的 input_schema
// （schema 需连上 server 才能拿到）。本阶段只注入 server 清单，已比现状（模型一无所知）大幅改善。
// tool schema 自动获取留待后续「MCP 自托管执行」。
package forwarder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"cursor/gen/agentv1"
)

// MCPSource 标识一个 MCP server 配置来自哪个工具。
type MCPSource string

const (
	MCPSourceCursor MCPSource = "cursor" // ~/.cursor/mcp.json 或 <ws>/.cursor/mcp.json
	MCPSourceClaude MCPSource = "claude" // ~/.claude.json、~/.claude/settings.json、<ws>/.mcp.json
	MCPSourceShared MCPSource = "shared" // <ws>/.agents/mcp.json 或 ~/.agents/mcp.json
	MCPSourceZCode  MCPSource = "zcode"  // ~/.zcode/cli/config.json、<ws>/.zcode/config.json（嵌套 mcp.servers）
	MCPSourceCodex  MCPSource = "codex"  // ~/.codex/config.toml 的 [mcp_servers.*]
	MCPSourceCline  MCPSource = "cline"  // cline_mcp_settings.json
)

// mcpScanCache 缓存一次 MCP 扫描结果（同 skill 扫描，按 mtime 失效）。
var (
	mcpScanCacheMu sync.RWMutex
	mcpScanRunMu   sync.Mutex
	mcpScanCache   []*agentv1.McpDescriptor
	mcpScanCacheFp string
)

// mcpConfigFile 是一个待扫描的配置文件及其解析方式。
type mcpConfigFile struct {
	Path   string
	Source MCPSource
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

// ScanAllMCPServers 扫描所有工具的 MCP 配置，按 ServerIdentifier 去重（先到先得），
// 返回 *agentv1.McpDescriptor 列表。workspaceRoot 为空时仅扫描用户级配置。
func ScanAllMCPServers(workspaceRoot string) []*agentv1.McpDescriptor {
	return scanMCPServers(workspaceRoot, nil, nil)
}

func scanMCPServers(workspaceRoot string, enabledSources map[string]bool, disabledServers map[string]bool) []*agentv1.McpDescriptor {
	files := orderedMCPConfigFiles(workspaceRoot)
	fingerprint := mcpScanFingerprint(files, enabledSources, disabledServers)
	if cached, ok := loadCachedMCPServers(fingerprint); ok {
		return cached
	}
	mcpScanRunMu.Lock()
	defer mcpScanRunMu.Unlock()
	if cached, ok := loadCachedMCPServers(fingerprint); ok {
		return cached
	}

	seen := make(map[string]struct{}, 16)
	merged := make([]*agentv1.McpDescriptor, 0, 16)
	for _, f := range files {
		if !sourceEnabled(enabledSources, string(f.Source)) {
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
			if !srv.Enabled {
				continue
			}
			identifier := firstNonEmpty(srv.ServerIdentifier, srv.ServerName, name)
			if identifier == "" {
				continue
			}
			key := strings.ToLower(identifier)
			if disabledServers != nil && disabledServers[key] {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			descriptor := buildMcpDescriptor(srv, identifier)
			merged = append(merged, descriptor)
		}
	}

	mcpScanCacheMu.Lock()
	mcpScanCache = merged
	mcpScanCacheFp = fingerprint
	mcpScanCacheMu.Unlock()
	return merged
}

// buildMcpDescriptor 把归一化的 server 配置转成 proto descriptor。
// 不带 Tools schema（磁盘配置里没有）；ServerUseInstructions 给模型一句简短能力提示。
func buildMcpDescriptor(srv normalizedMCPServer, identifier string) *agentv1.McpDescriptor {
	desc := &agentv1.McpDescriptor{
		ServerName:       firstNonEmpty(srv.ServerName, identifier),
		ServerIdentifier: identifier,
	}
	if folder := strings.TrimSpace(srv.Cwd); folder != "" {
		f := folder
		desc.FolderPath = &f
	}
	if hint := buildMCPUseHint(srv); hint != "" {
		h := hint
		desc.ServerUseInstructions = &h
	}
	return desc
}

// buildMCPUseHint 给模型一句简短的能力/调用提示，弥补缺少 tool schema 的信息缺口。
func buildMCPUseHint(srv normalizedMCPServer) string {
	transport := strings.TrimSpace(srv.Transport)
	if transport == "" {
		transport = "stdio"
	}
	var b strings.Builder
	b.WriteString("MCP server \"")
	b.WriteString(firstNonEmpty(srv.ServerName, srv.ServerIdentifier))
	b.WriteString("\" (")
	b.WriteString(transport)
	b.WriteString(")")
	if srv.Command != "" {
		b.WriteString(", command: ")
		b.WriteString(srv.Command)
	}
	if srv.URL != "" {
		b.WriteString(", url: ")
		b.WriteString(srv.URL)
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
	addJSON := func(path string, source MCPSource, nested bool) {
		if strings.TrimSpace(path) == "" {
			return
		}
		parser := parseMCPJSON
		if nested {
			parser = parseZCodeMCPJSON
		}
		files = append(files, mcpConfigFile{Path: path, Source: source, Parser: parser})
	}
	addTOML := func(path string, source MCPSource) {
		if strings.TrimSpace(path) == "" {
			return
		}
		files = append(files, mcpConfigFile{Path: path, Source: source, Parser: parseCodexMCPTOML})
	}

	// Cursor
	if home != "" {
		addJSON(filepath.Join(home, ".cursor", "mcp.json"), MCPSourceCursor, false)
	}
	if ws != "" {
		addJSON(filepath.Join(ws, ".cursor", "mcp.json"), MCPSourceCursor, false)
	}
	// Claude Code
	if ws != "" {
		addJSON(filepath.Join(ws, ".mcp.json"), MCPSourceClaude, false)
	}
	if home != "" {
		addJSON(filepath.Join(home, ".claude.json"), MCPSourceClaude, false)
		addJSON(filepath.Join(home, ".claude", "settings.json"), MCPSourceClaude, false)
	}
	// 共享 .agents
	if ws != "" {
		addJSON(filepath.Join(ws, ".agents", "mcp.json"), MCPSourceShared, false)
	}
	if home != "" {
		addJSON(filepath.Join(home, ".agents", "mcp.json"), MCPSourceShared, false)
	}
	// ZCode（嵌套 mcp.servers）
	if home != "" {
		addJSON(filepath.Join(home, ".zcode", "cli", "config.json"), MCPSourceZCode, true)
	}
	if ws != "" {
		addJSON(filepath.Join(ws, ".zcode", "config.json"), MCPSourceZCode, true)
		addJSON(filepath.Join(ws, "zcode.json"), MCPSourceZCode, true)
	}
	// Codex（TOML）
	if home != "" {
		addTOML(filepath.Join(home, ".codex", "config.toml"), MCPSourceCodex)
	}
	if ws != "" {
		addTOML(filepath.Join(ws, ".codex", "config.toml"), MCPSourceCodex)
	}
	// Cline
	if cline := clineMCPSettingsPath(); cline != "" {
		addJSON(cline, MCPSourceCline, false)
	}
	return files
}

// parseMCPJSON 解析顶层 { "mcpServers": { ... } } 格式（Cursor/Claude/Cline/共享）。
func parseMCPJSON(data []byte) (map[string]normalizedMCPServer, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	raw, ok := root["mcpServers"]
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
	return srv
}

// normalizeTransport 归一化 transport 类型名。
func normalizeTransport(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "stdio":
		return "stdio"
	case "sse":
		return "sse"
	case "http", "streamable-http", "streamable_http":
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
	currentFields := map[string]any{}

	flush := func() {
		if current == "" {
			return
		}
		if _, exists := servers[current]; !exists {
			servers[current] = normalizeOneMCPServer(current, currentFields)
		}
		current = ""
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
				flush()
				current = strings.TrimSpace(strings.TrimPrefix(header, "mcp_servers."))
				currentFields = map[string]any{}
				continue
			}
			// 进入其他表，结束当前 server 块
			flush()
			current = ""
			continue
		}
		if current == "" {
			continue
		}
		key, value, ok := splitTOMLKeyValue(line)
		if !ok {
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
func mcpScanFingerprint(files []mcpConfigFile, enabledSources map[string]bool, disabledServers map[string]bool) string {
	var b strings.Builder
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

func loadCachedMCPServers(fingerprint string) ([]*agentv1.McpDescriptor, bool) {
	mcpScanCacheMu.RLock()
	defer mcpScanCacheMu.RUnlock()
	if mcpScanCacheFp == fingerprint && mcpScanCache != nil {
		out := make([]*agentv1.McpDescriptor, len(mcpScanCache))
		copy(out, mcpScanCache)
		return out, true
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

// MCPServerSnapshotItem 是管理界面展示用的单个 MCP server 快照。
type MCPServerSnapshotItem struct {
	Name        string `json:"name"`
	Identifier  string `json:"identifier"`
	Transport   string `json:"transport"`
	Command     string `json:"command,omitempty"`
	URL         string `json:"url,omitempty"`
	Source      string `json:"source"`
	HasTools    bool   `json:"hasTools"`
	SourceLabel string `json:"sourceLabel"`
}

// SnapshotMCPServers 返回当前所有已扫描 MCP server 的展示快照。
func SnapshotMCPServers(workspaceRoot string) []MCPServerSnapshotItem {
	servers := ScanAllMCPServers(workspaceRoot)
	out := make([]MCPServerSnapshotItem, 0, len(servers))
	for _, desc := range servers {
		transport := "stdio"
		hint := ""
		if desc.ServerUseInstructions != nil {
			hint = *desc.ServerUseInstructions
		}
		if strings.Contains(hint, "(http)") {
			transport = "http"
		} else if strings.Contains(hint, "(sse)") {
			transport = "sse"
		}
		out = append(out, MCPServerSnapshotItem{
			Name:        desc.GetServerName(),
			Identifier:  desc.GetServerIdentifier(),
			Transport:   transport,
			Source:      "",
			SourceLabel: inferMCPServerSourceLabel(hint, desc),
			HasTools:    len(desc.GetTools()) > 0,
		})
	}
	return out
}

// inferMCPServerSourceLabel 从 hint 文本推断来源（简化实现，真实来源在扫描时丢失，可后续传递）。
func inferMCPServerSourceLabel(_ string, _ *agentv1.McpDescriptor) string {
	return ""
}
