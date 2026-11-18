package modeladapter

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"cursor/internal/modelcontext"
)

// ContextBudgetExceededError is a local, pre-send rejection. Callers may lower
// their compile ceiling and recompile with a bounded retry, but only before output.
// Estimates are deliberately heuristic, not provider-confirmed tokenizer counts.
type ContextBudgetExceededError struct {
	ContextWindowTokens  int
	EstimatedInputTokens int64
	MaxOutputTokens      int
	// OutputStarted forbids caller-side compression replay after any visible event.
	OutputStarted bool
}

func (err *ContextBudgetExceededError) Error() string {
	return fmt.Sprintf("context budget exceeded: estimated input %d + reserved output %d exceeds window %d", err.EstimatedInputTokens, err.MaxOutputTokens, err.ContextWindowTokens)
}

// EstimateContextTextTokens follows the existing prompt estimator's /3 plus
// newline heuristic, with a UTF-8 byte floor for conservative multilingual input.
// This is not an exact tokenizer and intentionally leaves additional send margin.
func EstimateContextTextTokens(text string) int64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	// Convert before adding: len(text)+2 can overflow int on windows/386.
	return (int64(len(text))+2)/3 + int64(strings.Count(text, "\n"))
}

func requestContextWindow(req StreamRequest, modelID string) int {
	return modelcontext.ResolveContextBudget(modelcontext.ContextBudgetInput{
		ModelID:          modelID,
		ConfiguredTokens: req.ResolvedContextWindowTokens,
		RequestedTokens:  req.RequestedContextWindowTokens,
		CeilingTokens:    req.ContextWindowTokens,
	}).EffectiveTokens
}

// validateContextInput provides a cheap channel-local guard even for injected
// adapters/account gateways. Real HTTP adapters also check their final wire body.
func validateContextInput(req StreamRequest) error {
	input := int64(256)
	for _, message := range req.Messages {
		input += 8 + EstimateContextTextTokens(message.Role) + EstimateContextTextTokens(message.Content)
		input += EstimateContextTextTokens(message.ReasoningContent) + EstimateContextTextTokens(message.ReasoningSignature)
		input += EstimateContextTextTokens(message.ToolCallID) + EstimateContextTextTokens(message.Name)
		for _, part := range message.ContentParts {
			if part.Type == "image" {
				input += 1024
			} else if strings.TrimSpace(message.Content) == "" {
				input += EstimateContextTextTokens(part.Text)
			}
		}
		for _, call := range message.ToolCalls {
			input += 6 + EstimateContextTextTokens(call.ID) + EstimateContextTextTokens(call.Type) + EstimateContextTextTokens(call.Function.Name) + EstimateContextTextTokens(call.Function.Arguments)
		}
	}
	for _, tool := range req.Tools {
		input += EstimateContextTextTokens(string(tool))
	}
	output := req.MaxTokens
	if req.Provider == "anthropic" {
		output = maxAnthropicTokens(req)
	}
	if output <= 0 {
		output = 4096
	}
	return checkContextBudget(req, req.ProviderModelID, input, output)
}

func checkContextBudget(req StreamRequest, modelID string, input int64, output int) error {
	window := requestContextWindow(req, modelID)
	if req.RequestKnobs != nil {
		req.RequestKnobs["context_window_tokens"] = window
		req.RequestKnobs["estimated_input_tokens"] = input
		req.RequestKnobs["reserved_output_tokens"] = output
		req.RequestKnobs["context_estimation"] = "conservative_heuristic"
	}
	if input > int64(window) || int64(output) > int64(window)-input {
		return &ContextBudgetExceededError{ContextWindowTokens: window, EstimatedInputTokens: input, MaxOutputTokens: output}
	}
	return nil
}

// ContextBudgetValidationError rejects malformed local budget values before HTTP.
// It is not a provider fault and must not trigger failover, cooldown or compression.
type ContextBudgetValidationError struct{ Detail string }

func (err *ContextBudgetValidationError) Error() string {
	return "invalid context budget: " + err.Detail
}

func validateContextOverride(body map[string]any) error {
	if _, err := json.Marshal(body); err != nil {
		return &ContextBudgetValidationError{Detail: "request body override is not valid JSON: " + err.Error()}
	}
	return nil
}

// validateProviderContextBudget runs after all protocol transformations. Only the
// active protocol's output fields can satisfy its wire-cap requirement.
func validateProviderContextBudget(req StreamRequest, modelID string, body map[string]any, outputField string, outputAliases ...string) error {
	output := 0
	fields := append([]string{outputField}, outputAliases...)
	container := body
	if outputField == "maxOutputTokens" {
		if snake, exists := body["generation_config"]; exists {
			if _, duplicate := body["generationConfig"]; duplicate {
				return &ContextBudgetValidationError{Detail: "duplicate generation config aliases"}
			}
			body["generationConfig"] = snake
			delete(body, "generation_config")
		}
		var ok bool
		if value, exists := body["generationConfig"]; exists && value != nil {
			container, ok = value.(map[string]any)
			if !ok {
				return &ContextBudgetValidationError{Detail: "generationConfig must be an object"}
			}
		} else {
			container = map[string]any{}
			body["generationConfig"] = container
		}
		if snake, exists := container["max_output_tokens"]; exists {
			if _, duplicate := container["maxOutputTokens"]; duplicate {
				return &ContextBudgetValidationError{Detail: "duplicate max output token aliases"}
			}
			container["maxOutputTokens"] = snake
			delete(container, "max_output_tokens")
		}
	}
	for _, field := range fields {
		if value, exists := container[field]; exists && value != nil {
			n, err := positiveBudgetInt(value)
			if err != nil {
				return &ContextBudgetValidationError{Detail: field + " " + err.Error()}
			}
			if n > output {
				output = n
			}
		}
	}
	// A body override can omit an existing positive channel/request output limit.
	// Preserve that limit; use 4096 only if neither final body nor request has one.
	if output == 0 {
		output = req.MaxTokens
		if outputField == "max_tokens" && len(outputAliases) == 0 && req.AnthropicMaxTokens > 0 {
			output = req.AnthropicMaxTokens
		}
		if output <= 0 {
			output = 4096
		}
		container[outputField] = output
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return &ContextBudgetValidationError{Detail: err.Error()}
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return &ContextBudgetValidationError{Detail: err.Error()}
	}
	value, images := contextBudgetValue(value)
	encoded, err = json.Marshal(value)
	if err != nil {
		return &ContextBudgetValidationError{Detail: err.Error()}
	}
	input := EstimateContextTextTokens(string(encoded)) + images*1024 + 256
	// Overrides must not evade a smaller model's capability on the actual wire.
	req.ContextWindowTokens = requestContextWindow(req, modelID)
	if outputField != "maxOutputTokens" {
		if wireModel, ok := body["model"].(string); ok && strings.TrimSpace(wireModel) != "" {
			modelID = wireModel
		}
	}
	return checkContextBudget(req, modelID, input, output)
}

func positiveBudgetInt(value any) (int, error) {
	maxInt := uint64(^uint(0) >> 1)
	var n int64
	switch number := value.(type) {
	case int:
		n = int64(number)
	case int64:
		n = number
	case float64:
		// 2^63 rounds to the float64 representation of MaxInt64; reject it
		// before conversion. MaxInt32 itself is exactly representable and valid.
		if math.IsNaN(number) || math.IsInf(number, 0) || number <= 0 || math.Trunc(number) != number || number >= math.Ldexp(1, 63) || number > float64(maxInt) {
			return 0, fmt.Errorf("must be a positive platform-sized integer")
		}
		n = int64(number)
	case json.Number:
		var err error
		n, err = number.Int64()
		if err != nil {
			return 0, fmt.Errorf("must be a positive platform-sized integer")
		}
	default:
		return 0, fmt.Errorf("must be a positive platform-sized integer")
	}
	if n <= 0 || uint64(n) > maxInt {
		return 0, fmt.Errorf("must be a positive platform-sized integer")
	}
	return int(n), nil
}

func budgetInt(value any) int { n, _ := positiveBudgetInt(value); return n }

func contextBudgetValue(value any) (any, int64) {
	switch item := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(item))
		var images int64
		kind, _ := item["type"].(string)
		isImage := kind == "image" || kind == "image_url" || kind == "input_image"
		if isImage {
			images++
		}
		if item["fileData"] != nil || item["file_data"] != nil {
			images++
		}
		for key, v := range item {
			// Redact binary image transport only, never the whole object. Text,
			// tool schemas and extension siblings must remain in the estimate.
			if isImage && key == "source" {
				if source, ok := v.(map[string]any); ok {
					copy := make(map[string]any, len(source))
					for k, val := range source {
						copy[k] = val
					}
					if source["type"] == "base64" {
						if _, exists := source["data"]; exists {
							copy["data"] = "[image data]"
						}
					}
					v = copy
				}
			}
			if key == "inlineData" || key == "inline_data" {
				if data, ok := v.(map[string]any); ok {
					copy := make(map[string]any, len(data))
					for k, val := range data {
						copy[k] = val
					}
					mime, _ := data["mimeType"].(string)
					if mime == "" {
						mime, _ = data["mime_type"].(string)
					}
					if _, exists := data["data"]; exists && strings.HasPrefix(strings.ToLower(mime), "image/") {
						copy["data"] = "[image data]"
						images++
					}
					v = copy
				}
			}
			if isImage && (key == "image_url" || key == "url") {
				switch image := v.(type) {
				case string:
					if strings.HasPrefix(image, "data:image/") {
						v = "[image data URL]"
					}
				case map[string]any:
					if url, ok := image["url"].(string); ok && strings.HasPrefix(url, "data:image/") {
						copy := make(map[string]any, len(image))
						for k, val := range image {
							copy[k] = val
						}
						copy["url"] = "[image data URL]"
						v = copy
					}
				}
			}
			clean, count := contextBudgetValue(v)
			out[key] = clean
			images += count
		}
		return out, images
	case []any:
		out := make([]any, len(item))
		var images int64
		for i, v := range item {
			clean, count := contextBudgetValue(v)
			out[i] = clean
			images += count
		}
		return out, images
	default:
		return value, 0
	}
}
