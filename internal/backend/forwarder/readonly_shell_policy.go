// readonly_shell_policy.go 定义 inspect（只读）子代理的受控 Shell 白名单策略。
// 安全边界在服务端 pre-dispatch 强制执行，不依赖提示词描述。
package forwarder

import (
	"encoding/json"
	"fmt"
	"strings"

	runtimecore "cursor/internal/backend/agent/core"
)

// readonlyShellMaxBlockUntilMS 限定 inspect Shell 只允许短前台窗口，禁止 0（立即后台）。
const readonlyShellMaxBlockUntilMS = 120000

// gitReadonlySubcommands 是允许的只读 Git 证据链子命令。
// 刻意排除：config（可读出凭据 helper/token 配置）、worktree（add/remove/prune 会改状态）、
// ls-remote/fetch（网络访问）。
var gitReadonlySubcommands = map[string]struct{}{
	"status": {}, "diff": {}, "log": {}, "show": {}, "blame": {}, "rev-parse": {},
	"merge-base": {}, "tag": {}, "branch": {}, "remote": {}, "ls-tree": {}, "ls-files": {},
	"grep": {}, "describe": {}, "shortlog": {}, "cherry": {}, "count-objects": {},
	"stash": {}, "reflog": {},
}

// gitListOnlySubcommands 的位置参数会创建/删除引用，因此只允许纯 flag 的列表形式。
var gitListOnlySubcommands = map[string]struct{}{"tag": {}, "branch": {}, "remote": {}}

// readonlyShellSimpleExecutables 是白名单中的进程/端口/哈希查询命令；
// 值非空表示第一个参数必须精确命中（certutil 只允许哈希用途，阻断 LOLBIN 下载路径）。
var readonlyShellSimpleExecutables = map[string]string{
	"tasklist":  "",
	"netstat":   "",
	"ps":        "",
	"ss":        "",
	"lsof":      "",
	"sha256sum": "",
	"sha1sum":   "",
	"md5sum":    "",
	"shasum":    "",
	"certutil":  "-hashfile",
}

// applyReadonlyShellPolicy 校验 inspect 子代理的 Shell 参数，并返回注入只读保护后的 args。
// 工具 schema 对外宣告过的可选字段（notify_on_output、profile）必须被容忍而不是拒绝：
// 模型（尤其 GPT 系）会如实填写 schema 里的字段，硬拒绝会造成整段 Shell 调用被 Skipped 并无限重试。
func applyReadonlyShellPolicy(argsJSON []byte, workspacePaths []string) ([]byte, error) {
	args, err := runtimecore.DecodeArgsMap(argsJSON)
	if err != nil {
		return nil, fmt.Errorf("decode Shell args: %w", err)
	}
	mutated := false
	// notify_on_output 只对后台命令有意义；inspect 强制短前台窗口，静默剥离即可，不改变语义。
	for _, key := range []string{"notify_on_output", "notifyOnOutput"} {
		if _, found := args[key]; found {
			delete(args, key)
			mutated = true
		}
	}
	// 显式 profile 会经 base64 launcher 包装改写实际执行命令，绕过白名单看到的命令视图；静默归一到 auto。
	if profile := strings.TrimSpace(runtimecore.ReadStringArg(args, "profile")); profile != "" && profile != "auto" {
		args["profile"] = "auto"
		mutated = true
	}
	if blockUntilMS, found, err := runtimecore.ReadFloat64Arg(args, "block_until_ms", "blockUntilMS"); err != nil {
		return nil, fmt.Errorf("inspect Shell block_until_ms must be a number")
	} else if found && (blockUntilMS <= 0 || blockUntilMS > readonlyShellMaxBlockUntilMS) {
		return nil, fmt.Errorf("inspect Shell block_until_ms must stay within a short foreground window (1-%d ms)", readonlyShellMaxBlockUntilMS)
	}
	if err := validateReadonlyShellWorkingDirectory(runtimecore.ReadStringArg(args, "working_directory", "workingDirectory"), workspacePaths); err != nil {
		return nil, err
	}
	tokens, simple := parseReadonlyShellCommand(runtimecore.ReadStringArg(args, "command"))
	if !simple || len(tokens) == 0 {
		return nil, fmt.Errorf("inspect Shell only accepts a single simple command without pipes, redirection, variables, or command substitution")
	}
	rewrittenTokens, err := validateReadonlyShellCommandTokens(tokens)
	if err != nil {
		return nil, err
	}
	if rewrittenTokens != nil {
		args["command"] = joinReadonlyShellTokens(rewrittenTokens)
		mutated = true
	}
	if !mutated {
		return argsJSON, nil
	}
	rewritten, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("encode inspect Shell args: %w", err)
	}
	return rewritten, nil
}

// parseReadonlyShellCommand 是 inspect 白名单专用的引号感知分词器。工具描述要求模型为含空格
// 路径加引号，因此 "..." 与 '...' 必须被接受；引号外仍拒绝管道、重定向、变量与命令替换。
// 不改动 execbridge.ParseSimpleShellCommand——那是发给 Cursor 客户端 parsing_result 的通用视图。
func parseReadonlyShellCommand(command string) ([]string, bool) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return nil, false
	}
	var tokens []string
	var current strings.Builder
	inToken := false
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		switch {
		case ch == '"' || ch == '\'':
			quote := ch
			inToken = true
			closed := false
			for i++; i < len(trimmed); i++ {
				c := trimmed[i]
				if quote == '"' && c == '\\' && i+1 < len(trimmed) && trimmed[i+1] == '"' {
					current.WriteByte('"')
					i++
					continue
				}
				if c == quote {
					closed = true
					break
				}
				current.WriteByte(c)
			}
			if !closed {
				return nil, false
			}
		case ch == ' ' || ch == '\t':
			if inToken {
				tokens = append(tokens, current.String())
				current.Reset()
				inToken = false
			}
		case strings.ContainsRune("$;|&`<>(){}[]^\r\n", rune(ch)):
			return nil, false
		default:
			current.WriteByte(ch)
			inToken = true
		}
	}
	if inToken {
		tokens = append(tokens, current.String())
	}
	if len(tokens) == 0 || strings.Contains(tokens[0], "=") {
		return nil, false
	}
	return tokens, true
}

// joinReadonlyShellTokens 重组改写后的命令行，并为含空白的词元恢复双引号，
// 避免 --no-pager 注入把带空格路径拆散。
func joinReadonlyShellTokens(tokens []string) string {
	joined := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if strings.ContainsAny(token, " \t") {
			joined = append(joined, `"`+strings.ReplaceAll(token, `"`, `\"`)+`"`)
			continue
		}
		joined = append(joined, token)
	}
	return strings.Join(joined, " ")
}

// validateReadonlyShellWorkingDirectory 把 inspect Shell 绑定在会话工作区内。
func validateReadonlyShellWorkingDirectory(workingDirectory string, workspacePaths []string) error {
	trimmed := strings.TrimSpace(workingDirectory)
	if trimmed == "" {
		return nil
	}
	normalized := normalizeReadonlyShellPath(trimmed)
	for _, workspace := range workspacePaths {
		workspaceNormalized := normalizeReadonlyShellPath(workspace)
		if workspaceNormalized == "" {
			continue
		}
		if normalized == workspaceNormalized || strings.HasPrefix(normalized, workspaceNormalized+"/") {
			return nil
		}
	}
	return fmt.Errorf("inspect Shell working_directory must stay inside the workspace: %s", trimmed)
}

func normalizeReadonlyShellPath(path string) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
	return strings.TrimRight(normalized, "/")
}

// validateReadonlyShellCommandTokens 返回非 nil 时表示需要用改写后的词元替换原命令。
func validateReadonlyShellCommandTokens(tokens []string) ([]string, error) {
	if strings.ContainsAny(tokens[0], "/\\") {
		return nil, fmt.Errorf("inspect Shell requires a bare executable name: %s", tokens[0])
	}
	executable := strings.TrimSuffix(strings.ToLower(tokens[0]), ".exe")
	if executable == "git" {
		return validateReadonlyGitCommand(tokens)
	}
	requiredFirst, allowed := readonlyShellSimpleExecutables[executable]
	if !allowed {
		return nil, fmt.Errorf("inspect Shell command %q is not in the read-only whitelist", tokens[0])
	}
	if requiredFirst != "" && (len(tokens) < 2 || !strings.EqualFold(tokens[1], requiredFirst)) {
		return nil, fmt.Errorf("inspect Shell %q is only allowed as %q", executable, executable+" "+requiredFirst)
	}
	return nil, nil
}

func validateReadonlyGitCommand(tokens []string) ([]string, error) {
	if len(tokens) < 2 {
		return nil, fmt.Errorf("inspect Shell git requires a read-only subcommand")
	}
	subcommand := strings.ToLower(tokens[1])
	if _, ok := gitReadonlySubcommands[subcommand]; !ok {
		return nil, fmt.Errorf("inspect Shell git subcommand %q is not read-only", tokens[1])
	}
	rest := tokens[2:]
	if _, listOnly := gitListOnlySubcommands[subcommand]; listOnly {
		for _, token := range rest {
			if !strings.HasPrefix(token, "-") {
				return nil, fmt.Errorf("inspect Shell git %s only allows list-style flags, positional arguments would mutate refs", subcommand)
			}
			if !isAllowedGitListFlag(token) {
				return nil, fmt.Errorf("inspect Shell git %s flag %q is not allowed", subcommand, token)
			}
		}
	} else {
		switch subcommand {
		case "stash":
			// stash 仅允许只读的 list 形式；pop/apply/drop/push 会改写工作区或引用。
			if len(rest) == 0 || !strings.EqualFold(rest[0], "list") {
				return nil, fmt.Errorf(`inspect Shell git stash only allows "git stash list"`)
			}
		case "reflog":
			// 裸 reflog 等价于 show；expire/delete 会修改引用日志。
			if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") && !strings.EqualFold(rest[0], "show") {
				return nil, fmt.Errorf(`inspect Shell git reflog only allows "git reflog" or "git reflog show"`)
			}
		}
		for _, token := range rest {
			lower := strings.ToLower(token)
			if lower == "--output" || strings.HasPrefix(lower, "--output=") || lower == "--ext-diff" {
				return nil, fmt.Errorf("inspect Shell git flag %q is not allowed", token)
			}
			// git grep -O / --open-files-in-pager 会启动任意 pager 进程，等价命令执行。
			if subcommand == "grep" && (token == "-O" || strings.HasPrefix(token, "-O") || strings.HasPrefix(lower, "--open-files-in-pager")) {
				return nil, fmt.Errorf("inspect Shell git grep flag %q is not allowed", token)
			}
		}
	}
	// 注入 --no-pager --no-optional-locks，确保无分页器阻塞且不写 index 锁。
	return append([]string{tokens[0], "--no-pager", "--no-optional-locks"}, tokens[1:]...), nil
}

func isAllowedGitListFlag(token string) bool {
	lower := strings.ToLower(token)
	switch lower {
	case "-a", "-v", "-vv", "-l", "--all", "--list", "--verbose", "--show-current", "--merged", "--no-merged":
		return true
	}
	for _, prefix := range []string{"--sort=", "--format=", "--points-at=", "--contains=", "--column"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// enforceReadonlyShellPolicy 在 pre-dispatch 阶段对 inspect child 的 Shell 调用强制白名单，
// 校验通过时返回注入保护参数后的 invocation。
func (service *Service) enforceReadonlyShellPolicy(stream *ActiveStream, invocation runtimecore.ToolInvocation) (runtimecore.ToolInvocation, error) {
	if service == nil || stream == nil {
		return invocation, nil
	}
	stream.mu.Lock()
	workspacePaths := append([]string(nil), stream.WorkspacePaths...)
	stream.mu.Unlock()
	rewritten, err := applyReadonlyShellPolicy(invocation.ArgsJSON, workspacePaths)
	if err != nil {
		return invocation, err
	}
	invocation.ArgsJSON = rewritten
	return invocation, nil
}