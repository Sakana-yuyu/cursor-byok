package client

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cursor/internal/appdata"
	modeladapter "cursor/internal/backend/agent/model"
	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/logger"
	"cursor/internal/modelchannel"
	"cursor/internal/safego"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	modelAdapterTestUpdatedEvent      = "model-adapter-test:updated"
	modelAdapterTestPrompt            = "Output the numbers 1 through 120 separated by a single space. No commas, no newlines, no explanation."
	modelAdapterTestTimeout           = 45 * time.Second
	modelAdapterTestDefaultMaxTokens  = 65_536
	modelAdapterTestEmptyTextError    = "未收到文本输出，无法计算测速结果"
	modelAdapterTestMaxErrorBodyBytes = 8192
)

type ModelAdapterTestStatus string

const (
	ModelAdapterTestStatusRunning ModelAdapterTestStatus = "running"
	ModelAdapterTestStatusSuccess ModelAdapterTestStatus = "success"
	ModelAdapterTestStatusError   ModelAdapterTestStatus = "error"
)

// ModelAdapterTestResult 表示一次模型测速结果。
type ModelAdapterTestResult struct {
	AdapterID               string  `json:"adapterID"`
	RequestHash             string  `json:"requestHash"`
	Status                  string  `json:"status"`
	TokensPerSecond         float64 `json:"tokensPerSecond"`
	VisibleTokensPerSecond  float64 `json:"visibleTokensPerSecond"`
	FirstResponseMS         int64   `json:"firstResponseMS"`
	FirstTextTokenMS        int64   `json:"firstTextTokenMS"`
	TotalDurationMS         int64   `json:"totalDurationMS"`
	OutputTokens            int64   `json:"outputTokens"`
	VisibleOutputTokens     int64   `json:"visibleOutputTokens"`
	ReasoningTokens         int64   `json:"reasoningTokens"`
	EffectiveThinkingEffort string  `json:"effectiveThinkingEffort"`
	TokensEstimated         bool    `json:"tokensEstimated"`
	SummaryText             string  `json:"summaryText"`
	Error                   string  `json:"error"`
	RawResponse             string  `json:"rawResponse"`
	TestedAt                string  `json:"testedAt"`
}

// ModelAdapterTestResultsPayload 用于向前端广播当前测速结果快照。
type ModelAdapterTestResultsPayload struct {
	Results []ModelAdapterTestResult `json:"results"`
}

type modelAdapterTestMetrics struct {
	firstResponseAt         time.Time
	firstTextTokenAt        time.Time
	finishedAt              time.Time
	outputTokens            int64
	reasoningTokens         int64
	outputProvided          bool
	effectiveThinkingEffort string
	text                    strings.Builder
	rawResponse             string
}

type modelAdapterTestArtifactObserver struct {
	mu       sync.Mutex
	response strings.Builder
}

func (observer *modelAdapterTestArtifactObserver) RecordLLMRequest(string, string, string, map[string]any) (string, error) {
	return "", nil
}

func (observer *modelAdapterTestArtifactObserver) AppendLLMResponseChunk(_ string, _ string, _ string, chunk string) (string, error) {
	if observer == nil {
		return "", nil
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	_, _ = observer.response.WriteString(chunk)
	return "", nil
}

func (observer *modelAdapterTestArtifactObserver) RecordLLMSummary(string, string, string, map[string]any) (string, error) {
	return "", nil
}

func (observer *modelAdapterTestArtifactObserver) RawResponse() string {
	if observer == nil {
		return ""
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return strings.TrimSpace(observer.response.String())
}

func (s *ProxyService) GetModelAdapterTestResults() []ModelAdapterTestResult {
	return s.snapshotModelAdapterTestResults()
}

func (s *ProxyService) TestModelAdapter(adapter serverconfig.ModelAdapterConfig) (ModelAdapterTestResult, error) {
	requestHash := buildModelAdapterTestRequestHash(adapter)
	adapterID := buildModelAdapterTestCacheKey(adapter, requestHash)

	if cached, ok := s.getRunningModelAdapterTestResult(adapterID, requestHash); ok {
		return cached, nil
	}

	normalized, err := normalizeSingleModelAdapterConfig(adapter)
	if err != nil {
		result := ModelAdapterTestResult{
			AdapterID:   adapterID,
			RequestHash: requestHash,
			Status:      string(ModelAdapterTestStatusError),
			SummaryText: buildModelAdapterTestErrorSummary(err),
			Error:       buildModelAdapterTestErrorSummary(err),
			RawResponse: strings.TrimSpace(modelAdapterTestErrorMessage(err)),
			TestedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		}
		s.storeAndEmitModelAdapterTestResult(result)
		return result, err
	}

	running := ModelAdapterTestResult{
		AdapterID:   normalized.ID,
		RequestHash: requestHash,
		Status:      string(ModelAdapterTestStatusRunning),
		SummaryText: "测试中...",
		TestedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}
	s.storeAndEmitModelAdapterTestResult(running)

	// 即使测试过程中发生 panic，也必须把结果从 "running" 推进到终态，
	// 否则 getRunningModelAdapterTestResult 会永久阻止该 adapter 的后续重试。
	result, testErr := s.safeRunModelAdapterTest(normalized, requestHash)
	s.storeAndEmitModelAdapterTestResult(result)
	if testErr != nil {
		return result, testErr
	}
	return result, nil
}

// safeRunModelAdapterTest 包装 runModelAdapterTest，在 panic 时返回 error 结果而非让
// "running" 状态永久滞留。
func (s *ProxyService) safeRunModelAdapterTest(adapter serverconfig.ModelAdapterConfig, requestHash string) (result ModelAdapterTestResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			panicErr := fmt.Errorf("model adapter test panicked: %v", r)
			result = buildErroredModelAdapterTestResult(adapter.ID, requestHash, panicErr)
			err = panicErr
		}
	}()
	return s.runModelAdapterTest(adapter, requestHash)
}

func normalizeSingleModelAdapterConfig(adapter serverconfig.ModelAdapterConfig) (serverconfig.ModelAdapterConfig, error) {
	normalized, err := serverconfig.NormalizeModelAdapterConfigs([]serverconfig.ModelAdapterConfig{adapter})
	if err != nil {
		return serverconfig.ModelAdapterConfig{}, err
	}
	if len(normalized) == 0 {
		return serverconfig.ModelAdapterConfig{}, errors.New("模型配置不能为空")
	}
	return normalized[0], nil
}

func (s *ProxyService) runModelAdapterTest(adapter serverconfig.ModelAdapterConfig, requestHash string) (ModelAdapterTestResult, error) {
	return s.runModelAdapterTestWithFallback(adapter, requestHash, true)
}

func (s *ProxyService) runModelAdapterTestWithFallback(adapter serverconfig.ModelAdapterConfig, requestHash string, allowEndpointFallback bool) (ModelAdapterTestResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), modelAdapterTestTimeout)
	defer cancel()

	startedAt := time.Now().UTC()
	metrics, requestErr := s.executeModelAdapterNonStreamingTest(ctx, adapter)
	if requestErr != nil {
		// 界面中的渠道测试可在端点不兼容时自动探测备选协议并保存；
		// 隔离测速明确关闭此分支，确保结果对应当前配置。
		if allowEndpointFallback {
			fallbackResult, ok := s.tryOpenAIEndpointFallback(ctx, adapter, requestHash, requestErr)
			if ok {
				return fallbackResult, nil
			}
		}
		result := buildErroredModelAdapterTestResult(adapter.ID, requestHash, requestErr)
		return result, requestErr
	}

	result, ok := buildSuccessfulModelAdapterTestResult(adapter.ID, requestHash, startedAt, metrics)
	if !ok {
		emptyTextErr := errors.New(modelAdapterTestEmptyTextError)
		return buildErroredModelAdapterTestResult(adapter.ID, requestHash, emptyTextErr), emptyTextErr
	}
	return result, nil
}

func (s *ProxyService) executeModelAdapterNonStreamingTest(ctx context.Context, adapter serverconfig.ModelAdapterConfig) (*modelAdapterTestMetrics, error) {
	switch strings.TrimSpace(adapter.Type) {
	case "openai":
		return s.executeOpenAIStreamingTest(ctx, adapter)
	case "anthropic":
		return s.executeAnthropicStreamingTest(ctx, adapter)
	default:
		return nil, fmt.Errorf("unsupported provider %q", strings.TrimSpace(adapter.Type))
	}
}

func (s *ProxyService) executeOpenAIStreamingTest(ctx context.Context, adapter serverconfig.ModelAdapterConfig) (*modelAdapterTestMetrics, error) {
	_ = s
	metrics := &modelAdapterTestMetrics{}
	observer := &modelAdapterTestArtifactObserver{}
	maxTokens := modelAdapterTestConfiguredOpenAIMaxTokens(adapter)
	requestID := "model-adapter-test-" + buildModelAdapterTestRequestHash(adapter)
	req := modeladapter.StreamRequest{
		RequestID:                   requestID,
		RunID:                       requestID,
		ModelCallID:                 requestID,
		ModelID:                     strings.TrimSpace(adapter.ID),
		Provider:                    "openai",
		ProtocolMode:                strings.TrimSpace(adapter.ProtocolMode),
		ProtocolGroup:               strings.TrimSpace(adapter.ProtocolGroup),
		BaseURL:                     strings.TrimSpace(adapter.BaseURL),
		APIKey:                      strings.TrimSpace(adapter.APIKey),
		ProviderModelID:             strings.TrimSpace(adapter.ModelID),
		ResolvedChannelID:           strings.TrimSpace(adapter.ID),
		ResolvedChannelName:         strings.TrimSpace(adapter.DisplayName),
		ResolvedContextWindowTokens: adapter.ContextWindowTokens,
		ReasoningEffort:             strings.TrimSpace(adapter.ReasoningEffort),
		OpenAIEndpoint:              strings.TrimSpace(adapter.OpenAIEndpoint),
		OpenAIRequestGroup:          strings.TrimSpace(adapter.OpenAIRequestGroup),
		OpenAIExtraParamsEnabled:    adapter.OpenAIExtraParamsEnabled,
		OpenAIExtraParamsJSON:       strings.TrimSpace(adapter.OpenAIExtraParamsJSON),
		CustomHeadersEnabled:        adapter.CustomHeadersEnabled,
		CustomHeadersJSON:           strings.TrimSpace(adapter.CustomHeadersJSON),
		Messages:                    []modeladapter.Message{{Role: "user", Content: modelAdapterTestPrompt}},
		MaxTokens:                   maxTokens,
		Stream:                      true,
		RequestKnobs:                map[string]any{"stream": true, "max_tokens": maxTokens},
		Observer:                    observer,
		ProviderStreamIdleTimeout:   modelAdapterTestTimeout,
	}
	metrics.effectiveThinkingEffort = strings.TrimSpace(adapter.ReasoningEffort)
	err := modeladapter.NewOpenAIAdapter().Stream(ctx, req, func(event modeladapter.ModelEvent) error {
		now := time.Now().UTC()
		if metrics.firstResponseAt.IsZero() && isBenchmarkEffectiveResponseEvent(event) {
			metrics.firstResponseAt = now
		}
		switch event.Kind {
		case modeladapter.ModelEventKindTextDelta:
			if strings.TrimSpace(event.Text) != "" && metrics.firstTextTokenAt.IsZero() {
				metrics.firstTextTokenAt = now
			}
			_, _ = metrics.text.WriteString(event.Text)
		case modeladapter.ModelEventKindTurnFinished:
			metrics.finishedAt = now
			metrics.reasoningTokens = event.ReasoningTokens
			if event.OutputTokens > 0 {
				metrics.outputTokens = event.OutputTokens
				metrics.outputProvided = true
			}
		case modeladapter.ModelEventKindProviderError:
			if event.Err != nil {
				return event.Err
			}
			return errors.New("provider error")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if metrics.finishedAt.IsZero() {
		metrics.finishedAt = time.Now().UTC()
	}
	metrics.rawResponse = observer.RawResponse()
	if strings.TrimSpace(metrics.rawResponse) == "" {
		metrics.rawResponse = strings.TrimSpace(metrics.text.String())
	}
	return metrics, nil
}

func (s *ProxyService) executeAnthropicStreamingTest(ctx context.Context, adapter serverconfig.ModelAdapterConfig) (*modelAdapterTestMetrics, error) {
	_ = s
	metrics := &modelAdapterTestMetrics{}
	observer := &modelAdapterTestArtifactObserver{}
	maxTokens := modelAdapterTestConfiguredAnthropicMaxTokens(adapter)
	thinkingEffort := normalizeModelAdapterTestAnthropicThinkingEffort(adapter.AnthropicThinkingEffort)
	requestID := "model-adapter-test-" + buildModelAdapterTestRequestHash(adapter)
	req := modeladapter.StreamRequest{
		RequestID:                   requestID,
		RunID:                       requestID,
		ModelCallID:                 requestID,
		ModelID:                     strings.TrimSpace(adapter.ID),
		Provider:                    "anthropic",
		ProtocolMode:                strings.TrimSpace(adapter.ProtocolMode),
		ProtocolGroup:               strings.TrimSpace(adapter.ProtocolGroup),
		BaseURL:                     strings.TrimSpace(adapter.BaseURL),
		APIKey:                      strings.TrimSpace(adapter.APIKey),
		ProviderModelID:             strings.TrimSpace(adapter.ModelID),
		ResolvedChannelID:           strings.TrimSpace(adapter.ID),
		ResolvedChannelName:         strings.TrimSpace(adapter.DisplayName),
		ResolvedContextWindowTokens: adapter.ContextWindowTokens,
		ThinkingEffort:              thinkingEffort,
		AnthropicMaxTokens:          maxTokens,
		AnthropicThinkingEffort:     thinkingEffort,
		CustomHeadersEnabled:        adapter.CustomHeadersEnabled,
		CustomHeadersJSON:           strings.TrimSpace(adapter.CustomHeadersJSON),
		AnthropicExtraParamsEnabled: adapter.AnthropicExtraParamsEnabled,
		AnthropicExtraParamsJSON:    strings.TrimSpace(adapter.AnthropicExtraParamsJSON),
		ThinkingBudgetTokens:        adapter.ThinkingBudgetTokens,
		Messages:                    []modeladapter.Message{{Role: "user", Content: modelAdapterTestPrompt}},
		MaxTokens:                   maxTokens,
		Stream:                      true,
		RequestKnobs:                map[string]any{"stream": true, "anthropic_max_tokens": maxTokens, "max_tokens": maxTokens},
		Observer:                    observer,
		ProviderStreamIdleTimeout:   modelAdapterTestTimeout,
	}
	metrics.effectiveThinkingEffort = thinkingEffort
	err := modeladapter.NewAnthropicAdapter().Stream(ctx, req, func(event modeladapter.ModelEvent) error {
		now := time.Now().UTC()
		if metrics.firstResponseAt.IsZero() && isBenchmarkEffectiveResponseEvent(event) {
			metrics.firstResponseAt = now
		}
		switch event.Kind {
		case modeladapter.ModelEventKindTextDelta:
			if strings.TrimSpace(event.Text) != "" && metrics.firstTextTokenAt.IsZero() {
				metrics.firstTextTokenAt = now
			}
			_, _ = metrics.text.WriteString(event.Text)
		case modeladapter.ModelEventKindTurnFinished:
			metrics.finishedAt = now
			metrics.reasoningTokens = event.ReasoningTokens
			if event.OutputTokens > 0 {
				metrics.outputTokens = event.OutputTokens
				metrics.outputProvided = true
			}
		case modeladapter.ModelEventKindProviderError:
			if event.Err != nil {
				return event.Err
			}
			return errors.New("provider error")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if metrics.finishedAt.IsZero() {
		metrics.finishedAt = time.Now().UTC()
	}
	metrics.rawResponse = observer.RawResponse()
	if strings.TrimSpace(metrics.rawResponse) == "" {
		metrics.rawResponse = strings.TrimSpace(metrics.text.String())
	}
	return metrics, nil
}

func (s *ProxyService) getRunningModelAdapterTestResult(adapterID string, requestHash string) (ModelAdapterTestResult, bool) {
	s.modelTestMu.RLock()
	defer s.modelTestMu.RUnlock()

	if s.modelTestResults == nil {
		return ModelAdapterTestResult{}, false
	}
	result, ok := s.modelTestResults[adapterID]
	if !ok {
		return ModelAdapterTestResult{}, false
	}
	if strings.TrimSpace(result.Status) != string(ModelAdapterTestStatusRunning) {
		return ModelAdapterTestResult{}, false
	}
	if strings.TrimSpace(result.RequestHash) != strings.TrimSpace(requestHash) {
		return ModelAdapterTestResult{}, false
	}
	return result, true
}

func (s *ProxyService) storeAndEmitModelAdapterTestResult(result ModelAdapterTestResult) {
	if strings.TrimSpace(result.AdapterID) == "" {
		return
	}
	s.modelTestMu.Lock()
	if s.modelTestResults == nil {
		s.modelTestResults = make(map[string]ModelAdapterTestResult)
	}
	s.modelTestResults[result.AdapterID] = result
	snapshot := snapshotModelAdapterTestResultsLocked(s.modelTestResults)
	shouldPersist := strings.TrimSpace(result.Status) == string(ModelAdapterTestStatusSuccess) ||
		strings.TrimSpace(result.Status) == string(ModelAdapterTestStatusError)
	s.modelTestMu.Unlock()
	s.emitModelAdapterTestResults(snapshot)
	if shouldPersist {
		s.persistModelAdapterTestResultsAsync(snapshot)
	}
}

// loadPersistedModelAdapterTestResults 从 appdata 恢复上次进程结束前的终态测速结果。
func (s *ProxyService) loadPersistedModelAdapterTestResults() {
	if s == nil {
		return
	}
	path := appdata.ModelAdapterTestResultsFilePath()
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return
	}
	var payload ModelAdapterTestResultsPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		logger.Errorf("load model adapter test results failed path=%s err=%v", path, err)
		return
	}
	s.modelTestMu.Lock()
	defer s.modelTestMu.Unlock()
	if s.modelTestResults == nil {
		s.modelTestResults = make(map[string]ModelAdapterTestResult)
	}
	for _, item := range payload.Results {
		id := strings.TrimSpace(item.AdapterID)
		if id == "" {
			continue
		}
		status := strings.TrimSpace(item.Status)
		if status != string(ModelAdapterTestStatusSuccess) && status != string(ModelAdapterTestStatusError) {
			continue
		}
		// 落盘不保留大段 rawResponse，启动后仍可展示 status/summary。
		item.RawResponse = ""
		s.modelTestResults[id] = item
	}
}

func (s *ProxyService) persistModelAdapterTestResultsAsync(snapshot []ModelAdapterTestResult) {
	if s == nil {
		return
	}
	// 仅写终态，并去掉 raw 响应体，避免磁盘膨胀与密钥回显风险。
	toWrite := make([]ModelAdapterTestResult, 0, len(snapshot))
	for _, item := range snapshot {
		status := strings.TrimSpace(item.Status)
		if status != string(ModelAdapterTestStatusSuccess) && status != string(ModelAdapterTestStatusError) {
			continue
		}
		item.RawResponse = ""
		toWrite = append(toWrite, item)
	}
	safego.Go("client:persist-test-results", func() {
		path := appdata.ModelAdapterTestResultsFilePath()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			logger.Errorf("persist model adapter test results mkdir failed path=%s err=%v", path, err)
			return
		}
		payload, err := json.MarshalIndent(ModelAdapterTestResultsPayload{Results: toWrite}, "", "  ")
		if err != nil {
			logger.Errorf("persist model adapter test results marshal failed err=%v", err)
			return
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, payload, 0o600); err != nil {
			logger.Errorf("persist model adapter test results write failed path=%s err=%v", tmp, err)
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(path)
			if err2 := os.Rename(tmp, path); err2 != nil {
				logger.Errorf("persist model adapter test results rename failed path=%s err=%v", path, err2)
			}
		}
	})
}

func (s *ProxyService) snapshotModelAdapterTestResults() []ModelAdapterTestResult {
	s.modelTestMu.RLock()
	defer s.modelTestMu.RUnlock()
	return snapshotModelAdapterTestResultsLocked(s.modelTestResults)
}

func snapshotModelAdapterTestResultsLocked(items map[string]ModelAdapterTestResult) []ModelAdapterTestResult {
	if len(items) == 0 {
		return []ModelAdapterTestResult{}
	}
	results := make([]ModelAdapterTestResult, 0, len(items))
	for _, item := range items {
		results = append(results, item)
	}
	sort.Slice(results, func(i int, j int) bool {
		if results[i].TestedAt == results[j].TestedAt {
			return results[i].AdapterID < results[j].AdapterID
		}
		return results[i].TestedAt > results[j].TestedAt
	})
	return results
}

func (s *ProxyService) emitModelAdapterTestResults(results []ModelAdapterTestResult) {
	app := application.Get()
	if app == nil {
		return
	}
	app.Event.Emit(modelAdapterTestUpdatedEvent, ModelAdapterTestResultsPayload{
		Results: results,
	})
}

func buildErroredModelAdapterTestResult(adapterID string, requestHash string, err error) ModelAdapterTestResult {
	message := strings.TrimSpace(modelAdapterTestErrorMessage(err))
	summary := buildModelAdapterTestErrorSummary(err)
	return ModelAdapterTestResult{
		AdapterID:   strings.TrimSpace(adapterID),
		RequestHash: strings.TrimSpace(requestHash),
		Status:      string(ModelAdapterTestStatusError),
		SummaryText: summary,
		Error:       summary,
		RawResponse: message,
		TestedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func buildModelAdapterTestSummaryText(result ModelAdapterTestResult) string {
	if strings.TrimSpace(result.Status) != string(ModelAdapterTestStatusSuccess) {
		return firstNonEmptyTrimmed(result.SummaryText, "测试失败")
	}
	return fmt.Sprintf(
		"总生成 %d t/s | 正文 %d t/s | 首响应 %s | 首字 %s",
		int(math.Round(maxFloat64(result.TokensPerSecond, 0))),
		int(math.Round(maxFloat64(result.VisibleTokensPerSecond, 0))),
		formatModelAdapterTestDuration(result.FirstResponseMS),
		formatModelAdapterTestDuration(result.FirstTextTokenMS),
	)
}

func buildModelAdapterHTTPStatusError(prefix string, resp *http.Response) error {
	if resp == nil {
		return fmt.Errorf("%s response is nil", strings.TrimSpace(prefix))
	}
	limitedBody, err := io.ReadAll(io.LimitReader(resp.Body, modelAdapterTestMaxErrorBodyBytes))
	if err != nil {
		if retrySummary := modeladapter.ProviderRetryAttemptSummary(resp); retrySummary != "" {
			return fmt.Errorf("%s status=%d %s body_read_error=%v", strings.TrimSpace(prefix), resp.StatusCode, retrySummary, err)
		}
		return fmt.Errorf("%s status=%d body_read_error=%v", strings.TrimSpace(prefix), resp.StatusCode, err)
	}
	retrySummary := modeladapter.ProviderRetryAttemptSummary(resp)
	bodyText := strings.TrimSpace(string(limitedBody))
	if bodyText == "" {
		if retrySummary != "" {
			return fmt.Errorf("%s status=%d %s", strings.TrimSpace(prefix), resp.StatusCode, retrySummary)
		}
		return fmt.Errorf("%s status=%d", strings.TrimSpace(prefix), resp.StatusCode)
	}
	if retrySummary != "" {
		return fmt.Errorf("%s status=%d %s body=%s", strings.TrimSpace(prefix), resp.StatusCode, retrySummary, bodyText)
	}
	return fmt.Errorf("%s status=%d body=%s", strings.TrimSpace(prefix), resp.StatusCode, bodyText)
}

func buildModelAdapterProviderBodyError(prefix string, body []byte) error {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	errorValue, ok := payload["error"]
	if !ok || errorValue == nil {
		return nil
	}
	message := ""
	details := make([]string, 0, 2)
	switch value := errorValue.(type) {
	case string:
		message = strings.TrimSpace(value)
	case map[string]any:
		message = strings.TrimSpace(fmt.Sprint(value["message"]))
		if errorType := strings.TrimSpace(fmt.Sprint(value["type"])); errorType != "" && errorType != "<nil>" {
			details = append(details, "type="+errorType)
		}
		if code := strings.TrimSpace(fmt.Sprint(value["code"])); code != "" && code != "<nil>" {
			details = append(details, "code="+code)
		}
	default:
		message = strings.TrimSpace(fmt.Sprint(value))
	}
	if message == "" || message == "<nil>" {
		message = "provider returned error response"
	}
	summary := strings.TrimSpace(prefix)
	if summary == "" {
		summary = "model adapter"
	}
	if len(details) > 0 {
		return fmt.Errorf("%s provider error %s: %s", summary, strings.Join(details, " "), message)
	}
	return fmt.Errorf("%s provider error: %s", summary, message)
}

func modelAdapterTestErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "模型测试失败"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "模型测试超时，请稍后重试"
	}
	return message
}

func buildModelAdapterTestErrorSummary(err error) string {
	if err == nil {
		return "测试失败"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "测试超时"
	}
	message := strings.TrimSpace(err.Error())
	switch {
	case strings.Contains(message, modelAdapterTestEmptyTextError):
		return "无正文返回"
	case strings.Contains(strings.ToLower(message), "context canceled"):
		return "测试已停止"
	default:
		return "测试失败"
	}
}

func formatModelAdapterTestDuration(durationMS int64) string {
	if durationMS < 1000 {
		if durationMS < 0 {
			durationMS = 0
		}
		return fmt.Sprintf("%d ms", durationMS)
	}
	seconds := float64(durationMS) / 1000
	return fmt.Sprintf("%.1f s", seconds)
}

func estimateBenchmarkTextTokens(text string) int64 {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	runeCount := utf8.RuneCountInString(trimmed)
	if runeCount <= 0 {
		return 0
	}
	estimated := int64((runeCount + 3) / 4)
	estimated += int64(strings.Count(trimmed, "\n"))
	if estimated < 1 {
		return 1
	}
	return estimated
}

func buildModelAdapterTestCacheKey(adapter serverconfig.ModelAdapterConfig, requestHash string) string {
	baseURL, baseURLErr := modelchannel.NormalizeBaseURL(adapter.BaseURL)
	if baseURLErr == nil &&
		strings.TrimSpace(adapter.DisplayName) != "" &&
		strings.TrimSpace(adapter.ModelID) != "" &&
		strings.TrimSpace(adapter.APIKey) != "" {
		return modelchannel.BuildChannelID(baseURL, adapter.ModelID, adapter.APIKey, adapter.DisplayName, modelchannel.NormalizeOpenAIEndpoint(adapter.Type, adapter.OpenAIEndpoint))
	}
	return "invalid:" + strings.TrimSpace(requestHash)
}

func buildModelAdapterTestRequestHash(adapter serverconfig.ModelAdapterConfig) string {
	source := normalizeModelAdapterTestHashSource(adapter)
	payload := strings.Join([]string{
		source.Type,
		source.BaseURL,
		source.APIKey,
		source.ModelID,
		source.ProtocolMode,
		source.ProtocolGroup,
		source.ReasoningEffort,
		source.OpenAIEndpoint,
		source.OpenAIRequestGroup,
		strconv.Itoa(source.OpenAIExtraParamsEnabled),
		source.OpenAIExtraParamsJSON,
		strconv.Itoa(source.CustomHeadersEnabled),
		source.CustomHeadersJSON,
		strconv.Itoa(source.AnthropicExtraParamsEnabled),
		source.AnthropicExtraParamsJSON,
		strconv.Itoa(source.ContextWindowTokens),
		strconv.Itoa(source.MaxCompletionTokens),
		strconv.Itoa(source.AnthropicMaxTokens),
		source.AnthropicThinkingEffort,
	}, "\n")
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(payload))
	sum := hasher.Sum(nil)
	return hex.EncodeToString(sum)
}

type modelAdapterTestHashSource struct {
	Type                        string
	BaseURL                     string
	APIKey                      string
	ModelID                     string
	ProtocolMode                string
	ProtocolGroup               string
	ReasoningEffort             string
	OpenAIEndpoint              string
	OpenAIRequestGroup          string
	OpenAIExtraParamsEnabled    int
	OpenAIExtraParamsJSON       string
	CustomHeadersEnabled        int
	CustomHeadersJSON           string
	AnthropicExtraParamsEnabled int
	AnthropicExtraParamsJSON    string
	ContextWindowTokens         int
	MaxCompletionTokens         int
	AnthropicMaxTokens          int
	AnthropicThinkingEffort     string
}

func normalizeModelAdapterTestHashSource(adapter serverconfig.ModelAdapterConfig) modelAdapterTestHashSource {
	baseURL := strings.TrimSpace(adapter.BaseURL)
	if normalizedBaseURL, err := modelchannel.NormalizeBaseURL(adapter.BaseURL); err == nil {
		baseURL = normalizedBaseURL
	}
	return modelAdapterTestHashSource{
		Type:                        normalizeModelAdapterTestType(adapter.Type),
		BaseURL:                     baseURL,
		APIKey:                      strings.TrimSpace(adapter.APIKey),
		ModelID:                     strings.TrimSpace(adapter.ModelID),
		ProtocolMode:                modelchannel.NormalizeProtocolMode(adapter.ProtocolMode),
		ProtocolGroup:               modelchannel.ResolveProtocolGroup(adapter.ProtocolMode, adapter.Type, adapter.ModelID, adapter.BaseURL, adapter.OpenAIEndpoint, firstNonEmptyTrimmed(adapter.ProtocolGroup, adapter.OpenAIRequestGroup)),
		ReasoningEffort:             normalizeModelAdapterTestProviderReasoning(adapter),
		OpenAIEndpoint:              modelchannel.NormalizeOpenAIEndpoint(adapter.Type, adapter.OpenAIEndpoint),
		OpenAIRequestGroup:          modelchannel.NormalizeOpenAIRequestGroup(adapter.Type, adapter.OpenAIEndpoint, adapter.OpenAIRequestGroup),
		OpenAIExtraParamsEnabled:    normalizeModelAdapterTestBool(adapter.Type == "openai" && adapter.OpenAIExtraParamsEnabled),
		OpenAIExtraParamsJSON:       normalizeModelAdapterTestOpenAIExtraParamsJSON(adapter),
		CustomHeadersEnabled:        normalizeModelAdapterTestBool(adapter.CustomHeadersEnabled),
		CustomHeadersJSON:           normalizeModelAdapterTestCustomHeadersJSON(adapter),
		AnthropicExtraParamsEnabled: normalizeModelAdapterTestBool(adapter.Type == "anthropic" && adapter.AnthropicExtraParamsEnabled),
		AnthropicExtraParamsJSON:    normalizeModelAdapterTestAnthropicExtraParamsJSON(adapter),
		ContextWindowTokens:         normalizeModelAdapterTestInt(adapter.ContextWindowTokens),
		MaxCompletionTokens:         normalizeModelAdapterTestInt(adapter.MaxCompletionTokens),
		AnthropicMaxTokens:          normalizeModelAdapterTestInt(adapter.AnthropicMaxTokens),
		AnthropicThinkingEffort:     normalizeModelAdapterTestProviderAnthropicThinkingEffort(adapter),
	}
}

func normalizeModelAdapterTestType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "anthropic":
		return "anthropic"
	case "openai":
		return "openai"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeModelAdapterTestReasoning(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low", "medium", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "medium"
	}
}

func normalizeModelAdapterTestProviderReasoning(adapter serverconfig.ModelAdapterConfig) string {
	if normalizeModelAdapterTestType(adapter.Type) != "openai" {
		return ""
	}
	return normalizeModelAdapterTestReasoning(adapter.ReasoningEffort)
}

func normalizeModelAdapterTestAnthropicThinkingEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low", "medium", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "xhigh"
	}
}

func normalizeModelAdapterTestProviderAnthropicThinkingEffort(adapter serverconfig.ModelAdapterConfig) string {
	if normalizeModelAdapterTestType(adapter.Type) != "anthropic" {
		return ""
	}
	return normalizeModelAdapterTestAnthropicThinkingEffort(adapter.AnthropicThinkingEffort)
}

func modelAdapterTestConfiguredAnthropicMaxTokens(adapter serverconfig.ModelAdapterConfig) int {
	if adapter.AnthropicMaxTokens > 0 {
		return adapter.AnthropicMaxTokens
	}
	if adapter.MaxCompletionTokens > 0 {
		return adapter.MaxCompletionTokens
	}
	return modelAdapterTestDefaultMaxTokens
}

func modelAdapterTestConfiguredOpenAIMaxTokens(adapter serverconfig.ModelAdapterConfig) int {
	if adapter.MaxCompletionTokens > 0 {
		return adapter.MaxCompletionTokens
	}
	if adapter.AnthropicMaxTokens > 0 {
		return adapter.AnthropicMaxTokens
	}
	return modelAdapterTestDefaultMaxTokens
}

func normalizeModelAdapterTestBool(value bool) int {
	if value {
		return 1
	}
	return 0
}

func normalizeModelAdapterTestOpenAIExtraParamsJSON(adapter serverconfig.ModelAdapterConfig) string {
	if normalizeModelAdapterTestType(adapter.Type) != "openai" || !adapter.OpenAIExtraParamsEnabled {
		return ""
	}
	return strings.TrimSpace(adapter.OpenAIExtraParamsJSON)
}

func normalizeModelAdapterTestCustomHeadersJSON(adapter serverconfig.ModelAdapterConfig) string {
	if !adapter.CustomHeadersEnabled {
		return ""
	}
	return strings.TrimSpace(adapter.CustomHeadersJSON)
}

func normalizeModelAdapterTestAnthropicExtraParamsJSON(adapter serverconfig.ModelAdapterConfig) string {
	if normalizeModelAdapterTestType(adapter.Type) != "anthropic" || !adapter.AnthropicExtraParamsEnabled {
		return ""
	}
	return strings.TrimSpace(adapter.AnthropicExtraParamsJSON)
}

func normalizeModelAdapterTestInt(value int) int {
	if value <= 0 {
		return 0
	}
	return value
}

func maxFloat64(value float64, fallback float64) float64 {
	if value < fallback {
		return fallback
	}
	return value
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type openAIProbeCandidate struct {
	Endpoint     string
	RequestGroup string
}

// shouldTryEndpointFallback 判断错误是否属于 endpoint / 协议形态不支持的典型特征。
func shouldTryEndpointFallback(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "convert_request_failed") ||
		strings.Contains(msg, "not implemented") ||
		strings.Contains(msg, "endpoint_not_supported") ||
		strings.Contains(msg, "status=404") ||
		strings.Contains(msg, "status=501")
}

func buildOpenAIProbeCandidates(adapter serverconfig.ModelAdapterConfig) []openAIProbeCandidate {
	currentEndpoint := modelchannel.NormalizeOpenAIEndpoint("openai", adapter.OpenAIEndpoint)
	currentGroup := modelchannel.ResolveProtocolGroup(adapter.ProtocolMode, "openai", adapter.ModelID, adapter.BaseURL, currentEndpoint, firstNonEmptyTrimmed(adapter.ProtocolGroup, adapter.OpenAIRequestGroup))
	candidates := make([]openAIProbeCandidate, 0, 4)
	seen := map[string]struct{}{}
	appendCandidate := func(endpoint string, group string) {
		normalizedEndpoint := modelchannel.NormalizeOpenAIEndpoint("openai", endpoint)
		normalizedGroup := modelchannel.NormalizeOpenAIRequestGroup("openai", normalizedEndpoint, group)
		if normalizedEndpoint == "" || normalizedGroup == "" {
			return
		}
		key := normalizedEndpoint + "\n" + normalizedGroup
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, openAIProbeCandidate{Endpoint: normalizedEndpoint, RequestGroup: normalizedGroup})
	}

	appendCandidate(currentEndpoint, currentGroup)
	if currentGroup == modelchannel.OpenAIRequestGroupChatCompletions {
		appendCandidate(currentEndpoint, modelchannel.OpenAIRequestGroupChatCompletionsCompat)
	}
	if currentEndpoint == modelchannel.OpenAIEndpointCustom {
		return candidates
	}
	if currentEndpoint != modelchannel.OpenAIEndpointChatCompletions {
		appendCandidate(modelchannel.OpenAIEndpointChatCompletions, modelchannel.OpenAIRequestGroupChatCompletions)
	}
	appendCandidate(modelchannel.OpenAIEndpointChatCompletions, modelchannel.OpenAIRequestGroupChatCompletionsCompat)
	if currentEndpoint != modelchannel.OpenAIEndpointResponses {
		appendCandidate(modelchannel.OpenAIEndpointResponses, modelchannel.OpenAIRequestGroupResponses)
	}
	return candidates
}

func buildSuccessfulModelAdapterTestResult(adapterID string, requestHash string, startedAt time.Time, metrics *modelAdapterTestMetrics) (ModelAdapterTestResult, bool) {
	if metrics == nil {
		return ModelAdapterTestResult{}, false
	}
	if metrics.finishedAt.IsZero() {
		metrics.finishedAt = time.Now().UTC()
	}
	if metrics.firstTextTokenAt.IsZero() {
		return ModelAdapterTestResult{}, false
	}
	if metrics.firstResponseAt.IsZero() {
		metrics.firstResponseAt = metrics.firstTextTokenAt
	}
	outputTokens := metrics.outputTokens
	tokensEstimated := false
	if !metrics.outputProvided || outputTokens <= 0 {
		outputTokens = estimateBenchmarkTextTokens(metrics.text.String())
		tokensEstimated = true
	}
	firstTextTokenMS := metrics.firstTextTokenAt.Sub(startedAt).Milliseconds()
	if firstTextTokenMS < 0 {
		firstTextTokenMS = 0
	}
	firstResponseMS := metrics.firstResponseAt.Sub(startedAt).Milliseconds()
	if firstResponseMS < 0 {
		firstResponseMS = 0
	}
	totalDurationMS := metrics.finishedAt.Sub(startedAt).Milliseconds()
	if totalDurationMS < 0 {
		totalDurationMS = 0
	}
	tokensPerSecond := calculateGenerationTokensPerSecond(
		outputTokens,
		metrics.firstResponseAt,
		metrics.finishedAt,
	)
	visibleOutputTokens := estimateBenchmarkTextTokens(metrics.text.String())
	visibleTokensPerSecond := calculateVisibleTokensPerSecond(
		visibleOutputTokens,
		metrics.firstTextTokenAt,
		metrics.finishedAt,
	)
	result := ModelAdapterTestResult{
		AdapterID:               adapterID,
		RequestHash:             requestHash,
		Status:                  string(ModelAdapterTestStatusSuccess),
		TokensPerSecond:         tokensPerSecond,
		VisibleTokensPerSecond:  visibleTokensPerSecond,
		FirstResponseMS:         firstResponseMS,
		FirstTextTokenMS:        firstTextTokenMS,
		TotalDurationMS:         totalDurationMS,
		OutputTokens:            outputTokens,
		VisibleOutputTokens:     visibleOutputTokens,
		ReasoningTokens:         metrics.reasoningTokens,
		EffectiveThinkingEffort: strings.TrimSpace(metrics.effectiveThinkingEffort),
		TokensEstimated:         tokensEstimated,
		TestedAt:                time.Now().UTC().Format(time.RFC3339Nano),
		RawResponse:             strings.TrimSpace(metrics.rawResponse),
	}
	result.SummaryText = buildModelAdapterTestSummaryText(result)
	return result, true
}

// tryOpenAIEndpointFallback 在当前 endpoint/请求分组失败且错误符合"不支持"特征时，
// 依次探测候选协议组合。成功后静默保存新的 endpoint + request group 到用户配置。
func (s *ProxyService) tryOpenAIEndpointFallback(
	ctx context.Context,
	adapter serverconfig.ModelAdapterConfig,
	requestHash string,
	originalErr error,
) (ModelAdapterTestResult, bool) {
	if strings.TrimSpace(adapter.Type) != "openai" {
		return ModelAdapterTestResult{}, false
	}
	if modelchannel.NormalizeProtocolMode(adapter.ProtocolMode) == modelchannel.ProtocolModeFixed {
		return ModelAdapterTestResult{}, false
	}
	if !shouldTryEndpointFallback(originalErr) {
		return ModelAdapterTestResult{}, false
	}
	candidates := buildOpenAIProbeCandidates(adapter)
	if len(candidates) <= 1 {
		return ModelAdapterTestResult{}, false
	}
	for _, candidate := range candidates[1:] {
		fallbackCtx, cancel := context.WithTimeout(context.Background(), modelAdapterTestTimeout)
		fallbackAdapter := adapter
		fallbackAdapter.OpenAIEndpoint = candidate.Endpoint
		fallbackAdapter.OpenAIRequestGroup = candidate.RequestGroup
		fallbackAdapter.ProtocolGroup = candidate.RequestGroup
		candidateStartedAt := time.Now().UTC()
		metrics, retryErr := s.executeModelAdapterNonStreamingTest(fallbackCtx, fallbackAdapter)
		cancel()
		if retryErr != nil {
			continue
		}
		result, ok := buildSuccessfulModelAdapterTestResult(
			adapter.ID,
			requestHash,
			candidateStartedAt,
			metrics,
		)
		if !ok {
			continue
		}
		s.persistOpenAITransportOverride(adapter.ID, candidate.Endpoint, candidate.RequestGroup)
		return result, true
	}
	return ModelAdapterTestResult{}, false
}

func calculateGenerationTokensPerSecond(
	outputTokens int64,
	firstResponseAt time.Time,
	finishedAt time.Time,
) float64 {
	if outputTokens <= 0 || firstResponseAt.IsZero() || finishedAt.IsZero() {
		return 0
	}

	generationDuration := finishedAt.Sub(firstResponseAt)
	if generationDuration <= 0 {
		return 0
	}
	return float64(outputTokens) / generationDuration.Seconds()
}

func calculateVisibleTokensPerSecond(visibleOutputTokens int64, firstTextTokenAt time.Time, finishedAt time.Time) float64 {
	if visibleOutputTokens <= 0 || firstTextTokenAt.IsZero() || finishedAt.IsZero() {
		return 0
	}
	visibleDuration := finishedAt.Sub(firstTextTokenAt)
	if visibleDuration <= 0 {
		return 0
	}
	return float64(visibleOutputTokens) / visibleDuration.Seconds()
}

func isBenchmarkEffectiveResponseEvent(event modeladapter.ModelEvent) bool {
	switch event.Kind {
	case modeladapter.ModelEventKindTextDelta,
		modeladapter.ModelEventKindThinkingDelta,
		modeladapter.ModelEventKindPartialToolCall,
		modeladapter.ModelEventKindToolCallDelta,
		modeladapter.ModelEventKindToolLikeCompleted:
		return true
	default:
		return false
	}
}

// persistOpenAITransportOverride 把单个 adapter 的 endpoint / request group 静默写回配置文件。
func (s *ProxyService) persistOpenAITransportOverride(adapterID string, endpoint string, requestGroup string) {
	if s == nil || strings.TrimSpace(adapterID) == "" || strings.TrimSpace(endpoint) == "" {
		return
	}
	cfg, err := s.LoadUserConfig()
	if err != nil || len(cfg.ModelAdapters) == 0 {
		return
	}
	changed := false
	for index := range cfg.ModelAdapters {
		item := &cfg.ModelAdapters[index]
		if strings.TrimSpace(item.ID) != strings.TrimSpace(adapterID) {
			continue
		}
		if modelchannel.NormalizeProtocolMode(item.ProtocolMode) == modelchannel.ProtocolModeFixed ||
			strings.TrimSpace(item.OpenAIEndpoint) == modelchannel.OpenAIEndpointCustom {
			return
		}
		if strings.TrimSpace(item.OpenAIEndpoint) != strings.TrimSpace(endpoint) {
			item.OpenAIEndpoint = endpoint
			changed = true
		}
		normalizedGroup := modelchannel.NormalizeOpenAIRequestGroup("openai", endpoint, requestGroup)
		if normalizedGroup != "" && strings.TrimSpace(item.OpenAIRequestGroup) != normalizedGroup {
			item.OpenAIRequestGroup = normalizedGroup
			changed = true
		}
		if normalizedGroup != "" && strings.TrimSpace(item.ProtocolGroup) != normalizedGroup {
			item.ProtocolGroup = normalizedGroup
			changed = true
		}
		break
	}
	if !changed {
		return
	}
	if err := s.SaveUserConfig(cfg); err != nil {
		fmt.Printf("model adapter transport fallback persist failed adapter=%s endpoint=%s group=%s err=%v\n", adapterID, endpoint, requestGroup, err)
	}
}
