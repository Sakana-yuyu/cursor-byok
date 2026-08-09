// openai_body_equiv_test.go 验证 buildOpenAIResponsesBodyMap / buildOpenAIChatBodyMap
// 与旧路径（构造强类型 struct → requestBodyToMap → 同一组 apply 变换 → marshal）
// 的最终请求体字节完全一致。锁死 json 等价形态，防止未来遗漏 omitempty / nil /
// 嵌套类型（[]any vs []map[string]any）语义导致的上游请求差异。
package modeladapter

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// legacyOpenAIResponsesBodyPipeline 复刻优化前 openai_stream_responses.go 的构造路径：
// 强类型 struct → requestBodyToMap（marshal+unmarshal）→ 同一组 apply 变换。
func legacyOpenAIResponsesBodyPipeline(req StreamRequest, modelID string, baseURL string, manualPromptCacheKey bool) ([]byte, error) {
	overrideBody := cloneRequestBodyOverride(req.RequestBodyOverride)
	var body any = overrideBody
	if len(overrideBody) == 0 {
		instructions, input, err := normalizeOpenAIResponsesInput(req.Messages)
		if err != nil {
			return nil, err
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
		if key := openAIPromptCacheKey(req, modelID, 0); key != "" {
			requestBody.PromptCacheKey = key
		}
		if len(req.Tools) > 0 {
			tools, err := normalizeOpenAIResponsesTools(req.Tools)
			if err != nil {
				return nil, err
			}
			if shouldExposeOpenAIResponsesImageGeneration(req, tools) {
				tools = ensureOpenAIResponsesImageGenerationTool(tools)
			}
			requestBody.Tools = tools
		}
		if effort := strings.TrimSpace(req.ReasoningEffort); effort != "" {
			requestBody.Reasoning = &openAIResponsesReasoning{Effort: effort, Summary: "auto"}
			requestBody.Include = []string{"reasoning.encrypted_content"}
		}
		body = requestBody
	} else {
		applyOpenAIPromptCacheKeyOverride(overrideBody, req, modelID, 0)
	}
	bodyMap, err := requestBodyToMap(body)
	if err != nil {
		return nil, err
	}
	applyOpenAIThinkingDisable(bodyMap, req, baseURL, modelID, req.OpenAIEndpoint)
	applyOpenAIParallelToolCalls(bodyMap, modelID)
	if err := ApplyOpenAIExtraParams(bodyMap, req.OpenAIExtraParamsEnabled, req.OpenAIExtraParamsJSON); err != nil {
		return nil, err
	}
	normalizeOpenAIResponsesRequestToolSchemas(bodyMap)
	applyOpenAIResponsesCompatibility(bodyMap, baseURL, modelID, manualPromptCacheKey)
	return json.Marshal(bodyMap)
}

// legacyOpenAIChatBodyPipeline 复刻优化前 openai_stream_chat.go 的构造路径。
func legacyOpenAIChatBodyPipeline(req StreamRequest, modelID string, baseURL string, manualPromptCacheKey bool) ([]byte, error) {
	overrideBody := cloneRequestBodyOverride(req.RequestBodyOverride)
	var body any = overrideBody
	if len(overrideBody) == 0 {
		normalizedMessages, err := normalizeOpenAIProviderMessages(req.Messages, strings.TrimSpace(req.ReasoningEffort) != "", isKimiOpenAIRequest(baseURL, modelID))
		if err != nil {
			return nil, err
		}
		requestBody := openAIChatRequestBody(req, modelID, 0)
		requestBody.Messages = normalizedMessages
		if len(req.Tools) > 0 {
			tools, err := normalizeOpenAIChatTools(req.Tools)
			if err != nil {
				return nil, err
			}
			requestBody.Tools = tools
		}
		body = requestBody
	} else {
		if !openAIChatRequestGroupUsesCompatShape(req.OpenAIRequestGroup) {
			applyOpenAIPromptCacheKeyOverride(overrideBody, req, modelID, 0)
		}
	}
	bodyMap, err := requestBodyToMap(body)
	if err != nil {
		return nil, err
	}
	applyOpenAIThinkingDisable(bodyMap, req, baseURL, modelID, req.OpenAIEndpoint)
	if err := ApplyOpenAIExtraParams(bodyMap, req.OpenAIExtraParamsEnabled, req.OpenAIExtraParamsJSON); err != nil {
		return nil, err
	}
	normalizeOpenAIRequestToolSchemas(bodyMap)
	applyOpenAIChatCompletionsCompatibility(bodyMap, baseURL, modelID, manualPromptCacheKey)
	return json.Marshal(bodyMap)
}

func TestBuildOpenAIResponsesBodyMapUsesSupportedReasoningIncludes(t *testing.T) {
	body, err := buildOpenAIResponsesBodyMap(StreamRequest{ReasoningEffort: "medium"}, "gpt-5.6-sol", 0)
	if err != nil {
		t.Fatalf("build responses body: %v", err)
	}

	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %#v, want object", body["reasoning"])
	}
	if reasoning["effort"] != "medium" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v, want effort=medium and summary=auto", reasoning)
	}

	include, ok := body["include"].([]any)
	if !ok {
		t.Fatalf("include = %#v, want array", body["include"])
	}
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v, want only reasoning.encrypted_content", include)
	}
}

func TestOpenAIResponsesBodyPipelineEquivalence(t *testing.T) {
	req := benchOpenAIResponsesRequest()
	for _, modelID := range []string{"gpt-5.6", "gpt-4o", "unknown-model"} {
		newPayload, err := benchOpenAIResponsesBodyPipeline(req, modelID, "https://api.openai.com/v1", false)
		if err != nil {
			t.Fatalf("new pipeline error: %v", err)
		}
		legacyPayload, err := legacyOpenAIResponsesBodyPipeline(req, modelID, "https://api.openai.com/v1", false)
		if err != nil {
			t.Fatalf("legacy pipeline error: %v", err)
		}
		if !bytes.Equal(newPayload, legacyPayload) {
			t.Fatalf("model=%s: responses body mismatch\nnew:    %s\nlegacy: %s", modelID, newPayload, legacyPayload)
		}
	}
}

func TestOpenAIChatBodyPipelineEquivalence(t *testing.T) {
	req := benchOpenAIChatRequest()
	for _, modelID := range []string{"gpt-5.6", "gpt-4o", "unknown-model"} {
		newPayload, err := benchOpenAIChatBodyPipeline(req, modelID, "https://api.openai.com/v1", false)
		if err != nil {
			t.Fatalf("new pipeline error: %v", err)
		}
		legacyPayload, err := legacyOpenAIChatBodyPipeline(req, modelID, "https://api.openai.com/v1", false)
		if err != nil {
			t.Fatalf("legacy pipeline error: %v", err)
		}
		if !bytes.Equal(newPayload, legacyPayload) {
			t.Fatalf("model=%s: chat body mismatch\nnew:    %s\nlegacy: %s", modelID, newPayload, legacyPayload)
		}
	}
}

// TestOpenAIBodyPipelineEquivalenceOverridePath 验证 RequestBodyOverride 分支（map 原样路径）
// 新老管线输出一致，覆盖 compat shape 分支的 stream_options nil 语义。
func TestOpenAIBodyPipelineEquivalenceOverridePath(t *testing.T) {
	req := benchOpenAIChatRequest()
	req.RequestBodyOverride = map[string]any{
		"model":    "claude-sonnet-4-5",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"stream":   true,
	}
	req.OpenAIRequestGroup = "chat-completions-compat"
	newPayload, err := benchOpenAIChatBodyPipeline(req, "gpt-5.6", "https://api.openai.com/v1", false)
	if err != nil {
		t.Fatalf("new pipeline error: %v", err)
	}
	legacyPayload, err := legacyOpenAIChatBodyPipeline(req, "gpt-5.6", "https://api.openai.com/v1", false)
	if err != nil {
		t.Fatalf("legacy pipeline error: %v", err)
	}
	if !bytes.Equal(newPayload, legacyPayload) {
		t.Fatalf("override path body mismatch\nnew:    %s\nlegacy: %s", newPayload, legacyPayload)
	}
}
