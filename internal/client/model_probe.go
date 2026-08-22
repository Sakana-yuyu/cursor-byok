// model_probe.go 提供轻量的模型可用性探测，用于批量拉取后快速体检。
//
// 与 model_adapter_benchmark.go 的测速不同：探测只发送一个极短请求
// （max_tokens 很小、禁用思考），仅判断"模型在该供应商是否可用"，
// 避免逐个模型跑完整测速把上游打满。
package client

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
	serverconfig "cursor/internal/backend/server/config"
)

const (
	modelAdapterProbePrompt    = "hi"
	modelAdapterProbeMaxTokens = 64
	modelAdapterProbeTimeout   = 20 * time.Second
)

// modelAdapterProbeStatusPattern 从 adapter 错误信息中提取上游 HTTP 状态码。
var modelAdapterProbeStatusPattern = regexp.MustCompile(`status=(\d{3})`)

// ModelAdapterProbeResult 表示一次轻量可用性探测结果。
type ModelAdapterProbeResult struct {
	// ID 回传探测目标标识（优先 adapter.ID，其次 modelID），供前端映射。
	ID string `json:"id"`
	// ModelID 表示被探测的 provider 侧模型标识。
	ModelID string `json:"modelID"`
	// OK 表示模型可用（上游正常受理请求）。
	OK bool `json:"ok"`
	// Status 表示上游 HTTP 状态码，成功时为 200，无法解析时为 0。
	Status int `json:"status"`
	// Message 表示人类可读的失败原因，成功时为空。
	Message string `json:"message"`
	// RawResponse 表示原始错误信息，供排查。
	RawResponse string `json:"rawResponse"`
}

// ProbeModelAdapter 发送最小请求探测模型是否可用。
func (s *ProxyService) ProbeModelAdapter(adapter serverconfig.ModelAdapterConfig) ModelAdapterProbeResult {
	id := strings.TrimSpace(adapter.ID)
	if id == "" {
		id = strings.TrimSpace(adapter.ModelID)
	}

	normalized, err := normalizeSingleModelAdapterConfig(adapter)
	if err != nil {
		return ModelAdapterProbeResult{
			ID:      id,
			ModelID: strings.TrimSpace(adapter.ModelID),
			OK:      false,
			Message: humanizeModelAdapterProbeError(err, 0),
		}
	}
	if isCursorAccountModelAdapter(normalized) {
		return ModelAdapterProbeResult{
			ID:      id,
			ModelID: strings.TrimSpace(normalized.ModelID),
			OK:      false,
			Message: errCursorAccountModelOperationUnavailable.Error(),
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), modelAdapterProbeTimeout)
	defer cancel()

	sawContent, streamErr := runModelAdapterProbeStream(ctx, normalized)
	result := ModelAdapterProbeResult{ID: id, ModelID: strings.TrimSpace(normalized.ModelID)}
	if streamErr == nil {
		// 收紧成功判定：一个仅返回 200 却没有任何正文/思考/用量事件的流
		// （例如上游以 200 直接吐出 error 负载）不应算作可用，避免误报。
		if !sawContent {
			result.OK = false
			result.Status = 200
			result.Message = "供应商返回 200，但未产出任何内容，模型可能不可用"
			return result
		}
		result.OK = true
		result.Status = 200
		return result
	}

	status := parseModelAdapterProbeStatus(streamErr)
	result.OK = false
	result.Status = status
	result.Message = humanizeModelAdapterProbeError(streamErr, status)
	result.RawResponse = strings.TrimSpace(streamErr.Error())
	return result
}

// runModelAdapterProbeStream 依据供应商类型构造最小流式请求并执行。
// 返回值 sawContent 表示流中是否出现过真实的正文/思考/工具/用量事件，
// 供上层收紧成功判定使用。
func runModelAdapterProbeStream(ctx context.Context, adapter serverconfig.ModelAdapterConfig) (bool, error) {
	requestID := "model-adapter-probe-" + buildModelAdapterTestRequestHash(adapter)
	base := modeladapter.StreamRequest{
		RequestID:                   requestID,
		RunID:                       requestID,
		ModelCallID:                 requestID,
		ModelID:                     strings.TrimSpace(adapter.ID),
		ProtocolMode:                strings.TrimSpace(adapter.ProtocolMode),
		ProtocolGroup:               strings.TrimSpace(adapter.ProtocolGroup),
		BaseURL:                     strings.TrimSpace(adapter.BaseURL),
		APIKey:                      strings.TrimSpace(adapter.APIKey),
		ProviderModelID:             strings.TrimSpace(adapter.ModelID),
		ResolvedChannelID:           strings.TrimSpace(adapter.ID),
		ResolvedChannelName:         strings.TrimSpace(adapter.DisplayName),
		ResolvedContextWindowTokens: adapter.ContextWindowTokens,
		// 探测统一禁用思考，避免 reasoning/adaptive 在极小 max_tokens 下报错。
		ThinkingEffort:            "disabled",
		CustomHeadersEnabled:      adapter.CustomHeadersEnabled,
		CustomHeadersJSON:         strings.TrimSpace(adapter.CustomHeadersJSON),
		Messages:                  []modeladapter.Message{{Role: "user", Content: modelAdapterProbePrompt}},
		MaxTokens:                 modelAdapterProbeMaxTokens,
		Stream:                    true,
		ProviderStreamIdleTimeout: modelAdapterProbeTimeout,
	}

	// sawContent 记录是否出现过真实的正文/思考/工具/用量事件。
	sawContent := false
	// sink 遇到 provider 错误时向上返回错误；只有真正产出内容的事件才标记可用。
	sink := func(event modeladapter.ModelEvent) error {
		switch event.Kind {
		case modeladapter.ModelEventKindProviderError:
			if event.Err != nil {
				return event.Err
			}
			return errors.New("provider error")
		case modeladapter.ModelEventKindTextDelta,
			modeladapter.ModelEventKindThinkingDelta,
			modeladapter.ModelEventKindThinkingCompleted,
			modeladapter.ModelEventKindPartialToolCall,
			modeladapter.ModelEventKindToolCallDelta,
			modeladapter.ModelEventKindToolLikeCompleted:
			sawContent = true
		case modeladapter.ModelEventKindTurnFinished:
			// 回合结束若带有 usage，说明上游确实处理了本次请求。
			if event.UsagePresent {
				sawContent = true
			}
		}
		return nil
	}

	switch strings.TrimSpace(strings.ToLower(adapter.Type)) {
	case "anthropic":
		req := base
		req.Provider = "anthropic"
		req.AnthropicAuthMode = strings.TrimSpace(adapter.AnthropicAuthMode)
		req.AnthropicMaxTokens = modelAdapterProbeMaxTokens
		req.RequestKnobs = map[string]any{
			"stream":               true,
			"anthropic_max_tokens": modelAdapterProbeMaxTokens,
			"max_tokens":           modelAdapterProbeMaxTokens,
		}
		err := modeladapter.NewAnthropicAdapter().Stream(ctx, req, sink)
		return sawContent, err
	case "openai":
		req := base
		req.Provider = "openai"
		req.OpenAIEndpoint = strings.TrimSpace(adapter.OpenAIEndpoint)
		req.OpenAIRequestGroup = strings.TrimSpace(adapter.OpenAIRequestGroup)
		req.OpenAIExtraParamsEnabled = adapter.OpenAIExtraParamsEnabled
		req.OpenAIExtraParamsJSON = strings.TrimSpace(adapter.OpenAIExtraParamsJSON)
		req.RequestKnobs = map[string]any{"stream": true, "max_tokens": modelAdapterProbeMaxTokens}
		err := modeladapter.NewOpenAIAdapter().Stream(ctx, req, sink)
		return sawContent, err
	case "gemini":
		req := base
		req.Provider = "gemini"
		thinkingEffort := geminiProbeThinkingEffort(adapter.ModelID)
		req.ReasoningEffort = thinkingEffort
		req.ThinkingEffort = thinkingEffort
		req.RequestKnobs = map[string]any{"stream": true, "max_tokens": modelAdapterProbeMaxTokens}
		if thinkingEffort != "" {
			req.RequestKnobs["thinking_effort"] = thinkingEffort
		}
		err := modeladapter.NewGeminiAdapter().Stream(ctx, req, sink)
		return sawContent, err
	default:
		return false, fmt.Errorf("不支持的供应商类型 %q", strings.TrimSpace(adapter.Type))
	}
}

func geminiProbeThinkingEffort(modelID string) string {
	model := strings.ToLower(strings.TrimSpace(modelID))
	if strings.Contains(model, "gemini-2.5-flash") {
		return "disabled"
	}
	return ""
}

// parseModelAdapterProbeStatus 解析上游 HTTP 状态码。
// 优先通过 errors.As 读取适配器返回的结构化 HTTPStatusError，
// 仅在无结构化状态时回退到从错误文本中提取 status=xxx。
func parseModelAdapterProbeStatus(err error) int {
	if err == nil {
		return 0
	}
	var statusErr *modeladapter.HTTPStatusError
	if errors.As(err, &statusErr) && statusErr != nil && statusErr.StatusCode > 0 {
		return statusErr.StatusCode
	}
	matches := modelAdapterProbeStatusPattern.FindStringSubmatch(err.Error())
	if len(matches) < 2 {
		return 0
	}
	status, convErr := strconv.Atoi(matches[1])
	if convErr != nil {
		return 0
	}
	return status
}

// humanizeModelAdapterProbeError 把探测错误翻译成人类可读的失败原因。
func humanizeModelAdapterProbeError(err error, status int) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "请求超时，供应商无响应"
	}
	if errors.Is(err, context.Canceled) {
		return "探测已取消"
	}
	switch status {
	case 400:
		return "请求被拒绝（可能是该模型不支持当前参数）"
	case 401, 403:
		return "密钥无效或无权限访问该模型"
	case 404:
		return "该模型在此供应商不存在或未开通"
	case 429:
		return "触发限流，请稍后重试"
	case 500, 502, 503, 504:
		return "供应商服务异常，请稍后重试"
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "模型不可用"
	}
	return message
}
