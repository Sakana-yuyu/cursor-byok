package bridge

// skill_editor.go 提供 Skills/MCP 设置页的「编辑」与「一键简介」能力。
//
// 职责边界：
//   - 编辑：读取 / 写回技能 SKILL.md 原文（仅允许操作扫描结果中的技能文件，防止任意路径写入）。
//   - 简介：用当前配置的第一个可用模型通道生成中文简介，持久化到 config.yaml
//     （skillSummaries / mcpSummaries），不写回 SKILL.md，避免污染原始内容。

import (
	"bytes"
	"context"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/forwarder"
	serverconfig "cursor/internal/backend/server/config"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SkillFileContent 是编辑技能正文时返回的文件快照。
type SkillFileContent struct {
	Name     string `json:"name"`
	FullPath string `json:"fullPath"`
	Content  string `json:"content"`
}

const (
	summaryKindSkill = "skill"
	summaryKindMCP   = "mcp"
)

// summarySystemPrompt 要求模型输出纯简介，不附带任何包装文本。
const summarySystemPrompt = "你是技能文档分析器。阅读用户提供的内容，用简体中文输出 2~4 句话的简介，概括它的作用与核心功能。直接输出简介本身，不要输出标题、引号或多余说明。"

// summaryRequest 是 OpenAI 兼容 chat/completions 的最小非流式请求体。
type summaryRequest struct {
	Model       string `json:"model"`
	Messages    []summaryMessage `json:"messages"`
	Temperature float64 `json:"temperature"`
	Stream      bool   `json:"stream"`
}

type summaryMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// normalizeSummaryKey 与前端 normalizeConfigKey 保持一致的规范化 key。
func normalizeSummaryKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// findSkillByKey 从扫描结果中按 name（小写）查找技能记录。
func findSkillByKey(workspaceRoot, key string) (forwarder.SourcedGlobalSkill, bool) {
	key = normalizeSummaryKey(key)
	for _, skill := range forwarder.ScanAllSkills(workspaceRoot) {
		if normalizeSummaryKey(skill.Name) == key {
			return skill, true
		}
	}
	return forwarder.SourcedGlobalSkill{}, false
}

// ReadSkillFile 返回技能 SKILL.md 的完整正文（含 frontmatter），供设置页编辑。
func (s *ProxyService) ReadSkillFile(workspaceRoot, name string) (SkillFileContent, error) {
	skill, ok := findSkillByKey(workspaceRoot, name)
	if !ok || strings.TrimSpace(skill.FullPath) == "" {
		return SkillFileContent{}, fmt.Errorf("未找到技能 %q", name)
	}
	content, err := os.ReadFile(skill.FullPath)
	if err != nil {
		return SkillFileContent{}, fmt.Errorf("读取技能文件失败: %w", err)
	}
	return SkillFileContent{
		Name:     skill.Name,
		FullPath: skill.FullPath,
		Content:  string(content),
	}, nil
}

// SaveSkillFile 将编辑后的正文写回原 SKILL.md。
// 安全约束：目标必须是扫描结果中该技能对应的 SKILL.md 绝对路径，
// 且文件名必须为 SKILL.md，防止通过 name 参数构造任意路径写入。
func (s *ProxyService) SaveSkillFile(workspaceRoot, name, content string) error {
	skill, ok := findSkillByKey(workspaceRoot, name)
	if !ok || strings.TrimSpace(skill.FullPath) == "" {
		return fmt.Errorf("未找到技能 %q", name)
	}
	fullPath := skill.FullPath
	if filepath.Base(fullPath) != "SKILL.md" {
		return fmt.Errorf("拒绝写入非 SKILL.md 路径: %s", fullPath)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("写入技能文件失败: %w", err)
	}
	forwarder.InvalidateSkillScanCache()
	return nil
}

// pickSummaryModel 选取配置中第一个可用的模型通道（baseURL / apiKey / modelID 均非空）。
func pickSummaryModel(cfg serverconfig.Config) (serverconfig.ModelAdapterConfig, bool) {
	for _, adapter := range cfg.ModelAdapters {
		if strings.TrimSpace(adapter.BaseURL) != "" &&
			strings.TrimSpace(adapter.APIKey) != "" &&
			strings.TrimSpace(adapter.ModelID) != "" {
			return adapter, true
		}
	}
	return serverconfig.ModelAdapterConfig{}, false
}

// callChatCompletion 用 OpenAI 兼容 chat/completions 非流式接口生成文本。
func callChatCompletion(adapter serverconfig.ModelAdapterConfig, systemPrompt, userContent string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(adapter.BaseURL), "/")
	if base == "" {
		return "", fmt.Errorf("模型通道 baseURL 为空")
	}
	endpoint := base + "/chat/completions"
	// 复用主流程的端点拼接规则：baseURL 以 /vN 结尾时剥离 endpoint 的 /vN/ 版本前缀，
	// 避免 baseURL=/v1 + endpoint=/v1/chat/completions 拼出 /v1/v1/chat/completions 这类 404 路径。
	if ep := strings.TrimSpace(adapter.OpenAIEndpoint); strings.Contains(ep, "chat/completions") {
		endpoint = modeladapter.OpenAIEndpointURL(base, ep)
	}

	payload, err := json.Marshal(summaryRequest{
		Model: adapter.ModelID,
		Messages: []summaryMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userContent},
		},
		Temperature: 0.3,
		Stream:      false,
	})
	if err != nil {
		return "", fmt.Errorf("构造请求失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("构造 HTTP 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adapter.APIKey)
	if adapter.CustomHeadersEnabled && strings.TrimSpace(adapter.CustomHeadersJSON) != "" {
		var headers map[string]string
		if json.Unmarshal([]byte(adapter.CustomHeadersJSON), &headers) == nil {
			for key, value := range headers {
				req.Header.Set(key, value)
			}
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求模型失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("读取模型响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("模型返回异常状态 %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("解析模型响应失败: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return "", fmt.Errorf("模型响应为空")
	}
	return strings.TrimSpace(decoded.Choices[0].Message.Content), nil
}

// GenerateSkillSummary 用当前配置的第一个可用模型为 skill/MCP 生成中文简介，
// 生成成功后写入 config（skillSummaries / mcpSummaries）持久化，并返回简介文本。
// kind 取值为 "skill" 或 "mcp"；key 为技能名 / MCP identifier（小写匹配）。
func (s *ProxyService) GenerateSkillSummary(workspaceRoot, kind, key string) (string, error) {
	cfg, err := s.core.LoadUserConfig()
	if err != nil {
		return "", err
	}
	adapter, ok := pickSummaryModel(cfg)
	if !ok {
		return "", fmt.Errorf("没有可用的模型通道，请先在模型配置中填写 baseURL、API Key 与模型 ID")
	}

	key = normalizeSummaryKey(key)
	var userContent, targetDesc string
	switch kind {
	case summaryKindSkill:
		skill, found := findSkillByKey(workspaceRoot, key)
		if !found {
			return "", fmt.Errorf("未找到技能 %q", key)
		}
		content, err := os.ReadFile(skill.FullPath)
		if err != nil {
			return "", fmt.Errorf("读取技能文件失败: %w", err)
		}
		userContent = string(content)
		targetDesc = "技能 " + skill.Name
	case summaryKindMCP:
		servers := forwarder.SnapshotMCPServersWithSettings(workspaceRoot, skillMCPScanSettings(cfg.SkillMCPScan))
		var found *forwarder.MCPServerSnapshotItem
		for i := range servers {
			if normalizeSummaryKey(servers[i].Identifier) == key {
				found = &servers[i]
				break
			}
		}
		if found == nil {
			return "", fmt.Errorf("未找到 MCP server %q", key)
		}
		userContent = fmt.Sprintf(
			"名称: %s\n标识符: %s\n传输方式: %s\n命令: %s\nURL: %s\n来源: %s\n运行状态: %s\n工具数量: %d",
			found.Name, found.Identifier, found.Transport, found.Command, found.URL,
			found.Source, found.Status, found.ToolCount,
		)
		targetDesc = "MCP server " + found.Identifier
	default:
		return "", fmt.Errorf("未知的生成目标类型 %q", kind)
	}

	summary, err := callChatCompletion(adapter, summarySystemPrompt, userContent)
	if err != nil {
		return "", fmt.Errorf("生成%s简介失败: %w", targetDesc, err)
	}
	if summary == "" {
		return "", fmt.Errorf("生成%s简介失败: 模型返回为空", targetDesc)
	}

	if cfg.SkillMCPScan.SkillSummaries == nil {
		cfg.SkillMCPScan.SkillSummaries = map[string]string{}
	}
	if cfg.SkillMCPScan.MCPSummaries == nil {
		cfg.SkillMCPScan.MCPSummaries = map[string]string{}
	}
	switch kind {
	case summaryKindSkill:
		cfg.SkillMCPScan.SkillSummaries[key] = summary
	case summaryKindMCP:
		cfg.SkillMCPScan.MCPSummaries[key] = summary
	}
	if err := s.core.SaveUserConfig(cfg); err != nil {
		return "", fmt.Errorf("保存简介失败: %w", err)
	}
	return summary, nil
}