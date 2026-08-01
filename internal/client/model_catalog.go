package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	SupplierID           string `json:"supplierID,omitempty"`
	ModelCatalogStatus   string `json:"modelCatalogStatus,omitempty"`
	AppendModelCatalogCandidates *bool `json:"appendModelCatalogCandidates,omitempty"`
	BaseURL              string `json:"baseURL"`
	APIKey               string `json:"apiKey"`
	CustomHeadersEnabled bool   `json:"customHeadersEnabled"`
	CustomHeadersJSON    string `json:"customHeadersJSON"`
	ModelCatalogURL      string `json:"modelCatalogURL,omitempty"`
	ModelCatalogURLsJSON string `json:"modelCatalogURLsJSON,omitempty"`
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
	typeName := strings.ToLower(strings.TrimSpace(request.Type))
	if typeName != "openai" && typeName != "anthropic" && typeName != "gemini" {
		return ModelCatalogResult{}, i18n.NewError("error.model_adapter.type_invalid", i18n.CodeInvalidModelAdapter, "模型适配器 type 仅支持 openai、anthropic 或 gemini")
	}
	apiKey := strings.TrimSpace(request.APIKey)
	if apiKey == "" {
		return ModelCatalogResult{}, i18n.NewError("error.model_adapter.api_key_required", i18n.CodeInvalidModelAdapter, "模型适配器 apiKey 不能为空")
	}

	candidates, err := buildModelCatalogCandidates(request)
	if err != nil {
		return ModelCatalogResult{}, err
	}

	customHeaders, err := parseModelCatalogHeaders(request)
	if err != nil {
		return ModelCatalogResult{}, err
	}
	headerIdentity := modelCatalogHeadersIdentity(customHeaders)
	cacheKey := metadataCacheKey(typeName, request.BaseURL, apiKey) + "|supplier=" + strings.ToLower(strings.TrimSpace(request.SupplierID)) + "|catalog=" + strings.Join(candidates, "\x1f") + "|headers=" + headerIdentity
	if request.ForceRefresh {
		s.modelCatalogCache.invalidate(cacheKey)
	} else if cached, ok := s.modelCatalogCache.get(cacheKey); ok {
		return cached, nil
	}

	headers := make(http.Header)
	if typeName == "anthropic" {
		headers.Set("x-api-key", apiKey)
		headers.Set("anthropic-version", "2023-06-01")
	} else if typeName == "gemini" {
		headers.Set("x-goog-api-key", apiKey)
	} else {
		headers.Set("Authorization", "Bearer "+apiKey)
	}
	for name, values := range customHeaders {
		headers.Del(name)
		for _, value := range values {
			headers.Set(name, value)
		}
	}

	client := s.publicClient
	if client == nil {
		client = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(context.Background(), modelCatalogTimeout)
	defer cancel()
	lastStatus := 0
	var lastErr error
	for _, endpoint := range candidates {
		httpRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if requestErr != nil {
			lastErr = requestErr
			continue
		}
		httpRequest.Header = headers.Clone()
		response, requestErr := client.Do(httpRequest)
		if requestErr != nil {
			lastErr = requestErr
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, modelCatalogMaxBodyBytes+1))
		response.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		lastStatus = response.StatusCode
		if len(body) > modelCatalogMaxBodyBytes {
			lastErr = fmt.Errorf("模型列表响应超过 %d MB 限制", modelCatalogMaxBodyBytes/(1<<20))
			continue
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			lastErr = fmt.Errorf("服务返回 HTTP %d", response.StatusCode)
			continue
		}
		models, decodeErr := decodeModelCatalog(body)
		if decodeErr != nil {
			lastErr = decodeErr
			continue
		}
		models = filterModelCatalogByType(models, typeName)
		result := ModelCatalogResult{Models: models}
		s.modelCatalogCache.set(cacheKey, result)
		return result, nil
	}
	message := "所有模型目录候选地址均失败"
	if lastStatus > 0 {
		message = fmt.Sprintf("所有模型目录候选地址均失败，最后响应 HTTP %d", lastStatus)
	} else if lastErr != nil {
		message = message + "：" + lastErr.Error()
	}
	return ModelCatalogResult{}, i18n.NewError("error.model_catalog.request_failed", i18n.CodeModelCatalog, message)
}

func modelCatalogHeadersIdentity(headers http.Header) string {
	parts := make([]string, 0, len(headers))
	for name, values := range headers {
		parts = append(parts, strings.ToLower(strings.TrimSpace(name))+"="+strings.Join(values, "\x1f"))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
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

// buildModelCatalogCandidates keeps explicit provider URLs ahead of generated fallbacks.
func buildModelCatalogCandidates(request ModelCatalogRequest) ([]string, error) {
	baseURL, err := modelchannel.NormalizeBaseURL(request.BaseURL)
	if err != nil {
		return nil, err
	}
	typeName := strings.ToLower(strings.TrimSpace(request.Type))
	var candidates []string
	appendCandidate := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		if parsed, parseErr := url.Parse(raw); parseErr != nil || parsed.Scheme == "" || parsed.Host == "" {
			return
		}
		for _, existing := range candidates {
			if existing == raw {
				return
			}
		}
		candidates = append(candidates, raw)
	}
	appendCandidate(request.ModelCatalogURL)
	if strings.TrimSpace(request.ModelCatalogURLsJSON) != "" {
		var explicit []string
		if err := json.Unmarshal([]byte(request.ModelCatalogURLsJSON), &explicit); err != nil {
			return nil, i18n.NewError("error.model_catalog.urls_invalid", i18n.CodeInvalidModelAdapter, "模型目录候选地址必须是字符串数组 JSON")
		}
		for _, candidate := range explicit {
			appendCandidate(candidate)
		}
	}
	if strings.EqualFold(strings.TrimSpace(request.ModelCatalogStatus), "manual_only") {
		if len(candidates) == 0 {
			return nil, i18n.NewError("error.model_catalog.url_invalid", i18n.CodeModelCatalog, "该供应商未核验模型目录地址，请手动添加模型")
		}
		return candidates, nil
	}
	appendGenerated := request.AppendModelCatalogCandidates == nil || *request.AppendModelCatalogCandidates
	if typeName == "gemini" && appendGenerated {
		appendGeminiModelCatalogCandidates(baseURL, appendCandidate)
	} else if appendGenerated {
		appendGeneratedModelCatalogCandidates(candidatesForBaseURL(baseURL), appendCandidate)
	}
	if len(candidates) == 0 {
		return nil, i18n.NewError("error.model_catalog.url_invalid", i18n.CodeInvalidModelAdapter, "模型目录地址不能为空")
	}
	return candidates, nil
}

func candidatesForBaseURL(baseURL string) []string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil
	}
	path := strings.TrimRight(parsed.Path, "/")
	lowerPath := strings.ToLower(path)
	if strings.HasSuffix(lowerPath, "/models") {
		return []string{parsed.String()}
	}
	var result []string
	add := func(candidatePath string) {
		clone := *parsed
		clone.Path = candidatePath
		clone.RawPath = ""
		result = append(result, clone.String())
	}
	if strings.HasSuffix(lowerPath, "/v1") || hasVersionPathSuffix(path) {
		add(path + "/models")
		if !strings.HasSuffix(lowerPath, "/v1") {
			add(path + "/v1/models")
		}
	} else {
		add(path + "/v1/models")
	}
	for _, suffix := range []string{"/api/claudecode", "/api/anthropic", "/apps/anthropic", "/api/coding", "/claudecode", "/anthropic", "/step_plan", "/coding", "/claude", "/compatible"} {
		if strings.HasSuffix(strings.ToLower(path), suffix) {
			root := strings.TrimRight(path[:len(path)-len(suffix)], "/")
			if root != "" {
				add(root + "/v1/models")
				add(root + "/models")
			}
			break
		}
	}
	return result
}

func hasVersionPathSuffix(path string) bool {
	last := path
	if index := strings.LastIndex(last, "/"); index >= 0 {
		last = last[index+1:]
	}
	if len(last) < 2 || (last[0] != 'v' && last[0] != 'V') {
		return false
	}
	for _, ch := range last[1:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func appendGeneratedModelCatalogCandidates(generated []string, appendCandidate func(string)) {
	for _, candidate := range generated {
		appendCandidate(candidate)
	}
}

func appendGeminiModelCatalogCandidates(baseURL string, appendCandidate func(string)) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return
	}
	path := strings.TrimRight(parsed.Path, "/")
	lowerPath := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lowerPath, "/models"):
		appendCandidate(parsed.String())
	case strings.HasSuffix(lowerPath, "/v1beta"):
		parsed.Path = path + "/models"
		parsed.RawPath = ""
		appendCandidate(parsed.String())
	default:
		parsed.Path = path + "/v1beta/models"
		parsed.RawPath = ""
		appendCandidate(parsed.String())
	}
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
