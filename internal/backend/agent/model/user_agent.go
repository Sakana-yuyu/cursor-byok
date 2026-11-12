package modeladapter

const (
	// ClaudeCodeUserAgent 用于将渠道模型请求伪装为 Claude Code 客户端。
	ClaudeCodeUserAgent = "claude-cli/2.1.177 (external, cli)"
	// AnthropicClaudeCodeUserAgent 用于 Anthropic provider 的 Claude Code UA 兼容。
	// 与上游 defaultCustomHeaders 的 User-Agent 保持一致（real claude-cli 版本号）。
	AnthropicClaudeCodeUserAgent = "claude-cli/2.1.177 (external, cli)"
)
