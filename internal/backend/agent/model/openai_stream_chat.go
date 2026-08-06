// openai_stream_chat.go 承载 OpenAI chat completions 流式实现：SSE 解析、
// tool 参数累积与进度事件、usage 快照与错误归一化。
package modeladapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/modelchannel"
)

func (adapter *OpenAIAdapter) streamChatCompletions(ctx context.Context, req StreamRequest, baseURL string, apiKey string, modelID string, promptCacheKeyMaximumLength int, manualPromptCacheKey bool, sink func(ModelEvent) error) error {
	startedAt := time.Now().UTC()
	finishedAt := time.Time{}
	overrideBody := cloneRequestBodyOverride(req.RequestBodyOverride)
	var body any = overrideBody
	if len(overrideBody) == 0 {
		normalizedMessages, err := normalizeOpenAIProviderMessages(req.Messages, strings.TrimSpace(req.ReasoningEffort) != "", isKimiOpenAIRequest(baseURL, modelID))
		if err != nil {
			finishedAt = time.Now().UTC()
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
			return err
		}
		requestBody := openAIChatRequestBody(req, modelID, promptCacheKeyMaximumLength)
		requestBody.Messages = normalizedMessages
		if len(req.Tools) > 0 {
			tools, err := normalizeOpenAIChatTools(req.Tools)
			if err != nil {
				finishedAt = time.Now().UTC()
				recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
				return err
			}
			requestBody.Tools = tools
		}
		body = requestBody
	} else {
		if !openAIChatRequestGroupUsesCompatShape(req.OpenAIRequestGroup) {

			applyOpenAIPromptCacheKeyOverride(overrideBody, req, modelID, promptCacheKeyMaximumLength)
		}
	}
	bodyMap, err := requestBodyToMap(body)
	if err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}
	applyOpenAIThinkingDisable(bodyMap, req, baseURL, modelID, req.OpenAIEndpoint)
	if err := ApplyOpenAIExtraParams(bodyMap, req.OpenAIExtraParamsEnabled, req.OpenAIExtraParamsJSON); err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}
	normalizeOpenAIRequestToolSchemas(bodyMap)
	applyOpenAIChatCompletionsCompatibility(bodyMap, baseURL, modelID, manualPromptCacheKey)
	if modelchannel.OpenAIRequestGroupSupportsAdvancedFields(req.OpenAIRequestGroup) {
		applyOpenAIServiceTier(bodyMap, req)
	}

	body = bodyMap
	requestURL := OpenAIEndpointURL(baseURL, req.OpenAIEndpoint)
	recordLLMRequestArtifact(req, "openai", modelID, "POST", requestURL, body)

	payload, err := json.Marshal(body)
	if err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}

	streamCtx, streamIdle := newProviderStreamIdleWatchdog(ctx, req.ProviderStreamIdleTimeout)
	defer streamIdle.Stop()

	resp, err := doProviderRequestWithGzipFallback(streamCtx, adapter.client, "openai", req.RequestID, req.ModelCallID, payload, requestURL, func(httpReq *http.Request) error {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("User-Agent", ClaudeCodeUserAgent)
		if err := ApplyCustomHeaders(httpReq, req.CustomHeadersEnabled, req.CustomHeadersJSON); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if idleErr := streamIdle.Err(); idleErr != nil {
			err = idleErr
		}
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}
	streamIdle.AttachBody(resp.Body)
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = buildHTTPStatusError("openai adapter", resp)
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}

	type openAIToolCallDelta struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	type openAIChunk struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Error     *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error,omitempty"`
		Choices []struct {
			Delta struct {
				Content          string                `json:"content"`
				ReasoningContent string                `json:"reasoning_content"`
				Reasoning        json.RawMessage       `json:"reasoning"`
				ReasoningDetails json.RawMessage       `json:"reasoning_details"`
				ToolCalls        []openAIToolCallDelta `json:"tool_calls"`
			} `json:"delta"`

			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Model string `json:"model"`
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			// DeepSeek 在顶层返回 prompt_cache_hit_tokens / prompt_cache_miss_tokens。
			PromptCacheHitTokens  int64 `json:"prompt_cache_hit_tokens"`
			PromptCacheMissTokens int64 `json:"prompt_cache_miss_tokens"`
			PromptTokensDetails   *struct {
				// OpenAI / MiMo 在嵌套结构里返回 cached_tokens。
				CachedTokens int64 `json:"cached_tokens"`
				// 部分中转（如包装 claude 的 OpenAI 兼容接口）在嵌套结构里返回 cache_creation_tokens，
				// 表示写入缓存的 token 数。对齐 anthropic.go 的语义记入 cacheWriteTokens。
				CacheCreationTokens int64 `json:"cache_creation_tokens"`
			} `json:"prompt_tokens_details,omitempty"`
		} `json:"usage,omitempty"`
	}

	tools := make(map[int]*openAIToolAccumulator)
	currentModel := modelID
	inputTokens := int64(0)
	outputTokens := int64(0)
	cacheReadTokens := int64(0)
	cacheWriteTokens := int64(0)
	usagePresent := false
	cacheReadPresent := false
	cacheWritePresent := false
	firstEventAt := time.Time{}
	finishReason := ""
	turnFinishedPending := false
	streamTerminated := false
	thinkingStarted := time.Time{}
	thinkingActive := false
	thinkParser := &openAIThinkTagParser{}
	flushThinkingCompleted := func() error {
		if !thinkingActive {
			return nil
		}
		duration := int32(time.Since(thinkingStarted).Milliseconds())
		if duration < 0 {
			duration = 0
		}
		if err := sink(ModelEvent{
			Kind:               ModelEventKindThinkingCompleted,
			OccurredAt:         time.Now().UTC(),
			Provider:           "openai",
			Model:              currentModel,
			ThinkingDurationMS: duration,
		}); err != nil {
			return err
		}
		thinkingActive = false
		thinkingStarted = time.Time{}
		return nil
	}
	flushTurnFinished := func() error {
		if !turnFinishedPending {
			return nil
		}
		turnFinishedPending = false
		return sink(ModelEvent{
			Kind:              ModelEventKindTurnFinished,
			OccurredAt:        time.Now().UTC(),
			Provider:          "openai",
			Model:             currentModel,
			InputTokens:       inputTokens,
			OutputTokens:      outputTokens,
			CacheReadTokens:   cacheReadTokens,
			CacheWriteTokens:  cacheWriteTokens,
			UsagePresent:      usagePresent,
			CacheReadPresent:  cacheReadPresent,
			CacheWritePresent: cacheWritePresent,
			FinishReason:      finishReason,
		})
	}
	emitTextDelta := func(text string) error {
		if text == "" {
			return nil
		}
		streamIdle.MarkEffectiveContent()
		return sink(ModelEvent{
			Kind:       ModelEventKindTextDelta,
			OccurredAt: time.Now().UTC(),
			Provider:   "openai",
			Model:      currentModel,
			Text:       text,
		})
	}

	emitThinkingDelta := func(reasoning string) error {
		if reasoning == "" {
			return nil
		}
		streamIdle.MarkEffectiveContent()
		if !thinkingActive {
			thinkingStarted = time.Now()
			thinkingActive = true
		}
		return sink(ModelEvent{
			Kind:          ModelEventKindThinkingDelta,
			OccurredAt:    time.Now().UTC(),
			Provider:      "openai",
			Model:         currentModel,
			Text:          reasoning,
			ThinkingStyle: agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT,
		})
	}
	emitTaggedContentParts := func(parts []openAIContentPart) error {
		for _, part := range parts {
			switch part.Kind {
			case openAIContentPartText:
				if err := emitTextDelta(part.Text); err != nil {
					return err
				}
			case openAIContentPartReasoning:
				if err := emitThinkingDelta(part.Text); err != nil {
					return err
				}
			case openAIContentPartThinkingCompleted:
				if err := flushThinkingCompleted(); err != nil {
					return err
				}
			}
		}
		return nil
	}
	flushTaggedContentTail := func() error {
		return emitTaggedContentParts(thinkParser.Flush())
	}
	fail := func(streamErr error) error {
		finishedAt = time.Now().UTC()
		logProviderStreamTiming("openai", currentModel, req, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, streamErr)
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", currentModel, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, streamErr))
		return streamErr
	}
	errorFromChunk := func(chunk openAIChunk) error {
		finishReason = "error"
		if chunk.Error != nil {
			parts := make([]string, 0, 4)
			if value := strings.TrimSpace(chunk.Error.Type); value != "" {
				parts = append(parts, "type="+value)
			}
			if value := strings.TrimSpace(chunk.Error.Code); value != "" {
				parts = append(parts, "code="+value)
			}
			if value := strings.TrimSpace(chunk.RequestID); value != "" {
				parts = append(parts, "request_id="+value)
			}
			if message := strings.TrimSpace(chunk.Error.Message); message != "" {
				if len(parts) > 0 {
					return fmt.Errorf("openai chat stream error %s: %s", strings.Join(parts, " "), message)
				}
				return fmt.Errorf("openai chat stream error: %s", message)
			}
			if len(parts) > 0 {
				return fmt.Errorf("openai chat stream error %s", strings.Join(parts, " "))
			}
		}
		return fmt.Errorf("openai chat stream error")
	}
	applyUsage := func(usage *struct {
		PromptTokens          int64 `json:"prompt_tokens"`
		CompletionTokens      int64 `json:"completion_tokens"`
		PromptCacheHitTokens  int64 `json:"prompt_cache_hit_tokens"`
		PromptCacheMissTokens int64 `json:"prompt_cache_miss_tokens"`
		PromptTokensDetails   *struct {
			CachedTokens        int64 `json:"cached_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_tokens"`
		} `json:"prompt_tokens_details,omitempty"`
	}) {
		if usage == nil {
			return
		}
		usagePresent = true
		promptTokens := usage.PromptTokens

		// 合并 DeepSeek 顶层格式和 OpenAI/MiMo 嵌套格式（参照 Reasonix normaliseUsage）。
		// DeepSeek: prompt_cache_hit_tokens / prompt_cache_miss_tokens（顶层）
		// OpenAI/MiMo: prompt_tokens_details.cached_tokens（嵌套）
		// 哪边非零用哪边。
		cachedTokens := int64(0)
		if usage.PromptCacheHitTokens > 0 {
			cacheReadPresent = true
			cachedTokens = usage.PromptCacheHitTokens
		} else if usage.PromptTokensDetails != nil {
			cacheReadPresent = true
			cachedTokens = usage.PromptTokensDetails.CachedTokens
		}

		// cache miss：DeepSeek 显式返回；OpenAI 无此字段时从 prompt-hit 推导。
		missTokens := int64(0)
		if usage.PromptCacheMissTokens > 0 {
			missTokens = usage.PromptCacheMissTokens
		} else if cachedTokens > 0 && promptTokens > cachedTokens {
			missTokens = promptTokens - cachedTokens
		}

		if promptTokens < 0 {
			promptTokens = 0
		}
		if cachedTokens < 0 {
			cachedTokens = 0
		}
		if cachedTokens > promptTokens {
			cachedTokens = promptTokens
		}
		inputTokens = missTokens
		if inputTokens <= 0 {
			inputTokens = promptTokens - cachedTokens
		}
		outputTokens = maxInt64(usage.CompletionTokens, 0)
		cacheReadTokens = cachedTokens
		// cache write：原生 OpenAI 协议无此概念（保持 0）；但包装 claude 的 OpenAI 兼容中转
		// 会在 prompt_tokens_details.cache_creation_tokens 返回缓存写入量，对齐 anthropic.go
		// 的语义（cache_creation → cacheWrite）记入 cacheWriteTokens，让统计准确。
		if usage.PromptTokensDetails != nil && usage.PromptTokensDetails.CacheCreationTokens > 0 {
			cacheWriteTokens = maxInt64(usage.PromptTokensDetails.CacheCreationTokens, 0)
		} else {
			cacheWriteTokens = 0
		}
		cacheWritePresent = true
	}
	// A2 SSE 逐块读超时：每次 Scan 前设置 30s 读 deadline，块到达后清除（不累积）。
	// 底层连接不支持 SetReadDeadline 时静默 fallback，行为与原来一致。
	// chunkTimedOut 记录本轮是否发生过逐块读超时；是则把扫描错误转为可触发
	// pre-output 重连的读超时错误（见下方 scanner.Err 处理）。
	var chunkTimedOut bool
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), openAIStreamMaxTokenSize)
	for {
		disarm := func() bool { return false }
		if d, ok := resetStreamReadDeadline(resp); ok {
			disarm = d
		}
		scanOK := scanner.Scan()
		chunkTimedOut = chunkTimedOut || disarm()
		if !scanOK {
			break
		}
		rawLine := scanner.Text()
		_, _ = appendLLMResponseArtifact(req, redactOpenAIStreamArtifactLine(rawLine)+"\n")
		line := strings.TrimSpace(rawLine)
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		if firstEventAt.IsZero() {
			firstEventAt = time.Now().UTC()
		}
		payloadLine := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payloadLine == "[DONE]" {
			streamTerminated = true
			if err := flushTaggedContentTail(); err != nil {
				return fail(err)
			}
			if err := flushThinkingCompleted(); err != nil {
				return fail(err)
			}
			if err := flushTurnFinished(); err != nil {
				return fail(err)
			}
			break
		}

		var chunk openAIChunk
		if err := json.Unmarshal([]byte(payloadLine), &chunk); err != nil {
			return fail(err)
		}
		if strings.TrimSpace(chunk.Type) == "error" || chunk.Error != nil {
			return fail(errorFromChunk(chunk))
		}
		if len(chunk.Choices) == 0 {
			if strings.TrimSpace(chunk.Model) != "" {
				currentModel = strings.TrimSpace(chunk.Model)
			}
			applyUsage(chunk.Usage)
			if err := flushTaggedContentTail(); err != nil {
				return fail(err)
			}
			if err := flushThinkingCompleted(); err != nil {
				return fail(err)
			}
			if err := flushTurnFinished(); err != nil {
				return fail(err)
			}
			continue
		}
		choice := chunk.Choices[0]
		if strings.TrimSpace(chunk.Model) != "" {
			currentModel = strings.TrimSpace(chunk.Model)
		}
		applyUsage(chunk.Usage)

		// Kimi K2 系列在思考阶段会把同一片段同时放进 content 与 reasoning_content，
		// 导致思考内容被当作正常文本重复输出。此处识别这种重复并跳过 content。
		reasoning := openAIChatDeltaReasoningText(choice.Delta.ReasoningContent, choice.Delta.Reasoning, choice.Delta.ReasoningDetails)
		if reasoning != "" {
			if err := emitThinkingDelta(reasoning); err != nil {
				return fail(err)
			}
		}
		// 当本片段同时携带 reasoning 与 content，且二者相同时，视为思考阶段的重复输出，跳过 content。
		skipContent := reasoning != "" && choice.Delta.Content == choice.Delta.ReasoningContent && choice.Delta.ReasoningContent != ""
		if text := choice.Delta.Content; text != "" && !skipContent {
			if err := emitTaggedContentParts(thinkParser.Consume(text)); err != nil {
				return fail(err)
			}
		}

		if len(choice.Delta.ToolCalls) > 0 && choice.Delta.Content == "" && choice.Delta.ReasoningContent == "" {
			if err := flushTaggedContentTail(); err != nil {
				return fail(err)
			}
			if err := flushThinkingCompleted(); err != nil {
				return fail(err)
			}
		}
		for _, item := range choice.Delta.ToolCalls {
			streamIdle.MarkEffectiveContent()
			accumulator, ok := tools[item.Index]
			if !ok {
				accumulator = &openAIToolAccumulator{}
				tools[item.Index] = accumulator
			}
			if strings.TrimSpace(item.ID) != "" {
				accumulator.CallID = namespaceToolCallID(req.ModelCallID, item.ID)
			}
			if strings.TrimSpace(item.Function.Name) != "" {
				accumulator.Name = strings.TrimSpace(item.Function.Name)
			}
			argsTextDelta := ""
			if item.Function.Arguments != "" {
				_, _ = accumulator.Args.WriteString(item.Function.Arguments)
				argsTextDelta = item.Function.Arguments
			}
			if argsTextDelta != "" || (strings.TrimSpace(accumulator.Name) == "CreatePlan" && accumulator.Args.Len() > 0) {
				if err := emitOpenAIToolProgress(sink, currentModel, accumulator, argsTextDelta); err != nil {
					return fail(err)
				}
			}
		}

		if choice.FinishReason != nil {
			streamTerminated = true
			if err := flushTaggedContentTail(); err != nil {
				return fail(err)
			}
			if err := flushThinkingCompleted(); err != nil {
				return fail(err)
			}
			for _, accumulator := range tools {
				argsJSON, argsErr := completedOpenAIToolArgsJSON(accumulator)
				if argsErr != nil {
					return fail(argsErr)
				}
				if err := sink(ModelEvent{
					Kind:       ModelEventKindToolLikeCompleted,
					OccurredAt: time.Now().UTC(),
					Provider:   "openai",
					Model:      currentModel,
					ToolInvocation: &runtimecore.ToolInvocation{
						CallID:   strings.TrimSpace(accumulator.CallID),
						ToolName: strings.TrimSpace(accumulator.Name),
						ArgsJSON: argsJSON,
					},
				}); err != nil {
					return fail(err)
				}
				streamIdle.MarkEffectiveContent()
			}
			tools = make(map[int]*openAIToolAccumulator)
			finishReason = strings.TrimSpace(*choice.FinishReason)
			turnFinishedPending = true
		}
	}
	for _, accumulator := range tools {
		argsJSON, argsErr := completedOpenAIToolArgsJSON(accumulator)
		if argsErr != nil {
			return fail(argsErr)
		}
		if err := sink(ModelEvent{
			Kind:       ModelEventKindToolLikeCompleted,
			OccurredAt: time.Now().UTC(),
			Provider:   "openai",
			Model:      currentModel,
			ToolInvocation: &runtimecore.ToolInvocation{
				CallID:   strings.TrimSpace(accumulator.CallID),
				ToolName: strings.TrimSpace(accumulator.Name),
				ArgsJSON: argsJSON,
			},
		}); err != nil {
			return fail(err)
		}
		streamIdle.MarkEffectiveContent()
	}
	if err := scanner.Err(); err != nil {
		if chunkTimedOut {
			err = streamChunkTimeoutError()
		}
		if idleErr := streamIdle.Err(); idleErr != nil {
			return fail(idleErr)
		}
		return fail(err)
	}
	if chunkTimedOut {
		return fail(streamChunkTimeoutError())
	}
	if !streamTerminated {
		return fail(fmt.Errorf("provider stream ended before terminal event"))
	}
	if err := flushTaggedContentTail(); err != nil {
		return fail(err)
	}
	if err := flushThinkingCompleted(); err != nil {
		return fail(err)
	}
	if err := flushTurnFinished(); err != nil {
		return fail(err)
	}
	finishedAt = time.Now().UTC()
	logProviderStreamTiming("openai", currentModel, req, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, nil)
	recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", currentModel, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, nil))
	return nil
}
