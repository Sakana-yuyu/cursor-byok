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
	"cursor/internal/logger"
	"cursor/internal/modelchannel"
)

func (adapter *OpenAIAdapter) streamChatCompletions(ctx context.Context, req StreamRequest, baseURL string, apiKey string, modelID string, promptCacheKeyMaximumLength int, manualPromptCacheKey bool, sink func(ModelEvent) error) error {
	startedAt := time.Now().UTC()
	finishedAt := time.Time{}
	overrideBody := cloneRequestBodyOverride(req.RequestBodyOverride)
	var bodyMap map[string]any
	if len(overrideBody) == 0 {
		built, err := buildOpenAIChatBodyMap(req, baseURL, modelID, promptCacheKeyMaximumLength)
		if err != nil {
			finishedAt = time.Now().UTC()
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
			return err
		}
		bodyMap = built
	} else {
		if !openAIChatRequestGroupUsesCompatShape(req.OpenAIRequestGroup) {
			applyOpenAIPromptCacheKeyOverride(overrideBody, req, modelID, promptCacheKeyMaximumLength)
		}
		bodyMap = overrideBody
	}
	applyOpenAIThinkingDisable(bodyMap, req, baseURL, modelID, req.OpenAIEndpoint)
	if err := ApplyOpenAIExtraParams(bodyMap, req.OpenAIExtraParamsEnabled, req.OpenAIExtraParamsJSON); err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}
	normalizeOpenAIRequestToolSchemas(bodyMap)
	applyOpenAIChatCompletionsCompatibility(bodyMap, baseURL, modelID, manualPromptCacheKey)
	admission, err := admitOpenAITools(bodyMap, false, req.Tools)
	if err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}
	req.ToolAdmission = admission
	if req.RequestKnobs != nil {
		req.RequestKnobs["tool_admission"] = admission.diagnostics()
	}
	if modelchannel.OpenAIRequestGroupSupportsAdvancedFields(req.OpenAIRequestGroup) {
		applyOpenAIServiceTier(bodyMap, req)
	}

	requestURL := OpenAIEndpointURL(baseURL, req.OpenAIEndpoint)
	recordLLMRequestArtifact(req, "openai", modelID, "POST", requestURL, bodyMap)

	payload, err := json.Marshal(bodyMap)
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
			// 部分供应商（如 MiniMax）把 arguments 按 JSON 对象下发而非字符串，
			// 这里用 RawMessage 接住双态，延迟到分片处理时再判定。
			Arguments json.RawMessage `json:"arguments"`
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
			PromptTokens            int64 `json:"prompt_tokens"`
			CompletionTokens        int64 `json:"completion_tokens"`
			CompletionTokensDetails *struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details,omitempty"`
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
	reasoningTokens := int64(0)
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
	// V-2：流内是否发出过结构化工具调用，用于 finish_reason 提升判定。
	emittedToolInvocation := false
	compatKind := classifyProviderCompatibility(baseURL, modelID).Kind
	textStripper := newDeepSeekSpecialTokenStripper(compatKind == "deepseek")
	argsStripper := newDeepSeekSpecialTokenStripper(compatKind == "deepseek")
	toolObjectArgs := make(map[int]map[string]any)
	toolArgsShardModes := make(map[int]string)
	lastToolIndex := -1
	finishPromotionLogged := false
	effectiveFinishReason := func() string {
		// 对齐 Responses 侧的终态提升逻辑：上游中转偶发在实际携带结构化
		// tool_calls 的回合误报 finish_reason=stop（或不带 finish_reason），
		// 下游会误判回合结束而跳过工具执行。流内已发出结构化工具调用且
		// 终态为空/stop 时提升为 tool_calls；仅提升、绝不降级。
		reason := strings.TrimSpace(finishReason)
		if emittedToolInvocation && (reason == "" || reason == "stop") {
			if !finishPromotionLogged {
				finishPromotionLogged = true
				logger.Info("openai chat 流 finish_reason 提升", "from", reason, "to", "tool_calls", "model_call_id", req.ModelCallID)
			}
			return "tool_calls"
		}
		return reason
	}
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
			ReasoningTokens:   reasoningTokens,
			CacheReadTokens:   cacheReadTokens,
			CacheWriteTokens:  cacheWriteTokens,
			UsagePresent:      usagePresent,
			CacheReadPresent:  cacheReadPresent,
			CacheWritePresent: cacheWritePresent,
			FinishReason:      effectiveFinishReason(),
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
	// routeXMLText 在 toolCallMode=xml_prompt 时接管文本增量出口（做 <tool_call>
	// 扫描与伪造结果剥离）；非 xml 模式保持 nil，文本仍直接走 emitTextDelta。
	var routeXMLText func(string) error
	emitTaggedContentParts := func(parts []openAIContentPart) error {
		for _, part := range parts {
			switch part.Kind {
			case openAIContentPartText:
				if routeXMLText != nil {
					if err := routeXMLText(part.Text); err != nil {
						return err
					}
					continue
				}
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
	// xml_prompt 模式：构建 in-band XML 工具协议扫描器并接管文本增量出口。
	// admission 已在上方 admitOpenAITools 构建（xml 模式下 body 无原生 tools，
	// admission 仅含 sourceTools 的名称映射），用于把模型输出的工具名解析回
	// 规范名并拒绝未声明工具。完成后发出的事件与原生 tool_calls 路径同构。
	var xmlScanner *xmlToolCallScanner
	if xmlToolCallPromptMode(req) {
		xmlScanner = newXMLToolCallScanner(req.ModelCallID, xmlToolNameAdmission(req.Tools))
		if req.RequestKnobs != nil {
			req.RequestKnobs["tool_call_mode"] = ToolCallModeXMLPrompt
		}
	}
	dispatchXMLScannerEvent := func(event xmlScannerEvent) error {
		if event.Call == nil {
			return emitTextDelta(event.Text)
		}
		if err := flushTaggedContentTail(); err != nil {
			return err
		}
		if err := flushThinkingCompleted(); err != nil {
			return err
		}
		if err := sink(ModelEvent{
			Kind:           ModelEventKindToolLikeCompleted,
			OccurredAt:     time.Now().UTC(),
			Provider:       "openai",
			Model:          currentModel,
			ToolInvocation: event.Call,
		}); err != nil {
			return err
		}
		emittedToolInvocation = true
		streamIdle.MarkEffectiveContent()
		return nil
	}
	if xmlScanner != nil {
		routeXMLText = func(text string) error {
			for _, event := range xmlScanner.Feed(text) {
				if err := dispatchXMLScannerEvent(event); err != nil {
					return err
				}
			}
			return nil
		}
	}
	flushXMLScanner := func() error {
		if xmlScanner == nil {
			return nil
		}
		for _, event := range xmlScanner.Flush() {
			if err := dispatchXMLScannerEvent(event); err != nil {
				return err
			}
		}
		return nil
	}
	fail := func(streamErr error) error {
		finishedAt = time.Now().UTC()
		logProviderStreamTiming("openai", currentModel, req, startedAt, firstEventAt, finishedAt, effectiveFinishReason(), inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, streamErr)
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", currentModel, startedAt, firstEventAt, finishedAt, effectiveFinishReason(), inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, streamErr))
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
		PromptTokens            int64 `json:"prompt_tokens"`
		CompletionTokens        int64 `json:"completion_tokens"`
		CompletionTokensDetails *struct {
			ReasoningTokens int64 `json:"reasoning_tokens"`
		} `json:"completion_tokens_details,omitempty"`
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
		if usage.CompletionTokensDetails != nil {
			reasoningTokens = maxInt64(usage.CompletionTokensDetails.ReasoningTokens, 0)
		}
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
	// A2 SSE 逐块读超时：每次 Scan 前设置读 deadline，块到达后清除（不累积）。
	// 超时阈值按请求空闲超时派生（委派/子代理流放宽，父代理保持 30s）。
	// 底层连接不支持 SetReadDeadline 时静默 fallback，行为与原来一致。
	// chunkTimedOut 记录本轮是否发生过逐块读超时；是则把扫描错误转为可触发
	// pre-output 重连的读超时错误（见下方 scanner.Err 处理）。
	var chunkTimedOut bool
	chunkReadTimeout := providerStreamChunkTimeout(req.ProviderStreamIdleTimeout)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), openAIStreamMaxTokenSize)
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
			if err := flushXMLScanner(); err != nil {
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
			if err := flushXMLScanner(); err != nil {
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
			// V-3：剥离偶发泄漏进正文的 DeepSeek 模板特殊 token（跨分片缓冲）。
			if stripped := textStripper.Feed(text); stripped != "" {
				if err := emitTaggedContentParts(thinkParser.Consume(stripped)); err != nil {
					return fail(err)
				}
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
			if providerName := strings.TrimSpace(item.Function.Name); providerName != "" {
				sourceName, admitted := req.ToolAdmission.ResolveFunction(providerName)
				if !admitted {
					return fail(toolAdmissionError("provider returned tool not advertised for this request"))
				}
				accumulator.Name = sourceName
			}
			argsTextDelta := ""
			if rawArgs := item.Function.Arguments; len(rawArgs) > 0 {
				trimmed := strings.TrimSpace(string(rawArgs))
				if trimmed != "" && trimmed != "null" && trimmed[0] == '{' {
					// V-1：MiniMax 等供应商把工具参数按 JSON 对象（而非字符串
					// 分片）下发，固定按字符串拼接会让终态 unmarshal 失败断流。
					// 对象分片递归深合并到累积对象后回写 Args 快照，进度解析与
					// completedOpenAIToolArgsJSON 终态校验路径无需感知差异。
					objDelta, objErr := decodeJSONObjectArgs(rawArgs)
					if objErr != nil {
						return fail(fmt.Errorf("openai chat 工具参数对象分片解析失败: %w", objErr))
					}
					merged := toolObjectArgs[item.Index]
					if merged == nil {
						merged = make(map[string]any)
						toolObjectArgs[item.Index] = merged
					}
					if toolArgsShardModes[item.Index] == openAIToolShardModeString {
						// F-3：混合形态防护——此前按字符串分片累积，先把已累积内容
						// 整体解析为对象并入 merged；无法解析时告警丢弃，不静默、不断流。
						absorbStringArgsIntoObject(req.ModelCallID, item.Index, accumulator.Args.String(), merged)
					}
					deepMergeJSONObject(merged, objDelta)
					snapshot, snapErr := json.Marshal(merged)
					if snapErr != nil {
						return fail(snapErr)
					}
					accumulator.Args.Reset()
					accumulator.Args.Write(snapshot)
					toolArgsShardModes[item.Index] = openAIToolShardModeObject
					// F-4：对象模式没有明文增量，快照变化后以摘要触发一次进度事件，
					// 避免普通工具的流式进度事件整体缺失。
					argsTextDelta = fmt.Sprintf("[merged-object-args %d bytes]", len(snapshot))
				} else {
					// 标准形态：arguments 是 JSON 字符串字面量（RawMessage 含引号
					// 与转义），先解码还原为明文分片再拼接，行为与旧 string 字段一致；
					// 非字符串也非对象的值按解析失败处理（与旧整块 unmarshal 失败等价）。
					var piece string
					if err := json.Unmarshal(rawArgs, &piece); err != nil {
						return fail(fmt.Errorf("openai chat 工具参数分片类型不支持: %w", err))
					}
					switch toolArgsShardModes[item.Index] {
					case openAIToolShardModeObject:
						// F-3：混合形态防护——此前已按对象分片累积，字符串分片仅在
						// 能整体解析为 JSON 对象时并入 merged 并回写快照；否则告警并
						// 忽略该分片（不追加进 Args 快照、不断流）。
						pieceObj, pieceErr := decodeJSONObjectArgs([]byte(piece))
						if pieceErr == nil && len(pieceObj) > 0 {
							deepMergeJSONObject(toolObjectArgs[item.Index], pieceObj)
							snapshot, snapErr := json.Marshal(toolObjectArgs[item.Index])
							if snapErr != nil {
								return fail(snapErr)
							}
							accumulator.Args.Reset()
							accumulator.Args.Write(snapshot)
							argsTextDelta = fmt.Sprintf("[merged-object-args %d bytes]", len(snapshot))
						} else {
							logger.Warn("openai chat 工具参数分片形态混用：对象模式下收到不可解析的字符串分片，已忽略",
								"model_call_id", req.ModelCallID, "tool_index", item.Index, "bytes", len(piece), "err", pieceErr)
						}
					default:
						if stripped := argsStripper.Feed(piece); stripped != "" {
							// V-3：剥离泄漏进工具参数的 DeepSeek 特殊 token。
							_, _ = accumulator.Args.WriteString(stripped)
							argsTextDelta = stripped
						}
						toolArgsShardModes[item.Index] = openAIToolShardModeString
					}
				}
				lastToolIndex = item.Index
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
			if err := flushXMLScanner(); err != nil {
				return fail(err)
			}
			if err := flushThinkingCompleted(); err != nil {
				return fail(err)
			}
			// 剥离器可能仍缓冲着最后一个工具参数的半截标记，先放行再终态校验。
			if tail := argsStripper.Flush(); tail != "" && lastToolIndex >= 0 {
				if acc := tools[lastToolIndex]; acc != nil {
					acc.Args.WriteString(tail)
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
				// F-1：只在真正发出 ToolLikeCompleted 后置位；纯文本回合的
				// stop 不得被提升为 tool_calls（否则下游 forwarder 空 resume
				// 会重复输出甚至循环）。
				emittedToolInvocation = true
			}
			tools = make(map[int]*openAIToolAccumulator)
			finishReason = strings.TrimSpace(*choice.FinishReason)
			turnFinishedPending = true
		}
	}
	// 剥离器可能仍缓冲着最后一个工具参数的半截标记，先放行再终态校验。
	if tail := argsStripper.Flush(); tail != "" && lastToolIndex >= 0 {
		if acc := tools[lastToolIndex]; acc != nil {
			acc.Args.WriteString(tail)
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
		emittedToolInvocation = true
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
		// 部分 OpenAI 兼容中转在完整输出后直接关闭 SSE，不发 [DONE]/finish_reason。
		// 流正常结束（无读错误、无逐块超时）时视为正常收口：补发 TurnFinished，
		// 避免回合被误判失败并被 router 整体重试（内容已完整展示，重试会重复输出）。
		if scanner.Err() == nil && !chunkTimedOut {
			finishReason = "stop"
			turnFinishedPending = true
		} else {
			return fail(fmt.Errorf("provider stream ended before terminal event"))
		}
	}
	// F-2：收尾时放行正文剥离器缓冲的残余。[DONE] 分支 break 与无终止事件的
	// EOF 两条路径都汇合到此处，若不 Flush，deepseek 剥离器跨分片缓冲的尾部
	// 正文会被静默丢弃。
	if tail := textStripper.Flush(); tail != "" {
		if err := emitTaggedContentParts(thinkParser.Consume(tail)); err != nil {
			return fail(err)
		}
	}
	if err := flushTaggedContentTail(); err != nil {
		return fail(err)
	}
	if err := flushXMLScanner(); err != nil {
		return fail(err)
	}
	if err := flushThinkingCompleted(); err != nil {
		return fail(err)
	}
	if err := flushTurnFinished(); err != nil {
		return fail(err)
	}
	finishedAt = time.Now().UTC()
	logProviderStreamTiming("openai", currentModel, req, startedAt, firstEventAt, finishedAt, effectiveFinishReason(), inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, nil)
	recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", currentModel, startedAt, firstEventAt, finishedAt, effectiveFinishReason(), inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, nil))
	return nil
}
