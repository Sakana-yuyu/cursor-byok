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
	"net/url"
	"strings"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/netproxy"
)

type GeminiAdapter struct {
	client *http.Client
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

	err = streamWithReconnect(ctx, sink, func(_ int, wrappedSink func(ModelEvent) error) error {
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
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	model := url.PathEscape(strings.TrimSpace(modelID))
	method := "generateContent"
	if stream {
		method = "streamGenerateContent?alt=sse"
	}
	if strings.Contains(base, ":generateContent") || strings.Contains(base, ":streamGenerateContent") {
		return base
	}
	return base + "/models/" + model + ":" + method
}

func geminiRequestBody(req StreamRequest, modelID string) (map[string]any, error) {
	systemParts := make([]map[string]any, 0, 1)
	contents := make([]map[string]any, 0, len(req.Messages))
	for _, message := range req.Messages {
		role := strings.TrimSpace(message.Role)
		if role == "system" {
			if text := strings.TrimSpace(protocolMessageText(message)); text != "" {
				systemParts = append(systemParts, map[string]any{"text": text})
			}
			continue
		}
		parts, err := geminiMessageParts(message)
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
	role := strings.TrimSpace(message.Role)
	parts := make([]map[string]any, 0, len(message.ContentParts)+len(message.ToolCalls)+1)
	if role == "tool" {
		name := strings.TrimSpace(message.Name)
		if name == "" {
			name = "tool"
		}
		parts = append(parts, map[string]any{"functionResponse": map[string]any{"name": name, "response": map[string]any{"content": protocolMessageText(message)}}})
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
			_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
		}
		parts = append(parts, map[string]any{"functionCall": map[string]any{"name": strings.TrimSpace(call.Function.Name), "args": args}})
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
	UsageMetadata struct {
		PromptTokenCount        int64 `json:"promptTokenCount"`
		CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
		CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

func (adapter *GeminiAdapter) streamGeminiEvents(resp *http.Response, req StreamRequest, modelID string, startedAt time.Time, streamIdle *providerStreamIdleWatchdog, sink func(ModelEvent) error) (int64, int64, int64, string, time.Time, error) {
	_ = adapter
	_ = startedAt
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	inputTokens, outputTokens, cacheReadTokens := int64(0), int64(0), int64(0)
	finishReason := ""
	firstEventAt := time.Time{}
	lastText := ""
	lastThinking := ""
	emittedTools := map[string]struct{}{}
	markFirst := func() {
		if firstEventAt.IsZero() {
			firstEventAt = time.Now().UTC()
		}
	}
	for scanner.Scan() {
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
		if chunk.UsageMetadata.PromptTokenCount > 0 {
			inputTokens = chunk.UsageMetadata.PromptTokenCount
		}
		if chunk.UsageMetadata.CandidatesTokenCount > 0 {
			outputTokens = chunk.UsageMetadata.CandidatesTokenCount
		}
		if chunk.UsageMetadata.CachedContentTokenCount > 0 {
			cacheReadTokens = chunk.UsageMetadata.CachedContentTokenCount
		}
		if len(chunk.Candidates) == 0 {
			continue
		}
		candidate := chunk.Candidates[0]
		if candidate.FinishReason != "" {
			finishReason = geminiFinishReason(candidate.FinishReason)
		}
		textSnapshot := bytes.Buffer{}
		thinkingSnapshot := bytes.Buffer{}
		for _, part := range candidate.Content.Parts {
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
				args, _ := json.Marshal(part.FunctionCall["args"])
				callID := stableGeminiToolCallID(req, name, string(args))
				if _, seen := emittedTools[callID]; seen {
					continue
				}
				emittedTools[callID] = struct{}{}
				markFirst()
				streamIdle.MarkEffectiveContent()
				if err := sink(ModelEvent{Kind: ModelEventKindToolLikeCompleted, OccurredAt: time.Now().UTC(), Provider: "gemini", Model: modelID, ToolCallID: callID, ToolInvocation: &runtimecore.ToolInvocation{CallID: callID, ToolName: name, ArgsJSON: args}}); err != nil {
					return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, err
				}
			}
		}
		if delta := suffixAfterCommonPrefix(lastThinking, thinkingSnapshot.String()); delta != "" {
			lastThinking = thinkingSnapshot.String()
			markFirst()
			streamIdle.MarkEffectiveContent()
			if err := sink(ModelEvent{Kind: ModelEventKindThinkingDelta, OccurredAt: time.Now().UTC(), Provider: "gemini", Model: modelID, ThinkingStyle: agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT, Text: delta}); err != nil {
				return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, err
			}
		}
		if delta := suffixAfterCommonPrefix(lastText, textSnapshot.String()); delta != "" {
			lastText = textSnapshot.String()
			markFirst()
			streamIdle.MarkEffectiveContent()
			if err := sink(ModelEvent{Kind: ModelEventKindTextDelta, OccurredAt: time.Now().UTC(), Provider: "gemini", Model: modelID, Text: delta}); err != nil {
				return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if idleErr := streamIdle.Err(); idleErr != nil {
			return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, idleErr
		}
		return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, err
	}
	if lastThinking != "" {
		if err := sink(ModelEvent{Kind: ModelEventKindThinkingCompleted, OccurredAt: time.Now().UTC(), Provider: "gemini", Model: modelID}); err != nil {
			return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, err
		}
	}
	if finishReason == "" {
		finishReason = "stop"
	}
	return inputTokens, outputTokens, cacheReadTokens, finishReason, firstEventAt, sink(ModelEvent{Kind: ModelEventKindTurnFinished, OccurredAt: time.Now().UTC(), Provider: "gemini", Model: modelID, FinishReason: finishReason, InputTokens: inputTokens, OutputTokens: outputTokens, CacheReadTokens: cacheReadTokens, UsagePresent: inputTokens > 0 || outputTokens > 0 || cacheReadTokens > 0, CacheReadPresent: cacheReadTokens > 0})
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

func stableGeminiToolCallID(req StreamRequest, name string, args string) string {
	base := strings.TrimSpace(req.ModelCallID) + ":" + strings.TrimSpace(name) + ":" + strings.TrimSpace(args)
	if base == "::" {
		base = time.Now().UTC().Format(time.RFC3339Nano)
	}
	sum := sha256.Sum256([]byte(base))
	return "gemini_synth_" + hex.EncodeToString(sum[:])[:16]
}
