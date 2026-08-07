// openai_stream_responses.go 承载 OpenAI responses 流式实现：SSE 事件解析、
// reasoning item 与 tool 进度事件、usage 快照与错误归一化。
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
)

func (adapter *OpenAIAdapter) streamResponses(ctx context.Context, req StreamRequest, baseURL string, apiKey string, modelID string, promptCacheKeyMaximumLength int, manualPromptCacheKey bool, sink func(ModelEvent) error) error {
	startedAt := time.Now().UTC()
	finishedAt := time.Time{}
	overrideBody := cloneRequestBodyOverride(req.RequestBodyOverride)
	var body any = overrideBody
	if len(overrideBody) == 0 {
		instructions, input, err := normalizeOpenAIResponsesInput(req.Messages)
		if err != nil {
			finishedAt = time.Now().UTC()
			recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
			return err
		}
		requestBody := openAIResponsesRequestBody{
			Model:        modelID,
			Instructions: instructions,
			Input:        input,
			Stream:       true,
			Store:        false,
		}
		if shouldSendOpenAIMaxOutputTokens(modelID) {
			requestBody.MaxOutputTokens = req.MaxTokens
		}
		if key := openAIPromptCacheKey(req, modelID, promptCacheKeyMaximumLength); key != "" {
			requestBody.PromptCacheKey = key
		}
		if len(req.Tools) > 0 {
			tools, err := normalizeOpenAIResponsesTools(req.Tools)
			if err != nil {
				finishedAt = time.Now().UTC()
				recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
				return err
			}
			if shouldExposeOpenAIResponsesImageGeneration(req, tools) {
				tools = ensureOpenAIResponsesImageGenerationTool(tools)
				if req.RequestKnobs != nil {
					req.RequestKnobs["openai_responses_image_generation_tool"] = "auto"
				}
			}
			requestBody.Tools = tools
		}
		if effort := strings.TrimSpace(req.ReasoningEffort); effort != "" {
			requestBody.Reasoning = &openAIResponsesReasoning{
				Effort:  effort,
				Summary: "auto",
			}
			// 同时请求明文 reasoning.summary 与 reasoning.encrypted_content：
			// - reasoning.summary 让 provider 返回可读思维链（response.reasoning_summary_text.delta
			//   走 thinking_delta 转发，Cursor 显示思维链而非 "Thinking is encrypted"）。
			// - reasoning.encrypted_content 保留 reasoning signature，供工具调用（委派 worker）
			//   的 prefix-cache 稳定，避免缓存命中率下降。
			requestBody.Include = []string{"reasoning.summary", "reasoning.encrypted_content"}
		}
		body = requestBody
	} else {
		applyOpenAIPromptCacheKeyOverride(overrideBody, req, modelID, promptCacheKeyMaximumLength)
	}
	bodyMap, err := requestBodyToMap(body)
	if err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}
	applyOpenAIThinkingDisable(bodyMap, req, baseURL, modelID, req.OpenAIEndpoint)
	applyOpenAIParallelToolCalls(bodyMap, modelID)
	if err := ApplyOpenAIExtraParams(bodyMap, req.OpenAIExtraParamsEnabled, req.OpenAIExtraParamsJSON); err != nil {
		finishedAt = time.Now().UTC()
		recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", modelID, startedAt, time.Time{}, finishedAt, "", 0, 0, 0, 0, err))
		return err
	}
	normalizeOpenAIResponsesRequestToolSchemas(bodyMap)
	applyOpenAIResponsesCompatibility(bodyMap, baseURL, modelID, manualPromptCacheKey)

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

	type openAIResponsesUsage struct {
		InputTokens        int64 `json:"input_tokens"`
		OutputTokens       int64 `json:"output_tokens"`
		InputTokensDetails *struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"input_tokens_details,omitempty"`
	}
	type openAIResponsesOutputContent struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type openAIResponsesOutputItem struct {
		ID               string                         `json:"id"`
		Type             string                         `json:"type"`
		Status           string                         `json:"status"`
		CallID           string                         `json:"call_id"`
		Name             string                         `json:"name"`
		Arguments        string                         `json:"arguments"`
		EncryptedContent string                         `json:"encrypted_content"`
		Summary          json.RawMessage                `json:"summary,omitempty"`
		Content          []openAIResponsesOutputContent `json:"content,omitempty"`
	}
	type openAIResponsesError struct {
		Message string          `json:"message"`
		Type    string          `json:"type"`
		Code    string          `json:"code"`
		Param   json.RawMessage `json:"param,omitempty"`
	}
	type openAIResponsesResponse struct {
		ID                string                      `json:"id"`
		Model             string                      `json:"model"`
		Status            string                      `json:"status"`
		Output            []openAIResponsesOutputItem `json:"output,omitempty"`
		OutputText        string                      `json:"output_text,omitempty"`
		Usage             *openAIResponsesUsage       `json:"usage,omitempty"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details,omitempty"`
		Error *openAIResponsesError `json:"error,omitempty"`
	}
	type openAIResponsesStreamEvent struct {
		Type            string                     `json:"type"`
		RequestID       string                     `json:"request_id"`
		Message         string                     `json:"message"`
		Code            string                     `json:"code"`
		Param           json.RawMessage            `json:"param,omitempty"`
		Delta           string                     `json:"delta"`
		Arguments       string                     `json:"arguments"`
		PartialImageB64 string                     `json:"partial_image_b64"`
		OutputFormat    string                     `json:"output_format"`
		OutputIndex     int                        `json:"output_index"`
		ItemID          string                     `json:"item_id"`
		Item            *openAIResponsesOutputItem `json:"item,omitempty"`
		Response        *openAIResponsesResponse   `json:"response,omitempty"`
		Error           *openAIResponsesError      `json:"error,omitempty"`
	}

	tools := make(map[string]*openAIToolAccumulator)
	completedTools := make(map[string]struct{})
	imageGenerations := make(map[string]*openAIImageGenerationAccumulator)
	completedImageGenerations := make(map[string]struct{})
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
	emittedToolInvocation := false
	emittedText := false
	thinkingStarted := time.Time{}
	thinkingActive := false
	emittedReasoningSignature := ""
	reasoningSummaryForwarded := ""
	thinkParser := &openAIThinkTagParser{}
	toolKey := func(itemID string, outputIndex int) string {
		if strings.TrimSpace(itemID) != "" {
			return strings.TrimSpace(itemID)
		}
		return fmt.Sprintf("output:%d", outputIndex)
	}
	effectiveFinishReason := func() string {
		reason := strings.TrimSpace(finishReason)
		if emittedToolInvocation && (reason == "" || reason == "completed") {
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
		if err := flushThinkingCompleted(); err != nil {
			return err
		}
		emittedText = true
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
	emitReasoningSummary := func(summary string) error {
		if summary == "" {
			return nil
		}
		// Responses sends summary deltas during the stream and may repeat the
		// complete summary on response.output_item.done. Forward only the
		// unseen suffix so Cursor gets one readable thinking block.
		if strings.TrimSpace(summary) == "" {
			return nil
		}
		forward := summary
		previous := reasoningSummaryForwarded
		if previous != "" {
			switch {
			case summary == previous:
				return nil
			case strings.HasPrefix(summary, previous):
				forward = summary[len(previous):]
			case strings.HasPrefix(previous, summary):
				return nil
			}
		}
		if strings.TrimSpace(forward) == "" {
			return nil
		}
		if err := emitThinkingDelta(forward); err != nil {
			return err
		}
		if previous == "" || strings.HasPrefix(summary, previous) {
			reasoningSummaryForwarded = summary
		} else {
			reasoningSummaryForwarded += forward
		}
		return nil
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
	applyUsage := func(usage *openAIResponsesUsage) {
		if usage == nil {
			return
		}
		usagePresent = true
		promptTokens := maxInt64(usage.InputTokens, 0)
		cachedTokens := int64(0)
		if usage.InputTokensDetails != nil {
			cacheReadPresent = true
			cachedTokens = maxInt64(usage.InputTokensDetails.CachedTokens, 0)
		}
		if cachedTokens > promptTokens {
			cachedTokens = promptTokens
		}
		inputTokens = promptTokens - cachedTokens
		outputTokens = maxInt64(usage.OutputTokens, 0)
		cacheReadTokens = cachedTokens
		cacheWriteTokens = 0
		cacheWritePresent = true
	}
	completeTool := func(key string, accumulator *openAIToolAccumulator) error {
		if accumulator == nil {
			return nil
		}
		completionKey := firstNonEmptyString(key, accumulator.CallID)
		if strings.TrimSpace(completionKey) == "" {
			completionKey = accumulator.Name + ":" + accumulator.Args.String()
		}
		if _, ok := completedTools[completionKey]; ok {
			return nil
		}
		if strings.TrimSpace(accumulator.CallID) != "" {
			if _, ok := completedTools[strings.TrimSpace(accumulator.CallID)]; ok {
				return nil
			}
		}
		completedTools[completionKey] = struct{}{}
		if strings.TrimSpace(accumulator.CallID) != "" {
			completedTools[strings.TrimSpace(accumulator.CallID)] = struct{}{}
		}
		argsJSON, argsErr := completedOpenAIToolArgsJSON(accumulator)
		if argsErr != nil {
			return argsErr
		}
		emittedToolInvocation = true
		if err := sink(ModelEvent{
			Kind:       ModelEventKindToolLikeCompleted,
			OccurredAt: time.Now().UTC(),
			Provider:   "openai",
			Model:      currentModel,
			ToolInvocation: &runtimecore.ToolInvocation{
				CallID:         strings.TrimSpace(accumulator.CallID),
				ToolName:       strings.TrimSpace(accumulator.Name),
				ArgsJSON:       argsJSON,
				ProviderItemID: strings.TrimSpace(accumulator.ProviderItemID),
				ProviderCallID: strings.TrimSpace(accumulator.ProviderCallID),
				ProviderStatus: strings.TrimSpace(accumulator.ProviderStatus),
			},
		}); err != nil {
			return err
		}
		streamIdle.MarkEffectiveContent()
		return nil
	}
	rememberImageGenerationItem := func(item openAIResponsesOutputItem, outputIndex int) *openAIImageGenerationAccumulator {
		key := toolKey(item.ID, outputIndex)
		accumulator, ok := imageGenerations[key]
		if !ok {
			accumulator = &openAIImageGenerationAccumulator{}
			imageGenerations[key] = accumulator
		}
		if itemID := strings.TrimSpace(item.ID); itemID != "" {
			accumulator.ProviderItemID = itemID
			accumulator.CallID = namespaceToolCallID(req.ModelCallID, itemID)
		}
		if status := strings.TrimSpace(item.Status); status != "" {
			accumulator.ProviderStatus = status
		}
		if strings.TrimSpace(accumulator.CallID) == "" {
			accumulator.CallID = namespaceToolCallID(req.ModelCallID, key)
		}
		return accumulator
	}
	emitImageGenerationStarted := func(accumulator *openAIImageGenerationAccumulator) error {
		if accumulator == nil || accumulator.StartedEmitted {
			return nil
		}
		callID := strings.TrimSpace(accumulator.CallID)
		if callID == "" {
			return nil
		}
		accumulator.StartedEmitted = true
		return sink(ModelEvent{
			Kind:       ModelEventKindPartialToolCall,
			OccurredAt: time.Now().UTC(),
			Provider:   "openai",
			Model:      currentModel,
			ToolCallID: callID,
			ToolCall: &agentv1.ToolCall{
				Tool: &agentv1.ToolCall_GenerateImageToolCall{
					GenerateImageToolCall: &agentv1.GenerateImageToolCall{
						Args: &agentv1.GenerateImageArgs{},
					},
				},
			},
		})
	}
	completeImageGeneration := func(key string, accumulator *openAIImageGenerationAccumulator) error {
		if accumulator == nil || strings.TrimSpace(accumulator.ImageData) == "" {
			return nil
		}
		completionKey := firstNonEmptyString(key, accumulator.CallID, accumulator.ProviderItemID)
		if strings.TrimSpace(completionKey) == "" {
			completionKey = accumulator.ImageData
		}
		if _, ok := completedImageGenerations[completionKey]; ok {
			return nil
		}
		if strings.TrimSpace(accumulator.CallID) != "" {
			if _, ok := completedImageGenerations[strings.TrimSpace(accumulator.CallID)]; ok {
				return nil
			}
		}
		completedImageGenerations[completionKey] = struct{}{}
		if strings.TrimSpace(accumulator.CallID) != "" {
			completedImageGenerations[strings.TrimSpace(accumulator.CallID)] = struct{}{}
		}
		argsPayload := map[string]string{"image_data": strings.TrimSpace(accumulator.ImageData)}
		argsJSON, err := json.Marshal(argsPayload)
		if err != nil {
			return err
		}
		emittedToolInvocation = true
		if err := sink(ModelEvent{
			Kind:       ModelEventKindToolLikeCompleted,
			OccurredAt: time.Now().UTC(),
			Provider:   "openai",
			Model:      currentModel,
			ToolInvocation: &runtimecore.ToolInvocation{
				CallID:         strings.TrimSpace(accumulator.CallID),
				ToolName:       "GenerateImage",
				ArgsJSON:       argsJSON,
				ProviderItemID: strings.TrimSpace(accumulator.ProviderItemID),
				ProviderStatus: strings.TrimSpace(accumulator.ProviderStatus),
			},
		}); err != nil {
			return err
		}
		streamIdle.MarkEffectiveContent()
		return nil
	}
	emitReasoningSignature := func(signature string, providerItemID string, providerStatus string, providerSummary json.RawMessage) error {
		if summary := extractOpenAIResponsesReasoningSummary(providerSummary); summary != "" {
			if err := emitReasoningSummary(summary); err != nil {
				return err
			}
			logger.Infof("openai responses reasoning summary item request_id=%s model_call_id=%s item_id=%s summary_bytes=%d forwarded_bytes=%d", strings.TrimSpace(req.RequestID), strings.TrimSpace(req.ModelCallID), strings.TrimSpace(providerItemID), len(summary), len(reasoningSummaryForwarded))
		} else if strings.TrimSpace(signature) != "" {
			logger.Infof("openai responses reasoning summary unavailable request_id=%s model_call_id=%s item_id=%s encrypted=true", strings.TrimSpace(req.RequestID), strings.TrimSpace(req.ModelCallID), strings.TrimSpace(providerItemID))
		}
		trimmedSignature := strings.TrimSpace(signature)
		if trimmedSignature == "" || trimmedSignature == emittedReasoningSignature {
			return nil
		}
		duration := int32(0)
		if thinkingActive {
			duration = int32(time.Since(thinkingStarted).Milliseconds())
			if duration < 0 {
				duration = 0
			}
			thinkingActive = false
			thinkingStarted = time.Time{}
		}
		emittedReasoningSignature = trimmedSignature
		return sink(ModelEvent{
			Kind:                    ModelEventKindThinkingCompleted,
			OccurredAt:              time.Now().UTC(),
			Provider:                "openai",
			Model:                   currentModel,
			ThinkingDurationMS:      duration,
			ThinkingSignature:       trimmedSignature,
			ThinkingSignatureSource: ReasoningSignatureSourceOpenAIResponses,
			ProviderItemID:          strings.TrimSpace(providerItemID),
			ProviderStatus:          strings.TrimSpace(providerStatus),
			ProviderSummary:         cloneRawJSON(providerSummary),
		})
	}
	applyFunctionCallItem := func(item openAIResponsesOutputItem, outputIndex int, complete bool) error {
		if strings.TrimSpace(item.Type) != "function_call" {
			return nil
		}
		streamIdle.MarkEffectiveContent()
		key := toolKey(firstNonEmptyString(item.ID, item.CallID), outputIndex)
		accumulator, ok := tools[key]
		if !ok {
			accumulator = &openAIToolAccumulator{}
			tools[key] = accumulator
		}
		if strings.TrimSpace(item.ID) != "" {
			accumulator.ProviderItemID = strings.TrimSpace(item.ID)
		}
		if strings.TrimSpace(item.Status) != "" {
			accumulator.ProviderStatus = strings.TrimSpace(item.Status)
		}
		if strings.TrimSpace(item.CallID) != "" {
			accumulator.ProviderCallID = strings.TrimSpace(item.CallID)
			accumulator.CallID = namespaceToolCallID(req.ModelCallID, item.CallID)
		} else if strings.TrimSpace(item.ID) != "" {
			accumulator.CallID = namespaceToolCallID(req.ModelCallID, item.ID)
		}
		if strings.TrimSpace(item.Name) != "" {
			accumulator.Name = strings.TrimSpace(item.Name)
		}
		argsTextDelta := ""
		if item.Arguments != "" && accumulator.Args.Len() == 0 {
			_, _ = accumulator.Args.WriteString(item.Arguments)
			argsTextDelta = item.Arguments
		}
		if argsTextDelta != "" || (strings.TrimSpace(accumulator.Name) == "CreatePlan" && accumulator.Args.Len() > 0) {
			if err := emitOpenAIToolProgress(sink, currentModel, accumulator, argsTextDelta); err != nil {
				return err
			}
		}
		if complete {
			delete(tools, key)
			return completeTool(key, accumulator)
		}
		return nil
	}
	applyOutputItem := func(item openAIResponsesOutputItem, outputIndex int, complete bool) error {
		switch strings.TrimSpace(item.Type) {
		case "reasoning":
			// The added event only announces the reasoning item. Its encrypted
			// content and complete summary are finalized on output_item.done;
			// completing here would split a later summary into a second Cursor
			// thinking block.
			if !complete {
				return nil
			}
			return emitReasoningSignature(item.EncryptedContent, item.ID, item.Status, item.Summary)
		case "function_call":
			return applyFunctionCallItem(item, outputIndex, complete)
		case "image_generation_call":
			accumulator := rememberImageGenerationItem(item, outputIndex)
			if !complete {
				return emitImageGenerationStarted(accumulator)
			}
			key := toolKey(item.ID, outputIndex)
			delete(imageGenerations, key)
			return completeImageGeneration(key, accumulator)
		default:
			return nil
		}
	}
	errorFromEvent := func(event openAIResponsesStreamEvent) error {
		errorType := ""
		code := strings.TrimSpace(event.Code)
		message := strings.TrimSpace(event.Message)
		if event.Error != nil {
			errorType = strings.TrimSpace(event.Error.Type)
			code = firstNonEmptyString(strings.TrimSpace(event.Error.Code), code)
			message = firstNonEmptyString(strings.TrimSpace(event.Error.Message), message)
		}
		if event.Response != nil && event.Response.Error != nil {
			errorType = firstNonEmptyString(strings.TrimSpace(event.Response.Error.Type), errorType)
			code = firstNonEmptyString(strings.TrimSpace(event.Response.Error.Code), code)
			message = firstNonEmptyString(strings.TrimSpace(event.Response.Error.Message), message)
		}
		if message != "" {
			return fmt.Errorf("openai responses stream error %s: %s", openAIStreamErrorDetails(errorType, code, event.RequestID), message)
		}

		details := make([]string, 0, 5)
		if eventType := strings.TrimSpace(event.Type); eventType != "" {
			details = append(details, "event_type="+eventType)
		}
		if providerDetails := openAIStreamErrorDetails(errorType, code, event.RequestID); providerDetails != "provider_error" {
			details = append(details, providerDetails)
		}
		if event.Response != nil {
			if responseID := strings.TrimSpace(event.Response.ID); responseID != "" {
				details = append(details, "response_id="+responseID)
			}
			if status := strings.TrimSpace(event.Response.Status); status != "" {
				details = append(details, "status="+status)
			}
			if event.Response.IncompleteDetails != nil {
				if reason := strings.TrimSpace(event.Response.IncompleteDetails.Reason); reason != "" {
					details = append(details, "reason="+reason)
				}
			}
		}
		if len(details) == 0 {
			return fmt.Errorf("openai responses stream failed without error details")
		}
		return fmt.Errorf("openai responses stream failed %s", strings.Join(details, " "))
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
			for key, accumulator := range tools {
				if err := completeTool(key, accumulator); err != nil {
					return fail(err)
				}
			}
			for key, accumulator := range imageGenerations {
				if err := completeImageGeneration(key, accumulator); err != nil {
					return fail(err)
				}
			}
			if err := flushTurnFinished(); err != nil {
				return fail(err)
			}
			break
		}

		var event openAIResponsesStreamEvent
		if err := json.Unmarshal([]byte(payloadLine), &event); err != nil {
			return fail(err)
		}
		if event.Response != nil {
			if strings.TrimSpace(event.Response.Model) != "" {
				currentModel = strings.TrimSpace(event.Response.Model)
			}
			applyUsage(event.Response.Usage)
		}

		switch strings.TrimSpace(event.Type) {
		case "response.output_text.delta":
			if err := emitTaggedContentParts(thinkParser.Consume(event.Delta)); err != nil {
				return fail(err)
			}
		case "response.output_item.added":
			if event.Item != nil {
				if err := applyOutputItem(*event.Item, event.OutputIndex, false); err != nil {
					return fail(err)
				}
			}
		case "response.function_call_arguments.delta":
			key := toolKey(event.ItemID, event.OutputIndex)
			accumulator, ok := tools[key]
			if !ok {
				accumulator = &openAIToolAccumulator{}
				tools[key] = accumulator
			}
			if event.Delta != "" {
				_, _ = accumulator.Args.WriteString(event.Delta)
				streamIdle.MarkEffectiveContent()
				if err := emitOpenAIToolProgress(sink, currentModel, accumulator, event.Delta); err != nil {
					return fail(err)
				}
			}
		case "response.function_call_arguments.done":
			key := toolKey(event.ItemID, event.OutputIndex)
			accumulator, ok := tools[key]
			if !ok {
				accumulator = &openAIToolAccumulator{}
				tools[key] = accumulator
			}
			if event.Arguments != "" && accumulator.Args.Len() == 0 {
				_, _ = accumulator.Args.WriteString(event.Arguments)
				streamIdle.MarkEffectiveContent()
				if err := emitOpenAIToolProgress(sink, currentModel, accumulator, event.Arguments); err != nil {
					return fail(err)
				}
			}
		case "response.image_generation_call.partial_image":
			key := toolKey(event.ItemID, event.OutputIndex)
			accumulator, ok := imageGenerations[key]
			if !ok {
				accumulator = &openAIImageGenerationAccumulator{}
				imageGenerations[key] = accumulator
			}
			if itemID := strings.TrimSpace(event.ItemID); itemID != "" {
				accumulator.ProviderItemID = itemID
				accumulator.CallID = namespaceToolCallID(req.ModelCallID, itemID)
			}
			if strings.TrimSpace(accumulator.CallID) == "" {
				accumulator.CallID = namespaceToolCallID(req.ModelCallID, key)
			}
			if err := emitImageGenerationStarted(accumulator); err != nil {
				return fail(err)
			}
			if imageData := strings.TrimSpace(event.PartialImageB64); imageData != "" {
				accumulator.ImageData = imageData
				streamIdle.MarkEffectiveContent()
			}
			if outputFormat := strings.TrimSpace(event.OutputFormat); outputFormat != "" {
				accumulator.OutputFormat = outputFormat
			}
		case "response.output_item.done":
			if event.Item != nil {
				if err := applyOutputItem(*event.Item, event.OutputIndex, true); err != nil {
					return fail(err)
				}
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			streamIdle.MarkEffectiveContent()
			if err := emitReasoningSummary(event.Delta); err != nil {
				return fail(err)
			}
		case "response.completed", "response.incomplete":
			streamTerminated = true
			if event.Response != nil && !emittedText {
				if strings.TrimSpace(event.Response.OutputText) != "" {
					if err := emitTaggedContentParts(thinkParser.Consume(event.Response.OutputText)); err != nil {
						return fail(err)
					}
				} else {
					for _, item := range event.Response.Output {
						for _, content := range item.Content {
							if strings.TrimSpace(content.Type) != "output_text" && strings.TrimSpace(content.Type) != "text" {
								continue
							}
							if err := emitTaggedContentParts(thinkParser.Consume(content.Text)); err != nil {
								return fail(err)
							}
						}
					}
				}
			}
			if err := flushTaggedContentTail(); err != nil {
				return fail(err)
			}
			if err := flushThinkingCompleted(); err != nil {
				return fail(err)
			}
			if event.Response != nil {
				for index, item := range event.Response.Output {
					if err := applyOutputItem(item, index, true); err != nil {
						return fail(err)
					}
				}
				finishReason = strings.TrimSpace(event.Response.Status)
				if event.Response.IncompleteDetails != nil && strings.TrimSpace(event.Response.IncompleteDetails.Reason) != "" {
					finishReason = strings.TrimSpace(event.Response.IncompleteDetails.Reason)
				}
			}
			turnFinishedPending = true
		case "response.failed", "error":
			return fail(errorFromEvent(event))
		}
	}
	for key, accumulator := range tools {
		if err := completeTool(key, accumulator); err != nil {
			return fail(err)
		}
	}
	for key, accumulator := range imageGenerations {
		if err := completeImageGeneration(key, accumulator); err != nil {
			return fail(err)
		}
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
	logProviderStreamTiming("openai", currentModel, req, startedAt, firstEventAt, finishedAt, effectiveFinishReason(), inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, nil)
	recordLLMSummaryArtifact(req, buildLLMSummaryPayload(req, "openai", currentModel, startedAt, firstEventAt, finishedAt, effectiveFinishReason(), inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, nil))
	return nil
}
