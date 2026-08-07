// anthropic_stream.go 承载 Anthropic 流式实现：SSE 解析、thinking 块、
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
)

func (adapter *AnthropicAdapter) Stream(ctx context.Context, req StreamRequest, sink func(ModelEvent) error) error {
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("anthropic base url is empty")
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		return fmt.Errorf("anthropic api key is empty")
	}
	modelID := strings.TrimSpace(req.ProviderModelID)
	if modelID == "" {
		modelID = strings.TrimSpace(req.ModelID)
	}
	if modelID == "" {
		return fmt.Errorf("anthropic model id is empty")
	}

	startedAt := time.Now().UTC()
	finishedAt := time.Time{}
	requestURL := anthropicEndpointURL(baseURL)
	body := cloneRequestBodyOverride(req.RequestBodyOverride)
	if len(body) == 0 {
		thinkingConfig := buildAnthropicThinkingConfig(req)
		relocateImages := shouldRelocateAnthropicImages(baseURL)
		stableMessageCount := anthropicStableProviderMessageCount(req.Messages, req.StableMessageCount, thinkingConfig != nil)
		systemParts, messages, err := normalizeAnthropicProviderMessages(req.Messages, thinkingConfig != nil, relocateImages)
		if err != nil {
			return err
		}

		tools := make([]anthropicTool, 0, len(req.Tools))
		for _, raw := range req.Tools {
			var descriptor struct {
				Function struct {
					Name        string         `json:"name"`
					Description string         `json:"description"`
					Parameters  map[string]any `json:"parameters"`
				} `json:"function"`
			}
			if err := json.Unmarshal(raw, &descriptor); err != nil {
				finishedAt = time.Now().UTC()
				draftBody := map[string]any{
					"model":          modelID,
					"messages":       messages,
					"stream":         true,
					"max_tokens":     req.MaxTokens,
					"tool_raw_count": len(req.Tools),
				}
				draftBody["system"] = anthropicProviderSystemBlocks(systemParts)
				recordLLMRequestArtifact(req, "anthropic", modelID, "POST", requestURL, draftBody)
				recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "anthropic", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
				return err
			}
			schema := descriptor.Function.Parameters
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			// 与 OpenAI 路径一致：修复 required:null → required:[]，避免 Anthropic
			// schema 校验返回 [standard_violation] /required: null is not of type "array"
			normalizeOpenAIToolSchemaRequired(schema)
			tools = append(tools, anthropicTool{
				Name:        strings.TrimSpace(descriptor.Function.Name),
				Description: strings.TrimSpace(descriptor.Function.Description),
				InputSchema: schema,
			})
		}

		body = map[string]any{
			"model":      modelID,
			"messages":   messages,
			"stream":     true,
			"max_tokens": maxAnthropicTokens(req),
		}
		if len(tools) > 0 {
			body["tools"] = tools
		}
		body["system"] = anthropicProviderSystemBlocks(systemParts)
		frontier := buildAnthropicCacheFrontier(body, stableMessageCount)
		req.RequestKnobs = annotateAnthropicRequestKnobs(req.RequestKnobs, body, frontier)
		// 注意：此处 clone 不可省略——它把 []anthropicTool/[]anthropicMessage
		// 等类型化 slice 经 marshal+unmarshal 转为 []any，applyAnthropicCacheBreakpoints
		// 依赖 .([]any) 断言；省略会导致 tools/system/messages 的 cache breakpoint 失效。
		body = cloneRequestBodyOverride(body)
		applyAnthropicCacheBreakpoints(body, frontier.BreakpointPositions)
		frontier.BreakpointCount = len(frontier.BreakpointPositions)
	}
	// applyAnthropicThinkingConfig 在 override 块之外无条件调用，确保 RequestBodyOverride
	// 路径与正常构造路径行为一致：disabled 时强制 thinking:{type:disabled} 并清理冲突字段，
	// 非 disabled 时按 AnthropicThinkingEffort 写 adaptive 配置。与 openai.go 的
	// applyOpenAIThinkingDisable 对称——后者也是无条件在两条路径之后调用。
	applyAnthropicThinkingConfig(body, req)
	if err := ApplyAnthropicExtraParams(body, req.AnthropicExtraParamsEnabled, req.AnthropicExtraParamsJSON); err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "anthropic", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}
	applyAnthropicProviderCompatibility(body, req, baseURL, modelID)
	applyProviderCompatibilitySanitization(body, baseURL, modelID)
	recordLLMRequestArtifact(req, "anthropic", modelID, "POST", requestURL, body)

	payload, err := json.Marshal(body)
	if err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "anthropic", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}

	err = streamWithReconnect(ctx, sink, func(_ int, wrappedSink func(ModelEvent) error) error {
		streamCtx, streamIdle := newProviderStreamIdleWatchdog(ctx, req.ProviderStreamIdleTimeout)
		defer streamIdle.Stop()

		resp, err := doProviderRequestWithGzipFallback(streamCtx, adapter.client, "anthropic", req.RequestID, req.ModelCallID, payload, requestURL, func(httpReq *http.Request) error {
			ApplyAnthropicCompatibleAuthHeaders(httpReq, apiKey)
			httpReq.Header.Set("anthropic-version", "2023-06-01")
			httpReq.Header.Set("content-type", "application/json")
			httpReq.Header.Set("User-Agent", AnthropicClaudeCodeUserAgent)
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
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "anthropic", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
			return err
		}
		streamIdle.AttachBody(resp.Body)
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			err = buildHTTPStatusError("anthropic adapter", resp)
			finishedAt = time.Now().UTC()
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "anthropic", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
			return err
		}

		type anthropicUsage struct {
			InputTokens              *int64 `json:"input_tokens,omitempty"`
			OutputTokens             *int64 `json:"output_tokens,omitempty"`
			CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens,omitempty"`
			CacheReadInputTokens     *int64 `json:"cache_read_input_tokens,omitempty"`
		}

		type contentBlock struct {
			Type  string `json:"type"`
			ID    string `json:"id"`
			Name  string `json:"name"`
			Text  string `json:"text"`
			Input any    `json:"input"`
		}
		type anthropicEvent struct {
			Type         string       `json:"type"`
			RequestID    string       `json:"request_id"`
			Index        int          `json:"index"`
			ContentBlock contentBlock `json:"content_block"`
			Message      struct {
				Model string         `json:"model"`
				Usage anthropicUsage `json:"usage"`
			} `json:"message"`
			Usage anthropicUsage `json:"usage"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
				Signature   string `json:"signature"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Error *struct {
				Type    string `json:"type"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}

		toolBlocks := make(map[int]*anthropicToolAccumulator)
		thinkingStarted := time.Time{}
		currentThinkingSignature := ""
		thinkParser := &anthropicThinkTagParser{}
		currentModel := modelID
		inputTokens := int64(0)
		outputTokens := int64(0)
		cacheReadTokens := int64(0)
		cacheWriteTokens := int64(0)
		usagePresent := false
		cacheReadPresent := false
		cacheWritePresent := false
		finishReason := "message_stop"
		messageStopped := false
		firstEventAt := time.Time{}
		fail := func(streamErr error) error {
			finishedAt = time.Now().UTC()
			logProviderStreamTiming("anthropic", currentModel, req, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, streamErr)
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "anthropic", currentModel, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, streamErr))
			return streamErr
		}
		flushThinkingCompleted := func() error {
			if thinkingStarted.IsZero() {
				return nil
			}
			duration := int32(time.Since(thinkingStarted).Milliseconds())
			if duration < 0 {
				duration = 0
			}
			thinkingSignature := strings.TrimSpace(currentThinkingSignature)
			thinkingSignatureSource := ""
			if thinkingSignature != "" {
				thinkingSignatureSource = ReasoningSignatureSourceAnthropic
			}
			if err := wrappedSink(ModelEvent{
				Kind:                    ModelEventKindThinkingCompleted,
				OccurredAt:              time.Now().UTC(),
				Provider:                "anthropic",
				Model:                   currentModel,
				ThinkingDurationMS:      duration,
				ThinkingSignature:       thinkingSignature,
				ThinkingSignatureSource: thinkingSignatureSource,
			}); err != nil {
				return err
			}
			thinkingStarted = time.Time{}
			currentThinkingSignature = ""
			return nil
		}
		emitTextDelta := func(text string) error {
			if text == "" {
				return nil
			}
			streamIdle.MarkEffectiveContent()
			if err := flushThinkingCompleted(); err != nil {
				return err
			}
			return wrappedSink(ModelEvent{
				Kind:       ModelEventKindTextDelta,
				OccurredAt: time.Now().UTC(),
				Provider:   "anthropic",
				Model:      currentModel,
				Text:       text,
			})
		}
		emitThinkingDelta := func(reasoning string) error {
			if reasoning == "" {
				return nil
			}
			streamIdle.MarkEffectiveContent()
			if thinkingStarted.IsZero() {
				thinkingStarted = time.Now()
			}
			return wrappedSink(ModelEvent{
				Kind:          ModelEventKindThinkingDelta,
				OccurredAt:    time.Now().UTC(),
				Provider:      "anthropic",
				Model:         currentModel,
				Text:          reasoning,
				ThinkingStyle: agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT,
			})
		}
		emitTaggedTextParts := func(parts []anthropicContentPart) error {
			for _, part := range parts {
				switch part.Kind {
				case anthropicContentPartText:
					if err := emitTextDelta(part.Text); err != nil {
						return err
					}
				case anthropicContentPartReasoning:
					if err := emitThinkingDelta(part.Text); err != nil {
						return err
					}
				case anthropicContentPartThinkingCompleted:
					if err := flushThinkingCompleted(); err != nil {
						return err
					}
				}
			}
			return nil
		}
		flushTaggedTextTail := func() error {
			return emitTaggedTextParts(thinkParser.Flush())
		}
		applyUsage := func(usage anthropicUsage) {
			if usage.InputTokens != nil {
				usagePresent = true
				inputTokens = maxInt64(*usage.InputTokens, 0)
			}
			if usage.OutputTokens != nil {
				usagePresent = true
				outputTokens = maxInt64(*usage.OutputTokens, 0)
			}
			if usage.CacheReadInputTokens != nil {
				usagePresent = true
				cacheReadPresent = true
				cacheReadTokens = maxInt64(*usage.CacheReadInputTokens, 0)
			}
			if usage.CacheCreationInputTokens != nil {
				usagePresent = true
				cacheWritePresent = true
				cacheWriteTokens = maxInt64(*usage.CacheCreationInputTokens, 0)
			}
		}
		errorFromEvent := func(event anthropicEvent) error {
			finishReason = "error"
			if event.Error != nil {
				parts := make([]string, 0, 4)
				if value := strings.TrimSpace(event.Error.Type); value != "" {
					parts = append(parts, "type="+value)
				}
				if value := strings.TrimSpace(event.Error.Code); value != "" {
					parts = append(parts, "code="+value)
				}
				if value := strings.TrimSpace(event.RequestID); value != "" {
					parts = append(parts, "request_id="+value)
				}
				if message := strings.TrimSpace(event.Error.Message); message != "" {
					if len(parts) > 0 {
						return fmt.Errorf("anthropic provider error %s: %s", strings.Join(parts, " "), message)
					}
					return fmt.Errorf("anthropic provider error: %s", message)
				}
				if len(parts) > 0 {
					return fmt.Errorf("anthropic provider error %s", strings.Join(parts, " "))
				}
			}
			return fmt.Errorf("anthropic provider error")
		}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		currentEvent := ""
		dataLines := make([]string, 0, 2)
		flush := func() error {
			if currentEvent == "" || len(dataLines) == 0 {
				dataLines = dataLines[:0]
				return nil
			}
			payloadLine := strings.Join(dataLines, "\n")
			dataLines = dataLines[:0]
			if strings.TrimSpace(payloadLine) == "[DONE]" {
				return nil
			}

			var event anthropicEvent
			if err := json.Unmarshal([]byte(payloadLine), &event); err != nil {
				return err
			}
			if currentEvent == "error" || strings.TrimSpace(event.Type) == "error" {
				return errorFromEvent(event)
			}

			switch currentEvent {
			case "message_start":
				if strings.TrimSpace(event.Message.Model) != "" {
					currentModel = strings.TrimSpace(event.Message.Model)
				}
				applyUsage(event.Message.Usage)
			case "content_block_start":
				if strings.TrimSpace(event.ContentBlock.Type) == "tool_use" {
					if err := flushTaggedTextTail(); err != nil {
						return err
					}
					if err := flushThinkingCompleted(); err != nil {
						return err
					}
					accumulator := &anthropicToolAccumulator{
						CallID: namespaceToolCallID(req.ModelCallID, event.ContentBlock.ID),
						Name:   strings.TrimSpace(event.ContentBlock.Name),
					}
					if !isEmptyAnthropicToolInput(event.ContentBlock.Input) {
						if encoded, err := json.Marshal(event.ContentBlock.Input); err == nil && string(encoded) != "null" {
							_, _ = accumulator.Args.Write(encoded)
						}
					}
					toolBlocks[event.Index] = accumulator
					streamIdle.MarkEffectiveContent()
					if err := emitAnthropicToolProgress(wrappedSink, currentModel, accumulator, ""); err != nil {
						return err
					}
				}
			case "content_block_delta":
				if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
					streamIdle.MarkEffectiveContent()
					if err := emitTaggedTextParts(thinkParser.Consume(event.Delta.Text)); err != nil {
						return err
					}
				}
				if event.Delta.Type == "thinking_delta" && event.Delta.Thinking != "" {
					streamIdle.MarkEffectiveContent()
					if err := emitThinkingDelta(event.Delta.Thinking); err != nil {
						return err
					}
				}
				if event.Delta.Signature != "" {
					currentThinkingSignature = strings.TrimSpace(event.Delta.Signature)
				}
				if event.Delta.Type == "input_json_delta" {
					accumulator := toolBlocks[event.Index]
					if accumulator != nil && event.Delta.PartialJSON != "" {
						_, _ = accumulator.Args.WriteString(event.Delta.PartialJSON)
						streamIdle.MarkEffectiveContent()
						if err := emitAnthropicToolProgress(wrappedSink, currentModel, accumulator, event.Delta.PartialJSON); err != nil {
							return err
						}
					}
				}
			case "content_block_stop":
				accumulator := toolBlocks[event.Index]
				if accumulator != nil {
					argsJSON, err := completedAnthropicToolArgsJSON(accumulator)
					if err != nil {
						delete(toolBlocks, event.Index)
						return err
					}
					if err := emitAnthropicToolProgress(wrappedSink, currentModel, accumulator, ""); err != nil {
						return err
					}
					streamIdle.MarkEffectiveContent()
					if err := wrappedSink(ModelEvent{
						Kind:       ModelEventKindToolLikeCompleted,
						OccurredAt: time.Now().UTC(),
						Provider:   "anthropic",
						Model:      currentModel,
						ToolInvocation: &runtimecore.ToolInvocation{
							CallID:   accumulator.CallID,
							ToolName: accumulator.Name,
							ArgsJSON: argsJSON,
						},
					}); err != nil {
						return err
					}
					delete(toolBlocks, event.Index)
					return nil
				}
				if err := flushTaggedTextTail(); err != nil {
					return err
				}
				if err := flushThinkingCompleted(); err != nil {
					return err
				}
			case "message_delta":
				applyUsage(event.Usage)
				if strings.TrimSpace(event.Delta.StopReason) != "" {
					finishReason = strings.TrimSpace(event.Delta.StopReason)
				}
				// 当前 MVP 阶段只在 message_stop 时统一收口，不在这里重复发 turn finished。
				return nil
			case "message_stop":
				messageStopped = true
				if err := flushTaggedTextTail(); err != nil {
					return err
				}
				if err := flushThinkingCompleted(); err != nil {
					return err
				}
				if err := wrappedSink(ModelEvent{
					Kind:              ModelEventKindTurnFinished,
					OccurredAt:        time.Now().UTC(),
					Provider:          "anthropic",
					Model:             currentModel,
					InputTokens:       inputTokens,
					OutputTokens:      outputTokens,
					CacheReadTokens:   cacheReadTokens,
					CacheWriteTokens:  cacheWriteTokens,
					UsagePresent:      usagePresent,
					CacheReadPresent:  cacheReadPresent,
					CacheWritePresent: cacheWritePresent,
					FinishReason:      finishReason,
				}); err != nil {
					return err
				}
			}
			return nil
		}

		// A2 SSE 逐块读超时：每次 Scan 前设置 30s 读 deadline，块到达后清除（不累积）。
		// 底层连接不支持 SetReadDeadline 时静默 fallback，行为与原来一致。
		// chunkTimedOut 记录本轮是否发生过逐块读超时；是则把扫描错误转为可触发
		// pre-output 重连的读超时错误（见下方 scanner.Err 处理）。
		var chunkTimedOut bool
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
			_, _ = appendLLMResponseArtifact(req, rawLine+"\n")
			line := strings.TrimSpace(rawLine)
			if line == "" {
				if err := flush(); err != nil {
					return fail(err)
				}
				currentEvent = ""
				continue
			}
			if firstEventAt.IsZero() {
				firstEventAt = time.Now().UTC()
			}
			if strings.HasPrefix(line, "event:") {
				currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				continue
			}
			if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if err := flush(); err != nil {
			return fail(err)
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
		if !messageStopped {
			return fail(fmt.Errorf("anthropic stream ended before message_stop"))
		}
		if err := flushTaggedTextTail(); err != nil {
			return fail(err)
		}
		if err := flushThinkingCompleted(); err != nil {
			return fail(err)
		}
		finishedAt = time.Now().UTC()
		logProviderStreamTiming("anthropic", currentModel, req, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, nil)
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "anthropic", currentModel, startedAt, firstEventAt, finishedAt, finishReason, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, nil))
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}
