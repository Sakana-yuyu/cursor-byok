package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cursor/internal/modelchannel"
	"cursor/internal/modelcontext"
)

var (
	// ErrInvalidSystemSetting 表示当前模块中的 ErrInvalidSystemSetting 状态值。
	ErrInvalidSystemSetting = errors.New("invalid system setting")
	// ErrChannelNotAvailable 表示当前没有可用模型渠道。
	ErrChannelNotAvailable = errors.New("model channel not available")
)

type ModelPricing struct {
	Input      *float64 `json:"input,omitempty"`
	Output     *float64 `json:"output,omitempty"`
	CacheRead  *float64 `json:"cacheRead,omitempty"`
	CacheWrite *float64 `json:"cacheWrite,omitempty"`
	Currency   string   `json:"currency,omitempty"`
	Known      bool     `json:"known"`
	Source     string   `json:"source,omitempty"`
}

// ModelAdapterConfig 定义了当前模块中的 ModelAdapterConfig 类型。
type ModelAdapterConfig struct {
	ID string `json:"id,omitempty"`
	// DisplayName 表示当前声明中的 DisplayName。
	DisplayName string `json:"displayName"`
	// GroupName 表示模型渠道所属的用户可编辑分组名称。
	GroupName string `json:"groupName,omitempty"`
	// Type 表示当前声明中的 Type。
	Type string `json:"type"`
	// SupplierID 表示品牌供应商标识，仅用于模板和展示。
	SupplierID string `json:"supplierID,omitempty"`
	// ProtocolMode 表示协议选择模式：auto 或 fixed。
	ProtocolMode string `json:"protocolMode,omitempty"`
	// ProtocolGroup 表示最终模型请求协议分组。
	ProtocolGroup string `json:"protocolGroup,omitempty"`
	// BaseURL 表示当前声明中的 BaseURL。
	BaseURL string `json:"baseURL"`
	// APIKey 表示当前声明中的 APIKey。
	APIKey string `json:"apiKey"`
	// TooltipData 表示当前声明中的 TooltipData。
	TooltipData string `json:"tooltipData"`
	// ModelID 表示当前声明中的 ModelID。
	ModelID string `json:"modelID"`
	// ReasoningEffort 表示当前声明中的 ReasoningEffort。
	ReasoningEffort string `json:"reasoningEffort"`
	// OpenAIEndpoint 表示 OpenAI 兼容适配器使用的 API 端点。
	OpenAIEndpoint string `json:"openAIEndpoint"`
	// OpenAIRequestGroup 表示 OpenAI 兼容适配器使用的请求分组。
	OpenAIRequestGroup string `json:"openAIRequestGroup,omitempty"`
	// OpenAIExtraParamsEnabled 表示是否启用 OpenAI 额外请求参数。
	OpenAIExtraParamsEnabled bool `json:"openAIExtraParamsEnabled"`
	// OpenAIExtraParamsJSON 表示 OpenAI 额外请求参数 JSON 对象。
	OpenAIExtraParamsJSON string `json:"openAIExtraParamsJSON"`
	// CustomHeadersEnabled 表示是否启用自定义请求头。
	CustomHeadersEnabled bool `json:"customHeadersEnabled"`
	// CustomHeadersJSON 表示自定义请求头 JSON 对象。
	CustomHeadersJSON string `json:"customHeadersJSON"`
	// AnthropicExtraParamsEnabled 表示是否启用 Anthropic 额外请求参数。
	AnthropicExtraParamsEnabled bool `json:"anthropicExtraParamsEnabled"`
	// AnthropicExtraParamsJSON 表示 Anthropic 额外请求参数 JSON 对象。
	AnthropicExtraParamsJSON string `json:"anthropicExtraParamsJSON"`
	// ContextWindowTokens 表示当前声明中的 ContextWindowTokens。
	ContextWindowTokens int `json:"contextWindowTokens"`
	// MaxCompletionTokens 表示当前声明中的 MaxCompletionTokens。
	MaxCompletionTokens int `json:"maxCompletionTokens"`
	// AnthropicMaxTokens 表示当前声明中的 AnthropicMaxTokens。
	AnthropicMaxTokens int `json:"anthropicMaxTokens"`
	// AnthropicThinkingEffort 表示 Anthropic adaptive thinking 的 output_config.effort。
	AnthropicThinkingEffort string `json:"anthropicThinkingEffort,omitempty"`
	// ThinkingBudgetTokens 表示当前声明中的 ThinkingBudgetTokens。
	ThinkingBudgetTokens int `json:"thinkingBudgetTokens"`
	// Pricing 表示该模型的可选明确价格。
	Pricing *ModelPricing `json:"pricing,omitempty"`
	// FastMode 表示 OpenAI/GPT 是否请求 priority service tier。
	FastMode bool `json:"fastMode,omitempty"`
	// OpenAIServiceTier 表示显式 OpenAI service tier。
	OpenAIServiceTier string `json:"openAIServiceTier,omitempty"`
}

// RuntimeConfigSnapshot 定义了当前模块中的 RuntimeConfigSnapshot 类型。
type RuntimeConfigSnapshot struct {
	// ObservabilityLogEnabled 表示当前声明中的 ObservabilityLogEnabled。
	ObservabilityLogEnabled bool
	// ProviderStreamIdleTimeout 表示 provider 流式响应无有效内容时的空闲超时，单位秒。
	ProviderStreamIdleTimeout int
	// ModelAdapters 表示当前声明中的 ModelAdapters。
	ModelAdapters []ModelAdapterConfig
}

// NormalizeModelAdapterConfigs 用于处理与 NormalizeModelAdapterConfigs 相关的逻辑。
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
			DisplayName:  strings.TrimSpace(item.DisplayName),
			GroupName:    strings.TrimSpace(item.GroupName),
			Type:         nextType,
			SupplierID:   strings.TrimSpace(item.SupplierID),
			ProtocolMode: protocolMode,

			ProtocolGroup:        protocolGroup,
			BaseURL:              baseURL,
			APIKey:               strings.TrimSpace(item.APIKey),
			TooltipData:          strings.TrimSpace(item.TooltipData),
			ModelID:              strings.TrimSpace(item.ModelID),
			ReasoningEffort:      normalizeReasoningEffort(item.ReasoningEffort),
			OpenAIEndpoint:       openAIEndpoint,
			OpenAIRequestGroup:   modelchannel.NormalizeOpenAIRequestGroup(nextType, openAIEndpoint, protocolGroup),
			ContextWindowTokens:  modelcontext.Resolve(item.ModelID, normalizeMaxCompletionTokens(item.ContextWindowTokens)),
			MaxCompletionTokens:  normalizeMaxCompletionTokens(item.MaxCompletionTokens),
			AnthropicMaxTokens:   normalizeMaxCompletionTokens(item.AnthropicMaxTokens),
			ThinkingBudgetTokens: normalizeMaxCompletionTokens(item.ThinkingBudgetTokens),
			Pricing:              item.Pricing,
			FastMode:             item.FastMode,
			OpenAIServiceTier:    strings.TrimSpace(item.OpenAIServiceTier),
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
			return nil, errors.New("模型适配器 displayName 不能为空")
		case next.Type == "":
			return nil, errors.New("模型适配器 type 仅支持 openai、anthropic 或 gemini")
		case next.APIKey == "":
			return nil, errors.New("模型适配器 apiKey 不能为空")
		case next.TooltipData == "":
			return nil, errors.New("模型适配器 tooltipData 不能为空")
		case next.ModelID == "":
			return nil, errors.New("模型适配器 modelID 不能为空")
		case next.ProtocolMode == "":
			return nil, errors.New("模型适配器 protocolMode 仅支持 auto 或 fixed")
		case next.ProtocolGroup == "":
			return nil, errors.New("模型适配器 protocolGroup 与 provider 不匹配")
		case next.Type == "openai" && next.ReasoningEffort == "":
			return nil, errors.New("模型适配器 reasoningEffort 仅支持 low、medium、high、xhigh、max")
		case next.Type == "gemini" && next.ReasoningEffort == "":
			return nil, errors.New("模型适配器 reasoningEffort 仅支持 low、medium、high、xhigh、max")
		case next.Type == "openai" && next.OpenAIEndpoint == "":
			return nil, errors.New("模型适配器 openAIEndpoint 仅支持 /v1/responses 或 /v1/chat/completions")
		case next.Type == "openai" && next.OpenAIRequestGroup == "":
			return nil, errors.New("模型适配器 openAIRequestGroup 仅支持 responses、chat_completions、chat_completions_compat")
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
			return nil, errors.New("模型适配器 anthropicThinkingEffort 仅支持 low、medium、high、xhigh、max")
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
		return fmt.Errorf("模型适配器 %s 不能为空", fieldName)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return fmt.Errorf("模型适配器 %s 必须是合法 JSON 对象", fieldName)
	}
	if parsed == nil {
		return fmt.Errorf("模型适配器 %s 必须是 JSON 对象", fieldName)
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
		return errors.New("模型适配器 customHeadersJSON 的值必须是字符串")
	}
	for key := range parsed {
		if strings.TrimSpace(key) == "" {
			return errors.New("模型适配器 customHeadersJSON 的请求头名称不能为空")
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

func normalizeMaxCompletionTokens(value int) int {
	if value <= 0 {
		return 0
	}
	return value
}

// normalizeModelAdapterType 用于处理与 normalizeModelAdapterType 相关的逻辑。
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

// ResolvedChannel 表示当前选中的模型渠道。
type ResolvedChannel struct {
	// ID 表示当前声明中的 ID。
	ID string
	// Name 表示当前声明中的 Name。
	Name string
	// GroupName 表示当前声明中的 GroupName。
	GroupName string
	// Provider 表示当前声明中的 Provider。
	Provider string
	// ProtocolMode 表示协议选择模式。
	ProtocolMode string
	// ProtocolGroup 表示最终模型请求协议分组。
	ProtocolGroup string
	// BaseURL 表示当前声明中的 BaseURL。
	BaseURL string
	// APIKey 表示当前声明中的 APIKey。
	APIKey string
	// Model 表示当前声明中的 Model。
	Model string
	// ContextWindowTokens 表示当前声明中的 ContextWindowTokens。
	ContextWindowTokens int
	// MaxTokens 表示当前声明中的 MaxTokens。
	MaxTokens int
	// ReasoningEffort 表示当前声明中的 ReasoningEffort。
	ReasoningEffort string
	// OpenAIEndpoint 表示 OpenAI 兼容适配器使用的 API 端点。
	OpenAIEndpoint string
	// OpenAIRequestGroup 表示 OpenAI 兼容适配器使用的请求分组。
	OpenAIRequestGroup string
	// OpenAIExtraParamsEnabled 表示是否启用 OpenAI 额外请求参数。
	OpenAIExtraParamsEnabled bool
	// OpenAIExtraParamsJSON 表示 OpenAI 额外请求参数 JSON 对象。
	OpenAIExtraParamsJSON string
	// CustomHeadersEnabled 表示是否启用自定义请求头。
	CustomHeadersEnabled bool
	// CustomHeadersJSON 表示自定义请求头 JSON 对象。
	CustomHeadersJSON string
	// AnthropicExtraParamsEnabled 表示是否启用 Anthropic 额外请求参数。
	AnthropicExtraParamsEnabled bool
	// AnthropicExtraParamsJSON 表示 Anthropic 额外请求参数 JSON 对象。
	AnthropicExtraParamsJSON string
	// AnthropicMaxTokens 表示当前声明中的 AnthropicMaxTokens。
	AnthropicMaxTokens int
	// AnthropicThinkingEffort 表示 Anthropic adaptive thinking 的 output_config.effort。
	AnthropicThinkingEffort string
	// FastMode 表示 OpenAI/GPT 是否请求 priority service tier。
	FastMode bool
	// OpenAIServiceTier 表示显式 OpenAI service tier。
	OpenAIServiceTier string
	// ThinkingBudgetTokens 表示当前声明中的 ThinkingBudgetTokens。
	ThinkingBudgetTokens int
}
