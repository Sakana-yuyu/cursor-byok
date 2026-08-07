// agentsmd.go 实现 AGENTS.md/CLAUDE.md/GEMINI.md 的后端扫描兜底：
// 当客户端 rules 信息不完整时，把 workspace 根目录的通用 agent 说明文件
// 作为 non_file_rules 补充注入（与客户端内容按路径/内容去重）。
// 客户端已提供完整规则信息时完全不输出，保持 prompt 前缀稳定。
package forwarder

import (
	"os"
	"path/filepath"
	"strings"

	"cursor/gen/agentv1"
	"cursor/internal/logger"
)

// agentsFilesCandidates 是 workspace 根目录需要兜底扫描的通用 agent 说明文件。
var agentsFilesCandidates = []struct {
	Name string
	Path func(root string) string
}{
	{Name: "AGENTS.md", Path: func(root string) string { return filepath.Join(root, "AGENTS.md") }},
	{Name: "CLAUDE.md", Path: func(root string) string { return filepath.Join(root, "CLAUDE.md") }},
	{Name: "GEMINI.md", Path: func(root string) string { return filepath.Join(root, "GEMINI.md") }},
}

// enrichRequestContextWithAgentsFiles 在客户端 rules 信息不完整时扫描并兜底注入。
func (service *Service) enrichRequestContextWithAgentsFiles(intent *InboundIntent) {
	if service == nil || intent == nil || intent.RequestContext == nil {
		return
	}
	rc := intent.RequestContext
	// 客户端已声明规则信息完整：规则由客户端负责，后端不重复注入。
	if rc.GetRulesInfoComplete() {
		return
	}
	workspaceRoot := strings.TrimSpace(resolveWorkspaceRootFromIntent(intent))
	if workspaceRoot == "" {
		return
	}
	// 按 full_path 去重（客户端 rules 已含同名文件时不再扫描）。
	seenPaths := make(map[string]struct{}, len(rc.GetRules()))
	for _, rule := range rc.GetRules() {
		if rule == nil {
			continue
		}
		if path := strings.TrimSpace(rule.GetFullPath()); path != "" {
			seenPaths[path] = struct{}{}
		}
	}
	added := 0
	for _, candidate := range agentsFilesCandidates {
		fullPath := candidate.Path(workspaceRoot)
		if _, dup := seenPaths[fullPath]; dup {
			continue
		}
		content, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(content))
		if text == "" {
			continue
		}
		rc.NonFileRules = append(rc.NonFileRules, &agentv1.CursorRule{
			FullPath: fullPath,
			Content:  text,
		})
		seenPaths[fullPath] = struct{}{}
		added++
	}
	if added == 0 {
		return
	}
	rc.NonFileRules = guardCursorRules(rc.NonFileRules)
	logAgentsFilesInjection(intent.RequestID, intent.ConversationID, added)
}

// logAgentsFilesInjection 记录 AGENTS.md 兜底注入的运行时日志（debug 证据链）。
func logAgentsFilesInjection(requestID string, conversationID string, count int) {
	if count <= 0 {
		return
	}
	logger.Infof(
		"forwarder agents files injected request_id=%s conversation_id=%s files=%d",
		strings.TrimSpace(requestID),
		strings.TrimSpace(conversationID),
		count,
	)
}