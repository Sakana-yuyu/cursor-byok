package modeladapter

import (
	"regexp"
	"strings"
	"sync"

	"cursor/internal/logger"
	"cursor/internal/modelchannel"
)

// ProviderCompatibility is the conservative compatibility policy for an
// OpenAI-compatible provider. It is intentionally request-shape agnostic so
// Chat Completions and Responses can share the same provider decisions.
type ProviderCompatibility struct {
	Kind                      string
	PromptCacheKey            bool
	ThinkingDisableKind       string
	StripPrivateFields        bool
	FilterResponsesTools      bool
	DropResponsesFields       bool
	DropGrok45Sampling        bool
	AllowResponsesEOFFallback bool
	// MaxTokensField 表示 Chat Completions 输出 token 上限使用的字段名：
	// maxTokensFieldLegacy（默认）或 maxTokensFieldCompletion。官方 OpenAI
	// 推理系模型（o 系、gpt-5）的 chat completions 端点已废弃 max_tokens，
	// 必须改发 max_completion_tokens，否则上游直接 400。
	MaxTokensField string
}

// Chat Completions 输出 token 上限字段的两个合法取值。
const (
	maxTokensFieldLegacy     = "max_tokens"
	maxTokensFieldCompletion = "max_completion_tokens"
)

// openAIReasoningModelMaxCompletionPattern 匹配官方 OpenAI 要求
// max_completion_tokens 的模型名：o1/o3/o4 系列（^o\d 统一覆盖 o1-preview、
// o4-mini 等变体）与 gpt-5 系列。
var openAIReasoningModelMaxCompletionPattern = regexp.MustCompile(`^(o\d|gpt-5)`)

// validCompatibilityKinds 列出显式 compatibilityKind 字段允许的全部取值，
// 与自动匹配可产生的 Kind 一一对应；额外提供 "openai" 表示强制走默认 OpenAI
// 兼容策略（Kind 为空），用于在 baseURL/模型名含误导性字符串信号时关闭自动 quirk。
var validCompatibilityKinds = map[string]struct{}{
	"openai":      {},
	"copilot":     {},
	"deepseek":    {},
	"xai":         {},
	"kimi":        {},
	"openrouter":  {},
	"siliconflow": {},
	"zhipu":       {},
	"qwen":        {},
	"mimo":        {},
	"minimax":     {},
	"stepfun":     {},
}

// 显式兼容 kind 覆盖表：键为规范化 "baseURL\nmodelID"。由配置解析层在每次渠道
// 解析前全量同步，热加载后自动收敛到最新值；classify 请求侧只读。
var (
	explicitKindMu        sync.RWMutex
	explicitKindByChannel map[string]string
	invalidKindWarnMu     sync.Mutex
	invalidKindWarned     = map[string]struct{}{}
)

// SetExplicitCompatibilityKindOverrides 全量替换显式兼容 kind 覆盖表。
// 值会被规范化为小写；非法值记 warning 并跳过（运行时回落自动匹配）。
func SetExplicitCompatibilityKindOverrides(overrides map[string]string) {
	next := make(map[string]string, len(overrides))
	for key, kind := range overrides {
		normalized := strings.ToLower(strings.TrimSpace(kind))
		if normalized == "" {
			continue
		}
		if _, valid := validCompatibilityKinds[normalized]; !valid {
			warnInvalidCompatibilityKindOnce(normalized)
			continue
		}
		next[strings.ToLower(strings.TrimSpace(key))] = normalized
	}
	explicitKindMu.Lock()
	explicitKindByChannel = next
	explicitKindMu.Unlock()
}

func explicitCompatibilityKind(baseURL, modelID string) (string, bool) {
	explicitKindMu.RLock()
	defer explicitKindMu.RUnlock()
	if len(explicitKindByChannel) == 0 {
		return "", false
	}
	key := strings.ToLower(strings.TrimSpace(baseURL)) + "\n" + strings.ToLower(strings.TrimSpace(modelID))
	kind, ok := explicitKindByChannel[key]
	return kind, ok
}

// warnInvalidCompatibilityKindOnce 对同一个非法值只告警一次，避免逐请求刷日志。
func warnInvalidCompatibilityKindOnce(kind string) {
	invalidKindWarnMu.Lock()
	defer invalidKindWarnMu.Unlock()
	if _, seen := invalidKindWarned[kind]; seen {
		return
	}
	invalidKindWarned[kind] = struct{}{}
	logger.Warnf("模型适配器 compatibilityKind 非法值 %q，回落为自动匹配（合法值：openai、copilot、deepseek、xai、kimi、openrouter、siliconflow、zhipu、qwen、mimo、minimax、stepfun）", kind)
}

func classifyProviderCompatibility(baseURL, modelID string) ProviderCompatibility {
	base := strings.ToLower(strings.TrimSpace(baseURL))
	model := strings.ToLower(strings.TrimSpace(modelID))
	// prompt_cache_key 默认对所有 provider 开启。绝大多数 OpenAI 兼容服务
	// （OpenAI 官方、xAI Grok、智谱 GLM、通义 Qwen、月之暗面 Kimi、DeepSeek 及各类第三方中转）
	// 要么原生支持 prompt_cache_key，要么会忽略未知字段，因此默认发送，让 provider 按
	// conversation 复用前缀缓存，显著提升缓存命中率。仅对已知会因未知字段报错的 provider 显式关闭。
	policy := ProviderCompatibility{PromptCacheKey: true}
	// 用户显式指定的 compatibilityKind 优先于字符串信号匹配：上游改 URL 前缀或模型改名
	// 不再影响分类。非法值记 warning 后回落自动匹配。
	if kind, ok := explicitCompatibilityKind(baseURL, modelID); ok {
		if _, valid := validCompatibilityKinds[kind]; valid {
			applyCompatibilityKindPolicy(&policy, kind, base, model)
			policy.AllowResponsesEOFFallback = !isOfficialOpenAIBaseURL(baseURL)
			policy.ThinkingDisableKind = compatibilityThinkingDisableKind(policy.Kind, modelID)
			policy.MaxTokensField = resolveOpenAIMaxTokensField(policy.Kind, modelID)
			return policy
		}
		warnInvalidCompatibilityKindOnce(kind)
	}
	signal := base + " " + model
	switch {
	case isOfficialOpenAIBaseURL(baseURL):
		// OpenAI 官方端点，继承默认 PromptCacheKey=true。
	case strings.Contains(signal, "githubcopilot") || strings.Contains(signal, "copilot"):
		applyCompatibilityKindPolicy(&policy, "copilot", base, model)
	case strings.Contains(signal, "deepseek"):
		applyCompatibilityKindPolicy(&policy, "deepseek", base, model)
	case strings.Contains(signal, "api.x.ai") || strings.Contains(model, "grok"):
		applyCompatibilityKindPolicy(&policy, "xai", base, model)
	case strings.Contains(signal, "kimi") || strings.Contains(signal, "moonshot"):
		applyCompatibilityKindPolicy(&policy, "kimi", base, model)
	case strings.Contains(signal, "openrouter"):
		applyCompatibilityKindPolicy(&policy, "openrouter", base, model)
	case strings.Contains(signal, "siliconflow"):
		applyCompatibilityKindPolicy(&policy, "siliconflow", base, model)
	case strings.Contains(signal, "bigmodel") || strings.Contains(signal, "z.ai") || strings.Contains(signal, "zhipu") || (strings.Contains(model, "glm") && isZhipuOfficialBaseURL(base)):
		applyCompatibilityKindPolicy(&policy, "zhipu", base, model)
	case strings.Contains(signal, "dashscope") || strings.Contains(signal, "qwen") || strings.Contains(signal, "aliyun") || strings.Contains(signal, "bailian"):
		applyCompatibilityKindPolicy(&policy, "qwen", base, model)
	case strings.Contains(signal, "xiaomimimo") || strings.Contains(signal, "mimo"):
		applyCompatibilityKindPolicy(&policy, "mimo", base, model)
	case strings.Contains(signal, "minimax"):
		applyCompatibilityKindPolicy(&policy, "minimax", base, model)
	case strings.Contains(signal, "stepfun") || strings.Contains(signal, "step-"):
		applyCompatibilityKindPolicy(&policy, "stepfun", base, model)
	}
	// Native OpenAI Responses supplies response.completed/incomplete. Preserve a
	// narrow [DONE]-only fallback for non-official compatibility relays, which
	// historically closed without native terminal envelopes.
	policy.AllowResponsesEOFFallback = !isOfficialOpenAIBaseURL(baseURL)
	policy.ThinkingDisableKind = compatibilityThinkingDisableKind(policy.Kind, modelID)
	policy.MaxTokensField = resolveOpenAIMaxTokensField(policy.Kind, modelID)
	return policy
}

// applyCompatibilityKindPolicy 按 kind 填充对应的请求整形策略。
// 自动匹配与用户显式指定的 compatibilityKind 共用本函数，保证两条路径行为一致。
func applyCompatibilityKindPolicy(policy *ProviderCompatibility, kind, base, model string) {
	switch kind {
	case "copilot":
		// Copilot 会因未知字段报错，显式关闭 prompt_cache_key。
		policy.Kind = "copilot"
		policy.PromptCacheKey = false
		policy.StripPrivateFields = true
	case "deepseek":
		policy.Kind = "deepseek"
		policy.StripPrivateFields = true
	case "xai":
		policy.Kind = "xai"
		policy.StripPrivateFields = true
		policy.FilterResponsesTools = true
		policy.DropResponsesFields = true
		policy.DropGrok45Sampling = strings.Contains(model, "grok-4.5")
	default:
		// kimi 继承默认 PromptCacheKey=true（api.kimi.com/coding 端点原生支持）；
		// openrouter/siliconflow/zhipu/qwen/mimo/minimax/stepfun 仅需标记 Kind；
		// "openai" 表示强制默认 OpenAI 兼容策略，Kind 保持为空。
		policy.Kind = kind
		if kind == "openai" {
			policy.Kind = ""
		}
	}
}

// resolveOpenAIMaxTokensField 决定 Chat Completions 输出 token 上限字段名。
// 优先序：已知 provider kind（copilot/deepseek/xai/kimi(moonshot)/zhipu/
// openrouter/siliconflow/qwen/mimo/minimax/stepfun 等，含用户显式指定的
// compatibilityKind）固定发传统 max_tokens——这些非 OpenAI 官方后端不认识
// max_completion_tokens，因此 kind 优先于模型名推断；仅当无任何 kind 信号
// （OpenAI 官方端点或未知中转）时按模型名推断：o 系（^o\d）/gpt-5 系列发
// max_completion_tokens，其余发 max_tokens。本函数是 (kind, modelID) 的纯
// 函数，同一 adapter 配置恒定映射到同一字段，满足 prefix-cache 确定性约束。
func resolveOpenAIMaxTokensField(kind, modelID string) string {
	if kind != "" {
		return maxTokensFieldLegacy
	}
	model := strings.ToLower(strings.TrimSpace(modelID))
	if openAIReasoningModelMaxCompletionPattern.MatchString(model) {
		return maxTokensFieldCompletion
	}
	return maxTokensFieldLegacy
}

func isOfficialOpenAIBaseURL(baseURL string) bool {
	host := modelchannel.URLHostForProtocol(baseURL)
	if host == "api.openai.com" {
		return true
	}
	if host != "chatgpt.com" {
		return false
	}
	path := strings.ToLower(strings.TrimRight(modelchannel.URLPathForProtocol(baseURL), "/"))
	return strings.HasPrefix(path, "/backend-api/codex")
}

func compatibilityThinkingDisableKind(kind, modelID string) string {
	switch kind {
	case "kimi":
		if isKimiK27CodeModel(modelID) {
			return ""
		}
		return "thinking_type"
	case "openrouter":
		return "reasoning_object_none"
	case "siliconflow", "qwen":
		return "enable_thinking"
	case "deepseek", "zhipu", "mimo", "minimax":
		return "thinking_type"
	case "stepfun":
		if stepFunModelSupportsReasoningEffort(modelID) {
			return "reasoning_none"
		}
		return "thinking_type"
	}
	if openAIModelSupportsReasoningNone(strings.ToLower(strings.TrimSpace(modelID))) {
		return "reasoning_none"
	}
	return ""
}

func providerPromptCacheKeyAllowed(baseURL, modelID string) bool {
	return classifyProviderCompatibility(baseURL, modelID).PromptCacheKey
}

// isOpenAIChannelClaudeModel 判断一个 type=openai 的渠道是否实际承载 claude 模型。
//
// 用于运行时把 claude 模型从 OpenAI 协议自动升级到 Anthropic 原生协议：claude 的前缀
// 缓存依赖 cache_control 断点（Anthropic /v1/messages 协议），而 OpenAI 的 prompt_cache_key
// 对 claude 无效。升级后走 AnthropicAdapter，请求发往 {baseURL}/messages 并带 cache_control 断点。
//
// 边界：modelID 为 meta alias（如 "auto"）的渠道不在请求时确定真实模型（由中转路由），
// 无法可靠判断是否是 claude，因此不升级——漏判是安全的（继续走 openai 协议不会出错）。
// 若希望这类渠道也走 anthropic 协议，应把 modelID 显式设为 claude-* 前缀。
func isOpenAIChannelClaudeModel(providerType, modelID string) bool {
	if strings.ToLower(strings.TrimSpace(providerType)) != "openai" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelID)), "claude")
}

// upgradeOpenAIClaudeToAnthropic 把 type=openai 且承载 claude 模型的渠道运行时升级到
// Anthropic 原生协议（修改 resolved 的 Provider/ProtocolGroup 等）。返回 true 表示发生了升级。
// protocolMode=fixed 表示用户明确锁定协议（逃生口），此时跳过升级。
// 升级失败（如中转商不支持 /v1/messages）时，router.streamChannel 会降级回 openai。
func upgradeOpenAIClaudeToAnthropic(resolved *StreamRequest) bool {
	if resolved == nil {
		return false
	}
	if !isOpenAIChannelClaudeModel(resolved.Provider, resolved.ProviderModelID) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(resolved.ProtocolMode), "fixed") {
		return false
	}
	resolved.Provider = "anthropic"
	resolved.ProtocolGroup = "messages"
	resolved.BaseURL = modelchannel.BaseURLWithoutKnownOpenAIEndpoint(resolved.BaseURL)
	if strings.TrimSpace(resolved.AnthropicThinkingEffort) == "" {
		resolved.AnthropicThinkingEffort = normalizeRuntimeThinkingEffort(resolved.ReasoningEffort)
	}
	// OpenAI 专属字段不再适用，避免残留导致误解。
	resolved.OpenAIEndpoint = ""
	resolved.OpenAIRequestGroup = ""
	return true
}

// isZhipuOfficialBaseURL 判断 baseURL 是否指向智谱官方端点。
// 仅官方端点确认支持 thinking 字段；第三方中转站（如 daoxe.com）即使转发 glm 模型
// 也不应注入 thinking 字段，否则上游会返回 400 "Invalid request for the selected model"。
func isZhipuOfficialBaseURL(base string) bool {
	switch {
	case strings.Contains(base, "bigmodel.cn"),
		strings.Contains(base, "bigmodel.com"),
		strings.Contains(base, "z.ai"),
		strings.Contains(base, "zhipuai.ai"),
		strings.Contains(base, "open.bigmodel"),
		strings.Contains(base, "api.z.ai"):
		return true
	}
	return false
}

// sanitizeProviderPrivateFields removes provider-private underscore fields from
// request objects. JSON Schema property names are data, so names below
// "properties" are deliberately preserved.
func sanitizeProviderPrivateFields(value any) {
	sanitizeProviderPrivateFieldsAt(value, false)
}

func sanitizeProviderPrivateFieldsAt(value any, preserveMapKeys bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if !preserveMapKeys && strings.HasPrefix(key, "_") {
				delete(typed, key)
				continue
			}
			childPreservesKeys := preserveMapKeys || key == "properties" || key == "patternProperties"
			sanitizeProviderPrivateFieldsAt(child, childPreservesKeys)
		}
	case []any:
		for _, child := range typed {
			sanitizeProviderPrivateFieldsAt(child, preserveMapKeys)
		}
	}
}

func applyProviderCompatibilitySanitization(body map[string]any, baseURL, modelID string) {
	policy := classifyProviderCompatibility(baseURL, modelID)
	if policy.StripPrivateFields {
		sanitizeProviderPrivateFields(body)
	}
}
