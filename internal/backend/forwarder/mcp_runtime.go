package forwarder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"cursor/gen/agentv1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/types/known/structpb"
)

const mcpDiscoveryTimeout = 2 * time.Second

// mcpStderrTailLimit 保留子进程 stderr 的尾部字节数，用于连接/初始化失败时的诊断。
const mcpStderrTailLimit = 16 * 1024

// mcpStderrBuffer 是并发安全的 stderr 尾部缓冲（参考 Reasonix stdioTransport 的 tailBuffer）。
type mcpStderrBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (b *mcpStderrBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if b.limit > 0 && len(b.buf) > b.limit {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.limit:]...)
	}
	return len(p), nil
}

func (b *mcpStderrBuffer) String() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.buf))
}

// mcpStderrSuffix 把子进程 stderr 尾部拼成错误信息后缀（无内容时返回空串）。
func mcpStderrSuffix(buffer *mcpStderrBuffer) string {
	if buffer == nil {
		return ""
	}
	tail := buffer.String()
	if tail == "" {
		return ""
	}
	return fmt.Sprintf(" (stderr: %s)", tail)
}

// resolveMCPStdioCommand 解析 stdio 命令为可执行路径（参考 Reasonix resolveStdioExecutable）：
//   - 含路径分隔符时按绝对/相对路径直接使用；
//   - 否则先按当前环境 LookPath 解析；
//   - Windows 下 GUI 启动的进程可能缺少用户级 PATH，再按常见工具目录兜底查找一次；
//   - 仍找不到时返回可操作的中文错误（提示用绝对路径或在 env 中设置 PATH）。
func resolveMCPStdioCommand(config MCPServerConfig) (string, error) {
	command := strings.TrimSpace(config.Command)
	if command == "" {
		return "", fmt.Errorf("mcp stdio server %q 未配置 command", config.Name)
	}
	if strings.ContainsAny(command, `/\`) {
		return command, nil
	}
	if resolved, err := exec.LookPath(command); err == nil {
		return resolved, nil
	}
	if runtime.GOOS == "windows" {
		env := append([]string{}, os.Environ()...)
		for key, value := range config.Env {
			env = append(env, key+"="+value)
		}
		fallback := windowsMCPFallbackPATH(env)
		if fallback != "" {
			currentPath, _ := envLookup(env, "PATH")
			candidateEnv := setEnvValue(env, "PATH", mergePathLists(fallback, currentPath))
			if resolved, ok := lookPathInEnv(command, candidateEnv); ok {
				return resolved, nil
			}
		}
	}
	return "", fmt.Errorf("mcp stdio server %q: 命令 %q 不在 PATH 中。请使用命令的绝对路径，或在 MCP 配置的 env 中设置 PATH", config.Name, command)
}

// windowsMCPFallbackPATH 返回 Windows 上常见工具安装目录（nodejs/npm/python/scoop/bun 等），
// 仅保留实际存在的目录。GUI 启动的进程可能没继承交互式 shell 的 PATH，兜底这些位置。
func windowsMCPFallbackPATH(env []string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	programFiles, _ := envLookup(env, "ProgramFiles")
	programFilesX86, _ := envLookup(env, "ProgramFiles(x86)")
	localAppData, _ := envLookup(env, "LOCALAPPDATA")
	appData, _ := envLookup(env, "APPDATA")
	userProfile, _ := envLookup(env, "USERPROFILE")
	chocolatey, _ := envLookup(env, "ChocolateyInstall")
	if localAppData == "" && userProfile != "" {
		localAppData = filepath.Join(userProfile, "AppData", "Local")
	}
	if appData == "" && userProfile != "" {
		appData = filepath.Join(userProfile, "AppData", "Roaming")
	}
	candidates := []string{
		filepath.Join(programFiles, "nodejs"),
		filepath.Join(programFilesX86, "nodejs"),
		filepath.Join(localAppData, "Programs", "nodejs"),
		filepath.Join(appData, "npm"),
		filepath.Join(localAppData, "Microsoft", "WindowsApps"),
		filepath.Join(localAppData, "Programs", "Python"),
		filepath.Join(programFiles, "Python313"),
		filepath.Join(programFiles, "Python312"),
		filepath.Join(programFiles, "Python311"),
		filepath.Join(userProfile, "scoop", "shims"),
		filepath.Join(userProfile, ".bun", "bin"),
		filepath.Join(userProfile, ".cargo", "bin"),
		filepath.Join(chocolatey, "bin"),
	}
	var existing []string
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			existing = append(existing, dir)
		}
	}
	return strings.Join(existing, string(os.PathListSeparator))
}

// lookPathInEnv 在指定 env 的 PATH 中查找可执行文件（Windows 下按 PATHEXT 扩展）。
func lookPathInEnv(command string, env []string) (string, bool) {
	path, _ := envLookup(env, "PATH")
	pathext, _ := envLookup(env, "PATHEXT")
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		for _, name := range executableNames(command, pathext) {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, true
			}
		}
	}
	return "", false
}

// executableNames 返回 Windows 下命令的候选可执行文件名（含 PATHEXT 扩展）。
func executableNames(command, pathext string) []string {
	if runtime.GOOS != "windows" || filepath.Ext(command) != "" {
		return []string{command}
	}
	if strings.TrimSpace(pathext) == "" {
		pathext = ".COM;.EXE;.BAT;.CMD"
	}
	names := []string{command}
	seen := map[string]bool{strings.ToLower(command): true}
	for _, ext := range strings.Split(pathext, ";") {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		name := command + ext
		key := strings.ToLower(name)
		if !seen[key] {
			seen[key] = true
			names = append(names, name)
		}
	}
	return names
}

// envLookup 返回 env 切片中最后一个匹配 key 的值。
func envLookup(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		k, v, ok := strings.Cut(env[i], "=")
		if ok && ((runtime.GOOS == "windows" && strings.EqualFold(k, key)) || k == key) {
			return v, true
		}
	}
	return "", false
}

// setEnvValue 返回替换/追加 key 后的新 env 切片（不修改原切片）。
func setEnvValue(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if ok && ((runtime.GOOS == "windows" && strings.EqualFold(k, key)) || k == key) {
			if !replaced {
				out = append(out, key+"="+value)
				replaced = true
			}
			continue
		}
		out = append(out, kv)
	}
	if !replaced {
		out = append(out, key+"="+value)
	}
	return out
}

// mergePathLists 合并两条 PATH 列表并去重（前者优先）。
func mergePathLists(primary, secondary string) string {
	var out []string
	seen := map[string]bool{}
	for _, path := range []string{primary, secondary} {
		for _, dir := range filepath.SplitList(path) {
			if dir == "" || seen[dir] {
				continue
			}
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}

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
	cmd.SysProcAttr = hiddenWindowAttr()
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
