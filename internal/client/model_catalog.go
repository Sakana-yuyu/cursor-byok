package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"cursor/internal/i18n"
	"cursor/internal/modelchannel"
)

const (
	modelCatalogTimeout       = 15 * time.Second
	modelCatalogMaxBodyBytes  = 4 << 20
	modelCatalogDefaultObject = "model"
)

// ModelCatalogRequest 是拉取模型列表所需的临时连接参数，不会写入配置。
type ModelCatalogRequest struct {
	Type                 string `json:"type"`
	BaseURL              string `json:"baseURL"`
	APIKey               string `json:"apiKey"`
	CustomHeadersEnabled bool   `json:"customHeadersEnabled"`
	CustomHeadersJSON    string `json:"customHeadersJSON"`
	// ForceRefresh 为 true 时绕过进程内 TTL 缓存，强制重新拉取（供 UI 显式刷新使用）。
	ForceRefresh bool `json:"forceRefresh,omitempty"`
}

type ModelPricing struct {
	Input      *float64 `json:"input,omitempty"`
	Output     *float64 `json:"output,omitempty"`
	CacheRead  *float64 `json:"cacheRead,omitempty"`
	CacheWrite *float64 `json:"cacheWrite,omitempty"`
	Currency   string   `json:"currency,omitempty"`
	Known      bool     `json:"known"`
	Source     string   `json:"source,omitempty"`
}

// ModelCatalogItem 是服务端返回的模型摘要。
type ModelCatalogItem struct {
	ID                  string                 `json:"id"`
	Object              string                 `json:"object,omitempty"`
	OwnedBy             string                 `json:"ownedBy,omitempty"`
	ContextWindowTokens int                    `json:"contextWindowTokens,omitempty"`
	Pricing             *ModelPricing          `json:"pricing,omitempty"`
	Capabilities        map[string]interface{} `json:"capabilities,omitempty"`
}

// ModelCatalogResult 是拉取模型列表的结果。
type ModelCatalogResult struct {
	Models []ModelCatalogItem `json:"models"`
}

// FetchModelCatalog 使用当前编辑器中的临时参数拉取模型，不要求先保存模型配置。
func (s *ProxyService) FetchModelCatalog(request ModelCatalogRequest) (ModelCatalogResult, error) {
	endpoint, err := buildModelCatalogURL(request.BaseURL)
	if err != nil {
		return ModelCatalogResult{}, err
	}

	typeName := strings.ToLower(strings.TrimSpace(request.Type))
	if typeName != "openai" && typeName != "anthropic" && typeName != "gemini" {
		return ModelCatalogResult{}, i18n.NewError("error.model_adapter.type_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 type 仅支持 openai、anthropic 或 gemini")
	}
	apiKey := strings.TrimSpace(request.APIKey)
	if apiKey == "" {
		return ModelCatalogResult{}, i18n.NewError("error.model_adapter.api_key_required", i18n.CodeInvalidModelAdapter, "模型适配器 apiKey 不能为空")
	}

	cacheKey := metadataCacheKey(typeName, request.BaseURL, apiKey)
	if request.ForceRefresh {
		s.modelCatalogCache.invalidate(cacheKey)
	} else if cached, ok := s.modelCatalogCache.get(cacheKey); ok {
		return cached, nil
	}

	headers, err := parseModelCatalogHeaders(request)
	if err != nil {
		return ModelCatalogResult{}, err
	}
	if typeName == "anthropic" {
		headers.Set("x-api-key", apiKey)
		headers.Set("anthropic-version", "2023-06-01")
	} else if typeName == "gemini" {
		headers.Set("x-goog-api-key", apiKey)
	} else {
		headers.Set("Authorization", "Bearer "+apiKey)
	}

	ctx, cancel := context.WithTimeout(context.Background(), modelCatalogTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ModelCatalogResult{}, i18n.WrapError("error.model_catalog.request_failed", i18n.CodeModelCatalog, "创建模型列表请求失败", err)
	}
	httpRequest.Header = headers

	client := s.publicClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return ModelCatalogResult{}, i18n.WrapError("error.model_catalog.request_failed", i18n.CodeModelCatalog, "拉取模型列表失败", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, modelCatalogMaxBodyBytes+1))
	if err != nil {
		return ModelCatalogResult{}, i18n.WrapError("error.model_catalog.request_failed", i18n.CodeModelCatalog, "读取模型列表失败", err)
	}
	if len(body) > modelCatalogMaxBodyBytes {
		return ModelCatalogResult{}, i18n.NewError("error.model_catalog.response_invalid", i18n.CodeModelCatalog, fmt.Sprintf("模型列表响应超过 %d MB 限制", modelCatalogMaxBodyBytes/(1<<20)))
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ModelCatalogResult{}, i18n.NewError("error.model_catalog.request_failed", i18n.CodeModelCatalog, fmt.Sprintf("拉取模型列表失败，服务返回 HTTP %d", response.StatusCode))
	}

	models, err := decodeModelCatalog(body)
	if err != nil {
		return ModelCatalogResult{}, err
	}
	models = filterModelCatalogByType(models, typeName)
	result := ModelCatalogResult{Models: models}
	// 仅缓存成功结果；错误路径在上方已直接返回，绝不缓存错误。
	s.modelCatalogCache.set(cacheKey, result)
	return result, nil
}

// filterModelCatalogByType 剔除与适配器类型不匹配的占位/水印模型。
//
// 部分中转站在收到 Anthropic 版本请求（x-api-key + anthropic-version）时，
// 会把非 Anthropic 模型伪装成 claude-* 前缀的占位条目（如 claude-fable-5-dd-<反转名>），
// 但其 owned_by 仍为 openai/xai 等真实厂商。这类模型无法在 /v1/messages 调用，
// 因此对 anthropic 适配器仅保留 owned_by 为空或等于 anthropic 的模型。
func filterModelCatalogByType(models []ModelCatalogItem, typeName string) []ModelCatalogItem {
	if strings.ToLower(strings.TrimSpace(typeName)) != "anthropic" {
		return models
	}
	filtered := make([]ModelCatalogItem, 0, len(models))
	for _, model := range models {
		owner := strings.ToLower(strings.TrimSpace(model.OwnedBy))
		if owner == "" || owner == "anthropic" {
			filtered = append(filtered, model)
		}
	}
	// 全部被过滤掉时说明该服务未按 owned_by 标注厂商，回退为不过滤，避免误伤合法中转站。
	if len(filtered) == 0 {
		return models
	}
	return filtered
}

func buildModelCatalogURL(rawBaseURL string) (string, error) {
	baseURL, err := modelchannel.NormalizeBaseURL(rawBaseURL)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("模型适配器 baseURL 不是合法 URL")
	}
	path := strings.TrimRight(parsed.Path, "/")
	lowerPath := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lowerPath, "/models"):
		// 用户已经填写完整的模型列表地址。
	case strings.HasSuffix(lowerPath, "/chat/completions"), strings.HasSuffix(lowerPath, "/responses"):
		path = path[:strings.LastIndex(path, "/")]
		path = strings.TrimRight(path, "/") + "/models"
	default:
		path += "/models"
	}
	parsed.Path = path
	parsed.RawPath = ""
	return parsed.String(), nil
}

func parseModelCatalogHeaders(request ModelCatalogRequest) (http.Header, error) {
	headers := make(http.Header)
	if !request.CustomHeadersEnabled || strings.TrimSpace(request.CustomHeadersJSON) == "" {
		return headers, nil
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(request.CustomHeadersJSON), &values); err != nil || values == nil {
		return nil, i18n.NewError("error.model_adapter.headers_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 customHeadersJSON 必须是合法 JSON 对象，且值必须是字符串")
	}
	for name, value := range values {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, i18n.NewError("error.model_adapter.headers_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 customHeadersJSON 的请求头名称不能为空")
		}
		headers.Set(name, value)
	}
	return headers, nil
}

func decodeModelCatalog(body []byte) ([]ModelCatalogItem, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, i18n.NewError("error.model_catalog.response_invalid", i18n.CodeModelCatalog, "模型列表响应不是合法 JSON")
	}

	items := make([]ModelCatalogItem, 0)
	appendValue := func(value any) {
		item, ok := modelCatalogItemFromValue(value)
		if ok {
			items = append(items, item)
		}
	}
	switch value := payload.(type) {
	case []any:
		for _, item := range value {
			appendValue(item)
		}
	case map[string]any:
		for _, key := range []string{"data", "models", "items"} {
			if list, ok := value[key].([]any); ok {
				for _, item := range list {
					appendValue(item)
				}
				break
			}
		}
	}
	if len(items) == 0 {
		return nil, i18n.NewError("error.model_catalog.empty", i18n.CodeModelCatalog, "模型列表响应中没有可用模型")
	}

	byID := make(map[string]ModelCatalogItem, len(items))
	for _, item := range items {
		if _, exists := byID[item.ID]; !exists {
			byID[item.ID] = item
		}
	}
	items = items[:0]
	for _, item := range byID {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].ID) < strings.ToLower(items[j].ID) })
	return items, nil
}

func modelCatalogItemFromValue(value any) (ModelCatalogItem, bool) {
	switch item := value.(type) {
	case string:
		id := strings.TrimSpace(item)
		return ModelCatalogItem{ID: id, Object: modelCatalogDefaultObject}, id != ""
	case map[string]any:
		id := firstString(item, "id", "model", "name")
		if id == "" {
			return ModelCatalogItem{}, false
		}
		id = strings.TrimPrefix(id, "models/")
		contextWindowTokens := firstInt(item, "contextWindowTokens", "context_window", "context_window_tokens", "contextLength", "context_length", "inputTokenLimit", "input_token_limit")
		return ModelCatalogItem{
			ID:                  id,
			Object:              firstString(item, "object", "type"),
			OwnedBy:             firstString(item, "owned_by", "ownedBy", "provider"),
			ContextWindowTokens: contextWindowTokens,
			Pricing:             modelPricingFromValue(item),
			Capabilities:        firstMap(item, "capabilities"),
		}, true
	default:
		return ModelCatalogItem{}, false
	}
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstInt(values map[string]any, keys ...string) int {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch number := value.(type) {
		case float64:
			if number > 0 && number == float64(int(number)) {
				return int(number)
			}
		case json.Number:
			parsed, err := number.Int64()
			if err == nil && parsed > 0 {
				return int(parsed)
			}
		case int:
			if number > 0 {
				return number
			}
		}
	}
	return 0
}

func firstMap(values map[string]any, keys ...string) map[string]interface{} {
	for _, key := range keys {
		if value, ok := values[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func firstFloat(values map[string]any, keys ...string) (*float64, bool) {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		var number float64
		switch typed := value.(type) {
		case float64:
			number = typed
		case json.Number:
			parsed, err := typed.Float64()
			if err != nil {
				continue
			}
			number = parsed
		case int:
			number = float64(typed)
		default:
			continue
		}
		if number >= 0 {
			return &number, true
		}
	}
	return nil, false
}

func modelPricingFromValue(values map[string]any) *ModelPricing {
	pricingValues := values
	for _, key := range []string{"pricing", "price"} {
		if nested, ok := values[key].(map[string]any); ok {
			pricingValues = nested
			break
		}
	}
	input, inputOK := firstFloat(pricingValues, "input", "input_price", "inputPrice", "prompt", "prompt_price")
	output, outputOK := firstFloat(pricingValues, "output", "output_price", "outputPrice", "completion", "completion_price")
	cacheRead, readOK := firstFloat(pricingValues, "cacheRead", "cache_read", "cache_read_price", "cacheReadPrice")
	cacheWrite, writeOK := firstFloat(pricingValues, "cacheWrite", "cache_write", "cache_write_price", "cacheWritePrice")
	if !inputOK && !outputOK && !readOK && !writeOK {
		return nil
	}
	return &ModelPricing{
		Input: input, Output: output, CacheRead: cacheRead, CacheWrite: cacheWrite,
		Currency: firstString(pricingValues, "currency", "unit"), Known: true,
		Source: "catalog",
	}
}
