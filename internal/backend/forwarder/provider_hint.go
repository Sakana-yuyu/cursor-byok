// provider_hint.go 为已知上游 provider 错误提供可读的本地化提示。
// 命中时返回按当前 UI 语言本地化的建议文案（供 forwarder 错误收口处追加到
// 用户可见消息之前，原始技术错误保留在后）；未命中返回空串，保持原有透传行为。
package forwarder

import (
	"errors"
	"strings"

	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/i18n"
)

// providerErrorContext 汇总 provider 错误链中可用于 hint 判定的结构化字段。
type providerErrorContext struct {
	Provider   string
	Model      string
	BaseURL    string
	StatusCode int
	Body       string
	Message    string
}

// providerHintRule 描述一条「已知上游错误 → 提示」规则。
type providerHintRule struct {
	// match 在已提取的 provider 错误上下文上做判定。
	match func(ctx providerErrorContext) bool
	// key 是 i18n 提示文案的 message key（见 internal/i18n/i18n.go）。
	key string
}

// providerHintRules 是已知的 provider 错误提示规则表，按声明顺序匹配。
// 新增「不支持某能力」类提示时，在此追加规则与对应 i18n key 即可，无需改动收口逻辑。
var providerHintRules = []providerHintRule{
	{
		// grok 多代理变体（grok-*-multi-agent）不支持 client-side tools（function calling），
		// 上游 xAI 会以 400 "Client-side tools for multi-agent models require beta access" 拒绝。
		// 该限制请求侧无法规避（剥离 tools 会让 Cursor 工具调用全失效），故仅在错误侧给出改用建议。
		match: isGrokMultiAgentClientSideToolsError,
		key:   "hint.grok_multi_agent_no_client_tools",
	},
}

// isGrokMultiAgentClientSideToolsError 识别 xAI/grok 的多代理变体因 client-side tools
// 被上游拒绝的 400。匹配信号宽松覆盖正文/消息的不同措辞，但要求模型名明确含 multi-agent，
// 避免误伤同样返回 400 的普通 grok 模型。
func isGrokMultiAgentClientSideToolsError(ctx providerErrorContext) bool {
	if ctx.StatusCode != 400 {
		return false
	}
	identity := strings.ToLower(strings.TrimSpace(ctx.Provider + " " + ctx.Model + " " + ctx.BaseURL))
	if !strings.Contains(identity, "grok") && !strings.Contains(identity, "x.ai") && !strings.Contains(identity, "xai") {
		return false
	}
	if !strings.Contains(strings.ToLower(ctx.Model), "multi-agent") {
		return false
	}
	haystack := strings.ToLower(ctx.Body + " " + ctx.Message)
	return strings.Contains(haystack, "client-side tools") ||
		strings.Contains(haystack, "multi-agent models require beta access")
}

// knownProviderErrorHint 返回针对已知上游 provider 错误的本地化提示文案；
// locale 取自进程级 i18n.CurrentLocale()（由前端 locale:changed 事件驱动）。
// 未命中任何规则或缺少渠道身份时返回空串。
func knownProviderErrorHint(cause error) string {
	return knownProviderErrorHintForLocale(i18n.CurrentLocale(), cause)
}

// knownProviderErrorHintForLocale 是 knownProviderErrorHint 的可测试形式，
// 显式传入 locale，避免测试依赖进程级全局状态。
func knownProviderErrorHintForLocale(locale string, cause error) string {
	if cause == nil {
		return ""
	}
	ctx, ok := extractProviderErrorContext(cause)
	if !ok {
		return ""
	}
	for _, rule := range providerHintRules {
		if rule.match(ctx) {
			return i18n.T(locale, rule.key)
		}
	}
	return ""
}

// extractProviderErrorContext 从错误链中提取 provider 身份与 HTTP 状态信息。
// 依赖 modeladapter.ChannelError（router 在 streamChannel 时填充 Provider/Model/BaseURL）
// 与 modeladapter.HTTPStatusError（携带上游 Body/StatusCode/Message）。
// 两者任一缺失则返回 ok=false（无法判定模型身份或上游响应）。
func extractProviderErrorContext(cause error) (providerErrorContext, bool) {
	var channelErr *modeladapter.ChannelError
	if !errors.As(cause, &channelErr) {
		return providerErrorContext{}, false
	}
	var statusErr *modeladapter.HTTPStatusError
	if !errors.As(cause, &statusErr) {
		return providerErrorContext{}, false
	}
	return providerErrorContext{
		Provider:   channelErr.Provider,
		Model:      channelErr.Model,
		BaseURL:    channelErr.BaseURL,
		StatusCode: statusErr.StatusCode,
		Body:       statusErr.Body,
		Message:    statusErr.Message,
	}, true
}
