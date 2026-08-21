// provider.go 把 forwarder 的 canonical 请求转交给现有的 provider adapter 层。
package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"cursor/internal/appdata"
	modeladapter "cursor/internal/backend/agent/model"
)

type provider400RecoveryReason string

const (
	provider400RecoveryContentExists provider400RecoveryReason = "content_exists"
	provider400RecoveryToolSchema    provider400RecoveryReason = "tool_schema"
)

// classifyProvider400Recovery returns a non-empty reason only for a known
// relay-side transient 400. Generic 400 responses remain terminal so malformed
// requests are not retried indefinitely.
func classifyProvider400Recovery(err error) provider400RecoveryReason {
	if err == nil {
		return ""
	}
	var statusErr *modeladapter.HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr == nil || statusErr.StatusCode != 400 {
		return ""
	}
	message := strings.ToLower(strings.TrimSpace(statusErr.Message))
	body := strings.ToLower(strings.TrimSpace(statusErr.Body))
	if exactContentExistsMessage(message) || exactContentExistsMessage(body) {
		return provider400RecoveryContentExists
	}
	if isProviderToolSchema400(message) || isProviderToolSchema400(body) {
		// 只有错误明确点名了 provider 工具才做 tool-schema 恢复；仅凭 marker 命中的
		// 泛化 400（如参数类错误）保持终态，避免对无法定位的请求盲目重试。
		if name, ok := providerToolSchema400ToolName(err); ok && strings.TrimSpace(name) != "" {
			return provider400RecoveryToolSchema
		}
	}
	return ""
}

// isProviderToolSchema400 matches a bounded set of schema/descriptor rejection
// markers. A generic 400 with no marker remains terminal.
func isProviderToolSchema400(value string) bool {
	for _, marker := range []string{
		"schema", "parameter", "input_schema", "tool_choice", "tool_call",
		"strict", "required", "additional_properties", "prompt_tool",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

// providerToolNameQuoteRe 匹配引号/反引号包裹的疑似工具名（1-64 字符，允许 MCP 名称中的空格）。
var providerToolNameQuoteRe = regexp.MustCompile("[`'\"]\\s*([A-Za-z_][A-Za-z0-9_\\-/. ]{0,63})\\s*[`'\"]")

// providerToolSchema400ToolName 从结构化 HTTPStatusError 中提取被明确命名的 provider 工具。
// 优先扫描 JSON 错误体提取出的 error.message 明文，再退回 Message 与原始 Body；
// 提取不到返回 ok=false。
func providerToolSchema400ToolName(err error) (string, bool) {
	var statusErr *modeladapter.HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr == nil {
		return "", false
	}
	if text := friendlyErrorMessageFromBody(statusErr.Body); text != "" {
		if name := extractProviderNamedTool(text); name != "" {
			return name, true
		}
	}
	for _, text := range []string{statusErr.Message, statusErr.Body} {
		if name := extractProviderNamedTool(text); name != "" {
			return name, true
		}
	}
	return "", false
}

// friendlyErrorMessageFromBody 从 JSON 错误体中提取 error.message / message 明文，
// 供工具名提取优先扫描，避免在原始 JSON 里命中结构键（如 "error"）。
func friendlyErrorMessageFromBody(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return ""
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(trimmed), &parsed) != nil {
		return ""
	}
	if errorObj, ok := parsed["error"].(map[string]any); ok {
		if message, ok := errorObj["message"].(string); ok {
			return strings.TrimSpace(message)
		}
	}
	if message, ok := parsed["message"].(string); ok {
		return strings.TrimSpace(message)
	}
	return ""
}

// providerToolNameStopwords 是 JSON 结构键/常见错误字段的停用词，防止原始文本扫描
// 把 {"error":...} 中的键误当成工具名。
var providerToolNameStopwords = map[string]struct{}{
	"error": {}, "message": {}, "type": {}, "code": {}, "detail": {},
	"body": {}, "value": {}, "key": {}, "data": {}, "status": {},
	"request": {}, "response": {}, "param": {}, "parameter": {}, "name": {},
}

// extractProviderNamedTool 从错误文本中提取引号包裹的工具名。
func extractProviderNamedTool(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	for _, match := range providerToolNameQuoteRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		name := strings.TrimSpace(match[1])
		if !isPlausibleProviderToolName(name) {
			continue
		}
		if _, stop := providerToolNameStopwords[strings.ToLower(name)]; stop {
			continue
		}
		return name
	}
	return ""
}

func isPlausibleProviderToolName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

// normalizeToolNameForMatch 把工具名折叠为仅含小写字母数字的形式，
// 用于兼容 provider 侧对 MCP 等工具名的转义（如 "mcp tool/unsafe" ↔ "mcp_tool_unsafe"）。
func normalizeToolNameForMatch(name string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func exactContentExistsMessage(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "content exists" || value == "content_exists" {
		return true
	}
	return strings.HasSuffix(value, "body=content exists") || strings.HasSuffix(value, "body=content_exists")
}

type DefaultProviderGateway struct {
	router modeladapter.ModelAdapterRouter
}

// NewProviderGateway 创建默认 provider 网关。
//
// 当 resolver 能提供本地响应缓存配置时，返回一个精确匹配响应缓存包装网关；
// 该缓存默认关闭，关闭时 StartStream 直接透传底层网关，行为与今日完全一致。
func NewProviderGateway(resolver modeladapter.ChannelResolver) ProviderGateway {
	base := &DefaultProviderGateway{
		router: modeladapter.NewRouter(resolver),
	}
	if settingsProvider, ok := resolver.(localResponseCacheSettingsProvider); ok {
		// 缓存条目持久化到磁盘（L2 层），跨进程/重启保留；持久化开关关闭时退回纯内存。
		persistPath := localResponseCachePersistPath()
		if _, _, _, persist := settingsProvider.LocalResponseCacheSettings(); !persist {
			persistPath = ""
		}
		return newCachingProviderGateway(base, settingsProvider.LocalResponseCacheSettings, persistPath)
	}
	return base
}

// localResponseCachePersistPath 返回本地响应缓存的磁盘持久化路径。
func localResponseCachePersistPath() string {
	return filepath.Join(appdata.DataRootPath(), "local-response-cache.json")
}

// StartStream 把 forwarder 的 provider 请求翻译成 modeladapter.StreamRequest 并发起流式调用。
func (gateway *DefaultProviderGateway) StartStream(ctx context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	defer releaseArtifactSession(req.Observer, req.RequestID, req.ModelCallID)
	requestKnobs := make(map[string]any, len(req.RequestKnobs)+2)
	for key, value := range req.RequestKnobs {
		requestKnobs[key] = value
	}
	requestKnobs["stream"] = true
	if req.MaxTokens > 0 {
		requestKnobs["max_tokens"] = req.MaxTokens
	}
	if strings.TrimSpace(req.ThinkingEffort) != "" {
		requestKnobs["runtime_thinking_effort"] = strings.TrimSpace(req.ThinkingEffort)
	}
	err := gateway.router.Stream(ctx, modeladapter.StreamRequest{
		RequestID:                 req.RequestID,
		RunID:                     req.RunID,
		ModelCallID:               req.ModelCallID,
		ConversationID:            req.ConversationID,
		Mode:                      req.Mode,
		ModelID:                   req.ModelID,
		ModelName:                 req.ModelName,
		Role:                      req.Role,
		ParentModel:               req.ParentModel,
		ModelGroupID:              req.ModelGroupID,
		TaskID:                    req.TaskID,
		ExecutionMode:             req.ExecutionMode,
		SupervisorModel:           req.SupervisorModel,
		ReviewerModel:             req.ReviewerModel,
		ThinkingEffort:            req.ThinkingEffort,
		MaxMode:                   req.MaxMode,
		Messages:                  req.Messages,
		StableMessageCount:        req.StableMessageCount,
		Tools:                     append([]json.RawMessage(nil), req.Tools...),
		MaxTokens:                 req.MaxTokens,
		Stream:                    true,
		RequestKnobs:              requestKnobs,
		CompileSummary:            req.CompileSummary,
		Observer:                  req.Observer,
		ArtifactPaths:             req.ArtifactPaths,
		RequestBodyOverride:       req.RequestBodyOverride,
		ProviderStreamIdleTimeout: req.ProviderStreamIdleTimeout,
	}, sink)
	if err != nil {
		return providerTerminalError{cause: err}
	}
	return nil
}

type artifactSessionCleaner interface {
	ClearActiveArtifacts(requestID string, modelCallID string)
}

func releaseArtifactSession(observer modeladapter.LLMArtifactObserver, requestID string, modelCallID string) {
	cleaner, ok := observer.(artifactSessionCleaner)
	if !ok {
		return
	}
	cleaner.ClearActiveArtifacts(requestID, modelCallID)
}
