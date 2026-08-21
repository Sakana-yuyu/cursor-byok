package modeladapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func cloneRequestBodyOverride(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil
	}
	var cloned map[string]any
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil
	}
	return cloned
}

func requestBodyToMap(input any) (map[string]any, error) {
	if body, ok := input.(map[string]any); ok {
		return body, nil
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, err
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, nil
}

var openAIProtectedExtraParamKeys = map[string]struct{}{
	"model": {}, "stream": {}, "stream_options": {}, "messages": {}, "input": {}, "instructions": {}, "tools": {},
	"max_tokens": {}, "max_completion_tokens": {}, "max_output_tokens": {}, "reasoning_effort": {}, "reasoning": {},
	"thinking": {}, "enable_thinking": {},
}

var anthropicProtectedExtraParamKeys = map[string]struct{}{
	"model": {}, "stream": {}, "system": {}, "messages": {}, "tools": {}, "max_tokens": {}, "thinking": {}, "output_config": {},
}

func ApplyOpenAIExtraParams(body map[string]any, enabled bool, paramsJSON string) error {
	return applyExtraParams(body, enabled, paramsJSON, "openai extra params json", openAIProtectedExtraParamKeys)
}

func ApplyAnthropicExtraParams(body map[string]any, enabled bool, paramsJSON string) error {
	return applyExtraParams(body, enabled, paramsJSON, "anthropic extra params json", anthropicProtectedExtraParamKeys)
}

func applyExtraParams(body map[string]any, enabled bool, paramsJSON string, label string, protected map[string]struct{}) error {
	if !enabled {
		return nil
	}
	if body == nil {
		return fmt.Errorf("%s target body is nil", label)
	}
	extraParams, err := parseJSONMap(paramsJSON, label)
	if err != nil {
		return err
	}
	for key, value := range extraParams {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		if _, blocked := protected[strings.ToLower(name)]; blocked {
			continue
		}
		body[name] = value
	}
	return nil
}

func ParseCustomHeaders(enabled bool, headersJSON string) (http.Header, error) {
	headers := make(http.Header)
	if !enabled {
		return headers, nil
	}
	values, err := parseStringJSONMap(headersJSON, "custom headers json")
	if err != nil {
		return nil, err
	}
	for key, value := range values {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		headers.Set(name, value)
	}
	return headers, nil
}

func ApplyHeaderSet(httpReq *http.Request, headers http.Header) error {
	if httpReq == nil {
		return fmt.Errorf("custom headers target request is nil")
	}
	for key, values := range headers {
		httpReq.Header.Del(key)
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
	return nil
}

func ApplyCustomHeaders(httpReq *http.Request, enabled bool, headersJSON string) error {
	headers, err := ParseCustomHeaders(enabled, headersJSON)
	if err != nil {
		return err
	}
	return ApplyHeaderSet(httpReq, headers)
}

func parseJSONMap(value string, label string) (map[string]any, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return nil, fmt.Errorf("%s is empty", label)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, fmt.Errorf("%s must be an object: %w", label, err)
	}
	if parsed == nil {
		return nil, fmt.Errorf("%s must be an object", label)
	}
	return parsed, nil
}

func parseStringJSONMap(value string, label string) (map[string]string, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return nil, fmt.Errorf("%s is empty", label)
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, fmt.Errorf("%s must be an object with string values: %w", label, err)
	}
	if parsed == nil {
		return nil, fmt.Errorf("%s must be an object", label)
	}
	return parsed, nil
}
