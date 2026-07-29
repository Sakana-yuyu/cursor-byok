package config

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"cursor/internal/i18n"
	"cursor/internal/modelchannel"
	"cursor/internal/modelcontext"
	legacyruntime "cursor/internal/runtime"
)

const (
	DefaultBackendListenAddr                = "127.0.0.1:18090"
	DefaultProxyListenAddr                  = "127.0.0.1:18080"
	DefaultFrontendBaseURL                  = "http://127.0.0.1"
	DefaultRoutingMode                      = "local"
	DefaultProviderStreamIdleTimeoutSeconds = 90
	MinProviderStreamIdleTimeoutSeconds     = 30
	// DefaultTurnStaleTimeoutSeconds 表示一轮回合进入「等待外部（工具/交互结果）」后，
	// 在无任何进展时由 turn-staleness 看门狗触发自救的默认阈值，单位秒。
	DefaultTurnStaleTimeoutSeconds = 120
	// MinTurnStaleTimeoutSeconds 表示 turn-staleness 看门狗允许的最小阈值，单位秒。
	MinTurnStaleTimeoutSeconds = 30
	// TurnStaleGraceSeconds 表示阶段一（重对齐 append 序列）后给真实工具结果的宽限期，单位秒。
	TurnStaleGraceSeconds = 60
	// DefaultLocalResponseCacheTTLSeconds 表示本地响应缓存条目的默认存活时长。
	DefaultLocalResponseCacheTTLSeconds = 900
	// DefaultLocalResponseCacheMaxEntries 表示本地响应缓存的默认最大条目数。
	DefaultLocalResponseCacheMaxEntries = 256
	// DefaultAutoMatchContextWindow 表示是否在启动时/手动触发时自动为所有模型适配器
	// 配对正确的上下文窗口（目录命中则覆盖，目录无则探测 provider /models 回填）。
	DefaultAutoMatchContextWindow = true
)

type ModelPricing = legacyruntime.ModelPricing

type ModelAdapterConfig struct {
	ID          string `json:"id,omitempty" yaml:"-"`
	DisplayName string `json:"displayName" yaml:"displayName"`
	GroupName   string `json:"groupName,omitempty" yaml:"groupName,omitempty"`
	Type        string `json:"type" yaml:"type"`
	// SupplierID 是品牌供应商标识，仅用于模板、展示和供应商专用能力；协议仍由 Type 决定。
	SupplierID                  string        `json:"supplierID,omitempty" yaml:"supplierID,omitempty"`
	ProtocolMode                string        `json:"protocolMode,omitempty" yaml:"protocolMode,omitempty"`
	ProtocolGroup               string        `json:"protocolGroup,omitempty" yaml:"protocolGroup,omitempty"`
	BaseURL                     string        `json:"baseURL" yaml:"baseURL"`
	APIKey                      string        `json:"apiKey" yaml:"apiKey"`
	TooltipData                 string        `json:"tooltipData" yaml:"tooltipData"`
	ModelID                     string        `json:"modelID" yaml:"modelID"`
	ReasoningEffort             string        `json:"reasoningEffort" yaml:"reasoningEffort"`
	OpenAIEndpoint              string        `json:"openAIEndpoint" yaml:"openAIEndpoint"`
	OpenAIRequestGroup          string        `json:"openAIRequestGroup,omitempty" yaml:"openAIRequestGroup,omitempty"`
	OpenAIExtraParamsEnabled    bool          `json:"openAIExtraParamsEnabled" yaml:"openAIExtraParamsEnabled"`
	OpenAIExtraParamsJSON       string        `json:"openAIExtraParamsJSON" yaml:"openAIExtraParamsJSON"`
	CustomHeadersEnabled        bool          `json:"customHeadersEnabled" yaml:"customHeadersEnabled"`
	CustomHeadersJSON           string        `json:"customHeadersJSON" yaml:"customHeadersJSON"`
	AnthropicExtraParamsEnabled bool          `json:"anthropicExtraParamsEnabled" yaml:"anthropicExtraParamsEnabled"`
	AnthropicExtraParamsJSON    string        `json:"anthropicExtraParamsJSON" yaml:"anthropicExtraParamsJSON"`
	ContextWindowTokens         int           `json:"contextWindowTokens" yaml:"contextWindowTokens"`
	MaxCompletionTokens         int           `json:"maxCompletionTokens" yaml:"maxCompletionTokens"`
	AnthropicMaxTokens          int           `json:"anthropicMaxTokens" yaml:"anthropicMaxTokens"`
	AnthropicThinkingEffort     string        `json:"anthropicThinkingEffort,omitempty" yaml:"anthropicThinkingEffort,omitempty"`
	ThinkingBudgetTokens        int           `json:"thinkingBudgetTokens" yaml:"thinkingBudgetTokens"`
	Pricing                     *ModelPricing `json:"pricing,omitempty" yaml:"pricing,omitempty"`
	FastMode                    bool          `json:"fastMode,omitempty" yaml:"fastMode,omitempty"`
	OpenAIServiceTier           string        `json:"openAIServiceTier,omitempty" yaml:"openAIServiceTier,omitempty"`
	// 余额查询（可配置兜底）：全部可选，零值即「未启用」。
	// 具名 provider 未命中时，若 BalanceQueryURL 非空，则用它发一次 GET 并按点分路径取值。
	// URL/Headers 支持 {{apiKey}}、{{baseUrl}}、{{accessToken}}、{{userId}} 占位符替换。
	BalanceQueryURL     string            `json:"balanceQueryURL,omitempty" yaml:"balanceQueryURL,omitempty"`
	BalanceQueryField   string            `json:"balanceQueryField,omitempty" yaml:"balanceQueryField,omitempty"`
	BalanceQueryHeaders map[string]string `json:"balanceQueryHeaders,omitempty" yaml:"balanceQueryHeaders,omitempty"`
	// BalanceProfile 对齐 cc-switch 用量模板：general | newapi | token_plan | custom | official。
	// auto 仅为旧配置兼容值：后端按 baseURL / 字段推断；official 走具名官方余额接口。
	BalanceProfile string `json:"balanceProfile,omitempty" yaml:"balanceProfile,omitempty"`
	// New API 等站点的 Web 访问令牌（与渠道 sk 不同）。
	BalanceAccessToken string `json:"balanceAccessToken,omitempty" yaml:"balanceAccessToken,omitempty"`
	// New API 的 New-Api-User 用户 ID。
	BalanceUserID string `json:"balanceUserID,omitempty" yaml:"balanceUserID,omitempty"`
	// Token Plan 显式供应商：kimi | zhipu | zhipu_team | minimax | zenmux | volcengine。
	// 空则按 baseURL 自动检测（zhipu_team 无法自动区分，须显式指定）。
	BalanceCodingPlanProvider string `json:"balanceCodingPlanProvider,omitempty" yaml:"balanceCodingPlanProvider,omitempty"`
}

type RoutingConfig struct {
	Mode string `json:"mode" yaml:"mode"`
}

type HomeMetricsConfig struct {
	IncludeCacheWriteInHitRate bool `json:"includeCacheWriteInHitRate" yaml:"includeCacheWriteInHitRate"`
}

// LocalResponseCacheConfig 控制本地（进程内）精确匹配 LLM 响应缓存。
// 默认（零值）为关闭：Enabled 为 false 时 provider 链路与今日完全一致。
type LocalResponseCacheConfig struct {
	// Enabled 表示是否启用本地响应缓存；默认 false（关闭）。
	Enabled bool `json:"enabled" yaml:"enabled"`
	// TTLSeconds 表示缓存条目的存活秒数；<=0 时回退默认值。
	TTLSeconds int `json:"ttlSeconds,omitempty" yaml:"ttlSeconds,omitempty"`
	// MaxEntries 表示缓存最大条目数；<=0 时回退默认值。
	MaxEntries int `json:"maxEntries,omitempty" yaml:"maxEntries,omitempty"`
}

type Config struct {
	Log                       bool                     `json:"log" yaml:"log"`
	ProviderStreamIdleTimeout int                      `json:"providerStreamIdleTimeout" yaml:"providerStreamIdleTimeout"`
	TurnStaleTimeout          int                      `json:"turnStaleTimeout" yaml:"turnStaleTimeout"`
	AutoMatchContextWindow    bool                     `json:"autoMatchContextWindow" yaml:"autoMatchContextWindow"`
	BackendListenAddr         string                   `json:"backendListenAddr" yaml:"backendListenAddr"`
	ProxyListenAddr           string                   `json:"proxyListenAddr" yaml:"proxyListenAddr"`
	ModelAdapters             []ModelAdapterConfig     `json:"modelAdapters" yaml:"modelAdapters"`
	Routing                   RoutingConfig            `json:"routing" yaml:"routing"`
	HomeMetrics               HomeMetricsConfig        `json:"homeMetrics" yaml:"homeMetrics"`
	LocalResponseCache        LocalResponseCacheConfig `json:"localResponseCache" yaml:"localResponseCache"`
	LastAgentModelHash        string                   `json:"lastAgentModelHash" yaml:"lastAgentModelHash"`
}

func DefaultConfig() Config {
	return Config{
		Log:                       false,
		ProviderStreamIdleTimeout: DefaultProviderStreamIdleTimeoutSeconds,
		TurnStaleTimeout:          DefaultTurnStaleTimeoutSeconds,
		AutoMatchContextWindow:    DefaultAutoMatchContextWindow,
		BackendListenAddr:         DefaultBackendListenAddr,
		ProxyListenAddr:           DefaultProxyListenAddr,
		ModelAdapters:             []ModelAdapterConfig{},
		Routing: RoutingConfig{
			Mode: DefaultRoutingMode,
		},
	}
}

func NormalizeConfig(input Config) (Config, error) {
	output := DefaultConfig()
	output.Log = input.Log
	output.ProviderStreamIdleTimeout = normalizeProviderStreamIdleTimeout(input.ProviderStreamIdleTimeout)
	output.TurnStaleTimeout = normalizeTurnStaleTimeout(input.TurnStaleTimeout)
	output.AutoMatchContextWindow = normalizeAutoMatchContextWindow(input.AutoMatchContextWindow)
	backendListenAddr, err := normalizeListenAddr(input.BackendListenAddr, DefaultBackendListenAddr, "backendListenAddr")
	if err != nil {
		return Config{}, err
	}
	proxyListenAddr, err := normalizeListenAddr(input.ProxyListenAddr, DefaultProxyListenAddr, "proxyListenAddr")
	if err != nil {
		return Config{}, err
	}
	output.BackendListenAddr = backendListenAddr
	output.ProxyListenAddr = proxyListenAddr
	output.HomeMetrics.IncludeCacheWriteInHitRate = input.HomeMetrics.IncludeCacheWriteInHitRate
	output.LocalResponseCache = normalizeLocalResponseCache(input.LocalResponseCache)
	output.LastAgentModelHash = strings.TrimSpace(input.LastAgentModelHash)
	output.Routing.Mode = normalizeRoutingMode(input.Routing.Mode)
	if output.Routing.Mode == "" {
		output.Routing.Mode = DefaultRoutingMode
	}
	adapters, err := NormalizeModelAdapterConfigs(input.ModelAdapters)
	if err != nil {
		return Config{}, err
	}
	output.ModelAdapters = adapters
	return output, nil
}

func NormalizeModelAdapterConfigs(input []ModelAdapterConfig) ([]ModelAdapterConfig, error) {
	if len(input) == 0 {
		return []ModelAdapterConfig{}, nil
	}

	normalized := make([]ModelAdapterConfig, 0, len(input))
	channelIndexByID := make(map[string]int, len(input))
	for _, item := range input {
		baseURL, err := modelchannel.NormalizeBaseURL(item.BaseURL)
		if err != nil {
			return nil, err
		}
		nextType := normalizeModelAdapterType(item.Type)
		protocolMode := modelchannel.NormalizeProtocolMode(item.ProtocolMode)
		protocolGroup := modelchannel.ResolveProtocolGroup(protocolMode, nextType, item.ModelID, baseURL, item.OpenAIEndpoint, firstNonEmptyProtocolGroup(item.ProtocolGroup, item.OpenAIRequestGroup))
		openAIEndpoint := modelchannel.NormalizeOpenAIEndpoint(nextType, item.OpenAIEndpoint)
		if nextType == "openai" && openAIEndpoint != modelchannel.OpenAIEndpointCustom {
			openAIEndpoint = modelchannel.OpenAIEndpointForProtocolGroup(protocolGroup, openAIEndpoint)
		}
		next := ModelAdapterConfig{
			DisplayName:   strings.TrimSpace(item.DisplayName),
			GroupName:     strings.TrimSpace(item.GroupName),
			Type:          nextType,
			SupplierID:    strings.TrimSpace(item.SupplierID),
			ProtocolMode:  protocolMode,
			ProtocolGroup: protocolGroup,

			BaseURL:                   baseURL,
			APIKey:                    strings.TrimSpace(item.APIKey),
			TooltipData:               strings.TrimSpace(item.TooltipData),
			ModelID:                   strings.TrimSpace(item.ModelID),
			ReasoningEffort:           normalizeReasoningEffort(item.ReasoningEffort),
			OpenAIEndpoint:            openAIEndpoint,
			OpenAIRequestGroup:        modelchannel.NormalizeOpenAIRequestGroup(nextType, openAIEndpoint, protocolGroup),
			ContextWindowTokens:       modelcontext.Resolve(item.ModelID, normalizeMaxCompletionTokens(item.ContextWindowTokens)),
			MaxCompletionTokens:       normalizeMaxCompletionTokens(item.MaxCompletionTokens),
			AnthropicMaxTokens:        normalizeMaxCompletionTokens(item.AnthropicMaxTokens),
			ThinkingBudgetTokens:      normalizeMaxCompletionTokens(item.ThinkingBudgetTokens),
			Pricing:                   item.Pricing,
			FastMode:                  item.FastMode,
			OpenAIServiceTier:         strings.TrimSpace(item.OpenAIServiceTier),
			BalanceQueryURL:           strings.TrimSpace(item.BalanceQueryURL),
			BalanceQueryField:         strings.TrimSpace(item.BalanceQueryField),
			BalanceQueryHeaders:       item.BalanceQueryHeaders,
			BalanceProfile:            strings.ToLower(strings.TrimSpace(item.BalanceProfile)),
			BalanceAccessToken:        strings.TrimSpace(item.BalanceAccessToken),
			BalanceUserID:             strings.TrimSpace(item.BalanceUserID),
			BalanceCodingPlanProvider: strings.ToLower(strings.TrimSpace(item.BalanceCodingPlanProvider)),
		}
		if next.Type == "openai" {
			next.OpenAIExtraParamsEnabled = item.OpenAIExtraParamsEnabled
			next.OpenAIExtraParamsJSON = strings.TrimSpace(item.OpenAIExtraParamsJSON)
		} else if next.Type == "anthropic" {
			next.AnthropicThinkingEffort = normalizeAnthropicThinkingEffort(item.AnthropicThinkingEffort)
			next.AnthropicExtraParamsEnabled = item.AnthropicExtraParamsEnabled
			next.AnthropicExtraParamsJSON = strings.TrimSpace(item.AnthropicExtraParamsJSON)
		}
		next.CustomHeadersEnabled = item.CustomHeadersEnabled
		next.CustomHeadersJSON = strings.TrimSpace(item.CustomHeadersJSON)
		switch {
		case next.DisplayName == "":
			return nil, i18n.NewError("error.model_adapter.display_name_required", i18n.CodeInvalidModelAdapter, "模型适配器 displayName 不能为空")
		case next.Type == "":
			return nil, i18n.NewError("error.model_adapter.type_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 type 仅支持 openai、anthropic 或 gemini")
		case next.APIKey == "":
			return nil, i18n.NewError("error.model_adapter.api_key_required", i18n.CodeInvalidModelAdapter, "模型适配器 apiKey 不能为空")
		case next.TooltipData == "":
			return nil, i18n.NewError("error.model_adapter.tooltip_required", i18n.CodeInvalidModelAdapter, "模型适配器 tooltipData 不能为空")
		case next.ModelID == "":
			return nil, i18n.NewError("error.model_adapter.model_id_required", i18n.CodeInvalidModelAdapter, "模型适配器 modelID 不能为空")
		case next.ProtocolMode == "":
			return nil, i18n.NewError("error.model_adapter.protocol_mode_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 protocolMode 仅支持 auto 或 fixed")
		case next.ProtocolGroup == "":
			return nil, i18n.NewError("error.model_adapter.protocol_group_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 protocolGroup 与 provider 不匹配")
		case next.Type == "openai" && next.ReasoningEffort == "":
			return nil, i18n.NewError("error.model_adapter.reasoning_effort_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 reasoningEffort 仅支持 low、medium、high、xhigh、max")
		case next.Type == "gemini" && next.ReasoningEffort == "":
			return nil, i18n.NewError("error.model_adapter.reasoning_effort_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 reasoningEffort 仅支持 low、medium、high、xhigh、max")
		case next.Type == "openai" && next.OpenAIEndpoint == "":
			return nil, i18n.NewError("error.model_adapter.endpoint_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 openAIEndpoint 仅支持 /v1/responses、/v1/chat/completions 或 /custom（自定义路径）")
		case next.Type == "openai" && next.OpenAIRequestGroup == "":
			return nil, i18n.NewError("error.model_adapter.request_group_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 openAIRequestGroup 仅支持 responses、chat_completions、chat_completions_compat")
		case next.Type == "openai" && next.OpenAIExtraParamsEnabled:
			if err := validateJSONMap(next.OpenAIExtraParamsJSON, "openAIExtraParamsJSON"); err != nil {
				return nil, err
			}
		case next.CustomHeadersEnabled:
			if err := validateHeadersJSON(next.CustomHeadersJSON); err != nil {
				return nil, err
			}
		case next.Type == "anthropic" && next.AnthropicExtraParamsEnabled:
			if err := validateJSONMap(next.AnthropicExtraParamsJSON, "anthropicExtraParamsJSON"); err != nil {
				return nil, err
			}
		case next.Type == "anthropic" && next.AnthropicThinkingEffort == "":
			return nil, i18n.NewError("error.model_adapter.thinking_effort_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 anthropicThinkingEffort 仅支持 low、medium、high、xhigh、max")
		}
		next.ID = modelchannel.BuildChannelID(next.BaseURL, next.ModelID, next.APIKey, next.DisplayName, next.OpenAIEndpoint)
		if existingIndex, exists := channelIndexByID[next.ID]; exists {
			existing := normalized[existingIndex]
			if existing.GroupName == "" {
				existing.GroupName = next.GroupName
			}
			if existing.TooltipData == "" {
				existing.TooltipData = next.TooltipData
			}
			if existing.ContextWindowTokens <= 0 {
				existing.ContextWindowTokens = next.ContextWindowTokens
			}
			if existing.Pricing == nil {
				existing.Pricing = next.Pricing
			}
			if existing.SupplierID == "" {
				existing.SupplierID = next.SupplierID
			}
			if existing.BalanceQueryURL == "" {
				existing.BalanceQueryURL = next.BalanceQueryURL
			}
			if existing.BalanceQueryField == "" {
				existing.BalanceQueryField = next.BalanceQueryField
			}
			if len(existing.BalanceQueryHeaders) == 0 {
				existing.BalanceQueryHeaders = next.BalanceQueryHeaders
			}
			if existing.BalanceProfile == "" {
				existing.BalanceProfile = next.BalanceProfile
			}
			if existing.BalanceAccessToken == "" {
				existing.BalanceAccessToken = next.BalanceAccessToken
			}
			if existing.BalanceUserID == "" {
				existing.BalanceUserID = next.BalanceUserID
			}
			if existing.BalanceCodingPlanProvider == "" {
				existing.BalanceCodingPlanProvider = next.BalanceCodingPlanProvider
			}
			normalized[existingIndex] = existing
			continue
		}
		channelIndexByID[next.ID] = len(normalized)
		normalized = append(normalized, next)
	}
	return normalized, nil
}

func firstNonEmptyProtocolGroup(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func validateJSONMap(value string, fieldName string) error {
	text := strings.TrimSpace(value)
	if text == "" {
		return i18n.NewError("error.model_adapter.json_required", i18n.CodeInvalidModelAdapter, fmt.Sprintf("模型适配器 %s 不能为空", fieldName))
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return i18n.NewError("error.model_adapter.json_required", i18n.CodeInvalidModelAdapter, fmt.Sprintf("模型适配器 %s 必须是合法 JSON 对象", fieldName))
	}
	if parsed == nil {
		return i18n.NewError("error.model_adapter.json_required", i18n.CodeInvalidModelAdapter, fmt.Sprintf("模型适配器 %s 必须是 JSON 对象", fieldName))
	}
	return nil
}

func validateHeadersJSON(value string) error {
	text := strings.TrimSpace(value)
	if err := validateJSONMap(text, "customHeadersJSON"); err != nil {
		return err
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return i18n.NewError("error.model_adapter.headers_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 customHeadersJSON 的值必须是字符串")
	}
	for key := range parsed {
		if strings.TrimSpace(key) == "" {
			return i18n.NewError("error.model_adapter.headers_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 customHeadersJSON 的请求头名称不能为空")
		}
	}
	return nil
}

func normalizeReasoningEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "medium":
		return "medium"
	case "low", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeAnthropicThinkingEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "xhigh":
		return "xhigh"
	case "low", "medium", "high", "max":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeListenAddr(value string, defaultValue string, fieldName string) (string, error) {
	addr := strings.TrimSpace(value)
	if addr == "" {
		addr = defaultValue
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("%s 必须是 host:port 格式", fieldName)
	}
	if strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("%s host 不能为空", fieldName)
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return "", fmt.Errorf("%s port 必须在 1-65535 之间", fieldName)
	}
	return net.JoinHostPort(host, strconv.Itoa(parsedPort)), nil
}

// normalizeLocalResponseCache 归一化本地响应缓存配置：保留 Enabled（默认 false），
// 对无效的 TTL/MaxEntries 回退到默认值。
func normalizeLocalResponseCache(input LocalResponseCacheConfig) LocalResponseCacheConfig {
	output := input
	if output.TTLSeconds <= 0 {
		output.TTLSeconds = DefaultLocalResponseCacheTTLSeconds
	}
	if output.MaxEntries <= 0 {
		output.MaxEntries = DefaultLocalResponseCacheMaxEntries
	}
	return output
}

func normalizeProviderStreamIdleTimeout(value int) int {
	if value <= 0 {
		return DefaultProviderStreamIdleTimeoutSeconds
	}
	if value < MinProviderStreamIdleTimeoutSeconds {
		return MinProviderStreamIdleTimeoutSeconds
	}
	return value
}

// normalizeTurnStaleTimeout 把 turn-staleness 看门狗阈值约束到合法区间（单位秒）。
// <=0 回退默认值；过小回退最小值，避免过短阈值把正常的长工具调用误判为卡死。
func normalizeTurnStaleTimeout(value int) int {
	if value <= 0 {
		return DefaultTurnStaleTimeoutSeconds
	}
	if value < MinTurnStaleTimeoutSeconds {
		return MinTurnStaleTimeoutSeconds
	}
	return value
}

// normalizeAutoMatchContextWindow 把「自动配对上下文窗口」开关归一为显式 bool。
// YAML 中未出现该键时，Load 得到的字段为零值 false；但语义上「未配置」应回退到默认开启，
// 因此这里无法仅凭 false 区分「显式关闭」与「未配置」。当前实现：直接透传用户值；
// 默认开启由 DefaultConfig 保证（仅全新配置文件会落到默认 true）。
func normalizeAutoMatchContextWindow(value bool) bool {
	return value
}

func normalizeMaxCompletionTokens(value int) int {
	if value <= 0 {
		return 0
	}
	return value
}

func normalizeModelAdapterType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "openai":
		return "openai"
	case "anthropic":
		return "anthropic"
	case "gemini":
		return "gemini"
	default:
		return ""
	}
}

func normalizeRoutingMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "local":
		return "local"
	case "upstream":
		return "upstream"
	default:
		return ""
	}
}
