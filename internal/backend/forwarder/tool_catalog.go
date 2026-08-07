// tool_catalog.go 负责从静态 prompt 资产中装载并筛选 canonical tool catalog。
package forwarder

import (
	"encoding/json"
	"fmt"
	"strings"

	"cursor/gen/agentv1"
	promptassets "cursor/prompt"
)

type DefaultToolCatalog struct {
}

// NewToolCatalog 创建默认工具目录实现。
func NewToolCatalog() *DefaultToolCatalog {
	return &DefaultToolCatalog{}
}

// Load 按 mode 读取工具资产，并过滤出当前阶段真正允许暴露的工具。
func (catalog *DefaultToolCatalog) Load(mode agentv1.AgentMode, subagentTypeName string) ([]json.RawMessage, []string, error) {
	assetMode, err := toolAssetModeForConversation(mode, subagentTypeName)
	if err != nil {
		return nil, nil, err
	}
	rawTools, err := promptassets.ReadTools(assetMode)
	if err != nil {
		return nil, nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(rawTools, &items); err != nil {
		return nil, nil, fmt.Errorf("decode tools asset failed: %w", err)
	}
	filtered := make([]json.RawMessage, 0, len(items))
	names := make([]string, 0, len(items))
	for _, item := range items {
		name, err := extractToolName(item)
		if err != nil {
			return nil, nil, err
		}
		if !isToolAllowedInMode(mode, subagentTypeName, name) {
			continue
		}
		// inspect 子代理（Task 子会话 + PLAN 模式）的 Shell 描述改写为受控只读语义，
		// 并从 schema 删除 notify_on_output/profile：模型看不到的字段就不会填写，
		// 服务端剥离逻辑只兜底旧缓存请求。
		if name == "Shell" && isChildConversationSubagentTypeName(subagentTypeName) && normalizeMode(mode) == agentv1.AgentMode_AGENT_MODE_PLAN {
			item, err = rewriteReadonlyShellTool(item)
			if err != nil {
				return nil, nil, err
			}
		}
		filtered = append(filtered, item)
		names = append(names, name)
	}
	return filtered, names, nil
}

var agentModeToolNames = map[string]struct{}{
	"AskQuestion":             {},
	"CallMcpTool":             {},
	"Delete":                  {},
	"FetchMcpResource":        {},
	"GenerateImage":           {},
	"Glob":                    {},
	"Grep":                    {},
	"Ls":                      {},
	"PatchEdit":               {},
	"Read":                    {},
	"ReadLints":               {},
	"Shell":                   {},
	"AwaitShell":              {},
	"WriteShellStdin":         {},
	"ForceBackgroundShell":    {},
	"SwitchMode":              {},
	"Task":                    {},
	"TodoWrite":               {},
	"WebFetch":                {},
	"WebSearch":               {},
	"Write":                   {},
	"Fetch":                   {},
	"RecordScreen":            {},
	"ComputerUse":             {},
	"ForceBackgroundSubagent": {},
	"SubagentAwait":           {},
	"CreatePr":                {},
	"UpdatePr":                {},
	"SeeImage":                {},
	"send_final_summary":      {},
}

var multitaskModeToolNames = map[string]struct{}{
	"AskQuestion":             {},
	"CallMcpTool":             {},
	"Delete":                  {},
	"FetchMcpResource":        {},
	"GenerateImage":           {},
	"Glob":                    {},
	"Grep":                    {},
	"Ls":                      {},
	"PatchEdit":               {},
	"Read":                    {},
	"ReadLints":               {},
	"Shell":                   {},
	"AwaitShell":              {},
	"WriteShellStdin":         {},
	"ForceBackgroundShell":    {},
	"SwitchMode":              {},
	"Task":                    {},
	"TodoWrite":               {},
	"WebFetch":                {},
	"WebSearch":               {},
	"Write":                   {},
	"Fetch":                   {},
	"RecordScreen":            {},
	"ComputerUse":             {},
	"ForceBackgroundSubagent": {},
	"SubagentAwait":           {},
	"CreatePr":                {},
	"UpdatePr":                {},
	"SeeImage":                {},
}

var debugModeToolNames = map[string]struct{}{
	"AskQuestion":             {},
	"CallMcpTool":             {},
	"Delete":                  {},
	"FetchMcpResource":        {},
	"Glob":                    {},
	"Grep":                    {},
	"Ls":                      {},
	"PatchEdit":               {},
	"Read":                    {},
	"ReadLints":               {},
	"Shell":                   {},
	"AwaitShell":              {},
	"WriteShellStdin":         {},
	"ForceBackgroundShell":    {},
	"Task":                    {},
	"TodoWrite":               {},
	"WebFetch":                {},
	"WebSearch":               {},
	"Write":                   {},
	"Fetch":                   {},
	"RecordScreen":            {},
	"ComputerUse":             {},
	"ForceBackgroundSubagent": {},
	"SubagentAwait":           {},
	"CreatePr":                {},
	"UpdatePr":                {},
}

var askModeToolNames = map[string]struct{}{
	"AskQuestion":             {},
	"CallMcpTool":             {},
	"Delete":                  {},
	"FetchMcpResource":        {},
	"Glob":                    {},
	"Grep":                    {},
	"Ls":                      {},
	"PatchEdit":               {},
	"Read":                    {},
	"ReadLints":               {},
	"Shell":                   {},
	"AwaitShell":              {},
	"WriteShellStdin":         {},
	"ForceBackgroundShell":    {},
	"Task":                    {},
	"TodoWrite":               {},
	"WebFetch":                {},
	"WebSearch":               {},
	"Write":                   {},
	"Fetch":                   {},
	"RecordScreen":            {},
	"ComputerUse":             {},
	"ForceBackgroundSubagent": {},
	"SubagentAwait":           {},
	"CreatePr":                {},
	"UpdatePr":                {},
	"SeeImage":                {},
}

var planModeToolNames = map[string]struct{}{
	"AskQuestion":          {},
	"CallMcpTool":          {},
	"CreatePlan":           {},
	"FetchMcpResource":     {},
	"Glob":                 {},
	"Grep":                 {},
	"Ls":                   {},
	"Read":                 {},
	"ReadLints":            {},
	"Shell":                {},
	"AwaitShell":           {},
	"WriteShellStdin":      {},
	"ForceBackgroundShell": {},
	"Task":                 {},
	"TodoWrite":            {},
	"WebFetch":             {},
	"WebSearch":            {},
	"SeeImage":             {},
	"SubagentAwait":        {},
}

var childConversationDisallowedAgentToolNames = map[string]struct{}{
	"AskQuestion": {},
}

func supportedToolNamesForMode(mode agentv1.AgentMode) map[string]struct{} {
	switch normalizeMode(mode) {
	case agentv1.AgentMode_AGENT_MODE_AGENT:
		return agentModeToolNames
	case agentv1.AgentMode_AGENT_MODE_ASK:
		return askModeToolNames
	case agentv1.AgentMode_AGENT_MODE_PLAN:
		return planModeToolNames
	case agentv1.AgentMode_AGENT_MODE_DEBUG:
		return debugModeToolNames
	case agentv1.AgentMode_AGENT_MODE_MULTITASK:
		return multitaskModeToolNames
	default:
		return nil
	}
}

func isKnownBuiltInToolName(toolName string) bool {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return false
	}
	for _, names := range []map[string]struct{}{
		agentModeToolNames,
		multitaskModeToolNames,
		debugModeToolNames,
		askModeToolNames,
		planModeToolNames,
	} {
		if _, exists := names[name]; exists {
			return true
		}
	}
	return false
}

func isToolAllowedInMode(mode agentv1.AgentMode, subagentTypeName string, toolName string) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return false
	}
	if isChildConversationSubagentTypeName(subagentTypeName) {
		if _, disallowed := childConversationDisallowedAgentToolNames[trimmedToolName]; disallowed {
			return false
		}
		_, ok := agentModeToolNames[trimmedToolName]
		return ok
	}
	supported := supportedToolNamesForMode(mode)
	if supported == nil {
		return false
	}
	_, ok := supported[trimmedToolName]
	return ok
}

func isChildConversationSubagentTypeName(subagentTypeName string) bool {
	return strings.TrimSpace(subagentTypeName) != ""
}

func selectToolsByOrderedNames(items []json.RawMessage, orderedNames []string) ([]json.RawMessage, []string, error) {
	byName := make(map[string]json.RawMessage, len(items))
	for _, item := range items {
		name, err := extractToolName(item)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := byName[name]; !exists {
			byName[name] = item
		}
	}
	filtered := make([]json.RawMessage, 0, len(orderedNames))
	names := make([]string, 0, len(orderedNames))
	for _, name := range orderedNames {
		item, ok := byName[name]
		if !ok {
			return nil, nil, fmt.Errorf("tool descriptor %q not found in prompt asset", name)
		}
		filtered = append(filtered, item)
		names = append(names, name)
	}
	return filtered, names, nil
}

func toolAssetModeForConversation(mode agentv1.AgentMode, subagentTypeName string) (promptassets.Mode, error) {
	if isChildConversationSubagentTypeName(subagentTypeName) {
		return promptassets.ModeAgent, nil
	}
	return mapPromptMode(mode)
}

func promptAssetModeForConversation(mode agentv1.AgentMode, subagentTypeName string) (promptassets.Mode, error) {
	if isChildConversationSubagentTypeName(subagentTypeName) {
		return promptassets.ModeSubagent, nil
	}
	return mapPromptMode(mode)
}

// mapPromptMode 把协议 mode 映射为静态 prompt 资产对应的目录名。
func mapPromptMode(mode agentv1.AgentMode) (promptassets.Mode, error) {
	switch normalizeMode(mode) {
	case agentv1.AgentMode_AGENT_MODE_AGENT:
		return promptassets.ModeAgent, nil
	case agentv1.AgentMode_AGENT_MODE_ASK:
		return promptassets.ModeAsk, nil
	case agentv1.AgentMode_AGENT_MODE_PLAN:
		return promptassets.ModePlan, nil
	case agentv1.AgentMode_AGENT_MODE_DEBUG:
		return promptassets.ModeDebug, nil
	case agentv1.AgentMode_AGENT_MODE_MULTITASK:
		return promptassets.ModeMultitask, nil
	default:
		return "", fmt.Errorf("unsupported prompt asset mode: %s", mode.String())
	}
}

// rewriteReadonlyShellTool 把 inspect child 的 Shell 描述改写为受控白名单语义，与服务端校验保持一致；
// 同时从 schema 删除 notify_on_output/profile：模型看不到的字段就不会填写，
// 服务端剥离逻辑只兜底旧缓存请求（曾因 schema 宣告 + 服务端拒绝的矛盾导致模型无限重试 Skipped）。
func rewriteReadonlyShellTool(item json.RawMessage) (json.RawMessage, error) {
	var tool map[string]any
	if err := json.Unmarshal(item, &tool); err != nil {
		return nil, fmt.Errorf("decode Shell tool descriptor: %w", err)
	}
	function, _ := tool["function"].(map[string]any)
	if function == nil {
		return nil, fmt.Errorf("Shell tool descriptor function is missing")
	}
	function["description"] = "Restricted read-only Shell for inspect subagents. Server-enforced whitelist: a single simple command only (no pipes, redirection, variables, or command substitution; double or single quotes around arguments such as paths with spaces are allowed), working_directory must stay inside the workspace, and only a short foreground window is allowed (no backgrounding). Allowed commands: read-only git evidence (status/diff/log/show/blame/rev-parse/merge-base/ls-tree/ls-files/grep/describe/shortlog/cherry/count-objects, stash list, reflog show, plus flag-only tag/branch/remote listings; --no-pager --no-optional-locks are injected automatically), process/port queries (tasklist, netstat, ps, ss, lsof), and file hashing (sha256sum/sha1sum/md5sum/shasum, certutil -hashfile). Anything else is rejected before dispatch; do not attempt network access, builds, tests, script interpreters, or writes. Independent read-only checks should be issued as multiple Shell calls batched in the same reply."
	if parameters, ok := function["parameters"].(map[string]any); ok {
		if properties, ok := parameters["properties"].(map[string]any); ok {
			delete(properties, "notify_on_output")
			delete(properties, "profile")
		}
		if required, ok := parameters["required"].([]any); ok {
			parameters["required"] = removeSchemaFields(required, "notify_on_output", "profile")
		}
	}
	return json.Marshal(tool)
}

// removeSchemaFields 从 required 列表中移除指定字段，与 appendRequiredSchemaFields 对称。
func removeSchemaFields(required []any, fields ...string) []any {
	drop := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		drop[field] = struct{}{}
	}
	result := make([]any, 0, len(required))
	for _, field := range required {
		if text, ok := field.(string); ok {
			if _, found := drop[text]; found {
				continue
			}
		}
		result = append(result, field)
	}
	return result
}

// extractToolName 从原始 tool descriptor JSON 中提取函数名。
func extractToolName(raw json.RawMessage) (string, error) {
	var wrapper struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return "", fmt.Errorf("decode tool descriptor failed: %w", err)
	}
	name := strings.TrimSpace(wrapper.Function.Name)
	if name == "" {
		return "", fmt.Errorf("tool descriptor name is required")
	}
	return name, nil
}

// sanitizePromptAsset 去掉资产文件中的说明性标题，只保留真正的 prompt 文本。
func sanitizePromptAsset(text string, modelName string) string {
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "# 通用系统提示词", "# 模式静态补充", "---":
			continue
		default:
			filtered = append(filtered, line)
		}
	}
	return promptassets.RenderPromptTemplate(strings.TrimSpace(strings.Join(filtered, "\n")), modelName)
}
