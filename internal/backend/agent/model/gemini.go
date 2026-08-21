package modeladapter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/modelchannel"
	"cursor/internal/netproxy"
)

type GeminiAdapter struct {
	client *http.Client
}

var geminiStreamBufferPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

func NewGeminiAdapter() *GeminiAdapter {
	return &GeminiAdapter{client: netproxy.NewHTTPClient(0)}
}

func (adapter *GeminiAdapter) Stream(ctx context.Context, req StreamRequest, sink func(ModelEvent) error) error {
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("gemini base url is empty")
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		return fmt.Errorf("gemini api key is empty")
	}
	modelID := strings.TrimSpace(req.ProviderModelID)
	if modelID == "" {
		modelID = strings.TrimSpace(req.ModelID)
	}
	if modelID == "" {
		return fmt.Errorf("gemini model id is empty")
	}

	startedAt := time.Now().UTC()
	finishedAt := time.Time{}
	body := cloneRequestBodyOverride(req.RequestBodyOverride)
	if len(body) == 0 {
		var err error
		body, err = geminiRequestBody(req, modelID)
		if err != nil {
			finishedAt = time.Now().UTC()
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "gemini", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
			return err
		}
	}
	applyProviderCompatibilitySanitization(body, baseURL, modelID)
	requestURL := geminiEndpointURL(baseURL, modelID, true)
	recordLLMRequestArtifact(req, "gemini", modelID, "POST", requestURL, body)

	payload, err := json.Marshal(body)
	if err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "gemini", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}

	err = streamWithReconnect(ctx, sink, requestReplaySafety(req), func(_ int, wrappedSink func(ModelEvent) error) error {
		streamCtx, streamIdle := newProviderStreamIdleWatchdog(ctx, req.ProviderStreamIdleTimeout)
		defer streamIdle.Stop()
		resp, reqErr := doProviderRequestWithGzipFallback(streamCtx, adapter.client, "gemini", req.RequestID, req.ModelCallID, payload, requestURL, func(httpReq *http.Request) error {
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("User-Agent", ClaudeCodeUserAgent)
			httpReq.Header.Set("x-goog-api-key", apiKey)
			if err := ApplyCustomHeaders(httpReq, req.CustomHeadersEnabled, req.CustomHeadersJSON); err != nil {
				return err
			}
			return nil
		})
		if reqErr != nil {
			if idleErr := streamIdle.Err(); idleErr != nil {
				reqErr = idleErr
			}
			finishedAt = time.Now().UTC()
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "gemini", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, reqErr))
			return reqErr
		}
		streamIdle.AttachBody(resp.Body)
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			reqErr = buildHTTPStatusError("gemini adapter", resp)
			finishedAt = time.Now().UTC()
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "gemini", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, reqErr))
			return reqErr
		}
		inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, parseErr := adapter.streamGeminiEvents(resp, req, modelID, startedAt, streamIdle, wrappedSink)
		finishedAt = time.Now().UTC()
		if parseErr != nil {
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "gemini", modelID, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, 0, parseErr))
			return parseErr
		}
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "gemini", modelID, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, 0, nil))
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func geminiEndpointURL(baseURL string, modelID string, stream bool) string {
	plan, err := modelchannel.ResolveTransportPlan(modelchannel.TransportPlanInput{
		Provider:     "gemini",
		BaseURL:      baseURL,
		ModelID:      modelID,
		ProtocolMode: modelchannel.ProtocolModeFixed,
		Stream:       stream,
	})
	if err != nil {
		return strings.TrimSpace(baseURL)
	}
	return plan.RequestURL
}

func geminiRequestBody(req StreamRequest, modelID string) (map[string]any, error) {
	systemParts := make([]map[string]any, 0, 1)
	contents := make([]map[string]any, 0, len(req.Messages))
	providerCallIDs := make(map[string]string)
	for _, message := range req.Messages {
		for _, call := range message.ToolCalls {
			if internalID := strings.TrimSpace(call.ID); internalID != "" {
				if providerID := strings.TrimSpace(call.OpenAIResponsesCallID); providerID != "" {
					providerCallIDs[internalID] = providerID
				}
			}
		}
	}
	for _, message := range req.Messages {
		role := strings.TrimSpace(message.Role)
		if role == "system" {
			if text := strings.TrimSpace(protocolMessageText(message)); text != "" {
				systemParts = append(systemParts, map[string]any{"text": text})
			}
			continue
		}
		parts, err := geminiMessagePartsWithProviderCallIDs(message, providerCallIDs)
		if err != nil {
			return nil, err
		}
		if len(parts) == 0 {
			continue
		}
		contents = append(contents, map[string]any{"role": geminiRole(role), "parts": parts})
	}
	body := map[string]any{"contents": contents}
	if len(systemParts) > 0 {
		body["systemInstruction"] = map[string]any{"parts": systemParts}
	}
	generationConfig := map[string]any{}
	if req.MaxTokens > 0 {
		generationConfig["maxOutputTokens"] = req.MaxTokens
	}
	if effort := strings.TrimSpace(req.ReasoningEffort); effort != "" {
		generationConfig["thinkingConfig"] = map[string]any{"thinkingBudget": geminiThinkingBudget(effort)}
	}
	if len(generationConfig) > 0 {
		body["generationConfig"] = generationConfig
	}
	if len(req.Tools) > 0 {
		declarations, err := geminiFunctionDeclarations(req.Tools)
		if err != nil {
			return nil, err
		}
		if len(declarations) > 0 {
			body["tools"] = []any{map[string]any{"functionDeclarations": declarations}}
		}
	}
	return body, nil
}

func geminiRole(role string) string {
	switch strings.TrimSpace(role) {
	case "assistant":
		return "model"
	default:
		return "user"
	}
}

func geminiMessageParts(message Message) ([]map[string]any, error) {
	return geminiMessagePartsWithProviderCallIDs(message, nil)
}

func geminiMessagePartsWithProviderCallIDs(message Message, providerCallIDs map[string]string) ([]map[string]any, error) {
	role := strings.TrimSpace(message.Role)
	parts := make([]map[string]any, 0, len(message.ContentParts)+len(message.ToolCalls)+1)
	if role == "tool" {
		name := strings.TrimSpace(message.Name)
		if name == "" {
			name = "tool"
		}
		functionResponse := map[string]any{"name": name, "response": map[string]any{"content": protocolMessageText(message)}}
		if providerID := strings.TrimSpace(providerCallIDs[strings.TrimSpace(message.ToolCallID)]); providerID != "" {
			functionResponse["id"] = providerID
		}
		parts = append(parts, map[string]any{"functionResponse": functionResponse})
		return parts, nil
	}
	if len(message.ContentParts) == 0 && strings.TrimSpace(message.Content) != "" {
		parts = append(parts, map[string]any{"text": message.Content})
	}
	for _, part := range message.ContentParts {
		switch normalizeContentPartType(part.Type) {
		case contentPartTypeText:
			if part.Text != "" {
				parts = append(parts, map[string]any{"text": part.Text})
			}
		case contentPartTypeImage:
			payload, mediaType, err := resolveImageContent(part.Image)
			if err != nil {
				return nil, err
			}
			parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": mediaType, "data": base64.StdEncoding.EncodeToString(payload)}})
		default:
			return nil, fmt.Errorf("unsupported gemini content part type: %s", strings.TrimSpace(part.Type))
		}
	}
	for _, call := range message.ToolCalls {
		args := map[string]any{}
		if strings.TrimSpace(call.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
				return nil, fmt.Errorf("decode gemini tool call arguments for %q: %w", strings.TrimSpace(call.Function.Name), err)
			}
		}
		functionCall := map[string]any{"name": strings.TrimSpace(call.Function.Name), "args": args}
		if providerID := strings.TrimSpace(call.OpenAIResponsesCallID); providerID != "" {
			functionCall["id"] = providerID
		}
		parts = append(parts, map[string]any{"functionCall": functionCall})
	}
	return parts, nil
}

func geminiFunctionDeclarations(items []json.RawMessage) ([]map[string]any, error) {
	declarations := make([]map[string]any, 0, len(items))
	seen := map[string]struct{}{}
	for _, raw := range items {
		var tool map[string]any
		if err := json.Unmarshal(raw, &tool); err != nil {
			return nil, fmt.Errorf("decode gemini tool: %w", err)
		}
		if !normalizeOpenAIToolDescriptor(tool) {
			continue
		}
		var source map[string]any
		if fn, ok := tool["function"].(map[string]any); ok {
			source = fn
		} else {
			source = tool
		}
		name := strings.TrimSpace(asStringMapValue(source, "name"))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		declaration := map[string]any{"name": name}
		if description := strings.TrimSpace(asStringMapValue(source, "description")); description != "" {
			declaration["description"] = description
		}
		if parameters, ok := source["parameters"]; ok {
			declaration["parametersJsonSchema"] = parameters
		}
		declarations = append(declarations, declaration)
	}
	return declarations, nil
}

func geminiThinkingBudget(effort string) int {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "disabled", "disable", "off", "none", "false", "0":
		return 0
	case "low":
		return 2048
	case "medium":
		return 8192
	case "high":
		return 16384
	case "xhigh", "max":
		return 24576
	default:
		return 8192
	}
}

type geminiStreamResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text             string         `json:"text"`
				Thought          bool           `json:"thought"`
				ThoughtSignature string         `json:"thoughtSignature"`
				FunctionCall     map[string]any `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount        *int64 `json:"promptTokenCount"`
		CandidatesTokenCount    *int64 `json:"candidatesTokenCount"`
		CachedContentTokenCount *int64 `json:"cachedContentTokenCount"`
		ThoughtsTokenCount      *int64 `json:"thoughtsTokenCount"`
		ToolUsePromptTokenCount *int64 `json:"toolUsePromptTokenCount"`
		TotalTokenCount         *int64 `json:"totalTokenCount"`
	} `json:"usageMetadata,omitempty"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

type geminiContentAccumulator struct {
	emitted string
}

func (accumulator *geminiContentAccumulator) Delta(value string, _ bool) string {
	if accumulator == nil || value == "" {
		return ""
	}
	accumulator.emitted += value
	return value
}

func (adapter *GeminiAdapter) streamGeminiEvents(resp *http.Response, req StreamRequest, modelID string, startedAt time.Time, streamIdle *providerStreamIdleWatchdog, sink func(ModelEvent) error) (int64, int64, int64, string, time.Time, error) {
	_ = adapter
	_ = startedAt
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	inputTokens, outputTokens, cacheReadTokens := int64(0), int64(0), int64(0)
	promptTokens, candidateTokens, reasoningTokens, toolUsePromptTokens := int64(0), int64(0), int64(0), int64(0)
	usagePresent := false
	cacheReadPresent := false
	finishReason := ""
	firstEventAt := time.Time{}
	textAccumulator := &geminiContentAccumulator{}
	thinkingAccumulator := &geminiContentAccumulator{}
	emittedTools := map[string]struct{}{}
	// sawTerminalCandidate 标记是否收到带 finishReason 的完整候选。
	// Gemini 原生协议完整响应必然带 finishReason；EOF 前从未出现则说明流被截断。
	sawTerminalCandidate := false
	markFirst := func() {
		if firstEventAt.IsZero() {
			firstEventAt = time.Now().UTC()
		}
	}
	// A2 SSE 逐块读超时：每次 Scan 前设置读 deadline，块到达后清除（不累积）。
	// 超时阈值按请求空闲超时派生（委派/子代理流放宽，父代理保持 30s）。
	// 底层连接不支持 SetReadDeadline 时静默 fallback，行为与原来一致。
	// chunkTimedOut 记录本轮是否发生过逐块读超时；是则把扫描错误转为可触发
	// pre-output 重连的读超时错误（见下方 scanner.Err 处理）。
	var chunkTimedOut bool
	chunkReadTimeout := providerStreamChunkTimeout(req.ProviderStreamIdleTimeout)
	for {
		disarm := func() bool { return false }
		if d, ok := resetStreamReadDeadline(resp, chunkReadTimeout); ok {
			disarm = d
		}
		scanOK := scanner.Scan()
		chunkTimedOut = chunkTimedOut || disarm()
		if !scanOK {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk geminiStreamResponse
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, err
		}
		if chunk.Error != nil {
			return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, fmt.Errorf("gemini provider error: %s", strings.TrimSpace(chunk.Error.Message))
		}
		if chunk.UsageMetadata != nil {
			usagePresent = true
			if chunk.UsageMetadata.PromptTokenCount != nil {
				promptTokens = maxInt64(*chunk.UsageMetadata.PromptTokenCount, 0)
			}
			if chunk.UsageMetadata.CachedContentTokenCount != nil {
				cacheReadPresent = true
				cacheReadTokens = clampInt64(*chunk.UsageMetadata.CachedContentTokenCount, 0, promptTokens)
			}
			if chunk.UsageMetadata.ToolUsePromptTokenCount != nil {
				toolUsePromptTokens = maxInt64(*chunk.UsageMetadata.ToolUsePromptTokenCount, 0)
			}
			if chunk.UsageMetadata.CandidatesTokenCount != nil {
				candidateTokens = maxInt64(*chunk.UsageMetadata.CandidatesTokenCount, 0)
			}
			if chunk.UsageMetadata.ThoughtsTokenCount != nil {
				reasoningTokens = maxInt64(*chunk.UsageMetadata.ThoughtsTokenCount, 0)
			}
			inputTokens = maxInt64(promptTokens-cacheReadTokens, 0) + toolUsePromptTokens
			outputTokens = candidateTokens + reasoningTokens
			if chunk.UsageMetadata.TotalTokenCount != nil {
				reportedTotal := maxInt64(*chunk.UsageMetadata.TotalTokenCount, 0)
				accountedTotal := inputTokens + cacheReadTokens + outputTokens
				if reportedTotal > accountedTotal {
					outputTokens += reportedTotal - accountedTotal
				}
			}
		}
		if len(chunk.Candidates) == 0 {
			continue
		}
		candidate := chunk.Candidates[0]
		if candidate.FinishReason != "" {
			finishReason = geminiFinishReason(candidate.FinishReason)
			sawTerminalCandidate = true
		}
		textSnapshot := geminiStreamBufferPool.Get().(*bytes.Buffer)
		textSnapshot.Reset()
		thinkingSnapshot := geminiStreamBufferPool.Get().(*bytes.Buffer)
		thinkingSnapshot.Reset()
		for partIndex, part := range candidate.Content.Parts {
			if part.Text != "" {
				if part.Thought {
					thinkingSnapshot.WriteString(part.Text)
				} else {
					textSnapshot.WriteString(part.Text)
				}
			}
			if len(part.FunctionCall) > 0 {
				name := strings.TrimSpace(fmt.Sprint(part.FunctionCall["name"]))
				if name == "" {
					continue
				}
				args, err := json.Marshal(part.FunctionCall["args"])
				if err != nil {
					return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, fmt.Errorf("encode gemini tool call args for %q: %w", name, err)
				}
				providerCallID := ""
				if value, exists := part.FunctionCall["id"]; exists && value != nil {
					providerCallID = strings.TrimSpace(fmt.Sprint(value))
				}
				callID := stableGeminiToolCallID(req, name, string(args), partIndex, providerCallID)
				if _, seen := emittedTools[callID]; seen {
					continue
				}
				emittedTools[callID] = struct{}{}
				markFirst()
				streamIdle.MarkEffectiveContent()
				if err := sink(ModelEvent{Kind: ModelEventKindToolLikeCompleted, OccurredAt: time.Now().UTC(), Provider: "gemini", Model: modelID, ProviderCallID: providerCallID, ToolCallID: callID, ToolInvocation: &runtimecore.ToolInvocation{CallID: callID, ToolName: name, ArgsJSON: args, ProviderCallID: providerCallID}}); err != nil {
					return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, err
				}
			}
		}
		textValue := textSnapshot.String()
		thinkingValue := thinkingSnapshot.String()
		geminiStreamBufferPool.Put(textSnapshot)
		geminiStreamBufferPool.Put(thinkingSnapshot)
		terminalCandidate := candidate.FinishReason != ""
		if delta := thinkingAccumulator.Delta(thinkingValue, terminalCandidate); delta != "" {
			markFirst()
			streamIdle.MarkEffectiveContent()
			if err := sink(ModelEvent{Kind: ModelEventKindThinkingDelta, OccurredAt: time.Now().UTC(), Provider: "gemini", Model: modelID, ThinkingStyle: agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT, Text: delta}); err != nil {
				return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, err
			}
		}
		if delta := textAccumulator.Delta(textValue, terminalCandidate); delta != "" {
			markFirst()
			streamIdle.MarkEffectiveContent()
			if err := sink(ModelEvent{Kind: ModelEventKindTextDelta, OccurredAt: time.Now().UTC(), Provider: "gemini", Model: modelID, Text: delta}); err != nil {
				return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if chunkTimedOut {
			err = streamChunkTimeoutError()
		}
		if idleErr := streamIdle.Err(); idleErr != nil {
			return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, idleErr
		}
		return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, err
	}
	if chunkTimedOut {
		return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, streamChunkTimeoutError()
	}
	if !sawTerminalCandidate {
		if textAccumulator.emitted == "" && thinkingAccumulator.emitted == "" && len(emittedTools) == 0 {
			// 流提前结束且无任何输出：视为瞬时空响应，交回 router 重试。
			return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, fmt.Errorf("gemini stream ended before terminal candidate (no output)")
		}
		// 有部分输出但没有终态候选：诚实报告截断。内容已向客户端转发，
		// 附加 ErrMidStreamInterrupted 标记避免 router 整体重试造成重复输出。
		return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, midStreamInterruptedError(fmt.Errorf("gemini stream truncated before terminal candidate"))
	}
	if thinkingAccumulator.emitted != "" {
		if err := sink(ModelEvent{Kind: ModelEventKindThinkingCompleted, OccurredAt: time.Now().UTC(), Provider: "gemini", Model: modelID}); err != nil {
			return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, err
		}
	}
	if finishReason == "" {
		finishReason = "stop"
	}
	return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, sink(ModelEvent{Kind: ModelEventKindTurnFinished, OccurredAt: time.Now().UTC(), Provider: "gemini", Model: modelID, FinishReason: finishReason, InputTokens: inputTokens, OutputTokens: outputTokens, ReasoningTokens: reasoningTokens, CacheReadTokens: cacheReadTokens, UsagePresent: usagePresent, CacheReadPresent: cacheReadPresent})
}

func clampInt64(value, minimum, maximum int64) int64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func geminiFinishReason(reason string) string {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case "MAX_TOKENS":
		return "max_tokens"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "MALFORMED_FUNCTION_CALL":
		return "refusal"
	case "STOP", "":
		return "stop"
	default:
		return strings.ToLower(strings.TrimSpace(reason))
	}
}

func stableGeminiToolCallID(req StreamRequest, name string, args string, ordinal int, providerCallID string) string {
	base := strings.TrimSpace(req.ModelCallID) + ":" + strings.TrimSpace(providerCallID) + ":" + fmt.Sprintf("%d", ordinal) + ":" + strings.TrimSpace(name) + ":" + strings.TrimSpace(args)
	if strings.Trim(base, ":0") == "" {
		base = time.Now().UTC().Format(time.RFC3339Nano)
	}
	sum := sha256.Sum256([]byte(base))
	return "gemini_synth_" + hex.EncodeToString(sum[:])[:16]
}
