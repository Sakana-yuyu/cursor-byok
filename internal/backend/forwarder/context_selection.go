package forwarder

import (
	"fmt"
	"strconv"
	"strings"

	"cursor/gen/agentv1"
)

// validateDuplicateContextSelection is pure so admission can check both queued
// and active request identities under the queue's lock, without a TOCTOU gap.
func validateDuplicateContextSelection(existing, incoming InboundIntent) error {
	if incoming.RequestedContextWindowTokens > 0 &&
		existing.RequestedContextWindowTokens != incoming.RequestedContextWindowTokens {
		return fmt.Errorf("该请求已运行或排队，不能静默切换 Context；请先取消旧请求，或在结束后使用新请求应用上下文档位")
	}
	return nil
}

// parseContextWindowTokens accepts only positive decimal values representable by protocol int32 fields.
func parseContextWindowTokens(raw string) (int, error) {
	if raw == "" {
		return 0, fmt.Errorf("context must be a positive decimal token count")
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid context token count %q", raw)
		}
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("context token count %q must be between 1 and 2147483647", raw)
	}
	return int(value), nil
}

// parseRequestedContextWindowTokens merges parameter and exploded-menu selections without silent overrides.
func parseRequestedContextWindowTokens(model *agentv1.RequestedModel) (int, error) {
	selected := 0
	merge := func(value int) error {
		if selected != 0 && selected != value {
			return fmt.Errorf("conflicting context token counts: %d and %d", selected, value)
		}
		selected = value
		return nil
	}
	for _, parameter := range model.GetParameters() {
		if parameter == nil || parameter.GetId() != "context" {
			continue
		}
		value, err := parseContextWindowTokens(parameter.GetValue())
		if err != nil {
			return 0, err
		}
		if err := merge(value); err != nil {
			return 0, err
		}
	}
	if model.GetIsVariantStringRepresentation() {
		_, _, value, err := parseRuntimeModelVariantString(model.GetModelId())
		if err != nil {
			return 0, err
		}
		if value != 0 {
			if err := merge(value); err != nil {
				return 0, err
			}
		}
	}
	return selected, nil
}

// parseRuntimeModelVariantString removes menu attributes from the right, preserving colons inside model IDs.
func parseRuntimeModelVariantString(raw string) (modelID, effort string, contextTokens int, err error) {
	text := strings.TrimSpace(raw)
	for {
		index := strings.LastIndex(text, ":")
		if index <= 0 {
			break
		}
		suffix := text[index+1:]
		key, value, ok := strings.Cut(suffix, "=")
		if !ok || (key != "context" && key != "fast") {
			break
		}
		if key == "context" {
			parsed, parseErr := parseContextWindowTokens(value)
			if parseErr != nil {
				return "", "", 0, parseErr
			}
			if contextTokens != 0 && contextTokens != parsed {
				return "", "", 0, fmt.Errorf("conflicting variant context token counts: %d and %d", contextTokens, parsed)
			}
			contextTokens = parsed
		}
		text = strings.TrimSpace(text[:index])
	}
	modelID, effort = splitLegacyRuntimeThinkingEffortVariantString(text)
	if modelID == "" {
		modelID = text
	}
	return modelID, effort, contextTokens, nil
}

func splitRuntimeThinkingEffortVariantString(raw string) (string, string) {
	modelID, effort, _, err := parseRuntimeModelVariantString(raw)
	if err != nil {
		return "", ""
	}
	// Preserve the legacy effort-only representation.
	if normalizeRuntimeThinkingEffort(strings.TrimSpace(raw)) != "" {
		return "", effort
	}
	if effort == "" && modelID == strings.TrimSpace(raw) {
		return "", ""
	}
	return modelID, effort
}
